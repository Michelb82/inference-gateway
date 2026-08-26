# AGENTS.md

Instructions for AI agents working in this repository (Cursor and similar tools).

## Tests before commit

Before **every** commit that changes code (Go sources, manifests used by tests, build/test wiring):

1. **Run the unit test suite** and wait for it to finish:
   - Prefer `make test-docker` (no local Go required), or `make test` if Go is available.
2. **All tests must pass** before creating the commit.
3. **On failure:** do **not** commit. Fix the failing tests (or the code under test), re-run the suite, and only commit once green.
4. Never use `--no-verify` or otherwise skip hooks/tests to land a red suite.
5. Docs-only commits that touch no executable code may skip the suite; if unsure, run the tests.

Failed tests block the next commit until they are fixed.

A git pre-commit hook lives in [`.githooks/pre-commit`](.githooks/pre-commit) and runs `make test-docker`. Enable it in a fresh clone with:

```bash
cp .githooks/pre-commit .git/hooks/pre-commit
```

## Feature tracking (`feature/gw-*.md`)

On **every feature request** (new capability, enhancement, refactor with user-visible/architectural impact, or hardening follow-up):

1. **Create** a new file `feature/gw-<N>.md` before or with the implementation.
2. Choose `<N>` as the next unused integer after existing `feature/gw-*.md` files (currently through `gw-6`).
3. **Include that file in the same commit(s)** as the feature work. Do not ship code without the matching `feature/gw-*.md`.
4. Do not skip this for “small” features. Only omit it for pure typo/docs-only edits that do not introduce a feature.

### Document shape

Use the same structure as existing entries:

```markdown
# GW-<N>: <short title>

## Problem

<what is wrong or missing>

## Impact

- <bullet risks / user impact>

## Shorthand solution

<concise intended approach>

## Related code

- `<paths>`
```

If the work implements an existing `feature/gw-*.md`, update that file’s status or notes in the same commit rather than inventing a duplicate number.

### Commit checklist

Before committing feature work, verify:

- [ ] Unit tests were run and passed (`make test-docker` or `make test`)
- [ ] `feature/gw-<N>.md` exists (new or intentionally updated)
- [ ] It is staged with the feature changes
- [ ] The commit message reflects the feature; the gw file rides along in that commit
