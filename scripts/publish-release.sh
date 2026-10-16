#!/usr/bin/env bash
# Called only after packaging, checksums and binary smoke tests pass.
set -euo pipefail
: "${GH_REPO:?}" "${DODU_RELEASE_TAG:?}" "${DODU_RELEASE_CHANNEL:?}"
sha=$(git rev-parse HEAD)
tag=$DODU_RELEASE_TAG

if [[ "$DODU_RELEASE_CHANNEL" == nightly && "$tag" == nightly ]]; then
  # Build before replacing anything. Release-wide concurrency serializes writers.
  if gh release view nightly --json id >/dev/null 2>&1; then
    gh release delete nightly --yes
  fi
  if gh api "repos/$GH_REPO/git/ref/tags/nightly" >/dev/null 2>&1; then
    gh api --method PATCH "repos/$GH_REPO/git/refs/tags/nightly" -f sha="$sha" -F force=true >/dev/null
  else
    gh api --method POST "repos/$GH_REPO/git/refs" -f ref=refs/tags/nightly -f sha="$sha" >/dev/null
  fi
  title="Nightly $(date -u +%Y-%m-%d) (${sha:0:12})"
  flags=(--prerelease --latest=false)
elif [[ "$DODU_RELEASE_CHANNEL" == stable && "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  # Published stable releases are immutable; retrying cannot replace their assets.
  if gh release view "$tag" --json isDraft >/dev/null 2>&1; then
    if [[ "$(gh release view "$tag" --json isDraft --jq .isDraft)" != true ]]; then
      echo "Release $tag is already published; use a new version." >&2
      exit 1
    fi
    gh release delete "$tag" --yes
  fi
  title="dodu $tag"
  flags=(--latest)
else
  echo 'Invalid release channel/tag' >&2
  exit 1
fi

gh release create "$tag" --verify-tag --target "$sha" --draft \
  --title "$title" --notes-file release-notes.md "${flags[@]}" \
  dist/*.tar.gz dist/checksums.txt dist/build-info.json
gh release edit "$tag" --draft=false "${flags[@]}"
gh release view "$tag" --json url,tagName,isPrerelease,assets
