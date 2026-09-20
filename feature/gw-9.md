# GW-9: Manifest-defined HTTP tools

## Problem

The gateway cannot execute a manifest-approved HTTP tool after an LLM formats a request, or let the LLM decide whether a failed tool call should be retried.

## Impact

- Callers cannot request simple agent-style execution through the gateway.
- Tool endpoints and instructions need a trusted, centrally managed source.
- Intermediate inference and tool failures must not leak onto the egress channel.

## Shorthand solution

Define named HTTP tools in the manifest, accept a tool name on an inference request, add the manifest instruction to the capability system prompt, execute the selected endpoint, and publish only the final tool result. On HTTP 4xx/5xx, give the response to the LLM and honor a structured retry/stop decision, with at most three retries.

## Related code

- `internal/configmgmt`
- `internal/openai`
- `internal/worker`
- `internal/tool`
- `manifest.json.dist`
