# GW-4: Replace queue ingestion with OpenAI HTTP

## Problem

Producers ingest work by `LPUSH`/`BRPOP` of CloudEvents on Redis. That couples every caller to an internal bus, a custom envelope, and async correlation. Callers that already speak OpenAI Chat Completions cannot use the gateway without a Redis adapter.

## Impact

- External services must implement CloudEvents and Redis lists instead of a standard HTTP client
- The producer contract and the internal work queue are the same channel
- Priority fairness stays useful internally but should not be the public API

## Shorthand solution

Expose OpenAI-compatible HTTP (`POST /v1/chat/completions`, `GET /v1/models`) as the producer contract. `model` is a capability id. The gateway maps the request to an internal CloudEvent, `LPUSH`es the existing Redis input list, and the HTTP handler `BRPOP`s a per-request correlation key after the worker publishes. Redis remains the internal work queue (priority lanes + worker).

## Status

Implemented: OpenAI HTTP producer contract (`POST /v1/chat/completions`, `GET /v1/models`); Redis remains the internal work queue with per-request correlation wait.

## Related code

- `docs/openai-http-ingestion.md`
- `internal/openai`
- `internal/queue/redis.go` (`Enqueue`, `Wait`, correlation `Publish`)
- `cmd/gateway/main.go`
- `internal/health/health.go` (shared `:80` mux)
- `README.md`
- `scripts/push-test-event.sh`
