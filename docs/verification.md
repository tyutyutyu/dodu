# Functional verification (2026-09-15)

## Blockers found and fixed

The checkout had implementations for all phases, but only unit tests and stale
bootstrap documentation. The fork had no recorded Actions runs when inspected.
There is no evidence establishing why development stopped; these are reproduced
functional blockers, not a claim about the author's reason for pausing:

- Volume listing did not contain usage data: a real scan displayed 179 volumes
  as 0 B. Using Docker's disk-usage API reports 23.3 GiB on the same host.
- Image totals omitted shared layers, and grouped views counted log bytes twice.
- A cache miss retained its own database lock, delaying and preventing cache save.
- Selected build-cache records triggered an unfiltered global prune.
- Paused/restarting containers were treated as stopped candidates.
- Execution silently ignored audit failures and reported failed deletions as success.
- The TUI lacked details, marking, planner, export, and confirmed execution.
- Hidden cleanup keys under help and ignored Ctrl+C were caught during final review.
- The Taskfile selected a system v1 linter with a v2 configuration; installation
  instructions pointed to the upstream repository and an invalid local go-install command.

## Evidence

| Requirement | Verification |
|---|---|
| Scan/accounting | Real Docker 29.7.2, overlay2, ext4 host; Docker 29.8.0 with vfs in disposable Alpine daemon |
| Alpine | Static binary; isolated vfs scan, nonzero volume data, export and cleanup smoke |
| Slackware | Same binary starts on Slackware 15.0 and scans isolated daemon over TCP from a read-only container root with tmpfs cache |
| Filesystem independence | Docker API accounting; no storage-driver tree traversal; failed cache storage falls back to scanning |
| Guarded cleanup | Disposable daemon test preserves running container and mounted data, deletes stopped container/unused volume, checks dry-run and readonly |
| Build cache | Real BuildKit records removed using scoped IDs; HTTP regressions verify exact escaped matching and scoped `all` |
| Audit | Durable intent before mutation and result afterward; regression tests cover write/sync failures |
| Terminal | Actual 120x32 PTY: atlas, details, marking, preview, readonly, help, clean quit |
| Other CPU/OS targets | Linux arm64 and macOS amd64/arm64 cross-build; runtime testing was Linux amd64 |
| Quality | Race tests, vet/static analysis, tidy check; regression tests accompany corrected behavior |

## Repeat

```sh
task verify
sh test/integration/smoke.sh
python3 test/integration/tui_smoke.py   # reachable daemon, read-only interaction
```

The integration shell script owns a disposable privileged Docker-in-Docker
container and cleans it up on exit. It never passes the host socket to dodu and
never prunes the host daemon. Docker and image download access are needed.
The PTY script uses Python's standard library and the current Docker endpoint.

## Limits

These tests establish functioning software on representative systems, not an
exhaustive test of every filesystem, kernel, daemon version, or hardware target.
Unraid itself and native macOS/arm64 execution have not been tested. Slackware
userspace compatibility does not prove every Unraid storage plugin's reporting.
Docker remains authoritative for logical size and deletion eligibility. Physical
reclaim can differ on compressed, reflinked, sparse, snapshotting, or remote storage.
Logs inaccessible to the client and unsupported volume-plugin accounting are
unmeasured, not proof of zero physical consumption.

Release publication and artifact generation are now handled by the shared
[release workflows](releasing.md). The table above records functional validation;
native runtime coverage and filesystem limits still apply to published binaries.
Package repositories, screenshots, and universal performance SLAs remain roadmap
work. A local build is available at `bin/dodu`.
