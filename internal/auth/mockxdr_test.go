// Package auth — in-process mock XDR server for unit tests.
//
// mockXDR is an httptest.Server-backed mock that implements both documented
// XDR endpoints:
//
//	POST /api/v1/sdk/register
//	POST /api/v1/sdk/credential/refresh
//
// It tracks consumed InstallTokens so single-use enforcement can be exercised,
// and exposes a ForceStatus knob to drive non-2xx paths.
//
// NOTE: this helper lives in internal/auth/*_test.go and is NOT importable from
// other packages. Tests in internal/agent (plan 05-02) must stand up their own
// small httptest mock.
package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// mockXDR is a test-only in-process XDR server.
type mockXDR struct {
	Server *httptest.Server

	mu             sync.Mutex
	consumedTokens map[string]struct{}

	// ForceStatus, when non-zero, causes every handler to respond with that
	// HTTP status code (no body). Use this to exercise non-2xx paths.
	ForceStatus int
}

// newMockXDR starts an in-process TLS httptest server implementing both
// documented XDR endpoints and returns the helper.  The server is registered
// with t.Cleanup so callers do not need to call Close() explicitly.
func newMockXDR(t *testing.T) *mockXDR {
	t.Helper()
	m := &mockXDR{
		consumedTokens: make(map[string]struct{}),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/sdk/register", m.handleRegister)
	mux.HandleFunc("/api/v1/sdk/credential/refresh", m.handleRefresh)
	m.Server = httptest.NewTLSServer(mux)
	t.Cleanup(m.Server.Close)
	return m
}

// Client returns the httptest.Server's pre-configured *http.Client, which
// already trusts the mock's TLS certificate.  Use this as the httpClient
// argument to NewRemoteRegistrar / NewRemoteCredentialRefresher in tests.
func (m *mockXDR) Client() *http.Client {
	return m.Server.Client()
}

// URL returns the base URL of the mock server (e.g. "https://127.0.0.1:PORT").
func (m *mockXDR) URL() string {
	return m.Server.URL
}

// handleRegister serves POST /api/v1/sdk/register.
func (m *mockXDR) handleRegister(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	forceStatus := m.ForceStatus
	m.mu.Unlock()

	if forceStatus != 0 {
		w.WriteHeader(forceStatus)
		return
	}

	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request body", http.StatusBadRequest)
		return
	}
	if req.GroupID == "" || req.InstallToken == "" {
		http.Error(w, "group_id and install_token required", http.StatusBadRequest)
		return
	}

	m.mu.Lock()
	_, consumed := m.consumedTokens[req.InstallToken]
	if !consumed {
		m.consumedTokens[req.InstallToken] = struct{}{}
	}
	m.mu.Unlock()

	if consumed {
		// Token already used — signal single-use invalidation.
		w.WriteHeader(http.StatusConflict)
		return
	}

	// Deterministic response so tests can assert exact values.
	resp := registerResponse{
		InstanceID: fmt.Sprintf("instance-%s", req.GroupID),
		Credential: "cred-initial",
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// handleRefresh serves POST /api/v1/sdk/credential/refresh.
func (m *mockXDR) handleRefresh(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	forceStatus := m.ForceStatus
	m.mu.Unlock()

	if forceStatus != 0 {
		w.WriteHeader(forceStatus)
		return
	}

	var req refreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request body", http.StatusBadRequest)
		return
	}
	if req.GroupID == "" || req.InstanceID == "" || req.Credential == "" {
		http.Error(w, "group_id, instance_id and credential required", http.StatusBadRequest)
		return
	}

	// Unknown/empty InstanceID → 401.
	if req.InstanceID == "unknown" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	resp := refreshResponse{
		Credential: fmt.Sprintf("%s-rotated", req.Credential),
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// ─── Smoke test ───────────────────────────────────────────────────────────────

// TestMockXDR_Smoke verifies that the mock XDR server responds correctly
// BEFORE the production remoteRegistrar/remoteCredentialRefresher is wired up.
// This is the RED gate: it exercises only the mock's raw HTTP behavior.
func TestMockXDR_Smoke(t *testing.T) {
	mock := newMockXDR(t)
	client := mock.Client()
	baseURL := mock.URL()

	// ── Register: fresh token → 200 ───────────────────────────────────────────
	t.Run("register_fresh_token_200", func(t *testing.T) {
		req := registerRequest{
			GroupID:      "grp-1",
			InstallToken: "token-smoke-1",
		}
		var resp registerResponse
		status, err := postJSON(t.Context(), client, baseURL+"/api/v1/sdk/register", req, &resp)
		if err != nil {
			t.Fatalf("register transport error: %v", err)
		}
		if status != http.StatusOK {
			t.Fatalf("expected 200, got %d", status)
		}
		if resp.InstanceID != "instance-grp-1" {
			t.Errorf("expected instance-grp-1, got %q", resp.InstanceID)
		}
		if resp.Credential != "cred-initial" {
			t.Errorf("expected cred-initial, got %q", resp.Credential)
		}
	})

	// ── Register: reused token → 409 ──────────────────────────────────────────
	t.Run("register_reused_token_409", func(t *testing.T) {
		req := registerRequest{
			GroupID:      "grp-1",
			InstallToken: "token-smoke-1", // already consumed above
		}
		status, err := postJSON(t.Context(), client, baseURL+"/api/v1/sdk/register", req, nil)
		if err != nil {
			t.Fatalf("register transport error: %v", err)
		}
		if status != http.StatusConflict {
			t.Fatalf("expected 409, got %d", status)
		}
	})

	// ── Refresh: unknown InstanceID → 401 ─────────────────────────────────────
	t.Run("refresh_unknown_instance_401", func(t *testing.T) {
		req := refreshRequest{
			GroupID:    "grp-1",
			InstanceID: "unknown",
			Credential: "some-cred",
		}
		status, err := postJSON(t.Context(), client, baseURL+"/api/v1/sdk/credential/refresh", req, nil)
		if err != nil {
			t.Fatalf("refresh transport error: %v", err)
		}
		if status != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", status)
		}
	})

	// ── Register: missing required fields → 400 ───────────────────────────────
	t.Run("register_missing_fields_400", func(t *testing.T) {
		req := registerRequest{GroupID: "", InstallToken: ""}
		status, err := postJSON(t.Context(), client, baseURL+"/api/v1/sdk/register", req, nil)
		if err != nil {
			t.Fatalf("register transport error: %v", err)
		}
		if status != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", status)
		}
	})
}
