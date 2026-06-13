# ArgusSDK — Signal Collection Agent

ArgusSDK is a standalone, lightweight, config-driven signal collection and forwarding agent written in Go. Its mental model is NXLog: it collects signals from configured sources, normalises them internally, and routes them to configured output destinations.

**ArgusSDK is the agent. ArgusXDR is one possible destination among many.**

The Python and TypeScript instrumentation libraries (in the ArgusXDR repo under `sdk/`) are instrumentation libraries only — they embed in LLM applications and emit signals to the local ArgusSDK agent. The agent owns all transport, batching, buffering, and routing.

---

## Two Output Modes

### Mode 1 — ArgusXDR output (native proto)

When the destination is an ArgusXDR instance, the agent emits signals in the existing `ArgusSignal` protobuf format. No OCSF translation. XDR's internal schema, pipeline, ClickHouse storage, detection engine, and backpressure logic are unchanged. This is the mode for users who run the full Argus platform.

### Mode 2 — External/SIEM output (OCSF)

When the destination is an external platform (Kafka, Splunk, Sentinel, Chronicle, Elastic, etc.), the agent translates signals to [OCSF v1.3](https://schema.ocsf.io/) before delivery. OCSF mapping lives entirely in the agent (`internal/ocsf/`). XDR never sees OCSF and does not need to change.

OCSF is a switch, not a rewrite. The `ArgusSignal` proto stays. Set `ocsf: true` on an output connector to enable translation for that destination.

---

## Authentication Model

The SDK authentication model follows the EDR agent pattern (CrowdStrike / SentinelOne):

1. **Admin creates an Agent Group** in the ArgusXDR portal  
   → receives: **Group ID** + **Installation Token** (one-time use, time-limited)

2. **Agent starts on an endpoint or server**  
   → presents: Group ID + Installation Token to the XDR registration endpoint  
   → XDR verifies, registers the instance, returns: **SDK Instance ID** (server-assigned UUID)  
   → Installation Token is consumed and invalidated after use

3. **All subsequent signal submission**  
   → Agent presents: Group ID + Instance ID + API credential  
   → XDR stamps the server-verified Instance ID on every ingested signal batch  
   → `instance_id` is never self-reported by the SDK

Every signal is traceable to a server-verified SDK instance, not to whatever the agent self-reports.

---

## Local vs Remote Mode

| | Local | Remote |
|---|---|---|
| Transport | Loopback TCP or Unix socket | TLS 1.3 (mandatory, no downgrade) |
| Auth | Simplified (file permissions on Unix socket) | Full three-part auth |
| Use case | Developer, single-machine deployments | Fleet deployments, cloud XDR |

### Local signal buffering

When output connectors are unreachable, signals queue locally in the agent using a flat-file write-ahead log (WAL). When connectivity restores, the buffer drains in order with exponential backoff and jitter on reconnect to prevent fleet-wide thundering-herd against the ingest endpoint.

---

## EUC (End User Computing) — Shadow AI Observability

On enterprise endpoints, the agent's **sole purpose** is detecting which AI services are being accessed. It does not duplicate existing EDR telemetry (no general process enumeration, file access monitoring, or memory inspection).

**What the EUC collector watches:**
- DNS queries and TCP connections to known AI service endpoints (config-driven, hot-reloadable list)
- Local AI runtime detection on known inference ports (Ollama :11434, LM Studio :1234, etc.)
- Corporate proxy/DNS telemetry where available

**What it explicitly does not do:**
- General process enumeration or monitoring
- File system access monitoring  
- Full network flow capture
- Content inspection of AI API payloads

OS-specific implementations:
- `linux.go` — eBPF kprobe observer on `tcp_v4/v6_connect` + gopsutil local-port sampler (degrades gracefully without `CAP_BPF`)
- `windows.go` — ETW Kernel-Network provider
- `darwin.go` — no-root established-connection sampler via gopsutil (a full `NEDNSProxyProvider` Network Extension is deferred; it requires a signed, notarized `.app` with a managed entitlement, which is not distributable as a `go install` CLI)

---

## Installation

The agent ships as a native, installable service for each platform (like NXLog
or rsyslog) plus a container image. Download the artifact for your OS from the
[latest release](https://github.com/kairos-dev-kairos-ecl/ArgusSDK/releases),
then configure `/etc/argus-agent/agent.yaml` before starting.

### Linux (.deb / .rpm)

```bash
# Debian/Ubuntu
sudo dpkg -i argus-agent_<version>_linux_amd64.deb
# RHEL/Fedora/SUSE
sudo rpm -i argus-agent-<version>.x86_64.rpm

# The package installs a systemd unit and a default config. Edit it, then:
sudo nano /etc/argus-agent/agent.yaml
sudo systemctl enable --now argus-agent
sudo systemctl status argus-agent          # check it's running
sudo systemctl reload argus-agent          # apply EUC/log-level changes (SIGHUP)
```

### Windows (MSI installer)

```powershell
# Interactive: double-click the .msi, or run
msiexec /i argus-agent_<version>_windows_amd64.msi

# Silent, for MDM / Intune / SCCM:
msiexec /i argus-agent_<version>_windows_amd64.msi /quiet
```

The installer registers and starts the **argus-agent** Windows service (auto-start
at boot) and writes a default config to `C:\ProgramData\argus-agent\agent.yaml`.
The default runs in **local mode** so the service starts immediately with no
credentials; push your managed config (or switch to `mode: remote`) via MDM, then
restart the service to apply:

```powershell
Restart-Service argus-agent
```

Uninstall removes the service (`msiexec /x …` or Add/Remove Programs). The binary
also exposes `argus-agent service install|start|stop|uninstall` for manual setups.

### macOS (launchd)

```bash
# Extract the darwin archive, then:
sudo ./packaging/macos/install.sh
sudo nano /etc/argus-agent/agent.yaml
sudo launchctl load -w /Library/LaunchDaemons/org.kairos-foundation.argus-agent.plist
```

### Docker / Kubernetes

```bash
docker run -v /etc/argus-agent:/etc/argus-agent \
  ghcr.io/kairos-dev-kairos-ecl/argus-agent:latest
```

A reference manifest is in [`deploy/kubernetes/deployment.yaml`](deploy/kubernetes/deployment.yaml).

### Build from source

```bash
make build                 # compile for the current platform
make install               # install argus-agent to GOPATH/bin
cp config/agent.example.yaml agent.yaml
argus-agent --config agent.yaml
```

> The first run in `mode: remote` triggers XDR registration (Group ID + install
> token → server-assigned instance ID). See [Deployment](#deployment) below.

## Instrumenting an application

Your LLM app sends signals to the **local agent**, not directly to XDR:

```python
# Python
from argus_sdk import ArgusClient
client = ArgusClient(endpoint="http://127.0.0.1:5002")
client.emit(layer="L9_API_GATEWAY", category="api.request", severity="INFO")
```

---

## Observability

A single HTTP server (bind address `observability.addr`, default `127.0.0.1:9090`) exposes:

- **Liveness probe** (`GET /healthz`) — returns `200 OK` once the process is serving.
- **Readiness probe** (`GET /readyz`) — returns `200 OK` once startup completes and connectors are wired; `503` otherwise so orchestrators hold traffic until the agent is ready.
- **Metrics endpoint** (`GET /metrics`) — Prometheus text-format dispatcher counters (`argus_dispatch_accepted_total`, `_delivered_total`, `_failed_total`, `_dropped_total`). No client library; the format is hand-rolled to avoid a dependency.

The server is on by default. Bind `0.0.0.0` in containers so the kubelet can reach the probes at the pod IP, or set `observability.disabled: true` to turn it off entirely.

---

## Deployment

### XDR Registration Flow

1. **Admin creates Agent Group** in ArgusXDR → receives Group ID + Install Token
2. **Agent starts** with install_token in config → calls XDR registration endpoint
3. **XDR verifies token, assigns Instance ID** → stored in encrypted agent-state.json
4. **Clear install_token** from config after registration succeeds
5. **Subsequent signal submissions** use Group ID + Instance ID + rotating API credential

### Credential Refresh

The agent automatically rotates API credentials. Fresh credentials are requested on a schedule or after failures. XDR returns new credentials as part of every signal submission response.

### Hot-reload via SIGHUP

Send SIGHUP to the agent process to trigger config reload without restarting:

```bash
kill -HUP $(pidof argus-agent)
```

Hot-reload is intentionally **bounded**: SIGHUP re-applies only the EUC watch list (`AIEndpoints` + `LocalInferencePorts`) and the log level. Transport, auth, and output topology are not reloaded — those require a restart by design, so a bad edit can never silently re-wire delivery. In-flight signals are preserved in the WAL buffer throughout.

---

## Directory Structure

```
argus-sdk/
├── cmd/argus-agent/        # Binary entry point (cobra CLI)
├── internal/
│   ├── agent/              # Core lifecycle: start/stop, config wiring,
│   │                       #   observability server, SIGHUP reload
│   ├── auth/               # Registration, credential refresh, encrypted state
│   ├── buffer/             # WAL-backed local signal buffer
│   ├── collector/
│   │   ├── collector.go    # Collector interface
│   │   ├── llm/            # gRPC listener for Python/TS instrumentation libs
│   │   └── euc/            # Shadow AI OS-level collector (Linux/Windows/macOS)
│   ├── connector/
│   │   ├── connector.go    # Connector interface + registry + dispatcher
│   │   ├── factory/        # Connector factory (instantiation by type)
│   │   ├── argusxdr/       # Mode 1: ArgusSignal proto → XDR
│   │   ├── kafka/          # Mode 2: OCSF → Kafka
│   │   ├── splunk/         # Mode 2: OCSF → Splunk HEC
│   │   ├── elastic/        # Mode 2: OCSF → Elasticsearch
│   │   └── syslog/         # CEF/ArcSight → syslog server
│   ├── ocsf/               # ArgusSignal → OCSF v1.3 mapper
│   ├── dryrun/             # Offline OCSF validation + signal recorder/loader
│   ├── resilience/         # Circuit breaker, token-bucket rate limiter
│   └── secrets/            # AES-256-GCM encrypted secrets store
├── pkg/signal/             # Public signal types (consumed by instrumentation libs)
├── proto/                  # Protocol buffer definitions (gen/ is generated output)
├── packaging/              # systemd unit, launchd plist, nfpm + macOS scripts
├── deploy/kubernetes/      # Reference Kubernetes manifest
├── test/llmsignal/         # End-to-end integration harness
├── docs/                   # RELEASING and other operator docs
├── config/agent.example.yaml
├── .goreleaser.yaml        # Release build: binaries, deb/rpm, docker, signing
├── Dockerfile              # Distroless/static container image
├── Makefile
├── go.mod
└── README.md
```

---

## Technology Stack

| Concern | Library |
|---------|---------|
| CLI | `github.com/spf13/cobra` |
| Config | `github.com/spf13/viper` |
| Logging | `go.uber.org/zap` |
| gRPC | `google.golang.org/grpc` |
| Protobuf | `google.golang.org/protobuf` |
| OS telemetry (Linux) | `github.com/cilium/ebpf` |
| OS telemetry (Windows) | `github.com/0xrawsec/golang-etw` |
| System metrics | `github.com/shirou/gopsutil/v4` |
| Kafka connector | `github.com/twmb/franz-go` |
| Test infrastructure | `github.com/testcontainers/testcontainers-go` |

---

## Relationship to ArgusXDR

ArgusSDK is a **sibling project**, not a fork or internal component of ArgusXDR. The only shared artifact is the `ArgusSignal` protobuf schema — the SDK uses it for Mode 1 output; XDR uses it internally throughout its pipeline.

Do not import XDR-internal packages from the SDK. The boundary is the proto schema and the public gRPC ingest API.

---

## Documentation & Project

| | |
|---|---|
| Release notes | [CHANGELOG.md](CHANGELOG.md) |
| Building & cutting a release | [docs/RELEASING.md](docs/RELEASING.md) |
| Contributing & local development | [CONTRIBUTING.md](CONTRIBUTING.md) |
| Security policy & disclosure | [SECURITY.md](SECURITY.md) |
| License | [Apache-2.0](LICENSE) |

## License

ArgusSDK is licensed under the [Apache License 2.0](LICENSE). Contributions are
accepted under the same license; see [CONTRIBUTING.md](CONTRIBUTING.md).
