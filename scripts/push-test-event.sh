#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:18080}"
CAPABILITY="${CAPABILITY:-intent-classification}"
MESSAGE="${MESSAGE:-I need my living room painted}"
ORG_ID="${ORG_ID:-}"
TIMEOUT_SECONDS="${TIMEOUT_SECONDS:-130}"

BODY=$(python3 - "${CAPABILITY}" "${MESSAGE}" <<'PY'
import json
import sys

print(json.dumps({
    "model": sys.argv[1],
    "messages": [{"role": "user", "content": sys.argv[2]}],
    "priority": "LOW",
}))
PY
)

HEADERS=(-H "Content-Type: application/json")
if [[ -n "${ORG_ID}" ]]; then
  HEADERS+=(-H "X-Organisation-Id: ${ORG_ID}")
fi

echo "POST ${BASE_URL}/v1/chat/completions model=${CAPABILITY} priority=LOW" >&2
curl --silent --show-error --fail-with-body \
  --connect-timeout 5 --max-time "${TIMEOUT_SECONDS}" \
  "${HEADERS[@]}" -d "${BODY}" "${BASE_URL%/}/v1/chat/completions"
echo
