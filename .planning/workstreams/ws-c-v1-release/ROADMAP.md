# Roadmap: WS-C — v1.0 Release Hardening

## Overview

Take ArgusSDK from "data plane works end-to-end" to a **clean, production-ready
v1.0**. The signal pipeline (collectors → ingest → OCSF/proto → connectors →
buffer/drain) is complete and verified in WS-A/WS-B. WS-C closes the two
remaining feature blockers — real XDR Mode-1 identity and real EUC Shadow-AI
capture — and then hardens correctness, operability, and docs for release.

Target for v1.0: **XDR (Mode 1) + EUC**, fully implemented and verified.

**Depends on:** WS-A (proto foundation) and WS-B (connector layer + agent wiring),
both complete.

## Phases

- [x] **Phase 5: XDR Mode-1 Registration & Credential Lifecycle** (complete 2026-06-12)
- [x] **Phase 6: EUC OS Collectors — Linux eBPF + Windows ETW/WFP + macOS Network Extension** (complete 2026-06-12)
- [ ] **Phase 7: Release Hardening — OCSF Fidelity, Hot-Reload, Docs/Config/CI Cleanup**

## Phase Details

### Phase 5: XDR Mode-1 Registration & Credential Lifecycle
**Goal:** Replace the deterministic local-hash InstanceID stand-in
(`internal/agent/registration.go` `localRegistrar`) with the real EDR-pattern
registration and credential lifecycle described in `internal/auth/auth.go` and
the README. Build to the documented HTTP contract and verify against an
in-process mock XDR server (no live XDR required). After this phase, Mode-1
instance identity is server-assigned and persisted, never self-reported.

**Depends on:** WS-B Phase 4 (agent lifecycle, `ensureInstance`, `secrets.Store`)

**Decision inputs (from /gsd-plan-phase 2026-06-12):** build to contract + mock;
remote registrar becomes the default for `mode: remote`; local mode keeps the
simplified path.

**Success Criteria** (what must be TRUE):
  1. `internal/auth` implements `Registrar.Register` against `POST /api/v1/sdk/register` (GroupID + InstallToken → InstanceID + Credential) over TLS 1.3; install token is cleared after a successful call.
  2. InstanceID + Credential are persisted to an encrypted `agent-state.json` via `secrets.Store` (AES-256-GCM) and reloaded on restart; never written in plaintext.
  3. `CredentialRefresher.Refresh` calls `POST /api/v1/sdk/credential/refresh` and atomically replaces the stored credential.
  4. `agent.ensureInstance` uses the real remote registrar by default in `mode: remote`; `mode: local` keeps the existing simplified path; a registered InstanceID short-circuits re-registration.
  5. An in-process mock XDR server backs unit tests covering: register happy-path, persistence + reload across restart, credential refresh, install-token single-use invalidation, TLS-required enforcement, and registration/refresh failure paths (non-2xx, network error).
  6. `go test ./internal/auth/... ./internal/agent/...` passes; `go build ./...` clean; no new runtime dependency beyond what registration strictly requires.

**Requirements:** R-51, R-52, R-53, R-54, R-55

**Plans:** 2 plans across 2 waves (plan-checked + revised 2026-06-12)

Plans:
- [x] 05-01-PLAN.md — auth HTTP client: remote Registrar + CredentialRefresher (TLS 1.3, stdlib net/http) + in-process mock XDR + tests
- [x] 05-02-PLAN.md — encrypted Identity persistence + AgentConfig.xdr_endpoint + mode-aware registrar + resolveIdentity wiring + Agent.RefreshCredential + mock-XDR integration tests

### Phase 6: EUC OS Collectors — Linux eBPF + Windows ETW/WFP + macOS Network Extension
**Goal:** Implement real Shadow-AI capture behind the existing `euc.OSCollector`
seam on all three platforms, replacing the stub bodies in
`internal/collector/euc/{linux,windows,darwin}.go`. Each platform observes DNS
queries / TCP connections to configured AI endpoints and local inference ports
(Ollama :11434, LM Studio :1234, vLLM :8000) and emits `euc.Observation`s. The
no-op collector remains the fallback for unsupported build targets.

**Depends on:** WS-B Phase 4 (EUC collector `fanOut`, `OSCollector` interface, noop seam)

**Decision inputs:** all three platforms in v1.0 (one plan per platform, disjoint
files → parallelizable). Real eBPF/ETW/Network-Extension capture may require cgo,
elevated privileges, or signed helpers; verification is platform-gated and skips
cleanly off-platform.

**Success Criteria** (what must be TRUE):
  1. `linux.go` (build tag `linux`): an eBPF-based observer attaches to network hooks, detects DNS/TCP to configured AI endpoints + local inference ports, and forwards `euc.Observation`s; degrades gracefully without privileges.
  2. `windows.go` (build tag `windows`): an ETW/WFP observer detects the same and forwards Observations.
  3. `darwin.go` (build tag `darwin`): a Network-Extension/helper observer detects the same and forwards Observations.
  4. Observed endpoints/ports are driven by `euc.Config.AIEndpoints` + `LocalInferencePorts` (config), not hardcoded; the watch list is hot-reloadable.
  5. Each platform impl has tests that run on that platform and `t.Skip` cleanly elsewhere; the no-op collector remains wired as the fallback for unsupported targets; `go build ./...` succeeds on all of linux/windows/darwin.
  6. `internal/collector/euc` low-privilege contract is preserved — no general process enumeration, file-access monitoring, or full network capture (per package contract + threat model).

**Requirements:** R-61, R-62, R-63, R-64, R-65

**Plans:** 5 plans across 3 waves (plan-checked + revised 2026-06-12)

Plans:
- [x] 06-00-PLAN.md — shared tag-free netcommon.go (matchHost/matchPort + local-inference-port helper) [Wave 0]
- [x] 06-01-PLAN.md — Linux eBPF observer (cilium/ebpf, precompiled bpf2go object, no cgo) [Wave 1]
- [x] 06-02-PLAN.md — Windows ETW observer (golang-etw, Kernel-Network provider) [Wave 1]
- [x] 06-03-PLAN.md — macOS no-root connection sampler (gopsutil); full NetExt deferred [Wave 1]
- [x] 06-04-PLAN.md — build-tag-selected NewOSCollector + agent wiring + cross-compile/isolation CI [Wave 2]

### Phase 7: Release Hardening — OCSF Fidelity, Hot-Reload, Docs/Config/CI Cleanup
**Goal:** Close the correctness, operability, and documentation gaps so v1.0 is a
clean cut: the repo is honest about its own state, OCSF output is complete for
the classes in scope, config changes don't require a restart, and CI proves the
release on a real runner.

**Depends on:** Phase 5 and Phase 6 (docs/config must describe the shipped feature set)

**Success Criteria** (what must be TRUE):
  1. OCSF fidelity: `web_resources`/databucket objects are first-class on the `Event` (no `url = s.Category` placeholder at `mapper.go:396`); `Mapper` clock is injectable for deterministic output; affected classes round-trip in tests.
  2. Config hot-reload: SIGHUP reloads agent config (the `agent.go:157` TODO is resolved) and the EUC endpoint/port watch list updates without restart (delivering the README "hot-reloadable" promise).
  3. Sample config is consolidated to a single canonical file (`config/agent.example.yaml`); the deploy artifacts reference it rather than duplicating it.
  4. README is synced to reality: tech-stack lists only deps actually in `go.mod` (no unused `ulid`/`prometheus`), the directory structure includes `elastic`/`factory`/`observability`, and the quick-start path is valid; observability + deploy are documented.
  5. Makefile: the `docker` target builds the existing `Dockerfile`; the `install` target points at `./cmd/argus-agent`.
  6. CI: `-race` and integration jobs run green on a real runner (or documented evidence + fixes for any failures); `go build ./...`, `go vet ./...`, and `go test ./...` are clean.
  7. A milestone-level record (top-level roadmap or `.planning/WORKSTREAMS.md`) ties WS-A/WS-B/WS-C together for the v1.0 release.

**Requirements:** R-71, R-72, R-73, R-74, R-75, R-76, R-77

**Plans:** 4 plans across 2 waves (plan-checked + revised 2026-06-12)

Plans:
- [ ] 07-01-PLAN.md — OCSF fidelity: injectable clock + web_resources/databucket first-class + honest URL [Wave 1]
- [ ] 07-02-PLAN.md — bounded SIGHUP hot-reload (EUC watch list + log level) [Wave 1]
- [ ] 07-03-PLAN.md — sample-config consolidation + buffer mapstructure tags + README sync + Makefile + milestone record [Wave 2]
- [ ] 07-04-PLAN.md — CI completeness (build/vet/race/integration + cross-compile/isolation matrix) [Wave 1/2]
