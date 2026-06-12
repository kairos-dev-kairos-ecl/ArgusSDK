package auth

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/secrets"
)

// makeTestStore creates a secrets.Store backed by a temp dir for testing.
// It generates a fresh 32-byte master key and decodes it so the store
// never touches ARGUS_MASTER_KEY.
func makeTestStore(t *testing.T) *secrets.Store {
	t.Helper()
	keyB64, err := secrets.GenerateMasterKey()
	if err != nil {
		t.Fatalf("GenerateMasterKey: %v", err)
	}
	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		t.Fatalf("decode key: %v", err)
	}
	path := filepath.Join(t.TempDir(), StateFile)
	store, err := secrets.NewStore(path, key)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
}

// TestIdentityLoad_AbsentFile asserts that LoadIdentity on a fresh store
// (no state file yet) returns ok=false and no error.
func TestIdentityLoad_AbsentFile(t *testing.T) {
	store := makeTestStore(t)

	id, ok, err := LoadIdentity(store)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Errorf("expected ok=false for absent file, got ok=true (id=%+v)", id)
	}
}

// TestIdentityRoundTrip asserts that SaveIdentity then LoadIdentity returns
// the same Identity (GroupID + InstanceID + Credential preserved).
func TestIdentityRoundTrip(t *testing.T) {
	store := makeTestStore(t)

	original := Identity{
		GroupID:    "grp-abc",
		InstanceID: "inst-xyz-0001",
		Credential: "super-secret-cred",
	}
	if err := SaveIdentity(store, original); err != nil {
		t.Fatalf("SaveIdentity: %v", err)
	}

	loaded, ok, err := LoadIdentity(store)
	if err != nil {
		t.Fatalf("LoadIdentity: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true after SaveIdentity")
	}
	if loaded.GroupID != original.GroupID {
		t.Errorf("GroupID: got %q, want %q", loaded.GroupID, original.GroupID)
	}
	if loaded.InstanceID != original.InstanceID {
		t.Errorf("InstanceID: got %q, want %q", loaded.InstanceID, original.InstanceID)
	}
	if loaded.Credential != original.Credential {
		t.Errorf("Credential: got %q, want %q", loaded.Credential, original.Credential)
	}
}

// TestReplaceCredential asserts that ReplaceCredential updates only the
// Credential field while leaving InstanceID and GroupID unchanged.
func TestReplaceCredential(t *testing.T) {
	store := makeTestStore(t)

	original := Identity{
		GroupID:    "grp-replace",
		InstanceID: "inst-replace-001",
		Credential: "old-credential",
	}
	if err := SaveIdentity(store, original); err != nil {
		t.Fatalf("SaveIdentity: %v", err)
	}

	newCred := "new-rotated-credential"
	if err := ReplaceCredential(store, newCred); err != nil {
		t.Fatalf("ReplaceCredential: %v", err)
	}

	loaded, ok, err := LoadIdentity(store)
	if err != nil {
		t.Fatalf("LoadIdentity after ReplaceCredential: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true after ReplaceCredential")
	}
	if loaded.Credential != newCred {
		t.Errorf("Credential after replace: got %q, want %q", loaded.Credential, newCred)
	}
	if loaded.InstanceID != original.InstanceID {
		t.Errorf("InstanceID must be unchanged: got %q, want %q", loaded.InstanceID, original.InstanceID)
	}
	if loaded.GroupID != original.GroupID {
		t.Errorf("GroupID must be unchanged: got %q, want %q", loaded.GroupID, original.GroupID)
	}
}

// TestIdentityNoPlaintext is a negative test that reads the raw agent-state.json
// bytes and asserts neither the InstanceID nor the Credential appear in plaintext.
// This satisfies T-05-05: credentials at rest must be AES-256-GCM encrypted.
func TestIdentityNoPlaintext(t *testing.T) {
	keyB64, err := secrets.GenerateMasterKey()
	if err != nil {
		t.Fatalf("GenerateMasterKey: %v", err)
	}
	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		t.Fatalf("decode key: %v", err)
	}

	dir := t.TempDir()
	statePath := filepath.Join(dir, StateFile)
	store, err := secrets.NewStore(statePath, key)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	id := Identity{
		GroupID:    "grp-plaintext-test",
		InstanceID: "PLAINTEXT_INSTANCE_CANARY",
		Credential: "PLAINTEXT_CREDENTIAL_CANARY",
	}
	if err := SaveIdentity(store, id); err != nil {
		t.Fatalf("SaveIdentity: %v", err)
	}

	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	rawStr := string(raw)
	if contains(rawStr, id.InstanceID) {
		t.Errorf("agent-state.json contains InstanceID %q in plaintext — encryption failed", id.InstanceID)
	}
	if contains(rawStr, id.Credential) {
		t.Errorf("agent-state.json contains Credential %q in plaintext — encryption failed", id.Credential)
	}
}

// contains checks if s contains substr using a simple byte scan.
// We avoid importing strings to stay in the auth package comfortably.
func contains(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
