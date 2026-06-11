---
phase: 04-agent-wiring
plan: "05"
subsystem: agent-wiring
tags: [agent, wiring, registration, ingest-loop, buffer, dispatcher, ocsf-routing, tdd]
dependency_graph:
  requires:
    - 04-01  # connector factory + NewBatchID
    - 04-02  # syslog connector
    - 04-03  # argusxdr connector
    - 04-04  # llm + euc collectors
  provides:
    - internal/agent/agent.go (full start()/stop() wiring + ingest loop + drain func)
    - internal/agent/registration.go (Registrar interface + localRegistrar + ensureInstance)
    - internal/connector/connector.go (SignalBatch.AppID/Env fields)
  affects:
    - SC-2: start() wiring — registry, dispatcher, buffer.Start with real drain
    - SC-3: ingest loop signal.Batch→SignalBatch with per-OCSF-group dispatch
    - SC-4: stop() buffer.Flush with real drain (no nil path)
    - SC-12: in-process smoke — ingest→deliver and graceful drain
tech_stack:
  added: []
  patterns:
    - TDD (RED → GREEN per task)
    - Per-OCSF-group DispatchJob partition (argusxdr always nonOCSF, others follow OCSF flag)
    - sync.WaitGroup for ingest loop drain coordination in stop()
    - SHA-256 truncated hash for deterministic local InstanceID (locked decision 8)
    - WAL buffer as backpressure sink for Enqueue failures (T-04-15)
key_files:
  created:
    - internal/agent/registration.go
    - internal/agent/registration_test.go
    - internal/agent/agent_test.go
  modified:
    - internal/agent/agent.go
    - internal/connector/connector.go
decisions:
  - "localRegistrar derives InstanceID from SHA-256(groupID:installToken) truncated to 16 hex chars — stable across restarts, no live-XDR required"
  - "ingestLoop partitions connectors into ocsfTargets (OCSF=true AND Type!=argusxdr) and nonOCSFTargets per locked decision 5"
  - "deliver() uses UseOCSF flag on buffered batch to route back to the correct partition"
  - "stop() waits for ingest loop via sync.WaitGroup before Flush to guarantee in-flight batches are enqueued or buffered"
  - "shutdownTimeout=30s for buffer.Flush — matches DefaultDispatchConfig.ShutdownTimeout"
  - "euc collector wired with NewNoopOSCollector as cross-platform seam; real OSCollector deferred"
  - "Agent struct carries registry, instanceID, ocsfTargets, nonOCSFTargets, ingestCh, drain, wg as new fields"
metrics:
  duration: "5m"
  completed_date: "2026-06-11"
  tasks: 3
  files: 5
---

# Phase 4 Plan 05: Agent start()/stop() Wiring + Ingest Loop + Drain Summary

**One-liner:** Full agent wiring — factory-built ConnectorRegistry, per-OCSF-group ingest loop dispatching separate DispatchJobs for argusxdr (UseOCSF=false) and OCSF targets (UseOCSF=true), WAL buffer with real drain func, and graceful stop() via sync.WaitGroup + buffer.Flush; plus a SHA-256-based registration seam.

## Tasks Completed

| Task | Name | Commit (RED) | Commit (GREEN) | Files |
|------|------|-------------|----------------|-------|
| 1 | Registration seam (InstallToken→InstanceID) | 8efb37c | 5a4fcef | registration.go, registration_test.go |
| 2 | SignalBatch AppID/Env + start() wiring | 082c5a0 | 0732354 | connector.go, agent.go, agent_test.go |
| 3 | stop() graceful drain with real drain func | 082c5a0 (same RED) | 0732354 (same GREEN) | agent.go, agent_test.go |

## What Was Built

### Task 1 — Registration Seam (SC deferred local path — locked decision 8)

`internal/agent/registration.go`:
- `Registrar` interface: `Register(ctx, installToken, groupID) (string, error)`
- `localRegistrar`: SHA-256(`groupID:installToken`) truncated to 16 hex chars — deterministic, no network call
- `ensureInstance`: precedence — existing InstanceID > InstallToken register > error
- Remote registrar deferred; interface is defined so a live-XDR impl can be dropped in

Tests (7 total):
- `TestEnsureInstance_AlreadySet`: short-circuit when InstanceID present
- `TestEnsureInstance_TokenSet`: calls registrar, passes token + group
- `TestEnsureInstance_BothEmpty`: returns error
- `TestEnsureInstance_RegistrarError`: error propagated via `%w`
- `TestLocalRegistrar_Deterministic`: same inputs → same ID
- `TestLocalRegistrar_DifferentInputs`: different groups → different IDs
- `TestEnsureInstance_FakeRemoteRegistrar`: fake remote seam exercised (locked decision 8 proof)

### Task 2 — SignalBatch AppID/Env + start() Wiring (SC-2, SC-3)

`internal/connector/connector.go`:
- Added `AppID` and `Env` string fields to `SignalBatch` with doc comments noting they are populated by the agent ingest loop and consumed only by the argusxdr connector. kafka/splunk_hec/elastic/syslog `Send()` are unaffected.

`internal/agent/agent.go` — `start()` implements:
1. `ensureInstance` with `localRegistrar` → stored as `a.instanceID`
2. Registry built from `cfg.Outputs` via `factory.Build` + `Connect` + `Register`; error on empty Outputs
3. OCSF partition: `OCSF=true AND Type!="argusxdr"` → `a.ocsfTargets`; else → `a.nonOCSFTargets` (argusxdr always non-OCSF per locked decision 5)
4. `connector.NewDispatcher(DefaultDispatchConfig(), registry, logger)` → `a.dispatcher`
5. `buffer.New(cfg.Buffer)` + `drainFunc := a.deliver(ctx, b)` + `buffer.Start(ctx, drainFunc)` → `a.buffer`; drain stored as `a.drain`
6. LLM collector wired on `cfg.Ingest.Listen.GRPC`; EUC collector wired with `NewNoopOSCollector()`
7. `a.ingestCh = make(chan signal.Batch, 256)` + `ingestLoop` goroutine + all collectors started

`ingestLoop` per-OCSF dispatch:
- Base `SignalBatch`: `NewBatchID()`, `a.instanceID`, `cfg.Agent.GroupID`, `AppID`, `Env`, `time.Now()`, `Signals`
- Non-empty `nonOCSFTargets` → `DispatchJob{UseOCSF=false, Targets=nonOCSFTargets}`; Enqueue failure → `buffer.Write`
- Non-empty `ocsfTargets` → `DispatchJob{UseOCSF=true, Targets=ocsfTargets}`; Enqueue failure → `buffer.Write`

`deliver()` routes buffered batches by matching `b.UseOCSF` to the correct partition.

### Task 3 — stop() Graceful Drain (SC-4, SC-12)

`stop()` sequence:
1. `a.cancel()` — cancels agent context
2. Collectors: `c.Close()` for each (logs warnings)
3. `close(a.ingestCh)` + `a.wg.Wait()` — drain in-flight batches
4. `buffer.Flush(ctx-with-30s-timeout, a.drain)` — real drain, never nil (F5 nil-drain removed)
5. `buffer.Close()` then `dispatcher.Close()`

The F5 nil-drain TODO comment is gone. `a.drain` is always set by `start()` before `stop()` can be called.

Tests (3 total in agent_test.go):
- `TestStart_OCSFRouting`: injects fakes directly into `a.registry`; feeds one `signal.Batch`; asserts xdr got UseOCSF=false and ocsf got UseOCSF=true, both with AppID/Env/BatchID set
- `TestStart_EmptyOutputs`: asserts error on empty Outputs
- `TestStop_GracefulDrain`: writes one batch to WAL buffer, calls `stop()`, asserts fake connector received it via `buffer.Flush` (SC-12 automated smoke + SC-4 proof)

## Verification Results

```
go build ./...                                      PASS
go vet ./...                                        PASS
go test ./internal/agent/ -count=1 -timeout 120s   PASS (10 tests, 0.688s)
go test ./internal/connector/... -count=1           PASS (all sub-packages)
go test ./... -count=1 -timeout 300s               PASS (all packages, 18 test binaries)
grep -c "AppID" internal/connector/connector.go    → 3  (field present)
grep -c "factory.Build" internal/agent/agent.go    → 1  (factory use confirmed)
grep -c "Enqueue" internal/agent/agent.go          → 4  (ingest loop dispatches)
grep -c "Flush" internal/agent/agent.go            → 5  (stop() calls Flush)
grep "elastic" internal/agent/agent.go (type comment) → present
grep "TODO.*nil.*drain" internal/agent/agent.go    → none (F5 nil-drain removed)
```

## Deviations from Plan

None — plan executed exactly as written.

## Known Stubs

- EUC collector wired with `NewNoopOSCollector()` in `start()` — the noop impl emits no observations. This is intentional: a real OS-specific impl (eBPF/WFP/NEF) is a separate future plan. The seam is exercised end-to-end; no data loss risk (noop just silently produces nothing).
- Remote registrar (live ArgusXDR registration RPC) — deferred as documented in `registration.go` doc comment. The local SHA-256 path covers all current tests and smoke scenarios.

## Threat Flags

No new threat surface beyond the plan's STRIDE register:
- T-04-15 (DoS via queue full) — mitigated: `buffer.Write` on Enqueue failure
- T-04-16 (Repudiation on shutdown drain) — mitigated: `buffer.Flush` with real drain; error logged, not silenced
- T-04-17 (Registration elevation) — accepted: local deterministic registrar, no live-XDR trust
- T-04-18 (OCSF tampering) — mitigated: separate DispatchJobs per group; argusxdr always nonOCSFTargets

## Self-Check: PASSED

Files exist:
- [x] internal/agent/registration.go
- [x] internal/agent/registration_test.go
- [x] internal/agent/agent.go
- [x] internal/agent/agent_test.go
- [x] internal/connector/connector.go

Commits exist:
- [x] 8efb37c (test(04-05): add failing tests for registration seam)
- [x] 5a4fcef (feat(04-05): implement registration seam)
- [x] 082c5a0 (test(04-05): add failing tests for start()/stop() wiring)
- [x] 0732354 (feat(04-05): implement start()/stop() wiring, ingest loop, drain)
