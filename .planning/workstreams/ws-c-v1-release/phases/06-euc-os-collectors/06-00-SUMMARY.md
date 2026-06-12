---
phase: 06-euc-os-collectors
plan: "00"
subsystem: internal/collector/euc
tags: [euc, netcommon, host-match, port-match, tag-free, stdlib, tdd]
dependency_graph:
  requires: []
  provides:
    - matchHost (internal/collector/euc)
    - matchPort (internal/collector/euc)
    - isLocalInferencePort (internal/collector/euc)
  affects:
    - internal/collector/euc/linux.go (Wave 1 — consumes matchHost/matchPort/isLocalInferencePort)
    - internal/collector/euc/windows.go (Wave 1 — consumes matchHost/matchPort/isLocalInferencePort)
    - internal/collector/euc/darwin.go (Wave 1 — consumes matchHost/matchPort/isLocalInferencePort)
tech_stack:
  added: []
  patterns:
    - TDD (RED/GREEN)
    - Build-tag-free shared helper file (Pitfall 6 mitigation)
    - Label-boundary domain-suffix match (T-06-00b mitigation)
    - Untrusted-input length bounding (V5 / T-06-00a mitigation)
key_files:
  created:
    - internal/collector/euc/netcommon.go
    - internal/collector/euc/netcommon_test.go
  modified: []
decisions:
  - "matchHost uses '.'+endpoint suffix check (not bare HasSuffix) to enforce label-boundary — notopenai.com must not match openai.com"
  - "isLocalInferencePort delegates to matchPort; kept as a separately-named stable symbol so Wave-1 platform files call it directly without shared-file conflict"
  - "maxHostLen = 253 (RFC 1035 DNS name max) as the untrusted-input length bound"
  - "normalizeHost (lowercase + strip trailing dot) extracted as internal helper to keep matchHost readable"
metrics:
  duration_minutes: 8
  completed_date: "2026-06-12"
  tasks_completed: 1
  tasks_total: 1
  files_created: 2
  files_modified: 0
requirements: [R-64, R-65]
---

# Phase 06 Plan 00: Shared Tag-Free EUC Network Match Helpers Summary

**One-liner:** Build-tag-free `matchHost`/`matchPort`/`isLocalInferencePort` helpers in `netcommon.go` with label-boundary suffix matching and untrusted-input length bounding, fully unit-tested via TDD for Wave-1 platform consumption.

## What Was Built

`internal/collector/euc/netcommon.go` — the single, tag-free owner of shared host/port match helpers for the EUC collector. The file carries no `//go:build` directive and imports only `strings` (stdlib), satisfying Pitfall 6 (no platform dep enters any build graph through this file).

`internal/collector/euc/netcommon_test.go` — table-driven unit tests covering all `<behavior>` cases: exact match, case-insensitive exact, domain-suffix match, label-boundary enforcement, empty host, over-length host, trailing-dot normalization, port present/absent, nil/empty slices, and the three local-inference port variants.

### Functions provided

| Function | Signature | Purpose |
|---|---|---|
| `matchHost` | `(host string, endpoints []string) bool` | Case-insensitive exact + label-boundary domain-suffix match; rejects empty or >253-char hosts (V5) |
| `matchPort` | `(port int, ports []int) bool` | Linear scan returning true when port is in the configured list |
| `isLocalInferencePort` | `(port int, ports []int) bool` | Stable-named loopback/local helper; delegates to matchPort; consumed by all three Wave-1 platform files |
| `normalizeHost` (internal) | `(host string) string` | Lowercase + strip trailing dot — keeps matchHost clean |

## Task Execution

### Task 1: Shared tag-free host/port match helpers + unit tests

**TDD cycle:**

- **RED** (`4905680`): Created `netcommon_test.go` with failing tests (compile error — functions undefined). 23 subtests across `TestMatchHost`, `TestMatchPort`, `TestIsLocalInferencePort`.
- **GREEN** (`88cff38`): Created `netcommon.go` with `matchHost`, `matchPort`, `isLocalInferencePort`, `normalizeHost`, `maxHostLen`. All 23 subtests pass.

**Verification results:**

```
go build ./...                                   PASS (host GOOS)
GOOS=linux   go build ./internal/collector/euc/... PASS
GOOS=windows go build ./internal/collector/euc/... PASS
GOOS=darwin  go build ./internal/collector/euc/... PASS
go vet ./internal/collector/euc/...              PASS
go test ./internal/collector/euc/... -count=1    ok (0.425s)
```

## TDD Gate Compliance

- RED gate: `test(06-00)` commit `4905680` — failing test (compile error confirming functions absent).
- GREEN gate: `feat(06-00)` commit `88cff38` — all tests pass.
- REFACTOR gate: not required (implementation was clean on first pass).

## Deviations from Plan

None — plan executed exactly as written.

## Threat Mitigations Applied

| Threat ID | Mitigation Applied |
|---|---|
| T-06-00a | `matchHost` rejects empty strings and strings >253 chars (maxHostLen); normalizes trusted sentinel trailing-dot before comparison; never panics on untrusted input |
| T-06-00b | Suffix check uses `strings.HasSuffix(h, "."+e)` — requires a dot boundary before the endpoint, preventing "notopenai.com" from matching "openai.com" |
| T-06-00c | `netcommon.go` has NO `//go:build` tag and imports only `strings`; verified cross-GOOS via `GOOS=linux/windows/darwin go build ./...` |

## Known Stubs

None — this plan provides complete, production-ready helper functions. No placeholder values or TODO comments.

## Threat Flags

None — `netcommon.go` introduces no new network endpoints, auth paths, file access, or schema changes.

## Self-Check

- `internal/collector/euc/netcommon.go` — FOUND
- `internal/collector/euc/netcommon_test.go` — FOUND
- Commit `4905680` (RED) — FOUND
- Commit `88cff38` (GREEN) — FOUND
- `go build ./...` — PASS
- `go vet ./internal/collector/euc/...` — PASS
- `go test ./internal/collector/euc/... -count=1` — PASS (ok 0.425s)
- `euc.go` unchanged — CONFIRMED (git diff HEAD~2 HEAD -- euc.go: no output)
- `euc_noop.go` unchanged — CONFIRMED (git diff HEAD~2 HEAD -- euc_noop.go: no output)

## Self-Check: PASSED
