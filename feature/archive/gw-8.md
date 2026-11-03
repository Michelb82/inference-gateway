# GW-8: Use Gitea CI for Docker Hub publishing

## Problem

CI still uses GitHub-specific workflow paths, context, and release bot identity although the repository is hosted on Gitea.

## Impact

- Hosting configuration is misleading and mixes GitHub conventions with Gitea usage.
- Docker Hub publishing must retain both versioned images and the latest tag.

## Shorthand solution

Move CI and its version helper to `.gitea`, use Gitea context and step outputs, and replace the release bot identity. Preserve the test dependency, main-branch push condition, version calculation, git release tags, and Docker Hub credentials and image tags.

## Related code

- `.gitea/workflows/ci.yml`
- `.gitea/scripts/bump-version.sh`
