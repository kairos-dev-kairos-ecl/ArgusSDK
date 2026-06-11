# Phase 4 Context — Agent Wiring + Integration Hardening

**Gathered:** 2026-06-11
**Status:** Ready for planning
**Source:** Post-Phase-3 gap analysis + user scope decisions (2026-06-11)

The connector layer's components are verified (Phase 2 built them, Phase 3 hardened them),
but nothing is assembled. `internal/agent/agent.go:124 start()` is entirely TODO — registry,
dispatcher, buffer, and collectors are never created, so no signal flows end-to-end. The two
non-priority connectors (syslog, argusxdr) are empty `Send()` scaffolds. There is no
real-infrastructure integration testing and no CI. This phase closes the gap between
"components verified" and "deployable agent."

## Phase Boundary

IN scope:
- Connector factory (`OutputConfig` → typed connector Config → registered `Connector`).
- Full `agent.start()` / `agent.stop()` wiring: registry, dispatcher, buffer + real drain func, ingest loop, collectors.
- Collector implementations: LLM gRPC ingest server + EUC OS-observation collector (at least one OS impl end-to-end).
- Build out syslog (CEF/TCP/TLS) and argusxdr (gRPC `SDKIngestService.IngestBatch`) connector bodies.
- Integration tests via testcontainers-go (Kafka, Elasticsearch; Splunk HEC if feasible).
- CI workflow: unit + vet + CGO `-race` + integration job.
- `cmd/argus-agent` end-to-end smoke (config → ingest → deliver → graceful drain).

OUT of scope (scope fences below).

## User Scope Decisions (locked 2026-06-11)

1. **Wiring scope = FULL agent including collectors.** Wire the output path AND the
   collector layer (LLM gRPC + EUC) so the agent collects real signals end-to-end —
   not just the dispatch half.
2. **Integration testing = testcontainers-go.** Real Kafka + Elasticsearch containers,
   build-tag gated (`//go:build integration`) so unit tests stay fast. Splunk HEC via
   container if practical; otherwise a documented gated smoke test (SC-10 fallback).
3. **Scaffold connectors = IMPLEMENT in this phase.** Build syslog (CEF over TCP/TLS) and
   argusxdr (gRPC) `Send()` bodies with unit tests. Four+ connectors hardened, not three.
4. **`-race` = CI gate, documented locally.** No C compiler on the dev machine; the
   authoritative race gate is a CGO-enabled Linux CI job. Local docs note the gcc/mingw
   requirement. SC references the CI run, not the dev box.

## Locked Decisions

1. **Connector factory** lives in `internal/connector` (e.g. `factory.go`): a
   `Build(out agent.OutputConfig, logger) (Connector, error)` or a registration map keyed by
   `Type`. It decodes `OutputConfig.Auth`/`Endpoint`/`TLS`/`Extra` into each connector's typed
   Config via `mapstructure` (already a dep). To avoid an import cycle (agent imports connector),
   the factory takes a neutral struct or the decode happens agent-side — planner chooses, but
   MUST NOT create an `agent ↔ connector` import cycle. Existing connector `New(cfg)` /
   `NewWithClient(cfg, client)` constructors are the targets.
2. **Type adapter (collector → connector).** Collectors emit `signal.Batch`
   (`pkg/signal/signal.go:82`); the Dispatcher consumes `connector.SignalBatch`
   (`internal/connector/connector.go:22`). The agent ingest loop converts: new ULID `BatchID`,
   `InstanceID` from `cfg.Auth.InstanceID`, `GroupID` from `cfg.Agent.GroupID`,
   `ReceivedAt = time.Now()`, and per-target `UseOCSF` from each `OutputConfig.OCSF`
   (argusxdr is always `UseOCSF=false`; all others true). ULID generation: no ULID lib is a
   dep yet — use an existing approach if present, else a small deterministic generator; do NOT
   add a ULID dependency without flagging it (prefer crypto/rand-based).
3. **Drain func.** `buffer.Start(ctx, drain)` and `buffer.Flush(ctx, drain)` take
   `func(ctx, *connector.SignalBatch) error`. The agent's drain delivers a buffered batch
   through the Dispatcher path (or directly through the registry's connectors) and returns
   non-nil on failure so the buffer retries — honoring the Phase 3 delivery contract.
4. **syslog connector.** CEF formatting over TCP, TLS 1.3 via `connector.NewTLSConfig`.
   stdlib `net` + `crypto/tls` only — NO new dependency. Map OCSF event fields → CEF header +
   extensions. Unit-test against a local `net.Listen` TCP server (and a TLS listener).
5. **argusxdr connector.** `grpc.NewClient`/`DialContext` with TLS + per-RPC API-key creds;
   marshal signals to `sdkv1.SignalBatch` (use `signal.Batch.ToProto`) and call the generated
   `SDKIngestService.IngestBatch` (`gen/go/sdk/v1/ingest_grpc.pb.go`). `UseOCSF` must be false.
   Unit-test against an in-process gRPC server (bufconn or a real loopback listener). gRPC is
   already a dep — NO new dependency.
6. **Integration tests** are build-tag gated `//go:build integration` and run via the existing
   `make test-int` target (`go test ./... -tags=integration`). Use `testcontainers-go` (the one
   allowed new dep). Each integration test SKIPs cleanly (`t.Skip`) when Docker is unavailable
   so the tagged suite degrades gracefully.
7. **CI** is GitHub Actions at `.github/workflows/ci.yml` (none exists today). Jobs: (a) lint/vet
   + `go build ./...`; (b) `go test -race ./...` on `ubuntu-latest` with `CGO_ENABLED=1`;
   (c) integration `make test-int` with Docker available. The `-race` job is the authoritative
   race gate (SC-11).
8. **Registration flow** (`AuthConfig.InstallToken` → `InstanceID`): wire the local path fully;
   for remote registration, define the seam (interface + unit test with a fake) but do NOT
   require a live ArgusXDR — there is nothing to register against in test. Document remote
   registration as the one deferred sub-item if it cannot be exercised.
9. **Delivery semantics** unchanged from Phase 3: failed delivery ⇒ non-nil error; chunking is
   sequential abort-on-first-failure; TLS 1.3 enforced everywhere via `connector.NewTLSConfig`.

## Scope Fences

- Do NOT change the proto contract or `pkg/signal` public API.
- Do NOT add runtime dependencies beyond `testcontainers-go` (test-only). syslog/argusxdr use
  stdlib + existing gRPC; flag any other new dep before adding.
- Do NOT implement SIGHUP hot-reload (agent.go:116 TODO) — separate future phase.
- Do NOT build a remote ArgusXDR registration server or require a live XDR for tests (decision 8).
- Do NOT regress any Phase 3 fix (F1–F16) — the delivery contract, WAL format, injection fix,
  watcher, circuit breaker, secrets cache must remain intact; integration tests must not weaken them.
- Keep unit tests green and Docker-free; all real-infra tests behind the `integration` build tag.

## Current-State File Map (verified 2026-06-11)

| File | Current state | Phase 4 work |
|------|---------------|--------------|
| `internal/agent/agent.go` | `start()` all TODO; `stop()` Flush removed (F5) | Wire registry/dispatcher/buffer/ingest-loop/collectors; restore Flush with real drain |
| `internal/connector/factory.go` | does not exist | NEW — `OutputConfig.Type` → typed Config → `Connector` |
| `internal/collector/llm/llm.go` | scaffold (`Start` TODO) | Implement gRPC `SDKIngestService` server emitting `signal.Batch` |
| `internal/collector/euc/euc.go` | scaffold (`fanOut` TODO) | Build `signal.Signal` from `Observation`; wire ≥1 OS impl |
| `internal/connector/syslog/connector.go` | scaffold `Send()` | CEF over TCP/TLS |
| `internal/connector/argusxdr/connector.go` | scaffold `Send()` | gRPC `IngestBatch` client |
| `internal/connector/kafka|splunk|elastic/*_integration_test.go` | none | testcontainers integration tests |
| `.github/workflows/ci.yml` | does not exist | NEW — unit + vet + `-race` + integration |
| `cmd/argus-agent/main.go` | calls `agent.Run()` | end-to-end smoke target/doc |

## Key Interfaces (verified against source)

```go
// internal/connector/connector.go
type Connector interface { Name() string; Connect(ctx) error; Send(ctx, *SignalBatch) (*DeliveryAck, error); Health(ctx) error; Close() error }
type SignalBatch struct { BatchID, InstanceID, GroupID string; Signals []signal.Signal; ReceivedAt time.Time; UseOCSF bool }
func NewConnectorRegistry(logger) *ConnectorRegistry
func NewDispatcher(cfg *DispatchConfig, registry *ConnectorRegistry, logger) (*Dispatcher, error)
func (d *Dispatcher) Enqueue(job *DispatchJob) error   // DispatchJob{Batch *SignalBatch; Targets []string}
func NewTLSConfig(TLSClientConfig) (*tls.Config, error)  // tls.go — MinVersion TLS 1.3

// internal/collector/collector.go
type Collector interface { Name() string; Start(ctx, out chan<- signal.Batch) error; Health(ctx) error; Close() error }

// internal/buffer/buffer.go
func (b *Buffer) Start(ctx, drain func(ctx, *connector.SignalBatch) error) error  // errors on nil drain
func (b *Buffer) Flush(ctx, drain func(ctx, *connector.SignalBatch) error) error  // errors on nil drain

// pkg/signal/signal.go
type Batch struct { ... }
func (b Batch) ToProto(batchID, sdkVersion string) *sdkv1.SignalBatch

// gen/go/sdk/v1/ingest_grpc.pb.go
type SDKIngestServiceClient interface { IngestBatch(ctx, *SignalBatch, ...) (*BatchAck, error) }
type SDKIngestServiceServer interface { IngestBatch(ctx, *SignalBatch) (*BatchAck, error); ... }

// internal/agent/agent.go
type OutputConfig struct { Name, Type, Endpoint string; OCSF bool; TLS TLSConfig; Auth map[string]string; Extra map[string]interface{} }
```

## Success Criteria (mirror ROADMAP Phase 4 SC-1..SC-12)

SC-1 connector factory · SC-2 start() wiring + buffer.Start drain · SC-3 ingest loop signal.Batch→SignalBatch ·
SC-4 stop() Flush with real drain · SC-5 LLM gRPC collector · SC-6 EUC collector · SC-7 syslog CEF/TLS ·
SC-8 argusxdr gRPC · SC-9 Kafka+Elastic testcontainers integration · SC-10 Splunk HEC integration/smoke ·
SC-11 CI with CGO -race gate · SC-12 cmd/argus-agent end-to-end smoke + graceful drain.

## Test Environment Notes

- Dev machine (Windows) has NO C compiler → `-race` cannot run locally; it is a CI gate (decision 7).
- Dev machine may not have Docker → integration tests must `t.Skip` cleanly when unavailable.
- Worktree-isolated subagents do not work here — plans execute SEQUENTIALLY on the main tree at
  `C:\Users\Drupad\argus-sdk` (same constraint as Phases 2–3).
- Unit suite must stay Docker-free and fast; everything real-infra behind `//go:build integration`.
