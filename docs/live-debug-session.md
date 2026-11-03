# Live debug session

Fire a LOW-priority `intent-classification` request from your terminal and wait for the result or error. Redis is assumed to be running. The gateway must be running with a valid manifest applied and its configured inference model available. The helper uses the HTTP endpoint, which enqueues the request into Redis, processes it through the priority scheduler and worker, and waits for the matching response.

Requires Bash, Python 3 (JSON encoding), and curl 7.76+ (`--fail-with-body`) on your host. Run these commands from the repository root.

Before the session, check the manifest syntax and run this checkout's gateway image:

```bash
python3 -m json.tool manifest.json >/dev/null
docker compose up -d --build ai-gateway
```

For an OpenAI-compatible provider, the local manifest must declare an `inference_endpoint` with `vendor: "openai"`, its base URL, and the upstream runtime model. The capability's rank-0 `model` must reference that endpoint's ID. Put the key in `.env` as `OPENAI_BEARER_TOKEN`; the gateway supplies the upstream Authorization header. The helper does not need your key. The distribution sample uses Ollama at rank 0, so configure its OpenAI placeholder and rank-0 bindings before using it to test a key. Each ranked entry requires a prompt, `max_input_chars`, and `input_field`; declare locale variables and output schemas where needed.

JSON syntax checking is separate from gateway contract validation. Against a running gateway from this checkout, validate without applying the candidate or making provider calls:

```bash
curl --silent --show-error --fail-with-body \
  -H 'Content-Type: application/json' \
  --data-binary @manifest.json \
  http://localhost:18080/management/v1/manifests/validate
```

Expect `valid: true` and `contract_version: "2"`. An invalid document returns 422 with an error. If the local manifest prevents startup, inspect `docker compose logs --tail=50 ai-gateway`, fix the reported error, and start it again. After changing `.env`, recreate the gateway so it reads the new key. These commands build the current source; a published `latest` image does not automatically include unpushed local changes.

In one terminal, follow the running gateway's logs:

```bash
docker compose logs --follow --tail=50 ai-gateway
```

In another terminal, check readiness and send a request:

```bash
curl --silent --show-error --fail-with-body http://localhost:18080/health.json
bash scripts/push-test-event.sh
```

The active manifest must configure `intent-classification` for the default command. With a custom-only manifest, use `CAPABILITY=appointment-summary bash scripts/push-test-event.sh`. Use `GET /v1/models` to discover active IDs.

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

From the construction container, use the shared-network address instead of the host port. This request exercises the gateway's configured key and the full Redis flow:

```bash
curl --silent --show-error --fail-with-body --max-time 130 \
  -H 'Content-Type: application/json' \
  -d '{"model":"intent-classification","messages":[{"role":"user","content":"Can I change my appointment to next Tuesday?"}],"priority":"LOW"}' \
  http://ai-gateway/v1/chat/completions
```

Only a request whose rank-0 endpoint uses vendor `openai` tests the provider key. A successful completion confirms that path; 401/403 from the provider appear in the gateway error/logs, while queue, model-output, and timeout errors have different causes. Health reports cached endpoint probes, so an actual completion is the decisive live test.

To select another allowed language pair on the same capability:

```bash
curl --silent --show-error --fail-with-body --max-time 130 \
  -H 'Content-Type: application/json' \
  -d '{"model":"translate","messages":[{"role":"user","content":"Good morning"}],"source_locale":"en","target_locale":"pl","priority":"LOW"}' \
  http://localhost:18080/v1/chat/completions
```

The updated sample and local manifests default to Dutch → English, allow Dutch/English sources, and allow English/Dutch/Polish targets. No separate capability is needed for each pair. Omitting locale extras (as the shell helper does) uses the defaults. An unsupported selection returns 400 with `invalid_prompt_variable`; edit the manifest's `allowed` lists to enable other languages.
