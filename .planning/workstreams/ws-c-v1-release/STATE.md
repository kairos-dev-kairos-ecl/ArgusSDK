---
gsd_state_version: 1.0
milestone: v1.0
milestone_name: v1-release
current_phase: 07
current_plan: null
status: in_progress
stopped_at: "Phase 7 Wave 1: 07-01 (OCSF fidelity) + 07-02 (SIGHUP hot-reload) complete. Merged to main. Next: 07-04 (CI completeness), 07-03 (Wave 2), Phase 7 verification."
last_updated: "2026-06-13T00:00:00Z"
last_activity: 2026-06-13
progress:
  total_phases: 3
  completed_phases: 2
  total_plans: 11
  completed_plans: 9
  percent: 82
---

# WS-C v1.0 Release Hardening — State

## Current Position

Phase: 07 (Release Hardening) — IN PROGRESS  
**Status:** Wave 1 complete (07-01 + 07-02); Wave 2 + verification pending.
**Last Activity:** 2026-06-13
**Last Activity Description:** Phase 7 Wave 1 complete.
- 07-01 (OCSF fidelity): Injectable clock on Mapper; first-class WebResources/Databucket on Event; honest HTTP URL (never s.Category). Tests green.
- 07-02 (SIGHUP hot-reload): Bounded reload (EUC watch list + log level only). EUC collector UpdateWatchList/WatchList seam; buildLogger returns live AtomicLevel; reloadConfig in reload.go; SIGHUP signal handler in agent.Run(). Tests green.
Both merged to main. Next: 07-04 (CI completeness), 07-03 (Wave 2: config/docs/Makefile), Phase 7 verification, tracking update.

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
