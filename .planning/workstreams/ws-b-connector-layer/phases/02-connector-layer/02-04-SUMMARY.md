---
phase: 02-connector-layer
plan: "04"
subsystem: connector
tags: [splunk, hec, net/http, ocsf, tls, connector]

# Dependency graph
requires:
  - phase: 02-01
    provides: ocsf.Mapper and Event types used in Send()
  - phase: 02-02
    provides: connector.NewTLSConfig enforcing TLS 1.3 for HTTP transport
  - phase: 02-03
    provides: Kafka connector patterns (NewWithClient test helper, mapper construction)
provides:
  - internal/connector/splunk/connector.go — full net/http HEC implementation
  - internal/connector/splunk/connector_test.go — httptest unit tests + gated integration test
affects: [argus-agent, dispatcher, output-pipeline, mode-2-ocsf]

# Tech tracking
tech-stack:
  added: []  # stdlib net/http only; no new external packages
  patterns:
    - "NewWithClient(cfg, *http.Client) test constructor injects httptest.Server client"
    - "Newline-delimited HEC JSON: each line is {event, time, index, sourcetype}"
    - "healthCheck() factored as private method shared by Connect() and Health()"
    - "InsecureSkipVerify retained in TLSConfig struct for compatibility but ignored at runtime"

key-files:
  created:
    - internal/connector/splunk/connector.go
    - internal/connector/splunk/connector_test.go
  modified: []

key-decisions:
  - "NewWithClient() test constructor exposes an *http.Client injection point; avoids TLS cert complexity in httptest.NewServer (non-TLS) tests while keeping the production path always using connector.NewTLSConfig"
  - "InsecureSkipVerify bool retained in TLSConfig struct for backward compatibility with scaffolded Config; field is ignored at runtime and a warning is logged if caller sets it — connector.NewTLSConfig never propagates InsecureSkipVerify to tls.Config"
  - "healthCheck() extracted as private method shared by both Connect() and Health() to avoid duplication"
  - "TDD order followed: RED commit (d7549ec) then GREEN commit (fcc42f0)"

patterns-established:
  - "HEC payload: each signal produces one newline-terminated JSON line: {event:<ocsf_json>, time:<unix_float>, index, sourcetype}"
  - "DeliveryAck.Status='failed' when HEC response code != 0; Error = 'splunk_hec: <text> (code:<n>)'"
  - "All HTTP requests carry Authorization: Splunk <Token>; token never logged"

requirements-completed:
  - SC-4
  - SC-7

# Metrics
duration: 25min
completed: 2026-05-28
---

# Phase 2 Plan 04: Splunk HEC Connector Summary

**Splunk HEC connector using stdlib net/http: OCSF-mapped signals POSTed as newline-delimited JSON with TLS 1.3 enforcement, token auth, health check gating, and httptest unit tests**

## Performance

- **Duration:** ~25 min
- **Started:** 2026-05-28T16:52:00Z
- **Completed:** 2026-05-28T17:17:53Z
- **Tasks:** 1 (TDD: RED + GREEN)
- **Files modified:** 2

## Accomplishments

- Full `net/http` Splunk HEC connector implementing the `connector.Connector` interface
- `Connect()` validates endpoint/token, builds TLS 1.3 HTTP client via `connector.NewTLSConfig`, and health-checks the HEC endpoint before storing the client
- `Send()` maps each signal through `ocsf.Mapper`, builds newline-delimited `{"event":<ocsf>,"time":<unix_float>,"index":"...","sourcetype":"..."}` payload, POSTs to `/services/collector/event` with `Authorization: Splunk <Token>`, and parses HEC response code to set `DeliveryAck.Status`
- `X-Splunk-Request-Channel` header added when `ChannelID` is non-empty
- `InsecureSkipVerify` never set on `tls.Config`; only logs a warning if caller sets the field on the struct
- 10 unit tests using `httptest.NewServer`; integration test gated on `SPLUNK_HEC_ENDPOINT`

## Task Commits

1. **RED: Splunk HEC tests** - `d7549ec` (test)
2. **GREEN: Splunk HEC implementation** - `fcc42f0` (feat)

**Plan metadata:** (this SUMMARY commit) (docs)

## Files Created/Modified

- `internal/connector/splunk/connector.go` — Full implementation: `New`, `NewWithClient`, `Connect`, `Send`, `Health`, `Close`, `healthCheck`
- `internal/connector/splunk/connector_test.go` — 10 unit tests with `httptest.NewServer`, 1 gated integration test

## Decisions Made

- `NewWithClient(cfg Config, client *http.Client)` test constructor allows injecting an `httptest.Server` client without `httptest.NewTLSServer` complexity. Production `New()` defers client creation to `Connect()` as specified.
- `healthCheck()` extracted as shared private method used by both `Connect()` and `Health()` — DRY without duplicating auth header logic.
- `InsecureSkipVerify bool` retained in `TLSConfig` struct (plan specifies "do not change fields") but is ignored at runtime; a warning is logged if it is set. `connector.NewTLSConfig` is the sole TLS config path.
- HEC `time` field uses Unix milliseconds from `signal.Signal.Timestamp` divided by 1000 to produce a float seconds value, matching Splunk HEC's expected format.

## Deviations from Plan

None — plan executed exactly as written.

The plan's `<action>` description specified retaining the existing `Config` and `TLSConfig` structs unchanged. `InsecureSkipVerify bool` is present in the struct but is ignored and documented as such — this matches the plan's guidance: "log a warning if InsecureSkipVerify is true in cfg but proceed safely."

## Issues Encountered

None.

## Threat Surface Scan

No new network endpoints, auth paths, file access patterns, or schema changes introduced beyond what the plan's threat model covers (T-02-09, T-02-10, T-02-11). The HEC token crosses one trust boundary (SDK → Splunk HEC over HTTPS) — token is in the `Authorization` header only; it is never logged.

## User Setup Required

Integration test requires:
- `SPLUNK_HEC_ENDPOINT` — full HEC URL, e.g. `https://splunk.company.com:8088`
- `SPLUNK_HEC_TOKEN` — Splunk Dashboard → Settings → Data Inputs → HTTP Event Collector → Token Value

Integration test is skipped automatically when these env vars are not set.

## Next Phase Readiness

- Splunk HEC connector is production-ready for Mode 2 (OCSF) signal delivery
- Can be registered with `ConnectorRegistry` and dispatched by the `Dispatcher`
- Elastic connector (02-05 if planned) can follow the same `NewWithClient` test pattern

---
*Phase: 02-connector-layer*
*Completed: 2026-05-28*
