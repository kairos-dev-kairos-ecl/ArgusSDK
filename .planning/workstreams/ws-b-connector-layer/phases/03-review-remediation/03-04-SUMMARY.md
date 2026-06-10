---
plan: 03-04
phase: 3
subsystem: dryrun
tags: [f8, index-alignment, mapper, tdd, phase-exit, sc-8, sc-12]
dependency_graph:
  requires: [03-01, 03-02, 03-03]
  provides: [F8-closed, SC-12-verified, phase-3-complete]
  affects: [internal/dryrun]
tech_stack:
  added: []
  patterns: [per-signal-map-loop, nil-padded-parallel-slice]
key_files:
  created: []
  modified:
    - internal/dryrun/dryrun.go
    - internal/dryrun/dryrun_test.go
decisions:
  - Per-signal mapper.Map loop with nil-padded events slice replaces MapBatch call in dryrun (F8)
  - mapFailSet map[int]bool tracks mapper-failed indexes directly, folded into the Map loop
  - validator.go untouched — validateOCSFBatch already skips nil events (confirmed)
  - recorder.go untouched — writeOCSF already filters nil events before JSON marshal (confirmed)
metrics:
  duration_seconds: 145
  completed_date: "2026-06-10"
  tasks_completed: 3
  files_modified: 2
---

# Phase 3 Plan 04: dryrun Index Alignment + Phase-Exit Gate Summary

**One-liner:** Per-signal `mapper.Map` loop with nil-padded `events` slice fixes SignalID misattribution and inflated OCSFValid counts (F8); full-module `go test ./...` + `go vet ./...` gate passes clean (SC-12).

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Failing test — invalid layer mid-batch attribution (F8, RED) | 3424d83 | internal/dryrun/dryrun_test.go |
| 2 | Per-signal Map loop with parallel events slice (F8, GREEN) | 5b9578d | internal/dryrun/dryrun.go |
| 3 | Phase-exit gate — full-module test + vet + build | (no commit — verification only) | — |

## Implementation Details

### F8 Root Cause

`mapper.MapBatch` compacts both return slices: failed signals are skipped in `events` (not nil-padded), and `errs` is a separate compacted slice with no index correspondence. `dryrun.go` paired these slices with `signals` by index:

- `mapErrs[0]` (the first error) was attributed to `signals[0]` — wrong when any signal earlier in the batch failed
- The compacted `events` slice passed to `validateOCSFBatch` was shorter than `signals`, mispairing events with signals
- OCSFValid count walked the compacted events slice with shifted indexes

### Fix Applied

Replaced the `mapper.MapBatch` block with a per-signal loop:

```go
events := make([]*ocsf.Event, len(signals))  // nil-padded, parallel to signals
mapFailSet := make(map[int]bool, len(signals))
for i, s := range signals {
    ev, err := mapper.Map(s)
    if err != nil {
        report.Errors = append(report.Errors, ValidationError{
            Index:    i,
            SignalID: signals[i].SignalID,
            Stage:    "ocsf_schema",
            Field:    "mapper",
            Message:  err.Error(),
        })
        mapFailSet[i] = true
        continue  // events[i] remains nil
    }
    events[i] = ev
}
```

- `validateOCSFBatch(events, signals)` receives a len-aligned slice and already skips nil entries (no change needed to validator.go)
- `writeOCSF` already filters nil events before JSON marshal (no change needed to recorder.go)
- OCSFValid count loop is now index-correct since `events` and `signals` have identical length

### TDD Gate Compliance

- RED commit: `3424d83` — `test(03-04): failing index-alignment test for dryrun mapper errors (F8)`
- GREEN commit: `5b9578d` — `fix(03-04): dryrun per-signal Map with index-aligned events (F8)`
- REFACTOR: none needed

## Phase-Exit Gate Results (SC-12, Task 3)

### `go test ./... -count=1 -timeout 300s`

```
?   github.com/kairos-dev-kairos-ecl/ArgusSDK/cmd/argus-agent        [no test files]
?   github.com/kairos-dev-kairos-ecl/ArgusSDK/gen/go/sdk/v1          [no test files]
?   github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/agent         [no test files]
?   github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/auth          [no test files]
ok  github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/buffer        2.037s
?   github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/collector     [no test files]
?   github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/collector/euc [no test files]
?   github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/collector/llm [no test files]
ok  github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector     1.788s
?   github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector/argusxdr [no test files]
ok  github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector/elastic  0.327s
ok  github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector/kafka    0.337s
ok  github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector/splunk   0.339s
?   github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector/syslog   [no test files]
ok  github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/dryrun             0.556s
ok  github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/ocsf               0.306s
ok  github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/resilience         0.916s
ok  github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/secrets            1.038s
ok  github.com/kairos-dev-kairos-ecl/ArgusSDK/pkg/signal                  0.305s
```

Result: **ALL PASS** (no -race flag; CGO unavailable on this machine)

### `go vet ./...`

Result: **CLEAN** (no output)

### `go build ./...`

Result: **CLEAN** (no output)

## Finding Closure Summary (All 16 Fixed Findings)

| Finding | Severity | Plan | Status |
|---------|----------|------|--------|
| F1 — WAL consumed-marker corrupts stream | Critical | 03-01 | Closed: 39da508 |
| F2 — Data race on b.seg in Buffer.Write | Critical | 03-01 | Closed: 39da508 |
| F3 — Segment rotation strands records | Critical | 03-01 | Closed: 39da508 |
| F4 — Elastic NDJSON injection | Critical | 03-03 | Closed: 73bde20 area |
| F5 — agent.stop panics on nil drain | Critical | 03-01 | Closed: 39da508 |
| F6 — Failed delivery invisible | High | 03-02 | Closed: 39c0b22 |
| F7 — Batch truncation silently drops | High | 03-02 | Closed: 39c0b22 |
| F8 — dryrun index misalignment | High | 03-04 | Closed: 5b9578d |
| F9 — Config Watcher misses renames | High | 03-03 | Closed: eacd271 area |
| F10 — CircuitBreaker TOCTOU | Medium | 03-03 | Closed: 21177d6 |
| F11 — GetSecret decrypts on every call | Medium | 03-03 | Closed: 2eb99d2 |
| F12 — Kafka RequiredAcks=0 unreachable | Medium | 03-03 | Closed: a660c6b |
| F13 — activity_id=99 without ActivityName | Low | 03-03 | Closed: 73bde20 |
| F14 — Drain backoff inside streamRecords | Medium | 03-01 | Closed: 39da508 |
| F15 — secrets temp file perms window | Low | 03-03 | Closed: 2eb99d2 |
| F16 — Elastic raw APIKey retained | Low | 03-03 | Closed: ec26039 area |

## F17 Deferral (Documented)

**F17 — mapper LoggedTime non-determinism** (`mapper.go:304`): `time.Now()` in `Map()` is accepted/deferred. Clock injection is an API-breaking change touching all connectors. A `// NOTE(F17): ...` comment was added at the call site in plan 03-03 (commit 73bde20). No behavioral change. This is the only deferred finding.

## Deviations from Plan

None — plan executed exactly as written. `validator.go` and `recorder.go` did not require changes (both already handled nil events correctly).

## Known Stubs

None.

## Threat Flags

None — no new network endpoints, auth paths, file access patterns, or schema changes introduced.

## Self-Check: PASSED

- [x] `internal/dryrun/dryrun_test.go` exists with `TestRun_MapperErrorMidBatch_IndexAlignment`
- [x] `internal/dryrun/dryrun.go` uses `mapper.Map` loop (MapBatch not called)
- [x] Commit `3424d83` (RED test) exists
- [x] Commit `5b9578d` (GREEN fix) exists
- [x] All 13 dryrun tests pass
- [x] Full-module `go test ./...` passes
- [x] `go vet ./...` clean
- [x] `go build ./...` clean
