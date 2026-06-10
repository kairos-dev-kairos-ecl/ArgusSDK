package secrets

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/gob"
	"fmt"
	"io"
	"os"
	"sync"
)

// Canonical secret keys for backward compatibility with env-var fallback
const (
	KeyJWTPrivateKey = "JWT_PRIVATE_KEY_PEM"
	KeyDBPassword    = "DB_PASSWORD"
	KeyRedisPassword = "REDIS_PASSWORD"
	KeyMFAEncryption = "mfa_encryption_key"
)

// Store manages encrypted secrets stored in a file using AES-256-GCM.
// The file format is:
//   - Magic (4 bytes): "ARGS"
//   - Version (1 byte): 0x01
//   - Nonce (12 bytes): random per write
//   - Ciphertext+Tag (remaining bytes): encrypted gob-encoded map
//
// F11 / T-03-16 mitigated: GetSecret serves from a cached decrypted map behind
// sync.RWMutex; SaveSecrets invalidates and repopulates the cache. The store
// file is NOT re-read and re-decrypted on every GetSecret call.
type Store struct {
	path      string
	masterKey []byte

	// F11: in-memory cache of the decrypted secrets map.
	cacheMu    sync.RWMutex
	cache      map[string]string
	cacheValid bool
}

// NewStore creates a Store backed by the given file path.
// If masterKey is nil, it attempts to read ARGUS_MASTER_KEY env var
// (must be base64-encoded 32 bytes). If env var is also empty, returns error.
func NewStore(path string, masterKey []byte) (*Store, error) {
	key := masterKey
	if key == nil {
		masterKeyEnv := os.Getenv("ARGUS_MASTER_KEY")
		if masterKeyEnv == "" {
			return nil, fmt.Errorf("ARGUS_MASTER_KEY env var not set and no master key provided")
		}
		decoded, err := base64.StdEncoding.DecodeString(masterKeyEnv)
		if err != nil {
			return nil, fmt.Errorf("failed to decode ARGUS_MASTER_KEY from base64: %w", err)
		}
		key = decoded
	}

	if len(key) != 32 {
		return nil, fmt.Errorf("master key must be exactly 32 bytes (was %d)", len(key))
	}

	return &Store{
		path:      path,
		masterKey: key,
	}, nil
}

// LoadSecrets reads and decrypts the secrets file.
// Returns an empty map (no error) if the file does not exist.
// Returns error only for decryption/format failures.
func (s *Store) LoadSecrets() (map[string]string, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]string), nil
		}
		return nil, fmt.Errorf("failed to read secrets file: %w", err)
	}

	// Parse header
	if len(data) < 17 { // 4 (magic) + 1 (version) + 12 (nonce) minimum
		return nil, fmt.Errorf("secrets file too short")
	}

	magic := string(data[0:4])
	if magic != "ARGS" {
		return nil, fmt.Errorf("invalid secrets file magic: got %q, expected 'ARGS'", magic)
	}

	version := data[4]
	if version != 0x01 {
		return nil, fmt.Errorf("unsupported secrets file version: %d", version)
	}

	nonce := data[5:17]
	ciphertext := data[17:]

	// Decrypt
	block, err := aes.NewCipher(s.masterKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decryption failed (authentication tag mismatch or corrupted data): %w", err)
	}

	// Decode gob map
	secrets := make(map[string]string)
	dec := gob.NewDecoder(bytes.NewReader(plaintext))
	if err := dec.Decode(&secrets); err != nil {
		return nil, fmt.Errorf("failed to decode secrets: %w", err)
	}

	return secrets, nil
}

// lookup returns the value for key from the in-memory cache, loading from disk
// if the cache is not yet populated.
//
// F11: RLock fast path when cacheValid; full Lock + double-check + load on
// cache miss. Never returns the internal map slice to callers.
func (s *Store) lookup(key string) (string, bool, error) {
	// Fast path: cache already valid.
	s.cacheMu.RLock()
	if s.cacheValid {
		v, ok := s.cache[key]
		s.cacheMu.RUnlock()
		return v, ok, nil
	}
	s.cacheMu.RUnlock()

	// Slow path: acquire write lock, double-check, then load from disk.
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()

	// Double-check — another goroutine may have loaded while we waited.
	if s.cacheValid {
		v, ok := s.cache[key]
		return v, ok, nil
	}

	m, err := s.LoadSecrets()
	if err != nil {
		return "", false, err
	}

	// Populate cache with a copy — never expose the internal map.
	cp := make(map[string]string, len(m))
	for k, v := range m {
		cp[k] = v
	}
	s.cache = cp
	s.cacheValid = true

	v, ok := s.cache[key]
	return v, ok, nil
}

// SaveSecrets encrypts and atomically writes the secrets map to the file with 0600 permissions.
// Uses a random nonce per write, ensuring identical plaintext produces different ciphertexts.
// F11: on successful write, updates the in-memory cache so subsequent GetSecret calls
// are served without re-reading the file.
func (s *Store) SaveSecrets(secrets map[string]string) error {
	// Encode map to gob
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(secrets); err != nil {
		return fmt.Errorf("failed to encode secrets: %w", err)
	}
	plaintext := buf.Bytes()

	// Generate random nonce
	nonce := make([]byte, 12)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Encrypt
	block, err := aes.NewCipher(s.masterKey)
	if err != nil {
		return fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("failed to create GCM: %w", err)
	}

	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)

	// Build file content: magic || version || nonce || ciphertext
	var fileData bytes.Buffer
	fileData.WriteString("ARGS")
	fileData.WriteByte(0x01)
	fileData.Write(nonce)
	fileData.Write(ciphertext)

	// F15 / T-03-15 mitigated: use O_CREATE|O_WRONLY|O_TRUNC with mode 0600 so
	// the temp file is never momentarily world-readable (os.Create uses 0666&umask).
	tmpPath := s.path + ".tmp"
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}

	if _, err := f.Write(fileData.Bytes()); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	// Fsync to ensure durability
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("failed to sync temp file: %w", err)
	}

	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	// Rename to final location (atomic on POSIX, best effort on Windows)
	if err := os.Rename(tmpPath, s.path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	// Set file permissions to 0600 (owner read/write only) — belt-and-braces.
	if err := os.Chmod(s.path, 0600); err != nil {
		return fmt.Errorf("failed to set file permissions: %w", err)
	}

	// F11: update cache with a copy of the newly-saved map.
	s.cacheMu.Lock()
	cp := make(map[string]string, len(secrets))
	for k, v := range secrets {
		cp[k] = v
	}
	s.cache = cp
	s.cacheValid = true
	s.cacheMu.Unlock()

	return nil
}

// GenerateMasterKey returns a base64-encoded 32-byte key suitable for ARGUS_MASTER_KEY.
func GenerateMasterKey() (string, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return "", fmt.Errorf("failed to generate master key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(key), nil
}
