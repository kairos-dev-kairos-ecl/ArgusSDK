# Phase 6: EUC OS Collectors - Research

**Researched:** 2026-06-12
**Domain:** OS-level network observation (eBPF / ETW / Network Extension) behind a Go interface seam
**Confidence:** HIGH (Linux, Windows libraries + build isolation), MEDIUM (macOS shippability tradeoff)

## Summary

This phase replaces three stub `OSCollector` bodies with real Shadow-AI network observers, one per GOOS, behind the existing `euc.OSCollector` seam. The seam is already clean: `newOSCollector(cfg Config) OSCollector` is selected at build time by GOOS-tagged files, the `Collector.fanOut` pipeline and `Observation` shape are frozen (do NOT touch), and the no-op stays as the universal fallback. The whole job is: open a platform capture, filter events to `cfg.AIEndpoints` / `cfg.LocalInferencePorts`, map matches to `Observation`, forward on the channel, and degrade to no-op-like behavior (log + emit nothing) when the OS denies privilege.

The two large platforms have mature, **pure-Go (no cgo)** libraries that are ideal given the dev/CI constraints (Windows dev box, ubuntu CI, no local C compiler): **`github.com/cilium/ebpf` v0.21.0** for Linux (with a precompiled `.o` embedded via `bpf2go`, so no clang at build/runtime on the agent) and **`github.com/0xrawsec/golang-etw` v1.6.2** for Windows ETW. macOS is the honesty problem: a real `NEDNSProxyProvider` Network Extension requires a packaged `.app` + signed system extension + the *managed* `com.apple.developer.networking.networkextension` entitlement + notarization — none of which a `go install`'d CLI can satisfy. The shippable v1.0 recommendation for darwin is a **low-privilege established-connection sampler** (libproc/`lsof`-equivalent, no root) that honors the contract, with the full Network Extension explicitly documented and descoped.

**Primary recommendation:** Linux = cilium/ebpf with a `kprobe/tcp_connect` (IPv4+IPv6) precompiled object embedded via bpf2go, degrade-to-noop when `CAP_BPF`/`CAP_PERFMON` absent. Windows = golang-etw real-time session on `Microsoft-Windows-Kernel-Network`, filter to watched remote hosts/ports. macOS = ship a no-root established-connection sampler now; gate the Network Extension behind a follow-on packaging phase. All three isolated behind GOOS build tags; nothing enters the default `go test ./...`.

## User Constraints (from CONTEXT.md)

### Locked Decisions
- Implement **all three** platforms this phase: `linux.go` (eBPF), `windows.go` (ETW/WFP), `darwin.go` (Network Extension). One plan per platform (disjoint files → parallelizable in Wave 1).
- Each platform file is build-tagged for its GOOS and provides the real `OSCollector` via the existing seam. The no-op collector (`euc_noop.go` / `NewNoopOSCollector`) **REMAINS** as the fallback for unsupported targets and for environments lacking privileges/capabilities.
- `go build ./...` must succeed when cross-compiling for linux, windows, and darwin (build tags must isolate platform-specific imports so non-target builds never pull them in).
- Observe DNS queries and/or TCP connections to: (a) hostnames in `euc.Config.AIEndpoints`, and (b) local inference ports in `euc.Config.LocalInferencePorts`.
- Emit `euc.Observation{ConnectedHost, LocalPort, IsLocal, ProcessName, Username}` — ProcessName/Username are **BEST-EFFORT** (may be empty without elevated privilege).
- Watch lists come from `euc.Config` at Start (config-driven, never hardcoded). Design so a list update is feasible, but do **not** build the SIGHUP path here.
- **PRESERVE the low-privilege contract** (package doc + threat model T-04-14): NO general process enumeration, NO file-access monitoring, NO full packet/flow capture, NO AI API payload/content inspection. Capture only connection metadata. Degrade gracefully (log + fall back) when privilege is denied — never crash.
- New OS-capture deps permitted **only where genuinely required**, **isolated behind GOOS build tags**, kept minimal.
- Each platform impl has tests that run on that platform and `t.Skip` cleanly elsewhere. Where capture needs privilege/hardware not in CI, gate behind a build tag and/or env probe and skip — never hard-fail. Default suite needs no Docker.

### Claude's Discretion
- Exact library per platform (eBPF loader, ETW/WFP binding, NetExt packaging), the precise capture mechanism (DNS hook vs connect() kprobe vs flow events), and how ProcessName/Username are obtained within the low-privilege contract — researcher recommends, planner locks per this RESEARCH.md.

### Deferred Ideas (OUT OF SCOPE)
- SIGHUP / live hot-reload of the watch list → Phase 7.
- Corporate proxy/DNS telemetry ingestion → post-v1.0 unless trivial.
- Deep per-process attribution requiring elevated privilege beyond the contract → out of scope.
- Any change to `euc.fanOut`, `euc.Observation`, `euc.Config`, or the signal schema → out of scope.

## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| LINUX | Real eBPF `OSCollector` in `linux.go`, build-tagged `linux` | cilium/ebpf v0.21.0 + bpf2go precompiled object; kprobe on `tcp_connect`; degrade on missing CAP_BPF |
| WINDOWS | Real ETW/WFP `OSCollector` in `windows.go`, build-tagged `windows` | golang-etw v1.6.2 real-time session on `Microsoft-Windows-Kernel-Network` |
| DARWIN | Real `OSCollector` in `darwin.go`, build-tagged `darwin` | Shippable v1.0 = no-root established-connection sampler (libproc); full NetExt documented + descoped |
| ISOLATION | `go build ./...` cross-compiles all 3; default `go test ./...` pulls in no platform deps | GOOS build tags on every file; deps imported only inside tagged files; pure-Go (no cgo) libs |
| CONTRACT | Best-effort ProcessName/Username, no process enum / file mon / packet capture | Connection-metadata-only mechanisms per platform; graceful degradation paths documented |
| TEST | Per-platform tests that `t.Skip` elsewhere; no Docker; CI-runnable subset | gopsutil/libproc local-port path is CI-runnable on ubuntu; privileged capture gated behind build tag + env probe |

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Outbound TCP/DNS observation to AI endpoints | OS kernel hook (eBPF/ETW) | userspace filter | Connection events originate in kernel; only the kernel sees them pre-encryption-agnostic at connect time |
| Local inference port detection | OS connection table (no root) | userspace filter | Listening/established sockets on `LocalInferencePorts` are readable via libproc/`/proc`/IP Helper without privilege |
| Watch-list filtering | userspace (Go, per-platform impl) | — | `cfg.AIEndpoints`/`LocalInferencePorts` matched in Go after the OS surfaces the event |
| Observation → signal conversion | `euc.Collector.fanOut` (frozen) | — | Already built; impls only PRODUCE `Observation`, never convert |
| Privilege detection / degradation | userspace (per-platform impl) | no-op fallback | Each impl probes capability at Start, logs, returns/falls back without crashing |

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/cilium/ebpf` | v0.21.0 | Linux: load precompiled eBPF object, attach kprobe, read perf/ringbuf events | [VERIFIED: Go module proxy] Pure-Go, **no cgo**, no libbpf runtime dep; maintained by Cilium + Cloudflare; de-facto standard Go eBPF loader [CITED: github.com/cilium/ebpf] |
| `github.com/0xrawsec/golang-etw` | v1.6.2 | Windows: real-time ETW session + consumer for `Microsoft-Windows-Kernel-Network` | [VERIFIED: Go module proxy] Pure-Go, **no cgo** ("no need to enable CGO"); the maintained Go ETW consumer [CITED: github.com/0xrawsec/golang-etw] |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `github.com/shirou/gopsutil/v4` | v4.26.3 (**already an indirect dep**) | Cross-platform local connection table (`net.Connections`) for local-inference port detection without root | Use for the `IsLocal`/`LocalPort` path on all platforms; promotes an existing dep to direct — zero new module |
| `golang.org/x/sys` | v0.44.0 (**already present**) | Windows IP Helper (`GetExtendedTcpTable` via syscall) and Linux/darwin syscalls as a fallback | Already in the tree; no new dep. Optional alternative to gopsutil if a tighter footprint is wanted |
| `github.com/cilium/ebpf/cmd/bpf2go` | (tool, build-time only) | `go generate` step that compiles the `.c` to a `.o` and emits an embedded Go skeleton | Run once on a machine with clang (CI Linux job), commit the generated `_bpfel.o` + `.go` — agent never needs clang |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| cilium/ebpf | `aquasecurity/libbpfgo` | Requires cgo + libbpf headers → breaks "no local C compiler" and complicates cross-compile. **Reject.** |
| Linux kprobe on `tcp_connect` | cgroup/connect4+connect6 BPF hook | cgroup hook gives clean per-connection events but needs a cgroup mount + arguably higher privilege; kprobe is simpler and well-trodden. Note kprobe symbol stability across kernels (see Pitfalls). |
| golang-etw | WFP callout driver (kernel) | WFP callouts require a signed kernel driver — far beyond a low-privilege user service. **Reject** for v1.0. WFP `FWPM` *filter* user-mode API is possible but heavier than ETW for read-only observation. |
| golang-etw | `tekert/goetw` (fork) | Active fork with extras, but 0xrawsec is the canonical, more widely used base. Prefer 0xrawsec unless a specific feature is missing. |
| macOS Network Extension | libproc established-connection sampler | NetExt = real-time + complete but needs signing/entitlement/packaging (not shippable as a CLI). Sampler = honest, no-root, ships now. **Recommend sampler for v1.0.** |

**Installation:**
```bash
go get github.com/cilium/ebpf@v0.21.0          # linux.go only (build-tagged)
go get github.com/0xrawsec/golang-etw@v1.6.2   # windows.go only (build-tagged)
# gopsutil/v4 and x/sys are already in go.mod — promote gopsutil to direct on first use
```

**Version verification:**
- `cilium/ebpf` — `go list -m -versions` returned up to **v0.21.0** [VERIFIED: Go module proxy]
- `golang-etw` — `go list -m -versions` returned up to **v1.6.2** [VERIFIED: Go module proxy]
- `gopsutil/v4 v4.26.3` and `golang.org/x/sys v0.44.0` — already pinned in `go.mod` [VERIFIED: codebase]

## Package Legitimacy Audit

> slopcheck v0.6.1 is installed but supports only npm/PyPI ecosystems; it does not validate Go modules. Go modules are verified against the **Go module proxy** (`go list -m -versions`), the authoritative registry for this ecosystem, plus source-repo confirmation. Cross-ecosystem confusion is not applicable (Go import paths are repo-qualified).

| Package | Registry | Age / Maturity | Source Repo | slopcheck | Disposition |
|---------|----------|----------------|-------------|-----------|-------------|
| `github.com/cilium/ebpf` | Go proxy | Years; Cilium/Cloudflare maintained | github.com/cilium/ebpf | N/A (Go) | Approved (proxy-verified v0.21.0) |
| `github.com/0xrawsec/golang-etw` | Go proxy | Years; established security-tooling author | github.com/0xrawsec/golang-etw | N/A (Go) | Approved (proxy-verified v1.6.2) |
| `github.com/shirou/gopsutil/v4` | Go proxy | Mature; already an indirect dep | github.com/shirou/gopsutil | N/A (Go) | Approved (in-tree) |

**Packages removed due to slopcheck [SLOP]:** none
**Packages flagged [SUS]:** none — but because slopcheck does not cover Go, the planner SHOULD still keep the two NEW modules (`cilium/ebpf`, `golang-etw`) behind a single `checkpoint:human-verify` at first `go get` as a defense-in-depth step.

## Architecture Patterns

### System Architecture Diagram

```
                        cfg.AIEndpoints / cfg.LocalInferencePorts (from euc.Config at Start)
                                              │
        ┌─────────────────────────────────────┼─────────────────────────────────────┐
        │ linux.go (//go:build linux)          │ windows.go (//go:build windows)      │ darwin.go (//go:build darwin)
        │                                      │                                      │
   ┌────▼─────┐  privilege probe          ┌────▼─────┐  session start            ┌────▼─────┐  (no root)
   │ CAP_BPF? │──no──► log + return nil   │ ETW perm?│──no──► log + return nil   │ libproc  │
   └────┬─────┘       (degrade)           └────┬─────┘       (degrade)           │ sampler  │
        │yes                                   │yes                              └────┬─────┘
   ┌────▼─────────────┐                   ┌────▼──────────────────┐                  │ poll established
   │ load embedded .o │                   │ realtime session on   │                  │ + listening sockets
   │ attach kprobe    │                   │ Microsoft-Windows-    │                  │ on watched ports
   │ tcp_connect v4/v6│                   │ Kernel-Network        │                  │
   │ read ringbuf     │                   │ consume TcpConnect    │                  │
   └────┬─────────────┘                   └────┬──────────────────┘                  │
        │ raw conn events                      │ raw conn events                     │
        └──────────────┬───────────────────────┴──────────────┬─────────────────────┘
                       ▼                                        ▼
              ┌──────────────────────────────────────────────────────┐
              │  userspace filter (Go): match remote host ∈ AIEndpoints │
              │  OR local port ∈ LocalInferencePorts; set IsLocal       │
              │  best-effort ProcessName/Username (only if cheap+allowed)│
              └───────────────────────────┬──────────────────────────────┘
                                          ▼ euc.Observation
                          obs chan<- Observation  (provided by Collector.Start)
                                          ▼
                    euc.Collector.fanOut  ── FROZEN ──►  signal.Batch ──► ingest channel
```

File-to-impl mapping lives in the table below, not the diagram.

### Recommended Project Structure
```
internal/collector/euc/
├── euc.go              # FROZEN: interface, Observation, Config, fanOut
├── euc_noop.go         # FROZEN: NewNoopOSCollector fallback (retain)
├── linux.go            # //go:build linux   — cilium/ebpf impl
├── linux_test.go       # //go:build linux   — t.Skip when not root / no CAP_BPF
├── bpf/                # eBPF C source + bpf2go generate directive
│   ├── tcpconnect.c    # kprobe program (committed)
│   ├── gen.go          # //go:generate bpf2go ... (build tag: ignore)
│   └── tcpconnect_bpfel.o + _bpfel.go  # GENERATED, committed (no clang on agent)
├── windows.go          # //go:build windows — golang-etw impl
├── windows_test.go     # //go:build windows — t.Skip when ETW session denied
├── darwin.go           # //go:build darwin  — libproc sampler impl
├── darwin_test.go      # //go:build darwin
└── netcommon.go        # (optional) shared, build-tag-free helpers: host match, port match
```

### Pattern 1: Build-tag isolation of platform deps
**What:** Each platform file carries `//go:build <goos>` (already present on the stubs). The new heavy import (`cilium/ebpf`, `golang-etw`) appears **only** inside its tagged file. `go build ./...` for a non-target GOOS never compiles that file, so the dep never enters that build graph, and the default `go test ./...` (host GOOS) only pulls in the host's dep.
**When to use:** Every platform impl + its test.
**Example:**
```go
//go:build linux

package euc

import "github.com/cilium/ebpf/link" // only compiled for GOOS=linux
```
Cross-compile verification command the planner should bake into a task:
```bash
GOOS=linux   go build ./...   # pulls cilium/ebpf
GOOS=windows go build ./...   # pulls golang-etw, NOT ebpf
GOOS=darwin  go build ./...   # pulls neither heavy dep
go vet ./internal/collector/euc/...
```

### Pattern 2: Precompiled eBPF object via bpf2go (no clang on the agent or in the agent build)
**What:** Compile the `.c` to an ELF `.o` once (CI Linux job or a maintainer machine with clang), embed it through the bpf2go-generated `.go` skeleton, and `go:embed`/load it at runtime. The agent binary and `go build` need **no** C compiler. cilium/ebpf is pure-Go for the load/attach side. [CITED: github.com/cilium/ebpf — bpf2go ships compiled code as ELF embedded into a Go package]
**When to use:** The Linux impl. This is what makes Linux viable given "no local C compiler on dev, CI is Linux."
**Note:** The `go generate` (clang) step runs in CI/maintainer context; the generated artifacts are committed. Document that regenerating requires clang + kernel headers.

### Pattern 3: Privilege-probe-then-degrade at Start
**What:** `Start` first checks whether the required privilege is available (Linux: attempt the load/attach and detect `EPERM`/`EACCES`, or pre-check effective caps; Windows: attempt to start the realtime session and detect access-denied; darwin: sampler needs no privilege). On denial: log a warning and return `nil` (collector runs, emits nothing) — mirroring the no-op so the agent stays up. Never `panic`, never return an error that aborts agent start.
**When to use:** All three Start paths.

### Anti-Patterns to Avoid
- **Enumerating all processes to attribute a connection.** Violates the contract (no general process enumeration). Only resolve ProcessName for the *specific* connection that already matched a watched host/port, and only if the OS hands it back cheaply (e.g., ETW event field, eBPF `bpf_get_current_comm` for the connecting task). If it requires scanning `/proc` or `ToolHelp32Snapshot` of everything, leave it empty.
- **Full packet/flow capture (libpcap, XDP packet mirroring, NDIS PacketCapture).** Out of contract. Capture connection *metadata* (5-tuple at connect time), never payload. The stub's comment about `Microsoft-Windows-NDIS-PacketCapture` should be **rejected** in favor of `Microsoft-Windows-Kernel-Network` connect events.
- **Returning an error from Start when privilege is missing.** Aborts the whole agent. Degrade instead.
- **Hardcoding endpoints/ports.** Read exclusively from `cfg`.
- **Touching `fanOut`/`Observation`/`Config`.** Frozen by CONTEXT.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| eBPF program load + kprobe attach + map/ringbuf read | Raw `bpf()` syscalls + perf-event plumbing | `cilium/ebpf` (+ `link`, `ringbuf`) | Hundreds of edge cases: BTF relocation, verifier interaction, ringbuf epoll loop, kernel-version probing |
| ETW realtime session + schema decode | Raw `StartTrace`/`OpenTrace`/`ProcessTrace` via syscall | `golang-etw` | TDH event decoding, session lifecycle, MOF/manifest parsing are large and error-prone |
| Local connection table (established/listening sockets) | Parse `/proc/net/tcp`, `netstat`, `GetExtendedTcpTable` by hand per OS | `gopsutil/v4 net.Connections` (already in tree) | Cross-platform, no root for own-user sockets, already a dep |
| eBPF compilation toolchain at agent runtime | Shipping clang or compiling on the endpoint | `bpf2go` precompiled + embedded object | Endpoints don't have clang; CO-RE/precompiled keeps the agent self-contained |

**Key insight:** Every "capture mechanism" here has a maintained, pure-Go library that already solved the privilege/decode/lifecycle minefield. The phase's real work is the thin **filter + map-to-Observation + degrade** glue, not the capture primitives.

## Common Pitfalls

### Pitfall 1: kprobe symbol instability across kernels (Linux)
**What goes wrong:** Attaching a kprobe to `tcp_connect` (or `tcp_v4_connect`/`tcp_v6_connect`) fails on kernels where the symbol name/signature differs or is inlined.
**Why it happens:** Kernel internal symbols are not a stable ABI; different distros/versions inline or rename.
**How to avoid:** Prefer attaching to **both** `tcp_v4_connect` and `tcp_v6_connect` (more stable than a single hook), or use a tracepoint if a suitable one exists. Treat attach failure as the degrade path (log + emit nothing), not a crash. Document the tested minimum kernel (target a recent LTS, e.g. 5.15+). [CITED: cilium/ebpf docs; kernel kprobe semantics]
**Warning signs:** `link.Kprobe` returns "no such file or directory" / symbol-not-found at Start.

### Pitfall 2: BPF privilege model is effectively root (Linux)
**What goes wrong:** Loading the program / attaching the kprobe returns `EPERM` for a non-root, no-caps user.
**Why it happens:** kprobe (perfmon) programs require `CAP_BPF` **and** `CAP_PERFMON` (and historically `CAP_SYS_ADMIN`); unprivileged BPF is commonly disabled (`kernel.unprivileged_bpf_disabled`, default-locked since 5.13). [CITED: bpfman Linux capabilities; kernel 5.13/5.19 changes]
**How to avoid:** Probe capability at Start; if missing, degrade. Document that production deployment grants `CAP_BPF,CAP_PERFMON` to the agent (systemd `AmbientCapabilities=`) rather than running as root. The local-inference port path (gopsutil) needs **no** caps — keep it working even when the eBPF path degrades, so the collector still produces `euc.local_inference` signals.

### Pitfall 3: ETW realtime session needs elevation / specific rights (Windows)
**What goes wrong:** Starting a realtime session on a kernel provider fails with access-denied for a plain user.
**Why it happens:** Realtime ETW sessions for kernel providers typically require administrator or membership in **Performance Log Users**.
**How to avoid:** Detect the access-denied on session start and degrade. Document that the agent service should run with the minimal right (Performance Log Users group) rather than SYSTEM/Admin. As with Linux, the gopsutil local-port path needs no elevation and should still run.
**Warning signs:** session start returns `ERROR_ACCESS_DENIED` (5).

### Pitfall 4: macOS Network Extension cannot ship in a `go install` CLI
**What goes wrong:** Planning a real `NEDNSProxyProvider`/`NEFilterDataProvider` for v1.0 stalls the phase.
**Why it happens:** A system extension must live inside a signed `.app` bundle, carry the **managed** `com.apple.developer.networking.networkextension` entitlement (Apple-approved), be signed with a Developer ID, notarized, and activated via `OSSystemExtensionRequest` + user approval. A standalone Go binary distributed via `go install` satisfies none of these. [CITED: developer.apple.com NEDNSProxyProvider; networkextension entitlement; macOS Catalina+ signing/notarization]
**How to avoid:** For v1.0, implement the **no-root established-connection sampler** in `darwin.go` (libproc/gopsutil `net.Connections`): poll established outbound connections + listening sockets, match against `cfg`, emit Observations. It honors the low-privilege contract, ships as a CLI, and produces the same `Observation` shape. Document the full Network Extension as a future packaging phase.
**Warning signs:** Any plan task that references entitlements/signing/`.app` bundles inside *this* phase — that's a descope signal.

### Pitfall 5: Over-attribution breaks the contract
**What goes wrong:** Implementer adds `/proc` scans or `ToolHelp32Snapshot` to fill ProcessName/Username for every event.
**Why it happens:** The `Observation` has the fields, so it feels mandatory.
**How to avoid:** Fields are **best-effort**. Only populate from data the matched event already carries (eBPF `comm`/PID of the connecting task, ETW event PID resolved to a single process). Leave empty otherwise. No global enumeration.

### Pitfall 6: Default `go test ./...` accidentally pulls a platform dep
**What goes wrong:** A shared helper file forgets its build tag and imports `cilium/ebpf`, dragging it into every build.
**Why it happens:** Helper code placed without a tag.
**How to avoid:** Heavy imports live ONLY in `linux.go`/`windows.go`. Shared, tag-free helpers (`netcommon.go`) must import nothing platform-specific. Add a CI matrix step asserting `GOOS=darwin go build ./...` produces no `cilium/ebpf`/`golang-etw` in `go list -deps`.

## Runtime State Inventory

> This phase adds new capture mechanisms; it does not rename or migrate stored state. Inventory included for completeness.

| Category | Items Found | Action Required |
|----------|-------------|------------------|
| Stored data | None — collectors are stateless, emit transient Observations | None — verified: no datastore writes in euc package |
| Live service config | OS-registered capture sessions are created/destroyed within Start/Close (ETW session, eBPF link). These are process-lifetime, not persisted to disk. | Ensure Close fully tears down (detach kprobe, stop ETW session) to avoid orphaned sessions across restarts |
| OS-registered state | Windows ETW realtime session has a session *name* — if the agent crashes, a stale session can linger until reboot/manual `logman stop`. | Use a deterministic session name and stop-if-exists on Start; document `logman query -ets` cleanup |
| Secrets/env vars | None | None |
| Build artifacts | New: committed bpf2go output (`tcpconnect_bpfel.o`, `_bpfel.go`). Stale if `.c` changes without regenerating. | Document `go generate ./internal/collector/euc/bpf/...` (needs clang) as the regeneration step |

## Code Examples

### Linux: load embedded object + attach kprobe (shape)
```go
//go:build linux
// Source: pattern per github.com/cilium/ebpf (link + ringbuf), see github.com/cilium/ebpf examples
package euc

func (c *linuxCollector) Start(ctx context.Context, out chan<- Observation) error {
    objs := tcpconnectObjects{}            // generated by bpf2go (embedded .o)
    if err := loadTcpconnectObjects(&objs, nil); err != nil {
        c.log.Warn("eBPF load failed; degrading to no-op", zap.Error(err))
        return nil                          // degrade, do not abort agent
    }
    kp4, err := link.Kprobe("tcp_v4_connect", objs.TcpV4Connect, nil)
    // ... kp6 for tcp_v6_connect ...
    if err != nil {
        c.log.Warn("kprobe attach failed; degrading", zap.Error(err))
        objs.Close(); return nil
    }
    rd, _ := ringbuf.NewReader(objs.Events)
    go c.readLoop(ctx, rd, out)            // decode event → filter cfg → Observation
    return nil
}
```

### Windows: ETW realtime session on Kernel-Network (shape)
```go
//go:build windows
// Source: pattern per github.com/0xrawsec/golang-etw README (NewRealTimeSession + Consumer)
package euc

func (c *windowsCollector) Start(ctx context.Context, out chan<- Observation) error {
    s := etw.NewRealTimeSession("ArgusEUC")
    if err := s.EnableProvider(etw.MustParseProvider("Microsoft-Windows-Kernel-Network")); err != nil {
        c.log.Warn("ETW provider enable failed; degrading", zap.Error(err)); return nil
    }
    cons := etw.NewConsumer(ctx).FromSessions(s)
    cons.EventCallback = func(e *etw.Event) error {
        host, port, pid := extractConn(e)   // remote addr / local port / pid
        if obs, ok := c.match(host, port, pid); ok {
            select { case out <- obs: case <-ctx.Done(): }
        }
        return nil
    }
    if err := cons.Start(); err != nil {    // access-denied → degrade
        c.log.Warn("ETW consumer start failed; degrading", zap.Error(err)); _ = s.Stop(); return nil
    }
    return nil
}
```

### macOS: no-root established-connection sampler (shape)
```go
//go:build darwin
// Source: gopsutil/v4 net.Connections — already an in-tree dependency
package euc

func (c *darwinCollector) Start(ctx context.Context, out chan<- Observation) error {
    go func() {
        t := time.NewTicker(c.interval) // e.g. 2s
        defer t.Stop()
        for {
            select {
            case <-ctx.Done(): return
            case <-t.C:
                conns, err := gnet.ConnectionsWithContext(ctx, "tcp")
                if err != nil { c.log.Warn("conn sample failed", zap.Error(err)); continue }
                for _, k := range conns {
                    if obs, ok := c.match(k); ok && c.notSeen(k) {
                        select { case out <- obs: case <-ctx.Done(): return }
                    }
                }
            }
        }
    }()
    return nil
}
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| libbpf + cgo for Go eBPF | Pure-Go `cilium/ebpf` + bpf2go precompiled objects | Mature since ~2022 | No cgo, no clang on target — fits this project's constraints exactly |
| Raw ETW via syscall / WMI | `golang-etw` pure-Go consumer | Stable | No cgo; realtime kernel-provider consumption from Go |
| Stub `NDIS-PacketCapture` (full capture) referenced in windows.go stub | `Microsoft-Windows-Kernel-Network` connect events (metadata only) | This phase | Honors no-full-capture contract |

**Deprecated/outdated:**
- The windows.go stub's suggestion of `Microsoft-Windows-NDIS-PacketCapture`: reject — that is packet capture, violating the contract. Use `Microsoft-Windows-Kernel-Network`.
- The darwin.go stub's assumption of a CGo system-extension helper in this phase: descope to a future packaging phase.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | `Microsoft-Windows-Kernel-Network` surfaces outbound TCP connect with remote address + local port + PID via golang-etw | Windows stack/examples | If field shape differs, executor may need `Microsoft-Windows-TCPIP` provider instead — same library, swap provider GUID. Verify on a Windows box during Wave 1. |
| A2 | gopsutil `net.Connections` returns own-user established/listening sockets without root on macOS and Linux | Supporting stack, darwin sampler | If it needs elevation for full table, sampler still sees own-process sockets (sufficient for local-inference detection); outbound-to-AI attribution may be partial. Verify per-platform. |
| A3 | Attaching to `tcp_v4_connect`/`tcp_v6_connect` is the most portable kprobe target for outbound connects | Linux pattern, Pitfall 1 | If inlined on target kernels, fall back to a tracepoint (`tcp/tcp_connect` if available) or fexit. Pin/document a tested minimum kernel. |
| A4 | The managed NetExt entitlement is not obtainable for a CLI, making a real Network Extension non-shippable for v1.0 | Pitfall 4, darwin recommendation | If the product later ships a signed `.app`, the full NetExt becomes viable — that's the deferred packaging phase, not a v1.0 blocker. |

## Open Questions

1. **Windows provider precision** — `Microsoft-Windows-Kernel-Network` vs `Microsoft-Windows-TCPIP` for the cleanest "outbound connect with remote host + PID" event.
   - What we know: golang-etw can enable either; both are kernel network providers.
   - What's unclear: which yields remote-host + local-port + PID most directly with least noise.
   - Recommendation: Wave-1 task on a Windows box dumps both with `etwdump` (ships with golang-etw) and the executor picks; library choice is unaffected.

2. **macOS darwin scope sign-off** — confirm the sampler (not a real Network Extension) is acceptable for v1.0.
   - What we know: CONTEXT locks "all three platforms" and "Network Extension" as the named darwin mechanism, but also locks the low-privilege/shippable contract and lets Claude choose the mechanism.
   - What's unclear: whether the sampler satisfies "Network Extension" intent or whether v1.0 accepts the documented-limited darwin impl.
   - Recommendation: planner surfaces this as a `checkpoint:human-verify` before the darwin plan — the sampler is the only thing shippable as a CLI; the real NetExt needs a packaging phase. (This is a discuss-phase / planner decision, flagged by [ASSUMED] A4.)

3. **ProcessName/Username best-effort depth** — how much attribution is "best-effort" vs over-reach.
   - Recommendation: populate only from the matched event's own PID/comm; never enumerate. Treat empty as acceptable.

## Environment Availability

| Dependency | Required By | Available (dev: win11) | Version | Fallback |
|------------|------------|------------------------|---------|----------|
| Go toolchain | all | ✓ | go1.26.1 | — |
| C compiler (clang/gcc) | eBPF **regeneration only** (bpf2go), not agent build | ✗ (dev) | — | Run `go generate` in CI Linux job / maintainer box; commit artifacts. Agent + `go build` need no compiler. |
| Linux kernel w/ BTF + CAP_BPF | Linux real eBPF capture at runtime | ✗ (dev is Windows) | — | Degrade to no-op when caps absent; gopsutil local-port path still works |
| Windows + Performance Log Users / Admin | Windows ETW realtime session | ✓ (dev is Windows; rights TBD) | win11 | Degrade when access-denied |
| macOS | darwin sampler | ✗ (no mac in dev/CI) | — | Build-tag compiles via cross-compile; runtime test is manual/gated |
| CI = ubuntu-latest | cross-compile build matrix + CI-runnable tests | ✓ | — | — |

**Missing dependencies with no fallback:** none block the phase.
**Missing with fallback:**
- No clang on dev → eBPF object is precompiled+committed; agent never compiles eBPF. Only *regenerating* the `.o` needs clang (do it in CI/maintainer context).
- No Linux/macOS runtime on dev → privileged capture is tested via gated tests on CI/target hardware; everything cross-compiles on the Windows dev box.

## Validation Architecture

> nyquist_validation not explicitly disabled in config → section included.

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go standard `testing` |
| Config file | none (Go convention) |
| Quick run command | `go test ./internal/collector/euc/...` |
| Full suite command | `go test ./...` |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| ISOLATION | All 3 GOOS cross-compile; non-target builds exclude heavy deps | build | `GOOS=linux/windows/darwin go build ./...` + `go list -deps` assertion | ❌ Wave 0 (CI matrix task) |
| CONTRACT/shape | Observation→signal shape unchanged | unit | `go test ./test/llmsignal/... -run EUC` (existing, must stay green) | ✅ existing |
| LINUX | eBPF impl produces Observation for watched host | integration (gated) | `go test -tags=euclinux ./internal/collector/euc/...` on Linux w/ CAP_BPF; else `t.Skip` | ❌ Wave 0 |
| WINDOWS | ETW impl produces Observation for watched host/port | integration (gated) | run on Windows w/ rights; else `t.Skip` | ❌ Wave 0 |
| DARWIN | sampler produces Observation for a live local port | integration (gated) | run on macOS; else `t.Skip` | ❌ Wave 0 |
| CONTRACT/degrade | Start returns nil (no crash) when privilege absent | unit | per-platform test asserting degrade path returns nil | ❌ Wave 0 |

### Sampling Rate
- **Per task commit:** `go test ./internal/collector/euc/...`
- **Per wave merge:** `go test ./...` + cross-compile matrix (`GOOS=linux,windows,darwin go build ./...`)
- **Phase gate:** full suite green + 3-GOOS build green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `internal/collector/euc/linux_test.go` — covers LINUX + degrade (skip when not root/no CAP_BPF)
- [ ] `internal/collector/euc/windows_test.go` — covers WINDOWS + degrade (skip when access-denied)
- [ ] `internal/collector/euc/darwin_test.go` — covers DARWIN sampler (skip when no live port)
- [ ] CI matrix step asserting `go list -deps` for `GOOS=darwin` contains neither `cilium/ebpf` nor `golang-etw`
- [ ] `internal/collector/euc/bpf/` directory + `tcpconnect.c` + `//go:generate bpf2go` + committed generated artifacts
- [ ] Reuse existing `fakeOSCollector` pattern (`test/llmsignal/fakes.go`) for any pure-shape tests — no new fake needed

## Security Domain

> security_enforcement not disabled → section included.

### Applicable ASVS Categories
| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | no | collector emits internal signals; no auth surface added |
| V3 Session Management | no | n/a |
| V4 Access Control | yes | Run with **least privilege**: `CAP_BPF,CAP_PERFMON` (not root) on Linux; Performance Log Users (not SYSTEM) on Windows; no entitlement-elevated on macOS. Degrade rather than escalate. |
| V5 Input Validation | yes | `cfg.AIEndpoints`/`LocalInferencePorts` and all decoded OS event fields (hostnames, addresses, PIDs) are untrusted → validate/bound before use; cap host string lengths; ignore malformed events |
| V6 Cryptography | no | no crypto introduced |
| V7 Error/Logging | yes | Degradation logs must NOT leak captured payloads (there are none) and must avoid logging full connection tables; log counts, not contents |

### Known Threat Patterns for OS-capture collectors
| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| Privilege escalation via capture mechanism (T-04-14) | Elevation of Privilege | Least-privilege caps/rights; degrade-not-escalate; never request SeDebugPrivilege / root |
| Over-capture beyond Shadow-AI scope (EDR overlap) | Information Disclosure | Filter to `cfg` watch lists in-kernel/early; no general process enum, no file mon, no packet payload |
| Orphaned ETW session after crash | Denial of Service (resource leak) | Deterministic session name; stop-if-exists on Start; documented `logman` cleanup |
| Untrusted event-field injection (hostname/PID) | Tampering | Validate/bound decoded fields; treat OS event data as untrusted input |
| Stale/poisoned committed eBPF object | Tampering | Object regenerated only in CI/maintainer context from committed `.c`; review diff on regeneration |

## Sources

### Primary (HIGH confidence)
- Go module proxy via `go list -m -versions` — cilium/ebpf **v0.21.0**, golang-etw **v1.6.2** [VERIFIED]
- `go.mod` (codebase) — gopsutil/v4 v4.26.3, golang.org/x/sys v0.44.0 already present [VERIFIED]
- `internal/collector/euc/euc.go`, `euc_noop.go`, `{linux,windows,darwin}.go`, `internal/agent/agent.go`, `test/llmsignal/{euc_local_inference_test.go,fakes.go}`, `README.md` — contract + seam + existing test pattern [VERIFIED: codebase]
- github.com/cilium/ebpf (README/pkg.go.dev) — pure-Go, no cgo, CO-RE, bpf2go precompiled objects [CITED]
- github.com/0xrawsec/golang-etw (README/pkg.go.dev) — pure-Go, no cgo, realtime session + kernel providers [CITED]
- developer.apple.com — NEDNSProxyProvider, `com.apple.developer.networking.networkextension` entitlement, Catalina+ signing/notarization [CITED]

### Secondary (MEDIUM confidence)
- bpfman Linux capabilities; kernel `unprivileged_bpf_disabled` (5.13) and CAP_BPF scope change (5.19) — privilege model for kprobe/perfmon programs
- smadi0x86 ETW+Go deep dive — golang-etw realtime session usage shape

### Tertiary (LOW confidence)
- Help Net Security / general macOS articles — `lsof`-equivalent no-root connection listing on macOS (supports the sampler approach; field-level behavior to confirm on a Mac)

## Metadata

**Confidence breakdown:**
- Standard stack (Linux/Windows libs + versions): HIGH — proxy-verified, pure-Go, fits no-cgo/no-clang constraint
- Build-tag isolation + cross-compile: HIGH — stubs already tagged; pattern is standard Go
- macOS approach: MEDIUM — sampler is clearly shippable and contract-compliant; "is sampler acceptable as the darwin v1.0?" is a product call flagged for checkpoint
- Pitfalls (kprobe stability, privilege, ETW session): HIGH on existence, MEDIUM on exact symbols/provider fields (confirm on target hardware in Wave 1)

**Research date:** 2026-06-12
**Valid until:** 2026-07-12 (stable libraries; ~30 days)
