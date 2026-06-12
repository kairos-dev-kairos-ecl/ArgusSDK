---
phase: 05-xdr-registration
plan: "01"
subsystem: auth
tags: [auth, xdr, registration, tls, http-client, mock, unit-test]
dependency_graph:
  requires: [internal/auth/auth.go, internal/connector/tls.go]
  provides: [internal/auth/remote.go, internal/auth/mockxdr_test.go, internal/auth/remote_test.go]
  affects: [internal/agent (plan 05-02 consumes constructors)]
tech_stack:
  added: []
  patterns: [stdlib net/http, httptest in-process mock, TLS 1.3 via connector.NewTLSConfig, typed sentinel errors]
key_files:
  created:
    - internal/auth/remote.go
    - internal/auth/mockxdr_test.go
    - internal/auth/remote_test.go
  modified: []
decisions:
  - "Wire structs (registerRequest/Response, refreshRequest/Response) defined in remote.go with json tags because auth.go types lack them — avoids modifying the contract file"
  - "ErrInstallTokenConsumed maps both 409 (conflict) and 401 (unauthorized) on the register path — covers token-already-used and token-invalidated-by-server cases"
  - "defaultHTTPClient panics on NewTLSConfig failure (empty config, system roots) — this path is only reachable on a broken runtime, not a user error"
  - "mockXDR.ForceStatus knob drives non-2xx paths; Server.Close() drives transport-error paths — both are needed by Task 2 tests"
metrics:
  duration_seconds: 262
  completed_date: "2026-06-12"
  tasks_completed: 2
  tasks_total: 2
  files_created: 3
  files_modified: 0
  commits: 2
---

# Phase 5 Plan 01: XDR Auth HTTP Client + Mock Harness Summary

**One-liner:** stdlib net/http Registrar + CredentialRefresher over TLS 1.3 with typed errors and an in-process httptest mock XDR for Docker-free unit testing.

## What Was Built

### Task 1 — Mock XDR httptest server + typed errors

`internal/auth/remote.go` declares the typed errors and wire structs the production client surfaces:

- `ErrInstallTokenConsumed` — sentinel for a consumed/invalid install token (HTTP 409 or 401 on register path)
- `httpStatusError{Path, Status}` — non-2xx responses that are not otherwise mapped
- `registerRequest/registerResponse` and `refreshRequest/refreshResponse` — private wire structs with `json:""` tags (auth.go types lack tags)

`internal/auth/mockxdr_test.go` provides `newMockXDR(t)` — an `httptest.NewTLSServer` backed helper serving:

- `POST /api/v1/sdk/register` — asserts GroupID+InstallToken non-empty (400), tracks consumed tokens (409 on reuse), returns deterministic `instance-<GroupID>` + `cred-initial` on a fresh token
- `POST /api/v1/sdk/credential/refresh` — asserts all three fields non-empty, returns 401 for `InstanceID=="unknown"`, returns `<old>-rotated` otherwise
- `ForceStatus` knob for non-2xx path testing; `Server.Close()` for transport-error testing

`TestMockXDR_Smoke` verifies mock behavior raw (200 fresh / 409 reused / 401 unknown / 400 missing fields) before the production client is introduced.

### Task 2 — remoteRegistrar + remoteCredentialRefresher

`internal/auth/remote.go` implements:

- `NewRemoteRegistrar(baseURL, httpClient) Registrar` — returns the exported `auth.Registrar` interface; nil client gets `defaultHTTPClient()` (TLS 1.3 via `connector.NewTLSConfig`, 30s timeout)
- `NewRemoteCredentialRefresher(baseURL, httpClient) CredentialRefresher` — same nil-client behaviour
- `Register(ctx, req)` — marshals `RegistrationRequest` via private wire struct, POSTs to `/api/v1/sdk/register`, maps 409/401 → `ErrInstallTokenConsumed` (nil response), other non-2xx → `httpStatusError` (nil response), transport error → wrapped error (nil response)
- `Refresh(ctx, id)` — POSTs `refreshRequest`, returns new credential on 2xx, typed errors + `""` otherwise
- `postJSON(ctx, client, url, body, out)` — package-private helper managing marshal/request/response/decode lifecycle
- `defaultHTTPClient()` — `connector.NewTLSConfig(TLSClientConfig{})` sets TLS 1.3 floor; `InsecureSkipVerify` is never set (enforced by `connector.NewTLSConfig`)

`internal/auth/remote_test.go` covers:

| Test | Path | Assertion |
|------|------|-----------|
| `TestRemoteRegistrar_HappyPath` | Register 200 | InstanceID + Credential equal mock deterministic values |
| `TestRemoteRegistrar_TokenReuse_ErrInstallTokenConsumed` | Register 409 | `errors.Is(err, ErrInstallTokenConsumed)` AND `resp == nil` |
| `TestRemoteRegistrar_Non2xx_HttpStatusError` | Register 500 | `errors.As(err, *httpStatusError)` with status 500 |
| `TestRemoteRegistrar_TransportError_NilResponse` | closed server | non-nil error, nil response, no panic |
| `TestRemoteCredentialRefresher_HappyPath` | Refresh 200 | new credential != input credential |
| `TestRemoteCredentialRefresher_UnknownInstance_TypedError` | Refresh 401 | `*httpStatusError` + `""` |
| `TestRemoteCredentialRefresher_TransportError` | closed server | non-nil error, `""`, no panic |

Compile-time assertions: `var _ Registrar = NewRemoteRegistrar("", nil)` and `var _ CredentialRefresher = NewRemoteCredentialRefresher("", nil)`.

## Commits

| Hash | Type | Description |
|------|------|-------------|
| `647df12` | test(05-01) | Mock XDR httptest server + typed errors (Task 1 RED+GREEN) |
| `82d7a69` | feat(05-01) | remoteRegistrar + remoteCredentialRefresher (Task 2) |

## Success Criteria Verification

- [x] **R-51**: `remoteRegistrar.Register` POSTs `RegistrationRequest` to `/api/v1/sdk/register` over a TLS 1.3 net/http client; non-2xx/transport errors are typed.
- [x] **R-53**: `remoteCredentialRefresher.Refresh` POSTs to `/api/v1/sdk/credential/refresh` and returns the rotated credential.
- [x] **R-55 (auth-layer half)**: in-process httptest mock XDR backs unit tests for register happy-path, credential refresh, install-token single-use invalidation, and failure paths.
- [x] **WARNING 5**: `NewRemoteRegistrar` returns `Registrar`; `NewRemoteCredentialRefresher` returns `CredentialRefresher` — compile-time asserted.
- [x] **No new module**: stdlib `net/http` only; `go.mod` unchanged; TLS 1.3 via `connector.NewTLSConfig`; `InsecureSkipVerify` never set.

## Threat Model Coverage

| Threat | Mitigation | Verified |
|--------|------------|---------|
| T-05-01: TLS downgrade | `connector.NewTLSConfig` guarantees MinVersion TLS 1.3; grep gate: 0 occurrences of `InsecureSkipVerify` in remote.go | yes |
| T-05-02: install-token theft & reuse | Mock returns 409 on reuse; client maps to `ErrInstallTokenConsumed` + nil response | yes |
| T-05-03: rogue XDR (certificate spoofing) | TLS cert validation via system roots (no `InsecureSkipVerify`); test uses mock's own cert | yes |
| T-05-04: silent failure on non-2xx / transport error | `httpStatusError` for non-2xx; wrapped error for transport; always returns `(nil/"", err)` — no partial state | yes |
| T-05-SC: supply chain | No new module; stdlib only; verified by go.mod diff | yes |

## Deviations from Plan

None — plan executed exactly as written. Wire struct approach (private structs with json tags rather than modifying auth.go) matched the plan's explicit fallback guidance. Both tasks completed in a single GREEN pass as `remote.go` was written once with full implementation (Task 1's requirement to declare typed errors + wire structs and Task 2's full implementation were naturally combined in one coherent file).

## Known Stubs

None.

## Self-Check

- [x] `internal/auth/remote.go` exists in worktree
- [x] `internal/auth/mockxdr_test.go` exists in worktree
- [x] `internal/auth/remote_test.go` exists in worktree
- [x] Commit `647df12` exists
- [x] Commit `82d7a69` exists
- [x] `go test ./internal/auth/... -count=1` passes (7 tests)
- [x] `go vet ./internal/auth/...` clean
- [x] `go build ./...` clean
- [x] `go.mod` unchanged (no new module)
- [x] `InsecureSkipVerify` not in `remote.go` source lines (grep count = 0)

## Self-Check: PASSED
