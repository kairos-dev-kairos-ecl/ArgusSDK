---
gsd_state_version: 1.0
milestone: v1.0
milestone_name: connector-layer
current_phase: 03
current_plan: "03-04"
status: in_progress
stopped_at: "03-03 complete — injection fix (F4), APIKey zeroing (F16), watcher rename-survival (F9), circuit-breaker TOCTOU (F10), secrets cache (F11), 0600 temp (F15), Kafka RequiredAcks *int (F12), ActivityName (F13), F17 deferred"
last_updated: "2026-06-10T11:12:37Z"
last_activity: 2026-06-10
progress:
  total_phases: 2
  completed_phases: 1
  total_plans: 10
  completed_plans: 8
  percent: 80
---

# WS-B Connector Layer — State

## Current Position

Phase: 03 (review-remediation) — IN PROGRESS
**Status:** 3/4 plans complete
**Last Activity:** 2026-06-10
**Last Activity Description:** 03-03 injection + infra fixes complete — F4 (json.Marshal action line), F16 (APIKey zeroing), F9 (watcher rename-survival + zap), F10 (circuit-breaker single-lock), F11 (secrets cache), F15 (0600 temp), F12 (Kafka RequiredAcks *int), F13 (ActivityName=Other), F17 (deferred/commented) all closed

## Plans Completed

| Plan | Name | Commit | Status |
|------|------|--------|--------|
| 02-01 | OCSF mapper | (wave 1) | done |
| 02-02 | TLS + buf codegen | (wave 1) | done |
| 02-03 | Signal conversion | (wave 1) | done |
| 02-04 | Kafka connector | (wave 2) | done |
| 02-05 | Elastic connector | 2a5c827 | done |
| 02-06 | WAL dead-letter buffer | 8a7a2ed | done |
| 03-01 | WAL/buffer hardening (F1,F2,F3,F5,F14) | 39da508 | done |
| 03-02 | Delivery contract (F6,F7,dispatcher counters) | 39c0b22 | done |
| 03-03 | Injection + infra fixes (F4,F9,F10,F11,F12,F13,F15,F16,F17) | 73bde20 | done |

## Plans Remaining

| Plan | Name | Wave | Status |
|------|------|------|--------|
| 03-04 | dryrun alignment + full-suite race verification | 3 | planned |

## Decisions Made

- NDJSON /_bulk over stdlib net/http — no go-elasticsearch SDK; works with ES 8.x and OpenSearch 2.x
- APIKey pre-computed at New() time via base64.StdEncoding to avoid per-request encoding overhead
- ocsfToECS returns flat map[string]interface{} for direct JSON serialisation without nested struct allocation
- Health() returns nil for both green and yellow — yellow is warning state, not error
- encoding/gob used for WAL buffer records (not proto) — SignalBatch is internal Go struct with no proto methods
- WAL record format fixed to [status:1][len:4 BE][payload] — markConsumed writes only status byte, preserving length for skip-on-read (F1)
- segMu held across entire Write (not released before appendRecord) — eliminates data race on b.seg (F2)
- drainOnce opens mark file once per segment and reuses handle across all markConsumedAt calls — avoids per-record open/close overhead on Windows
- Backoff owned by drainLoop/Flush; streamRecords callback returns errDrainFailed sentinel immediately (F14)
- Start/Flush return error on nil drain; agent.stop removes the nil Flush call (F5)
- Chunking is ceil(n/limit) sequential requests; abort on first failed chunk naming chunk i/total in error (F7)
- sendChunk returns nil for all-unmappable chunk (skip POST, not a failure) — preserves existing empty-payload behavior
- Dispatcher accepted incremented only on successful queue insert, not on full-queue or shutdown rejection (F6)
- Stats() returns map[string]uint64 for stable observability accessor (accepted/delivered/failed/dropped)
- F4: json.Marshal for Elastic action line — cfg.Index is data, never syntax; hostile names JSON-escaped
- F16: cfg.APIKey zeroed after apiKeyHeader computed; Connect() checks apiKeyHeader; Cfg()/APIKeyHeader() white-box accessors
- F9: Watcher watches parent directory (not file inode); filters by filepath.Base(event.Name); handles Rename/Remove; zap logger injected (nil→NewNop())
- F10: CircuitBreaker.Call single mutex hold covers Open check + lastFailureTime read + HalfOpen transition — no TOCTOU
- F11: Store.lookup() RWMutex cache; SaveSecrets updates cache; GetSecret uses lookup() not LoadSecrets()
- F15: os.OpenFile with 0600 flag at temp file creation instead of os.Create
- F12: Config.RequiredAcks *int — nil=default(1), *0=NoAck; kgo.NoAck() now reachable
- F13: Map() sets ActivityName="Other" when activityID==99
- F17: accepted/deferred — NOTE comment at LoggedTime; no behavioral change (clock injection is API-breaking)

## Session Continuity

**Stopped At:** 03-03 complete — injection + infra fixes done; next: 03-04 dryrun alignment + race verification
**Resume File:** phases/03-review-remediation/03-CONTEXT.md
**Research:** phases/02-connector-layer/02-RESEARCH.md
