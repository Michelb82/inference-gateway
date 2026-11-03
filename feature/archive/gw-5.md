# GW-5: OpenAI API inference runtime vendor

## Problem

The gateway can only execute inference against Ollama. The Ollama client is used directly as the runtime, with no port/vendor boundary. Capabilities cannot be bound to an OpenAI-compatible HTTP API.

Catalog entries were named `models` with an optional `adapter` defaulting to Ollama, which always assumed an Ollama process even when unused.

## Impact

- Ollama-specific pull/tags logic is coupled to the worker and health path
- No way to route a capability to an OpenAI-spec endpoint
- An unused Ollama runtime would still be treated as a live dependency
- OpenAI-spec inference must stay disabled without a Bearer token

## Shorthand solution

Introduce an inference **port** (`internal/inference`). There are **two** vendors: `ollama` and `openai`. Catalog them as `inference_endpoint` with required `vendor`. Read `OPENAI_BEARER_TOKEN` from the environment (`.env` / Compose). If the token is empty, the OpenAI vendor is not used and those endpoints stay unavailable.

Register and probe only vendors present in the applied rank-0 bindings. Ollama does not need to exist and is never contacted unless the manifest binds `vendor: ollama`.

OpenAI vendor: `POST /v1/chat/completions`, `GET /v1/models`, `Authorization: Bearer`. `url` is a base URL (example `"https://someurl"`). Catalog `model` may be `null`; then the outbound body omits `model`.

## Status

Implemented: inference port with Ollama and OpenAI vendors; catalog `inference_endpoint` + `vendor`; Bearer token from `OPENAI_BEARER_TOKEN`; only applied vendors are active.

## Related code

- `internal/inference`
- `internal/ollama`
- `internal/capability`
- `internal/configmgmt`
- `internal/config`
- `internal/worker`
- `internal/health`
- `cmd/gateway/main.go`
- `docker-compose.yml`
- `.env.dist`
- `README.md`
