package secrets

import (
	"os"
	"sync/atomic"
)

// storePtr holds the active Store (may be nil).
// Atomic pointer allows concurrent safe access from GetSecret.
var storePtr atomic.Pointer[Store]

// SetStore replaces the package-level Store reference.
func SetStore(s *Store) {
	storePtr.Store(s)
}

// envAlias maps canonical secret keys to OS environment variable names for backward compatibility.
var envAlias = map[string]string{
	KeyJWTPrivateKey: "ARGUS_JWT_PRIVATE_KEY_PEM",
	KeyDBPassword:    "DB_PASSWORD",
	KeyRedisPassword: "REDIS_PASSWORD",
	KeyMFAEncryption: "ARGUS_MFA_ENCRYPTION_KEY",
}

// GetSecret returns the value for the given key, preferring the Store over env vars.
// Fallback precedence:
//  1. Value from Store (if present) — served from cache (F11)
//  2. Value from env var (using envAlias mapping)
//  3. ("", false) if neither source has the key
//
// This allows encrypted argus.key to take precedence over env vars
// while maintaining backward compatibility with env-var-only deployments.
//
// F11 / T-03-16: uses Store.lookup() which serves from an in-memory cache
// populated on first access and invalidated on SaveSecrets. The store file is
// NOT re-read and re-decrypted on every call.
func GetSecret(key string) (string, bool) {
	// Try Store first — use cached lookup, not full LoadSecrets per call.
	if s := storePtr.Load(); s != nil {
		if v, ok, err := s.lookup(key); err == nil && ok && v != "" {
			return v, true
		}
	}

	// Fall back to environment variable
	envVar := envAlias[key]
	if envVar == "" {
		// If no alias, use the key itself as the env var name
		envVar = key
	}

	if v := os.Getenv(envVar); v != "" {
		return v, true
	}

	return "", false
}
