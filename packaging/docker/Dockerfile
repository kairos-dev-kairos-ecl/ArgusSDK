# GoReleaser container build.
#
# This consumes the pre-cross-compiled `argus-agent` binary that GoReleaser
# places in the build context — it does NOT build from source. For a
# from-source image (e.g. `make docker`), use the Dockerfile at the repo root.
#
# Distroless static base: no shell, no package manager, runs as nonroot (uid
# 65532).
FROM gcr.io/distroless/static-debian12:nonroot

COPY argus-agent /usr/local/bin/argus-agent

# Observability port (config.observability.addr default :9090) and the gRPC
# ingest listener (config.ingest.listen.grpc).
EXPOSE 9090 5002

# Config is expected at /etc/argus-agent/agent.yaml (mount a ConfigMap/secret).
ENTRYPOINT ["/usr/local/bin/argus-agent", "--config", "/etc/argus-agent/agent.yaml"]
