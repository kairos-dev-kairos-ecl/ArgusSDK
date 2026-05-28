---
phase: 02
plan: "02-05"
subsystem: connector-layer
tags: [elastic, opensearch, connector, ocsf, ecs, tls, bulk-api]
dependency_graph:
  requires: [02-01, 02-02]
  provides: [elastic-connector]
  affects: []
tech_stack:
  added: []
  patterns: [stdlib-net-http, ndjson-bulk-api, httptest-injection, ocsf-to-ecs-mapping]
key_files:
  created:
    - internal/connector/elastic/connector.go
    - internal/connector/elastic/connector_test.go
  modified: []
decisions:
  - "NDJSON /_bulk over stdlib net/http — no go-elasticsearch SDK; avoids version pinning and works with both ES 8.x and OpenSearch 2.x"
  - "APIKey pre-computed at New() time via base64.StdEncoding to avoid per-request encoding overhead"
  - "ocsfToECS returns flat map[string]interface{} — enables direct JSON serialisation without nested struct allocation"
  - "severityIDToECSText maps OCSF severity_id 1-5 to ECS text labels; unknown IDs return 'unknown'"
  - "Health() returns nil for both green and yellow — yellow is a warning state, not an error"
metrics:
  duration: "~8 minutes"
  completed: "2026-05-28"
  tasks_completed: 1
  tasks_total: 1
  files_created: 2
  files_modified: 0
---

# Phase 02 Plan 05: Elastic/OpenSearch Connector Summary

**One-liner:** Elastic/OpenSearch connector using stdlib net/http /_bulk NDJSON with OCSF-to-ECS field mapping (7 fields) and API key auth enforcing TLS 1.3 via NewTLSConfig.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 RED | Elastic connector failing tests | 27d22b3 | internal/connector/elastic/connector_test.go |
| 1 GREEN | Elastic connector implementation | 2a5c827 | internal/connector/elastic/connector.go |

## What Was Built

**`internal/connector/elastic/connector.go`** — Full Elastic/OpenSearch connector:

- `New(cfg Config) *Connector` — constructor; sets defaults (Index="argus-signals", MaxBatchDocs=500); pre-computes `apiKeyHeader = "ApiKey " + base64(cfg.APIKey)`.
- `NewWithClient(cfg Config, client *http.Client) *Connector` — test-injection constructor.
- `Connect(ctx)` — validates Endpoint and APIKey, builds `*http.Client` via `connector.NewTLSConfig` (TLS 1.3 minimum, InsecureSkipVerify never set), issues `GET <Endpoint>/` and validates 200 response containing cluster info.
- `Send(ctx, batch)` — maps each signal via `ocsf.Mapper`, projects OCSF Event to ECS document, writes alternating action+document NDJSON lines to a `bytes.Buffer`, POSTs to `/_bulk`; returns `Status="failed"` if `errors:true` in response.
- `Health(ctx)` — issues `GET /_cluster/health`; returns nil for "green" or "yellow", error for "red".
- `Close()` — calls `c.client.CloseIdleConnections()`; no-op if client is nil.
- `ocsfToECS(ev)` — projects 8 ECS fields: `@timestamp`, `event.code`, `event.category`, `event.kind`, `event.severity`, `agent.name`, `event.module`, `event.uid`.
- `severityIDToECSText(id)` — OCSF severity_id 1=informational, 2=low, 3=medium, 4=high, 5=critical.

**`internal/connector/elastic/connector_test.go`** — 13 test functions:
- Unit tests using `httptest.NewServer` + `NewWithClient` injection
- `TestElasticConnector_Integration` — skips if `ELASTIC_ENDPOINT` not set
- `TestElasticConnector_ECSMapping` — verifies all 6 required ECS fields present in sent document

## Verification Results

```
go test ./internal/connector/elastic/... -v -count=1
--- PASS: TestElasticConnector_Name
--- PASS: TestElasticConnector_ConnectNoEndpoint
--- PASS: TestElasticConnector_ConnectNoAPIKey
--- PASS: TestElasticConnector_ConnectClusterInfo
--- PASS: TestElasticConnector_ConnectFails
--- PASS: TestElasticConnector_SendBulkFormat
--- PASS: TestElasticConnector_SendBulkError
--- PASS: TestElasticConnector_SendBulkSuccess
--- PASS: TestElasticConnector_Health
--- PASS: TestElasticConnector_HealthYellow
--- PASS: TestElasticConnector_HealthRed
--- SKIP: TestElasticConnector_Integration (ELASTIC_ENDPOINT not set)
--- PASS: TestElasticConnector_ECSMapping
PASS ok  github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector/elastic 0.323s

go build ./...  → OK
grep InsecureSkipVerify internal/connector/elastic/connector.go → comment only (line 6)
```

## Deviations from Plan

None — plan executed exactly as written.

## Known Stubs

None — all data flows are wired. The integration test requires `ELASTIC_ENDPOINT`, `ELASTIC_API_KEY`, and optionally `ELASTIC_INDEX` from the environment (as documented in user_setup).

## Threat Surface Scan

No new threat surface beyond what the plan's threat_model covers:
- T-02-12: APIKey is pre-computed and never logged — only endpoint and index are logged.
- T-02-13: TLS built via `connector.NewTLSConfig`; `InsecureSkipVerify` appears only in comment.
- T-02-14: `/_bulk` response parsed into fixed `bulkResponse` struct; `errors:true` → Status="failed".
- T-02-SC: No new external packages; stdlib net/http only.

## Self-Check

- [x] `internal/connector/elastic/connector.go` exists
- [x] `internal/connector/elastic/connector_test.go` exists (13 tests, 454+ lines)
- [x] Commit 27d22b3 (test RED) exists
- [x] Commit 2a5c827 (feat GREEN) exists
- [x] `go build ./...` passes
- [x] `go test ./internal/connector/elastic/...` — 12 PASS, 1 SKIP

## Self-Check: PASSED
