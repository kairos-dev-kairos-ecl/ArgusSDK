# Argus SDK — Makefile
# See .planning/WORKSTREAMS.md G5 for the full target list.

.PHONY: proto build build-all test test-int test-llm lint docker install

# proto: regenerate Go gRPC stubs from proto/sdk/v1/ingest.proto
#
# Requires buf CLI (https://buf.build/docs/installation).
# Generated stubs are committed to gen/go/sdk/v1/ for reproducibility.
proto:
	@command -v buf > /dev/null 2>&1 || { \
		echo "Error: buf is not installed. Install from https://buf.build/docs/installation"; \
		exit 1; \
	}
	cd proto && buf generate

# build: compile for the current GOOS/GOARCH
build:
	go build ./...

# build-all: cross-compile for all supported platforms
build-all:
	GOOS=linux   GOARCH=amd64  go build -o bin/argus-linux-amd64   ./...
	GOOS=linux   GOARCH=arm64  go build -o bin/argus-linux-arm64   ./...
	GOOS=windows GOARCH=amd64  go build -o bin/argus-windows-amd64.exe ./...
	GOOS=darwin  GOARCH=arm64  go build -o bin/argus-darwin-arm64  ./...

# test: run unit tests
test:
	go test ./...

# test-int: run integration tests (requires Docker)
test-int:
	go test ./... -tags=integration

# test-llm: run local LLM signal tests (requires a local Ollama or vLLM server)
# Skips cleanly when no backend is reachable. See test/llmsignal/README.md.
test-llm:
	go test -tags=llmlocal ./test/llmsignal/... -v

# lint: run golangci-lint
lint:
	golangci-lint run ./...

# docker: build distroless/static container image (WS-G5)
docker:
	@echo "TODO: implement in WS-G5 (distroless/static image, < 20 MB)"

# install: install argus-agent binary to GOPATH/bin
install:
	go install ./cmd/argus
