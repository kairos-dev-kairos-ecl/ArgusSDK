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

- [ ] **Phase 2: Connector Layer — Framework + OCSF + Priority Connectors**

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

## Progress

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 2. Connector Layer | 0/6 | Pending | — |
