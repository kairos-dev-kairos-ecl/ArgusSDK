---
phase: "01-proto-codegen-signal-alignment"
plan: 3
subsystem: "signal-conversion"
tags: ["proto", "signal", "fromproto", "toproto", "codegen"]
dependency_graph:
  requires:
    - "01-01"  # proto schema definition
    - "01-02"  # buf codegen pipeline (gen/go/sdk/v1/ stubs)
  provides:
    - "FromProto field mapping (used by WS-E gRPC handler)"
    - "ToProto field mapping (used by SDK lib testing and batch dispatch)"
  affects:
    - "internal/collector/llm/llm.go (future — WS-E wires signal.FromProto in IngestBatch handler)"
tech_stack:
  added: []
  patterns:
    - "Proto field-mapper functions (FromProto/ToProto) in pkg/signal as single authoritative mapping"
    - "TDD RED/GREEN cycle with stdlib testing package"
    - "Nil-guard on proto Timestamp message field (proto3 optional semantics)"
key_files:
  created:
    - pkg/signal/signal_test.go
  modified:
    - pkg/signal/signal.go
decisions:
  - "FromProto does not set AppID/Env/SDKVersion — these come from SignalBatch, not SDKSignal; caller responsibility"
  - "toProtoSignal is package-private (lowercase) — only Batch.ToProto needs to call it; not part of public API"
  - "Nil Timestamp guard: if p.Timestamp != nil before .AsTime() — matches T-03-03 threat mitigation"
  - "Task 2 (llm.go registration fix): no change needed — file has only TODO comments, no incorrect Register* call"
metrics:
  duration: "~8 minutes"
  completed: "2026-05-25"
  tasks_completed: 2
  files_changed: 2
---

# Phase 01 Plan 03: Proto-to-Signal Conversion Functions Summary

**One-liner:** Added `FromProto`/`ToProto` field-mapper functions to `pkg/signal/signal.go` using TDD, mapping all 14 SDKSignal proto fields to the internal Signal type with nil-timestamp guard.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 (RED) | Add failing tests for FromProto/ToProto | 46f4b9c | pkg/signal/signal_test.go |
| 1 (GREEN) | Implement FromProto and ToProto | ce23786 | pkg/signal/signal.go |
| 2 | Fix service registration in llm.go | (no change needed — documented below) | — |

## What Was Built

### `pkg/signal/signal.go` additions

Three new functions added after the existing `Batch` type definition (struct definitions are byte-for-byte unchanged):

**`FromProto(p *sdkv1.SDKSignal) Signal`**
- Maps all 14 SDKSignal fields to internal Signal fields
- Guards `p.Timestamp != nil` before calling `.AsTime()` — returns `time.Time{}` if nil (mitigates T-03-03)
- Intentionally leaves `AppID`, `Env`, `SDKVersion` as zero values — these come from `SignalBatch`, not `SDKSignal`

**`toProtoSignal(s Signal) *sdkv1.SDKSignal`** (package-private)
- Reverse mapping for all 14 fields
- Converts `s.Timestamp` via `timestamppb.New(s.Timestamp)`
- Converts `s.Layer` and `s.Severity` via `int32(...)` cast

**`(b Batch) ToProto(batchID, sdkVersion string) *sdkv1.SignalBatch`**
- Converts full Batch to proto SignalBatch
- Iterates `b.Signals` calling `toProtoSignal` for each element
- Sets `BatchId`, `AppId`, `Env`, `SdkVersion` from parameters and batch fields

### `pkg/signal/signal_test.go`

Test file `TestFromProtoRoundTrip` with 4 subtests (stdlib `testing` package, white-box `package signal`):

1. `AllFieldsPopulated` — all 14 fields mapped in identity round-trip
2. `NilTimestamp` — no panic when `p.Timestamp == nil`, returns `time.Time{}`
3. `BatchToProto` — round-trip via `b.ToProto("batch-123", "1.0.0")`
4. `NoAppIDEnvSDKVersion` — confirms AppID/Env/SDKVersion NOT set by FromProto

### Task 2: `internal/collector/llm/llm.go` service registration

No change was needed. The file contains only TODO comments for the registration call — it does not reference `RegisterAgentIngestServiceServer` (the incorrect name that would have required fixing). The correct function name `RegisterSDKIngestServiceServer` exists in `gen/go/sdk/v1/ingest_grpc.pb.go` and will be used when WS-E implements the full gRPC handler.

## Verification Results

```
go test ./pkg/signal/... -v -run TestFromProto
  --- PASS: TestFromProtoRoundTrip/AllFieldsPopulated
  --- PASS: TestFromProtoRoundTrip/NilTimestamp
  --- PASS: TestFromProtoRoundTrip/BatchToProto
  --- PASS: TestFromProtoRoundTrip/NoAppIDEnvSDKVersion
PASS

go build ./...   # exits 0 (full module compiles cleanly)
go build ./internal/collector/llm/...   # exits 0
```

## Deviations from Plan

**Task 2 — No change made (by plan design)**

The plan explicitly states: "If the stub already has a TODO comment where the registration call will go, or uses the correct name: make no change and document in SUMMARY."

`internal/collector/llm/llm.go` has only TODO comments (`// TODO: create grpc.Server, register IngestServiceServer, listen on GRPCAddr`). It does not reference `RegisterAgentIngestServiceServer` or any incorrect registration name. No modification was required.

## Threat Model Coverage

| Threat | Disposition | Result |
|--------|-------------|--------|
| T-03-01: UserID info disclosure | accept | UserID passes through as raw string — SDK lib hashes before send |
| T-03-02: ContextJSON tampering | accept | Validation is WS-E responsibility; FromProto is a pure mapper |
| T-03-03: nil Timestamp panic (DoS) | mitigate | `if p.Timestamp != nil` guard present — tested by Test 2 |
| T-03-SC: No new packages | accept | Only adds import of already-present google.golang.org/protobuf |

## Self-Check: PASSED

- [x] `pkg/signal/signal.go` exists and contains `func FromProto`
- [x] `pkg/signal/signal_test.go` exists with 4 subtests
- [x] Commit `46f4b9c` (RED test) exists
- [x] Commit `ce23786` (GREEN implementation) exists
- [x] `go test ./pkg/signal/... -v -run TestFromProto` — all 4 subtests PASS
- [x] `go build ./...` — exits 0
- [x] Signal/Batch struct definitions unchanged (byte-for-byte identical)
- [x] `AppID`, `Env`, `SDKVersion` not assigned inside `FromProto`
