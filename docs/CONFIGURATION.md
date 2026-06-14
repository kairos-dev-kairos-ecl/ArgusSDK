# Configuration

`argus-agent` is driven entirely by a single YAML file. This guide explains
where data goes and how to define what the agent watches and where it sends
detections.

## Where data goes (read this first)

Three different things are easy to confuse:

| Thing | Goes to | Purpose |
|---|---|---|
| **Detections** (signals, e.g. `euc.ai_access`) | the **`outputs[]`** you configure — Kafka, Splunk, Elastic, syslog, or ArgusXDR | the product data your security team consumes |
| **Agent operational log** | `logging.file` (+ stdout) | diagnostics only — startup, errors, ETW warnings. **Not** the detections |
| **Local buffer** | `buffer.dir` (write-ahead log) | holds detections on disk while an output is unreachable, drains on reconnect |

So `…\logs\agent.log` is the agent talking about itself. The detections of AI
usage leave the host via `outputs[]` — that is where you read them.

## Config file location

| Platform | Path | Apply changes |
|---|---|---|
| Windows | `C:\ProgramData\argus-agent\agent.yaml` | `Restart-Service argus-agent` |
| Linux | `/etc/argus-agent/agent.yaml` | `sudo systemctl restart argus-agent` (or `systemctl reload` for a bounded hot-reload) |
| macOS | `/etc/argus-agent/agent.yaml` | reload the launchd job |

Override the path with `--config <file>`. On Linux/macOS, **SIGHUP** performs a
bounded hot-reload of just the EUC watch list and log level without a restart;
on Windows use `Restart-Service`.

A full annotated reference ships as `agent.example.yaml` (and is installed next
to your config); the safe local-mode default is `agent.default.yaml`.

## Defining what to watch — `ingest.euc` (the rules)

```yaml
ingest:
  listen:
    grpc: "127.0.0.1:5002"   # where instrumentation libraries send signals
  euc:
    # Cloud AI services to flag, matched against the hostname seen in DNS queries.
    # A built-in catalog (OpenAI, Anthropic, Gemini, Copilot, Cursor,
    # Windsurf/Codeium, Cody, Tabnine, ...) is ALWAYS active; entries here are
    # ADDED to it — e.g. your internal LLM gateway. Suffix match: "acme.ai"
    # also matches "api.acme.ai".
    ai_endpoints:
      - "llm-gateway.corp.example"
    # Local AI-runtime ports to flag (Ollama 11434, LM Studio 1234, vLLM 8000).
    local_inference_ports:
      - 11434
      - 1234
      - 8000
```

A detection becomes an OCSF event with `category: euc.ai_access` (cloud) or
`euc.local_inference` (local), including the matched host/port and the process
name where available.

## Defining where detections go — `outputs[]`

At least one output is required. Each delivers every detection; set `ocsf: true`
to translate to OCSF before sending (ArgusXDR always receives the native proto).

```yaml
outputs:
  - name: "siem-kafka"        # operator-chosen name (used for routing/logs)
    type: "kafka"
    endpoint: "broker1:9092,broker2:9092"
    ocsf: true
    topic: "argus-signals"
    tls: { enabled: true, min_version: "1.3" }
    auth:
      sasl_mechanism: "SCRAM-SHA-256"
      sasl_username: "argus"
      sasl_password: "${ARGUS_SDK_KAFKA_PASSWORD}"
```

Supported `type` values and their keys:

| type | `endpoint` | auth / extra keys |
|---|---|---|
| `argusxdr` | XDR gRPC `host:port` | `auth.group_id`, `auth.instance_id`, `auth.credential` |
| `kafka` | comma-separated brokers | `topic`; `auth.sasl_mechanism`/`sasl_username`/`sasl_password` |
| `splunk_hec` | HEC URL | `auth.token`; `index`, `source`, `sourcetype` |
| `elastic` | Elasticsearch URL | `auth.api_key`; `index` |
| `syslog` | `host:port` | `transport` (`udp`\|`tcp`\|`tls`) |

Secrets should come from `ARGUS_SDK_*` environment variables (referenced as
`${...}`) rather than being written in the file.

## Other sections

```yaml
agent:
  instance_name: "prod-laptop-01"
  mode: "local"            # "local" = no XDR/credentials; "remote" = TLS 1.3 + XDR registration
  # mode: remote also needs: group_id, xdr_endpoint, and auth.install_token (first run)

buffer:
  dir: "/var/lib/argus-agent/buffer"
  max_size_mb: 256
  flush_interval: "5s"
  drain_on_reconnect: true

tls:
  enabled: true
  min_version: "1.3"       # 1.3 is the floor; not downgradable
  ca_cert: ""             # optional custom CA PEM

logging:
  level: "info"            # debug | info | warn | error
  format: "json"           # json | console
  file: "/var/log/argus-agent/agent.log"   # rotating agent diagnostics (a service has no console)

observability:
  disabled: false
  addr: "127.0.0.1:9090"   # GET /healthz, /readyz, /metrics
```

## Reading the detections

Consume them from whatever output you configured — e.g. the Kafka topic, the
Splunk index, the Elasticsearch index, your syslog collector, or ArgusXDR. The
agent's own `agent.log` is only for troubleshooting the agent itself.

```bash
# example: tail the Kafka topic
kafka-console-consumer --bootstrap-server <broker> --topic argus-signals --from-beginning
```
