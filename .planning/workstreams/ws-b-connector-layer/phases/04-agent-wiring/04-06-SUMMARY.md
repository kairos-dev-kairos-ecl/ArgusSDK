---
phase: 04-agent-wiring
plan: "06"
subsystem: integration-testing
tags: [integration-tests, testcontainers, ci, smoke, kafka, elastic, splunk, github-actions]
dependency_graph:
  requires:
    - 04-01  # connector factory
    - 04-02  # syslog connector
    - 04-03  # argusxdr connector
    - 04-04  # llm + euc collectors
    - 04-05  # agent wiring + ingest loop
  provides:
    - internal/connector/kafka/connector_integration_test.go
    - internal/connector/elastic/connector_integration_test.go
    - internal/connector/splunk/connector_integration_test.go
    - internal/agent/e2e_smoke_test.go
    - .github/workflows/ci.yml
  affects:
    - SC-9: Kafka+Elastic testcontainers integration delivery
    - SC-10: Splunk HEC documented gated smoke test
    - SC-11: CI workflow with CGO-enabled race gate
    - SC-12: Docker-free end-to-end smoke (ingest→deliver→drain)
tech_stack:
  added:
    - github.com/testcontainers/testcontainers-go v0.42.0 (test-only)
    - github.com/testcontainers/testcontainers-go/modules/kafka v0.42.0 (test-only)
    - github.com/testcontainers/testcontainers-go/modules/elasticsearch v0.42.0 (test-only)
  patterns:
    - testcontainers-go //go:build integration gated tests
    - env-gated smoke test (SC-10 fallback for Splunk HEC)
    - GitHub Actions three-job CI (build-vet / race / integration)
    - In-process test seam reused from agent_test.go for Docker-free smoke
key_files:
  created:
    - internal/connector/kafka/connector_integration_test.go
    - internal/connector/elastic/connector_integration_test.go
    - internal/connector/splunk/connector_integration_test.go
    - internal/agent/e2e_smoke_test.go
    - .github/workflows/ci.yml
  modified:
    - go.mod (testcontainers-go added)
    - go.sum
decisions:
  - "SC-10 Splunk HEC uses env-gated smoke (SPLUNK_HEC_ENDPOINT + SPLUNK_HEC_TOKEN) rather than testcontainers; Splunk Enterprise image is ~2 GB + requires license-accept env var at startup — not suitable for ephemeral CI containers"
  - "e2e smoke placed in internal/agent/e2e_smoke_test.go (package agent) not cmd/argus-agent because fake-connector injection requires direct field access on *Agent not accessible from package main"
  - "Elasticsearch 7.17.10 used in integration tests; no mandatory xpack TLS+auth by default — simplifies container test setup"
  - "CI race job uses CGO_ENABLED=1 on ubuntu-latest; dev machine (Windows, no C compiler) cannot run -race locally — CI is the authoritative gate (locked decision 7)"
metrics:
  duration: "15m"
  completed_date: "2026-06-11"
  tasks: 3
  files: 7
---

# Phase 4 Plan 06: Kafka/Elastic/Splunk Integration Tests + CI + Smoke Summary

**One-liner:** testcontainers-go integration tests for Kafka and Elasticsearch (multi-chunk F7, t.Skip on no-Docker), env-gated Splunk HEC smoke, three-job GitHub Actions CI (build+vet / CGO race gate / integration), and Docker-free in-process e2e smoke (ingest→deliver→graceful-drain).

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Kafka + Elasticsearch integration tests (SC-9) | a9af3e5 | connector_integration_test.go x2, go.mod, go.sum |
| 2 | Splunk HEC documented gated smoke (SC-10) | 13b110c | splunk/connector_integration_test.go |
| 3 | CI workflow + e2e smoke (SC-11, SC-12) | c4ca668 | .github/workflows/ci.yml, agent/e2e_smoke_test.go |

## What Was Built

### Task 1 — Kafka + Elasticsearch Integration Tests (SC-9)

`internal/connector/kafka/connector_integration_test.go`:
- `//go:build integration` guard; `testcontainers.SkipIfProviderIsNotHealthy(t)` on every Docker-dependent test
- `TestKafkaConnector_DeliverMultiSignalBatch`: starts `confluentinc/confluent-local:7.5.0` container (KRaft mode); Connects kafka.Connector to mapped broker; sends 3-signal batch with UseOCSF=true; asserts `ack.Status=="delivered"`
- `TestKafkaConnector_FailedDelivery_ClosedConnector`: exercises F6 unconnected-connector error path — no Docker needed; asserts non-nil error + `ack.Status=="failed"`

`internal/connector/elastic/connector_integration_test.go`:
- `TestElasticConnector_DeliverBatch`: ES 7.17.10 container; single-chunk 5-signal batch; asserts delivered + queries `_count`
- `TestElasticConnector_MultiChunk`: ES 7.17.10 container; `MaxBatchDocs=3`, 7-signal batch → 3 sequential /_bulk calls (exercises F7 chunking); asserts delivered
- `TestElasticConnector_FailedDelivery_UnconnectedConnector`: F6 error path; no Docker needed

`go.mod`: added `github.com/testcontainers/testcontainers-go v0.42.0` and `modules/kafka`, `modules/elasticsearch` — test-only; `go build ./...` confirmed clean (testcontainers absent from runtime package imports).

### Task 2 — Splunk HEC Documented Gated Smoke (SC-10)

`internal/connector/splunk/connector_integration_test.go`:
- `//go:build integration` guard
- SC-10 fallback approach: env-gated smoke reads `SPLUNK_HEC_ENDPOINT` and `SPLUNK_HEC_TOKEN`; `t.Skip("set SPLUNK_HEC_ENDPOINT/TOKEN to run")` when unset — clean degradation
- `TestSplunkHEC_DeliverBatch`: full HEC delivery cycle; asserts `ack.Status=="delivered"`
- `TestSplunkHEC_BadToken_FailedAck`: bad token → Connect fails health check (401) or Send returns non-nil error + `ack.Status=="failed"` — preserves F6/T-04-21 delivery contract
- File comment documents the rationale for env-gated over testcontainers (Splunk Enterprise ~2 GB image, license-accept requirement)

### Task 3 — CI Workflow + Docker-Free Smoke (SC-11, SC-12)

`.github/workflows/ci.yml` — three jobs triggered on push and pull_request:
- `build-vet`: `go build ./...` + `go vet ./...` + `go vet -tags=integration ./internal/connector/{kafka,elastic,splunk}/`; covers `cmd/argus-agent` compilation
- `race`: `go test -race ./... -count=1 -timeout 300s` with `CGO_ENABLED: "1"` on ubuntu-latest; **the authoritative SC-11 race gate** (CGO not available on Windows dev machine)
- `integration`: `make test-int` with Docker available; runs all `//go:build integration` tests; Kafka + Elastic containers will start; Splunk will t.Skip unless env vars are injected as secrets
- Uses `actions/checkout@v4`, `actions/setup-go@v5`, pinned to `go-version: "1.26.1"` (go.mod directive)

`internal/agent/e2e_smoke_test.go` — Docker-free, no integration tag (runs in default suite):
- `TestE2ESmoke_IngestDeliverDrain`: injects fakeConnector via registry seam (established in agent_test.go, 04-05); builds WAL buffer with real drain func; feeds one signal.Batch via ingestCh; asserts fakeConnector received a batch with correct AppID/BatchID; triggers graceful drain (close(ingestCh) → wg.Wait → buffer.Flush → buffer.Close → disp.Close) without panic
- Placement rationale documented: cmd/argus-agent boot path is covered by build+vet CI job; manual smoke `go run ./cmd/argus-agent --config sample.yaml` documented in SUMMARY

## Verification Results

```
go build ./...                                                PASS
go vet ./...                                                  PASS
go vet -tags=integration ./internal/connector/kafka/          PASS
go vet -tags=integration ./internal/connector/elastic/        PASS
go vet -tags=integration ./internal/connector/splunk/         PASS
go test ./... -count=1 -timeout 300s                         PASS (19 packages; 0 failures)
go test ./internal/agent/... -run TestE2ESmoke                PASS
testcontainers in runtime packages                           NONE (clean)
```

Local `-race` not run (Windows, no C compiler) — CI is the authoritative race gate.

## Manual Smoke Command

To exercise the cmd/argus-agent boot path manually (not required for CI pass):

```bash
go run ./cmd/argus-agent --config sample.yaml
# Expects agent.yaml or ARGUS_SDK_* env vars; exits cleanly on Ctrl+C (SIGINT).
```

## Deviations from Plan

### Design Choice: e2e smoke in internal/agent not cmd/argus-agent

The plan offered two placements. Chose `internal/agent/e2e_smoke_test.go` because:
- The fake-connector injection seam (direct field access on `*Agent.registry`, `.nonOCSFTargets`, etc.) is not accessible from `package main`.
- The `agent.New` + `agent.Run` exported path unconditionally calls `factory.Build` → `c.Connect` which requires real infrastructure or mocked connectors.
- Moving the seam to an exported API would be a Rule 4 architectural change.
- The build+vet CI job already compiles `cmd/argus-agent` confirming the cmd boot path is sound.

This matches the plan's documented fallback: "note in the SUMMARY that cmd boot is covered by build+vet plus a documented manual run".

## Known Stubs

None — all integration tests and the smoke test are fully wired. The Splunk HEC test skips without env vars (by design), not because it is a stub.

## Threat Flags

- T-04-19 (Tampering — test dependency): `testcontainers-go` is the single new dependency; confirmed absent from runtime packages via `go build ./...` and package import scan.
- T-04-20 (DoS — CI race gate): CGO-enabled `-race` job in CI is the authoritative gate; all agent tests and the new smoke pass cleanly.
- T-04-21 (Repudiation — delivery contract): Kafka + Elastic error-path tests and Splunk bad-token test assert `failed` ack + non-nil error, preventing regression of F6/F7.

## Self-Check: PASSED

Files created:
- [x] internal/connector/kafka/connector_integration_test.go
- [x] internal/connector/elastic/connector_integration_test.go
- [x] internal/connector/splunk/connector_integration_test.go
- [x] internal/agent/e2e_smoke_test.go
- [x] .github/workflows/ci.yml

Commits verified:
- [x] a9af3e5 (feat(04-06): add Kafka + Elasticsearch integration tests)
- [x] 13b110c (feat(04-06): add Splunk HEC documented gated smoke test)
- [x] c4ca668 (feat(04-06): add CI workflow + e2e Docker-free smoke test)
