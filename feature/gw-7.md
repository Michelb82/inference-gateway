# GW-7: Log configuration and manifest sources at startup

## Problem

Startup does not identify configuration and manifest sources together before attempting to load a local manifest.

## Impact

- Operators cannot easily see where startup configuration comes from, especially when manifest loading fails.
- A local bootstrap manifest can obscure that remote polling remains enabled.

## Shorthand solution

Emit an INFO JSON record to stdout before manifest bootstrap identifying process environment with built-in defaults, the optional local manifest path, the remote polling URL, and polling interval. Report sources only, without dumping configuration or the bearer token. Document that Docker Compose supplies `.env` values to the process environment.

## Related code

- `cmd/gateway/main.go`
- `README.md`
