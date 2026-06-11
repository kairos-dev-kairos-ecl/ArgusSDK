// Package agent — registration seam.
//
// This file defines the Registrar interface that maps an InstallToken+GroupID
// pair to a server-assigned InstanceID. Two implementations exist:
//
//   - localRegistrar: derives a deterministic InstanceID from the GroupID and
//     InstallToken using a truncated SHA-256 hash. This is the fully-wired
//     local path (locked decision 8).
//
//   - Remote registrar (deferred sub-item): a future implementation will call
//     the live ArgusXDR registration RPC. The Registrar interface is defined
//     here so a remote impl can be dropped in without modifying the agent
//     wiring. It is NOT required for tests — ensureInstance is exercised via
//     the fakeRegistrar defined in registration_test.go.
//
// Thread safety: localRegistrar is stateless; it is safe to call Register
// concurrently.
package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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
