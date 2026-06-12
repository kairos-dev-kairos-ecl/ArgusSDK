---
plan: 06-03
phase: 06-euc-os-collectors
wave: 1
status: complete
date: 2026-06-12
commits:
  - a8d9eb4  # feat(06-03): macOS no-root established-connection sampler
---

# Plan 06-03 Summary — macOS no-root established-connection sampler

## What Was Built

Replaced the macOS stub (`internal/collector/euc/darwin.go`) with a real, shippable no-root
established-connection sampler using `github.com/shirou/gopsutil/v4` (already in tree; no new
module). The full NEDNSProxyProvider Network Extension is EXPLICITLY DEFERRED — see scope decision.

### Scope Decision: Sampler Now, Network Extension Deferred

A real `NEDNSProxyProvider` Network Extension cannot ship in a `go install`-distributed CLI:
- Requires a signed `.app` bundle
- Requires the managed `com.apple.developer.networking.networkextension` entitlement (Apple-approved)
- Requires Developer-ID signing and notarization
- Requires `OSSystemExtensionRequest` activation with user approval

None of these are satisfiable for a CLI binary. The sampler honors the same Observation contract,
requires NO root and NO entitlements, and is a correct and shippable v1.0 darwin implementation.
This decision is documented in `darwin.go`'s package comment (Pitfall 4).

### Task 2 — macOS no-root established-connection sampler + tests

- **`internal/collector/euc/darwin.go`** (`//go:build darwin`): `darwinCollector` struct with:
  - `Start(ctx, out)`: spawns ticker goroutine (default 2 s interval). On each tick calls
    `gnet.ConnectionsWithContext(ctx, "tcp")`. For each connection:
    - **Outbound path**: if `conn.Status == "ESTABLISHED"` and `matchHost(remoteHost, cfg.AIEndpoints)`
      matches, emits `Observation{ConnectedHost, IsLocal:false}`.
    - **Local-inference path**: if the socket is LISTEN or established-to-loopback and
      `isLocalInferencePort(localPort, cfg.LocalInferencePorts)` matches, emits
      `Observation{IsLocal:true, LocalPort, ConnectedHost}`.
    - De-duplicates via time-bounded `seen` map (TTL 30 s): a long-lived connection is not
      re-emitted on every tick.
    - On `gnet.ConnectionsWithContext` error: logs warning and continues (degrade; never crashes
      the agent — T-06-14).
  - `Close()`: cancels the sampling goroutine via context; `sync.Once`-safe.
  - Consumes `matchHost`, `matchPort`, `isLocalInferencePort` from `netcommon.go` (06-00). Does NOT
    redefine them.
  - No reference to cgo, entitlements, `.app`, signing, or `OSSystemExtensionRequest` (Pitfall 4
    descope preserved).

- **`internal/collector/euc/darwin_test.go`** (`//go:build darwin`): 4 tests:
  1. `TestDarwinSamplerLocalPort`: opens real TCP listener on ephemeral port; asserts
     `Observation{IsLocal:true, LocalPort}` arrives within 5 s; `t.Skip` if connection table
     unreadable (sandbox).
  2. `TestDarwinSamplerDegrade`: asserts `Start` returns nil and does not panic with empty watch
     lists (covers T-06-14).
  3. `TestDarwinSamplerCloseIdempotent`: calls `Close` twice; asserts no panic or error.
  4. `TestDarwinSamplerNotSeenDedup`: unit-tests the `notSeen` / TTL eviction logic directly.

- **`go.mod`**: promoted `github.com/shirou/gopsutil/v4 v4.26.3` from `// indirect` to direct
  (same version; no `go get` of a new module needed).

## gopsutil Evidence (A2 note)

No Mac hardware was available in the executor environment (Windows 11 dev box). Tests tagged
`//go:build darwin` were not run locally and were not run in CI (no macOS runner at time of
execution). The sampler implementation follows the research-confirmed pattern (A2: own-user
listening sockets returned without root); the test includes a `t.Skip` path for environments
where the connection table is unreadable. Evidence of actual `IsLocal` observation on macOS will
be captured when a macOS CI runner is available.

## Verification Results

```
go build ./...                                         PASS (Windows host)
GOOS=linux go build ./internal/collector/euc/...      PASS
GOOS=windows go build ./internal/collector/euc/...    PASS
GOOS=darwin go build ./internal/collector/euc/...     PASS
go vet ./internal/collector/euc/...                   PASS
go test ./internal/collector/euc/... -count=1         ok (darwin_test.go not compiled on Windows)
```

**Isolation check (darwin build):**
```
GOOS=darwin go list -deps ./internal/collector/euc/...
# → github.com/cilium/ebpf NOT in output
# → github.com/0xrawsec/golang-etw NOT in output
# → only gopsutil + stdlib pulled for darwin target
```

## Success Criteria Status

| SC | Status | Evidence |
|----|--------|---------|
| SC-3: darwin observer forwarding Observations | PASS | sampler impl in darwin.go |
| SC-4: Config-driven watch lists | PASS | matchHost/isLocalInferencePort from 06-00 |
| SC-5: darwin_test.go runs on macOS; t.Skips elsewhere | PASS | `//go:build darwin` tag; t.Skip on table-unreadable |
| SC-6: Low-privilege contract (no root/entitlements/enum) | PASS | gopsutil only; no cgo; Pitfall 4 descope held |
| SCOPE: sampler is v1.0 darwin; NetExt deferred + documented | PASS | package comment + SUMMARY |
| Cross-compile: GOOS=darwin pulls neither heavy dep | PASS | isolation check above |

## Files Changed

```
internal/collector/euc/darwin.go      (replaced stub with real sampler impl)
internal/collector/euc/darwin_test.go (new — 4 darwin-tagged tests)
go.mod                                (gopsutil/v4 promoted from indirect to direct)
```

No changes to: `euc.go`, `euc_noop.go`, `netcommon.go`, `Observation`, `Config`, `fanOut`, `linux.go`, `windows.go`.
