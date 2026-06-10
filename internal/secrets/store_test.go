package secrets

import (
	"crypto/rand"
	"os"
	"runtime"
	"testing"
)

// randomKey generates a random 32-byte master key for tests.
func randomKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return key
}

// newTestStore creates a Store backed by a temporary file. The file is
// removed when the test completes.
func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	f, err := os.CreateTemp("", "secrets-test-*.key")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	path := f.Name()
	f.Close()
	os.Remove(path) // start without the file

	s, err := NewStore(path, randomKey(t))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { os.Remove(path) })
	return s, path
}

// ---------------------------------------------------------------------------
// Basic round-trip coverage
// ---------------------------------------------------------------------------

func TestSaveAndLoad(t *testing.T) {
	s, _ := newTestStore(t)
	secrets := map[string]string{"foo": "bar", "baz": "qux"}
	if err := s.SaveSecrets(secrets); err != nil {
		t.Fatalf("SaveSecrets: %v", err)
	}
	got, err := s.LoadSecrets()
	if err != nil {
		t.Fatalf("LoadSecrets: %v", err)
	}
	for k, v := range secrets {
		if got[k] != v {
			t.Errorf("key %q: got %q, want %q", k, got[k], v)
		}
	}
}

func TestLoadSecrets_MissingFile(t *testing.T) {
	s, _ := newTestStore(t)
	// File was removed by newTestStore — should return empty map, not error.
	got, err := s.LoadSecrets()
	if err != nil {
		t.Fatalf("LoadSecrets on missing file: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map for missing file, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// F11: GetSecret cache test (RED phase)
// ---------------------------------------------------------------------------

// TestGetSecret_UsesCache (F11): after saving a secret, GetSecret must serve
// subsequent calls from cache — even after the store FILE has been deleted
// between the first and second call. Proves no re-read occurs.
func TestGetSecret_UsesCache(t *testing.T) {
	s, path := newTestStore(t)
	SetStore(s)
	t.Cleanup(func() { SetStore(nil) })

	// Save initial value.
	if err := s.SaveSecrets(map[string]string{"mykey": "value1"}); err != nil {
		t.Fatalf("SaveSecrets: %v", err)
	}

	// First call — warms cache.
	v1, ok := GetSecret("mykey")
	if !ok || v1 != "value1" {
		t.Fatalf("GetSecret first call: got %q %v, want %q true", v1, ok, "value1")
	}

	// Delete the backing file — second call must still serve from cache.
	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	v2, ok2 := GetSecret("mykey")
	if !ok2 || v2 != "value1" {
		t.Errorf("GetSecret second call (file deleted): got %q %v, want %q true (cache must serve)", v2, ok2, "value1")
	}

	// Recreate store with new value — SaveSecrets must invalidate cache.
	s2, path2 := newTestStore(t)
	SetStore(s2)
	t.Cleanup(func() { os.Remove(path2) })

	if err := s2.SaveSecrets(map[string]string{"mykey": "value2"}); err != nil {
		t.Fatalf("SaveSecrets s2: %v", err)
	}
	v3, ok3 := GetSecret("mykey")
	if !ok3 || v3 != "value2" {
		t.Errorf("GetSecret after SaveSecrets invalidation: got %q %v, want %q true", v3, ok3, "value2")
	}
}

// ---------------------------------------------------------------------------
// F15: temp file permissions test (RED phase)
// ---------------------------------------------------------------------------

// TestSaveSecrets_TempFilePerms (F15): the final secrets file must have mode 0600.
// On Windows, Go file perms are advisory, so the key assertion is that the
// SaveSecrets call succeeds and round-trips correctly; the 0600 mode flag usage
// is enforced structurally by the must_haves contains-check on "O_CREATE".
func TestSaveSecrets_TempFilePerms(t *testing.T) {
	s, path := newTestStore(t)

	if err := s.SaveSecrets(map[string]string{"k": "v"}); err != nil {
		t.Fatalf("SaveSecrets: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}

	if info.Mode().IsDir() {
		t.Error("secrets path is a directory, expected a file")
	}

	// On POSIX: assert exactly 0600 mode.
	// On Windows: file permissions are advisory (Go's os.Chmod has no effect on
	// NTFS ACLs) and Mode().Perm() always returns 0666 for regular files.
	// The meaningful assertion on Windows is that SaveSecrets uses
	// os.OpenFile(..., 0600) at creation (enforced by the must_haves contains-check
	// on "O_CREATE" in the plan). We skip the mode assertion on Windows.
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("file mode = %04o, want 0600", perm)
		}
	}

	// Verify round-trip regardless of platform.
	got, err := s.LoadSecrets()
	if err != nil {
		t.Fatalf("LoadSecrets after SaveSecrets: %v", err)
	}
	if got["k"] != "v" {
		t.Errorf("round-trip: got %q, want %q", got["k"], "v")
	}
}
