---
phase: 03-review-remediation
verified: 2026-06-10T12:00:00Z
status: passed
score: 12/12 must-haves verified
overrides_applied: 0
deferred:
  - truth: "F17 (LoggedTime non-determinism) is documented as accepted/deferred — code comment present, NOT fixed"
    addressed_in: "Accepted by locked decision 5 on 2026-06-10 — no future phase needed"
    evidence: "NOTE(F17) comment present at mapper.go LoggedTime line; behavior unchanged per locked decision 5"
---

# Phase 3: Review Remediation Verification Report

**Phase Goal:** Fix all 17 findings from the 2026-06-10 full-codebase code/security review — buffer hardening, delivery contract unification, injection fixes, dryrun index alignment.
**Verified:** 2026-06-10T12:00:00Z
**Status:** PASSED
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| SC-1 | WAL record format is [1-byte status][4-byte BE length][payload]; markConsumed touches only the status byte | VERIFIED | `wal.go:1-16` package doc; `headerSize=5`; `appendRecord` writes `hdr[0]=statusLive` then 4-byte BE length; `markConsumedAt` writes only `[]byte{statusConsumed}` at offset; length bytes never touched |
| SC-2 | Buffer.Write is race-free — concurrent-writers test exists; suite passes | VERIFIED | `buffer.go:161-168`: `segMu.Lock()` at entry of Write, `defer segMu.Unlock()` covers open+append+rotate+enforceTotal; `TestBuffer_ConcurrentWriters` exists in buffer_test.go; suite passes per SC-12 evidence |
| SC-3 | Drain enumerates ALL wal-*.seg segments oldest-first; fully-consumed segments deleted; MaxSizeMB drops oldest + increments dropped_batches | VERIFIED | `drainOnce` calls `listSegments(cfg.Dir)` (globs wal-*.seg, sorts by (unixSec,seq)); `countLiveRecords` + `os.Remove` on fully-consumed non-active segments; `enforceTotal` calls `countLiveRecords` then `countDropped.Add(int64(live))` on eviction |
| SC-4 | Buffer.Flush returns error (not panic) on nil drain; agent.stop no longer passes nil | VERIFIED | `buffer.go:375-376`: `if drain == nil { return fmt.Errorf("buffer: Flush requires a non-nil drain func") }`; `Start` same guard at line 250-252; `agent.go:147-151`: nil Flush call replaced by TODO comment, only `a.buffer.Close()` called |
| SC-5 | Elastic /_bulk action line built via json.Marshal — no string concatenation of cfg.Index | VERIFIED | `elastic/connector.go:274-279`: `actionObj := map[string]any{"index": map[string]string{"_index": c.cfg.Index}}; actionBytes, err := json.Marshal(actionObj)`; grep confirms no string concat of cfg.Index remains anywhere in the file |
| SC-6 | Delivery contract unified: failed delivery returns non-nil error in kafka, splunk AND elastic; Dispatcher increments delivered/failed counters from results | VERIFIED | splunk `sendChunk` returns non-nil on all failure paths (non-2xx, body-read, unparseable, HEC code!=0); elastic `sendChunk` same; `connector.go:286-289`: `atomic.AddUint64(&d.failed,1)` on err!=nil; `atomic.AddUint64(&d.delivered,1)` on err==nil; `Stats()` at line 295-302 exposes all counters |
| SC-7 | Splunk/Elastic split oversized batches into multiple sequential requests — no silent truncation | VERIFIED | splunk `Send`: `chunkSize=limit` when `limit>0 && limit<len(signals)`; `total=(len+chunkSize-1)/chunkSize`; loop with abort on first `sendChunk` error; elastic identical pattern; no truncation block present |
| SC-8 | dryrun.Run uses per-signal mapper.Map with index alignment — mapper errors attribute the correct SignalID | VERIFIED | `dryrun.go:149-166`: `events := make([]*ocsf.Event, len(signals))`; `for i,s := range signals { ev,err := mapper.Map(s); ...ValidationError{Index:i, SignalID:signals[i].SignalID,...}; events[i]=ev }`; `MapBatch` does not appear as a call anywhere in dryrun.go |
| SC-9 | Config Watcher survives atomic-rename file replacement and logs via zap | VERIFIED | `config.go:97-98`: `dir := filepath.Dir(absPath); fw.Add(dir)` — watches parent directory; `config.go:129`: `filepath.Base(event.Name) != w.target` filter; handles Write|Create|Rename|Remove at line 135-136; `w.logger.Warn(...)` at line 141; no `fmt.Fprintf(os.Stderr)` anywhere in config.go |
| SC-10 | CircuitBreaker.Call holds the mutex for the full state-transition decision (no TOCTOU) | VERIFIED | `circuit_breaker.go:65-79`: single `cb.mu.Lock()` at entry; state check, `time.Since(cb.lastFailureTime)`, and Open->HalfOpen transition all under that lock; `cb.mu.Unlock()` only called before `operation()` (line 79) — lastFailureTime never read while unlocked |
| SC-11 | Medium/low fixes applied: GetSecret caches decrypted map; Kafka RequiredAcks uses pointer sentinel; ActivityName set when activity_id==99; secrets temp file 0600; drain backoff moved out of streamRecords callback | VERIFIED | `store.go:39-41`: `cacheMu sync.RWMutex; cache map[string]string; cacheValid bool`; `lookup()` RLock fast path; `env_fallback.go:39`: `s.lookup(key)` not `LoadSecrets()`; `store.go:211`: `os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)`; `kafka/connector.go:51`: `RequiredAcks *int`; `New()` sets `&1` on nil; `mapper.go:289-292`: `if activityID==99 { activityName="Other" }`; `buffer.go:269-279`: backoff in drainLoop after drainOnce returns, never inside streamRecords callback |
| SC-12 | go test ./... passes; go vet ./... clean | VERIFIED | Phase-exit gate in 03-04 Task 3: all 10 packages with tests pass (buffer, connector, elastic, kafka, splunk, dryrun, ocsf, resilience, secrets, signal); go vet ./... clean; go build ./... clean; -race flag not used (CGO unavailable on this Windows machine — documented constraint) |

**Score:** 12/12 truths verified

### Deferred Items

| # | Item | Addressed In | Evidence |
|---|------|-------------|----------|
| 1 | F17 — LoggedTime non-determinism in mapper.Map | Locked decision 5 (accepted, no future phase) | `NOTE(F17)` comment at `mapper.go:312-315`; behavior unchanged; deferral documented in 03-03-SUMMARY.md and 03-04-SUMMARY.md |

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/buffer/wal.go` | New format, parseSegmentName, listSegments | VERIFIED | Contains `statusLive`, `statusConsumed`, `headerSize=5`, `parseSegmentName`, `listSegments`, `markConsumedAt`, `segSeq atomic.Uint64` |
| `internal/buffer/buffer.go` | Race-free Write, multi-segment drain, nil-drain guards, errDrainFailed | VERIFIED | Contains `errDrainFailed`, `enforceTotal`, `segMaxBytes`, `totalMaxBytes`, nil-drain guards in Start+Flush, backoff in drainLoop |
| `internal/buffer/wal_test.go` | Format-level tests (>=80 lines) | VERIFIED | File exists with TestWAL_StatusByteFormat, TestWAL_MarkConsumedSkips, TestWAL_LargePayloadOffsetIntegrity, TestWAL_SegmentNaming |
| `internal/buffer/buffer_test.go` | Concurrent-writers and drain tests (>=200 lines) | VERIFIED | Contains TestBuffer_ConcurrentWriters, TestBuffer_MultiSegmentDrainOldestFirst, TestBuffer_ConsumedSegmentDeleted, TestBuffer_MaxSizeDropsOldest, TestBuffer_BackoffOutsideStream, TestBuffer_NilDrainErrors |
| `internal/agent/agent.go` | stop() no longer passes nil drain | VERIFIED | stop() contains only `a.buffer.Close()` and a TODO comment; no nil Flush call |
| `internal/connector/splunk/connector.go` | Chunked Send, failed=>error contract | VERIFIED | Contains `sendChunk`, `chunk` pattern, all failure paths return non-nil error |
| `internal/connector/elastic/connector.go` | Chunked Send, json.Marshal action line, APIKey zeroed | VERIFIED | Contains `sendChunk`, `json.Marshal(actionObj)`, `cfg.APIKey=""` after header computation, `c.apiKeyHeader` check in Connect |
| `internal/connector/connector.go` | Dispatcher Stats(), wired accepted/delivered/failed | VERIFIED | `Stats()` at line 295; `atomic.AddUint64(&d.accepted,1)` in Enqueue; `atomic.AddUint64(&d.delivered/failed,1)` in process |
| `internal/connector/config.go` | Parent-dir watcher, zap logging, Rename/Remove handling | VERIFIED | `fw.Add(filepath.Dir(absPath))`, `filepath.Base` filter, Write|Create|Rename|Remove handled, `w.logger.Warn(...)`, no fmt.Fprintf(os.Stderr) |
| `internal/resilience/circuit_breaker.go` | Single-lock state decision (>=150 lines) | VERIFIED | 195 lines; single `cb.mu.Lock()` covers state+lastFailureTime+transition; no unlocked read of guarded fields |
| `internal/secrets/store.go` | Cache behind RWMutex, O_CREATE 0600 temp file | VERIFIED | `cacheMu sync.RWMutex`, `lookup()` with RLock fast path, `os.OpenFile(..., 0600)` at line 211 |
| `internal/connector/kafka/connector.go` | RequiredAcks *int with nil=default sentinel | VERIFIED | Field `RequiredAcks *int` with doc comment; `New()` sets `&1` on nil; Connect switches on `*c.cfg.RequiredAcks` with reachable case 0 |
| `internal/ocsf/mapper.go` | ActivityName=Other for activity_id 99, F17 deferral comment | VERIFIED | `if activityID==99 { activityName="Other" }`; ActivityName field set in Event literal; `NOTE(F17)` comment at LoggedTime line |
| `internal/dryrun/dryrun.go` | Per-signal Map loop, nil-padded parallel events slice | VERIFIED | `events:=make([]*ocsf.Event,len(signals))`; per-signal `mapper.Map(s)` loop; MapBatch appears only in a comment (not called) |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `buffer.go drainOnce` | `wal.go listSegments` | `listSegments(cfg.Dir)` call | WIRED | drainOnce calls listSegments and iterates all refs |
| `buffer.go Write` | `buffer.go enforceTotal` | `b.enforceTotal()` call at end of Write, under segMu | WIRED | Eviction runs on every Write after append |
| `agent.go stop()` | `buffer.Close()` | `a.buffer.Close()` | WIRED | nil Flush call removed; Close still called |
| `elastic sendChunk` | `json.Marshal action line` | `json.Marshal(actionObj)` | WIRED | actionLine used for every document in the chunk loop |
| `elastic New()` | `cfg.APIKey zeroing` | `cfg.APIKey = ""` after header computation | WIRED | Connect checks `c.apiKeyHeader` not `cfg.APIKey` |
| `connector Enqueue` | `accepted counter` | `atomic.AddUint64(&d.accepted,1)` | WIRED | Only on successful channel insert |
| `connector process` | `delivered/failed counters` | `atomic.AddUint64` in err/no-err branches | WIRED | Both branches covered |
| `config.go NewWatcher` | `parent directory watch` | `fw.Add(filepath.Dir(absPath))` | WIRED | Confirmed; filename filter applied in Start |
| `circuit_breaker Call` | `single-lock decision` | `cb.mu.Lock()` before state read | WIRED | Lock held through lastFailureTime read and transition |
| `env_fallback GetSecret` | `store.lookup()` | `s.lookup(key)` | WIRED | LoadSecrets() no longer called per-invocation |
| `secrets SaveSecrets` | `0600 temp file` | `os.OpenFile(..., 0600)` | WIRED | tmpPath created with mode 0600 from first byte |
| `kafka New()` | `RequiredAcks nil default` | `if cfg.RequiredAcks == nil { one:=1; cfg.RequiredAcks=&one }` | WIRED | Connect switches on `*c.cfg.RequiredAcks`; case 0 reachable |
| `ocsf Map()` | `ActivityName=Other` | `if activityID==99 { activityName="Other" }` | WIRED | Set in Event literal |
| `dryrun Run` | `per-signal mapper.Map` | `for i,s := range signals { mapper.Map(s) }` | WIRED | MapBatch not called |

### Data-Flow Trace (Level 4)

Not applicable — no network endpoints or UI rendering components in this phase. All modified components are library/internal packages with test-verified behavior.

### Behavioral Spot-Checks

Step 7b: SKIPPED — no runnable server entry points that can be tested without starting a live service. The phase-exit gate (SC-12, Task 3 of plan 03-04) serves as the functional substitute: `go test ./... -count=1` passed all 10 packages.

### Probe Execution

Step 7c: No probe scripts declared in any plan or referenced in success criteria. No conventional `scripts/*/tests/probe-*.sh` files exist for this phase.

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|----------|
| SC-1 / F1 | 03-01 | WAL status-byte format; markConsumed writes only status byte | SATISFIED | wal.go record format + markConsumedAt implementation |
| SC-2 / F2 | 03-01 | Race-free Write; concurrent-writers test | SATISFIED | segMu held across full Write; TestBuffer_ConcurrentWriters |
| SC-3 / F3 | 03-01 | Multi-segment oldest-first drain; consumed seg deleted; MaxSizeMB drop accounting | SATISFIED | drainOnce + listSegments + enforceTotal implementation |
| SC-4 / F5 | 03-01 | Flush/Start error on nil drain; agent.stop fixed | SATISFIED | nil-drain guards in buffer.go; agent.go stop() |
| SC-5 / F4 | 03-03 | Elastic action line via json.Marshal | SATISFIED | sendChunk uses json.Marshal(actionObj) |
| SC-6 / F6 | 03-02 | Unified delivery contract; dispatcher counters | SATISFIED | sendChunk non-nil errors; Enqueue/process atomic counters; Stats() |
| SC-7 / F7 | 03-02 | Splunk/Elastic chunked batches | SATISFIED | ceil(n/limit) chunks; abort-on-first-failure |
| SC-8 / F8 | 03-04 | dryrun per-signal Map with index alignment | SATISFIED | per-signal Map loop; nil-padded events slice |
| SC-9 / F9 | 03-03 | Config Watcher atomic-rename survival + zap | SATISFIED | parent-dir watch; Rename/Remove events; zap logging |
| SC-10 / F10 | 03-03 | CircuitBreaker single-lock TOCTOU fix | SATISFIED | Single cb.mu.Lock() covers full state decision |
| SC-11 / F11,F12,F13,F14,F15,F16 | 03-01,03-03 | GetSecret cache; RequiredAcks pointer; ActivityName; 0600 temp; backoff outside stream; APIKey zeroing | SATISFIED | All verified in respective files |
| SC-12 | 03-04 | go test ./... passes; go vet ./... clean | SATISFIED | Phase-exit gate: all 10 test packages pass; vet clean |

### Anti-Patterns Found

No blockers. Scan of all modified files:

| File | Pattern | Severity | Assessment |
|------|---------|----------|-----------|
| `internal/agent/agent.go` | `// TODO: call a.buffer.Flush...` | INFO | Intentional scope fence per 03-CONTEXT.md — agent.start wiring is a future phase |
| `internal/ocsf/mapper.go` | `// NOTE(F17): time.Now()...` | INFO | Intentional deferral per locked decision 5 — documents accepted deviation |
| `internal/buffer/buffer.go` | `_ = b.seg.file.Sync()` (error discarded) | INFO | Best-effort on rotation path; not a correctness issue |

No `TBD`, `FIXME`, or `XXX` markers found in any files modified by this phase. No stubs or placeholder implementations found.

### Human Verification Required

None. All 12 success criteria are verifiable programmatically via source inspection and the test suite results documented in 03-04-SUMMARY.md.

### Gaps Summary

No gaps. All 17 review findings are addressed:
- 16 findings closed with implementation and tests (F1-F16)
- 1 finding (F17) accepted/deferred by locked decision 5, documented with `NOTE(F17)` comment in mapper.go — not a gap

The phase goal is fully achieved.

---

_Verified: 2026-06-10T12:00:00Z_
_Verifier: Claude (gsd-verifier)_
