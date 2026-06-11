package agent

import (
	"context"
	"errors"
	"testing"
)

// fakeRegistrar is a test double that returns a canned InstanceID on Register.
type fakeRegistrar struct {
	instanceID string
	err        error
	called     bool
	lastToken  string
	lastGroup  string
}

func (f *fakeRegistrar) Register(_ context.Context, installToken, groupID string) (string, error) {
	f.called = true
	f.lastToken = installToken
	f.lastGroup = groupID
	return f.instanceID, f.err
}

// TestEnsureInstance_AlreadySet verifies that a non-empty cfg.Auth.InstanceID
// is returned unchanged without invoking the registrar.
func TestEnsureInstance_AlreadySet(t *testing.T) {
	cfg := &Config{Auth: AuthConfig{InstanceID: "existing-id"}}
	r := &fakeRegistrar{instanceID: "should-not-be-used"}

	got, err := ensureInstance(context.Background(), cfg, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "existing-id" {
		t.Errorf("expected %q, got %q", "existing-id", got)
	}
	if r.called {
		t.Error("registrar should not be called when InstanceID is already set")
	}
}

// TestEnsureInstance_TokenSet verifies that when InstanceID is empty and
// InstallToken is set, ensureInstance calls the registrar and returns its result.
func TestEnsureInstance_TokenSet(t *testing.T) {
	cfg := &Config{
		Agent: AgentConfig{GroupID: "grp-1"},
		Auth:  AuthConfig{InstallToken: "tok-abc"},
	}
	r := &fakeRegistrar{instanceID: "generated-id"}

	got, err := ensureInstance(context.Background(), cfg, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "generated-id" {
		t.Errorf("expected %q, got %q", "generated-id", got)
	}
	if !r.called {
		t.Error("registrar should be called when InstanceID is empty and InstallToken is set")
	}
	if r.lastToken != "tok-abc" {
		t.Errorf("expected token %q, got %q", "tok-abc", r.lastToken)
	}
	if r.lastGroup != "grp-1" {
		t.Errorf("expected group %q, got %q", "grp-1", r.lastGroup)
	}
}

// TestEnsureInstance_BothEmpty verifies that an error is returned when both
// InstanceID and InstallToken are empty.
func TestEnsureInstance_BothEmpty(t *testing.T) {
	cfg := &Config{}
	r := &fakeRegistrar{}

	_, err := ensureInstance(context.Background(), cfg, r)
	if err == nil {
		t.Fatal("expected error when both InstanceID and InstallToken are empty")
	}
	if r.called {
		t.Error("registrar should not be called when both InstanceID and InstallToken are empty")
	}
}

// TestEnsureInstance_RegistrarError verifies that a registrar error is propagated.
func TestEnsureInstance_RegistrarError(t *testing.T) {
	cfg := &Config{
		Agent: AgentConfig{GroupID: "grp-1"},
		Auth:  AuthConfig{InstallToken: "tok-abc"},
	}
	wantErr := errors.New("registration failed")
	r := &fakeRegistrar{err: wantErr}

	_, err := ensureInstance(context.Background(), cfg, r)
	if err == nil {
		t.Fatal("expected error from registrar to be propagated")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("expected wrapped %v, got %v", wantErr, err)
	}
}

// TestLocalRegistrar_Deterministic verifies that the local registrar returns the
// same InstanceID for the same groupID+token pair (deterministic hashing).
func TestLocalRegistrar_Deterministic(t *testing.T) {
	r := &localRegistrar{}
	id1, err := r.Register(context.Background(), "token-x", "group-y")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	id2, err := r.Register(context.Background(), "token-x", "group-y")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id1 != id2 {
		t.Errorf("local registrar must be deterministic: got %q then %q", id1, id2)
	}
}

// TestLocalRegistrar_DifferentInputs verifies that different inputs produce
// different InstanceIDs.
func TestLocalRegistrar_DifferentInputs(t *testing.T) {
	r := &localRegistrar{}
	id1, _ := r.Register(context.Background(), "token-x", "group-a")
	id2, _ := r.Register(context.Background(), "token-x", "group-b")
	if id1 == id2 {
		t.Error("different groupIDs should produce different InstanceIDs")
	}
}

// TestEnsureInstance_FakeRemoteRegistrar demonstrates that the Registrar seam
// works with a fake remote-style registrar (proving the interface is exercisable
// without a live ArgusXDR — locked decision 8).
func TestEnsureInstance_FakeRemoteRegistrar(t *testing.T) {
	cfg := &Config{
		Agent: AgentConfig{GroupID: "remote-grp"},
		Auth:  AuthConfig{InstallToken: "remote-tok"},
	}
	// Simulate a "remote" registrar that derives a different ID format.
	remote := &fakeRegistrar{instanceID: "srv-assigned-uuid-1234"}

	got, err := ensureInstance(context.Background(), cfg, remote)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "srv-assigned-uuid-1234" {
		t.Errorf("expected server-assigned ID, got %q", got)
	}
	if !remote.called {
		t.Error("remote registrar should have been called")
	}
}
