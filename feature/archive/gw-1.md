# GW-1: Harden the request-path trust boundary

## Problem

Callers can send `data.input.system_prompt`, which replaces the gateway’s built-in prompts and bypasses result schema validation. Input size limits only cover `message` / `text`, not the system prompt.

Any Redis producer can hijack model behavior, inflate cost, and push unconstrained prompts.

## Impact

- Prompt injection / behavior takeover
- Schema bypass on results (`ParseRawJSON` instead of capability schemas)
- Cost / DoS via unbounded system prompts

## Shorthand solution

Treat the queue as untrusted:

- Remove or strictly gate `system_prompt` (feature flag / org allowlist)
- Always apply a character cap to system prompts
- Keep capability schema validation unless the override is explicitly authorized

## Status

Implemented with **org allowlist** (default deny):

- Manifest `config.system_prompt_override_orgs` — empty ⇒ no overrides
- Manifest `config.max_system_prompt_chars` — default `4000` when omitted/`<= 0`
- Unauthorized / missing `organisation_id` ⇒ request failed
- Authorized overrides only ⇒ `ParseRawJSON`; otherwise always `ParseResult`

## Related code

- `internal/capability/capability.go` (`BuildPrompts`, `ValidateInputBounds`)
- `internal/capability/override_policy.go`
- `internal/worker/worker.go` (allowlist + cap → optional `ParseRawJSON`)
- `internal/configmgmt` (`RuntimeConfig` / `Snapshot` policy fields)
