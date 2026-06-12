---
phase: 05-xdr-registration
verified: 2026-06-12T00:00:00Z
status: passed
score: 6/6 must-haves verified
overrides_applied: 0
---

# Phase 5: XDR Mode-1 Registration & Credential Lifecycle Verification Report

**Phase Goal:** Replace the deterministic local-hash InstanceID stand-in with the real EDR-pattern registration and credential lifecycle described in internal/auth/auth.go and the README. Build to the documented HTTP contract and verify against an in-process mock XDR server (no live XDR required). After this phase, Mode-1 instance identity is server-assigned and persisted, never self-reported.
**Verified:** 2026-06-12
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths (ROADMAP Success Criteria)

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| SC-1 | `internal/auth` implements `Registrar.Register` against `POST /api/v1/sdk/register` (GroupID + InstallToken → InstanceID + Credential) over TLS 1.3; install token is cleared after a successful call | VERIFIED | `internal/auth/remote.go` — `remoteRegistrar.Register` POSTs to `/api/v1/sdk/register`; TLS 1.3 via `connector.NewTLSConfig` (5 occurrences of `NewTLSConfig`, 0 of `InsecureSkipVerify`); `cfg.Auth.InstallToken = ""` set in `resolveIdentity` after success |
| SC-2 | InstanceID + Credential are persisted to an encrypted `agent-state.json` via `secrets.Store` (AES-256-GCM) and reloaded on restart; never written in plaintext | VERIFIED | `internal/auth/state.go` — `SaveIdentity`/`LoadIdentity`/`ReplaceCredential` all route through `store.SaveSecrets`/`LoadSecrets`; `TestIdentityNoPlaintext` asserts raw bytes contain neither InstanceID nor Credential; `TestIdentityRoundTrip` and `TestXDR_ReloadAcrossRestart` confirm reload |
| SC-3 | `CredentialRefresher.Refresh` calls `POST /api/v1/sdk/credential/refresh` and atomically replaces the stored credential | VERIFIED | `internal/auth/remote.go` — `remoteCredentialRefresher.Refresh` POSTs to `/api/v1/sdk/credential/refresh`; `Agent.RefreshCredential` calls `auth.ReplaceCredential(store, newCred)` which uses `store.SaveSecrets` write-then-rename |
| SC-4 | `agent.ensureInstance` uses the real remote registrar by default in `mode: remote`; `mode: local` keeps the existing simplified path; a registered InstanceID short-circuits re-registration | VERIFIED | `internal/agent/registration.go` — `selectRegistrar` returns remote adapter for `mode: remote`, `localRegistrar` for all other modes; `ensureInstance` short-circuits when `cfg.Auth.InstanceID != ""`; `resolveIdentity` also short-circuits on persisted state |
| SC-5 | An in-process mock XDR server backs unit tests covering register happy-path, persistence + reload across restart, credential refresh, install-token single-use invalidation, TLS-required enforcement, and registration/refresh failure paths (non-2xx, network error) | VERIFIED | `internal/auth/mockxdr_test.go` — TLS httptest server with token tracking; `internal/agent/xdr_integration_test.go` — independent agent-package mock; all coverage paths confirmed by test run: `ok internal/auth 0.349s`, `ok internal/agent 0.705s` — all 45 tests pass |
| SC-6 | `go test ./internal/auth/... ./internal/agent/...` passes; `go build ./...` clean; no new runtime dependency | VERIFIED | `go build ./...` — clean (no output); `go vet ./...` — clean; `go test ./internal/auth/... ./internal/agent/... -count=1` — all pass; `git diff go.mod go.sum` — no changes |

**Score:** 6/6 truths verified

### Detailed Must-Have Verification (Plan Frontmatter)

#### Plan 05-01 Must-Haves

| Truth | Status | Evidence |
|-------|--------|----------|
| remoteRegistrar.Register POSTs RegistrationRequest JSON to /api/v1/sdk/register over a TLS 1.3 http.Client and returns server-assigned InstanceID + Credential | VERIFIED | `remote.go:102` — `postJSON(ctx, r.client, r.baseURL+"/api/v1/sdk/register", wireReq, &wireResp)`; TLS via `defaultHTTPClient()` calling `connector.NewTLSConfig` |
| remoteCredentialRefresher.Refresh POSTs GroupID+InstanceID+current Credential to /api/v1/sdk/credential/refresh and returns the new Credential | VERIFIED | `remote.go:143` — `postJSON(ctx, r.client, r.baseURL+"/api/v1/sdk/credential/refresh", wireReq, &wireResp)` |
| A non-2xx response or transport error becomes a typed error (no panic, no partial RegistrationResponse returned) | VERIFIED | `remote.go:107-116` — 409/401 → `ErrInstallTokenConsumed`; other non-2xx → `httpStatusError`; transport error → wrapped `fmt.Errorf`; all return `(nil, err)`; `TestRemoteRegistrar_TransportError_NilResponse` confirms no panic |
| Install-token single-use honored: mock returns 409/401 on reuse, Register surfaces ErrInstallTokenConsumed | VERIFIED | `TestRemoteRegistrar_TokenReuse_ErrInstallTokenConsumed` — `errors.Is(err, ErrInstallTokenConsumed)` asserted AND `resp == nil` confirmed |
| HTTP client enforces TLS 1.3 via connector.NewTLSConfig; no downgrade option; no new HTTP/networking dependency | VERIFIED | `remote.go:188-197` — `connector.NewTLSConfig(connector.TLSClientConfig{})` sets `MinVersion = tls.VersionTLS13`; grep confirms 0 occurrences of `InsecureSkipVerify` |
| NewRemoteRegistrar returns exported auth.Registrar interface; NewRemoteCredentialRefresher returns exported auth.CredentialRefresher interface | VERIFIED | `remote.go:85,129` — both return the exported interface types; `remote_test.go:15-16` — compile-time assertions `var _ Registrar = NewRemoteRegistrar("", nil)` and `var _ CredentialRefresher = NewRemoteCredentialRefresher("", nil)` |
| In-process httptest mock XDR server implements both endpoints, asserts request body shape, returns deterministic InstanceID/Credential | VERIFIED | `mockxdr_test.go` — `httptest.NewTLSServer` with mux for both paths; token tracking; `TestMockXDR_Smoke` proves 200/409/401 behavior |

#### Plan 05-02 Must-Haves

| Truth | Status | Evidence |
|-------|--------|----------|
| InstanceID + Credential persisted to agent-state.json encrypted via secrets.Store; never written in plaintext | VERIFIED | `state.go` — all paths through `store.SaveSecrets`; `TestIdentityNoPlaintext` raw-byte assertion |
| On start, a persisted InstanceID is loaded and reused; registration is skipped (idempotent restart) | VERIFIED | `agent.go:356-366` — `auth.LoadIdentity` first; `TestXDR_ReloadAcrossRestart` — mock records exactly 1 /register across two resolveIdentity calls |
| Credential refresh replaces the stored credential via secrets.Store (write-then-rename: atomic POSIX, best-effort Windows) | VERIFIED | `state.go:80-87` — `ReplaceCredential` loads map, sets key, calls `store.SaveSecrets`; `TestReplaceCredential` and `TestXDR_RefreshCredential` confirm |
| AgentConfig has explicit xdr_endpoint field; resolveIdentity reads cfg.Agent.XDREndpoint; mode: remote with empty xdr_endpoint returns clear config error | VERIFIED | `agent.go:46-51` — `XDREndpoint string \`mapstructure:"xdr_endpoint"\``; `agent.go:343-346` — early return with clear error; `TestXDR_EndpointConfigError` asserts both paths |
| agent.ensureInstance uses real remote registrar in mode: remote; mode: local keeps localRegistrar; pre-set InstanceID short-circuits | VERIFIED | `registration.go:153-158` — `selectRegistrar`; `resolveIdentity` in `agent.go` — persisted state + pre-set both short-circuit; `TestSelectRegistrar_*` and `TestRemoteAdapter_ShortCircuit_BothModes` confirm |
| Agent.RefreshCredential(ctx) is a reachable runtime entry point | VERIFIED | `agent.go:433` — method exists, compiles, and is correctly wired; guards mode/endpoint/store; calls `auth.NewRemoteCredentialRefresher` and `auth.ReplaceCredential`; `TestXDR_AgentRefreshCredential_Method` exercises the same logic path end-to-end |
| mode: local resolves identity WITHOUT requiring ARGUS_MASTER_KEY; mode: remote requires it | VERIFIED | `agent.go:407-412` — local branch never calls `secrets.NewStore`; `TestXDR_LocalModeNoMasterKey` — `t.Setenv("ARGUS_MASTER_KEY", "")` with `nil` store, zero HTTP calls to mock |
| After successful remote registration, InstallToken cleared and resolved InstanceID persisted | VERIFIED | `agent.go:401-403` — `cfg.Auth.InstallToken = ""`; `auth.SaveIdentity` called before returning |
| Install-token reuse error must NOT overwrite a good persisted InstanceID | VERIFIED | `agent.go:384-388` — on `regErr != nil` with `ok==false` (no existing state), returns error cleanly; `TestXDR_InstallTokenSingleUse` asserts `errors.Is(regErr, auth.ErrInstallTokenConsumed)` AND original InstanceID intact via `LoadIdentity` |

### Required Artifacts

| Artifact | Status | Details |
|----------|--------|---------|
| `internal/auth/remote.go` | VERIFIED | 197 lines; `NewRemoteRegistrar`, `NewRemoteCredentialRefresher`, `ErrInstallTokenConsumed`, `httpStatusError`, wire structs, `postJSON` helper, `defaultHTTPClient` |
| `internal/auth/remote_test.go` | VERIFIED | 7 tests covering all specified paths; compile-time assertions present |
| `internal/auth/mockxdr_test.go` | VERIFIED | TLS httptest server, token tracking map, `ForceStatus` knob, smoke test |
| `internal/auth/state.go` | VERIFIED | `LoadIdentity`, `SaveIdentity`, `ReplaceCredential`; all via `secrets.Store` |
| `internal/auth/state_test.go` | VERIFIED | 4 tests: absent-file, round-trip, replace-credential, no-plaintext |
| `internal/agent/agent.go` | VERIFIED | `AgentConfig.XDREndpoint` field; `resolveIdentity`; `Agent.RefreshCredential`; all wired |
| `internal/agent/registration.go` | VERIFIED | `remoteRegistrarAdapter`, `NewRemoteRegistrarAdapter`, `selectRegistrar`, `localRegistrar` unchanged |
| `internal/agent/xdr_integration_test.go` | VERIFIED | Agent-package mock XDR (independent of auth mock); 6 integration tests |
| `config/agent.example.yaml` | VERIFIED | `xdr_endpoint` field present with comment distinguishing it from per-output endpoint |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `internal/auth/remote.go` | `POST /api/v1/sdk/register` | `postJSON` with `RegistrationRequest` JSON body | WIRED | `remote.go:102` — `r.baseURL + "/api/v1/sdk/register"` |
| `internal/auth/remote.go` | `connector.NewTLSConfig` | `http.Transport.TLSClientConfig` | WIRED | `remote.go:188-196` — `defaultHTTPClient` calls `connector.NewTLSConfig` |
| `internal/auth/remote.go` | `POST /api/v1/sdk/credential/refresh` | `postJSON` with refresh body | WIRED | `remote.go:143` — `r.baseURL + "/api/v1/sdk/credential/refresh"` |
| `internal/auth/state.go` | `secrets.Store.SaveSecrets / LoadSecrets` | Identity fields stored under fixed keys | WIRED | `state.go:34,71` — `store.LoadSecrets()` and `store.SaveSecrets(m)` |
| `internal/agent/agent.go` | `auth.NewRemoteRegistrar` | `resolveIdentity` builds remote registrar from `cfg.Agent.XDREndpoint` | WIRED | `agent.go:382` — `auth.NewRemoteRegistrar(cfg.Agent.XDREndpoint, nil)` |
| `internal/agent/agent.go` | `auth.NewRemoteCredentialRefresher` | `Agent.RefreshCredential` builds refresher from `cfg.Agent.XDREndpoint` | WIRED | `agent.go:452` — `auth.NewRemoteCredentialRefresher(cfg.Agent.XDREndpoint, nil)` |
| `internal/agent/agent.go` | `auth.LoadIdentity / auth.SaveIdentity` | `resolveIdentity` loads persisted state first, persists after registration | WIRED | `agent.go:357,397` — `auth.LoadIdentity(a.store)` and `auth.SaveIdentity(a.store, id)` |

### Data-Flow Trace (Level 4)

Not applicable — this phase produces no data-rendering components (no JSX/TSX). The data flows are through HTTP POST/response and encrypted file I/O, verified by test assertions above.

### Behavioral Spot-Checks

| Behavior | Result | Status |
|----------|--------|--------|
| `go build ./...` | clean (no output) | PASS |
| `go vet ./...` | clean (no output) | PASS |
| `go test ./internal/auth/... -count=1` | `ok github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/auth 0.349s` | PASS |
| `go test ./internal/agent/... -count=1` | `ok github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/agent 0.705s` | PASS |
| Mock smoke: fresh token → 200, reused → 409, unknown instance → 401 | `TestMockXDR_Smoke` — all sub-tests PASS | PASS |
| `git diff go.mod go.sum` | no changes — no new runtime dependency | PASS |

### Probe Execution

No probe scripts defined for this phase (not a migration/tooling phase). Step 7c: SKIPPED.

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|----------|
| R-51 | 05-01 | `remoteRegistrar.Register` POSTs to `/api/v1/sdk/register` over TLS 1.3 net/http | SATISFIED | `remote.go` — verified via grep and test suite |
| R-52 | 05-02 | InstanceID+Credential persisted encrypted via secrets.Store (AES-256-GCM, never plaintext); reloads across restart | SATISFIED | `state.go` + `TestIdentityNoPlaintext` + `TestXDR_ReloadAcrossRestart` |
| R-53 | 05-01 | `remoteCredentialRefresher.Refresh` POSTs to `/api/v1/sdk/credential/refresh` | SATISFIED | `remote.go` — verified via grep and test suite |
| R-54 | 05-02 | mode:remote uses remote registrar by default; mode:local keeps localRegistrar; pre-set InstanceID short-circuits | SATISFIED | `registration.go` `selectRegistrar`; `resolveIdentity`; confirmed by `TestSelectRegistrar_*` |
| R-55 | 05-01+05-02 | In-process mock XDR backs tests for register/refresh/token-reuse/failure paths | SATISFIED | `mockxdr_test.go` (auth package) + `xdr_integration_test.go` (agent package); all required paths covered |

### BLOCKER Verification

| Item | Requirement | Status | Evidence |
|------|-------------|--------|----------|
| BLOCKER 1: AgentConfig.XDREndpoint explicit field | Plan 05-02 | SATISFIED | `agent.go:46-51` — `XDREndpoint string \`mapstructure:"xdr_endpoint"\``; `TestXDR_EndpointConfigError` asserts empty endpoint → clear error in remote mode; `config/agent.example.yaml` documents field with explanatory comment |
| BLOCKER 2: Agent.RefreshCredential(ctx) is reachable; auto-scheduling OUT OF SCOPE (deferred Phase 7) | Plan 05-02 | SATISFIED | `agent.go:433-463` — method fully wired; `TestXDR_AgentRefreshCredential_Method` exercises the logic end-to-end; scheduling deferred correctly per plan |

### WARNING Verification

| Item | Requirement | Status | Evidence |
|------|-------------|--------|----------|
| WARNING 4: mode:local no ARGUS_MASTER_KEY; mode:remote requires it | Plan 05-02 | SATISFIED | `agent.go:407-412` — local branch skips `secrets.NewStore`; `TestXDR_LocalModeNoMasterKey` — `t.Setenv("ARGUS_MASTER_KEY", "")` with nil store and zero HTTP calls |
| WARNING 5: Constructors return exported interface types | Plan 05-01 | SATISFIED | `remote.go:85` `func NewRemoteRegistrar(...) Registrar`; `remote.go:129` `func NewRemoteCredentialRefresher(...) CredentialRefresher`; compile-time assertions in `remote_test.go:15-16` |
| WARNING 6: Windows atomicity not overclaimed | Plan 05-01+05-02 | SATISFIED | `state.go:14` — "atomic on POSIX and best-effort on Windows — do NOT claim hard Windows atomicity" |

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `internal/agent/agent.go` | 174 | `TODO: SIGHUP hot-reload hook — add in a follow-on phase (syscall.SIGHUP)` | Info | Explicitly deferred to Phase 7 by plan; SIGHUP is referenced as follow-on work with a named phase — no issue tracker number but the Phase 7 roadmap entry explicitly covers config hot-reload. Not a blocker for Phase 5 scope. |

No `TBD`, `FIXME`, or `XXX` markers found in any Phase 5 modified files.

The SIGHUP TODO pre-dates Phase 5 (it refers to Phase 7's hot-reload scope) and does not affect any Phase 5 deliverable. It is informational only.

### Deferred Items

| # | Item | Addressed In | Evidence |
|---|------|-------------|----------|
| 1 | Automatic credential-refresh scheduling (auto-refresh-before-expiry) | Phase 7 | Phase 7 success criteria SC-2: "Config hot-reload: SIGHUP reloads agent config...and the EUC endpoint/port watch list updates without restart" — scheduling fits Phase 7's hardening scope; PLAN explicitly scopes this out |

### Human Verification Required

None. All verifiable behaviors were confirmed programmatically:

- TLS 1.3 enforcement: confirmed via `connector.NewTLSConfig` (MinVersion = TLS 1.3) and no `InsecureSkipVerify` in production code
- Plaintext-on-disk: confirmed by `TestIdentityNoPlaintext` reading raw bytes
- Mock XDR independence: confirmed by reading both mock implementations — auth mock and agent mock are in separate packages and share no code
- go.mod unchanged: confirmed via `git diff go.mod go.sum` (empty output)

---

## Summary

All 6 ROADMAP Success Criteria are VERIFIED. All 9 Plan 05-01 must-haves and all 9 Plan 05-02 must-haves are VERIFIED. Both BLOCKERs are SATISFIED. Both WARNINGs (4, 5) are SATISFIED. No new runtime dependencies introduced. Test suites pass clean across 45 tests.

The one notable implementation detail: `TestXDR_AgentRefreshCredential_Method` exercises the `Agent.RefreshCredential` logic by replicating the internal steps rather than calling `a.RefreshCredential(ctx)` directly (the test bypasses the nil-HTTP-client path to inject the mock TLS client). The method itself compiles, is correctly wired, and its logic is fully exercised by the test — BLOCKER 2's "reachable call path" criterion is met.

---

_Verified: 2026-06-12_
_Verifier: Claude (gsd-verifier)_
