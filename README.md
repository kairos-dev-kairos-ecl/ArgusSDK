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

OS-specific implementations: `internal/collector/euc/linux.go` (eBPF), `windows.go` (WFP/ETW), `darwin.go` (Network Extension).

---

## Quick Start (stub — implementation in progress)

```bash
# Install
go install github.com/kairos-dev-kairos-ecl/ArgusSDK/cmd/argus-agent@latest

# Configure
cp config/agent.example.yaml agent.yaml
# Edit agent.yaml: set group_id, install_token, and outputs.endpoint

# Run (first run triggers registration)
argus-agent --config agent.yaml
```

Instrument your application:

```python
# Python (sends to local agent, not directly to XDR)
from argus_sdk import ArgusClient
client = ArgusClient(endpoint="http://127.0.0.1:5002")
client.emit(layer="L9_API_GATEWAY", category="api.request", severity="INFO")
```

---

## Directory Structure

```
argus-sdk/
├── cmd/argus-agent/        # Binary entry point (cobra CLI)
├── internal/
│   ├── agent/              # Core lifecycle: start, stop, config wiring
│   ├── auth/               # Registration, credential refresh
│   ├── buffer/             # WAL-backed local signal buffer
│   ├── collector/
│   │   ├── collector.go    # Collector interface
│   │   ├── llm/            # gRPC listener for Python/TS libs
│   │   └── euc/            # Shadow AI OS-level collector (Linux/Windows/macOS)
│   ├── connector/
│   │   ├── connector.go    # Connector interface + registry + dispatcher
│   │   ├── argusxdr/       # Mode 1: ArgusSignal proto → XDR
│   │   ├── kafka/          # Mode 2: OCSF → Kafka
│   │   ├── splunk/         # Mode 2: OCSF → Splunk HEC
│   │   └── syslog/         # CEF/ArcSight → syslog server
│   ├── ocsf/               # ArgusSignal → OCSF v1.3 mapper
│   ├── resilience/         # Circuit breaker, token-bucket rate limiter
│   └── secrets/            # AES-256-GCM encrypted secrets store
├── pkg/signal/             # Public signal types (consumed by instrumentation libs)
├── config/agent.example.yaml
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
| Signal IDs | `github.com/oklog/ulid/v2` |
| Metrics | `github.com/prometheus/client_golang` |

---

## Relationship to ArgusXDR

ArgusSDK is a **sibling project**, not a fork or internal component of ArgusXDR. The only shared artifact is the `ArgusSignal` protobuf schema — the SDK uses it for Mode 1 output; XDR uses it internally throughout its pipeline.

Do not import XDR-internal packages from the SDK. The boundary is the proto schema and the public gRPC ingest API.
