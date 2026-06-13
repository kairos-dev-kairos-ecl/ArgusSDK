# Contributing to ArgusSDK

Thanks for your interest in improving ArgusSDK. This guide covers how to build,
test, and submit changes.

## Prerequisites

- **Go 1.26.1+** (matches `go.mod`; the Docker build pins the same toolchain).
- **Docker** — only for integration tests (`make test-int`).
- **buf** — only if you regenerate gRPC stubs (`make proto`).
- A C toolchain is **not** required for normal development. The Linux eBPF
  objects are precompiled and committed; the agent itself builds with
  `CGO_ENABLED=0`.

## Build, test, lint

```bash
make build       # compile for the current platform
make test        # unit tests (fast, no external services)
make test-int    # integration tests (spins up Kafka/Elasticsearch via Docker)
make build-all   # cross-compile linux/windows/darwin
make lint        # golangci-lint
make help        # list all targets
```

Before opening a pull request, make sure the following are clean:

```bash
go build ./...
go vet ./...
go test ./...
```

CI additionally runs the race detector and the cross-compile/dependency-isolation
matrix (see `.github/workflows/ci.yml`).

## Project layout

The package boundaries are described in the [README](README.md#directory-structure).
A few conventions worth knowing:

- **`internal/`** is private to the agent. The only public surface is
  `pkg/signal` (the signal types the instrumentation libraries depend on) and
  the `proto/` schema.
- **Platform code is build-tag isolated.** `cilium/ebpf` appears only in
  `//go:build linux` files; `0xrawsec/golang-etw` only in `//go:build windows`.
  CI asserts these heavy dependencies never leak into the wrong target. Do not
  import a platform package outside its build tag.
- **Config keys are bound via `mapstructure` tags.** If you add a config field,
  add the tag and update `config/agent.example.yaml`. The
  `TestExampleConfigBinds` drift guard fails if the example documents a key the
  code does not read.
- **The EUC collector honors a low-privilege contract** — no process
  enumeration, file monitoring, full packet capture, or payload inspection.
  Keep it that way.

## Testing conventions

- Unit tests run with no network or external services and must pass on all three
  platforms (use `t.Skip` for platform-specific paths).
- Integration tests live alongside the connector they exercise
  (`*_integration_test.go`) and are gated behind the `integration` build tag.
- The end-to-end harness in `test/llmsignal/` exercises the full ingest →
  dispatch → output path; see its [README](test/llmsignal/README.md).

## Commit and PR guidelines

- Keep commits focused; write a clear subject line in the imperative mood.
- Include tests for behavior changes. Bug fixes should add a regression test.
- Update `CHANGELOG.md` under a new "Unreleased" section for user-visible changes.
- Make sure `go build ./...`, `go vet ./...`, and `go test ./...` pass locally.

## Reporting security issues

Please do not open public issues for security vulnerabilities. See
[SECURITY.md](SECURITY.md) for responsible disclosure instructions.
