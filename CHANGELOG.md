# Changelog

All notable changes to ArgusSDK are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.0.1] — 2026-06-14

### Fixed
- **Output delivery for named connectors.** Connectors were registered under
  their type name (e.g. `kafka`) while the dispatcher routes by the configured
  output name (e.g. `kafka-prod`); any output whose name differed from its type
  silently received no batches ("connector not found"). The factory now keys each
  connector by its configured output name. Found via an end-to-end Kafka smoke
  test; covered by a new regression test. (Unit/integration tests missed it
  because they exercised `Send()` directly, bypassing name-based routing — and
  the prior factory tests asserted the buggy behavior.)

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
- Installers ship a safe local-mode default config (`config/agent.default.yaml`)
  so the service starts immediately with no credentials; the full remote/XDR
  reference (`config/agent.example.yaml`) is shipped alongside. The Windows
  service backgrounds startup so a slow/misconfigured endpoint can never block
  the SCM start. Config is validated by a drift-guard test.
- `argus-agent --version` reports the build version.

**Packaging & distribution**
- Native installable service for every platform:
  - **Linux** — `.deb`/`.rpm` packages that install a systemd unit, a default
    config, and a dedicated unprivileged `argus-agent` service user.
  - **Windows** — an **MSI installer** that registers and auto-starts the
    `argus-agent` service and drops a default config; silently installable for
    MDM/Intune (`msiexec /i … /quiet`). The `argus-agent service ...`
    subcommands remain for manual setups.
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

[1.0.1]: https://github.com/kairos-dev-kairos-ecl/ArgusSDK/releases/tag/v1.0.1
[1.0.0]: https://github.com/kairos-dev-kairos-ecl/ArgusSDK/releases/tag/v1.0.0
