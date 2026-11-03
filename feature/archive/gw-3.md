# GW-3: Make data-plane lifecycle resilient and transactional

## Problem

Two related failure modes:

1. One bad Redis payload makes `Consume` error and the worker exits; it is not auto-restarted.
2. `Apply` can swap the registry (and Redis) before `EnsureModels` succeeds; on failure the manager keeps the old snapshot while the live data plane has already moved.

## Impact

- Single poisoned queue message can stop all consumption until process restart
- Control-plane “current snapshot” can disagree with the live registry / Redis client
- Health and workers may observe partial / inconsistent apply state

## Shorthand solution

- Skip malformed queue messages (log + continue); dead-letter queue deferred to a follow-up
- Auto-recover / restart the worker on unexpected exit via `worker.Supervise`
- Make apply transactional: prepare (new Redis ping) → `EnsureModels` → atomic swap (`ConfigureTypes` / registry / Redis / supervised worker); on EnsureModels failure close unused client and leave live plane untouched

## Status

Implemented: poison skip, supervised restart (including panic recovery), prepare-then-swap Apply
(EnsureModels before live mutation; stop old worker before registry/types/Redis swap).

Git pre-commit hook (`.githooks/pre-commit`, install via `make install-hooks`) runs `make test-docker`.

## Related code

- `internal/queue/redis.go` (parse errors soft-skipped in `routePayload` / `popLane`)
- `internal/worker/supervise.go` (`Supervise` restart loop with panic recovery)
- `internal/worker/worker.go` (`Run` exits on consume error; supervisor recovers)
- `cmd/gateway/main.go` (`dataPlane.Apply`: EnsureModels before live swap; stop before Store)
- `cmd/gateway/dataplane_test.go` (EnsureModels failure leaves live plane untouched)
- `internal/configmgmt/manager.go` (keeps previous snapshot when `onApply` fails)
- `.githooks/pre-commit` (runs unit tests before commit)
