# GW-6: Hourly inference probe cache

## Problem

Apply, `/health`, and every worker request treated `GET /v1/models` (or Ollama `/api/tags`) as “can we infer.” That extra catalog call ran on the hot path, and chat-only OpenAI-spec endpoints looked down even when completions worked. A named catalog model missing from the list also blocked readiness.

## Impact

- Extra latency and rate-limit exposure on every completion
- False `not_ready` when the models list is missing or incomplete
- No distinction between “chat is down” and “this model id is not in the list”

## Shorthand solution

Cache one models-list and one chat ping per unique `vendor|baseURL`. Refresh from `/health` at most hourly (apply forces a refresh). Worker `Complete` does not list models. If the manifest names a model that is not in the cached catalog, `/health` records a warning and stays ready when chat ping succeeded.

## Related code

- `internal/inference`
- `internal/health`
- `internal/worker`
- `cmd/gateway/main.go`
- `README.md`
