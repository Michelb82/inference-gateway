# Construction AI Gateway

Standalone Go service that exposes AI capabilities over an OpenAI-compatible HTTP API and keeps Ollama model details inside the gateway.

## Architecture

- External ingestion: OpenAI Chat Completions over HTTP (`POST /v1/chat/completions`, `GET /v1/models`)
- Internal work queue (Redis): `queue:ai.requests` plus priority lanes `queue:ai.requests:critical|high|medium|low`
- Internal responses: `queue:ai.responses` and per-request `queue:ai.responses:{id}`
- Built-in capabilities (gateway code): `routing`, `intent-classification`, `translate`
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
   │                                              Worker + Ollama
   │                                                      │
   ◄──────── wait on queue:ai.responses:{id} ◄────────────┘
   │
   ▼
OpenAI chat.completion
```

The HTTP call is synchronous: the handler enqueues an internal CloudEvent, the worker processes it with priority fairness, and the handler waits for the correlated response. The producer contract is [docs/openai-http-ingestion.md](docs/openai-http-ingestion.md).

Optional extra field `priority` on each request selects a processing lane: `CRITICAL`, `HIGH`, `MEDIUM`, or `LOW` (case-insensitive). Missing or invalid values default to `LOW`. The gateway demuxes the internal input list into Redis lanes and schedules work with fairness counters (see Configuration).

### Configuration management

Runtime configuration (models, capability→model bindings, input character limits, Redis **internal** queue, CloudEvent prefix, priority fairness) comes from a **manifest**, not from `.env`. The manifest is an ops/bindings document: capability prompts, I/O shapes, and parsers live in gateway code (`internal/capability`). `capability_models` must bind exactly the built-in capability ids.

- **Primary path:** poll `MANIFEST_URL` every `MANIFEST_POLLING_INTERVAL`. Until a valid manifest is applied, the gateway stays **dormant** (process up, health reports `dormant`, OpenAI endpoints return 503, no Redis consumer / capability work).
- **Optional local file:** `--manifest /path/to/manifest.json` is for development, tests, or experimental setups only. It is **not** required to boot. If the flag is set, the file must be valid or startup exits.
- **Soft failure:** if the manager is unavailable or returns an invalid document, the gateway keeps the current snapshot (or remains dormant). A failed poll never clears a working configuration.
- On a successful apply, the gateway activates (or updates) the data plane using **rank 0** model bindings from `capability_models`. Ranked fallbacks are reserved for later.

Compose for local/dev mounts `manifest.json` and passes `--manifest` so the stack works without AI Manager. Production-oriented runs can omit that flag and wait for the manager.

For planned control-plane / data-plane separation (Ingress, Orchestration, Policy, Service Manager), see [docs/future-architecture.md](docs/future-architecture.md). That document also describes how this gateway’s OpenAI HTTP producer contract relates to those later layers. Redis remains an internal work queue, not a future producer adapter.

## Prerequisites

1. Start the sibling construction stack so Redis and Ollama (`llm-model` / `foundation-model`) are available on the shared Docker network.

2. Ensure the `dev` network exists:

```bash
docker network ls | grep dev
```

3. For local/dev, provide a manifest (see below). On apply the gateway checks each capability’s LLM URL and pulls any missing configured models. **Redis must be reachable when a manifest is applied** (apply fails and the previous snapshot is kept, or the gateway stays dormant). Models are best-effort: if at least one configured model is available on its LLM, apply continues and missing models are logged as warnings; apply fails only when none are available.

## Run locally in Docker

```bash
cp .env.dist .env
cp manifest.json.dist manifest.json
# Edit .env / manifest.json for private hostnames or CloudEvent prefixes.
docker compose up --build
```

Health and OpenAI endpoints are published on host port `18080` by default (`http://localhost:18080/health`, `/health.json`, `/v1/models`, `/v1/chat/completions`).

## Configuration

### Bootstrap (`.env`)

Copy `.env.dist` to `.env`. These variables only configure how manifests are discovered:

| Variable | Default |
|----------|---------|
| `MANIFEST_URL` | `http://ai-manager:80/manifest.json` |
| `MANIFEST_POLLING_INTERVAL` | `5m` |
| `DEBUG` | `false` |

When `DEBUG=true`, the logger also emits Debug-level full Redis and Ollama payloads (`incoming/outgoing traffic payload`, `ollama incoming` / `ollama outgoing`) and appends the same JSON logs to `debug.log`.

### Manifest (`manifest.json`)

Copy `manifest.json.dist` to `manifest.json` for local/dev. Public defaults use placeholder names (`llm-model`, `com.mywebsite.ai`). Local private copies may use deploy-specific hostnames (for example `foundation-model`) and CloudEvent prefixes.

| Field | Purpose |
|-------|---------|
| `models` | Catalog entries with `id`, `url`, `model` (Ollama name), `keep_alive_seconds` |
| `capability_models` | Bindings for each built-in capability (`routing`, `intent-classification`, `translate`): ranked list of model `id`s (rank `0` is used today; higher ranks are reserved for failover). Optional `max_input_chars` on rank `0` (defaults: routing `200`, intent-classification `8000`, translate `16000`). Unknown capability keys are rejected. |
| `ingress` | Internal Redis work queue: adapter, address, ingress/egress channels, BRPOP timeout. Not the producer contract. |
| `config` | Internal CloudEvent `message_prefix`, `http_address`, priority fairness counts, optional `max_system_prompt_chars` (default `4000`), optional `system_prompt_override_orgs` (default empty / deny all) |

Example (trimmed):

```json
{
  "models": [
    {
      "id": "qwen3:1.7b",
      "url": "http://llm-model:11434",
      "model": "qwen3:1.7b-q4_K_M",
      "keep_alive_seconds": 300
    }
  ],
  "capability_models": {
    "routing": [
      {"rank": 0, "model": "qwen3:1.7b", "max_input_chars": 200},
      {"rank": 1, "model": "qwen3:4b"}
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
    "message_prefix": "com.mywebsite.ai",
    "http_address": ":80",
    "priority_count_high": 3,
    "priority_count_medium": 3,
    "max_system_prompt_chars": 4000,
    "system_prompt_override_orgs": []
  }
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

1. The built-in gateway system prompts are **not** used.
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

Without a system message, the gateway uses its default system prompts and validates the default result shapes in the table above.

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
| `/v1/models` | GET | Lists capability ids (`routing`, `intent-classification`, `translate`) |
| `/health` | GET | Per-capability readiness page (HTML) |
| `/health.json` | GET | Same data for probes/automation (JSON) |

While dormant (no manifest), `/v1/*` returns an OpenAI-shaped **503**. Health reports `dormant` with HTTP **503**.

Status values:

- Overall: `ready` / `not_ready` / `dormant` (awaiting manifest)
- Per capability: `available` / `unavailable` (omitted while dormant)

HTTP status is `200` when ready and `503` when not ready or dormant. Model names appear only on health endpoints (ops), not on successful OpenAI completions.

## Manual smoke test

With the gateway running and a manifest applied:

```bash
chmod +x scripts/push-test-event.sh
./scripts/push-test-event.sh
CAPABILITY=routing ./scripts/push-test-event.sh
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
