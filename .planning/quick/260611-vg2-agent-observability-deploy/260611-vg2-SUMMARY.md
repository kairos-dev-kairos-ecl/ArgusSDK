---
quick_id: 260611-vg2
slug: agent-observability-deploy
status: complete
date: 2026-06-12
commits:
  - 72138fd  # observability HTTP server
  - 92d8c41  # deployment artifacts
---

# Quick Task 260611-vg2 — Operability quick-wins

## What was delivered

### Task 1 — Observability HTTP server (commit 72138fd)
- New `internal/agent/observability.go`:
  - `ObservabilityConfig{Disabled bool; Addr string}` (mapstructure `observability`); default addr `127.0.0.1:9090`.
  - `obsServer` with an atomic readiness flag and a pull-based stats function.
  - `/healthz` — liveness, 200 once serving.
  - `/readyz` — readiness, 503 until startup completes, 200 after, 503 again immediately on shutdown.
  - `/metrics` — dispatcher `accepted/delivered/failed/dropped` counters in Prometheus text exposition format, hand-rolled (no new dependency). Output is deterministic (sorted keys).
- Wired into `internal/agent/agent.go`: `Observability` field on `Config`, `obs` field on `Agent`, server started in `start()` (ready=false → ready=true after collectors), shut down first in `stop()`.
- `internal/agent/observability_test.go`: 8 unit tests covering metrics encoding (format, nil, fallback help, sort order) and health/ready/metrics handlers via `httptest`. All Docker-free.

### Task 2 — Deployment artifacts (commit 92d8c41)
- `Dockerfile`: multi-stage, CGO-disabled static build pinned to go 1.26.1, distroless/static nonroot runtime, entrypoint reads `/etc/argus-agent/agent.yaml`.
- `deploy/agent.yaml`: documented sample config (agent/auth/ingest/buffer/tls/outputs/observability/logging) with argusxdr + kafka output examples and ARGUS_SDK_* override notes.
- `deploy/kubernetes/deployment.yaml`: ConfigMap + Deployment with `livenessProbe`→`/healthz` and `readinessProbe`→`/readyz` on the observability port; secrets injected via `ARGUS_SDK_*` env from a Secret.

## Verification
- `go build ./...` — clean.
- `go test ./... -count=1` — all 15 packages pass (`internal/agent` includes the 8 new tests).
- `go vet ./internal/agent/...` — clean.

## Notes / deviations
- The code default bind is loopback (`127.0.0.1:9090`) for bare-metal safety. The container/k8s artifacts override to `0.0.0.0:9090` so the kubelet can reach the probes at the pod IP — documented inline in both the sample config and the manifest.
- Server is on by default (zero value enabled); set `observability.disabled: true` to turn it off.
- Scope intentionally excluded the two production blockers (real remote registrar/credential rotation; real EUC OS collector) — those are full phases, not quick-task sized.
