---
gsd_state_version: 1.0
milestone: v1.0
milestone_name: v1-release
current_phase: 07
current_plan: null
status: in_progress
stopped_at: "Phase 6 complete (2026-06-12). EUC OS Collectors: Linux eBPF (cilium/ebpf, bpf2go skeleton committed), Windows ETW (golang-etw Kernel-Network), macOS no-root gopsutil sampler (NetExt deferred). Exported NewOSCollector + agent wiring. CI cross-compile/isolation matrix. Verification PASSED. Next: Phase 7 Release Hardening."
last_updated: "2026-06-12T00:00:00Z"
last_activity: 2026-06-12
progress:
  total_phases: 3
  completed_phases: 2
  total_plans: 11
  completed_plans: 7
  percent: 64
---

# WS-C v1.0 Release Hardening — State

## Current Position

Phase: 07 (Release Hardening) — PENDING
**Status:** Phase 6 complete; ready to execute Phase 7.
**Last Activity:** 2026-06-12
**Last Activity Description:** Phase 6 EUC OS Collectors complete — all 5 plans delivered and
verified. Linux eBPF observer (cilium/ebpf, precompiled bpf2go skeleton in package euc, no cgo),
Windows ETW observer (golang-etw Kernel-Network provider), macOS no-root gopsutil sampler (full
NEDNSProxyProvider Network Extension deferred). Exported euc.NewOSCollector wires the
build-tag-selected impl; agent updated. CI cross-compile matrix + dep-isolation assertions for all
three GOOS (including generated bpf2go skeleton path). All three platforms cross-compile clean.

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
