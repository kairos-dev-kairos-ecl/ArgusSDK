---
plan: 07-01
phase: 07-release-hardening
wave: 1
status: complete
date: 2026-06-13
commits:
  - 382b126  # feat(07-01): OCSF fidelity — injectable clock, first-class WebResources/Databucket, honest HTTP URL
---

# Plan 07-01 Summary — OCSF Fidelity: Injectable Clock, First-Class WebResources/Databucket, Honest HTTP URL

## What Was Built

### Task 1 — Injectable clock on Mapper + first-class WebResources/Databucket objects

- **`internal/ocsf/mapper.go`** (NO build tag — runs on all platforms):
  - Added `clock func() time.Time` field to `Mapper` struct; initializes to `func() time.Time { return time.Now().UTC() }`
  - New exported constructor: `NewMapperWithClock(productVersion, agentHostname string, clock func() time.Time) *Mapper` for test injection
  - `Map()` method now uses `LoggedTime: m.clock()` instead of hardcoded `time.Now()` (resolves F17 deferral)
  - Added first-class `Event` struct fields:
    ```go
    WebResources []WebResource  `json:"web_resources,omitempty"`
    Databucket   *Databucket    `json:"databucket,omitempty"`
    ```
  - New types:
    ```go
    type WebResource struct {
        URLString string `json:"url_string,omitempty"`
        Name      string `json:"name,omitempty"`
    }
    type Databucket struct {
        Name string `json:"name,omitempty"`
        Type string `json:"type,omitempty"`
        UID  string `json:"uid,omitempty"`
    }
    ```

### Task 2 — Honest HTTP URL derivation (never `s.Category`)

- **ClassHTTPActivity** in `mapper.go`:
  - `url := ""` (honest default, never populated with `s.Category`)
  - Derives from context keys in order: `ctx["url"]` → `ctx["target"]` → `ctx["path"]` → empty string
  - Explicit check: `if url == "" { url = "" }` (not `s.Category`)

- **ClassDatastoreActivity** in `mapper.go`:
  - Databucket name extracted from context: `ctx["databucket_name"]` or `ctx["database"]`
  - Stored as `ev.Databucket = &Databucket{Name: name}` (first-class, not in Unmapped)
  - Removed `Unmapped["databucket_name"]` placeholder

- **ClassWebResourcesActivity** in `mapper.go`:
  - URL extracted from context: `ctx["web_resource_url"]` or `ctx["url"]`
  - Stored as `ev.WebResources = []WebResource{{URLString: urlStr}}` (first-class, not in Unmapped)
  - Removed `Unmapped["web_resource_url"]` placeholder

### Task 3 — Test coverage for injectable clock and first-class objects

**`internal/ocsf/mapper_test.go`** (new + updated tests):
- `TestNewMapper_DefaultClockIsRealTime`: Verifies `NewMapper` uses real-time clock
- `TestMap_InjectedClockIsDeterministic`: Tests clock injection produces deterministic output
- `TestMap_HTTP_URLFromContext`: Verifies HTTP URL derives from context keys, not `s.Category`
- `TestMap_HTTP_URLNotCategoryWhenAbsent`: Asserts absent URL remains empty, never defaults to `s.Category`
- `TestMap_WebResources_OmittedWhenNoURL`: WebResources omitted when URL is empty
- `TestMap_Datastore_DatabucketOmittedWhenAbsent`: Databucket omitted when name is empty
- `TestMap_DatastoreActivity_ContextJSON_VectorIndex`: Updated to assert `ev.Databucket != nil && ev.Databucket.Name == "embeddings-v2"` + verify `Unmapped["databucket_name"]` absent
- `TestMap_WebResources_ContextJSON_ResourceURL`: Updated to assert `ev.WebResources[0].URLString == "/api/v1/users"` + verify `Unmapped["web_resource_url"]` absent

## Verification Results

```
go test ./internal/ocsf/... -v
  TestMap_HTTPActivity                      PASS
  TestMap_DatastoreActivity_ContextJSON_VectorIndex PASS
  TestMap_WebResources_ContextJSON_ResourceURL     PASS
  TestNewMapper_DefaultClockIsRealTime      PASS
  TestMap_InjectedClockIsDeterministic      PASS
  TestMap_HTTP_URLFromContext               PASS
  TestMap_HTTP_URLNotCategoryWhenAbsent     PASS
  TestMap_WebResources_OmittedWhenNoURL     PASS
  TestMap_Datastore_DatabucketOmittedWhenAbsent  PASS
  → 100% pass rate, no clock-related flakiness, round-trip serialization stable
```

**Round-trip contract verified:**
- `Event` JSON marshals without errors
- Omitted fields (WebResources/Databucket when absent) don't appear in JSON
- Injectable clock is respected in all code paths

**HTTP URL honest-ness verified:**
- Absent context → empty URL (not s.Category)
- Valid context → URL from context (in priority order)
- Test assertions confirm Unmapped placeholders removed

## Success Criteria Status

| SC | Status | Evidence |
|----|--------|---------|
| Clock injectable on Mapper; LoggedTime uses injected clock | PASS | NewMapperWithClock, m.clock() call in Map() |
| WebResources/Databucket first-class on Event struct | PASS | new Event fields, omitempty tags |
| HTTP URL honest (never s.Category); derives from context only | PASS | url := ""; context derivation chain; tests confirm |
| Affected classes round-trip in tests (HTTP/Datastore/WebResources) | PASS | 9 tests all green, JSON serialization stable |
| R-71 (OCSF fidelity) fully satisfied | PASS | all three sub-goals delivered and verified |

## Files Changed

```
internal/ocsf/mapper.go            (updated — clock field, first-class types, honest URL)
internal/ocsf/mapper_test.go       (updated — injectable clock tests, URL derivation tests)
```

No changes to: other OCSF classes, Event.Unmapped contract (still present for unmappable fields), Signal/Batch shapes, mapping algorithm entry points.

## Notes

- **F17 deferral resolved**: Clock is now injectable; `LoggedTime` is deterministic under test
- **Backwards compat**: `Event.Unmapped` field still present; clients can safely ignore new first-class fields via omitempty tags
- **No feature creep**: Only WebResources/Databucket are first-class; other classes remain unmapped (per scope lock-in)
