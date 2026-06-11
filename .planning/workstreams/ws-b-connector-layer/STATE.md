---
gsd_state_version: 1.0
milestone: v1.0
milestone_name: connector-layer
current_phase: 04
current_plan: "06"
status: complete
stopped_at: "04-06 complete — Kafka+Elastic testcontainers integration tests, Splunk HEC env-gated smoke, GitHub Actions CI (build/vet/race/integration), Docker-free e2e smoke. SC-9, SC-10, SC-11, SC-12 satisfied. Phase 4 ALL PLANS DONE."
last_updated: "2026-06-11T14:00:00Z"
last_activity: 2026-06-11
progress:
  total_phases: 3
  completed_phases: 3
  total_plans: 16
  completed_plans: 16
  percent: 100
---

# WS-B Connector Layer — State

## Current Position

Phase: 04 (agent-wiring) — COMPLETE
**Status:** 6/6 plans complete — ALL PHASE 4 PLANS DONE
**Last Activity:** 2026-06-11
**Last Activity Description:** 04-06 integration tests + CI — Kafka+Elastic testcontainers (SC-9), Splunk HEC env-gated smoke (SC-10), GitHub Actions CI with CGO race gate (SC-11), Docker-free e2e smoke ingest→deliver→drain (SC-12). testcontainers-go v0.42.0 added (test-only). All 19 packages pass go test ./...; go build ./... clean.

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
| 04-01 | Connector factory + BatchID generator | c1fb655 | done |
| 04-02 | syslog CEF over TCP/TLS 1.3 | e3d33fd | done |
| 04-03 | argusxdr gRPC IngestBatch | 5576dfe | done |
| 04-04 | LLM gRPC collector + EUC collector | 9cc396b | done |
| 04-05 | agent start()/stop() wiring + ingest loop + drain | 0732354 | done |
| 04-06 | Kafka/Elastic/Splunk integration + CI + smoke | c4ca668 | done |

## Plans Remaining

None — all 16 plans complete.

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
- Factory in internal/connector/factory (not internal/connector) to break connector↔subpackage import cycle
- NewBatchID: 6-byte ts + 8-byte rand + 2-byte monotonic seq counter — within-ms ordering guaranteed
- mapstructure WeaklyTypedInput=true for YAML/env config pipeline type coercion
- syslog TLS: ServerName derived from host part of server address after NewTLSConfig — required by Go TLS client when dialing by IP, not InsecureSkipVerify
- buildCEF sanitises pipe (→ /) and newlines in field values — prevents CEF header injection (T-04-05)
- Send: abort-on-first-write-error with signal index in error message (locked decision 9)
- argusxdr apiKeyCreds.RequireTransportSecurity returns true when TLS enabled — prevents API key over cleartext (T-04-07)
- argusxdr Health probes with empty IngestBatch{BatchId:"health-check"} — lightweight, no signal marshalling
- argusxdr maxBatchSize defaults to 500 if unset; chunking sequential abort-on-first-failure (locked decision 9)
- argusxdr UseOCSF silently ignored — always uses proto wire format (locked decision 5, T-04-10)
- LLM collector startOnListener seam allows bufconn injection in tests without binding a real port
- ingestServer embeds UnimplementedSDKIngestServiceServer by value (per generated NOTE in grpc stubs)
- EUC Layer=L9APIGateway — observations sit at the network/API-gateway boundary in the 10-layer taxonomy
- EUC SignalID uses crypto/rand 16-byte hex — no ULID dependency added (locked decision)
- noopOSCollector exported as NewNoopOSCollector() — canonical cross-platform OS impl seam for Windows
- localRegistrar derives InstanceID from SHA-256(groupID:installToken) truncated to 16 hex chars — deterministic, no live-XDR required (locked decision 8)
- ingestLoop partitions connectors into ocsfTargets (OCSF=true AND Type!=argusxdr) and nonOCSFTargets; separate DispatchJobs per group (locked decisions 5, N5)
- deliver() routes buffered batches by matching UseOCSF flag to the correct partition
- stop() waits for ingest loop via sync.WaitGroup before Flush — guarantees in-flight batches are enqueued or buffered
- shutdownTimeout=30s for buffer.Flush matches DefaultDispatchConfig.ShutdownTimeout
- euc collector wired with NewNoopOSCollector as cross-platform seam in start(); real OSCollector deferred

## Session Continuity

**Stopped At:** 04-06 complete — ALL WS-B plans done. SC-9 Kafka+Elastic testcontainers, SC-10 Splunk HEC gated smoke, SC-11 CI with CGO race gate, SC-12 Docker-free e2e smoke.
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
