---
gsd_state_version: 1.0
milestone: v1.0
milestone_name: connector-layer
current_phase: 04
current_plan: N/A
status: planned
stopped_at: "Phase 4 (agent wiring + integration hardening) planned — 6 plans across 3 waves, plan-checker PASS (1 revision cycle: fixed argusxdr proto-contract errors B1/B2 + per-target UseOCSF N5). Ready for /gsd:execute-phase 04."
last_updated: "2026-06-11T00:00:00Z"
last_activity: 2026-06-11
progress:
  total_phases: 3
  completed_phases: 2
  total_plans: 16
  completed_plans: 10
  percent: 62
---

# WS-B Connector Layer — State

## Current Position

Phase: 03 (review-remediation) — COMPLETE
**Status:** 4/4 plans complete — PHASE COMPLETE
**Last Activity:** 2026-06-10
**Last Activity Description:** 03-04 dryrun alignment + phase-exit gate — F8 (per-signal Map loop, nil-padded events slice), TestRun_MapperErrorMidBatch_IndexAlignment passes, go test ./... all green, go vet ./... clean, go build ./... clean. All 16 findings closed; F17 accepted/deferred.

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
| 03-04 | dryrun index alignment + phase-exit gate (F8, SC-12) | 5b9578d | done |

## Plans Remaining

None — all plans complete.

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
- F8: per-signal mapper.Map loop in dryrun.Run — nil-padded events slice; MapBatch removed from dryrun call site; index correspondence maintained for error attribution and OCSFValid counts

## Session Continuity

**Stopped At:** Phase 4 (agent wiring + integration hardening) planned and verified — 6 plans, 3 waves, plan-checker PASS. Ready for /gsd:execute-phase 04 (sequential main-tree execution).
**Resume File:** phases/04-agent-wiring/04-CONTEXT.md (locked decisions, file map, SC-1..SC-12)
**Research:** phases/02-connector-layer/02-RESEARCH.md

## Phase 4 Plan Map (planned 2026-06-11)

| Wave | Plan | Objective | SC |
|------|------|-----------|----|
| 1 | 04-01 | Connector factory (Type→typed Config→Connector) + crypto/rand BatchID | SC-1 |
| 1 | 04-02 | syslog CEF over TCP/TLS 1.3 | SC-7 |
| 1 | 04-03 | argusxdr gRPC IngestBatch (instance/group via metadata) | SC-8 |
| 1 | 04-04 | LLM gRPC collector + EUC collector | SC-5, SC-6 |
| 2 | 04-05 | agent start()/stop() wiring + ingest loop + drain + registration seam | SC-2,3,4,12 |
| 3 | 04-06 | Kafka/Elastic/Splunk testcontainers integration + CI -race gate + cmd smoke | SC-9,10,11,12 |

Scope decisions (2026-06-11): full agent incl. collectors; testcontainers integration; syslog+argusxdr implemented; -race as CI gate (no local C compiler). Only new dep: testcontainers-go (test-only).
