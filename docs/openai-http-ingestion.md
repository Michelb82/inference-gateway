# Inference Gateway — OpenAI HTTP client contract

This is the producer contract for calling the Inference Gateway. Redis CloudEvents are an internal work queue. Callers must not `LPUSH` or `BRPOP` gateway keys.

Use any OpenAI-compatible Chat Completions client pointed at the gateway base URL.

## Base URL

| Environment | Base URL |
|-------------|----------|
| Docker Compose (`dev` network) | `http://ai-gateway:80` |
| Host (published port) | `http://localhost:18080` |

There is no client authentication in this version. For upstream OpenAI-compatible providers, the gateway reads `OPENAI_BEARER_TOKEN` from its environment and adds the provider Authorization header. Clients calling the gateway do not need that provider key.

Until a manifest is applied the gateway is **dormant**. Completions and model listing return an OpenAI-shaped **503**.

## List capabilities

`GET /v1/models`

`id` is a capability ID from the active manifest. IDs are sorted; they can be added or removed by a valid manifest update. The distribution sample defines:

- `routing`
- `intent-classification`
- `parameter-extraction`
- `personal-details-extraction`
- `translate`
- `configure-environment`

Example:

```json
{
  "object": "list",
  "data": [
    {"id": "routing", "object": "model", "created": 0, "owned_by": "inference-gateway"},
    {"id": "intent-classification", "object": "model", "created": 0, "owned_by": "inference-gateway"},
    {"id": "parameter-extraction", "object": "model", "created": 0, "owned_by": "inference-gateway"},
    {"id": "personal-details-extraction", "object": "model", "created": 0, "owned_by": "inference-gateway"},
    {"id": "translate", "object": "model", "created": 0, "owned_by": "inference-gateway"},
    {"id": "configure-environment", "object": "model", "created": 0, "owned_by": "inference-gateway"}
  ]
}
```

These six names are examples, not mandatory built-ins. A manifest containing only a custom ID such as `appointment-summary` is valid. Every ranked binding requires `input_field` (`message` or `text`), integer `max_input_chars` from `200` through `500000`, and a nonblank `system_prompt`. Optional `prompt_variables` declares locale defaults and allowed lists and optional `output_schema` validates model output. See [manifest configuration](../README.md#manifest-manifestjson) for the contract and migration notes.

Real LLM names appear only on `/health` and `/health.json` (ops).

## Create a completion

`POST /v1/chat/completions`

`Content-Type: application/json`

`model` **is** the capability id. Unknown values (including real Ollama names) are rejected; they are never forwarded to the runtime.

### Request

```json
{
  "model": "intent-classification",
  "messages": [
    {"role": "user", "content": "I need my living room painted"}
  ]
}
```

| Field | Required | Meaning |
|-------|----------|---------|
| `model` | yes | An ID configured in the active manifest |
| `messages` | yes | Chat messages. The **last** `user` content is the capability input. The **first** `system` content is a system-prompt override (default deny). |
| `stream` | no | Must be omitted or `false`. `true` is rejected (parsers need the full JSON). |
| `priority` | no | Extra field: `CRITICAL`, `HIGH`, `MEDIUM`, or `LOW` (default `LOW`). Official SDKs: `extra_body`. |
| `source_locale` | no | Locale extra, forwarded only when declared in prompt_variables; defaults to the manifest default and must be in its allowed list. |
| `target_locale` | no | Locale extra, forwarded only when declared in prompt_variables; defaults to the manifest default and must be in its allowed list. |

Header `X-Organisation-Id` is the organisation for system-prompt override policy.

### Input mapping

The manifest's `input_field` selects whether the last user message becomes internal `message` or `text`. This does not depend on the capability name. Declared `source_locale` and `target_locale` extras are forwarded to prompt rendering; undeclared extras are ignored. One capability supports every pair permitted by its source and target allowed lists. Omitted or blank locale extras use their configured defaults. Matching is case-sensitive after trimming whitespace. Invalid selections return 400 with `error.code` set to `invalid_prompt_variable` and `error.param` set to `source_locale` or `target_locale`; they are rejected before enqueueing. The worker also rechecks selected values after manifest reloads, including for authorized caller prompt overrides. Existing names keep their previous mapping when using the updated distribution sample.

Example using the same capability with English as source and Polish as target (both must be allowed in the manifest):

```json
{
  "model": "translate",
  "messages": [{"role": "user", "content": "Good morning"}],
  "source_locale": "en",
  "target_locale": "pl",
  "priority": "LOW"
}
```

### Success

HTTP **200**. `choices[0].message.content` is a **JSON string** of the capability result. `model` is the capability id (not the Ollama name). `id` is the gateway request id (`chatcmpl-...`).

| Example capability in sample | `content` JSON |
|------------|----------------|
| `routing` | `{"capability":"<next-capability>"}` |
| `intent-classification` | `{"intent":"...","confidence":0.0}` |
| `translate` | `{"text":"..."}` |

```json
{
  "id": "chatcmpl-...",
  "object": "chat.completion",
  "created": 1710000000,
  "model": "intent-classification",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": "{\"intent\":\"wall-painting\",\"confidence\":0.95}"
      },
      "finish_reason": "stop"
    }
  ]
}
```

Output is always a nonempty JSON object. Its fields are governed by the configured output_schema; absent a schema, any nonempty object is accepted. Extra properties are preserved. The shapes above are sample configuration, not hard-coded parsers.

### Errors

HTTP 4xx/5xx with:

```json
{
  "error": {
    "message": "...",
    "type": "invalid_request_error",
    "param": "model",
    "code": "unknown_model"
  }
}
```

Typical statuses: **400** invalid body / unknown model / input bounds / `stream: true`; **403** system-prompt override denied; **503** dormant or bound model unavailable; **504** wait timeout; **500** inference or parse failure.

`param` and `code` may be omitted.

## System-prompt override

Optional first `messages` entry with `role: "system"`. Denied by default.

Accepted only when:

1. `X-Organisation-Id` is listed in manifest `config.system_prompt_override_orgs`, and
2. The override length is within `config.max_system_prompt_chars` (default `4000` runes).

When authorized, the gateway does not use the manifest-configured prompt and does not enforce the default result shapes. `content` is still a JSON string of the model’s JSON object.

## Priority and concurrency

`priority` selects an **internal** Redis lane. The HTTP call is synchronous: the connection waits until the worker publishes a matching response.

Concurrent HTTP requests serialize through a single worker via those lanes (CRITICAL first, then fairness among HIGH/MEDIUM/LOW). The Ollama chat timeout is 120s; the gateway waits up to the same bound.

Requests waiting in Redis use the definition active when worker processing begins. Removing their capability produces a correlated error. Requests already processing keep their captured definition. New requests for removed names return 400 with `unknown_model`.

## SDK notes

Python (`openai` SDK), extra fields via `extra_body`:

```python
from openai import OpenAI

client = OpenAI(base_url="http://localhost:18080/v1", api_key="not-used")

completion = client.chat.completions.create(
    model="translate",
    messages=[{"role": "user", "content": "Elektrische installaties voor woningen"}],
    extra_body={"source_locale": "nl", "target_locale": "en", "priority": "HIGH"},
    extra_headers={"X-Organisation-Id": "7"},
)
result = json.loads(completion.choices[0].message.content)
```

Do not send real LLM model names. Do not set `stream=True`.
