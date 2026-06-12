---
plan: 06-01
phase: 06-euc-os-collectors
wave: 1
status: complete
date: 2026-06-12
commits:
  - 9ebc080  # feat(06-01): eBPF C source + bpf2go gen directive + committed skeleton
  - 33bf5f0  # feat(06-01): real Linux eBPF OSCollector + gopsutil local-port path
---

# Plan 06-01 Summary — Linux eBPF OSCollector + gopsutil local-port path

## What Was Built

Replaced the Linux stub (`internal/collector/euc/linux.go`) with a real eBPF-based Shadow-AI
OSCollector using `github.com/cilium/ebpf v0.21.0` (pure-Go, no cgo).

### Task 2 — eBPF C source + bpf2go skeleton in package euc

- **`internal/collector/euc/bpf/tcpconnect.c`**: kprobe program capturing outbound TCP connect
  events (tcp_v4_connect + tcp_v6_connect). Emits fixed-size event records to a ringbuf map named
  `Events`: remote IPv4/IPv6 address, remote port, address family, connecting task PID + comm
  (bounded to `TASK_COMM_LEN`). Connection metadata only — no payload, no general process scan.

- **`internal/collector/euc/gen.go`** (`//go:build ignore`): `//go:generate` directive running
  bpf2go with `-target bpfel,bpfeb -type event` pointing at `./bpf/tcpconnect.c`. Regeneration
  requires clang + kernel headers; committed artifacts mean agent build needs no clang/cgo.

- **`internal/collector/euc/tcpconnect_bpfel.go` / `tcpconnect_bpfeb.go`**: bpf2go-generated
  skeleton committed directly into `package euc` (NOT a subpackage). Both files carry
  `//go:build (amd64 || arm64 || ...) && linux` constraints — `cilium/ebpf` is reachable ONLY from
  GOOS=linux builds.

- **`internal/collector/euc/tcpconnect_bpfel.o` / `tcpconnect_bpfeb.o`**: precompiled ELF objects
  committed alongside the .go skeleton. Runtime load requires no compilation.

### Task 3 — Real Linux OSCollector + gopsutil local-port path

- **`internal/collector/euc/linux.go`** (`//go:build linux`): `linuxCollector` struct with:
  - `Start(ctx, out)`: loads embedded objects via same-package `loadTcpconnectObjects`; attaches
    kprobe to BOTH `tcp_v4_connect` and `tcp_v6_connect` (Pitfall 1); opens ringbuf reader; spawns
    `readLoop` goroutine decoding events and filtering via `matchHost`/`matchPort` helpers from
    `netcommon.go` (06-00). On EPERM/EACCES: logs a warning, returns nil (degrade, never crash).
  - gopsutil local-inference ticker: polls `gnet.ConnectionsWithContext(ctx, "tcp")` even when eBPF
    degraded; matches own-user listening / loopback-established sockets against
    `cfg.LocalInferencePorts` via `isLocalInferencePort`; de-dupes repeats; emits
    `Observation{IsLocal:true}` on out.
  - `Close()`: detaches kprobes, closes ringbuf + objects, stops gopsutil ticker; `sync.Once`-safe.

- **`internal/collector/euc/linux_test.go`** (`//go:build linux`):
  - Degrade test: asserts `Start` returns nil when CAP_BPF is absent (no panic; eBPF load fails →
    warn + continue).
  - Local-port test: opens real TCP listener, puts port in `cfg.LocalInferencePorts`, asserts
    `Observation{IsLocal:true}` arrives within timeout via the gopsutil path; `t.Skip` if connection
    table is unreadable.
  - All tests CI-runnable on Ubuntu without root.

- **`go.mod` / `go.sum`**: `github.com/cilium/ebpf v0.21.0` added as direct dependency.
  `github.com/shirou/gopsutil/v4` promoted to direct (already in tree; no version bump).

## Verification Results

```
go build ./...                                         PASS
GOOS=linux go build ./internal/collector/euc/...      PASS
GOOS=windows go build ./internal/collector/euc/...    PASS
GOOS=darwin go build ./internal/collector/euc/...     PASS
go vet ./internal/collector/euc/...                   PASS
go test ./internal/collector/euc/... -count=1         ok (5.618s)
```

**Isolation check:**
```
GOOS=darwin go list -deps ./internal/collector/euc/...
# → github.com/cilium/ebpf NOT in output (eBPF impl + generated skeleton confined to GOOS=linux)
```

## Success Criteria Status

| SC | Status | Evidence |
|----|--------|---------|
| SC-1: Linux eBPF observer degrades without privileges | PASS | degrade test asserts Start returns nil |
| SC-4: Config-driven watch lists (AIEndpoints + LocalInferencePorts) | PASS | matchHost/matchPort from 06-00 |
| SC-5: linux_test.go build-tagged; t.Skip off-privilege | PASS | `//go:build linux`; degrade + local-port tests skip cleanly |
| SC-6: Low-privilege contract (no process enum / packet capture) | PASS | 5-tuple metadata only; ProcessName from event comm only |
| Cross-compile: all three GOOS succeed | PASS | all build targets verified |
| cilium/ebpf confined to GOOS=linux | PASS | isolation check above |

## Module Verification

`github.com/cilium/ebpf v0.21.0` — verified on pkg.go.dev before `go get`. Maintained by
Cilium/Cloudflare. Pure-Go (no required cgo for load/attach path). Import path exactly
`github.com/cilium/ebpf` (not a typosquat). Task 1 human-verify checkpoint cleared.

## Files Changed

```
internal/collector/euc/bpf/tcpconnect.c   (new)
internal/collector/euc/gen.go             (new)
internal/collector/euc/tcpconnect_bpfel.go (new — generated, committed)
internal/collector/euc/tcpconnect_bpfeb.go (new — generated, committed)
internal/collector/euc/tcpconnect_bpfel.o  (new — committed ELF object)
internal/collector/euc/tcpconnect_bpfeb.o  (new — committed ELF object)
internal/collector/euc/linux.go            (replaced stub with real impl)
internal/collector/euc/linux_test.go       (new)
go.mod                                     (cilium/ebpf v0.21.0 + gopsutil promoted)
go.sum                                     (updated)
```

No changes to: `euc.go`, `euc_noop.go`, `netcommon.go`, `Observation`, `Config`, `fanOut`.
