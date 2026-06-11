---
phase: 04-agent-wiring
plan: "02"
subsystem: syslog-connector
tags: [syslog, cef, tls, tcp, connector]
dependency_graph:
  requires: [internal/connector/tls.go, pkg/signal/signal.go, internal/connector/connector.go]
  provides: [internal/connector/syslog/connector.go]
  affects: [connector factory (04-01), agent wiring (04-05)]
tech_stack:
  added: []
  patterns: [CEF-over-TCP, TLS-1.3-via-NewTLSConfig, abort-on-first-failure]
key_files:
  created: [internal/connector/syslog/connector_test.go]
  modified: [internal/connector/syslog/connector.go]
decisions:
  - ServerName derived from server address host portion to enable TLS verification without InsecureSkipVerify
  - buildCEF sanitises pipe and newline chars in field values (T-04-05 injection mitigation)
  - Send aborts on first write error (sequential abort-on-first-failure, locked decision 9)
  - writeFile helper lives in syslog package (test-only, unexported to production)
metrics:
  duration_seconds: 220
  completed_date: "2026-06-11"
  tasks_completed: 2
  files_modified: 2
---

# Phase 04 Plan 02: Syslog CEF/TCP/TLS Connector Summary

**One-liner:** CEF over TCP (TLS 1.3 via connector.NewTLSConfig) with per-signal formatting, pipe/newline injection guard, and abort-on-first-failure delivery contract.

## Tasks Completed

| Task | Name | Commit | Status |
|------|------|--------|--------|
| 1 (RED) | Failing tests for Connect/buildCEF | b40ffb3 | done |
| 1+2 (GREEN) | Implement Connect, buildCEF, Send | e3d33fd | done |

## What Was Built

### `internal/connector/syslog/connector.go`

**Connect** — switch on Transport:
- `TransportTCP`: `net.DialTimeout("tcp", server, 10s)` — stores `net.Conn`
- `TransportTLS`: calls `connector.NewTLSConfig` (TLS 1.3, no `tls.Config{}` literal), derives `ServerName` from host part of server address, dials with `net.Dialer` + `tls.Client` + explicit `Handshake()` — stores `*tls.Conn`
- `TransportUDP`: `net.Dial("udp", server)` fallback

**buildCEF** — formats a `signal.Signal` as a single CEF line (no trailing newline):
```
CEF:0|Argus|SDK|1.0|<SignalID>|<Category>|<cefSeverity>|layer=<int> app_id=<AppID> trace_id=<TraceID>
```
Severity mapping: Info→1, Low→3, Medium→5, High→7, Critical→9, Unspecified→0

**Send** — delivery contract (locked decision 9):
1. `c.conn == nil` → `DeliveryAck{Status:"failed"}` + non-nil error (no silent drop)
2. Per-signal: `buildCEF(s, appName) + "\n"` → `fmt.Fprint(c.conn, ...)`; first write error → failed ack + error with signal index
3. Full success → `DeliveryAck{Status:"delivered", BatchID: batch.BatchID, Timestamp: time.Now()}`

**CEF injection mitigation (T-04-05):**
- `sanitizeCEFField`: replaces `|` with `/`, strips `\r\n`, `\r`, `\n`
- `sanitizeCEFExtValue`: strips newlines, replaces `=` with `_`

### `internal/connector/syslog/connector_test.go`

10 tests against local in-process listeners (no Docker):
- `TestBuildCEF_Format` — prefix, SignalID, category, extensions
- `TestBuildCEF_SeverityMapping` — all 6 severity levels
- `TestBuildCEF_NoPipeInjection` — exactly 7 pipes in CEF header
- `TestConnect_EmptyServer` — error on empty address
- `TestConnect_TCP` — connects to `net.Listen("tcp")` listener
- `TestConnect_TLS` — connects to `tls.Listen` with self-signed cert; NewTLSConfig path
- `TestSend_DeliversCEFLines` — 3-signal batch → 3 CEF lines, status=delivered
- `TestSend_NilConnReturnsFailedAck` — nil conn → failed ack + error
- `TestSend_WriteErrorReturnsFailedAck` — closed conn → failed ack + error
- `TestSend_EmptyBatch` — 0 signals → delivered, 0 lines written

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] TLS ServerName not set — handshake failure**
- **Found during:** GREEN phase, `TestConnect_TLS`
- **Issue:** `NewTLSConfig` returns `*tls.Config` without `ServerName`. When dialing by IP (`127.0.0.1`), Go's TLS client requires `ServerName` to be set (or `InsecureSkipVerify`, which is prohibited by T-04-04 and locked decision 4).
- **Fix:** After calling `connector.NewTLSConfig`, derive `ServerName` from the host portion of `c.cfg.Server` via `net.SplitHostPort`. Set only if `tlsCfg.ServerName == ""` so a caller-provided value takes precedence.
- **Files modified:** `internal/connector/syslog/connector.go`
- **Commit:** e3d33fd

## Known Stubs

None — the scaffold `Send()` that returned a fake "delivered" ack has been fully replaced.

## Threat Surface

T-04-04 (information disclosure / TLS): mitigated — TLS 1.3 via `connector.NewTLSConfig`; no `tls.Config{}` literal; no `InsecureSkipVerify`.

T-04-05 (CEF injection): mitigated — `sanitizeCEFField` and `sanitizeCEFExtValue` strip pipe and newline characters from all signal field values before embedding in the CEF line.

T-04-06 (repudiation / silent drop): mitigated — nil conn and write errors both return non-nil error + `DeliveryAck{Status:"failed"}`.

## Self-Check: PASSED

- `internal/connector/syslog/connector.go` — exists, `go build ./...` passes
- `internal/connector/syslog/connector_test.go` — exists, 10/10 tests pass
- Commit b40ffb3 (RED) — `git log --oneline | grep b40ffb3` → confirmed
- Commit e3d33fd (GREEN) — `git log --oneline | grep e3d33fd` → confirmed
- `go vet ./...` — passes
