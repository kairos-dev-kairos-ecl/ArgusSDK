---
plan: 06-04
phase: 06-euc-os-collectors
wave: 2
status: complete
date: 2026-06-12
commits:
  - d93f765  # feat(06-04): exported NewOSCollector + agent wiring
  - a01b840  # ci(06-04): cross-compile matrix + dep-isolation assertions
---

# Plan 06-04 Summary — Exported NewOSCollector + Agent Wiring + CI Matrix

## What Was Built

### Task 1 — Exported build-tag-selected OSCollector constructor + agent wiring

- **`internal/collector/euc/oscollector.go`** (NO build tag — compiled on every GOOS):
  ```go
  func NewOSCollector(cfg Config) OSCollector {
      impl := newOSCollector(cfg)
      if impl == nil {
          return NewNoopOSCollector()
      }
      return impl
  }
  ```
  Delegates to the GOOS-selected `newOSCollector(cfg)` (eBPF on Linux, ETW on Windows, gopsutil
  sampler on darwin). Falls back to `NewNoopOSCollector()` if the impl is nil (future target
  without a platform impl). Imports nothing platform-specific — cannot leak heavy deps (SC-5).

- **`internal/collector/euc/oscollector_test.go`** (NO build tag — runs in default CI):
  Asserts `NewOSCollector(cfg)` returns non-nil and that `Start` + `Close` are degrade-safe on
  the host platform without special privileges.

- **`internal/agent/agent.go`** — EUC wiring change:
  - Before: `euc.New(eucCfg, euc.NewNoopOSCollector())`
  - After: `euc.New(eucCfg, euc.NewOSCollector(eucCfg))`
  - Comment updated to reflect the build-tag-selected impl with noop fallback.
  - No new config fields added; `AIEndpoints`/`LocalInferencePorts` hot-reload is Phase 7.

### Task 2 — Cross-compile matrix + dep-isolation CI assertion

Added job **`euc-cross-compile`** to `.github/workflows/ci.yml` with steps on `ubuntu-latest`:
1. `GOOS=linux go build ./...` — full linux cross-compile (eBPF skeleton committed; no clang)
2. `GOOS=windows go build ./...` — full windows cross-compile
3. `GOOS=darwin go build ./...` — full darwin cross-compile
4. Isolation assertion: `GOOS=darwin go list -deps ./internal/collector/euc/...` must NOT contain
   `github.com/cilium/ebpf` or `github.com/0xrawsec/golang-etw` (covers the generated bpf2go
   skeleton path in package euc). Job fails on match.
5. Cross-isolation: windows must not pull cilium/ebpf; linux must not pull golang-etw.

All existing jobs (build-vet, race, integration) retained and unchanged.

## Verification Results

```
go build ./...                                         PASS
GOOS=linux go build ./...                              PASS
GOOS=windows go build ./...                            PASS
GOOS=darwin go build ./...                             PASS
go vet ./internal/collector/euc/... ./internal/agent/... PASS
go test ./internal/collector/euc/... ./internal/agent/... -count=1
  ok  github.com/.../collector/euc   6.187s
  ok  github.com/.../agent           0.692s
```

**Isolation check (darwin — CI assertion replicated locally):**
```
GOOS=darwin go list -deps ./internal/collector/euc/...
# → github.com/cilium/ebpf  NOT present
# → github.com/0xrawsec/golang-etw  NOT present
# result: OK — heavy platform deps absent from GOOS=darwin
```

**Frozen shape contracts:** `test/llmsignal/euc_local_inference_test.go` and the euc fanOut tests
are unchanged and remain green — the `Observation→signal` contract was not modified by this plan.

## Success Criteria Status

| SC | Status | Evidence |
|----|--------|---------|
| SC-5: noop remains universal fallback; build ./... succeeds all GOOS | PASS | all three cross-compile targets pass |
| Agent wires build-tag-selected collector via NewOSCollector | PASS | agent.go euc.NewOSCollector(eucCfg) |
| CI asserts GOOS=darwin isolation (incl. bpf2go skeleton path) | PASS | euc-cross-compile job; isolation steps |
| Frozen Observation→signal shape unchanged | PASS | no changes to euc.go/fanOut/shape tests |
| R-61..R-65 build-verified end-to-end through agent seam | PASS | agent → NewOSCollector → per-platform |

## Files Changed

```
internal/collector/euc/oscollector.go      (new — exported constructor, no build tag)
internal/collector/euc/oscollector_test.go (new — host-agnostic degrade-safe test)
internal/agent/agent.go                    (updated — euc.NewOSCollector(eucCfg))
.github/workflows/ci.yml                   (updated — euc-cross-compile job added)
```

No changes to: `euc.go`, `euc_noop.go`, `netcommon.go`, `Observation`, `Config`, `fanOut`,
`linux.go`, `windows.go`, `darwin.go`, `oscollector.go` Wave-0/1 per-platform impls.
