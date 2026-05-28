---
phase: 2
plan: "02-03"
subsystem: connector/kafka
tags: [kafka, franz-go, ocsf, sasl, tls, connector]
dependency_graph:
  requires: [02-01, 02-02]
  provides: [kafka-connector]
  affects: [internal/connector/kafka]
tech_stack:
  added:
    - github.com/twmb/franz-go v1.21.2 (kafka client)
    - github.com/twmb/franz-go/pkg/sasl/plain (PLAIN SASL)
    - github.com/twmb/franz-go/pkg/sasl/scram (SCRAM-SHA-256/512 SASL)
    - github.com/klauspost/compress v1.18.6 (transitive — snappy/zstd compression)
    - github.com/pierrec/lz4/v4 v4.1.26 (transitive — lz4 compression)
    - github.com/twmb/franz-go/pkg/kmsg v1.13.1 (transitive — kafka message types)
  patterns:
    - TDD red/green: tests committed before implementation
    - franz-go kgo.Client with synchronous produce (ProduceSync)
    - OCSF mapping via ocsf.Mapper inside Send() per signal
    - Partition key = InstanceID; headers x-argus-batch-id and x-argus-instance-id
    - TLS 1.3 enforced via connector.NewTLSConfig (never inline tls.Config)
key_files:
  created:
    - internal/connector/kafka/connector_test.go
  modified:
    - internal/connector/kafka/connector.go
    - go.mod
    - go.sum
decisions:
  - "Used franz-go (twmb/franz-go) per plan spec — kgo.ProduceSync for synchronous produce, client.Ping() for Health()"
  - "SASL packages (plain, scram) are part of the same franz-go module, fetched via go get"
  - "Send() skips unmappable signals rather than failing the entire batch — partial delivery preferred over full failure"
  - "RequiredAcks=0 in Config now maps to NoAck correctly (kgo.NoAck()) rather than being overridden to 1 by New()"
metrics:
  duration: "~15 minutes"
  completed: "2026-05-28"
  tasks_completed: 2
  files_changed: 4
---

# Phase 2 Plan 03: Kafka Connector (franz-go) Summary

**One-liner:** Full franz-go Kafka connector with OCSF JSON serialization, SASL/TLS, partition keying, and record headers.

## Tasks Completed

| Task | Description | Commit | Type |
|------|-------------|--------|------|
| 1 | Add franz-go dependency | 7b969f2 | chore |
| 2 (RED) | Kafka connector unit + integration tests | f7d6ce2 | test |
| 2 (GREEN) | Implement connector.go with franz-go | 8772fee | feat |

## What Was Built

### internal/connector/kafka/connector.go
Full implementation replacing all TODO stubs:

- **New()**: initializes `ocsf.Mapper` with `os.Hostname()` fallback, applies config defaults
- **Connect()**: builds `kgo.Client` opts — `SeedBrokers`, `ProducerBatchMaxBytes`, `RequiredAcks` (mapped from int to `kgo.AllISRAcks()`/`kgo.LeaderAck()`/`kgo.NoAck()`), optional TLS via `connector.NewTLSConfig`, optional SASL (PLAIN/SCRAM-SHA-256/SCRAM-SHA-512)
- **Send()**: maps each signal via `ocsf.Mapper.Map()`, JSON-marshals the `Event`, produces via `client.ProduceSync()` with `Key=[]byte(batch.InstanceID)` and headers `x-argus-batch-id`/`x-argus-instance-id`
- **Health()**: calls `client.Ping(ctx)` — no messages produced
- **Close()**: calls `client.Close()` when client is non-nil

### internal/connector/kafka/connector_test.go
Unit and integration tests (280 lines):
- `TestKafkaConnector_Name` — Name() = "kafka"
- `TestKafkaConnector_ConnectNoBrokers` — empty brokers returns error containing "broker"
- `TestKafkaConnector_SendMarshalOCSF` — OCSF JSON marshaling contract
- `TestKafkaConnector_SendPartitionKey` — partition key = batch.InstanceID
- `TestKafkaConnector_SendHeaders` — header key constants verified
- `TestKafkaConnector_OCSFJSONValid` — marshaled JSON contains class_uid
- `TestKafkaConnector_ConfigValidation` — constructor applies defaults
- `TestKafkaConnector_Close` — Close() before Connect() does not panic
- `TestKafkaConnector_Integration` — skipped when KAFKA_BROKERS unset; end-to-end with real broker

## Verification Results

```
go test ./internal/connector/kafka/... -v -count=1
=== RUN   TestKafkaConnector_Name           --- PASS
=== RUN   TestKafkaConnector_ConnectNoBrokers --- PASS
=== RUN   TestKafkaConnector_SendMarshalOCSF --- PASS
=== RUN   TestKafkaConnector_SendPartitionKey --- PASS
=== RUN   TestKafkaConnector_SendHeaders    --- PASS
=== RUN   TestKafkaConnector_OCSFJSONValid  --- PASS
=== RUN   TestKafkaConnector_ConfigValidation --- PASS
=== RUN   TestKafkaConnector_Close          --- PASS
=== RUN   TestKafkaConnector_Integration    --- SKIP (KAFKA_BROKERS not set)
PASS

go build ./... — BUILD OK
grep InsecureSkipVerify= internal/connector/kafka/connector.go — no matches
```

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Missing transitive go.sum entries after go get**
- **Found during:** Task 2 GREEN phase build
- **Issue:** `go build ./internal/connector/kafka/...` failed with missing go.sum entries for `klauspost/compress`, `pierrec/lz4`, `twmb/franz-go/pkg/kmsg` (transitive deps of franz-go's compression support)
- **Fix:** Ran `go mod tidy` which downloaded and recorded all transitive checksums
- **Files modified:** go.mod, go.sum
- **Commit:** 8772fee (included in feat commit)

**2. [Rule 2 - Missing critical functionality] RequiredAcks=0 silently overridden to 1**
- **Found during:** Task 2 GREEN phase implementation review
- **Issue:** The scaffold's `New()` function set `cfg.RequiredAcks = 1` when it was 0, making `NoAck` (0) unreachable. This is a correctness issue for callers that explicitly want fire-and-forget.
- **Fix:** Removed the `RequiredAcks == 0 → 1` override from `New()`. The `Connect()` switch correctly maps `0 → kgo.NoAck()` and defaults everything else to `kgo.LeaderAck()`.
- **Files modified:** internal/connector/kafka/connector.go

## Security Posture

All threat mitigations from the plan's threat model are implemented:

| Threat | Mitigation | Status |
|--------|-----------|--------|
| T-02-06: SASL credential logging | Config never passed to zap; only broker addresses referenced | Done |
| T-02-07: TLS tampering | TLS built via connector.NewTLSConfig (TLS 1.3, no InsecureSkipVerify) | Done |
| T-02-08: SASL identity spoofing | PLAIN/SCRAM-SHA-256/SCRAM-SHA-512 supported; unknown mechanism returns error | Done |

## Known Stubs

None. All method bodies are fully implemented.

## Threat Flags

No new security surface introduced beyond what is in the plan's threat model.

## Self-Check: PASSED

- [x] internal/connector/kafka/connector.go — exists, no TODO stubs
- [x] internal/connector/kafka/connector_test.go — exists, 280+ lines
- [x] go.mod contains github.com/twmb/franz-go v1.21.2
- [x] Commits 7b969f2, f7d6ce2, 8772fee — all exist in git log
- [x] go test ./internal/connector/kafka/... PASS
- [x] go build ./... BUILD OK
- [x] InsecureSkipVerify not assigned in connector.go
