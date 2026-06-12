# Phase 7: Release Hardening - Context

**Gathered:** 2026-06-12
**Status:** Ready for planning (no dedicated research — known internal cleanup items)
**Source:** /gsd-plan-phase decisions (2026-06-12) + v1.0 readiness assessment

<domain>
## Phase Boundary

The final v1.0 phase: close correctness, operability, and documentation gaps so
the release is a clean cut. After Phases 5 (XDR registration) and 6 (EUC OS
collectors), this phase makes the repo honest about its own state, completes the
OCSF output for in-scope classes, lets config change without a restart, and
proves the release in CI.

IN SCOPE: OCSF mapper fidelity gaps, config hot-reload (SIGHUP) limited to the
EUC watch list + log level, sample-config consolidation, README/docs sync,
Makefile fixes, CI completeness/green, and a milestone-level planning record.

OUT OF SCOPE: new features; connector live-reconfiguration/reconnect (only the
EUC watch list + log level hot-reload); anything in Phases 5/6; the deferred
macOS Network Extension packaging (tracked from Phase 6).
</domain>

<decisions>
## Implementation Decisions (LOCKED)

### OCSF fidelity (internal/ocsf/mapper.go)
- Promote `web_resources` and the databucket object to first-class fields on the OCSF `Event` (the `mapper.go:438` and `mapper.go:452` deferrals), populated for the classes that need them, instead of being dropped.
- Remove the `url = s.Category` placeholder at `mapper.go:396`: derive a real URL from the signal context where available, else omit the field rather than mis-populating it with the category.
- Make the mapper clock injectable (the `mapper.go:313`/`LoggedTime = time.Now()` non-determinism) so OCSF output is deterministic and golden-testable. Default to real time; tests inject a fixed clock.
- All affected OCSF classes must round-trip in unit tests; do not regress existing mapper tests.

### Config hot-reload (SIGHUP)
- Add a SIGHUP handler in the agent (the `agent.go:157` TODO) that re-reads config and applies a BOUNDED set of changes live: (a) the EUC watch list (`AIEndpoints` + `LocalInferencePorts`) — delivering the README "hot-reloadable" promise; (b) the log level. Everything else (outputs/connectors/listeners) is documented as restart-required for v1.0.
- The EUC collectors built in Phase 6 must accept an updated watch list at runtime; coordinate the seam (e.g. a method on the collector or a shared atomic config) without changing the `OSCollector`/`Observation`/`fanOut` contract.

### Sample config consolidation
- `config/agent.example.yaml` is the SINGLE canonical sample, updated to include every current field (observability, agent.xdr_endpoint, EUC endpoints/ports, outputs, tls, buffer, logging).
- Eliminate the duplication introduced by `deploy/agent.yaml`: either delete it in favor of referencing `config/agent.example.yaml`, or reduce it to a thin, clearly-labelled k8s-specific note that points at the canonical file. The k8s manifest's inline ConfigMap may stay but must not drift from the canonical example.

### README / docs sync
- Tech stack: remove `github.com/oklog/ulid/v2` and `github.com/prometheus/client_golang` (NOT in go.mod — BatchID is crypto/rand; metrics are hand-rolled). List only deps actually present.
- Directory structure: add `connector/elastic`, `connector/factory`, the observability server, and `test/llmsignal`; fix any other drift.
- Document the observability endpoints (`/healthz`, `/readyz`, `/metrics`), the deploy artifacts (Dockerfile, k8s manifest), `agent.xdr_endpoint`, and the EUC platform support matrix (incl. the macOS NetExt deferral).
- Ensure the Quick Start path is valid against the consolidated config.

### Makefile
- `docker` target builds the existing `Dockerfile` (replace the TODO).
- `install` target points at `./cmd/argus-agent` (currently `./cmd/argus`). [User OK'd fixing this here, not deferring.]

### CI
- Ensure `.github/workflows/ci.yml` is complete and correct: build, vet, CGO `-race`, integration, plus the Phase-6 cross-compile/isolation matrix (GOOS linux/windows/darwin; assert eBPF/ETW absent from non-target builds). Locally verify everything verifiable here (build, vet, non-race test); the actual green `-race`/integration run executes on the runner (push) — document evidence/known-good.

### Milestone record
- Create a milestone-level record tying WS-A/WS-B/WS-C for v1.0: either a top-level `.planning/ROADMAP.md`/`WORKSTREAMS.md` index or fill the stub `.planning/STATE.md`. Minimum: list the three workstreams, their phases, and v1.0 status. (The Makefile already references a non-existent `.planning/WORKSTREAMS.md` G5 — satisfy that reference.)

### Claude's Discretion
- Exact OCSF object shapes for web_resources/databucket (follow OCSF v1.3 + existing mapper conventions); the precise clock-injection API; whether deploy/agent.yaml is deleted vs reduced; the milestone-record format.
</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning/implementing.**

### OCSF
- `internal/ocsf/mapper.go` — the deferrals (`:313` clock, `:396` URL placeholder, `:438` databucket, `:452` web_resources) and the `Event`/class structures + `populateClassFields`.
- `internal/ocsf/mapper_test.go` — existing round-trip tests to preserve/extend.

### Agent + config
- `internal/agent/agent.go` — `Run()` SIGHUP TODO (`:157`), config loading, the observability server, EUC wiring; AgentConfig (incl. the `xdr_endpoint` added in Phase 5).
- `internal/collector/euc/euc.go` — `Config` (AIEndpoints/LocalInferencePorts) the hot-reload updates; the OSCollector seam Phase 6 implements.
- `cmd/argus-agent/main.go` — viper config load (for hot-reload + sample-config alignment).

### Config + deploy + docs + CI
- `config/agent.example.yaml` (canonical sample to consolidate to), `deploy/agent.yaml` + `deploy/kubernetes/deployment.yaml` (the duplication to resolve), `Dockerfile`.
- `README.md` (tech stack, directory structure, quick start, EUC section to sync).
- `Makefile` (docker TODO, install path bug).
- `.github/workflows/ci.yml` (jobs to complete).
- `.planning/STATE.md` (top-level stub to fill) + the three workstream ROADMAPs.
</canonical_refs>

<specifics>
## Specific Ideas

- Hot-reload scope is deliberately small (EUC watch list + log level) so v1.0 ships a real, testable reload without the complexity of reconnecting live connectors. Test it by sending the agent a config change and asserting the EUC watch list updates and a bad reload is rejected without crashing.
- The observability server, deploy artifacts, and llmsignal suite already exist (added earlier) — Phase 7 is documenting/aligning them, not rebuilding.
</specifics>

<deferred>
## Deferred Ideas

- Live reconfiguration of output connectors (reconnect on config change) — restart-required for v1.0.
- macOS full Network Extension packaging — tracked from Phase 6.
- Automatic credential refresh scheduling — tracked from Phase 5 (the call path exists; scheduling is post-v1.0 unless trivial to add here).
</deferred>

---

*Phase: 07-release-hardening*
*Context gathered: 2026-06-12 via /gsd-plan-phase*
