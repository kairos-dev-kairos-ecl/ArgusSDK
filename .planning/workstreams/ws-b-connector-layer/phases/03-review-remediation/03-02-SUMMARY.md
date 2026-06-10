---
phase: "03"
plan: "03-02"
subsystem: connector
tags: [delivery-contract, chunking, dispatcher-counters, f6, f7, tdd]
dependency_graph:
  requires: [03-01]
  provides: [chunked-splunk-send, chunked-elastic-send, dispatcher-stats]
  affects: [internal/connector/splunk, internal/connector/elastic, internal/connector]
tech_stack:
  added: []
  patterns: [sendChunk-helper, atomic-counters, sequential-chunking, abort-on-first-failure]
key_files:
  created:
    - internal/connector/dispatcher_test.go
  modified:
    - internal/connector/splunk/connector.go
    - internal/connector/splunk/connector_test.go
    - internal/connector/elastic/connector.go
    - internal/connector/elastic/connector_test.go
    - internal/connector/connector.go
decisions:
  - Chunking is ceil(n/limit) sequential requests; first failed chunk aborts and names chunk i/total in error
  - sendChunk returns nil for all-unmappable chunk (skip POST, not a failure)
  - Dispatcher accepted incremented only on successful queue insert, not on rejection
  - Stats() returns map[string]uint64 for stable observability accessor
  - TestElasticConnector_SendBulkError and TestSplunkConnector_SendSplunkError updated to assert non-nil error per F6 contract (old tests validated broken behavior)
  - -race flag skipped on Windows due to missing gcc/cgo (system constraint, not code issue)
metrics:
  duration: "~30 minutes"
  completed: "2026-06-10"
  tasks_completed: 3
  tasks_total: 3
  files_modified: 5
  files_created: 1
---

# Phase 3 Plan 02: Delivery Contract + Chunking + Dispatcher Counters Summary

**One-liner:** Chunked HEC/bulk POSTs with abort-on-first-failure, failed-ack implies non-nil error in splunk and elastic, dispatcher accepted/delivered/failed counters wired with Stats() accessor.

## Tasks Completed

| Task | Description | Commit | Status |
|------|-------------|--------|--------|
| 1 RED | Failing splunk contract+chunking tests (F6, F7) | ae137b9 | done |
| 1 GREEN | Splunk chunked Send with failed=>error contract | a67dc27 | done |
| 2 RED | Failing elastic contract+chunking tests (F6, F7) | def3fb2 | done |
| 2 GREEN | Elastic chunked Send with failed=>error contract | 3c04bb7 | done |
| 3 RED | Failing dispatcher counter tests (F6) | 6acb91b | done |
| 3 GREEN | Wire dispatcher accepted/delivered/failed counters + Stats | 39c0b22 | done |

## What Was Built

### Splunk: chunked Send + F6 contract (internal/connector/splunk/connector.go)

Removed the truncation block (`signals = signals[:limit]`). `Send` now chunks `batch.Signals` into `ceil(n/MaxBatchEvents)` slices and calls `sendChunk` sequentially. `sendChunk` builds the NDJSON HEC payload (existing record logic preserved) and returns `nil` only on HTTP 2xx AND `hecResp.Code == 0`. Added an explicit non-2xx HTTP status check. All failure paths return descriptive non-nil errors (wrapping existing detail strings). On first `sendChunk` error, `Send` stops and returns a failed ack naming the chunk (`chunk i/total`) with a non-nil error. All-unmappable chunks skip the POST (nil return, not a failure).

### Elastic: chunked Send + F6 contract (internal/connector/elastic/connector.go)

Same structure as splunk. Removed truncation. `sendChunk` builds the NDJSON `/_bulk` payload. Non-2xx, body-read, parse, and `errors:true` paths all return non-nil errors. Action line construction kept unchanged (F4 fix is plan 03-03's job). All-unmappable chunks skip the POST.

### Dispatcher counters + Stats() (internal/connector/connector.go)

Added `sync/atomic` import. `Enqueue`: `atomic.AddUint64(&d.accepted, 1)` on successful queue insert only (not on full-queue or shutdown errors). `process`: `atomic.AddUint64(&d.delivered, 1)` on Send success; `atomic.AddUint64(&d.failed, 1)` on Send error (error log preserved). `Stats()` returns `map[string]uint64` with `accepted`, `delivered`, `failed`, `dropped` via `atomic.LoadUint64`.

## Tests Added/Modified

| File | Tests | Coverage |
|------|-------|----------|
| splunk/connector_test.go | TestSend_FailedAckImpliesError, TestSend_Non200ImpliesError, TestSend_ChunksBatch, TestSend_ChunkFailureAborts, TestSend_AllUnmappableStillDelivered | F6+F7 |
| elastic/connector_test.go | TestSend_BulkErrorsImpliesError, TestSend_Non2xxImpliesError, TestSend_ChunksBatch, TestSend_ChunkFailureAborts, TestSend_AllUnmappableStillDelivered | F6+F7 |
| connector/dispatcher_test.go | TestDispatcher_CountersWired, TestDispatcher_AcceptedNotIncrementedOnFullQueue | F6 counters |

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Updated TestElasticConnector_SendBulkError and TestSplunkConnector_SendSplunkError**
- **Found during:** Task 2 GREEN phase
- **Issue:** Both tests were validating the old broken behavior (nil error alongside failed ack). After implementing the F6 contract, these tests failed with the correct message.
- **Fix:** Updated both tests to assert `err != nil` per the locked F6 contract. This is a test correctness fix, not a scope change.
- **Files modified:** elastic/connector_test.go, splunk/connector_test.go
- **Commit:** 3c04bb7

**2. [Rule 1 - Bug] Revised TestDispatcher_AcceptedNotIncrementedOnFullQueue**
- **Found during:** Task 3 GREEN phase
- **Issue:** Initial test with QueueCapacity=1 was flaky — the single worker dequeued the job before the second enqueue attempt, leaving the queue non-full. Revised to use QueueCapacity=2 with a blocking connector to reliably fill the queue.
- **Fix:** Increased capacity to 2, fill 2 slots after worker takes job 1 (blocks in Send), then verify the failing enqueue does not increment accepted.
- **Files modified:** dispatcher_test.go
- **Commit:** 39c0b22

## Known Stubs

None — no placeholder data or TODO-wired endpoints in this plan's output.

## Threat Flags

| Flag | File | Description |
|------|------|-------------|
| threat_flag: counters-not-exposed | internal/connector/connector.go | Stats() exposes lock-free counters; no auth gate in front (by design — internal observability only, no network endpoint created) |

No new network endpoints created. Stats() is an in-process method only.

## TDD Gate Compliance

- RED gate: `test(03-02)` commits exist for all 3 tasks (ae137b9, def3fb2, 6acb91b)
- GREEN gate: `feat(03-02)` commits follow each RED (a67dc27, 3c04bb7, 39c0b22)
- REFACTOR: no cleanup needed

## Verification Results

```
go test ./internal/connector/... -count=1 -timeout 120s
ok  github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector        0.558s
ok  github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector/elastic 0.356s
ok  github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector/kafka  0.327s
ok  github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector/splunk 0.349s

go vet ./internal/connector/...
(no output — clean)

Note: -race flag not available on this Windows machine (CGO_ENABLED requires gcc).
This matches pre-existing constraint in 03-CONTEXT.md (Windows file locking notes).
The code uses sync/atomic correctly for all counter operations.
```

## Self-Check: PASSED

- internal/connector/splunk/connector.go: FOUND
- internal/connector/elastic/connector.go: FOUND
- internal/connector/connector.go (Stats method): FOUND
- internal/connector/splunk/connector_test.go (min_lines 120+): FOUND (461 lines)
- internal/connector/elastic/connector_test.go (min_lines 120+): FOUND (706 lines)
- All commits verified in git log: ae137b9, a67dc27, def3fb2, 3c04bb7, 6acb91b, 39c0b22
