---
phase: "02"
plan: "06"
subsystem: buffer
tags: [wal, dead-letter, backoff, gob, atomic]
dependency_graph:
  requires: [internal/connector, pkg/signal]
  provides: [internal/buffer]
  affects: []
tech_stack:
  added: [encoding/gob, sync/atomic, math/rand]
  patterns: [length-prefixed WAL, exponential backoff with jitter, 0xFF consumed marker]
key_files:
  created:
    - internal/buffer/wal.go
    - internal/buffer/buffer_test.go
    - internal/buffer/wal_test.go
  modified:
    - internal/buffer/buffer.go
decisions:
  - "Used encoding/gob (not proto) — SignalBatch is an internal Go struct with no proto methods"
  - "0xFF consumed marker at record offset — simple, crash-safe (at-least-once delivery)"
  - "Exponential backoff with shift cap at 20 to prevent overflow"
  - "Segment files 0600 permissions, dir created with 0700"
  - "WAL-level tests use package buffer (white-box); Buffer-level tests use package buffer_test (black-box)"
metrics:
  duration: "~15 minutes"
  completed: "2026-05-28"
  tasks: 3
  files: 4
---

# Plan 02-06 — Dead-letter WAL Buffer — SUMMARY

## Status: COMPLETE

## One-liner
WAL-backed dead-letter buffer using length-prefixed gob records with 0xFF consumed markers, exponential backoff drain loop, and atomic counters for stats.

## What Was Done

- **Created `internal/buffer/wal.go`**: WAL segment I/O — `openSegment`, `appendRecord` (gob encode + 4-byte big-endian length prefix + Sync), `streamRecords` (skips 0xFF-marked records), `markConsumed` (overwrites first byte with 0xFF), `segmentPath`, `segmentSizeBytes`
- **Completed `internal/buffer/buffer.go`**: Full implementation of `Write` (lazy segment creation, dir mkdirall, rotation on size limit), `drainOnce` (exponential backoff with jitter on drain failure, markConsumed on success), `drainLoop` (ticker-driven), `Flush` (synchronous drain-until-done), `Close` (atomic closed flag, file sync+close), `Stats` (atomic counters for batches/bytes/dropped)
- **Created `internal/buffer/buffer_test.go`**: 6 black-box tests — WriteRead, Stats counter, DrainCallsDrainFn, DrainBackoff (gap assertion), Flush, Close-then-Write error
- **Created `internal/buffer/wal_test.go`**: 2 white-box tests — AppendReadRoundTrip (BatchID round-trip), MarkConsumed (record skipped after marking)

## Verification

- `go build ./internal/buffer/...` — PASS (no output)
- `go test ./internal/buffer/... -v -count=1 -timeout 60s` — PASS (8/8 tests)
- `go test ./internal/connector/... ./internal/ocsf/... -count=1 -timeout 60s` — PASS (SC-9)
- `go vet ./internal/buffer/...` — clean (no output)

## Key Decisions

1. **encoding/gob over proto** — `SignalBatch` is a pure Go struct with no protobuf registration; gob handles `time.Time` natively.
2. **0xFF consumed marker** — overwrites the first byte of the 4-byte length header. Crash-safe: at-least-once delivery guaranteed since the record is only marked after successful drain callback.
3. **Backoff shift capped at 20** — prevents `1 << shift` integer overflow; effective max is `BackoffBase * 1048576`, clamped further by `BackoffMax`.
4. **Permissions** — segment files at 0600, directory at 0700 (agent-local data, not world-readable).
5. **White-box vs black-box split** — `wal_test.go` uses `package buffer` to access unexported segment types; `buffer_test.go` uses `package buffer_test` for the public API. Windows requires explicit file close before TempDir cleanup — tests close segment handles explicitly.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Windows file locking in WAL tests**
- **Found during:** Task 3 (test execution)
- **Issue:** Windows holds exclusive lock on open files; `t.TempDir()` cleanup failed with "file is being used by another process" because `walSegment.file` was not closed before `streamRecords` (which opens the file independently) or before test cleanup.
- **Fix:** Added explicit `seg.file.Close()` after `appendRecord` in WAL-level tests before reading/marking.
- **Files modified:** `internal/buffer/wal_test.go`

**2. [Rule 2 - Missing] WAL-level tests needed white-box access to unexported types**
- **Found during:** Task 3 (design)
- **Issue:** External test package `buffer_test` cannot reference `*walSegment` (unexported). Plan called for a single test file in `buffer_test` package.
- **Fix:** Split into two test files — `wal_test.go` (package `buffer`, white-box) and `buffer_test.go` (package `buffer_test`, black-box). Removed `export_test.go` approach as it cannot re-export unexported types.

## Known Stubs

None. All method bodies are implemented.

## Threat Flags

None. `internal/buffer` writes to agent-local disk only (0600/0700 permissions), no network surface, no auth paths.

## Self-Check

Files exist:
- [x] `internal/buffer/wal.go`
- [x] `internal/buffer/buffer.go`
- [x] `internal/buffer/buffer_test.go`
- [x] `internal/buffer/wal_test.go`

Commits exist:
- [x] `8a7a2ed` — feat(02-06): WAL dead-letter buffer

## Self-Check: PASSED
