---
phase: "01-proto-codegen-signal-alignment"
plan: 1
subsystem: proto
tags: [proto, grpc, wire-contract, spec-3]
dependency_graph:
  requires: []
  provides:
    - proto/sdk/v1/ingest.proto
  affects:
    - gen/go/sdk/v1/ (consumed by Plan 01-02 buf generate)
    - pkg/signal/signal.go (consumed by Plan 01-03 FromProto/ToProto)
tech_stack:
  added: []
  patterns:
    - proto3 with google.protobuf.Timestamp for wall-clock fields
    - int32 for layer/severity (zero-cost cast; avoids cross-module enum import)
key_files:
  created:
    - proto/sdk/v1/ingest.proto
  modified: []
decisions:
  - "SPEC-3 field numbers LOCKED: SDKSignal fields 1-14, SignalBatch fields 1-5, BatchAck fields 1-4, RejectionDetail fields 1-2"
  - "layer and severity encoded as int32 (not imported enums) to avoid cross-module dependency on argus.v1"
  - "Unary RPC only (no streaming) — sub-millisecond Unix socket latency makes streaming unnecessary"
  - "Transport doc comment covers both Unix socket (Linux/macOS) and TCP 127.0.0.1:5002 (Windows fallback)"
metrics:
  duration: "~8 minutes"
  completed: "2026-05-24T20:41:43Z"
  tasks_completed: 1
  tasks_total: 1
  files_created: 1
  files_modified: 0
---

# Phase 01 Plan 01: Proto SDK Ingest Contract Summary

**One-liner:** proto3 wire contract (SDKIngestService, 14-field SDKSignal, SignalBatch/BatchAck/RejectionDetail) with SPEC-3 locked field numbers and google.protobuf.Timestamp for the timestamp field.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Create proto/sdk/v1/ingest.proto with locked SPEC-3 definition | 6e0c6ad | proto/sdk/v1/ingest.proto |

## What Was Built

`proto/sdk/v1/ingest.proto` defines the complete gRPC wire contract between Argus instrumentation libraries and the argus-agent process. It contains:

- **SDKIngestService** — single unary RPC `IngestBatch(SignalBatch) returns (BatchAck)`
- **SignalBatch** — 5 fields (batch_id, app_id, env, signals, sdk_version); field numbers 1-5 locked
- **SDKSignal** — 14 fields covering identity (signal_id through parent_span_id), classification (layer, category, severity), temporal (timestamp, duration_ms), payload (context_json), relationships (session_id, conversation_id, user_id), and source (app_version); field numbers 1-14 locked
- **BatchAck** — 4 fields echoing batch_id, accepted/rejected counts, per-signal rejection details
- **RejectionDetail** — 2 fields (signal_id, reason)

The file-level doc comment documents both transport options (Unix socket at OS-configured path for Linux/macOS; TCP loopback 127.0.0.1:5002 for Windows and as fallback) and the auth model (OS permissions on Unix socket; app_id registry validation on TCP path; no cryptographic auth on local hop).

## Verification Results

All automated checks passed:

- `grep -c "SDKIngestService|SignalBatch|SDKSignal|BatchAck|RejectionDetail"` → 17 (all 5 names present)
- `option go_package` → `github.com/kairos-dev-kairos-ecl/ArgusSDK/gen/go/sdk/v1;sdkv1` confirmed
- `app_version = 14` line found (confirms all 14 SDKSignal fields)
- `google.protobuf.Timestamp timestamp = 8` line found (confirms Timestamp type, not int64)
- Only one import: `google/protobuf/timestamp.proto`
- No `stream` keyword on IngestBatch RPC
- `protoc --proto_path=proto --proto_path=<include>` validation returned exit code 0

## Deviations from Plan

None - plan executed exactly as written.

## Known Stubs

None — this is a proto definition file, not application code. No placeholder values.

## Threat Flags

None — no new network endpoints, auth paths, or schema changes beyond what the threat model documents. T-01-01 (field number tampering) is mitigated by field numbers being declared in the committed file; buf breaking-change detection in Plan 01-02 will enforce this going forward.

## Self-Check

- [x] `proto/sdk/v1/ingest.proto` exists and is non-empty (5346 bytes)
- [x] Commit 6e0c6ad exists in git log
- [x] protoc validation passed (exit 0)
- [x] All 14 SDKSignal fields present (1-14, none skipped)
- [x] No streaming RPCs

## Self-Check: PASSED
