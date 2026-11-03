# Construction AI Gateway

Standalone Go service that exposes AI capabilities over an OpenAI-compatible HTTP API and keeps Ollama model details inside the gateway.

## Architecture

- External ingestion: OpenAI Chat Completions over HTTP (`POST /v1/chat/completions`, `GET /v1/models`)
- Internal work queue (Redis): `queue:ai.requests` plus priority lanes `queue:ai.requests:critical|high|medium|low`
- Internal responses: `queue:ai.responses` and per-request `queue:ai.responses:{id}`
- Built-in capabilities (gateway code): `routing`, `intent-classification`, `parameter-extraction`, `personal-details-extraction`, `translate`, `configure-environment`
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

Runtime configuration (inference endpoints, capability→endpoint bindings, input character limits, Redis **internal** queue, CloudEvent prefix, priority fairness) comes from a **manifest**, not from `.env` (except `OPENAI_BEARER_TOKEN`). The manifest is an ops/bindings document: capability system prompts and input limits come from `capability_models`; capability IDs, I/O shapes, and result parsers live in gateway code (`internal/capability`). `capability_models` must bind exactly the built-in capability ids.

- **Primary path:** poll `MANIFEST_URL` every `MANIFEST_POLLING_INTERVAL`. Until a valid manifest is applied, the gateway stays **dormant** (process up, health reports `dormant`, OpenAI endpoints return 503, no Redis consumer / capability work).
- **Optional local file:** `--manifest /path/to/manifest.json` is for development, tests, or experimental setups only. It is **not** required to boot. If the flag is set, the file must be valid or startup exits.
- **Soft failure:** if the manager is unavailable or returns an invalid document, the gateway keeps the current snapshot (or remains dormant). A failed poll never clears a working configuration.
- On a successful apply, the gateway activates (or updates) the data plane using **rank 0** model bindings from `capability_models`. Ranked fallbacks are reserved for later.

Private control-plane discovery remains available while the gateway is dormant:

- `GET /management/v1/manifest-support` lists the exact capabilities, inference vendors, defaults, and ingress adapters supported by this build.
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

Copy `manifest.json.dist` to `manifest.json` for local/dev. Its defaults match the sibling construction stack (`foundation-model`, Redis, and `com.buildright.ai`) and require no tool or Loki service. The sample includes all six required capabilities with a required rank-0 binding each and optional higher ranks. Every ranked binding explicitly defines its mandatory `max_input_chars`. The OpenAI catalog entry is unused until a capability references it at rank 0 and `OPENAI_BEARER_TOKEN` is set.

| Field | Purpose |
|-------|---------|
| `inference_endpoint` | Catalog entries with `id`, required `vendor` (`ollama` or `openai`), `url` (base URL), `model` (runtime model name; required for Ollama, nullable for OpenAI), `keep_alive_seconds` (required for Ollama; optional for OpenAI) |
| `capability_models` | Bindings for each built-in capability: ranked list of catalog `id`s (rank `0` is used today; higher ranks are reserved for failover). Every ranked entry requires integer `max_input_chars` from `200` through `500000` inclusive; there are no defaults. Every entry also requires a nonblank `system_prompt`. Unknown capability keys are rejected. |
| `tools` | Optional trusted tool catalog. Each entry requires a unique `name`, absolute HTTP(S) `endpoint`, and `instruction`. Requests select only by name; callers and model output cannot supply or override endpoints/instructions. |
| `ingress` | Internal Redis work queue: adapter, address, ingress/egress channels, BRPOP timeout. Not the producer contract. |
| `config` | Internal CloudEvent `message_prefix`, `http_address`, priority fairness counts, prompt controls, and optional `loki_endpoint`. Loki is disabled when blank and enabled only after `{endpoint}/ready` succeeds. |

All six keys are required in `capability_models`, even when your application calls only one capability:

| Required capability | Sample `max_input_chars` |
|---------------------|---------------------------|
| `routing` | 200 |
| `intent-classification` | 8000 |
| `parameter-extraction` | 16000 |
| `personal-details-extraction` | 16000 |
| `translate` | 16000 |
| `configure-environment` | 16000 |

Each key needs a nonempty list containing `rank: 0`. Every entry, including optional higher ranks, must include integer `max_input_chars` between `200` and `500000` inclusive. Missing or null values fail with `capability <id> has not defined max_input_chars please see documentation`. Zero, negative, and out-of-range values are rejected; strings and fractional numbers are invalid JSON field types. These limits are chosen in the manifest, with no built-in defaults. The binding's `model` references an `inference_endpoint[].id`, rather than the runtime model name in that endpoint's `model` field. All six capabilities may reference the same endpoint. Unknown capability keys, missing endpoint IDs, and duplicate ranks within a capability are rejected. Higher ranks are optional and currently do not provide automatic failover.

Existing manifests must add both `max_input_chars` and a nonblank `system_prompt` to every ranked entry before upgrading. An invalid local manifest stops startup; an invalid remote update is rejected and the current snapshot is retained. Management discovery (`GET /management/v1/manifest-support`) now reports `max_input_chars_required: true`, `min_input_chars: 200`, and `max_input_chars: 500000` for each capability instead of `default_max_input_chars`, plus `system_prompt_required: true`.

Complete sample (`manifest.json.dist`; replace endpoint URLs for your environment):

```json
{
  "inference_endpoint": [
    {
      "id": "qwen3:1.7b",
      "vendor": "ollama",
      "url": "http://foundation-model:11434",
      "model": "qwen3:1.7b-q4_K_M",
      "keep_alive_seconds": 300
    },
    {
      "id": "qwen3:4b",
      "vendor": "ollama",
      "url": "http://foundation-model:11434",
      "model": "qwen3:4b-q4_K_M",
      "keep_alive_seconds": 300
    },
    {
      "id": "qwen3:14b",
      "vendor": "ollama",
      "url": "http://foundation-model:11434",
      "model": "qwen3:14b-q4_K_M",
      "keep_alive_seconds": 120
    },
    {
      "id": "openai",
      "vendor": "openai",
      "url": "https://someurl",
      "model": null,
      "keep_alive_seconds": 300
    }
  ],
  "capability_models": {
    "routing": [
      {
        "rank": 0,
        "model": "qwen3:1.7b",
        "max_input_chars": 200,
        "system_prompt": "You are a routing model for a construction platform AI gateway.\nGiven a user message, choose the single best next AI capability.\nRespond with ONLY valid JSON in this exact shape:\n{\"capability\":\"<capability-name>\"}\nAllowed capability values: intent-classification, translate.\nDo not include markdown, explanation, or extra fields."
      },
      {
        "rank": 1,
        "model": "qwen3:4b",
        "max_input_chars": 200,
        "system_prompt": "You are a routing model for a construction platform AI gateway.\nGiven a user message, choose the single best next AI capability.\nRespond with ONLY valid JSON in this exact shape:\n{\"capability\":\"<capability-name>\"}\nAllowed capability values: intent-classification, translate.\nDo not include markdown, explanation, or extra fields."
      },
      {
        "rank": 2,
        "model": "openai",
        "max_input_chars": 200,
        "system_prompt": "You are a routing model for a construction platform AI gateway.\nGiven a user message, choose the single best next AI capability.\nRespond with ONLY valid JSON in this exact shape:\n{\"capability\":\"<capability-name>\"}\nAllowed capability values: intent-classification, translate.\nDo not include markdown, explanation, or extra fields."
      }
    ],
    "intent-classification": [
      {
        "rank": 0,
        "model": "qwen3:4b",
        "max_input_chars": 8000,
        "system_prompt": "You are an intent classification model for a construction services platform.\nGiven a user message, determine the user's intent and a confidence score between 0 and 1.\nRespond with ONLY valid JSON in this exact shape:\n{\"intent\":\"<intent-slug>\",\"confidence\":0.0}\nUse lowercase kebab-case for intent (for example wall-painting, bathroom-renovation).\nDo not include markdown, explanation, or extra fields."
      },
      {
        "rank": 1,
        "model": "qwen3:14b",
        "max_input_chars": 8000,
        "system_prompt": "You are an intent classification model for a construction services platform.\nGiven a user message, determine the user's intent and a confidence score between 0 and 1.\nRespond with ONLY valid JSON in this exact shape:\n{\"intent\":\"<intent-slug>\",\"confidence\":0.0}\nUse lowercase kebab-case for intent (for example wall-painting, bathroom-renovation).\nDo not include markdown, explanation, or extra fields."
      },
      {
        "rank": 2,
        "model": "qwen3:1.7b",
        "max_input_chars": 8000,
        "system_prompt": "You are an intent classification model for a construction services platform.\nGiven a user message, determine the user's intent and a confidence score between 0 and 1.\nRespond with ONLY valid JSON in this exact shape:\n{\"intent\":\"<intent-slug>\",\"confidence\":0.0}\nUse lowercase kebab-case for intent (for example wall-painting, bathroom-renovation).\nDo not include markdown, explanation, or extra fields."
      },
      {
        "rank": 3,
        "model": "openai",
        "max_input_chars": 8000,
        "system_prompt": "You are an intent classification model for a construction services platform.\nGiven a user message, determine the user's intent and a confidence score between 0 and 1.\nRespond with ONLY valid JSON in this exact shape:\n{\"intent\":\"<intent-slug>\",\"confidence\":0.0}\nUse lowercase kebab-case for intent (for example wall-painting, bathroom-renovation).\nDo not include markdown, explanation, or extra fields."
      }
    ],
    "parameter-extraction": [
      {
        "rank": 0,
        "model": "qwen3:14b",
        "max_input_chars": 16000,
        "system_prompt": "You are an AI assistant for a construction services platform.\nFollow the user request and respond with ONLY one valid JSON object.\nDo not include markdown or explanation."
      },
      {
        "rank": 1,
        "model": "qwen3:4b",
        "max_input_chars": 200,
        "system_prompt": "You are an AI assistant for a construction services platform.\nFollow the user request and respond with ONLY one valid JSON object.\nDo not include markdown or explanation."
      },
      {
        "rank": 2,
        "model": "openai",
        "max_input_chars": 200,
        "system_prompt": "You are an AI assistant for a construction services platform.\nFollow the user request and respond with ONLY one valid JSON object.\nDo not include markdown or explanation."
      }
    ],
    "personal-details-extraction": [
      {
        "rank": 0,
        "model": "qwen3:14b",
        "max_input_chars": 16000,
        "system_prompt": "You are an AI assistant for a construction services platform.\nFollow the user request and respond with ONLY one valid JSON object.\nDo not include markdown or explanation."
      },
      {
        "rank": 1,
        "model": "qwen3:4b",
        "max_input_chars": 200,
        "system_prompt": "You are an AI assistant for a construction services platform.\nFollow the user request and respond with ONLY one valid JSON object.\nDo not include markdown or explanation."
      },
      {
        "rank": 2,
        "model": "openai",
        "max_input_chars": 200,
        "system_prompt": "You are an AI assistant for a construction services platform.\nFollow the user request and respond with ONLY one valid JSON object.\nDo not include markdown or explanation."
      }
    ],
    "translate": [
      {
        "rank": 0,
        "model": "qwen3:14b",
        "max_input_chars": 16000,
        "system_prompt": "You are a professional translator for a construction services website. Translate the following service description from {source_locale} to {target_locale}.\nRespond with ONLY valid JSON in this exact shape:\n{\"text\":\"<translated text>\"}\nReturn the translated text with no quotes inside the value beyond normal punctuation, and no markdown or explanation."
      },
      {
        "rank": 1,
        "model": "qwen3:4b",
        "max_input_chars": 200,
        "system_prompt": "You are a professional translator for a construction services website. Translate the following service description from {source_locale} to {target_locale}.\nRespond with ONLY valid JSON in this exact shape:\n{\"text\":\"<translated text>\"}\nReturn the translated text with no quotes inside the value beyond normal punctuation, and no markdown or explanation."
      },
      {
        "rank": 2,
        "model": "openai",
        "max_input_chars": 200,
        "system_prompt": "You are a professional translator for a construction services website. Translate the following service description from {source_locale} to {target_locale}.\nRespond with ONLY valid JSON in this exact shape:\n{\"text\":\"<translated text>\"}\nReturn the translated text with no quotes inside the value beyond normal punctuation, and no markdown or explanation."
      }
    ],
    "configure-environment": [
      {
        "rank": 0,
        "model": "qwen3:14b",
        "max_input_chars": 16000,
        "system_prompt": "You are an AI assistant for a construction services platform.\nFollow the user request and respond with ONLY one valid JSON object.\nDo not include markdown or explanation."
      },
      {
        "rank": 1,
        "model": "qwen3:4b",
        "max_input_chars": 200,
        "system_prompt": "You are an AI assistant for a construction services platform.\nFollow the user request and respond with ONLY one valid JSON object.\nDo not include markdown or explanation."
      },
      {
        "rank": 2,
        "model": "openai",
        "max_input_chars": 200,
        "system_prompt": "You are an AI assistant for a construction services platform.\nFollow the user request and respond with ONLY one valid JSON object.\nDo not include markdown or explanation."
      }
    ]
  },
  "tools": [],
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
    "priority_count_medium": 3,
    "max_system_prompt_chars": 4000,
    "system_prompt_override_orgs": [],
    "loki_endpoint": ""
  }
}
```

### Capability system prompts

Every ranked binding requires a nonblank `system_prompt`; missing, null, empty, or whitespace-only values are rejected. The rank-0 prompt is active. Prompt edits are included in the configuration fingerprint and take effect when the manifest is applied, without rebuilding the gateway. No capability prompt is compiled into the application.

For translation, `{source_locale}` and `{target_locale}` placeholders are replaced with language names (for example Dutch and English). Other prompt text, including percent signs, is preserved. Authorized caller system messages replace the configured prompt verbatim and are not expanded as templates.

Result validation remains capability-specific: routing requires `capability`, intent-classification requires `intent` and `confidence`, translate requires `text`, and the other three accept a nonempty JSON object. Configure prompts to produce those shapes. `config.max_system_prompt_chars` controls caller overrides; it does not cap trusted manifest prompts.

For an OpenAI-only setup, set the `openai` endpoint's `url` to your provider's base URL and its `model` to the provider's runtime model name (or `null` if the provider selects it). Set `OPENAI_BEARER_TOKEN` in `.env`, then use these bindings:

```json
"capability_models": {
  "routing": [{"rank": 0, "model": "openai", "max_input_chars": 200, "system_prompt": "Respond with one JSON object matching this capability result schema."}],
  "intent-classification": [{"rank": 0, "model": "openai", "max_input_chars": 8000, "system_prompt": "Respond with one JSON object matching this capability result schema."}],
  "parameter-extraction": [{"rank": 0, "model": "openai", "max_input_chars": 16000, "system_prompt": "Respond with one JSON object matching this capability result schema."}],
  "personal-details-extraction": [{"rank": 0, "model": "openai", "max_input_chars": 16000, "system_prompt": "Respond with one JSON object matching this capability result schema."}],
  "translate": [{"rank": 0, "model": "openai", "max_input_chars": 16000, "system_prompt": "Respond with one JSON object matching this capability result schema."}],
  "configure-environment": [{"rank": 0, "model": "openai", "max_input_chars": 16000, "system_prompt": "Respond with one JSON object matching this capability result schema."}]
}
```

Internal request/completed/failed CloudEvent types are derived from `config.message_prefix` as `{prefix}.request`, `{prefix}.request.completed`, and `{prefix}.request.failed`.

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

| Capability | User content | Result JSON in `choices[0].message.content` |
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

Without a system message, the gateway uses the manifest-configured system prompt and validates the default result shapes in the table above.

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
| `/v1/models` | GET | Lists all six construction capability ids configured by the gateway. |
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
