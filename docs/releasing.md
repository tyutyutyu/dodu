# Releases

Dodu uses [Changie](https://changie.dev/) change fragments and GoReleaser packaging.
Neither tool is a runtime dependency. Published binaries do not need Go.

## Contribute a change

```sh
task change
# Or non-interactively:
task change -- --kind Fixed --body "Describe the user-visible fix."
```

Commit the generated `.changes/unreleased/*.yaml` alongside the change.
CI validates fragments and checks that VERSION and CHANGELOG match the batched
notes. Ordinary development can have pending fragments.

## Prepare a stable release

```sh
task release:prepare -- minor     # patch, minor, or major
```

This batches fragments into `.changes/vX.Y.Z.md`, retains source fragments under
`.changes/releases/vX.Y.Z/`, regenerates CHANGELOG.md, and updates VERSION together.
Review and merge these changes through a PR. Then tag the merged commit:

```sh
git switch main
git pull --ff-only
git tag -a "v$(cat VERSION)" -m "Release v$(cat VERSION)"
git push origin "v$(cat VERSION)"
```

The tag push runs **Release**. It checks tag/commit/version agreement and rejects
unbatched fragments. Tests and packaged Linux smoke tests must pass before any
publication. Four archives, `checksums.txt`, and `build-info.json` are uploaded to
a draft; the workflow makes it public only after uploads succeed. Published
stable releases are never overwritten. Fixes require a new version; a failed
draft can be retried using the same tag.

Release notes contain the version's Changie entries, exact commit, UTC build time,
workflow link, platform list, and copy/paste Unraid installation commands.

## Nightly builds

**Nightly** runs daily at **03:17 UTC** from the default branch. It calls the same
Release workflow directly, so it does not depend on tag events emitted by
`GITHUB_TOKEN` starting another workflow.

Before installing build tools, the shared workflow compares the selected commit
with the last complete published nightly. An unchanged commit skips the build and
publication, including manually triggered runs. Missing tags, missing releases,
and incomplete uploads remain retryable. The comparison runs inside the existing
per-tag concurrency group, so queued runs see the previous run's publication.

A successful build of changed source replaces the `nightly` tag and prerelease. It never becomes
the stable “latest” release. Archive names remain stable, for example:

`dodu_nightly_linux_amd64.tar.gz`

The embedded binary version includes a timestamp and commit. The release body
includes the latest batched changes, pending fragments, and full commit messages
since the most recent stable tag. The build manifest and checksums identify the
exact source and files despite the moving download URL.

Publication of each tag is serialized and is not cancelled midway by newer runs. The prior
nightly remains available through testing and packaging; replacing it requires
a brief publication window. An upload failure leaves a draft and must be retried.

## Manual runs

Every workflow has **Run workflow** in GitHub Actions:

- **CI**: verify a selected branch/ref and produce downloadable preview artifacts.
- **Release**: provide an existing `vMAJOR.MINOR.PATCH` tag, or `nightly` to build
  the selected ref. Stable runs always check out the tag, regardless of selected ref.
- **Nightly**: build the selected ref through Release; scheduled runs use main.

Equivalent CLI commands:

```sh
gh workflow run ci.yml --ref main
gh workflow run release.yml --ref main -f tag=v0.4.0
gh workflow run nightly.yml --ref main
```

Use `contents: write` through the repository's built-in workflow token. No personal
token is required. The moving nightly tag is excluded from GoReleaser semver
selection. GoReleaser OSS snapshot builds are published by the shared workflow;
the paid GoReleaser nightly mode is not used.

## Local checks

```sh
task verify
task release:check
python3 -m unittest discover -s scripts/tests -v
goreleaser check
python3 scripts/release.py prepare nightly
export DODU_BUILD_VERSION=$(python3 -c 'import json; print(json.load(open("release-metadata.json"))["version"])')
goreleaser release --snapshot --clean --skip=publish
python3 scripts/release.py verify-assets
```

GoReleaser is pinned to v2.18.1 in Actions. Changie is pinned in Taskfile.yml.
Local snapshots must export the prepared version, as above. CI sets it automatically.
Verification rejects mismatched binary-build metadata before writing provenance or
uploading preview and release archives.
