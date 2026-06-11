---
phase: 04-agent-wiring
plan: "01"
subsystem: connector-factory
tags: [connector, factory, ulid, batchid, mapstructure, tdd]
dependency_graph:
  requires: []
  provides:
    - internal/connector/ulid.go (NewBatchID)
    - internal/connector/factory/factory.go (Build, FactoryInput)
  affects:
    - internal/agent/agent.go (will use factory.Build in start() wiring — plan 04-05)
tech_stack:
  added:
    - github.com/mitchellh/mapstructure v1.5.0 (promoted from indirect to direct)
  patterns:
    - TDD (RED → GREEN per task)
    - Sub-package factory to break import cycle (connector/factory ↔ connector sub-packages)
key_files:
  created:
    - internal/connector/ulid.go
    - internal/connector/ulid_test.go
    - internal/connector/factory/factory.go
    - internal/connector/factory/factory_test.go
  modified:
    - go.mod (mapstructure promoted to direct require)
    - go.sum (updated)
decisions:
  - "Factory placed in internal/connector/factory (not internal/connector) to break the connector ↔ kafka/splunk/elastic/syslog/argusxdr import cycle"
  - "NewBatchID uses 6-byte timestamp + 8 random bytes + 2-byte monotonic sequence counter for within-ms ordering guarantee"
  - "FactoryInput defined in internal/connector/factory so agent can import factory without importing connector having to import agent"
  - "mapstructure WeaklyTypedInput=true for YAML/env config pipeline compatibility"
metrics:
  duration: "6m13s"
  completed_date: "2026-06-11"
  tasks: 2
  files: 6
---

# Phase 4 Plan 01: Connector Factory + BatchID Generator Summary

**One-liner:** Crypto/rand BatchID generator with monotonic within-ms ordering, and a five-type connector factory (kafka/splunk_hec/elastic/syslog/argusxdr) keyed by Type string using mapstructure Extra decoding.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | crypto/rand BatchID generator | e762f7f | ulid.go, ulid_test.go |
| 2 | connector factory keyed by Type | c1fb655 | factory/factory.go, factory/factory_test.go, go.mod |

## Verification Results

```
go build ./...         PASS
go vet ./...           PASS
go test ./internal/connector/ -count=1          PASS (ok, 1.839s)
go test ./internal/connector/factory/ -count=1  PASS (ok, 0.261s)
```

All 5 connector types build correctly. Unknown type returns descriptive error.
No agent import in factory package (import cycle prohibition upheld).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Factory moved from internal/connector to internal/connector/factory to break import cycle**
- **Found during:** Task 2 (GREEN phase)
- **Issue:** The plan specified `files_modified: internal/connector/factory.go` (in the connector package), but all sub-connectors (kafka, splunk, elastic, syslog, argusxdr) already import `internal/connector` for SignalBatch/Connector types. Placing the factory in the same package would create an internal/connector ↔ kafka circular import cycle.
- **Fix:** Created `internal/connector/factory/` as a separate package. This cleanly satisfies locked decision 1 (no agent↔connector cycle) while also avoiding the connector↔sub-connector cycle. The agent imports `internal/connector/factory` (not `internal/connector/factory.go` inside connector package).
- **Files modified:** internal/connector/factory/factory.go, internal/connector/factory/factory_test.go
- **Impact:** None — the factory API (FactoryInput, Build) is identical to what the plan specified. Only the Go package path changed.

**2. [Rule 1 - Bug] NewBatchID uses monotonic sequence counter for within-millisecond ordering**
- **Found during:** Task 1 (GREEN verification)
- **Issue:** Initial implementation used fresh random bytes per call; within the same millisecond, two IDs had identical timestamp prefixes and the random suffix had no ordering guarantee, causing the time-ordered test to fail.
- **Fix:** Added a mutex-protected state struct tracking lastMS, lastRnd (8 random bytes, refreshed per ms), and seq (16-bit counter, incremented within ms, reset on new ms). This guarantees lexicographic ordering for any two consecutive calls.
- **Files modified:** internal/connector/ulid.go

## Known Stubs

None. Both artifacts (ulid.go, factory/factory.go) are fully implemented.

## Threat Surface Scan

No new network endpoints, auth paths, or file access patterns introduced.
factory/factory.go handles credentials (Auth map) — T-04-02 mitigation applied: Auth/Extra values are never logged; only Name/Type are passed to the logger. Verified by code review.

## Self-Check: PASSED

Files exist:
- [x] internal/connector/ulid.go
- [x] internal/connector/ulid_test.go
- [x] internal/connector/factory/factory.go
- [x] internal/connector/factory/factory_test.go

Commits exist:
- [x] e762f7f (feat(04-01): add crypto/rand BatchID generator)
- [x] c1fb655 (feat(04-01): add connector factory keyed by Type)
