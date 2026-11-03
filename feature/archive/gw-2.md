# GW-2: Align capability domain with the manifest control plane

## Problem

`configmgmt` validates and resolves models/ingress, but prompts, I/O shapes, parsers, and the allowed capability set live in Go (`capability.go`) and are duplicated in `configmgmt` validation. Ranked fallbacks are accepted then ignored (rank 0 only).

The control plane cannot evolve capabilities without shipping gateway code. The manifest looks declarative but is effectively a bindings/ops document.

## Impact

- Split / duplicated source of truth for capability names
- Manifest cannot add capabilities, schemas, or failover without code changes
- Misleading domain separation between control plane and data plane

## Shorthand solution

Make one package the source of truth for “known capabilities,” and either:

1. Keep the manifest as a bindings-only ops document and stop advertising it as capability declaration, or
2. Move prompts/schemas into versioned capability definitions the manifest can reference

Do not keep three parallel capability lists.

## Status

Implemented option 1 (bindings-only):

- `capability.Known()` / `IsKnown` / `DefaultMaxInputChars` are the sole known-capability list
- `configmgmt` Validate/Resolve consume that list; `requiredCapabilities` removed
- Manifest no longer has a `capabilities` array; `capability_models` must bind exactly the built-in ids (unknown keys rejected)
- Legacy `capabilities` JSON is ignored on parse
- Ranked fallbacks remain validated but only rank 0 is applied (unchanged; documented as reserved)

Option 2 (versioned capability defs in the control plane) is deferred to AI Gateway Manager work.

## Related code

- `internal/capability/capability.go` (prompts, `Known`, `BuildPrompts`, `ParseResult`)
- `internal/configmgmt/validate.go` / `resolve.go` (bindings for `capability.Known()`, rank-0 only)
- `manifest.json.dist`
- `docs/future-architecture.md`
- `README.md`
