# Phase 5: XDR Mode-1 Registration & Credential Lifecycle - Context

**Gathered:** 2026-06-12
**Status:** Ready for planning
**Source:** /gsd-plan-phase decisions (2026-06-12)

<domain>
## Phase Boundary

Replace the deterministic local-hash InstanceID stand-in with the real EDR-pattern
registration + credential lifecycle. Build to the documented HTTP contract in
`internal/auth/auth.go` and verify against an in-process mock XDR server — no live
XDR endpoint is required. After this phase, Mode-1 instance identity is
server-assigned, persisted encrypted, and never self-reported.

IN SCOPE: real `Registrar` (register), `CredentialRefresher` (rotate), encrypted
persistence of InstanceID+Credential, wiring into `agent.ensureInstance` as the
default for `mode: remote`, and a mock-XDR-backed test suite.

OUT OF SCOPE: EUC OS collectors (Phase 6); OCSF/docs/CI hardening (Phase 7); any
change to the proto contract or to Mode-2 connectors.
</domain>

<decisions>
## Implementation Decisions (LOCKED)

### Transport & contract
- Build to the contract documented in `internal/auth/auth.go`:
  - `POST /api/v1/sdk/register` with `RegistrationRequest` (GroupID, InstanceName, InstallToken, AgentVersion, Hostname, Platform) → `RegistrationResponse` (InstanceID, Credential).
  - `POST /api/v1/sdk/credential/refresh` with GroupID + InstanceID + current Credential → new Credential.
- Remote mode requires TLS 1.3 (reuse `internal/connector` TLS posture or `net/http` with a TLS 1.3 minimum). No downgrade option.
- Use the Go stdlib `net/http` client. Do NOT add a new HTTP/networking dependency.

### Persistence
- Persist InstanceID + Credential to `agent-state.json` (the `auth.StateFile` constant) encrypted via `internal/secrets` `Store` (AES-256-GCM). Never plaintext on disk.
- On start, if a persisted InstanceID exists, reuse it and skip registration (idempotent restart). InstallToken is cleared/ignored after first successful registration.
- Credential refresh atomically replaces the stored credential (write-then-rename or the store's atomic update path).

### Wiring
- `agent.ensureInstance` selects the registrar by mode: `mode: remote` → real remote registrar (default); `mode: local` → existing simplified/local path (unchanged). A pre-set `Auth.InstanceID` short-circuits registration in both modes.
- Keep `internal/agent/registration.go` `localRegistrar` for local mode; it is no longer the remote default.

### Testing
- Stand up an in-process mock XDR HTTP server (httptest) implementing both endpoints. Cover: register happy-path, persist+reload across a simulated restart, credential refresh, install-token single-use invalidation (second register with a consumed token fails), TLS-required enforcement, and failure paths (non-2xx, network error → typed error, no panic, no partial state).
- Docker-free; runs in the default `go test ./...` suite.

### Claude's Discretion
- Exact package layout for the HTTP registrar (e.g. `internal/auth/remote.go` vs a sub-package), error types, retry/backoff on transient registration failure, and the precise secrets.Store API calls — choose to match existing conventions in `internal/secrets` and `internal/connector`.
</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Auth contract & current stand-in
- `internal/auth/auth.go` — the Registrar/CredentialRefresher interfaces, Identity/RegistrationRequest/RegistrationResponse types, StateFile constant, and the documented three-step EDR flow.
- `internal/agent/registration.go` — current `localRegistrar` (SHA-256 stand-in) + `ensureInstance` to be extended.
- `internal/agent/agent.go` — `start()` calls `ensureInstance(ctx, a.cfg, &localRegistrar{})`; `AuthConfig` (InstallToken/InstanceID/Credential) and `AgentConfig.Mode`.

### Persistence & TLS
- `internal/secrets/` (`store.go`) — AES-256-GCM encrypted store for `agent-state.json`.
- `internal/connector/tls.go` — `NewTLSConfig` (TLS 1.3 floor) for the registration HTTP client.

### Product contract
- `README.md` (Authentication Model + Local vs Remote sections) — the authoritative description of the registration flow and the "instance_id is never self-reported" invariant.
</canonical_refs>

<specifics>
## Specific Ideas

- The mock XDR server should assert the request body shape (GroupID/InstallToken present) and return a deterministic InstanceID/Credential so persistence/reload can be asserted exactly.
- Install-token single-use: the mock tracks consumed tokens and returns 401/409 on reuse; the agent must surface this as a typed error and must not overwrite a good persisted InstanceID.
</specifics>

<deferred>
## Deferred Ideas

- Live-XDR integration test (no endpoint available now — contract + mock is the v1.0 verification path).
- Credential auto-refresh-before-expiry scheduling (rotation triggering/policy) — implement the Refresh call now; automatic scheduling can follow if needed.
</deferred>

---

*Phase: 05-xdr-registration*
*Context gathered: 2026-06-12 via /gsd-plan-phase*
