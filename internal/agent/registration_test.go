package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/auth"
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

// ─── fakeAuthRegistrar — implements auth.Registrar for adapter tests ──────────

// fakeAuthRegistrar is a test double for auth.Registrar (the auth-package
// interface, not the agent-seam Registrar).
type fakeAuthRegistrar struct {
	resp  *auth.RegistrationResponse
	err   error
	calls int
}

func (f *fakeAuthRegistrar) Register(_ context.Context, req auth.RegistrationRequest) (*auth.RegistrationResponse, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

// ─── TestSelectRegistrar ──────────────────────────────────────────────────────

// TestSelectRegistrar_LocalMode asserts that selectRegistrar returns a localRegistrar
// (or equivalent) for mode "local".
func TestSelectRegistrar_LocalMode(t *testing.T) {
	cfg := &Config{Agent: AgentConfig{Mode: "local"}}
	fakeRemote := &fakeAuthRegistrar{}
	adapter := NewRemoteRegistrarAdapter(fakeRemote, "inst", "v0.0.0")

	r := selectRegistrar(cfg, adapter)
	// For local mode, selectRegistrar must NOT return the remote adapter.
	if r == adapter {
		t.Error("selectRegistrar: mode local must NOT return the remote adapter")
	}
	// Verify it IS a localRegistrar by checking its behaviour is deterministic SHA-256.
	id1, err := r.Register(context.Background(), "tok", "grp")
	if err != nil {
		t.Fatalf("Register on local path: %v", err)
	}
	id2, err := r.Register(context.Background(), "tok", "grp")
	if err != nil {
		t.Fatalf("Register on local path (2nd): %v", err)
	}
	if id1 != id2 {
		t.Error("local path must be deterministic")
	}
	// Auth registrar must never have been called.
	if fakeRemote.calls != 0 {
		t.Errorf("fakeAuthRegistrar called %d times; expected 0 for local mode", fakeRemote.calls)
	}
}

// TestSelectRegistrar_RemoteMode asserts that selectRegistrar returns the supplied
// remote adapter for mode "remote".
func TestSelectRegistrar_RemoteMode(t *testing.T) {
	cfg := &Config{Agent: AgentConfig{Mode: "remote"}}
	fakeRemote := &fakeAuthRegistrar{resp: &auth.RegistrationResponse{InstanceID: "srv-id", Credential: "cred"}}
	adapter := NewRemoteRegistrarAdapter(fakeRemote, "inst", "v0.0.0")

	r := selectRegistrar(cfg, adapter)
	if r != adapter {
		t.Error("selectRegistrar: mode remote must return the supplied remote adapter")
	}
}

// ─── TestRemoteAdapter ────────────────────────────────────────────────────────

// TestRemoteAdapter_HappyPath asserts that the adapter passes the call through
// to the auth.Registrar and returns the InstanceID; the Credential is accessible
// via LastCredential().
func TestRemoteAdapter_HappyPath(t *testing.T) {
	fakeRemote := &fakeAuthRegistrar{
		resp: &auth.RegistrationResponse{
			InstanceID: "srv-uuid-001",
			Credential: "cred-001",
		},
	}
	adapter := NewRemoteRegistrarAdapter(fakeRemote, "my-instance", "v1.0.0")

	cfg := &Config{
		Agent: AgentConfig{GroupID: "grp-adapter", Mode: "remote"},
		Auth:  AuthConfig{InstallToken: "tok-adapter"},
	}

	gotID, err := ensureInstance(context.Background(), cfg, adapter)
	if err != nil {
		t.Fatalf("ensureInstance: %v", err)
	}
	if gotID != "srv-uuid-001" {
		t.Errorf("InstanceID: got %q, want %q", gotID, "srv-uuid-001")
	}
	if adapter.LastCredential() != "cred-001" {
		t.Errorf("Credential: got %q, want %q", adapter.LastCredential(), "cred-001")
	}
	if fakeRemote.calls != 1 {
		t.Errorf("expected exactly 1 auth.Register call, got %d", fakeRemote.calls)
	}
}

// TestRemoteAdapter_TokenConsumed asserts that ErrInstallTokenConsumed propagates
// through ensureInstance and is matchable via errors.Is.
func TestRemoteAdapter_TokenConsumed(t *testing.T) {
	fakeRemote := &fakeAuthRegistrar{err: auth.ErrInstallTokenConsumed}
	adapter := NewRemoteRegistrarAdapter(fakeRemote, "inst", "v1.0.0")

	cfg := &Config{
		Agent: AgentConfig{GroupID: "grp-consumed", Mode: "remote"},
		Auth:  AuthConfig{InstallToken: "used-tok"},
	}

	_, err := ensureInstance(context.Background(), cfg, adapter)
	if err == nil {
		t.Fatal("expected error for consumed token, got nil")
	}
	if !errors.Is(err, auth.ErrInstallTokenConsumed) {
		t.Errorf("errors.Is(err, ErrInstallTokenConsumed) = false; err = %v", err)
	}
}

// TestRemoteAdapter_ShortCircuit_BothModes asserts that a pre-set
// cfg.Auth.InstanceID causes ensureInstance to return immediately in BOTH
// modes — the auth registrar is never called.
func TestRemoteAdapter_ShortCircuit_BothModes(t *testing.T) {
	for _, mode := range []string{"local", "remote"} {
		mode := mode
		t.Run(mode, func(t *testing.T) {
			fakeRemote := &fakeAuthRegistrar{resp: &auth.RegistrationResponse{InstanceID: "should-not-appear"}}
			adapter := NewRemoteRegistrarAdapter(fakeRemote, "inst", "v1.0.0")
			r := selectRegistrar(&Config{Agent: AgentConfig{Mode: mode}}, adapter)

			cfg := &Config{
				Agent: AgentConfig{GroupID: "grp-sc", Mode: mode},
				Auth:  AuthConfig{InstanceID: "pre-set-id"},
			}

			got, err := ensureInstance(context.Background(), cfg, r)
			if err != nil {
				t.Fatalf("ensureInstance: %v", err)
			}
			if got != "pre-set-id" {
				t.Errorf("expected pre-set id, got %q", got)
			}
			if fakeRemote.calls != 0 {
				t.Errorf("auth registrar must not be called when InstanceID pre-set; got %d calls", fakeRemote.calls)
			}
		})
	}
}
