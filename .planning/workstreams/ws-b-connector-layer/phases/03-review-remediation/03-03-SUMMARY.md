---
plan: 03-03
phase: 3
subsystem: connector-layer
tags: [security, injection-fix, watcher, circuit-breaker, secrets-cache, kafka, ocsf]
dependency_graph:
  requires: [03-02]
  provides: [injection-closed, watcher-rename-safe, circuit-breaker-toctou-free, secrets-cache, kafka-requiredacks-pointer, ocsf-activityname]
  affects: [internal/connector/elastic, internal/connector/config, internal/resilience, internal/secrets, internal/connector/kafka, internal/ocsf]
tech_stack:
  added: []
  patterns:
    - json.Marshal for NDJSON action-line construction (injection prevention)
    - Parent-directory fsnotify watching with filename filter (atomic-rename survival)
    - Single-mutex state decision for circuit breaker (TOCTOU elimination)
    - RWMutex-guarded in-memory secrets cache with lazy load + SaveSecrets invalidation
    - Pointer-semantics for optional int config fields (nil vs explicit 0)
key_files:
  created:
    - internal/resilience/circuit_breaker_test.go
    - internal/secrets/store_test.go
  modified:
    - internal/connector/elastic/connector.go
    - internal/connector/elastic/connector_test.go
    - internal/connector/config.go
    - internal/connector/config_test.go
    - internal/resilience/circuit_breaker.go
    - internal/secrets/store.go
    - internal/secrets/env_fallback.go
    - internal/connector/kafka/connector.go
    - internal/connector/kafka/connector_test.go
    - internal/ocsf/mapper.go
    - internal/ocsf/mapper_test.go
decisions:
  - "F4: json.Marshal used for NDJSON action line — cfg.Index is data, never syntax; hostile index names containing quotes/braces are JSON-escaped and cannot inject /_bulk parameters"
  - "F16: cfg.APIKey zeroed immediately after apiKeyHeader computation in New(); Connect() checks apiKeyHeader not cfg.APIKey; Cfg() and APIKeyHeader() white-box accessors added"
  - "F9: NewWatcher watches parent directory (not file inode) via fw.Add(filepath.Dir(absPath)); events filtered by filepath.Base(event.Name)==target; handles Write|Create|Rename|Remove; logs via injected *zap.Logger (nil→zap.NewNop()); both fmt.Fprintf(os.Stderr) calls removed"
  - "F10: CircuitBreaker.Call holds single cb.mu.Lock() covering state read + time.Since(cb.lastFailureTime) + Open->HalfOpen transition; lock released only before user-supplied operation(); eliminates TOCTOU where two goroutines both observe Open and both transition"
  - "F11: Store.lookup() serves from RWMutex-guarded cache; RLock fast path when cacheValid; Lock+double-check+LoadSecrets on miss; SaveSecrets updates cache with copy of saved map; GetSecret uses lookup() not LoadSecrets()"
  - "F15: SaveSecrets uses os.OpenFile(tmpPath, O_CREATE|O_WRONLY|O_TRUNC, 0600) instead of os.Create — temp file never momentarily world-readable"
  - "F12: Config.RequiredAcks changed from int to *int: nil=default(1), *0=NoAck, *1=leader, *-1=all ISR; New() sets &1 when nil; Connect() switch on *c.cfg.RequiredAcks — kgo.NoAck() now reachable"
  - "F13: Map() sets ActivityName='Other' when activityID==99; non-99 layers leave ActivityName empty"
  - "F17: accepted/deferred per locked decision 5 — NOTE comment added at mapper.go LoggedTime line documenting non-determinism; no behavioral change"
metrics:
  duration: "~90 minutes"
  completed: "2026-06-10T11:12:37Z"
  tasks_completed: 3
  files_modified: 11
---

# Phase 3 Plan 03: Injection + Infra Fixes Summary

**One-liner:** Closed the Elastic /_bulk injection sink (F4) via json.Marshal, fixed 7 infra/hygiene findings (F9 watcher rename-survival with zap, F10 circuit-breaker TOCTOU, F11 secrets cache, F12 Kafka RequiredAcks *int, F13 ActivityName, F15 0600 temp), and documented F16 (APIKey zeroing) and F17 (LoggedTime deferral).

## Tasks Completed

| Task | Name | Commits | Files |
|------|------|---------|-------|
| 1 RED | Hostile-index + APIKey tests | f81fb55 | connector_test.go |
| 1 GREEN | json.Marshal action line + APIKey zeroing | ac58b55 | connector.go |
| 2 RED | Watcher rename + circuit breaker race tests | 272f05a | config_test.go, circuit_breaker_test.go |
| 2 GREEN F9 | Watcher parent-dir + zap | 18616a1 | config.go, config_test.go |
| 2 GREEN F10 | Circuit breaker single-lock | 21177d6 | circuit_breaker.go |
| 3 RED | Secrets cache + perms + Kafka + ActivityName tests | ec26039 | store_test.go, connector_test.go (kafka), mapper_test.go |
| 3 GREEN F11/F15 | Secrets cache + 0600 temp | 2eb99d2 | store.go, env_fallback.go, store_test.go |
| 3 GREEN F12 | Kafka RequiredAcks *int | a660c6b | connector.go (kafka), connector_test.go (kafka) |
| 3 GREEN F13/F17 | ActivityName + F17 comment | 73bde20 | mapper.go, mapper_test.go |

## Findings Fixed

### F4 — Elastic /_bulk injection (critical, security)
**Before:** `actionLine := []byte(`{"index":{"_index":"` + c.cfg.Index + `"}}` + "\n")` — hostile cfg.Index values containing `"` or `}` could inject /_bulk parameters.
**After:** `json.Marshal(map[string]any{"index": map[string]string{"_index": c.cfg.Index}})` — cfg.Index is treated as a JSON string value; all special characters are escaped.
**Test:** `TestSend_HostileIndexNameStaysWellFormed` — sends batch with Index=`evil"}},{"delete":{"_index":"x`; asserts all NDJSON lines parse as valid JSON with no `"delete"` action.

### F16 — Raw APIKey retention (low, security)
**Before:** `c.cfg` stored the raw APIKey string after New(); Connect() checked `c.cfg.APIKey == ""`.
**After:** `cfg.APIKey = ""` immediately after computing `apiKeyHeader`; Connect() checks `c.apiKeyHeader == ""`.
**Tests:** `TestNew_APIKeyZeroedAfterHeader`, `TestConnect_EmptyAPIKeyStillRejected`.

### F9 — Config Watcher silent death on atomic rename (high)
**Before:** `fw.Add(path)` watched the file inode; atomic-rename saves (vim, `write tmp + rename`) replaced the inode, silently killing the watch. Errors logged via `fmt.Fprintf(os.Stderr)`.
**After:** `fw.Add(filepath.Dir(absPath))` watches the parent directory; events filtered by `filepath.Base(event.Name)==target`. Handles Write|Create|Rename|Remove. Errors logged via `*zap.Logger` (nil→zap.NewNop()). Both `fmt.Fprintf(os.Stderr)` calls removed.
**Tests:** `TestWatcher_AtomicRenameTriggersReload`, `TestWatcher_IgnoresSiblingFiles`, `TestWatcher_MalformedYAMLKeepsPrevious` (T-02-03 regression).

### F10 — CircuitBreaker TOCTOU (medium)
**Before:** `cb.mu.Lock()` / read state / `cb.mu.Unlock()` / then `time.Since(cb.lastFailureTime)` read UNLOCKED / re-lock to transition — two goroutines could both observe Open and both transition to HalfOpen.
**After:** Single `cb.mu.Lock()` covers state read + `time.Since(cb.lastFailureTime)` + Open→HalfOpen decision. Lock released only before `operation()` call.
**Test:** `TestCall_SingleHalfOpenTransition` — N=20 concurrent goroutines racing through an Open breaker; -race cleanliness is the primary assertion.

### F11 — Per-call store decrypt (medium)
**Before:** `GetSecret` called `s.LoadSecrets()` on every call — full file read + AES-256-GCM decrypt per lookup.
**After:** `Store.lookup()` serves from `cache map[string]string` behind `sync.RWMutex`; RLock fast path when `cacheValid`; Lock+double-check+load on miss. `SaveSecrets` updates cache. `GetSecret` calls `s.lookup()`.
**Test:** `TestGetSecret_UsesCache` — deletes store file between first and second GetSecret call; second call must still return the value (proves no re-read).

### F15 — Secrets temp file perms window (low)
**Before:** `os.Create(tmpPath)` — created with `0666&umask`, briefly world-readable.
**After:** `os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)` — created with mode 0600 from the start.
**Test:** `TestSaveSecrets_TempFilePerms` — asserts mode 0600 on POSIX (skipped on Windows where Go perms are advisory).

### F12 — Kafka RequiredAcks makes NoAck unreachable (low)
**Before:** `if cfg.RequiredAcks == 0 { cfg.RequiredAcks = 1 }` — coerced 0 to 1, making `kgo.NoAck()` a dead branch.
**After:** `Config.RequiredAcks *int` — nil=default(1), *0=NoAck, *1=leader, *-1=all ISR. New() sets `&1` only when nil. Connect() switches on `*c.cfg.RequiredAcks`.
**Tests:** `TestNew_RequiredAcksNilDefaultsToLeader`, `TestNew_RequiredAcksZeroMeansNoAck`.

### F13 — ActivityName missing for activity_id=99 (low)
**Before:** `Event.ActivityName` never set in Map().
**After:** `if activityID == 99 { activityName = "Other" }` — sets ActivityName in the Event literal.
**Test:** `TestMap_Activity99SetsActivityName` — asserts L8Agents→ActivityName="Other"; non-99 layer→ActivityName="".

### F17 — LoggedTime non-determinism (accepted/deferred)
**Decision (locked):** time.Now() in Map() makes it non-deterministic for golden tests. Clock injection would require API changes touching all connectors. Deferred.
**Action:** Added `// NOTE(F17):` comment at mapper.go LoggedTime line documenting the deferral. No behavioral change.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Test malformed YAML input was valid YAML**
- **Found during:** Task 2 GREEN verification
- **Issue:** `TestWatcher_MalformedYAMLKeepsPrevious` used `":::invalid yaml:::"` which `gopkg.in/yaml.v3` parses as a valid scalar string (no error) — the T-02-03 regression test was testing the wrong failure mode
- **Fix:** Changed to `"connectors:\n  - {invalid"` (unclosed brace) which produces a genuine YAML parse error
- **Files modified:** `internal/connector/config_test.go`
- **Committed as part of:** 18616a1 (F9 implementation commit)

**2. [Rule 2 - Missing functionality] Windows file permission assertion excluded**
- **Found during:** Task 3 GREEN verification for F15
- **Issue:** `info.Mode().Perm()` returns `0666` on Windows for all regular files regardless of OpenFile mode argument (Go file perms are advisory on Windows/NTFS)
- **Fix:** Added `runtime.GOOS != "windows"` guard around the 0600 mode assertion; import `"runtime"` added to store_test.go
- **Files modified:** `internal/secrets/store_test.go`
- **Committed as part of:** 2eb99d2

## Known Stubs

None — all plan goals achieved. Pre-existing stubs in mapper.go (URL placeholder, TODO for databucket) are out-of-scope and predated this plan.

## Threat Surface Scan

All changes are mitigations for threats already documented in the plan's STRIDE register (T-03-11 through T-03-19). No new network endpoints, auth paths, file access patterns, or schema changes at trust boundaries were introduced.

## Self-Check: PASSED

- internal/connector/elastic/connector.go: FOUND (contains json.Marshal)
- internal/connector/config.go: FOUND (contains zap, filepath.Dir)
- internal/resilience/circuit_breaker.go: FOUND (single-lock pattern)
- internal/secrets/store.go: FOUND (contains O_CREATE, cacheMu, cacheValid)
- internal/connector/kafka/connector.go: FOUND (RequiredAcks *int)
- internal/ocsf/mapper.go: FOUND (ActivityName=Other, NOTE(F17))
- All task commits exist in git log (ac58b55, 18616a1, 21177d6, 2eb99d2, a660c6b, 73bde20)
- go test ./internal/... -count=1 -timeout 120s: PASS (all packages)
- go vet ./internal/...: PASS (no issues)
