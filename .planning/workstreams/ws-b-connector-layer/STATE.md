---
gsd_state_version: 1.0
milestone: v1.0
milestone_name: connector-layer
current_phase: 03
current_plan: "03-01"
status: in_progress
stopped_at: "03-01 complete — WAL/buffer rewrite done; F1,F2,F3,F5,F14 closed"
last_updated: "2026-06-10T00:00:00Z"
last_activity: 2026-06-10
progress:
  total_phases: 2
  completed_phases: 1
  total_plans: 10
  completed_plans: 7
  percent: 70
---

# WS-B Connector Layer — State

## Current Position

Phase: 03 (review-remediation) — IN PROGRESS
**Status:** 1/4 plans complete
**Last Activity:** 2026-06-10
**Last Activity Description:** 03-01 WAL/buffer rewrite complete — F1 (status-byte format), F2 (race-free Write), F3 (multi-segment drain), F5 (nil-drain guard), F14 (backoff outside stream) all closed

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

## Plans Remaining

| Plan | Name | Wave | Status |
|------|------|------|--------|
| 03-02 | Delivery contract (F6,F7,dispatcher counters) | 1 | planned |
| 03-03 | Injection + infra fixes (F4,F8,F9,F10,F11,F12,F13,F15,F16) | 2 | planned |
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

## Session Continuity

**Stopped At:** 03-01 complete — buffer/WAL hardening done; next: 03-02 delivery contract
**Resume File:** phases/03-review-remediation/03-CONTEXT.md (F6, F7 are 03-02 scope)
**Research:** phases/02-connector-layer/02-RESEARCH.md
