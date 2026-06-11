# syntax=docker/dockerfile:1

# ---- Build stage ----
# Pinned to the go.mod toolchain (go 1.26.1). CGO is disabled so the binary is
# fully static and runs on the distroless static base with no libc.
FROM golang:1.26.1-bookworm AS build

WORKDIR /src

# Cache module downloads separately from the source tree.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Static build of the agent binary.
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/argus-agent ./cmd/argus-agent

# ---- Runtime stage ----
# Distroless static: no shell, no package manager, minimal attack surface.
# nonroot runs as uid 65532.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/argus-agent /usr/local/bin/argus-agent

# Observability port (config.observability.addr default :9090) and the gRPC
# ingest listener (config.ingest.listen.grpc).
EXPOSE 9090 5002

# Config is expected at /etc/argus-agent/agent.yaml (mount a ConfigMap/secret).
ENTRYPOINT ["/usr/local/bin/argus-agent", "--config", "/etc/argus-agent/agent.yaml"]
