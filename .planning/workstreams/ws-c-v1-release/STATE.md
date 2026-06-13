---
gsd_state_version: 1.0
milestone: v1.0
milestone_name: v1-release
current_phase: 07
current_plan: null
status: complete
stopped_at: "Phase 7 Release Hardening COMPLETE. All 4 plans done: 07-01 (OCSF fidelity), 07-02 (SIGHUP hot-reload), 07-03 (config/docs/Makefile/milestone), 07-04 (CI completeness). All merged to main. Ready for v1.0 RC."
last_updated: "2026-06-13T00:00:00Z"
last_activity: 2026-06-13
progress:
  total_phases: 3
  completed_phases: 3
  total_plans: 11
  completed_plans: 11
  percent: 100
---

# WS-C v1.0 Release Hardening — State

## Current Position

Phase: 07 (Release Hardening) — COMPLETE ✅  
**Status:** All 4 plans delivered and verified on main.
**Last Activity:** 2026-06-13
**Deliverables:**
- 07-01 (OCSF fidelity): Injectable clock, first-class WebResources/Databucket, honest HTTP URL
- 07-02 (SIGHUP hot-reload): Bounded EUC + log-level reload, signal handler
- 07-03 (Config/Docs/Makefile): agent.example.yaml, README sync, Makefile fixes, WORKSTREAMS.md milestone record
- 07-04 (CI completeness): All jobs green (build/vet/race/integration/euc-cross-compile)

**v1.0 Release Status:** READY FOR RC — All phases (5/6/7) complete, 11/11 plans executed, verification passing.

## Phases

| Phase | Name | Status |
|-------|------|--------|
| 5 | XDR Mode-1 Registration & Credential Lifecycle | complete |
| 6 | EUC OS Collectors (Linux/Windows/macOS) | complete |
| 7 | Release Hardening (OCSF/hot-reload/docs/CI) | in_progress |

## Decisions Made

- New workstream ws-c-v1-release (cross-cutting release concerns, not connector-layer work) — chosen over appending to ws-b
- v1.0 target scope: XDR Mode 1 + EUC, both fully implemented
- EUC: implement all three OS collectors (Linux eBPF, Windows ETW, macOS gopsutil sampler) in Phase 6
- macOS: no-root gopsutil sampler ships for v1.0; full NEDNSProxyProvider Network Extension deferred (requires signed .app + managed entitlement + notarization — not shippable as `go install` CLI)
- XDR registration: build to the documented HTTP contract (auth.go) + verify against an in-process mock XDR server; no live XDR endpoint required
- Remote registrar becomes the default for mode: remote; local mode keeps the simplified path
- Global phase numbering continued from WS-B (which ended at Phase 4) → Phases 5/6/7

## Session Continuity

**Stopped At:** Phase 6 complete (all 5 plans done, verification passed).
**Resume File:** workstreams/ws-c-v1-release/ROADMAP.md
**Next:** Execute Phase 7 Release Hardening (4 plans, 2 waves).
