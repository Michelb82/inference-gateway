#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:18080}"
CAPABILITY="${CAPABILITY:-intent-classification}"
MESSAGE="${MESSAGE:-I need my living room painted}"
ORG_ID="${ORG_ID:-}"

BODY=$(cat <<EOF
{
  "model": "${CAPABILITY}",
  "messages": [
    {"role": "user", "content": "${MESSAGE}"}
  ]
}
EOF
)

HEADERS=(-H "Content-Type: application/json")
if [[ -n "${ORG_ID}" ]]; then
  HEADERS+=(-H "X-Organisation-Id: ${ORG_ID}")
fi

echo "POST ${BASE_URL}/v1/chat/completions model=${CAPABILITY}"
curl -sS "${HEADERS[@]}" -d "${BODY}" "${BASE_URL}/v1/chat/completions"
echo
