# Phase 6: EUC OS Collectors - Context

**Gathered:** 2026-06-12
**Status:** Ready for research → planning
**Source:** /gsd-plan-phase decisions (2026-06-12)

<domain>
## Phase Boundary

Implement real Shadow-AI network capture behind the existing `euc.OSCollector`
seam on ALL THREE platforms, replacing the stub bodies in
`internal/collector/euc/{linux,windows,darwin}.go`. Each platform observes DNS
queries / TCP connections to configured AI service endpoints and local inference
ports and emits `euc.Observation` values; `fanOut` (already built) turns those
into `euc.ai_access` / `euc.local_inference` signals.

IN SCOPE: per-platform OSCollector implementations (Linux eBPF, Windows
ETW/WFP, macOS Network Extension), config-driven watch lists, build-tagged
compilation, platform-gated tests, retained no-op fallback.

OUT OF SCOPE: SIGHUP/hot-reload wiring of the watch list (Phase 7); XDR/auth
(Phase 5); OCSF/docs/CI (Phase 7); any change to `euc.fanOut`, `euc.Observation`,
`euc.Config`, or the signal schema.
</domain>

<decisions>
## Implementation Decisions (LOCKED)

### Coverage
- Implement all three platforms in this phase: `linux.go` (eBPF), `windows.go` (ETW/WFP), `darwin.go` (Network Extension). One plan per platform (disjoint files → parallelizable in Wave 1).
- Each platform file is build-tagged for its GOOS and provides the real `OSCollector` via the existing seam. The no-op collector (`euc_noop.go` / `NewNoopOSCollector`) REMAINS as the fallback for unsupported build targets and for environments lacking privileges/capabilities.
- `go build ./...` must succeed when cross-compiling for linux, windows, and darwin (build tags must isolate platform-specific imports so non-target builds never pull them in).

### Behavior contract (all platforms)
- Observe DNS queries and/or TCP connections to: (a) hostnames in `euc.Config.AIEndpoints`, and (b) local inference ports in `euc.Config.LocalInferencePorts` (Ollama 11434, LM Studio 1234, vLLM 8000, etc.).
- Emit `euc.Observation{ConnectedHost, LocalPort, IsLocal, ProcessName, Username}` — ProcessName/Username are BEST-EFFORT (may be empty if unavailable without elevated privilege).
- Watch lists come from `euc.Config` (config-driven), never hardcoded. The impl must accept the configured lists at Start; live hot-reload of the list is Phase 7 (SIGHUP) — design so a list update is feasible but do not build the SIGHUP path here.
- PRESERVE the low-privilege contract (package doc + threat model T-04-14): NO general process enumeration, NO file-access monitoring, NO full packet/flow capture, NO AI API payload/content inspection. Capture only connection metadata needed for Shadow-AI detection. Degrade gracefully (log + fall back) when the OS denies the required privilege rather than crashing.

### Dependencies
- New OS-capture dependencies are PERMITTED for this phase ONLY where genuinely required (e.g. a Linux eBPF loader, Windows ETW bindings), and MUST be isolated behind platform build tags so they never enter non-target builds or the default `go test ./...`. Prefer well-maintained, widely-used libraries; the researcher recommends specific libraries + the minimal-privilege approach per platform. Keep the dependency surface as small as possible.

### Testing
- Each platform impl has tests that run on that platform and `t.Skip` cleanly elsewhere (mirror the existing integration-test skip pattern). Where a real capture needs privilege/hardware not present in CI, gate behind a build tag and/or env probe and skip — never hard-fail.
- A cross-platform unit test (already exists for `fanOut`) stays green; new tests must not require Docker for the default suite.

### Claude's Discretion (pending research)
- Exact library per platform (eBPF loader, ETW/WFP binding, NetExt packaging), the precise capture mechanism (DNS hook vs connect() kprobe vs flow events), and how ProcessName/Username are obtained within the low-privilege contract — the researcher recommends; the planner locks per RESEARCH.md.
</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before researching/planning/implementing.**

### EUC seam + contract
- `internal/collector/euc/euc.go` — `OSCollector` interface, `Observation`, `Config` (AIEndpoints, LocalInferencePorts, AppID, Env), `fanOut` (consumes Observations — do NOT change), the low-privilege package contract.
- `internal/collector/euc/euc_noop.go` — `NewNoopOSCollector` fallback to retain.
- `internal/collector/euc/{linux,windows,darwin}.go` — the current stub bodies to replace (note their existing build tags + TODO comments).
- `internal/agent/agent.go` (EUC wiring around the collector construction) — how the agent currently wires `euc.New(cfg, NewNoopOSCollector())`; the real impl must slot in via the same seam (likely a platform `newOSCollector()` selected at build time).

### Product contract
- `README.md` (EUC — Shadow AI Observability section) — the authoritative "what it watches / what it must NOT do" contract and the named OS mechanisms (eBPF / WFP-ETW / Network Extension).
</canonical_refs>

<specifics>
## Specific Ideas

- The existing local-LLM test suite (`test/llmsignal/euc_local_inference_test.go`) already proves the Observation→signal shaping for a real local port; the OS collectors are the missing piece that PRODUCE those Observations from real OS events. Keep the produced Observation shape identical so that suite (and `fanOut`) is unchanged.
- A reasonable cross-platform skeleton: each `newOSCollector()` returns an impl that, on Start, opens the platform capture, filters events to the configured hosts/ports, maps to Observation, and forwards on the channel; on Close, tears down the capture. The noop remains the default when capture init fails.
</specifics>

<deferred>
## Deferred Ideas

- SIGHUP / live hot-reload of the AIEndpoints/LocalInferencePorts watch list → Phase 7.
- Corporate proxy/DNS telemetry ingestion (README mentions "where available") → post-v1.0 unless trivial.
- Deep per-process attribution requiring elevated privilege beyond the low-privilege contract → out of scope.
</deferred>

---

*Phase: 06-euc-os-collectors*
*Context gathered: 2026-06-12 via /gsd-plan-phase*
