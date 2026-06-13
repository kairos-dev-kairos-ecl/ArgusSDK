---
plan: 07-03
phase: 07-release-hardening
wave: 2
status: complete
date: 2026-06-13
commit: (pending)
---

# Plan 07-03 — Config/Docs/Makefile/Milestone Summary

## Objective

Consolidate documentation and operability artifacts for v1.0 release: sample config, README sync, Makefile fixes, milestone-level tracking.

## Tasks Completed

### Task 1: Sample Config Consolidation ✅

**File:** `config/agent.example.yaml`

**Changes:**
- **Added observability section** with health_check_port and metrics_port configuration
- Config now includes all v1.0 sections:
  - agent (group_id, instance_name, mode, xdr_endpoint)
  - auth (install_token, instance_id, credential)
  - ingest (grpc + unix listeners)
  - buffer (dir, max_size_mb, flush_interval, drain_on_reconnect)
  - outputs (argusxdr, splunk_hec, kafka, syslog examples)
  - tls (min_version = 1.3)
  - logging (level, format)
  - observability (health_check_port, metrics_port) — NEW
- Canonical single file per spec; no duplicates
- Realistic defaults for XDR remote mode with EUC enabled
- Comments explain each field and env var override syntax

**Validation:** YAML parses correctly; all sections documented.

---

### Task 2: README Sync to Reality ✅

**File:** `README.md`

**Changes:**

#### Tech-stack Update
- **Removed:** `github.com/oklog/ulid/v2` (not used; Phase 4 abandoned), `github.com/prometheus/client_golang` (observability deferred)
- **Added:** 
  - `github.com/cilium/ebpf` (Linux EUC collector)
  - `github.com/0xrawsec/golang-etw` (Windows EUC collector)
  - `github.com/shirou/gopsutil/v4` (macOS EUC collector)
  - `github.com/twmb/franz-go` (Kafka connector)
  - `github.com/testcontainers/testcontainers-go` (integration tests)

#### Directory Structure Update
- Added `/internal/connector/factory` (Connector factory, instantiation & registry)
- Added `/internal/observability` (Health checks, metrics, tracing — v1.0)
- Added `/proto/` (Protocol buffer definitions)
- Added `Dockerfile` (Distroless/static container image)
- Documented all key directories

#### Quick-start Validation
- Corrected `go install` path: `./cmd/argus-agent` (was `./cmd/argus`)
- Verified command sequence: `make build` → copy config → `make install` → run
- Added note about first-run XDR registration

#### New Sections

**Observability:**
- Documented liveness probe (`GET /_health`) at `observability.health_check_port` (default :9090)
- Documented readiness probe (`GET /_ready`) at same port
- Documented metrics endpoint (`GET /metrics`) at `observability.metrics_port` (default :9091)
- Explained use cases (Kubernetes probes, fleet monitoring)
- Noted how to disable (leave port empty)

**Deployment:**
- XDR registration flow (5 steps: Admin → Group ID + Token → Agent → Registration → Instance ID)
- Credential refresh (automatic rotation, new creds on each submission)
- Hot-reload via SIGHUP (config reload without restart, example command)
- Expected behavior (brief listener interruption, buffer preserves signals)

---

### Task 3: Makefile Fixes ✅

**File:** `Makefile`

**Changes:**
- **docker target:** Changed from TODO to actual implementation: `docker build -t argus-agent:latest -f Dockerfile .`
- **install target:** Fixed path: `go install ./cmd/argus-agent` (was `./cmd/argus`)

**Validation:** Both targets now point to correct paths and build artifacts.

---

### Task 4: Milestone-level Record ✅

**File:** `.planning/WORKSTREAMS.md` (created)

**Content:**
- **Ties all three workstreams** (WS-A, WS-B, WS-C) to v1.0 release milestone
- **WS-A Proto Foundation:** Proto schema, codegen pipeline, signal types (✅ COMPLETE)
- **WS-B Connector Layer:** Connectors, OCSF mapper, buffer, agents, collectors (✅ COMPLETE, 16/16 plans)
- **WS-C Release Hardening:** Registration, EUC OS collectors, OCSF/hot-reload/docs (🟡 IN PROGRESS, 9/11 plans)
- **v1.0 Release Scope:** What ships, what defers to v2.0+
- **Dependencies:** Shows proto→connectors→release progression
- **Verification checklist:** 16-point end-to-end readiness audit
- **Quick links:** Direct references to workstream directories

---

## Files Modified

| File | Change | Status |
|------|--------|--------|
| `config/agent.example.yaml` | Added observability section (health_check_port, metrics_port) | ✅ |
| `README.md` | Tech-stack cleanup, directory docs, Observability section, Deployment section, quick-start fix | ✅ |
| `Makefile` | docker target (→ Dockerfile), install target (→ ./cmd/argus-agent) | ✅ |
| `.planning/WORKSTREAMS.md` | Created — v1.0 milestone record linking WS-A/B/C | ✅ |

---

## Validation Results

- ✅ Config file syntax valid (YAML)
- ✅ README quick-start commands point to correct paths
- ✅ `make install` now installs to GOPATH/bin correctly
- ✅ `make docker` builds container image
- ✅ All sections of agent.example.yaml documented
- ✅ Tech-stack matches go.mod (no unused deps listed)
- ✅ Directory structure reflects actual repo layout
- ✅ Observability, Deployment sections complete
- ✅ WORKSTREAMS.md links are internal relative paths
- ✅ Verification checklist is actionable

---

## Notes

- Wave 2 is non-critical path; 07-04 can execute in parallel or after
- No code changes; pure documentation + config consolidation
- All code changes from 07-01/07-02 already merged to main
- This plan prepares repo for clean v1.0 release artifact
- Next: 07-04 (CI completeness) + Phase 7 verification

---

**Completed:** 2026-06-13  
**Wave:** 2 (non-critical)  
**Effort:** 4 tasks, ~30 min  
**Impact:** Release-ready documentation + configuration
