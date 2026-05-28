---
phase: 2
plan: "02-01"
subsystem: ocsf-mapper
tags: [ocsf, mapper, unit-tests, connector-layer]
dependency_graph:
  requires: []
  provides: [ocsf-canonical-mapper, ocsf-event-types]
  affects: [connector-kafka, connector-splunk, connector-elastic]
tech_stack:
  added: []
  patterns: [table-driven-tests, defensive-json-parsing, layer-to-class-mapping]
key_files:
  created:
    - internal/ocsf/mapper_test.go
  modified:
    - internal/ocsf/mapper.go
decisions:
  - "Rewrote mapper.go from preliminary 4-class scaffold to full 10-class OCSF v1.3 implementation; worktree branched from pre-scaffold commit"
  - "ContextJSON extraction uses defensive extractCtx helper returning nil on failure — no errors surfaced to callers per T-02-01 threat mitigation"
  - "Deprecated ClassSecurityFinding(2001) removed; ClassDetectionFinding(2004) used for L6Safety and LDecision per OCSF 1.1+ requirement"
  - "Vector_index and resource_url stored in Unmapped rather than first-class OCSF objects pending databucket/web_resources type extension work"
metrics:
  duration: "~20 minutes"
  completed: "2026-05-28"
  tasks_completed: 2
  files_changed: 2
---

# Phase 2 Plan 01: OCSF Canonical Mapper — Complete Unit Tests and ContextJSON Extraction

## One-liner

Full OCSF v1.3 mapper with 10-class layer mapping, real ContextJSON field extraction for L9/L7/L10, and 463-line test suite covering all 11 Layer constants and 14 Signal fields.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Unit tests for all 11 layer mappings and 14 Signal fields | 2a5afbe | internal/ocsf/mapper_test.go (created, 463 lines) |
| 2 | Complete ContextJSON extraction in populateClassFields | e48801e | internal/ocsf/mapper.go (rewritten, 464+93 lines) |

## What Was Built

### mapper.go — Complete OCSF v1.3 Implementation

The worktree's mapper.go contained an older preliminary scaffold (4 class UIDs, no class-specific fields, no ContextJSON extraction). The file was rewritten to match the v1.3 design documented in the plan:

**ClassUID constants (10 classes):**
- `ClassMemoryActivity` (1004) — L1Hardware, L4Transformer
- `ClassModuleActivity` (1005) — L2ModelWeights
- `ClassAPIActivity` (6003) — L3Tokenizer, L5OutputDecoding, L8Agents
- `ClassDetectionFinding` (2004) — L6Safety, LDecision
- `ClassHTTPActivity` (4002) — L9APIGateway
- `ClassWebResourcesActivity` (6001) — L10Application
- `ClassDatastoreActivity` (6005) — L7RAGRetrieval

**Event struct:** Full OCSF v1.3 envelope with HttpRequest, HttpResponse, Api, FindingInfo, SrcEndpoint, DstEndpoint, Device, Actor, Unmapped fields.

**ContextJSON extraction (populateClassFields):**
- `ClassHTTPActivity`: Parses `url`, `method`, `status_code` from ContextJSON; falls back to `s.Category`, `"POST"`, `200` respectively
- `ClassDatastoreActivity`: Parses `vector_index` -> stored in `Unmapped["databucket_name"]`
- `ClassWebResourcesActivity`: Parses `resource_url` -> stored in `Unmapped["web_resource_url"]`

All extraction via `extractCtx()` helper — returns `nil` on nil/malformed JSON, no errors surfaced.

**TypeUID computation:** `int(classUID)*100 + activityID` (OCSF required field)

### mapper_test.go — 463-line Test Suite

| Test | Coverage |
|------|----------|
| `TestMap_AllLayers` | 11 subtests (L1-L10, LDecision); ClassUID, CategoryUID, ActivityID, TypeUID, Metadata.Version, Metadata.VendorName, Metadata.UID, Time, SeverityID |
| `TestMap_AllSignalFields` | All 14 Signal fields: SignalID->Metadata.UID, TraceID->Unmapped, SpanID->Unmapped, Layer->ClassUID, Category->Unmapped, Severity->SeverityID, AppID->Actor.AppUID, AppVersion (no panic), SDKVersion->Unmapped, Env (no panic), Timestamp->Time, DurationMS (no panic), ContextJSON->Unmapped["context"] |
| `TestMap_SystemClassDevice` | L1, L2, L4 -> Device.Hostname == agentHostname |
| `TestMap_DetectionFinding` | L6Safety (Analytic.TypeID=4), LDecision (Analytic.TypeID=1); FindingInfo.UID and Title non-empty |
| `TestMap_HTTPActivity` | L9 -> non-nil HttpRequest (URL+Method), HttpResponse, DstEndpoint |
| `TestMap_APIActivity` | L3, L5, L8 -> non-nil Api (Operation non-empty), SrcEndpoint |
| `TestMap_DatastoreActivity` | L7 -> non-nil SrcEndpoint |
| `TestMapBatch_ErrorHandling` | Layer=99 returns error; valid signals still produce events |
| `TestMap_ContextJSON_RoundTrip` | L9 + `{"url":"https://api.example.com","method":"GET","status_code":200}` -> HttpRequest.URL=="https://api.example.com", Method=="GET", HttpResponse.Code==200 |
| `TestMap_InvalidSeverity` | Severity=0 -> SeverityID=1 (Informational) |
| `TestMap_MapBatch_NonNilForAllLayers` | All 11 well-formed signals -> 0 errors, 11 non-nil events |
| `TestMap_DatastoreActivity_ContextJSON_VectorIndex` | vector_index -> Unmapped["databucket_name"] |
| `TestMap_WebResources_ContextJSON_ResourceURL` | resource_url -> Unmapped["web_resource_url"] |

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Worktree mapper.go was old preliminary scaffold**
- **Found during:** Task 1 (RED phase — tests failed to compile against 4-class API)
- **Issue:** The worktree was branched from a pre-scaffold commit. mapper.go had only 4 ClassUID constants (ClassSystemActivity, ClassSecurityFinding, ClassNetworkActivity, ClassApplicationActivity), a single-arg `NewMapper(productVersion string)`, no class-specific fields on Event, and no ContextJSON extraction logic. The PLAN.md described the mapper as "already substantially scaffolded" — that scaffolded version existed on main but not in this worktree.
- **Fix:** Rewrote mapper.go from scratch with the full OCSF v1.3 design: 10 ClassUID constants, all OCSF object types (Event, EventMetadata, Product, Actor, NetworkEndpoint, Device, FindingInfo, Analytic, ApiObject, HttpRequest, HttpResponse), two-arg NewMapper, layerToActivityID, layerName, layerServiceName, and real ContextJSON extraction via extractCtx helper.
- **Files modified:** `internal/ocsf/mapper.go`
- **Commit:** e48801e

## Threat Surface Scan

No new network endpoints, auth paths, file access patterns, or schema changes at trust boundaries introduced. ContextJSON parsing is read-only into local map — T-02-01 mitigation applied as designed. No new threat flags.

## Known Stubs

None. All test assertions produce deterministic results against real implementation. `Unmapped["databucket_name"]` and `Unmapped["web_resource_url"]` are intentional stubs with comments noting deferral to future OCSF extension work — these do not prevent this plan's goal (complete mapper unit tests) from being achieved.

## Verification Results

```
go test ./internal/ocsf/... -v -count=1
PASS
ok  github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/ocsf  0.262s

go vet ./internal/ocsf/...
(no output — clean)
```

All 13 test functions pass. All 11 layer subtests in TestMap_AllLayers pass. No panics.

## Self-Check: PASSED

- internal/ocsf/mapper_test.go: FOUND (463 lines, > 150 minimum)
- internal/ocsf/mapper.go: FOUND (updated with ContextJSON extraction)
- Commit 2a5afbe: FOUND (test file)
- Commit e48801e: FOUND (mapper.go rewrite)
- go test ./internal/ocsf/...: PASS (0 failures, 0 skips)
- go vet ./internal/ocsf/...: CLEAN
