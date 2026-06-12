// Package auth — unit tests for remoteRegistrar and remoteCredentialRefresher.
//
// All tests run against the in-process mock XDR server from mockxdr_test.go.
// No Docker, no live XDR endpoint.
package auth

import (
	"errors"
	"testing"
)

// ─── Compile-time interface assertions ────────────────────────────────────────
// These assignments verify that the constructors' declared return types satisfy
// the exported interfaces. They must compile; any type mismatch is a build failure.
var _ Registrar = NewRemoteRegistrar("", nil)
var _ CredentialRefresher = NewRemoteCredentialRefresher("", nil)

// ─── remoteRegistrar tests ────────────────────────────────────────────────────

func TestRemoteRegistrar_HappyPath(t *testing.T) {
	mock := newMockXDR(t)
	reg := NewRemoteRegistrar(mock.URL(), mock.Client())

	resp, err := reg.Register(t.Context(), RegistrationRequest{
		GroupID:      "grp-happy",
		InstallToken: "token-happy-1",
		InstanceName: "test-instance",
		AgentVersion: "1.0.0",
		Hostname:     "test-host",
		Platform:     "linux",
	})
	if err != nil {
		t.Fatalf("Register error: %v", err)
	}
	if resp == nil {
		t.Fatal("Register returned nil response")
	}
	// Mock returns instance-<GroupID> and "cred-initial" deterministically.
	if resp.InstanceID != "instance-grp-happy" {
		t.Errorf("InstanceID: want %q, got %q", "instance-grp-happy", resp.InstanceID)
	}
	if resp.Credential != "cred-initial" {
		t.Errorf("Credential: want %q, got %q", "cred-initial", resp.Credential)
	}
}

func TestRemoteRegistrar_TokenReuse_ErrInstallTokenConsumed(t *testing.T) {
	mock := newMockXDR(t)
	reg := NewRemoteRegistrar(mock.URL(), mock.Client())

	req := RegistrationRequest{
		GroupID:      "grp-reuse",
		InstallToken: "token-reuse-1",
	}

	// First call consumes the token.
	_, err := reg.Register(t.Context(), req)
	if err != nil {
		t.Fatalf("first Register error: %v", err)
	}

	// Second call with the same token must return ErrInstallTokenConsumed and nil response.
	resp, err := reg.Register(t.Context(), req)
	if resp != nil {
		t.Errorf("expected nil response on token reuse, got %+v", resp)
	}
	if !errors.Is(err, ErrInstallTokenConsumed) {
		t.Errorf("expected ErrInstallTokenConsumed, got %v", err)
	}
}

func TestRemoteRegistrar_Non2xx_HttpStatusError(t *testing.T) {
	mock := newMockXDR(t)
	mock.ForceStatus = 500
	reg := NewRemoteRegistrar(mock.URL(), mock.Client())

	resp, err := reg.Register(t.Context(), RegistrationRequest{
		GroupID:      "grp-500",
		InstallToken: "token-500",
	})
	if resp != nil {
		t.Errorf("expected nil response on non-2xx, got %+v", resp)
	}
	if err == nil {
		t.Fatal("expected error on non-2xx, got nil")
	}
	var statusErr *httpStatusError
	if !errors.As(err, &statusErr) {
		t.Errorf("expected *httpStatusError, got %T: %v", err, err)
	} else if statusErr.Status != 500 {
		t.Errorf("expected status 500, got %d", statusErr.Status)
	}
}

func TestRemoteRegistrar_TransportError_NilResponse(t *testing.T) {
	mock := newMockXDR(t)
	// Close the server before making the request to cause a transport error.
	mock.Server.Close()
	reg := NewRemoteRegistrar(mock.URL(), mock.Client())

	resp, err := reg.Register(t.Context(), RegistrationRequest{
		GroupID:      "grp-closed",
		InstallToken: "token-closed",
	})
	if resp != nil {
		t.Errorf("expected nil response on transport error, got %+v", resp)
	}
	if err == nil {
		t.Fatal("expected non-nil error on transport error, got nil")
	}
	// Must not panic — the test reaching here proves that.
}

// ─── remoteCredentialRefresher tests ──────────────────────────────────────────

func TestRemoteCredentialRefresher_HappyPath(t *testing.T) {
	mock := newMockXDR(t)
	refresher := NewRemoteCredentialRefresher(mock.URL(), mock.Client())

	id := Identity{
		GroupID:    "grp-refresh",
		InstanceID: "instance-grp-refresh",
		Credential: "cred-initial",
	}
	newCred, err := refresher.Refresh(t.Context(), id)
	if err != nil {
		t.Fatalf("Refresh error: %v", err)
	}
	// Mock returns "<old>-rotated".
	if newCred == id.Credential {
		t.Error("expected new credential to differ from input credential")
	}
	if newCred != "cred-initial-rotated" {
		t.Errorf("expected %q, got %q", "cred-initial-rotated", newCred)
	}
}

func TestRemoteCredentialRefresher_UnknownInstance_TypedError(t *testing.T) {
	mock := newMockXDR(t)
	refresher := NewRemoteCredentialRefresher(mock.URL(), mock.Client())

	newCred, err := refresher.Refresh(t.Context(), Identity{
		GroupID:    "grp-unknown",
		InstanceID: "unknown", // mock returns 401 for this sentinel
		Credential: "some-cred",
	})
	if newCred != "" {
		t.Errorf("expected empty credential on unknown instance, got %q", newCred)
	}
	if err == nil {
		t.Fatal("expected non-nil error on unknown instance, got nil")
	}
	var statusErr *httpStatusError
	if !errors.As(err, &statusErr) {
		t.Errorf("expected *httpStatusError, got %T: %v", err, err)
	}
}

func TestRemoteCredentialRefresher_TransportError(t *testing.T) {
	mock := newMockXDR(t)
	mock.Server.Close()
	refresher := NewRemoteCredentialRefresher(mock.URL(), mock.Client())

	newCred, err := refresher.Refresh(t.Context(), Identity{
		GroupID:    "grp-closed",
		InstanceID: "instance-closed",
		Credential: "cred-closed",
	})
	if newCred != "" {
		t.Errorf("expected empty credential on transport error, got %q", newCred)
	}
	if err == nil {
		t.Fatal("expected non-nil error on transport error, got nil")
	}
	// Must not panic — the test reaching here proves that.
}
