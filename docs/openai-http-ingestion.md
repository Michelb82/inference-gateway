# Inference Gateway — OpenAI HTTP client contract

This is the producer contract for calling the Inference Gateway. Redis CloudEvents are an internal work queue. Callers must not `LPUSH` or `BRPOP` gateway keys.

Use any OpenAI-compatible Chat Completions client pointed at the gateway base URL.

## Base URL

| Environment | Base URL |
|-------------|----------|
| Docker Compose (`dev` network) | `http://ai-gateway:80` |
| Host (published port) | `http://localhost:18080` |

There is no authentication in this version.

Until a manifest is applied the gateway is **dormant**. Completions and model listing return an OpenAI-shaped **503**.

## List capabilities

`GET /v1/models`

`id` is a **capability**, not an Ollama model name:

- `routing`
- `intent-classification`
- `translate`

Example:

```json
{
  "object": "list",
  "data": [
    {"id": "routing", "object": "model", "created": 0, "owned_by": "inference-gateway"},
    {"id": "intent-classification", "object": "model", "created": 0, "owned_by": "inference-gateway"},
    {"id": "translate", "object": "model", "created": 0, "owned_by": "inference-gateway"}
  ]
}
```

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
| `model` | yes | Capability id (`routing`, `intent-classification`, `translate`) |
| `messages` | yes | Chat messages. The **last** `user` content is the capability input. The **first** `system` content is a system-prompt override (default deny). |
| `stream` | no | Must be omitted or `false`. `true` is rejected (parsers need the full JSON). |
| `priority` | no | Extra field: `CRITICAL`, `HIGH`, `MEDIUM`, or `LOW` (default `LOW`). Official SDKs: `extra_body`. |
| `source_locale` | no | Translate extra; default `nl`. |
| `target_locale` | no | Translate extra; default `en`. |

Header `X-Organisation-Id` is the organisation for system-prompt override policy.

### Input mapping

| Capability | User content becomes | Other extras |
|------------|----------------------|--------------|
| `routing` | `message` | |
| `intent-classification` | `message` | |
| `translate` | `text` | `source_locale`, `target_locale` |

### Success

HTTP **200**. `choices[0].message.content` is a **JSON string** of the capability result. `model` is the capability id (not the Ollama name). `id` is the gateway request id (`chatcmpl-...`).

| Capability | `content` JSON |
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

When authorized, the gateway does not use built-in prompts and does not enforce the default result shapes. `content` is still a JSON string of the model’s JSON object.

## Priority and concurrency

`priority` selects an **internal** Redis lane. The HTTP call is synchronous: the connection waits until the worker publishes a matching response.

Concurrent HTTP requests serialize through a single worker via those lanes (CRITICAL first, then fairness among HIGH/MEDIUM/LOW). The Ollama chat timeout is 120s; the gateway waits up to the same bound.

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
