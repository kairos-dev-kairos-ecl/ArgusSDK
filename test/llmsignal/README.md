# llmsignal — local LLM signal test suite

Real-infrastructure tests that drive a **local LLM runtime** (Ollama or vLLM)
with actual prompt passes and validate argus-agent's full signal lifecycle
against the resulting interactions.

These tests are gated behind the `llmlocal` build tag, so they never run in the
default unit suite (`go test ./...`) or the Docker integration job. They require
a local LLM server and **skip cleanly** when none is reachable.

## What it tests

| Goal | Test | Validates |
|------|------|-----------|
| 1 — E2E ingest pipeline | `TestIngestPipelineE2E` | Real call → proto batch → agent's `llm.Collector` gRPC server → ingest channel → dispatcher → fake connector **delivered**. The agent's real receive→convert→deliver path. |
| 2 — Signal extraction fidelity | `TestExtractionFidelity` | A real prompt pass produces a signal that faithfully captures model, latency, token usage, trace/span correlation, classification (layer/category/severity), and ContextJSON provenance. |
| 3 — EUC Shadow-AI detection | `TestEUCLocalInferenceDetection` | A genuinely-listening Ollama/vLLM port is shaped into a correct `euc.local_inference` signal (host/port/process in ContextJSON). |
| 4 — Connector wire output | `TestConnectorWireOutput` | Both delivery wire formats carry the interaction: Mode 1 `argusxdr` proto (`Batch.ToProto`) and Mode 2 OCSF event (`ocsf.Mapper.Map`). |

Both backends are driven through one `Client` interface (OpenAI-compatible `/v1`),
so every test runs against whichever backend(s) are up.

## Running

Start a backend, then run the suite:

```bash
# Ollama
ollama serve &
ollama pull llama3.2

# or vLLM (OpenAI-compatible server)
# python -m vllm.entrypoints.openai.api_server --model Qwen/Qwen2.5-0.5B-Instruct

make test-llm
# or:
go test -tags=llmlocal ./test/llmsignal/... -v
```

## Configuration (env overrides)

| Var | Default |
|-----|---------|
| `ARGUS_TEST_OLLAMA_URL` | `http://127.0.0.1:11434` |
| `ARGUS_TEST_OLLAMA_MODEL` | `llama3.2` |
| `ARGUS_TEST_VLLM_URL` | `http://127.0.0.1:8000` |
| `ARGUS_TEST_VLLM_MODEL` | `Qwen/Qwen2.5-0.5B-Instruct` |

## Caveat — EUC OS capture is stubbed

The platform OS collectors (`internal/collector/euc/{linux,windows,darwin}.go`)
are currently stubs — nothing yet captures real OS network events. Goal 3
therefore supplies the `Observation` a real OS collector *would* produce (built
from a live TCP probe of the running inference port) and asserts the collector
converts it correctly. When a real OS collector lands, only the **source** of the
Observation changes; these assertions still hold.
