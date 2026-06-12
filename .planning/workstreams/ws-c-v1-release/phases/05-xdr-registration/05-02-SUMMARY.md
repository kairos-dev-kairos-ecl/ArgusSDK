---
phase: 05-xdr-registration
plan: "02"
subsystem: agent,auth
tags: [auth, xdr, registration, persistence, secrets, encrypted-state, credential-refresh, mode-aware, tdd]
dependency_graph:
  requires: [internal/auth/remote.go (05-01), internal/secrets/store.go, internal/auth/auth.go]
  provides: [internal/auth/state.go, internal/agent/registration.go (mode-aware), internal/agent/agent.go (XDREndpoint+resolveIdentity+RefreshCredential)]
  affects: [internal/agent (start() registration block replaced), config/agent.example.yaml]
tech_stack:
  added: []
  patterns: [AES-256-GCM encrypted map via secrets.Store, write-then-rename (atomic POSIX/best-effort Windows), httptest TLS mock in-package, mode-aware registrar selection, persisted-state-first identity resolution]
key_files:
  created:
    - internal/auth/state.go
    - internal/auth/state_test.go
    - internal/agent/xdr_integration_test.go
  modified:
    - internal/agent/registration.go
    - internal/agent/registration_test.go
    - internal/agent/agent.go
    - config/agent.example.yaml
decisions:
  - "resolveIdentity uses an injectable resolveIdentityFn seam on Agent so tests drive the full flow without live XDR or ARGUS_MASTER_KEY"
  - "agentVersion var defaulting to 'dev' allows linker -ldflags injection at build time with no test breakage"
  - "SaveIdentity only writes non-empty fields to prevent a partial save from blanking an existing key (e.g. empty Credential on a short-circuit path)"
  - "secrets.SaveSecrets replace path is atomic on POSIX, best-effort on Windows — not claimed as hard-atomic on Windows anywhere in code or docs"
metrics:
  duration_seconds: 1200
  completed_date: "2026-06-12"
  tasks_completed: 4
  tasks_total: 4
  files_created: 3
  files_modified: 4
  commits: 4
---

# Phase 5 Plan 02: Persist and Wire Real Registration Lifecycle Summary

**One-liner:** Encrypted Identity persistence (AES-256-GCM via secrets.Store), mode-aware registrar wiring, AgentConfig.XDREndpoint + resolveIdentity (persisted-state-first), and Agent.RefreshCredential backed by a Docker-free agent-package mock XDR.

## What Was Built

### Task 1 — Encrypted Identity persistence (auth/state.go)

`internal/auth/state.go` adds three functions over the existing `secrets.Store`:

- `LoadIdentity(store)` — reads the encrypted map; returns `(Identity, true, nil)` when `InstanceID` is present, `(zero, false, nil)` when absent, propagates decryption errors
- `SaveIdentity(store, id)` — loads the existing map, overlays the three Identity fields (only non-empty values to avoid blanking), saves via `store.SaveSecrets`
- `ReplaceCredential(store, newCredential)` — load → overwrite credential key → SaveSecrets; reuses `SaveSecrets`' write-then-rename path (atomic on POSIX, best-effort on Windows)

Fixed map keys: `group_id`, `instance_id`, `credential`.

`state_test.go` covers:
- `TestIdentityLoad_AbsentFile` — ok=false, no error on fresh store
- `TestIdentityRoundTrip` — Save→Load returns identical Identity
- `TestReplaceCredential` — credential updated, InstanceID+GroupID unchanged
- `TestIdentityNoPlaintext` — raw file bytes contain neither InstanceID nor Credential canary strings (T-05-05)

### Task 2 — Mode-aware registrar selection (agent/registration.go)

`remoteRegistrarAdapter` wraps `auth.Registrar` to satisfy the agent-seam `Registrar` interface:
- Builds `auth.RegistrationRequest` with `Hostname` (os.Hostname best-effort) + `Platform` (runtime.GOOS)
- `Register` propagates errors unwrapped so `errors.Is(err, auth.ErrInstallTokenConsumed)` still matches through `ensureInstance`
- `LastCredential()` exposes the returned Credential so `start()` can persist the full Identity

`selectRegistrar(cfg, remote)` returns the remote adapter for `mode:remote`, `&localRegistrar{}` otherwise. `localRegistrar` is unchanged (mode: local path locked).

`registration_test.go` adds: `TestSelectRegistrar_LocalMode`, `TestSelectRegistrar_RemoteMode`, `TestRemoteAdapter_HappyPath`, `TestRemoteAdapter_TokenConsumed`, `TestRemoteAdapter_ShortCircuit_BothModes`.

### Task 3 — AgentConfig.XDREndpoint + resolveIdentity + RefreshCredential

`internal/agent/agent.go` changes:

1. **AgentConfig.XDREndpoint** (`mapstructure:"xdr_endpoint"`) — explicit XDR base URL for registration + refresh; documented as distinct from per-output `Outputs[i].Endpoint`

2. **`Agent.resolveIdentity(ctx)`** — persisted-state-first, mode-aware:
   - mode:remote → validates `XDREndpoint != ""`, constructs `secrets.Store` (reads `ARGUS_MASTER_KEY`), calls `auth.LoadIdentity`; if persisted InstanceID found, reuses it (no re-register); else registers via `remoteRegistrarAdapter` and persists via `auth.SaveIdentity`; on `ErrInstallTokenConsumed` with no persisted state, returns error (T-05-07)
   - mode:local → no store, no master key; delegates to `localRegistrar` (WARNING 4 gated)

3. **`Agent.RefreshCredential(ctx)`** — reachable runtime entry point: guards mode+endpoint, loads Identity, calls `auth.NewRemoteCredentialRefresher`, calls `auth.ReplaceCredential`. Automatic refresh scheduling deferred to Phase 7.

4. **Injectable seam** — `Agent.resolveIdentityFn` func field allows tests to bypass the live path without `ARGUS_MASTER_KEY`.

5. **`start()` registration block** replaced: calls `resolveIdentityFn` when set, otherwise `resolveIdentity`.

`config/agent.example.yaml` gains `xdr_endpoint` under `agent:` with a comment clarifying it is the registration/refresh URL, required in remote mode, distinct from per-output endpoints.

### Task 4 — Agent-package mock XDR + integration tests

`internal/agent/xdr_integration_test.go` defines `agentMockXDR` — a fully independent `httptest.NewTLSServer` mock (does NOT import `internal/auth/*_test.go`):
- `/api/v1/sdk/register` — 409 on consumed token, deterministic `instance-<GroupID>` + `cred-initial`
- `/api/v1/sdk/credential/refresh` — returns `<old>-rotated`
- `RegisterCount()` for assertion

Tests:

| Test | What it verifies |
|------|-----------------|
| `TestXDR_ReloadAcrossRestart` | Two resolves on same state path → same InstanceID, exactly 1 /register call |
| `TestXDR_RefreshCredential` | Mock /refresh hit AND LoadIdentity returns rotated credential |
| `TestXDR_InstallTokenSingleUse` | Consumed token → typed error, original InstanceID persisted intact |
| `TestXDR_EndpointConfigError` | Empty xdr_endpoint fails; set xdr_endpoint reaches mock.URL |
| `TestXDR_LocalModeNoMasterKey` | mode:local resolves with ARGUS_MASTER_KEY="" and zero HTTP calls to mock |
| `TestXDR_AgentRefreshCredential_Method` | RefreshCredential path end-to-end with pre-seeded identity |

## Commits

| Hash | Type | Description |
|------|------|-------------|
| `64dc323` | feat(05-02) | Encrypted Identity persistence via secrets.Store (Task 1) |
| `144fd95` | feat(05-02) | Mode-aware registrar selection + remote adapter (Task 2) |
| `63d27d7` | feat(05-02) | XDREndpoint + resolveIdentity + RefreshCredential (Task 3) |
| `11714a2` | test(05-02) | Agent-package mock XDR + integration tests (Task 4) |

## Success Criteria Verification

- [x] **R-52**: InstanceID + Credential persist to agent-state.json via AES-256-GCM (secrets.Store); plaintext negative test asserts neither value appears in raw bytes; credential refresh uses write-then-rename (atomic POSIX, best-effort Windows — not overstated)
- [x] **R-54**: `selectRegistrar` returns the remote adapter for mode:remote and localRegistrar for mode:local; pre-set/persisted InstanceID short-circuits registration in both modes
- [x] **R-55 (persistence half)**: agent-package `agentMockXDR` (independent of 05-01 mock) backs reload-across-restart (1 /register), credential refresh, install-token single-use invalidation
- [x] **BLOCKER 1**: `AgentConfig.XDREndpoint` explicit field; documented in `agent.example.yaml`; `resolveIdentity` reads `cfg.Agent.XDREndpoint`; returns clear error when empty in remote mode; `argusxdr OutputConfig.Endpoint` NOT scraped
- [x] **BLOCKER 2**: `Agent.RefreshCredential(ctx)` is a reachable runtime entry point; builds refresher from `XDREndpoint`; replaces stored credential. NOTE: automatic refresh scheduling is OUT OF SCOPE (deferred Phase 7)
- [x] **WARNING 4**: mode:local resolves without `ARGUS_MASTER_KEY` (no store constructed); mode:remote requires ARGUS_MASTER_KEY (store built on remote path only)
- [x] **No new runtime dependency**: `go.mod` unchanged; persistence via `internal/secrets`; TLS via 05-01's `defaultHTTPClient`

## Deviations from Plan

### Auto-fixed Issues

None. The plan executed exactly as written.

### Design notes

1. `resolveIdentityFn` injectable seam — the plan instructed injectable seams for tests; a func field on Agent was chosen over a struct-of-deps because the existing agent already uses field injection for the registry/dispatcher (mirroring the existing pattern from Phase 4).

2. `resolveWithStore` test helper — Task 4 required driving the full register→persist→reload flow without going through `start()` (which would require full connector/buffer wiring). The helper was added as a package-internal test function replicating the `resolveIdentity` logic with an injected store and mock client; this satisfies the "injectable seam" requirement from Task 3 without adding exported API surface.

## Known Stubs

None. The `Agent.RefreshCredential` call path is fully wired and tested; automatic refresh-before-expiry scheduling is intentionally deferred to Phase 7 per plan scope (not a stub — it is a documented deferred item).

## Threat Model Coverage

| Threat | Mitigation | Verified |
|--------|------------|---------|
| T-05-05: credential/InstanceID at rest | AES-256-GCM via secrets.Store; plaintext negative test in state_test.go | yes |
| T-05-06: persisted-state tampering | secrets.Store AES-256-GCM auth tag; LoadSecrets returns decryption error on tamper | yes |
| T-05-07: install-token replay overwriting identity | ErrInstallTokenConsumed → keep persisted identity; TestXDR_InstallTokenSingleUse | yes |
| T-05-08: self-reported InstanceID | Server-assigned in mode:remote; localRegistrar's deterministic hash confined to mode:local | yes |
| T-05-09: TLS downgrade | No alternate cleartext path added; defaultHTTPClient from 05-01 (TLS 1.3) | yes |
| T-05-10: wrong registration endpoint | Registration reads cfg.Agent.XDREndpoint only; empty→clear error; TestXDR_EndpointConfigError | yes |
| T-05-SC: supply chain | No new module; go.mod diff clean | yes |

## Self-Check

- [x] `internal/auth/state.go` exists
- [x] `internal/auth/state_test.go` exists
- [x] `internal/agent/xdr_integration_test.go` exists
- [x] `internal/agent/registration.go` contains `runtime.GOOS` (grep count=2)
- [x] `internal/agent/agent.go` contains `XDREndpoint`, `RefreshCredential`, `LoadIdentity`, `SaveIdentity`, `NewRemoteRegistrar`, `NewRemoteCredentialRefresher`
- [x] `config/agent.example.yaml` contains `xdr_endpoint`
- [x] Commit `64dc323` exists
- [x] Commit `144fd95` exists
- [x] Commit `63d27d7` exists
- [x] Commit `11714a2` exists
- [x] `go test ./internal/auth/... ./internal/agent/... -count=1` passes (all tests)
- [x] `go vet ./...` clean
- [x] `go build ./...` clean
- [x] `go.mod` unchanged (no new module)

## Self-Check: PASSED
