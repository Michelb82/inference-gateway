#!/usr/bin/env bash
set -euo pipefail

# Computes the next image tag from existing git tags.
# - First release: 0.1
# - Each push: increment minor (0.1 -> 0.2)
# - Commit message contains MAJOR_BUMP: increment major, reset minor (0.5 -> 1.0)

COMMIT_MSG="${COMMIT_MESSAGE:-$(git log -1 --pretty=%B)}"
LATEST_TAG=$(git tag -l 'v[0-9]*.[0-9]*' --sort=-v:refname | head -n1 || true)

if [ -z "$LATEST_TAG" ]; then
  VERSION="0.1"
else
  VERSION="${LATEST_TAG#v}"
  MAJOR="${VERSION%%.*}"
  MINOR="${VERSION#*.}"

  if echo "$COMMIT_MSG" | grep -qF 'MAJOR_BUMP'; then
    MAJOR=$((MAJOR + 1))
    MINOR=0
  else
    MINOR=$((MINOR + 1))
  fi

  VERSION="${MAJOR}.${MINOR}"
fi

echo "Next version: ${VERSION}"
echo "version=${VERSION}" >> "${GITEA_OUTPUT}"
