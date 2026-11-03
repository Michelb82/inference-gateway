# Construction AI Gateway

Standalone Go service that exposes AI capabilities over an OpenAI-compatible HTTP API and keeps Ollama model details inside the gateway.

## Architecture

- External ingestion: OpenAI Chat Completions over HTTP (`POST /v1/chat/completions`, `GET /v1/models`)
- Internal work queue (Redis): `queue:ai.requests` plus priority lanes `queue:ai.requests:critical|high|medium|low`
- Internal responses: `queue:ai.responses` and per-request `queue:ai.responses:{id}`
- Manifest-defined capabilities: add, edit, or remove any valid capability ID without rebuilding. The sample includes `routing`, `intent-classification`, `parameter-extraction`, `personal-details-extraction`, `translate`, and `configure-environment`.
- Inference runtimes: **two** vendors — `ollama` and `openai`. Only vendors bound in the applied manifest are active; Ollama does not need to run unless the manifest uses it.
- Optional manifest-defined HTTP tools provide a bounded agent loop: the LLM formats a request, the gateway calls the selected tool, and only the terminal success/error reaches egress.
- Structured logs always go to the local sink and are also sent to Loki when `config.loki_endpoint` is configured and its `/ready` endpoint is reachable.
- Health: `GET /health` (HTML), `GET /health.json` (JSON) on port 80
- Docker network: `dev` (external)

Applications request a **capability** via the OpenAI `model` field. The gateway maps that capability to a configured LLM endpoint and (quantized) model and never accepts caller-supplied LLM names.

Callers never talk to Redis. They use an OpenAI-compatible client against this HTTP API:

```text
Consumer
   │
   ▼
POST /v1/chat/completions   (model = capability id)
   │
   ▼
Gateway HTTP adapter  →  internal CloudEvent  →  Redis work queue
   │                                              (priority lanes)
   │                                                      │
   │                                              Worker + inference vendors
   │                                              (ollama / openai)
   │                                                      │
   ◄──────── wait on queue:ai.responses:{id} ◄────────────┘
   │
   ▼
OpenAI chat.completion
```

The HTTP call is synchronous: the handler enqueues an internal CloudEvent, the worker processes it with priority fairness, and the handler waits for the correlated response. The producer contract is [docs/openai-http-ingestion.md](docs/openai-http-ingestion.md).

Optional extra field `priority` on each request selects a processing lane: `CRITICAL`, `HIGH`, `MEDIUM`, or `LOW` (case-insensitive). Missing or invalid values default to `LOW`. The gateway demuxes the internal input list into Redis lanes and schedules work with fairness counters (see Configuration).

### Configuration management

Runtime configuration (inference endpoints, capability→endpoint bindings, input character limits, Redis **internal** queue, CloudEvent prefix, priority fairness) comes from a **manifest**, not from `.env` (except `OPENAI_BEARER_TOKEN`). The manifest defines capability IDs, endpoint bindings, input fields, system prompts, locale variables, input limits, and optional output schemas. The gateway executes these definitions generically; no capability names are compiled in.

- **Primary path:** poll `MANIFEST_URL` every `MANIFEST_POLLING_INTERVAL`. Until a valid manifest is applied, the gateway stays **dormant** (process up, health reports `dormant`, OpenAI endpoints return 503, no Redis consumer / capability work).
- **Optional local file:** `--manifest /path/to/manifest.json` is for development, tests, or experimental setups only. It is **not** required to boot. If the flag is set, the file must be valid or startup exits.
- **Soft failure:** if the manager is unavailable or returns an invalid document, the gateway keeps the current snapshot (or remains dormant). A failed poll never clears a working configuration.
- On a successful apply, the gateway activates (or updates) the data plane using **rank 0** model bindings from `capability_models`. Ranked fallbacks are reserved for later.

Private control-plane discovery remains available while the gateway is dormant:

- `GET /management/v1/manifest-support` describes the supported capability definition fields, schema keywords, inference vendors, and ingress adapters. Contract and manifest-format versions are now `2`; active capability IDs come from `/v1/models`.
- `POST /management/v1/manifests/validate` validates a candidate with the same parser and resolver used during polling. Valid documents return HTTP 200; invalid documents return HTTP 422.

These endpoints are for Inference Service Manager on a trusted Docker network. Do not expose `/management/` through a public reverse proxy. OpenAI-compatible runtimes such as vLLM use vendor `openai` unless a dedicated vendor is added to a future gateway build.

Compose for local/dev mounts `manifest.json` and passes `--manifest` so the stack works without AI Manager. Production-oriented runs can omit that flag and wait for the manager.

Redis remains an internal work queue; producers use the OpenAI-compatible HTTP API.

## Prerequisites

1. Start the sibling construction stack so Redis is available on the shared Docker network. Start Ollama (`llm-model` / `foundation-model`) only if the manifest binds `vendor: ollama`. An OpenAI-only manifest does not need Ollama.

2. Ensure the `dev` network exists:

```bash
docker network ls | grep dev
```

3. For local/dev, provide a manifest (see below). On apply the gateway checks each **bound** capability’s inference URL (only vendors present in the applied manifest). For Ollama it pulls any missing configured models. **Redis must be reachable when a manifest is applied** (apply fails and the previous snapshot is kept, or the gateway stays dormant). Models are best-effort: if at least one configured endpoint is available, apply continues and missing models are logged as warnings; apply fails only when none are available. Ollama is not contacted when no rank-0 binding uses `vendor: ollama`.

## Run locally in Docker

```bash
cp .env.dist .env
cp manifest.json.dist manifest.json
# Edit .env / manifest.json for private hostnames, CloudEvent prefixes, or the OpenAI catalog url.
docker compose up --build
```

Health and OpenAI endpoints are published on host port `18080` by default (`http://localhost:18080/health`, `/health.json`, `/v1/models`, `/v1/chat/completions`). Set `OPENAI_BEARER_TOKEN` in `.env` to enable the OpenAI vendor. Ollama-bound capabilities do not need that token, and Ollama is not contacted unless the applied manifest binds `vendor: ollama`.

## Configuration

### Bootstrap (`.env`)

Copy `.env.dist` to `.env`. These variables only configure how manifests are discovered:

| Variable | Default |
|----------|---------|
| `MANIFEST_URL` | `http://ai-manager:80/manifest.json` |
| `MANIFEST_POLLING_INTERVAL` | `5m` |
| `DEBUG` | `false` |
| `OPENAI_BEARER_TOKEN` | empty (OpenAI vendor disabled) |

At startup, an INFO JSON record named `configuration sources` is written to stdout before logger setup and manifest loading. It identifies the process environment (with built-in defaults), the optional `--manifest` local path (empty when absent), and the remote manifest URL and polling interval. Remote polling remains enabled when a local manifest is supplied. The gateway reads environment variables directly; Docker Compose supplies values from `.env`. This record does not dump configuration values or the bearer token.

When `DEBUG=true`, the logger also emits Debug-level full Redis and Ollama/OpenAI payloads (`incoming/outgoing traffic payload`, `ollama incoming` / `ollama outgoing`, `openai incoming` / `openai outgoing`) and appends the same JSON logs to `debug.log`. The Bearer token is never logged.

### Inference runtime vendors

There are **two** inference runtime vendors. Ingest is always OpenAI Chat Completions; egress follows `inference_endpoint[].vendor` on the bound catalog id.

The worker talks to an inference **port**. Only vendors present in the applied rank-0 bindings are probed or used:

| `inference_endpoint[].vendor` | Runtime | Notes |
|-------------------------------|---------|--------|
| `ollama` | Ollama | Chat Completions plus `/api/tags` / `/api/pull`. Required only when the manifest binds this vendor. |
| `openai` | OpenAI-compatible HTTP | [OpenAI OpenAPI](https://github.com/openai/openai-openapi): `POST /v1/chat/completions`, `GET /v1/models`, `Authorization: Bearer` |

`vendor` is required (`ollama` or `openai`). The OpenAI vendor is **off** unless `OPENAI_BEARER_TOKEN` is set. Without a token, OpenAI-bound endpoints stay unavailable; Ollama-bound capabilities still work. If nothing is bound to `ollama`, Ollama does not need to run and is never health-checked.

Example catalog entries:

```json
{
  "id": "qwen3:1.7b",
  "vendor": "ollama",
  "url": "http://llm-model:11434",
  "model": "qwen3:1.7b-q4_K_M",
  "keep_alive_seconds": 300
}
```

```json
{
  "id": "openai",
  "vendor": "openai",
  "url": "https://someurl",
  "model": null,
  "keep_alive_seconds": 300
}
```

`url` is a base URL. The OpenAI vendor appends `/v1/chat/completions` and `/v1/models`. When `model` is `null`, the outbound Chat Completions body omits `model`.

### Manifest (`manifest.json`)

Copy [manifest.json.dist](manifest.json.dist) to `manifest.json` for development. The distribution sample uses Ollama at rank 0; its `openai` endpoint is an inactive placeholder (`https://provider.example`, no runtime model). To test your API key, set that endpoint to your provider URL/model and reference it from the desired capability at rank 0. Merely adding an OpenAI endpoint or setting the key does not activate it. The distribution sample contains six example capabilities and optional ranked bindings. None of those IDs are mandatory: a manifest with just one custom capability is valid. All capabilities may share one inference endpoint.

| Field | Purpose |
|-------|---------|
| `inference_endpoint` | Endpoint catalog: `id`, `vendor` (`ollama` or `openai`), base `url`, runtime `model`, and `keep_alive_seconds`. Model and positive keep-alive are required for Ollama; OpenAI can omit its runtime model. |
| `capability_models` | Nonempty map from capability ID to ranked bindings. IDs match `[a-z][a-z0-9-]*`. Every list needs rank 0 and unique ranks; higher ranks do not yet provide automatic failover. |
| `tools` | Optional trusted HTTP tool definitions with `name`, `endpoint`, and `instruction`. |
| `ingress` | Internal Redis address, request/response queues, and BRPOP timeout. |
| `config` | CloudEvent prefix, HTTP address, priority fairness, caller override policy, and optional Loki endpoint. |

Every ranked capability binding declares:

| Field | Requirement |
|-------|-------------|
| `rank` | Rank 0 selects the active definition. |
| `model` | An `inference_endpoint[].id`, rather than that endpoint's runtime model name. |
| `max_input_chars` | Required integer from **200 through 500000**, inclusive; no default. Missing/null/zero values fail with `capability <id> has not defined max_input_chars please see documentation`. |
| `system_prompt` | Required nonblank instruction string. Missing/blank values fail with `capability <id> has not defined system_prompt please see documentation`. |
| `input_field` | Required: `message` or `text`. The last HTTP user message becomes this internal input field. |
| `prompt_variables` | Optional map declaring `source_locale` and/or `target_locale`. Each declaration requires `default` and a nonempty `allowed` list containing that default. No other variable names are supported. |
| `output_schema` | Optional JSON object schema from the supported subset below. Omit it to accept any nonempty JSON object. |

Complete minimal manifest with a new capability:

```json
{
  "inference_endpoint": [
    {
      "id": "local-model",
      "vendor": "ollama",
      "url": "http://foundation-model:11434",
      "model": "qwen3:1.7b-q4_K_M",
      "keep_alive_seconds": 300
    }
  ],
  "capability_models": {
    "appointment-summary": [
      {
        "rank": 0,
        "model": "local-model",
        "max_input_chars": 8000,
        "input_field": "message",
        "system_prompt": "Summarize the appointment request. Return only a JSON object with a summary string.",
        "output_schema": {
          "type": "object",
          "required": [
            "summary"
          ],
          "properties": {
            "summary": {
              "type": "string",
              "minLength": 1
            }
          }
        }
      }
    ]
  },
  "ingress": {
    "adapter": "redis",
    "address": "redis:6379",
    "ingress_channel": "ai.requests",
    "egress_channel": "ai.responses",
    "brpop_timeout_seconds": 5
  },
  "config": {
    "message_prefix": "com.buildright.ai",
    "http_address": ":80",
    "priority_count_high": 3,
    "priority_count_medium": 3
  }
}
```

Call it with `model: "appointment-summary"`. To use an OpenAI-compatible provider instead, change the endpoint's vendor to `openai`, configure its URL/runtime model, and set `OPENAI_BEARER_TOKEN` in `.env`. The capability binding still references the endpoint ID.

### Prompts, variables, and output schemas

Prompt edits, input mappings, variable defaults, schema edits, and capability additions/removals are fingerprinted and applied without a rebuild. The sample's six IDs are examples, not built-ins. No automatic routing chain is executed when a model returns a capability name.

For translation behavior on any capability, use `input_field: "text"` and declare:

```json
"prompt_variables": {
  "source_locale": {"default": "nl", "allowed": ["nl", "en"]},
  "target_locale": {"default": "en", "allowed": ["en", "nl", "pl"]}
},
"system_prompt": "Translate from {source_locale} to {target_locale}. Return only JSON with a text string."
```

A single capability handles every allowed source/target pair. Each request selects one source and one target using the HTTP extras of the same names. Omitted or blank values use the declared defaults; those defaults do not restrict translation to a single pair. Defaults and allowed entries must be nonblank strings, allowed entries must be unique after trimming, and the default must belong to its allowed list. Matching is case-sensitive after whitespace trimming. A disallowed value returns HTTP 400 with `code: "invalid_prompt_variable"` and `param` identifying the field, before enqueueing. Workers recheck queued requests against the definition active when processing begins; a value removed in a reload produces a correlated 400 instead of reaching inference. Authorized caller prompt overrides still enforce these selection limits.

For example, the same `translate` capability can translate English to Polish:

```json
{
  "model": "translate",
  "messages": [{"role": "user", "content": "Good morning"}],
  "source_locale": "en",
  "target_locale": "pl",
  "priority": "LOW"
}
```

Allowed values are configured per variable, so `pl` can be a permitted target without being a permitted source. The gateway renders nl/en as Dutch/English and other codes verbatim. Named placeholders must be declared; unsupported declarations/placeholders are rejected. Percent signs and JSON prompt examples are literal text. Authorized caller system messages replace the manifest prompt verbatim and bypass output-schema validation, retaining the existing generic JSON requirement and organisation policy. `config.max_system_prompt_chars` limits caller overrides, not trusted manifest prompts.

Supported output-schema rules:

- Root `type` must be `object`; optional `required` lists distinct fields declared in `properties`.
- Each property declares primitive `type`: `string`, `number`, `integer`, `boolean`, or `null`.
- Strings may declare nonnegative integer `minLength` (Unicode characters). Number/integer properties may declare inclusive `minimum` and `maximum`.
- Unsupported keywords (including `$ref`), nested object/array property types, null keyword values, and malformed rules are rejected when validating the manifest. This is a limited subset, not a full JSON Schema implementation.
- Results must be nonempty JSON objects. Extra fields are preserved; strings are not trimmed. Required fields and declared constraints are checked. With no schema, only the generic object check applies.

### Validate and test with your API key

Use `inference_endpoint` (the old `models` key is no longer the endpoint catalog), and set `vendor: "openai"` on the provider entry. Keep the key in `.env` as `OPENAI_BEARER_TOKEN`; do not put it in the manifest or client request. Each capability's rank-0 `model` references the provider entry's `id`. Its runtime `model` is the upstream model name.

First check JSON syntax, then rebuild the local gateway and fire a LOW-priority request:

```bash
python3 -m json.tool manifest.json >/dev/null
docker compose up -d --build ai-gateway
curl --silent --show-error --fail-with-body http://localhost:18080/health.json
curl --silent --show-error --fail-with-body http://localhost:18080/v1/models
MESSAGE='Can I change my appointment to next Tuesday?' bash scripts/push-test-event.sh
```

JSON syntax checking alone does not validate the gateway contract. The gateway validates a local manifest on startup; when a gateway is already running, `POST /management/v1/manifests/validate` can validate a candidate without applying it or contacting the provider. See the [live debug session](docs/live-debug-session.md) for this check, network addresses, error handling, and log inspection. Recreate the container after changing the key: it is read from the process environment at startup. Rebuild from this checkout to test the new capability behavior; an older published image may use a different manifest contract.

### Reload and migration

Queued requests use the configuration active when the worker starts processing. Removing their capability before processing produces a correlated failure instead of leaving the caller waiting. Once a worker captures a definition, it uses that same definition through completion. New HTTP requests for removed or unknown IDs return an OpenAI-shaped 400; `/v1/models` and health list only currently configured capabilities in sorted order. Invalid updates retain the previous runtime; an invalid local startup manifest fails startup.

Before upgrading, add `input_field` to every ranked entry and declare any locale placeholders explicitly. Replace earlier string-valued prompt-variable declarations with objects containing `default` and `allowed`; legacy string declarations are rejected. Update every ranked entry that declares variables, including inactive ranks. Keep mandatory `max_input_chars` and `system_prompt`. Add output schemas where validation is needed; use the updated sample as a reference. There is no implicit routing/intent/translation parser anymore. Compared with the previous specialized parsers, extra result fields now survive and declared numeric ranges can reject previously accepted values.

Management discovery uses `contract_version: "2"` and `manifest_format: "2"`. Its `capabilities` field is now a definition-support object (dynamic IDs, input fields, prompt variables, schema keywords, required prompt/limit flags and bounds), replacing the fixed capability array. `capabilities.prompt_variable_definition` describes the required `default`/`allowed` fields and selection rules. Update AI Manager clients accordingly. The `/management/v1/` URL paths remain unchanged; `/v1/models` supplies the active capability catalog. Validation remains dormant-safe and uses the exact parser/resolver used by polling.

Internal CloudEvent types remain `{config.message_prefix}.request`, `.request.completed`, and `.request.failed`.

### Priority fairness

Scheduling order (thresholds come from the manifest `config` section):

1. `CRITICAL` always first (does not affect counters)
2. `LOW` when due (see below)
3. `MEDIUM` when due (`priority_count_high` HIGH messages since the last due MEDIUM)
4. otherwise prefer `HIGH`, then `MEDIUM`, then `LOW`

Fairness counters reset when their threshold is reached:

- Every `priority_count_high` HIGH messages unlocks one MEDIUM
- Every `priority_count_medium` MEDIUM messages unlocks one LOW
- Every `priority_count_high * priority_count_medium` HIGH messages unlocks one LOW

With the defaults (`3` / `3`): every 3 HIGH → 1 MEDIUM; every 3 MEDIUM → 1 LOW; every 9 HIGH → 1 LOW. Example: after 9 HIGH and 2 MEDIUM, a LOW may run; processing one more MEDIUM then unlocks another LOW.

## Capability contract

Callers use OpenAI Chat Completions. `model` is the capability id. Full contract: [docs/openai-http-ingestion.md](docs/openai-http-ingestion.md).

```json
{
  "model": "intent-classification",
  "messages": [
    {"role": "user", "content": "I need my living room painted"}
  ],
  "priority": "HIGH"
}
```

### Manifest-defined tool execution

Add `"tool": "tool-name"` to a Chat Completions request to select a tool from the active manifest. The gateway appends that tool's trusted instruction to the capability system prompt, asks the model for a JSON request body, and POSTs it to the manifest endpoint. A 2xx response is returned as the completion result. For a 4xx/5xx response, the model receives the status and body and returns a structured `retry` or `stop` decision. The gateway permits at most three retries after the initial call and publishes only the final success or error.

```json
{
  "model": "intent-classification",
  "messages": [{"role": "user", "content": "Create the wall-painting job"}],
  "tool": "create-job"
}
```

Tool endpoints and instructions come only from the manifest. Do not place secrets in tool instructions: manifest contents may be operationally visible, and tool calls currently carry only a JSON body and `Content-Type: application/json`.

The current organisation header is caller-supplied and is not an authentication mechanism. Secure per-group RBAC must be connected to authenticated identity before organisation/tool allowlists can be treated as an authorization boundary.

| Example capability in sample | User content | Result JSON in `choices[0].message.content` |
|------------|--------------|-----------------------------------------------|
| `routing` | `message` | `{ "capability": "<next-capability>" }` |
| `intent-classification` | `message` | `{ "intent": "...", "confidence": 0.0-1.0 }` |
| `translate` | `text` (`source_locale` / `target_locale` extras) | `{ "text": "..." }` |

Success: HTTP 200, `object` is `chat.completion`, `model` is the capability id (no Ollama name). `content` is a **JSON string** of the result.

Failure: OpenAI `{ "error": { "message", "type", "param", "code" } }` with 4xx/5xx. Input over `max_input_chars` is `400` with a bounds message.

### Custom system prompt (`messages.role = system`)

The first `system` message is **denied by default**. It is accepted only when:

1. Header `X-Organisation-Id` is listed in manifest `config.system_prompt_override_orgs`, and
2. The override length is within `config.max_system_prompt_chars` (default `4000` runes).

Unauthorized or oversized overrides fail the HTTP request. Empty allowlist means no organisation may override.

When an override is authorized:

1. The manifest-configured system prompt is **not** used.
2. The value is sent to the LLM as `messages[]` with `role: "system"` (alongside the user message/text as `role: "user"`).
3. Result parsing is relaxed: the model’s JSON object is returned as `content` **as-is**, without enforcing the default capability result shapes above. Callers that override the system prompt own the response schema.

Example (organisation `7` must be in `system_prompt_override_orgs`):

```json
{
  "model": "routing",
  "messages": [
    {
      "role": "system",
      "content": "You are a routing specialist. Respond with ONLY valid JSON matching your organisation schema."
    },
    {
      "role": "user",
      "content": "{\"customer_request\":\"muren verven\",\"available_jobs\":[{\"id\":1,\"name\":\"Wall painting\"}]}"
    }
  ]
}
```

Without a system message, the gateway uses the manifest-configured system prompt and validates its output_schema, if configured.

Translate example:

```json
{
  "model": "translate",
  "messages": [
    {"role": "user", "content": "Elektrische installaties voor woningen"}
  ],
  "source_locale": "nl",
  "target_locale": "en"
}
```

## HTTP endpoints

| Path | Method | Meaning |
|------|--------|---------|
| `/v1/chat/completions` | POST | OpenAI Chat Completions; `model` is a capability id |
| `/v1/models` | GET | Lists the active manifest-defined capability IDs in sorted order. |
| `/health` | GET | Per-capability readiness page (HTML) |
| `/health.json` | GET | Same data for probes/automation (JSON) |

While dormant (no manifest), `/v1/*` returns an OpenAI-shaped **503**. Health reports `dormant` with HTTP **503**.

Status values:

- Overall: `ready` / `not_ready` / `dormant` (awaiting manifest)
- Per capability: `available` / `unavailable` (omitted while dormant)

HTTP status is `200` when ready and `503` when not ready or dormant. Model names appear only on health endpoints (ops), not on successful OpenAI completions.

`GET /health` and `/health.json` refresh each unique inference endpoint at most hourly: one catalog list (`GET /v1/models` or Ollama `GET /api/tags`) and one short chat ping. Manifest apply always refreshes. Overall `ready` follows chat ping success. A configured `model` missing from the catalog is a `warning` only; the capability stays `available` and HTTP remains **200**. Chat ping failure makes the capability `unavailable` and overall `not_ready` (**503**).

## Manual smoke test

For a [live debug session](docs/live-debug-session.md), follow gateway logs and send a **LOW** priority `intent-classification` request. Redis is assumed to be running; the gateway needs an applied manifest and an available model. The helper requires Bash, Python 3, and curl 7.76+ and prints the response or error (nonzero exit on failure).

With the gateway running and a manifest applied:

```bash
bash scripts/push-test-event.sh
MESSAGE='Please paint my kitchen' bash scripts/push-test-event.sh
CAPABILITY=routing bash scripts/push-test-event.sh
```

```bash
curl -sS http://localhost:18080/health.json
curl -sS http://localhost:18080/v1/models
```

## Tests

Run unit tests with Docker (no local Go required):

```bash
make test-docker
```

With Go installed locally:

```bash
make test
make test-cover
```

Manifest ingestion coverage lives in `internal/configmgmt` and includes:

- loading/validating `manifest.json.dist`
- parse/load/fetch error paths
- rank-0 resolution and fingerprint changes
- dormant boot, remote apply, soft-fail keep-current, and reject-on-apply-error behavior

Dev-only integration test against live Redis on `dev` (not run by `make test` / `make test-docker`):

```bash
make test-integration
```
