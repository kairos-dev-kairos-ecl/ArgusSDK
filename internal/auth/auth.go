// Package auth implements the SDK agent authentication model.
//
// Three-step flow (EDR agent pattern):
//
//  1. Admin creates an Agent Group in the ArgusXDR portal.
//     → receives: GroupID + InstallToken (one-time, time-limited)
//
//  2. Agent starts, presents GroupID + InstallToken to XDR registration endpoint.
//     → XDR returns: InstanceID (server-assigned UUID)
//     → InstallToken is invalidated by XDR after use
//     → InstanceID is persisted to agent-state.json (AES-256-GCM via secrets.Store)
//
//  3. All subsequent signal submission:
//     → Agent presents: GroupID + InstanceID + Credential
//     → XDR stamps the server-verified InstanceID on every ingested batch
//     → instance_id is no longer self-reported
//
// Local mode (same machine): simplified path via Unix socket with file permissions.
// Remote mode (cross-network): TLS 1.3 mandatory, no config option to disable.
package auth

import "context"

// Identity holds the three-part SDK auth credentials after registration.
type Identity struct {
	GroupID    string // assigned by XDR admin at group creation
	InstanceID string // server-assigned UUID, persisted after registration
	Credential string // rotating API credential for signal submission
}

// RegistrationRequest is sent once to XDR to register this agent instance.
type RegistrationRequest struct {
	GroupID      string
	InstanceName string // human-readable label (e.g. "prod-llm-server-01")
	InstallToken string // one-time token; cleared after successful registration
	AgentVersion string
	Hostname     string
	Platform     string // "linux" | "windows" | "darwin"
}

// RegistrationResponse is returned by XDR after successful registration.
type RegistrationResponse struct {
	InstanceID string // server-assigned, stable UUID for this agent instance
	Credential string // initial rotating credential
}

// Registrar performs the one-time agent registration against the XDR endpoint.
// After successful registration the InstallToken is invalidated server-side;
// the resulting InstanceID must be persisted before the agent exits.
//
// Implementation TODO (not in this scaffold):
//   - POST /api/v1/sdk/register with JSON RegistrationRequest
//   - Verify TLS (mandatory for remote mode)
//   - Parse RegistrationResponse, return InstanceID + Credential
//   - Caller persists InstanceID via secrets.Store before returning
type Registrar interface {
	Register(ctx context.Context, req RegistrationRequest) (*RegistrationResponse, error)
}

// CredentialRefresher rotates the agent credential before it expires.
// The XDR endpoint returns a new credential; the old one is invalidated.
//
// Implementation TODO (not in this scaffold):
//   - POST /api/v1/sdk/credential/refresh with GroupID + InstanceID + current Credential
//   - Replace credential in secrets.Store atomically
type CredentialRefresher interface {
	Refresh(ctx context.Context, id Identity) (newCredential string, err error)
}

// StateFile is the path where the agent persists its InstanceID between restarts.
// It is encrypted by secrets.Store (AES-256-GCM). Never plain-text on disk.
const StateFile = "agent-state.json"
