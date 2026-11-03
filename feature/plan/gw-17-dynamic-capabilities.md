# GW-17: Dynamic capabilities

## Intended outcome

An operator can add, edit, or remove a capability through a valid manifest update, without Go code changes or a rebuild. A manifest containing only one capability is valid. HTTP requests, model listing, health reporting, and workers all use the active registry. A capability name does not select a hidden implementation in Go.

This builds on GW-14 (segmented validation), GW-15 (mandatory input limits), and GW-16 (manifest system prompts). Implementation is recorded in `feature/gw-17.md`. The registry now follows the package split below; runtime schema/property metadata is prepared during registration.

## Proposed manifest contract

Retain capability_models and its ranked binding lists to avoid another structural migration. Add behavior fields to each ranked entry; rank 0 remains active. Higher ranks remain validated configuration, with no automatic failover in this work.

```json
{
  "capability_models": {
    "appointment-summary": [
      {
        "rank": 0,
        "model": "openai",
        "max_input_chars": 8000,
        "system_prompt": "Summarize the appointment request. Return only a JSON object containing summary.",
        "input_field": "message",
        "output_schema": {
          "type": "object",
          "required": ["summary"],
          "properties": {
            "summary": {"type": "string", "minLength": 1}
          }
        }
      }
    ]
  }
}
```

The example is the capability_models segment; inference_endpoint, ingress, and config remain required in a complete manifest.

- Capability IDs are exact, nonblank, lowercase identifiers matching `[a-z][a-z0-9-]*`. Reject surrounding whitespace instead of silently renaming keys. No six-capability minimum; require at least one configured capability. Sort IDs for validation and listing so ordering is deterministic.
- Every ranked entry still needs an existing endpoint ID, a nonblank system_prompt, and integer max_input_chars from 200 through 500000. Preserve the existing missing-limit error verbatim.
- input_field is required and selects message or text as the internal input key. It describes transport mapping, not capability identity. The last HTTP user message remains the user input.
- output_schema is optional. Without it, require a nonempty JSON object, matching the current generic JSON contract.
- When present, output_schema declares a documented, limited JSON Schema subset: object type, required fields, properties, primitive property types, string minLength, and numeric minimum/maximum. Reject unsupported keywords or malformed schemas during manifest validation; do not silently ignore rules. No remote references or executable validators. Preserve extra result fields by default; schema validation does not silently strip them.
- Optional prompt_variables declares supported source_locale and target_locale variables as objects with required default and allowed fields. Each allowed list must be nonempty, contain unique nonblank values, and include its nonblank default. One capability supports every configured source/target pair. Invalid request selections return 400 before enqueueing and are checked again by workers after reloads; authorized prompt overrides do not bypass allowed values. These names map to the existing HTTP extras. Keep Dutch/English labels for nl/en and literal codes for other locales. Match configured codes case-sensitively after whitespace trimming. Replace only declared named placeholders. Reject legacy string-valued variables and unknown placeholders/declarations during manifest validation. Literal percent signs stay unchanged.
- Trusted manifest prompts must produce the configured output contract. Caller overrides retain the existing organisation allowlist, size cap, verbatim handling, and generic JSON parsing bypass.

For translation, explicitly declare input_field=text and prompt_variables with source_locale={default: nl, allowed: [nl, en]} and target_locale={default: en, allowed: [en, nl, pl]}. Other capabilities can use the same behavior without being named translate. Do not introduce a general scripting or template execution language.

## Implementation sequence

### 1. Define and validate configurable behavior

Update configmgmt/types.go and the segment validator. Iterate the supplied capability_models keys instead of capability.Known(). Validate every ranked entry, its input mapping, variables, output schema, and endpoint reference. Keep segment validation functions separate.

Resolve every declared capability into an immutable runtime Definition. Include all active behavior fields in the snapshot fingerprint, with canonical ordering for maps and schema objects. Prompt-only, schema-only, variable-default, input-mapping, addition, and removal changes must all trigger application. Reject invalid candidates before mutating live state.

### 2. Make the registry independent of built-in IDs

Change NewRegistryFromBindings to iterate supplied bindings. Replace package-level Known()/IsKnown() with registry/holder lookup and sorted listing. Remove the fixed six-capability constructor and move tests to explicit registry fixtures. Delete capability constants from production once no caller depends on them.

Keep one immutable definition per capability and the thread-safe holder used for reloads. Registry construction must fail on malformed definitions rather than accepting partial state.

### Package layout and validation ownership

Split internal/capability by responsibility while keeping one package and its existing public API where practical:

| File | Responsibility |
|------|----------------|
| `capability.go` | Public definition/binding types, registry lookup/listing methods, and public runtime operations such as ValidateInputBounds, BuildPrompts, and ParseResult. Keep these operations readable and delegate substantive helper work where useful. |
| `capability_registration.go` | NewRegistryFromBindings and private registry assembly: validate the complete candidate, copy mutable definition data, and construct an immutable registry before publication. |
| `capability_validation.go` | Shared validation of capability definitions: IDs, input fields, required prompts/limits, variable declarations, and output-schema declarations. Distinguish definition validation from request-input and model-result validation. |
| `holder.go` | Holder and its synchronization, snapshot replacement, and dormant behavior. Keep all locking for this holder together. |
| `override_policy.go` | Existing caller override policy and its holder; preserve this separate concern. |

If prompt rendering or result validation grows substantially, extract focused `capability_prompts.go` and `capability_results.go` helpers. Avoid creating tiny files for individual functions; the split should make ownership easier to follow.

Go exports identifiers at package level, not file level. Therefore, capability.go is the primary entry point for readers, not a mandatory wrapper for every exported function. Keep the exported constructor next to its implementation in capability_registration.go. Do not introduce forwarding wrappers, registration interfaces, init-time registration, global mutable catalogs, or extra subpackages solely to enforce a file boundary. Other packages continue importing internal/capability as before.

Keep manifest-specific checks in configmgmt/validate_capability_models.go: required segments, ranked lists, rank uniqueness/rank 0, and endpoint references. That validator calls shared definition validation in capability_validation.go, and registry construction uses the same rules for direct callers. Put shared capability constraints in the capability package so validation does not drift; configmgmt may import capability, while capability must not import configmgmt. Preserve the required missing-max_input_chars error text. Compile validated schemas/rendering metadata during candidate construction, not repeatedly for each request.

This separation is preferable to putting every kind of validation in capability_registration.go: validation is also needed by dormant management requests, which must not register or publish capabilities. Keep validation free of network calls and live-state mutation. The constructor performs validation and construction atomically; the holder publishes only a fully valid registry. All() and Get() must not expose mutable schema or variable maps owned by that registry.

Mirror these responsibilities in tests where useful: registration, definition validation, holder reload/concurrency, and runtime prompt/result tests. Verify behavior through public package operations; avoid tests that depend on which file owns a function.

### 3. Replace name-based execution with generic operations

Through the capability package public runtime operations, validate bounds against Definition.InputField, build prompts from the configured system prompt and declared variables, parse a JSON object, and apply the configured output validator. Remove all switches on capability names.

In worker.go, capture one Definition when handling a request and use it for bounds, prompts, inference, and result validation. A concurrent reload must not mix fields from two definitions. Tool execution and authorized caller overrides retain their existing separate result handling.

### 4. Connect HTTP ingress to the active registry

Pass the registry through the HTTP handler's runtime source rather than importing a static capability catalog. eventFromRequest receives the resolved definition, maps the user content to its input_field, and copies supported declared locale extras. GET /v1/models lists only active registry IDs in sorted order.

Keep existing OpenAI response envelopes, LOW default priority, queue correlation, unknown-model errors, and dormant 503 behavior. A removed name is rejected for new HTTP requests. The Redis envelope remains internal.

### 5. Specify reload and queued-request behavior

Use processing-time configuration for queued work: the worker resolves the capability when processing begins. If a capability was removed while its request waited in Redis, publish a correlated failure explaining that it is no longer configured. This prevents the HTTP waiter from hanging until timeout. Once processing starts, retain the captured definition through completion even if the manifest changes.

HTTP listing and admission use a consistent active runtime view. If removal happens between HTTP admission and worker lookup, the same correlated failure applies. Invalid manifest updates keep the previous registry and data plane. Prompt/schema changes should not restart Redis consumers. Immutable snapshots must not share mutable schema maps with callers.

### 6. Update health and management discovery

Health enumerates only configured capabilities and continues deduplicating endpoint probes. A one-capability manifest must not create phantom entries for the other five.

Manifest-support describes supported definition fields, input mappings, prompt variables, schema keywords, and mandatory bounds instead of claiming a fixed capability catalog. It must remain available while dormant. Active capabilities are discovered through GET /v1/models. Version the changed management support contract and document the migration for AI Manager consumers; do not reuse an old response version with incompatible semantics.

### 7. Migrate examples and document behavior differences

Update manifest.json.dist with explicit input_field and output contracts for its existing six examples; those names become examples rather than required built-ins. Declare translation variables explicitly. Show a minimal one-capability manifest and a brand-new custom capability, and revise the terminal debug guide to explain that its default intent-classification name must be configured.

Document migration of deployed manifests before rollout. Existing HTTP capability names and success envelopes remain usable when those names are configured. Result validation becomes schema-based: extra fields are preserved instead of the current routing/intent/translate projection, and declared numeric ranges can reject values previously accepted. Call these differences out explicitly. Capability routing output remains data; the gateway does not automatically execute a second capability.

## Verification and acceptance criteria

- Parse and apply a manifest with only appointment-summary; no built-in names are required.
- Submit an HTTP request for that name through Redis and a fake inference server, and receive its validated result.
- Add another entirely new name through manifest reload; it appears in models and health and can execute without a rebuild.
- Remove a name; models/health stop listing it, new requests fail clearly, and already queued requests receive a correlated failure if not yet started.
- Change prompts, mappings, schema, and variable defaults independently; each changes the fingerprint and active behavior. Reordering JSON maps does not change the fingerprint.
- Reject missing/blank prompts, missing limits, 199/500001 limits, noninteger limits, invalid IDs, missing rank 0, invalid endpoint references, malformed schemas, unsupported schema keywords, and unsupported prompt variables. Accept limits 200 and 500000.
- Verify generic message/text inputs, Unicode character bounds, translation defaults, arbitrary percent signs, output schema failures, generic JSON output, caller override policy, tool execution, and existing priority/correlation behavior.
- Verify one capability handles all allowed language pairs, disallowed selections never reach inference, defaults belong to allowed lists, and allowed slices are copied across snapshots.
- Test concurrent manifest reloads against HTTP and worker processing; run the race detector for these new concurrency tests.
- Verify invalid updates retain the old runtime and dormant discovery remains available.
- Verify manifest validation and direct registry construction enforce the same definition rules, invalid registration leaves the active holder untouched, and callers cannot mutate registered schemas/variables through returned definitions.
- Run make test-docker and wait for a green suite before every code commit. Add GW-17 alongside implementation commits. Live provider/Redis smoke tests are optional and separate from deterministic unit tests.

## Scope boundary

This work makes capability definitions dynamic. It does not add ranked provider failover, automatic routing chains, arbitrary scripts, new inference vendors, or per-request generation of trusted system prompts. These need separate features.
