# ArgusSDK v1.0 Release — Workstreams

This document ties the three workstreams (WS-A, WS-B, WS-C) to the v1.0 release milestone. All three converge on a production-ready signal collection agent.

---

## WS-A: Proto Foundation (Completed)

**Goal:** Establish the base proto schema, codegen pipeline, and signal types for the agent.

**Key Deliverables:**
- `ArgusSignal` v1 protobuf schema in `proto/sdk/v1/signal.proto`
- `IngestBatch` gRPC service definition in `proto/sdk/v1/ingest.proto`
- buf CLI codegen pipeline (`proto/buf.gen.yaml`)
- Generated Go stubs committed to `gen/go/sdk/v1/` for reproducibility
- Public signal types in `pkg/signal/` (types.go, conversion, validation)
- Instrumentation library contracts (Python/TypeScript)

**Status:** ✅ **COMPLETE** (Phase 1-4, 3/3 plans done)  
**Latest:** `docs(phase-01): complete phase execution — all 3 plans done, verification passed` (0c6a23f, 2026-06-10)

**Commits:**
- `01-01`: proto schema + codegen pipeline
- `01-02`: buf codegen CI integration
- `01-03`: signal type conversions (FromProto/ToProto)

---

## WS-B: Connector Layer (Completed)

**Goal:** Implement the full connector architecture, OCSF mapper, and output destinations (Kafka, Splunk, Elastic, Syslog, ArgusXDR).

**Key Deliverables:**
- `internal/connector/` interfaces and registry
- OCSF v1.3 mapper (`internal/ocsf/`)
- Kafka connector (franz-go, SASL-SCRAM, OCSF)
- Splunk HEC connector (OCSF)
- Elasticsearch connector (NDJSON /_bulk, OCSF)
- Syslog CEF connector (TCP/TLS 1.3)
- ArgusXDR native proto connector (gRPC IngestBatch)
- WAL-backed local signal buffer (`internal/buffer/`)
- Delivery contracts, resilience patterns, circuit breakers
- LLM gRPC collector (`internal/collector/llm/`)
- EUC OS-level collector (`internal/collector/euc/`) — Linux/Windows/macOS stubs
- Full agent lifecycle (start/stop, ingest loop, drain, registration)
- Integration tests (testcontainers Kafka, Elasticsearch)
- GitHub Actions CI pipeline with -race gate

**Status:** ✅ **COMPLETE** (Phase 2-4, 16/16 plans done)  
**Latest:** `feat(04-06): Kafka/Elastic/Splunk integration + CI -race gate + smoke` (c4ca668, 2026-06-12)

**Phases:**
- Phase 2: Connector layer (6 plans) — OCSF mapper, TLS, signal conversion, Kafka, Elastic, WAL
- Phase 3: Buffer hardening (4 plans) — WAL delivery contract, dispatch, injection, dryrun alignment
- Phase 4: Agent wiring (6 plans) — factory, syslog, argusxdr, collectors, start/stop, integration

---

## WS-C: v1.0 Release Hardening (In Progress)

**Goal:** Finalize documentation, configuration, observability, and testing to produce a releasable v1.0 artifact.

**Key Deliverables:**

### Phase 5: XDR Mode-1 Registration & Credential Lifecycle ✅ COMPLETE
- HTTP registration contract (auth.go)
- Instance ID assignment from XDR
- Credential refresh loop
- Local mode registrar (simplified, no XDR required)
- Mock XDR server in tests

### Phase 6: EUC OS Collectors ✅ COMPLETE
- **Linux:** eBPF-based DNS/TCP + local inference port detection
- **Windows:** ETW-based network telemetry
- **macOS:** gopsutil process sampler (no-root; full Network Extension deferred)
- Unified collector interface
- Hot-reload watch list

### Phase 7: Release Hardening (IN PROGRESS)

#### Wave 1 ✅ COMPLETE
- **07-01:** OCSF fidelity improvements
  - Injectable clock on Mapper (testability)
  - First-class WebResources/Databucket on Event
  - Honest HTTP URL (never s.Category)
- **07-02:** SIGHUP hot-reload
  - Bounded reload (EUC watch list + log level only)
  - AtomicLevel for live log changes
  - Signal handler in agent.Run()

#### Wave 2 (Current)
- **07-03:** Config consolidation, docs sync, Makefile fixes, milestone record
  - Canonical `config/agent.example.yaml` with all sections (agent, auth, ingest, buffer, outputs, tls, logging, observability)
  - README tech-stack (remove unused ulid/prometheus, add actual deps)
  - Directory structure documentation (euc, factory, observability)
  - Quick-start validation
  - Observability section (liveness/readiness/metrics)
  - Deployment section (XDR flow, credential refresh, hot-reload)
  - Makefile: `docker` → Dockerfile, `install` → ./cmd/argus-agent
  - `.planning/WORKSTREAMS.md` (this file) — v1.0 milestone record
- **07-04:** CI completeness (parallel, non-critical)
  - GitHub Actions v1.0 release checklist
  - go test -race, go vet, go build
  - Docker image build validation
- **Phase 7 Verification:** End-to-end v1.0 readiness audit

**Status:** 🟡 **IN PROGRESS** (Phase 5-7, 9/11 plans done, 82% complete)  
**Latest:** WS-B Phase 4 complete (c4ca668, 2026-06-12); WS-C Phase 7 Wave 1 merged

---

## v1.0 Release Scope

### What Ships
- **Agent binary** (`cmd/argus-agent`) — config-driven, standalone
- **Two output modes:**
  - Mode 1: ArgusXDR (native `ArgusSignal` proto)
  - Mode 2: External SIEM (Kafka, Splunk, Elasticsearch, Syslog via OCSF v1.3 translation)
- **Local vs Remote:**
  - Remote: XDR registration, three-part auth, TLS 1.3 mandatory
  - Local: loopback/Unix socket, simplified auth
- **Collectors:**
  - LLM: gRPC ingest from Python/TypeScript instrumentation libraries
  - EUC (End User Computing): AI service shadow observability
    - Linux: eBPF DNS + TCP + local inference ports
    - Windows: ETW network telemetry
    - macOS: gopsutil process sampler (no-root)
- **Resilience:**
  - WAL-backed local buffer (configurable disk quota, flush on reconnect)
  - Circuit breaker (open/half-open/closed)
  - Exponential backoff with jitter on reconnect
- **Observability:**
  - Liveness/readiness HTTP probes
  - Prometheus metrics endpoint
  - Structured logging (JSON or console)
  - Hot-reload via SIGHUP
- **Configuration:**
  - YAML + environment variable overrides
  - Canonical example file with all sections

### What Defers to v2.0+
- macOS Network Extension (signed .app required — not shippable via `go install`)
- Prometheus `client_golang` observability (HTTP server only in v1.0)
- ULID signal IDs (using crypto/rand instead)
- Advanced SIEM connectors beyond Kafka/Splunk/Elastic/Syslog

---

## Dependencies Across Workstreams

```
WS-A (Proto Foundation)
    ↓
    └──→ WS-B (Connector Layer)
            ├── Uses ArgusSignal + IngestBatch from WS-A
            └── Produces agent binaries + connectors
    ↓
    └──→ WS-C (Release Hardening)
            ├── Uses complete agent from WS-B
            ├── Adds observability, docs, XDR registration, EUC OS collectors
            └── Ships v1.0 release artifact
```

---

## Verification Checklist for v1.0 Release

- [ ] `make build` succeeds on all supported platforms (Linux/Windows/macOS amd64/arm64)
- [ ] `make install` produces working binary in GOPATH/bin
- [ ] `make test` passes (unit tests, -race gate, coverage >80%)
- [ ] `make test-int` passes (testcontainers Kafka/Elasticsearch)
- [ ] `make docker` builds distroless image
- [ ] Config example (`config/agent.example.yaml`) parses with viper
- [ ] README quick-start commands execute end-to-end
- [ ] GitHub Actions CI builds, tests, lints
- [ ] Observability endpoints (/health, /ready, /metrics) respond
- [ ] SIGHUP hot-reload triggers without restart
- [ ] Buffer drains on connector recovery
- [ ] OCSF mapper produces valid events (dryrun validation)
- [ ] EUC collector runs without errors on each OS
- [ ] XDR registration succeeds against mock server
- [ ] Credential refresh loop completes
- [ ] All 16 WS-B plans verified
- [ ] All 6 WS-C Phase 7 plans verified
- [ ] Git history is clean (rebased/squashed commits per plan)

---

## Quick Links

- WS-A: [.planning/workstreams/ws-a-proto-foundation/](workstreams/ws-a-proto-foundation/)
- WS-B: [.planning/workstreams/ws-b-connector-layer/](workstreams/ws-b-connector-layer/)
- WS-C: [.planning/workstreams/ws-c-v1-release/](workstreams/ws-c-v1-release/)

---

**Last Updated:** 2026-06-13  
**Milestone:** ArgusSDK v1.0  
**Status:** Release Hardening (Phase 7 Wave 2, Plan 07-03 in progress)
