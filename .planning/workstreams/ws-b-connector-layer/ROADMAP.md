# Roadmap: WS-B — Connector Layer

## Overview

Build the signal routing layer that connects ArgusSDK to external platforms.
Depends on WS-A (Protocol Foundation) being complete — the proto contract is the
input to every connector transformation.

Architecture: native Go connector framework (Option A from research) with OCSF
as the canonical intermediate schema. Each connector implements a common interface.
The framework handles lifecycle, health, retry, and backpressure.

**Research:** `phases/02-connector-layer/02-RESEARCH.md`

## Phases

- [x] **Phase 2: Connector Layer — Framework + OCSF + Priority Connectors**
- [ ] **Phase 3: Review Remediation — Buffer Hardening + Delivery Contract + Injection Fixes**

## Phase Details

### Phase 2: Connector Layer — Framework + OCSF + Priority Connectors
**Goal:** Build the connector framework (interface, registry, lifecycle, circuit breaker,
dead-letter buffer), implement the ArgusSignal→OCSF v1.3 canonical mapper, and deliver
the three priority connectors (Kafka/Redpanda, Splunk HEC, Elastic/OpenSearch) — giving
ArgusSDK the ability to route signals to enterprise platforms without ArgusXDR.

**Depends on:** WS-A Phase 1 (proto contract — `pkg/signal` with `FromProto`/`ToProto`)

**Success Criteria** (what must be TRUE):
  1. `internal/connector/connector.go` defines the `Connector` interface (Accept, Health, Close) and a registry/dispatcher
  2. `internal/ocsf/` maps all 14 `ArgusSignal` fields to OCSF v1.3 — unit-tested round-trip
  3. `internal/connector/kafka/` sends OCSF-formatted signal batches to a Kafka/Redpanda topic with SASL/TLS; integration test passes
  4. `internal/connector/splunk/` POSTs OCSF→CIM batches to Splunk HEC with token auth; integration test passes
  5. `internal/connector/elastic/` bulk-indexes OCSF→ECS events with API key auth; integration test passes
  6. Dead-letter buffer: failed batches are retried with exponential backoff; persistent failures written to local WAL (`internal/buffer/`)
  7. TLS 1.3 enforced on all connector transports — `InsecureSkipVerify` never set
  8. All connectors are config-driven (YAML) and hot-reloadable without restart
  9. `go test ./internal/connector/... ./internal/ocsf/...` passes

**Plans:** 6 plans across 3 waves

Plans:
- [ ] 02-01-PLAN.md — OCSF mapper tests + ContextJSON extraction
- [ ] 02-02-PLAN.md — Connector framework TLS helper + YAML config hot-reload
- [ ] 02-03-PLAN.md — Kafka/Redpanda connector (franz-go)
- [ ] 02-04-PLAN.md — Splunk HEC connector
- [ ] 02-05-PLAN.md — Elastic/OpenSearch connector
- [ ] 02-06-PLAN.md — Dead-letter WAL implementation + phase integration tests

### Phase 3: Review Remediation — Buffer Hardening + Delivery Contract + Injection Fixes
**Goal:** Fix all 17 findings from the 2026-06-10 full-codebase code/security review
(REVIEW source: session review of all 19 packages). The dead-letter buffer must
actually not lose data (WAL format flaw, write race, rotation stranding), delivery
failures must be visible (unified ack/error contract, no silent truncation), and the
Elastic NDJSON injection sink must be closed.

**Depends on:** Phase 2 (all touched code shipped in Phase 2)

**Success Criteria** (what must be TRUE):
  1. WAL record format is `[1-byte status][4-byte big-endian length][payload]`; markConsumed touches only the status byte; records of any size round-trip correctly
  2. `Buffer.Write` is race-free — `go test -race ./internal/buffer/...` passes with a concurrent-writers test
  3. Drain enumerates ALL `wal-*.seg` segments oldest-first; fully-consumed segments are deleted; `MaxSizeMB` enforcement drops oldest segment and increments `dropped_batches`
  4. `Buffer.Flush` returns an error (not panic) on nil drain func; `agent.stop` no longer passes nil
  5. Elastic `/_bulk` action line is built via `json.Marshal` — no string concatenation of `cfg.Index`
  6. Delivery contract unified: failed delivery returns non-nil error in kafka, splunk, AND elastic; Dispatcher increments delivered/failed counters from results
  7. Splunk/Elastic split oversized batches into multiple sequential requests — no silent truncation
  8. `dryrun.Run` uses per-signal `mapper.Map` with index alignment — mapper errors attribute the correct SignalID
  9. Config Watcher survives atomic-rename file replacement (re-adds watch on Rename/Remove or watches parent dir) and logs via zap
 10. `CircuitBreaker.Call` holds the mutex for the full state-transition decision (no TOCTOU)
 11. Medium/low fixes applied: GetSecret caches decrypted map; Kafka RequiredAcks uses sentinel for "unset"; ActivityName set when activity_id==99; secrets temp file created 0600; drain backoff moved out of streamRecords callback
 12. `go test -race ./...` passes; `go vet ./...` clean

**Plans:** 4 plans across 3 waves (wave 1: 03-01 ∥ 03-02; wave 2: 03-03 after 03-02 — shared elastic file; wave 3: 03-04 gate)

Plans:
- [ ] 03-01-PLAN.md — Buffer/WAL rewrite: status-byte format, race fix, multi-segment drain, nil-drain guard
- [ ] 03-02-PLAN.md — Delivery contract: unified failed⇒error, batch chunking, dispatcher counters
- [ ] 03-03-PLAN.md — Injection + infra fixes: Elastic action line, Watcher rename, circuit breaker TOCTOU, medium/low items
- [ ] 03-04-PLAN.md — dryrun index alignment + full-suite race verification

## Progress

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 2. Connector Layer | 6/6 | Complete | 2026-05-28 |
| 3. Review Remediation | 0/4 | Planned | — |
