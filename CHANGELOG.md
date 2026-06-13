# Changelog

All notable changes to ArgusSDK are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.0.0] — 2026-06-13

First public release. ArgusSDK is a standalone, config-driven signal collection
and forwarding agent for LLM applications and enterprise endpoints.

### Added

**Signal pipeline**
- gRPC ingest listener for the Python/TypeScript instrumentation libraries
  (TCP and optional Unix socket).
- Fan-out dispatcher delivering each batch to all healthy output connectors via
  a fixed worker pool, with per-batch delivery accounting.
- WAL-backed local buffer: signals are persisted to a write-ahead log during
  output outages and drained in order on reconnect with exponential backoff and
  jitter. Survives agent restarts (at-least-once delivery).

**Output connectors**
- `argusxdr` — native `ArgusSignal` protobuf (Mode 1, no translation).
- `kafka`, `splunk_hec`, `elastic`, `syslog` — external/SIEM outputs.
- OCSF v1.3 translation (Mode 2): set `ocsf: true` on a connector to emit OCSF
  instead of the native proto. Mapping lives entirely in the agent.

**XDR Mode-1 identity**
- EDR-pattern registration: Group ID + one-time Install Token are exchanged for
  a server-assigned Instance ID over TLS 1.3; the install token is consumed and
  invalidated after first use.
- Instance ID + rotating credential are persisted to an AES-256-GCM encrypted
  `agent-state.json` and reloaded on restart. Credentials are never written in
  plaintext; identity is never self-reported.
- Automatic credential refresh.

**EUC — Shadow AI observability**
- OS-level collector detecting access to configured AI endpoints and local
  inference ports (Ollama, LM Studio, vLLM), driven entirely by config.
- Linux: eBPF kprobe observer (`cilium/ebpf`, precompiled, no cgo) with a
  gopsutil local-port sampler; degrades gracefully without `CAP_BPF`.
- Windows: ETW Kernel-Network provider observer.
- macOS: no-root established-connection sampler (full Network Extension
  deferred — it is not distributable as a `go install` CLI).
- Low-privilege contract: no process enumeration, file monitoring, full packet
  capture, or payload inspection.

**Operability**
- Single observability HTTP server exposing `/healthz`, `/readyz`, and
  Prometheus-format `/metrics` (dispatcher counters).
- Bounded SIGHUP hot-reload: re-applies the EUC watch list and log level without
  a restart. Transport, auth, and output topology require a restart by design.
- Single canonical sample configuration (`config/agent.example.yaml`), validated
  by a drift-guard test so documented keys cannot diverge from the code.
- `argus-agent --version` reports the build version.

**Packaging & distribution**
- Native installable service for every platform:
  - **Linux** — `.deb`/`.rpm` packages that install a systemd unit, a default
    config, and a dedicated unprivileged `argus-agent` service user.
  - **Windows** — runs under the Service Control Manager; `argus-agent service
    install|uninstall|start|stop` subcommands manage it.
  - **macOS** — launchd plist with `install.sh`/`uninstall.sh` helpers.
- Distroless static container image (`ghcr.io/kairos-dev-kairos-ecl/argus-agent`)
  and a reference Kubernetes manifest.
- Automated releases via GoReleaser: cross-platform binaries, packages, archives,
  and a multi-arch container image, published on tag.
- Supply-chain trust with zero cost or secrets: Sigstore **cosign keyless**
  signatures over the checksums and container image, plus **SLSA build-provenance
  attestations** for every artifact (verifiable with `cosign verify-blob` and
  `gh attestation verify`). See [docs/RELEASING.md](docs/RELEASING.md).

### Security
- TLS 1.3 enforced for remote mode (no downgrade path exposed).
- Encrypted-at-rest agent state (AES-256-GCM).
- Runs as a non-root user in the container image.

[1.0.0]: https://github.com/kairos-dev-kairos-ecl/ArgusSDK/releases/tag/v1.0.0
