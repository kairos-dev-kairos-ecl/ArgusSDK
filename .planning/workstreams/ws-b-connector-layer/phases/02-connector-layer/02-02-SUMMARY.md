---
phase: "02"
plan: "02-02"
subsystem: connector
tags: [tls, config, hot-reload, fsnotify, yaml, security]
dependency_graph:
  requires: []
  provides:
    - connector.TLSClientConfig
    - connector.NewTLSConfig
    - connector.ConnectorConfig
    - connector.ConnectorsFileConfig
    - connector.LoadConnectorsConfig
    - connector.Watcher
    - connector.NewWatcher
  affects:
    - internal/connector/kafka/connector.go   # can call NewTLSConfig instead of manual tls.Config
    - internal/connector/splunk/connector.go  # same
    - internal/connector/syslog              # same
tech_stack:
  added: []
  patterns:
    - TDD (RED/GREEN cycle)
    - fsnotify file-watch hot-reload
    - crypto/tls with enforced minimum version
    - x509 cert pool for custom CA trust
    - mTLS via tls.LoadX509KeyPair
key_files:
  created:
    - internal/connector/tls.go
    - internal/connector/config.go
    - internal/connector/config_test.go
  modified: []
decisions:
  - "TLSClientConfig struct defined in tls.go (package connector) to be the single shared type; all Wave 2 connectors call NewTLSConfig instead of constructing tls.Config directly"
  - "InsecureSkipVerify is left at zero-value false and is never mentioned by name in any assignment — only in comments explaining the deliberate omission"
  - "Watcher.Start validates YAML before calling onChange; malformed YAML is logged to stderr and the previous config remains active (mitigates T-02-03)"
  - "Watcher.Close also called inside Start defer so double-close is safe (fsnotify handles it)"
metrics:
  duration_minutes: 4
  completed_date: "2026-05-28"
  tasks_completed: 2
  files_created: 3
  tests_added: 8
---

# Phase 02 Plan 02-02: TLS Enforcement Helper and Config Hot-Reload Summary

**One-liner:** Shared TLS helper enforcing TLS 1.3 minimum via NewTLSConfig + fsnotify-based ConnectorConfig Watcher for operator hot-reload without restart.

## What Was Built

### Task 1 (TDD): TLS enforcement helper

`internal/connector/tls.go` exports:
- `TLSClientConfig` struct with `CACert`, `CertFile`, `KeyFile` fields
- `NewTLSConfig(cfg TLSClientConfig) (*tls.Config, error)` that:
  - Always sets `MinVersion = tls.VersionTLS13`
  - Never sets `InsecureSkipVerify` (zero value = false, enforced by test)
  - Loads custom CA from PEM path into x509.CertPool (errors if PEM parse fails)
  - Loads mTLS key pair via `tls.LoadX509KeyPair` when both CertFile+KeyFile set

### Task 2: YAML config struct + fsnotify Watcher

`internal/connector/config.go` exports:
- `ConnectorConfig` with YAML tags: `enabled`, `type`, `settings map[string]interface{}`
- `ConnectorsFileConfig` top-level struct with `connectors []ConnectorConfig`
- `LoadConnectorsConfig(path string) (*ConnectorsFileConfig, error)` — os.ReadFile + yaml.Unmarshal
- `Watcher` struct backed by `*fsnotify.Watcher`
- `NewWatcher(path, onChange)` — creates fsnotify watcher, adds path
- `(*Watcher).Start(ctx)` — goroutine selecting on Events/Errors/ctx.Done; validates YAML before invoking onChange
- `(*Watcher).Close()` — closes fsnotify watcher

## Test Results

```
=== RUN   TestNewTLSConfig_DefaultsTLS13   PASS
=== RUN   TestNewTLSConfig_NoInsecureSkipVerify   PASS
=== RUN   TestNewTLSConfig_CustomCA   PASS
=== RUN   TestNewTLSConfig_BadCA   PASS
=== RUN   TestNewTLSConfig_mTLS   PASS
=== RUN   TestLoadConnectorsConfig_Valid   PASS
=== RUN   TestLoadConnectorsConfig_Missing   PASS
=== RUN   TestWatcher_CallsOnChange   PASS
PASS ok internal/connector 0.455s
```

8/8 tests pass. go vet and go build clean.

## TDD Gate Compliance

| Gate | Commit | Status |
|------|--------|--------|
| RED  | 88ea0f4 test(02-02): add failing tests... | Passed — build failed with undefined errors |
| GREEN | ef1d808 feat(02-02): implement TLS enforcement... | Passed — all 8 tests pass |

## Security Verification

```
grep -n 'InsecureSkipVerify\s*=' internal/connector/tls.go
# no output — InsecureSkipVerify is never assigned
```

Threat mitigations delivered:

| Threat ID | Status |
|-----------|--------|
| T-02-03 (Tampering via malformed YAML) | Mitigated — Watcher rejects parse errors before onChange |
| T-02-04 (InsecureSkipVerify elevation) | Mitigated — zero value, never assigned, tested |
| T-02-05 (Untrusted CA cert) | Mitigated — AppendCertsFromPEM false → error returned |

## Deviations from Plan

None — plan executed exactly as written.

The one structural note: both tls.go and config.go were committed in a single `feat(02-02)` commit because the test file (config_test.go) references both `NewTLSConfig` and `LoadConnectorsConfig`/`NewWatcher` in the same package. The package only compiles when both source files exist; splitting the GREEN commit would have required a temporarily incomplete package. This matches the plan's intent (single test file for both tasks).

## Known Stubs

None. All functions are fully implemented with no placeholder returns.

## Threat Flags

No new security surface introduced beyond what is in the plan's threat model.

## Self-Check: PASSED

All files exist on disk and all commits are present on `worktree-agent-a3a6cfc1cc9480c72`:
- internal/connector/tls.go — FOUND
- internal/connector/config.go — FOUND
- internal/connector/config_test.go — FOUND
- .planning/.../02-02-SUMMARY.md — FOUND
- 88ea0f4 (RED test commit) — FOUND
- ef1d808 (GREEN impl commit) — FOUND
