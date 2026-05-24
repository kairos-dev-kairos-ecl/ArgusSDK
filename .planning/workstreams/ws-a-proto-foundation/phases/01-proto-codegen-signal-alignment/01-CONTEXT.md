# Phase 1: Proto + Codegen + Signal Alignment — Context

**Gathered:** 2026-05-25
**Status:** Ready for planning
**Source:** SPEC-3 locked decisions from .planning/WORKSTREAMS.md (research complete, all specs locked)

<domain>
## Phase Boundary

This phase creates the wire protocol between instrumentation libraries and the argus-agent.
It delivers three artifacts:
1. `proto/sdk/v1/ingest.proto` — the locked proto definition
2. `buf generate` pipeline → `gen/go/sdk/v1/` Go stubs
3. `pkg/signal/signal.go` updated with `FromProto()` and `ToProto()` conversion functions

**What is NOT in scope:**
- gRPC server implementation (WS-E: Ingest Layer)
- Authentication (WS-B)
- Any output connector (WS-D)
- The argus-agent lifecycle wiring (WS-G)

</domain>

<decisions>
## Implementation Decisions

All decisions below are LOCKED from SPEC-3 research (see .planning/WORKSTREAMS.md).

### Proto Service Definition
- Service name: `SDKIngestService` (package `sdk.v1`)
- Single RPC: `rpc IngestBatch(SignalBatch) returns (BatchAck)`
- RPC is UNARY — not client-streaming, not bidirectional streaming
- Rationale: < 1ms Unix socket round-trip; natural backpressure via context deadline; simpler Python/TypeScript client implementation

### Proto Package + Go Package
- Proto package: `sdk.v1`
- Go package option: `github.com/kairos-dev-kairos-ecl/ArgusSDK/gen/go/sdk/v1;sdkv1`
- File location: `proto/sdk/v1/ingest.proto`
- Generated stubs location: `gen/go/sdk/v1/`

### SignalBatch Message (field numbers LOCKED)
- `string batch_id = 1` — ULID assigned by SDK lib; echoed back in BatchAck
- `string app_id = 2` — validated against agent app registry; unknown → PERMISSION_DENIED
- `string env = 3` — "dev" | "staging" | "prod"
- `repeated SDKSignal signals = 4` — recommended max 500
- `string sdk_version = 5`

### SDKSignal Message (field numbers LOCKED)
- Identity: `signal_id=1`, `trace_id=2`, `span_id=3`, `parent_span_id=4`
- Classification: `layer=5` (int32, 1–11), `category=6` (string), `severity=7` (int32, 1–5)
- Temporal: `timestamp=8` (google.protobuf.Timestamp), `duration_ms=9` (float)
- Payload: `context_json=10` (bytes — JSON-encoded layer context)
- Relationships: `session_id=11`, `conversation_id=12`, `user_id=13`
- Source: `app_version=14`
- Fields NOT included: enrichment, governance, provider metadata (populated by agent/pipeline, not SDK)

### BatchAck Message (field numbers LOCKED)
- `string batch_id = 1` — echoes SignalBatch.batch_id
- `int32 accepted_count = 2`
- `int32 rejected_count = 3`
- `repeated RejectionDetail rejections = 4` — present only when rejected_count > 0

### RejectionDetail Message
- `string signal_id = 1`
- `string reason = 2` — human-readable, e.g. "invalid layer: 0 (unspecified)", "context_json: invalid JSON"

### Protobuf Import
- Import `google/protobuf/timestamp.proto` for the `timestamp` field in SDKSignal
- No other proto imports needed (int32 enums are zero-cost casts of the argus.v1 enums, no import required)

### Enum Representation Decision
- Layer and Severity are represented as `int32` (NOT imported enums from argus.v1)
- Rationale: avoids cross-module import of `github.com/argusxdr/argus/gen/go/argus/v1`; values are numerically identical; documented in proto comments

### Auth Model (documented in proto, not implemented in this phase)
- Unix socket: OS file permissions (0660) only — no transport-layer auth
- TCP loopback: `app_id` validation against agent registry
- No cryptographic auth on local SDK→agent hop

### Windows Unix Socket Note
- Unix domain sockets not supported on Windows
- `llm.Config.UnixSocket` stays empty in Windows dev configs
- `Start()` skips `net.Listen("unix", path)` when `runtime.GOOS == "windows"` — document in proto comment only (implementation is WS-E)

### buf Toolchain
- `buf.yaml` at `proto/` directory (version: v2)
- `buf.gen.yaml` at `proto/` directory for Go codegen
- Generates: Go server stubs (`protoc-gen-go` + `protoc-gen-go-grpc`)
- Python/TypeScript codegen: documented in buf.gen.yaml but optional for now (Python/TS libs update is WS-G6)
- `make proto` Makefile target runs `buf generate`

### pkg/signal Alignment
- Add `FromProto(p *sdkv1.SDKSignal) Signal` function — maps all 14 proto fields
- Add `(b Batch) ToProto(batchID, sdkVersion string) *sdkv1.SignalBatch` function
- Field mapping is 1:1; `timestamp` is `google.protobuf.Timestamp` → `time.Time` via `p.Timestamp.AsTime()`
- `context_json` maps directly as `[]byte` — no conversion needed
- Existing `signal.Signal` and `signal.Batch` struct definitions stay unchanged (no field renames)

### go.mod Changes
- Add `google.golang.org/protobuf` v1.34+ (for generated stubs)
- Add `google.golang.org/grpc` v1.63+ (for generated gRPC stubs)
- Both are already in the project plan; first addition to go.mod

### Claude's Discretion
- buf.gen.yaml plugin versions (pick latest stable at time of generation)
- Whether to add a `tools.go` for buf pinning or just use PATH-installed buf
- Proto comment style (sentence case, period-terminated)
- Whether `gen/go/sdk/v1/` is gitignored or committed (recommend: commit generated stubs for reproducibility)

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Locked Proto Specification
- `.planning/WORKSTREAMS.md` — Section "WS-A A1 SDK Ingest Proto" contains the full locked proto definition with all field numbers, message names, and rationale

### Existing Code to Align With
- `pkg/signal/signal.go` — Current Signal and Batch struct definitions; FromProto/ToProto must map to these exact field names
- `go.mod` — Current module path (`github.com/kairos-dev-kairos-ecl/ArgusSDK`) and existing dependencies; new deps must be added here
- `internal/collector/llm/llm.go` — Shows how the generated stubs will be consumed (stub currently references `sdkv1.RegisterAgentIngestServiceServer` — the generated service must match this)

### External References
- OCSF research is NOT relevant to this phase (WS-D concern)
- EUC OS research is NOT relevant to this phase (WS-F concern)

</canonical_refs>

<specifics>
## Specific Ideas

- The proto file must include the full doc comment block explaining transport options (Unix socket vs TCP loopback) and the auth model — this is SDK-facing documentation
- `gen/go/sdk/v1/` should be committed (not gitignored) so dependents don't need to run `make proto` to build
- Add `proto/sdk/v1/` to `.gitignore` patterns to EXCLUDE — it must be tracked
- The `Makefile` target for `make proto` should fail loudly if `buf` is not installed (not silent)

</specifics>

<deferred>
## Deferred Ideas

- Python stub generation (`grpcio-tools`) — WS-G6 (SDK library updates)
- TypeScript stub generation (`ts-proto`) — WS-G6
- `argus.v1` proto vendoring for Mode 1 (ArgusXDR connector) — WS-B2
- gRPC server implementation that uses the generated stubs — WS-E

</deferred>

---

*Phase: 01-proto-codegen-signal-alignment*
*Context gathered: 2026-05-25 via SPEC-3 locked decisions*
