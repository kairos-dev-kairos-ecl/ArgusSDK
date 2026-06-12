// Package agent — registration seam.
//
// This file defines the Registrar interface that maps an InstallToken+GroupID
// pair to a server-assigned InstanceID. Two implementations exist:
//
//   - localRegistrar: derives a deterministic InstanceID from the GroupID and
//     InstallToken using a truncated SHA-256 hash. This is the fully-wired
//     local path (locked decision 8).
//
//   - remoteRegistrarAdapter: wraps an auth.Registrar (from 05-01) to satisfy
//     the agent-seam Registrar interface. This is the default for mode: remote.
//     It builds the auth.RegistrationRequest (filling Hostname + Platform from
//     os.Hostname / runtime.GOOS), calls the auth.Registrar.Register, and
//     exposes the returned Credential via LastCredential so start() can persist
//     the full Identity.
//
// Thread safety: localRegistrar is stateless. remoteRegistrarAdapter is NOT
// safe for concurrent Register calls (lastCredential is unguarded) — it is
// called at most once per agent start.
package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"runtime"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/auth"
)

// Registrar maps an InstallToken + GroupID pair to a stable InstanceID.
// Implementations must be safe to call from multiple goroutines.
type Registrar interface {
	// Register exchanges an InstallToken for a server-assigned InstanceID.
	// It is called at most once per agent start (ensureInstance short-circuits
	// when cfg.Auth.InstanceID is already set).
	Register(ctx context.Context, installToken, groupID string) (string, error)
}

// localRegistrar is the default Registrar for the local path.
// It derives a deterministic InstanceID by hashing the GroupID and InstallToken
// with SHA-256 and returning the first 16 hex characters (64-bit prefix).
// No network call or live-XDR dependency is required.
//
// NOTE: The deterministic nature means the InstanceID is stable across restarts
// as long as the same GroupID+InstallToken pair is supplied. This is intentional
// for the local-path use case (locked decision 8).
type localRegistrar struct{}

// Register derives a deterministic InstanceID from groupID+installToken.
// The ID is a 16-character hex prefix of the SHA-256 digest of
// "<groupID>:<installToken>".
func (l *localRegistrar) Register(_ context.Context, installToken, groupID string) (string, error) {
	h := sha256.New()
	// Separator ':' prevents "ab"+"c" == "a"+"bc" collisions in the input.
	h.Write([]byte(groupID + ":" + installToken))
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:8]), nil // 8 bytes → 16 hex chars
}

// ensureInstance resolves the InstanceID for the agent using the following
// precedence:
//
//  1. If cfg.Auth.InstanceID is already non-empty, return it immediately
//     (short-circuit: no registration call).
//  2. If cfg.Auth.InstallToken is non-empty, call r.Register and return the
//     result.
//  3. If both are empty, return an error — the agent cannot operate without
//     an identity.
func ensureInstance(ctx context.Context, cfg *Config, r Registrar) (string, error) {
	if cfg.Auth.InstanceID != "" {
		return cfg.Auth.InstanceID, nil
	}
	if cfg.Auth.InstallToken == "" {
		return "", fmt.Errorf("agent: cannot resolve InstanceID: both Auth.InstanceID and Auth.InstallToken are empty")
	}
	id, err := r.Register(ctx, cfg.Auth.InstallToken, cfg.Agent.GroupID)
	if err != nil {
		return "", fmt.Errorf("agent: registration failed: %w", err)
	}
	return id, nil
}

// ─── remoteRegistrarAdapter ───────────────────────────────────────────────────

// remoteRegistrarAdapter adapts an auth.Registrar to the agent-seam Registrar
// interface. It bridges the agent's (installToken, groupID) signature to the
// auth.RegistrationRequest shape, filling Hostname + Platform from the runtime.
//
// After a successful Register call, the returned Credential is stored in
// lastCredential and is accessible via LastCredential() so the caller can
// persist the full Identity (InstanceID + Credential) to the secrets store.
type remoteRegistrarAdapter struct {
	inner          auth.Registrar
	instanceName   string
	agentVersion   string
	lastCredential string
}

// NewRemoteRegistrarAdapter wraps an auth.Registrar in the agent-seam adapter.
// instanceName and agentVersion are included in the RegistrationRequest sent
// to the XDR endpoint.
func NewRemoteRegistrarAdapter(r auth.Registrar, instanceName, agentVersion string) *remoteRegistrarAdapter {
	return &remoteRegistrarAdapter{
		inner:        r,
		instanceName: instanceName,
		agentVersion: agentVersion,
	}
}

// Register implements agent.Registrar by building an auth.RegistrationRequest
// and delegating to the wrapped auth.Registrar. On success, the Credential is
// stored internally and the InstanceID is returned. On failure, the error is
// returned unwrapped so errors.Is(err, auth.ErrInstallTokenConsumed) still
// matches after ensureInstance wraps it.
func (a *remoteRegistrarAdapter) Register(ctx context.Context, installToken, groupID string) (string, error) {
	hostname, _ := os.Hostname() // best-effort; empty string is acceptable
	req := auth.RegistrationRequest{
		GroupID:      groupID,
		InstanceName: a.instanceName,
		InstallToken: installToken,
		AgentVersion: a.agentVersion,
		Hostname:     hostname,
		Platform:     runtime.GOOS,
	}
	resp, err := a.inner.Register(ctx, req)
	if err != nil {
		return "", err // propagate so errors.Is still matches ErrInstallTokenConsumed
	}
	a.lastCredential = resp.Credential
	return resp.InstanceID, nil
}

// LastCredential returns the Credential from the most recent successful Register
// call. It is empty until Register succeeds.
func (a *remoteRegistrarAdapter) LastCredential() string {
	return a.lastCredential
}

// ─── selectRegistrar ──────────────────────────────────────────────────────────

// selectRegistrar returns the mode-appropriate Registrar for the agent.
//
//   - cfg.Agent.Mode == "remote" → returns the supplied remote adapter
//     (the default for mode: remote, locked decision from 05-CONTEXT).
//   - any other mode (including "local" and unset) → returns localRegistrar
//     (locked decision 8).
//
// The caller is responsible for constructing the remote adapter via
// NewRemoteRegistrarAdapter before calling selectRegistrar.
func selectRegistrar(cfg *Config, remote Registrar) Registrar {
	if cfg.Agent.Mode == "remote" {
		return remote
	}
	return &localRegistrar{}
}
