---
phase: "01-proto-codegen-signal-alignment"
plan: 2
subsystem: proto-codegen
tags: [buf, grpc, codegen, makefile, go-stubs]
dependency_graph:
  requires:
    - proto/sdk/v1/ingest.proto (Plan 01-01)
  provides:
    - proto/buf.yaml
    - proto/buf.gen.yaml
    - Makefile
    - gen/go/sdk/v1/ingest.pb.go
    - gen/go/sdk/v1/ingest_grpc.pb.go
  affects:
    - pkg/signal/signal.go (consumed by Plan 01-03 FromProto/ToProto)
    - internal/connector/llm/llm.go (uses RegisterSDKIngestServiceServer from generated stubs)
tech_stack:
  added:
    - google.golang.org/protobuf v1.34.2 (Go protobuf message stubs runtime)
    - google.golang.org/grpc v1.64.0 (Go gRPC stubs runtime — upgraded from 1.63 to satisfy generated stub requirement for SupportPackageIsVersion9)
  patterns:
    - buf v2 remote plugins (buf.build/protocolbuffers/go, buf.build/grpc/go)
    - paths=source_relative with out: ../gen/go (not ../gen/go/sdk/v1 — buf appends proto subdir automatically)
    - Generated stubs committed to repository for reproducibility
key_files:
  created:
    - proto/buf.yaml
    - proto/buf.gen.yaml
    - Makefile
    - gen/go/sdk/v1/ingest.pb.go
    - gen/go/sdk/v1/ingest_grpc.pb.go
  modified:
    - go.mod (added protobuf v1.34.2 and grpc v1.64.0)
    - go.sum (updated by go mod tidy)
decisions:
  - "buf v2 modules array with path: . (relative to proto/ directory); 'against' field removed — breaking detection uses FILE ruleset, CLI flag for baseline"
  - "out: ../gen/go (not ../gen/go/sdk/v1) — paths=source_relative appends the proto file's directory structure (sdk/v1/) automatically, producing gen/go/sdk/v1/ingest.pb.go"
  - "google.golang.org/grpc upgraded to v1.64.0 (from plan's v1.63.2) because generated stubs from protoc-gen-go-grpc v1.6.2 require grpc.SupportPackageIsVersion9 which was added in v1.64.0"
  - "Generated stubs committed to repository per CONTEXT.md decision for reproducibility"
  - "Python/TS plugin entries deferred to WS-G6 — commented placeholder block in buf.gen.yaml"
metrics:
  duration: "~15 minutes"
  completed: "2026-05-24T20:55:46Z"
  tasks_completed: 2
  tasks_total: 2
  files_created: 5
  files_modified: 2
---

# Phase 01 Plan 02: buf Codegen Pipeline Summary

**One-liner:** buf v2 codegen pipeline producing Go gRPC stubs (ingest.pb.go + ingest_grpc.pb.go) in gen/go/sdk/v1/ via remote plugins, with a Makefile proto target that fails loudly if buf is not installed.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Write proto/buf.yaml and proto/buf.gen.yaml | c6b4d7c | proto/buf.yaml, proto/buf.gen.yaml |
| 2 | Write Makefile with proto target and run buf generate | a0ff288 | Makefile, gen/go/sdk/v1/ingest.pb.go, gen/go/sdk/v1/ingest_grpc.pb.go, go.mod, go.sum |

## What Was Built

**`proto/buf.yaml`** — buf v2 module config for the proto/ directory. Declares version v2, a modules entry with `path: .` (proto/ relative), DEFAULT lint ruleset, and FILE breaking rules. The `against` field from the initial draft was removed as it is not valid in buf.yaml — breaking baseline is specified as a CLI flag (`buf breaking --against ...`).

**`proto/buf.gen.yaml`** — codegen pipeline config with two remote plugins:
- `buf.build/protocolbuffers/go` → `../gen/go` with `paths=source_relative`
- `buf.build/grpc/go` → `../gen/go` with `paths=source_relative`

Commented placeholder block documents where Python (grpcio-tools) and TypeScript (ts-proto) plugin entries will be added when WS-G6 runs. No active Python or TypeScript entries.

**`Makefile`** — repo root Makefile with all G5 targets:
- `proto`: buf guard (`command -v buf`) fails with exit 1 and error message if buf not on PATH; then `cd proto && buf generate`
- `build` / `build-all` (cross-compile: linux/amd64, linux/arm64, windows/amd64, darwin/arm64)
- `test` / `test-int` (integration with -tags=integration)
- `lint` (golangci-lint)
- `docker` (TODO stub for WS-G5)
- `install` (go install ./cmd/argus)

**`gen/go/sdk/v1/ingest.pb.go`** — generated Go protobuf stubs for all messages (SignalBatch, SDKSignal, BatchAck, RejectionDetail). Package `sdkv1`, module path `github.com/kairos-dev-kairos-ecl/ArgusSDK/gen/go/sdk/v1`.

**`gen/go/sdk/v1/ingest_grpc.pb.go`** — generated Go gRPC stubs exposing `SDKIngestServiceClient`, `SDKIngestServiceServer`, `RegisterSDKIngestServiceServer`, and `UnimplementedSDKIngestServiceServer`.

## Verification Results

All checks passed:

- `make proto`: exits 0, produces/updates gen/go/sdk/v1/
- `grep "package sdkv1" gen/go/sdk/v1/ingest.pb.go`: found
- `grep "RegisterSDKIngestServiceServer" gen/go/sdk/v1/ingest_grpc.pb.go`: found
- `go build ./...`: exits 0 (no import errors)
- `grep "command -v buf" Makefile`: found (fail-loud guard)
- `grep "buf generate" Makefile`: found
- `grep "^.PHONY" Makefile`: found with all targets listed
- `grep "google.golang.org/protobuf" go.mod`: v1.34.2
- `grep "google.golang.org/grpc" go.mod`: v1.64.0

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Removed invalid `against` field from buf.yaml**
- **Found during:** Task 1 (buf generate failed with "field against not found")
- **Issue:** buf v2 does not support an `against` field inside the `breaking:` block of buf.yaml. Breaking change baseline is specified as a CLI flag: `buf breaking --against git#branch=main`.
- **Fix:** Removed the `against:` block from buf.yaml. The FILE breaking ruleset is still declared; the git baseline is applied at CLI invocation time.
- **Files modified:** proto/buf.yaml
- **Commit:** a0ff288

**2. [Rule 1 - Bug] Corrected buf.gen.yaml output path from `../gen/go/sdk/v1` to `../gen/go`**
- **Found during:** Task 2 (generated files landed at gen/go/sdk/v1/sdk/v1/ingest.pb.go)
- **Issue:** With `paths=source_relative`, buf appends the proto file's module-relative path (`sdk/v1/`) to the `out` directory. Setting `out: ../gen/go/sdk/v1` produced `gen/go/sdk/v1/sdk/v1/ingest.pb.go`. The correct `out` is `../gen/go` so buf produces `gen/go/sdk/v1/ingest.pb.go`.
- **Fix:** Changed both plugin `out:` values from `../gen/go/sdk/v1` to `../gen/go`.
- **Files modified:** proto/buf.gen.yaml
- **Commit:** a0ff288

**3. [Rule 1 - Bug] Upgraded grpc from v1.63.2 to v1.64.0**
- **Found during:** Task 2 (`go build ./...` failed with `undefined: grpc.SupportPackageIsVersion9`)
- **Issue:** The remote `buf.build/grpc/go` plugin generates stubs using protoc-gen-go-grpc v1.6.2, which requires `grpc.SupportPackageIsVersion9`. This constant was introduced in grpc v1.64.0. The plan specified v1.63.2 which predates the constant.
- **Fix:** Upgraded `google.golang.org/grpc` to v1.64.0 via `go get`.
- **Files modified:** go.mod, go.sum
- **Commit:** a0ff288

## Known Stubs

None — Makefile `docker` target has a TODO comment for WS-G5 but this does not affect the plan's goal of establishing the codegen pipeline. The proto target is fully functional.

## Threat Flags

None — no new network endpoints or auth paths beyond what the plan's threat model documents. T-02-02 (go get supply chain) is mitigated by go.sum checksum verification. T-02-03 (silent buf failure) is mitigated by the `command -v buf` guard in the Makefile proto target.

## Self-Check

- [x] `proto/buf.yaml` exists with version: v2 and modules entry
- [x] `proto/buf.gen.yaml` exists with protoc-gen-go and protoc-gen-go-grpc plugins
- [x] `Makefile` exists at repo root with proto, build, test, lint targets
- [x] `gen/go/sdk/v1/ingest.pb.go` exists (generated by buf generate)
- [x] `gen/go/sdk/v1/ingest_grpc.pb.go` exists (generated by buf generate)
- [x] Commit c6b4d7c exists in git log (Task 1)
- [x] Commit a0ff288 exists in git log (Task 2)
- [x] `go build ./...` passes (exit 0)
- [x] `make proto` exits 0 (buf installed, stubs regenerated)
- [x] `package sdkv1` and `RegisterSDKIngestServiceServer` found in generated stubs

## Self-Check: PASSED
