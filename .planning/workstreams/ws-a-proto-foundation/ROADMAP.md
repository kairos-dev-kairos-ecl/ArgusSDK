# Roadmap: WS-A — Protocol Foundation

## Overview

Define the wire protocol between instrumentation libraries and the argus-agent.
All other workstreams (B, C, D, E) are blocked on this — the proto definition is the
shared contract that the LLM gRPC collector, signal validation, and buffer serialisation
all depend on. SPEC-3 is locked: unary `SDKIngestService.IngestBatch`, slim `SDKSignal`,
`BatchAck` with per-signal rejection detail.

## Phases

- [x] **Phase 1: Proto + Codegen + Signal Alignment** - Write ingest.proto, configure buf codegen, align pkg/signal with proto field numbers

## Phase Details

### Phase 1: Proto + Codegen + Signal Alignment
**Goal**: Produce a compilable `proto/sdk/v1/ingest.proto` with full buf codegen pipeline generating Go stubs into `gen/go/sdk/v1/`, and align `pkg/signal/signal.go` with the proto field numbers and a `FromProto()` conversion function — so all downstream workstreams have a stable, importable wire contract.
**Depends on**: Nothing (first phase, all specs locked)
**Success Criteria** (what must be TRUE):
  1. `proto/sdk/v1/ingest.proto` exists and matches the locked SPEC-3 definition (SDKIngestService.IngestBatch unary, SignalBatch/SDKSignal/BatchAck/RejectionDetail messages)
  2. `buf generate` runs without errors and produces Go stubs in `gen/go/sdk/v1/`
  3. `go build ./...` passes with the new generated stubs imported — no import errors
  4. `pkg/signal/signal.go` has a `FromProto(p *sdkv1.SDKSignal) Signal` function that correctly maps all 14 proto fields to Signal struct fields
  5. Existing `signal.Batch` type has a `ToProto(batchID, sdkVersion string) *sdkv1.SignalBatch` conversion function

**Plans**: 3 plans

Plans:
- [x] 01-01: Write proto/sdk/v1/ingest.proto
- [x] 01-02: Add buf toolchain + Makefile codegen target
- [x] 01-03: Update pkg/signal with FromProto/ToProto conversions

## Progress

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 1. Proto + Codegen + Signal Alignment | 3/3 | Complete | 2026-05-25 |
