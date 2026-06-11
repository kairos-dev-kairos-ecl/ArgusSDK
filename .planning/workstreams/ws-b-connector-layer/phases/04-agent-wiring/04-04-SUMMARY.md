---
phase: 04-agent-wiring
plan: "04"
subsystem: collectors
tags: [grpc, euc, llm, signal, tdd]
dependency_graph:
  requires:
    - 04-03  # argusxdr gRPC connector (bufconn pattern reference)
    - 01-03  # signal.FromProto (pkg/signal)
  provides:
    - LLM gRPC SDKIngestService collector (SC-5)
    - EUC Observation-to-Signal fanOut (SC-6)
    - NewNoopOSCollector cross-platform seam
  affects:
    - internal/agent/agent.go  # will wire these collectors in 04-05
tech_stack:
  added: []
  patterns:
    - bufconn-based gRPC server testing
    - sync.Once for idempotent GracefulStop
    - crypto/rand hex SignalID (no ULID dep)
    - observationContext struct for typed ContextJSON encoding
key_files:
  created:
    - internal/collector/llm/llm_test.go
    - internal/collector/euc/euc_test.go
    - internal/collector/euc/euc_noop.go
  modified:
    - internal/collector/llm/llm.go
    - internal/collector/euc/euc.go
decisions:
  - "LLM collector exposes startOnListener seam so tests inject a bufconn listener without a real port"
  - "ingestServer embeds UnimplementedSDKIngestServiceServer by value per generated NOTE"
  - "EUC Layer=L9APIGateway — observations sit at the network/API-gateway boundary in the 10-layer taxonomy"
  - "SignalID uses crypto/rand 16-byte hex — avoids adding a ULID dependency (locked decision)"
  - "fanOut emits one signal.Batch per Observation — matches 1:1 boundary semantics; agent ingest loop batches further if needed"
  - "noopOSCollector emits nothing and returns nil on Close — satisfies end-to-end wiring on Windows where eBPF/WFP are out of scope"
metrics:
  duration_minutes: 20
  completed_date: "2026-06-11"
  tasks_completed: 2
  tasks_total: 2
  files_created: 3
  files_modified: 2
---

# Phase 04 Plan 04: LLM gRPC Collector + EUC Collector Summary

**One-liner:** LLM collector serves SDKIngestService.IngestBatch via gRPC with proto→signal conversion; EUC collector converts OS Observations to signal.Batch with crypto/rand SignalID and typed ContextJSON; NewNoopOSCollector wires end-to-end on any platform.

## Tasks Completed

| Task | Name | Commit (RED) | Commit (GREEN) | Files |
|------|------|-------------|----------------|-------|
| 1 | LLM gRPC SDKIngestService collector | 0359b84 | 6e079cf | llm.go, llm_test.go |
| 2 | EUC Observation→Signal fanOut + noop impl | 45d9349 | 9cc396b | euc.go, euc_noop.go, euc_test.go |

## What Was Built

### Task 1 — LLM Collector (SC-5)

`internal/collector/llm/llm.go` is no longer a scaffold. It registers an `SDKIngestService` gRPC server via `RegisterSDKIngestServiceServer` and emits `signal.Batch` on the out channel.

Key design choices:
- `ingestServer` embeds `sdkv1.UnimplementedSDKIngestServiceServer` by value (per generated NOTE).
- `IngestBatch` validates each signal (`SignalID != ""` and `Layer != LayerUnspecified`); invalid signals increment `RejectedCount` and are dropped (T-04-13 mitigation).
- Valid signals are converted via `signal.FromProto`; then `AppID`, `Env`, `SDKVersion` are set from the enclosing `SignalBatch` (FromProto leaves these empty by contract).
- A single `signal.Batch` is emitted per RPC call, respecting `ctx.Done`.
- `grpc.MaxRecvMsgSize` and `grpc.MaxConcurrentStreams` enforced at server creation (T-04-12 mitigation).
- `startOnListener(ctx, net.Listener, out)` seam allows tests to inject a bufconn listener without binding a real port.
- Unix socket listener is skipped on `runtime.GOOS == "windows"` as per proto package comment.
- `Close` uses `sync.Once` around `server.GracefulStop()` — safe to call multiple times.

Tests (4 total, Docker-free):
- `TestIngestBatch_HappyPath` — 2 valid signals → 1 batch on out, AcceptedCount=2, AppID/Env/SDKVersion populated
- `TestIngestBatch_Validation` — 2 invalid + 1 valid → AcceptedCount=1, RejectedCount=2
- `TestIngestBatch_AllRejected` — 1 invalid → RejectedCount=1, no batch on out
- `TestClose_Safe` — double Close does not panic

### Task 2 — EUC Collector (SC-6)

`internal/collector/euc/euc.go` `fanOut` body is fully implemented; no `TODO` remains in any function body.

`fanOut` converts each `Observation` to a `signal.Signal`:
- `Category`: `"euc.local_inference"` if `o.IsLocal`, else `"euc.ai_access"`
- `Layer`: `signal.L9APIGateway` — the network/API-gateway boundary layer in the 10-layer taxonomy
- `Severity`: `signal.SeverityInfo`
- `AppID`/`Env`: from `cfg`
- `SignalID`: 16-byte `crypto/rand` hex string (no ULID dep, per locked decision)
- `Timestamp`: `time.Now()`
- `ContextJSON`: JSON of `{connected_host, local_port, is_local, process_name, username}` (T-04-14: intentional Shadow AI observability capture)

Emits `signal.Batch{AppID, Env, Signals: []Signal{sig}}` per observation, respecting `ctx.Done`.

`internal/collector/euc/euc_noop.go` provides `noopOSCollector` (unexported) and the exported constructor `NewNoopOSCollector() OSCollector`. This is the canonical cross-platform seam — it satisfies "at least one OS impl wired end-to-end" on Windows where eBPF/WFP are out of scope.

Tests (5 total, Docker-free):
- `TestFanOut_AIAccess` — non-local observation → batch with Category=euc.ai_access, populated ContextJSON
- `TestFanOut_LocalInference` — local observation → Category=euc.local_inference
- `TestFanOut_CtxCancel` — context cancel → fanOut exits, no batch emitted
- `TestNoopOSCollector` — exported constructor, Start/Close no error, no observations emitted
- `TestCollector_EndToEnd_Noop` — full Collector with noop impl runs without error

## Deviations from Plan

None — plan executed exactly as written.

## Verification

```
go test ./internal/collector/... -count=1  → PASS (9 tests across llm + euc)
go build ./...                             → OK
go vet ./...                               → OK
grep -n "TODO" internal/collector/euc/euc.go → (no matches)
grep -n "RegisterSDKIngestServiceServer" internal/collector/llm/llm.go → line 59
grep -n "FromProto" internal/collector/llm/llm.go → line 135
```

## Known Stubs

None — all TODO function bodies have been replaced with working implementations.

## Threat Flags

No new threat surface beyond what is documented in the plan's STRIDE register:
- T-04-12 (DoS via MaxRecvMsgSize/MaxConcurrentStreams) — mitigated in grpc.NewServer
- T-04-13 (Tampering via proto→signal) — mitigated via SignalID/Layer validation
- T-04-14 (EUC ContextJSON info disclosure) — accepted; intentional Shadow AI observability capture

## Self-Check

- [x] `internal/collector/llm/llm.go` exists with RegisterSDKIngestServiceServer
- [x] `internal/collector/llm/llm_test.go` exists with 4 passing tests
- [x] `internal/collector/euc/euc.go` exists with no TODO in function bodies
- [x] `internal/collector/euc/euc_noop.go` exists with NewNoopOSCollector exported
- [x] `internal/collector/euc/euc_test.go` exists with 5 passing tests
- [x] Commits: 0359b84, 6e079cf, 45d9349, 9cc396b exist
