---
phase: 04-agent-wiring
plan: "03"
subsystem: argusxdr-connector
tags: [grpc, tls, proto, connector, argusxdr]
dependency_graph:
  requires:
    - internal/connector/connector.go (SignalBatch, DeliveryAck, NewTLSConfig)
    - pkg/signal/signal.go (signal.Batch.ToProto)
    - gen/go/sdk/v1/ingest_grpc.pb.go (SDKIngestServiceClient, RegisterSDKIngestServiceServer)
  provides:
    - internal/connector/argusxdr/connector.go (gRPC IngestBatch client, Connect, Send, Health, Close, NewWithClient)
  affects: []
tech_stack:
  added: []
  patterns:
    - gRPC unary client with PerRPCCredentials (API-key as x-argus-api-key)
    - credentials.NewTLS from connector.NewTLSConfig (TLS 1.3)
    - metadata.NewOutgoingContext for per-RPC instance/group identity
    - bufconn in-process gRPC server for Docker-free unit tests
    - sequential abort-on-first-failure chunking (locked decision 9)
key_files:
  created:
    - internal/connector/argusxdr/connector_test.go
  modified:
    - internal/connector/argusxdr/connector.go
decisions:
  - apiKeyCreds.RequireTransportSecurity returns true when TLS enabled — prevents key over cleartext (T-04-07)
  - signal.Batch{Signals: batch.Signals}.ToProto() — AppID/Env intentionally empty until 04-05 adds those fields to connector.SignalBatch
  - Health probes with empty IngestBatch{BatchId: "health-check"} — lightweight, no signal marshalling needed
  - maxBatchSize defaults to 500 if unset; chunking is sequential abort-on-first-failure (locked decision 9)
  - UseOCSF silently ignored — argusxdr always uses proto wire format (locked decision 5, T-04-10)
metrics:
  duration: "~25 min"
  completed: "2026-06-11"
  tasks_completed: 2
  files_modified: 2
---

# Phase 4 Plan 03: argusxdr gRPC IngestBatch Summary

**One-liner:** gRPC IngestBatch client with TLS 1.3, per-RPC API-key creds, and InstanceID/GroupID in metadata — no OCSF, no new dependencies, 7 tests passing via bufconn.

## Tasks Completed

| Task | Name | Commit | Status |
|------|------|--------|--------|
| 1 (RED) | Failing tests for Connect/NewWithClient/Send | e83ddca | done |
| 2 (GREEN) | Full implementation — Connect, Send, Health, Close | 5576dfe | done |

## What Was Built

### `internal/connector/argusxdr/connector.go`

The scaffold's stub `Connect` (no-op) and `Send` (fake delivered ack) have been replaced with a complete gRPC client:

**Connector struct** — gained `conn *grpc.ClientConn` and `client sdkv1.SDKIngestServiceClient`.

**`Connect`** — guards empty Endpoint; when `TLS.Enabled`, builds transport credentials via `connector.NewTLSConfig` (enforces TLS 1.3 minimum per `tls.go`) wrapped with `credentials.NewTLS`; falls back to `insecure.NewCredentials()` for loopback/test. Installs `apiKeyCreds` via `grpc.WithPerRPCCredentials`.

**`apiKeyCreds`** — implements `credentials.PerRPCCredentials`; returns `{"x-argus-api-key": credential}`; `RequireTransportSecurity()` returns `true` when TLS is enabled (mitigates T-04-07: prevents API key leakage over cleartext).

**`NewWithClient`** — package-internal test constructor that injects a pre-built `SDKIngestServiceClient`, bypassing real gRPC dialing entirely.

**`Send`** — nil-client guard → failed ack + error. Reconstructs `signal.Batch{Signals: batch.Signals}` (AppID/Env left as `""` until plan 04-05 adds those fields to `connector.SignalBatch` — never reads `Signals[0].AppID` per the scope fence). Calls `ToProto(batch.BatchID, "argus-sdk/4")` for the proto body. Attaches `x-argus-instance-id` and `x-argus-group-id` via `metadata.NewOutgoingContext` before the RPC (T-04-11: identity in metadata not proto body). Chunks by `MaxBatchSize` (default 500) with sequential abort-on-first-failure. gRPC error → failed ack + wrapped error (T-04-09). `UseOCSF` silently ignored (T-04-10).

**`Health`** — calls `IngestBatch` with an empty `SignalBatch{BatchId: "health-check"}`.

**`Close`** — calls `conn.Close()` if non-nil.

### `internal/connector/argusxdr/connector_test.go`

7 tests using an in-process `bufconn` server — no Docker, no external ports:

| Test | What It Verifies |
|------|-----------------|
| `TestConnect_EmptyEndpointReturnsError` | Guard on empty Endpoint |
| `TestConnect_InsecureDialSucceeds` | Insecure dial produces usable client |
| `TestNewWithClient_NonNilClient` | Test constructor wires client stub |
| `TestSend_HappyPath` | Delivered ack; 2 signals received by server; AppId/Env are `""` (not from Signals[0]); `x-argus-instance-id`/`x-argus-group-id` in incoming metadata |
| `TestSend_ErrorPathReturnsFailedAck` | gRPC Unavailable → non-nil error + `status=failed` |
| `TestSend_NilClientReturnsFailedAck` | Nil client guard |
| `TestSend_UseOCSFIgnored` | `UseOCSF=true` does not affect proto path |

## Verification Results

```
ok  github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector/argusxdr  0.213s (7 tests)
go build ./...  — PASS (no output)
go vet ./...    — PASS (no output)
```

Acceptance criteria grep checks:
- `grep -c "NewTLSConfig"` → 2 (non-zero)
- `grep -c "WithPerRPCCredentials"` → 2 (non-zero)
- `grep -c "ToProto"` → 1 (non-zero)
- `grep -c "NewOutgoingContext"` → 1 (non-zero)

## Deviations from Plan

None — plan executed exactly as written.

**Note on AppID/Env:** The plan explicitly states "use empty strings for AppId/Env in ToProto since the fields don't exist yet on connector.SignalBatch." The implementation does exactly this: `signal.Batch{Signals: batch.Signals}.ToProto(...)` with no AppID/Env set. Plan 04-05 will add those fields to `connector.SignalBatch` and wire them in the ingest loop.

## Threat Mitigations Applied

| Threat | Mitigation |
|--------|-----------|
| T-04-07 Spoofing / API key leakage | `apiKeyCreds.RequireTransportSecurity()==true` when TLS enabled; key never sent over cleartext |
| T-04-08 Information disclosure | TLS 1.3 via `connector.NewTLSConfig` → `credentials.NewTLS`; insecure only for loopback tests |
| T-04-09 Repudiation (silent drop) | gRPC error → non-nil error + `Status:"failed"` ack; no fake delivered |
| T-04-10 OCSF bypass | `UseOCSF` silently ignored; only `ToProto` path exists |
| T-04-11 Instance/group spoofing | InstanceID/GroupID in per-RPC metadata over TLS-secured channel; not in proto body |

## Known Stubs

None — the scaffold stubs have been fully replaced.

## Threat Flags

None — no new network endpoints, auth paths, or schema changes beyond what is in the plan's threat model.

## TDD Gate Compliance

- RED gate: `test(04-03)` commit e83ddca — 7 failing tests written before implementation.
- GREEN gate: `feat(04-03)` commit 5576dfe — all 7 tests pass after implementation.
- REFACTOR gate: not needed — no cleanup required.

## Self-Check: PASSED

- `internal/connector/argusxdr/connector.go` — EXISTS
- `internal/connector/argusxdr/connector_test.go` — EXISTS
- RED commit e83ddca — FOUND
- GREEN commit 5576dfe — FOUND
