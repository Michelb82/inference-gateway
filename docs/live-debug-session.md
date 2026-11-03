# Live debug session

Fire a LOW-priority `intent-classification` request from your terminal and wait for the result or error. Redis is assumed to be running. The gateway must be running with a valid manifest applied and its configured inference model available. The helper uses the HTTP endpoint, which enqueues the request into Redis, processes it through the priority scheduler and worker, and waits for the matching response.

Requires Bash, Python 3 (JSON encoding), and curl 7.76+ (`--fail-with-body`) on your host. Run these commands from the repository root.

In one terminal, follow the running gateway's logs:

```bash
docker compose logs --follow --tail=50 ai-gateway
```

In another terminal, check readiness and send a request:

```bash
curl --silent --show-error --fail-with-body http://localhost:18080/health.json
bash scripts/push-test-event.sh
```

The default prompt is `I need my living room painted`. To supply your own prompt:

```bash
MESSAGE='Please paint the "living room" walls' bash scripts/push-test-event.sh
```

The helper prints the endpoint, capability, and `priority=LOW` to stderr. On success, stdout contains the full OpenAI completion; `choices[0].message.content` contains the JSON result as a string, for example `{"intent":"wall-painting","confidence":0.95}`. Actual classification values depend on the model. The completion `id` corresponds to the `request_id` in gateway logs.

To print only the decoded result (Bash):

```bash
set -o pipefail
bash scripts/push-test-event.sh | python3 -c 'import json,sys; print(json.dumps(json.loads(json.load(sys.stdin)["choices"][0]["message"]["content"]), indent=2))'
```

On HTTP failure the helper prints the gateway error JSON and exits nonzero. For example, a dormant gateway returns 503 with `error.message` set to `gateway is dormant`. Inference or parsing failures return an error response; a queue wait timeout returns 504. Connection failures and client timeouts produce a curl error and a nonzero exit status. LOW-priority requests can wait behind other traffic; a timeout does not remove already queued work.

Optional environment variables:

| Variable | Default | Purpose |
|----------|---------|---------|
| `MESSAGE` | `I need my living room painted` | User prompt |
| `BASE_URL` | `http://localhost:18080` | Gateway URL, without `/v1` |
| `CAPABILITY` | `intent-classification` | Capability to exercise |
| `ORG_ID` | empty | Optional `X-Organisation-Id` header |
| `TIMEOUT_SECONDS` | `130` | Total curl timeout; gateway response wait is 120 seconds |

Priority is always explicitly `LOW` for this helper. No Redis keys need to be read or modified manually.

For full Redis and Ollama/OpenAI payload logs, enable `DEBUG=true` in `.env` and recreate the gateway with `docker compose up -d --force-recreate ai-gateway`. Payloads also appear in `debug.log`; they include prompt and model content.
