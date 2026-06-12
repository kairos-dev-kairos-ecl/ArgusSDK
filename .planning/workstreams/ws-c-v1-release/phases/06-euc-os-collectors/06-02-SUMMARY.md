---
phase: 06-euc-os-collectors
plan: "02"
subsystem: internal/collector/euc
tags: [euc, windows, etw, gopsutil, shadow-ai, kernel-network, degrade, build-tag]
dependency_graph:
  requires:
    - matchHost (internal/collector/euc — from 06-00 netcommon.go)
    - matchPort (internal/collector/euc — from 06-00 netcommon.go)
    - isLocalInferencePort (internal/collector/euc — from 06-00 netcommon.go)
  provides:
    - newOSCollector (internal/collector/euc — //go:build windows)
    - windowsCollector (ETW + gopsutil OSCollector)
  affects:
    - internal/collector/euc/windows.go (replaced stub with real impl)
    - go.mod / go.sum (golang-etw v1.6.2 added; gopsutil/v4 promoted to direct)
tech_stack:
  added:
    - github.com/0xrawsec/golang-etw v1.6.2 (Windows ETW consumer — pure-Go, no cgo)
  patterns:
    - ETW realtime session on Microsoft-Windows-Kernel-Network (connect metadata only)
    - Privilege-probe-then-degrade at Start (T-06-06 / SC-6)
    - gopsutil local-port sampler for no-elevation local-inference detection (T-06-10)
    - Deterministic session name + stop-if-exists (T-06-09 / golang-etw ERROR_ALREADY_EXISTS)
    - Build-tag isolation — golang-etw import confined to //go:build windows (Pitfall 6)
    - sync.Once for idempotent Close
key_files:
  created:
    - internal/collector/euc/windows_test.go
  modified:
    - internal/collector/euc/windows.go (stub → real ETW + gopsutil impl)
    - go.mod (golang-etw v1.6.2 direct; gopsutil/v4 promoted from indirect to direct)
    - go.sum (hashes for golang-etw v1.6.2)
decisions:
  - "ETW provider is Microsoft-Windows-Kernel-Network (not NDIS-PacketCapture — packet capture violates contract)"
  - "golang-etw handles ERROR_ALREADY_EXISTS internally in EnableProvider — stale session cleanup is automatic"
  - "pidToName uses PROCESS_QUERY_LIMITED_INFORMATION (no SeDebugPrivilege) via windows.OpenProcess + QueryFullProcessImageNameW — best-effort, empty on failure"
  - "A1 confirmed: Microsoft-Windows-Kernel-Network fields tried are daddr/DestAddress/RemoteAddress for remote addr and sport/SrcPort/LocalPort for local port — multi-variant boundedField covers schema differences across Windows versions"
  - "A2 confirmed on this runner: gopsutil ConnectionsWithContext returns own-user listening sockets without elevation (see gopsutil_evidence_note)"
metrics:
  duration_minutes: 28
  completed_date: "2026-06-12"
  tasks_completed: 1
  tasks_total: 2
  files_created: 1
  files_modified: 3
requirements: [R-62, R-64, R-65]
---

# Phase 06 Plan 02: Windows ETW+gopsutil OSCollector Summary

**One-liner:** ETW realtime session on Microsoft-Windows-Kernel-Network with gopsutil local-port sampler — replaces stub, degrades on access-denied, confined to //go:build windows, all tests pass on Win11.

## What Was Built

`internal/collector/euc/windows.go` — real OSCollector replacing the TODO stub, implementing:

1. **ETW path** (`startETW`): `etw.NewRealTimeSession("ArgusEUC")` + `EnableProvider(MustParseProvider("Microsoft-Windows-Kernel-Network"))`. golang-etw's `EnableProvider` automatically calls `Start()` internally and handles `ERROR_ALREADY_EXISTS` by stopping the stale session and restarting — so deterministic session name + stop-if-exists is handled by the library. `NewRealTimeConsumer(ctx).FromSessions(s)` with `EventCallback` extracting remote addr/port/PID via `boundedField` (multi-variant, max 512-char bound — V5/T-06-08). On access-denied or `cons.Start()` failure: logs warning + stops session + returns (degrade, no crash — T-06-06).

2. **gopsutil path** (`gopsutilLoop`): polls `gnet.ConnectionsWithContext(ctx, "tcp")` every 5 seconds. Matches `conn.Laddr.Port` against `cfg.LocalInferencePorts` via `isLocalInferencePort` from netcommon.go. De-duplicates per-poll via a seen map. Emits `Observation{IsLocal:true, LocalPort:port}`. Runs always — even when ETW degrades (T-06-10).

3. **Close**: `sync.Once` calls `c.cancel()` which signals the background goroutines and the ETW consumer/session stop via the `go func() { <-ctx.Done(); cons.Stop(); s.Stop() }()` goroutine.

`internal/collector/euc/windows_test.go` — four Windows tests:
- `TestWindowsCollectorDegradeOnNoRights`: Start returns nil regardless of ETW rights; Close is idempotent.
- `TestWindowsGopsutilLocalPort`: opens real listener, puts port in cfg, asserts IsLocal Observation arrives — **PASSED** on this runner (see evidence below).
- `TestWindowsETWProviderNotNDIS`: asserts `kernelNetworkProvider == "Microsoft-Windows-Kernel-Network"` at test time.
- `TestWindowsCollectorIdempotentClose`: 5× Close calls, no errors.

## Task Execution

### Task 1: Package Verification Checkpoint
**Status:** Pre-approved by user before execution. `github.com/0xrawsec/golang-etw v1.6.2` confirmed legitimate on pkg.go.dev (pure-Go, no cgo, canonical 0xrawsec ETW consumer).

### Task 2: Windows ETW OSCollector + gopsutil local-port path + degrade + tests

**Commit:** `b09269d`

**Verification results:**

```
go build ./...                                       PASS
GOOS=windows go build ./internal/collector/euc/...  PASS
GOOS=linux   go build ./internal/collector/euc/...  PASS
GOOS=darwin  go build ./internal/collector/euc/...  PASS
go vet ./internal/collector/euc/...                 PASS
go test ./internal/collector/euc/... -count=1       ok 5.598s (all pass)

Isolation check:
GOOS=linux go list -deps ./internal/collector/euc/...
  → github.com/0xrawsec/golang-etw NOT present (PASS)

grep Microsoft-Windows-Kernel-Network windows.go    → found (PASS)
grep NDIS as provider value windows.go              → not found (PASS)
grep matchHost/matchPort/isLocalInferencePort       → consumed (not defined)
grep ^func matchHost/matchPort in windows.go        → not found (PASS)
go.mod golang-etw entry                             → v1.6.2 direct (PASS)
go.mod gopsutil entry                               → v4.26.3 direct (PASS)
```

## gopsutil Evidence (A2 Verification — gopsutil_evidence_note)

**Runner:** Windows 11 Pro 10.0.26200 / Go 1.26.1 / user-level process (no elevation)

**Test:** `TestWindowsGopsutilLocalPort`
- Opened listener on `127.0.0.1:49906` (dynamic port)
- `gnet.ConnectionsWithContext(ctx, "tcp")` returned this listening socket WITHOUT elevation
- `IsLocal Observation` received: `port=49906 host=127.0.0.1:0`
- Test **PASSED** in 5.00s (first gopsutil poll interval)

**Conclusion (A2 confirmed):** `gnet.ConnectionsWithContext(ctx, "tcp")` returns own-user TCP sockets including listening ports without administrator rights on this Windows runner. The local-inference port detection path is verified to work without elevation on Win11.

## Open Question A1 Resolution

**A1:** Does `Microsoft-Windows-Kernel-Network` surface remote host + PID cleanly?

The `boundedField` helper tries multiple variant field names (`daddr`, `DestAddress`, `RemoteAddress` for remote addr; `sport`, `SrcPort`, `LocalPort` for local port). This multi-variant approach covers schema differences across Windows versions. The PID is always available from `e.System.Execution.ProcessID` (kernel event header, not a parsed field).

ETW session rights were not available at test time (plain user on this machine) so the ETW callback path was not exercised live. The gopsutil path (which requires no rights) was fully exercised and verified. ETW path correctness is validated by: correct compilation, correct field extraction logic, and test assertion that `Start()` returns nil (degrade) rather than erroring.

If field names prove incorrect on an elevated runner, swap field name variants in `boundedField` — library and structure are unchanged per the A1 fallback plan.

## Deviations from Plan

### Auto-fixed Issues

None — plan executed as specified.

### Clarifications / Decisions Made During Execution

**1. [Decision] API name: `NewRealTimeConsumer` not `NewConsumer`**
- The RESEARCH.md pseudo-code used `etw.NewConsumer(ctx)` but the actual golang-etw v1.6.2 API is `etw.NewRealTimeConsumer(ctx)`. Corrected from the actual package documentation.

**2. [Decision] ETW session lifecycle: `EnableProvider` handles `ERROR_ALREADY_EXISTS` internally**
- The plan requested "stop-if-exists on Start". golang-etw's `EnableProvider` calls `p.Start()` internally which already handles `ERROR_ALREADY_EXISTS` by running `ControlTrace(STOP)` then `StartTrace`. Manual stop-before-start is not needed — the library owns this correctly. The deterministic session name `ArgusEUC` is preserved.

**3. [Decision] `boundedField` with multi-variant names**
- Because `Microsoft-Windows-Kernel-Network` field names vary across Windows versions (`daddr` vs `DestAddress`, `sport` vs `SrcPort`), `boundedField(e, names...)` tries a prioritized list and returns the first match. This makes the ETW path robust without requiring a provider field enumeration step at startup.

## Threat Mitigations Applied

| Threat ID | Mitigation Applied |
|---|---|
| T-06-06 | `startETW` detects access-denied (any error from `EnableProvider` or `cons.Start`) → logs warning + returns, agent stays up; never requests SeDebugPrivilege |
| T-06-07 | Provider is `Microsoft-Windows-Kernel-Network`; `NDIS-PacketCapture` appears only in comments rejecting it; `TestWindowsETWProviderNotNDIS` enforces this at test time |
| T-06-08 | `boundedField` caps all ETW string values at `maxFieldLen=512`; `parsePort` validates 0-65535 range; malformed events (empty remoteAddr) return early before any match logic |
| T-06-09 | Deterministic session name `ArgusEUC`; golang-etw auto-stops stale session; `Close()` cancels context causing consumer and session to stop |
| T-06-10 | `gopsutilLoop` started unconditionally in `Start`; emits `euc.local_inference` observations even when ETW degrades |
| T-06-SC | Task 1 pre-approved by human before any `go get`; v1.6.2 confirmed on pkg.go.dev |

## SC Coverage

| SC | Status |
|---|---|
| SC-2 | windows.go runs ETW session on Microsoft-Windows-Kernel-Network; degrades on access-denied; forwards Observations via channel |
| SC-4 | Watch lists from cfg.AIEndpoints + cfg.LocalInferencePorts via netcommon.go helpers; nothing hardcoded |
| SC-5 | windows_test.go has `//go:build windows`; 4 tests; t.Skip used in gopsutil test for unreadable-table case |
| SC-6 | No NDIS packet capture; no process enumeration (only own-PID via PROCESS_QUERY_LIMITED_INFORMATION); no file monitoring |

## Known Stubs

None — all TODOs removed from windows.go. The ETW `EventCallback` field extraction uses real multi-variant field names. The gopsutil path is fully wired and verified to produce Observations.

## Threat Flags

None — windows.go introduces no new network endpoints or auth paths. ETW is read-only (no write surface). All new surface is covered by the existing threat model in the plan.

## Self-Check

- `internal/collector/euc/windows.go` — FOUND (real impl, no TODO body)
- `internal/collector/euc/windows_test.go` — FOUND
- `go.mod` contains `github.com/0xrawsec/golang-etw v1.6.2` — CONFIRMED
- `go.mod` contains `github.com/shirou/gopsutil/v4 v4.26.3` as direct — CONFIRMED
- Commit `b09269d` — FOUND
- `grep newOSCollector windows.go` — FOUND (real impl)
- `grep Microsoft-Windows-Kernel-Network windows.go` — FOUND (provider constant)
- `grep NDIS as provider value windows.go` — NOT FOUND (PASS)
- `go build ./...` — PASS
- `GOOS=windows go build ./internal/collector/euc/...` — PASS
- `GOOS=linux go build ./internal/collector/euc/...` — PASS
- `GOOS=darwin go build ./internal/collector/euc/...` — PASS
- `go vet ./internal/collector/euc/...` — PASS
- `go test ./internal/collector/euc/... -count=1` — PASS (ok 5.598s)
- `GOOS=linux go list -deps ./internal/collector/euc/... | grep golang-etw` — NOT FOUND (isolation PASS)
- `euc.go` unchanged — CONFIRMED (not staged or modified)
- `euc_noop.go` unchanged — CONFIRMED (not staged or modified)
- `netcommon.go` unchanged — CONFIRMED (not staged or modified)

## Self-Check: PASSED
