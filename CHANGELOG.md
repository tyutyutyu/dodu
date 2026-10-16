# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

New changes are collected in [`.changes/unreleased`](.changes/unreleased) until the next release.

## [0.4.0](https://github.com/tyutyutyu/dodu/releases/tag/v0.4.0) - 2026-09-15

### Added

- Complete the interactive storage atlas with details, marking, export, cleanup preview, confirmation, cancellation, and read-only operation.
- Publish versioned Linux and macOS archives for amd64 and arm64, SHA-256 checksums, and build provenance through a manually runnable tag-release workflow. Static Linux binaries run without Go or a libc dependency, including on Alpine and Slackware/Unraid systems.
- Build a scheduled or manually triggered nightly prerelease through the shared release workflow. Refresh the nightly tag and assets with detailed changes, commit identity, and verification metadata.
- Collect contributor-facing change fragments with Changie and prepare versioned release notes and VERSION together; retain the previous changelog history.

### Changed

- Gather Docker storage usage through one disk-usage request instead of storage-driver internals; preserve scanning when cache storage is unavailable or unwritable.

### Fixed

- Correct volume and shared-image accounting, duplicated log totals, and cache lock retention. Scope build-cache cleanup to selected records, protect active containers, and propagate audit and deletion failures.

**Full changelog:** [Changes since the 0.3.0 source snapshot](https://github.com/tyutyutyu/dodu/compare/3b9167939fd0eba58dd68ebd40d722e8f05ea020...v0.4.0)

## [0.3.0] - 2026-05-01

### Added
- `pkg/export`: machine-readable JSON (`ToJSON`, `Document` schema v1.0) and
  CSV (`ToCSV` for images / containers / volumes / build_cache) snapshot
  exporters with stable column ordering.
- `pkg/plan`: deterministic clean-up planner. `Build` produces a `Plan` with
  blocked items (running containers, in-use images/volumes/build cache) and
  warnings (tagged images backing live containers). `Plan.Execute` performs
  the destructive operations through `docker.Client`, honouring
  `DODU_READONLY=1` and writing a JSON-Lines audit log
  (`$XDG_STATE_HOME/dodu/audit.log`).
- `pkg/docker`: `RemoveImage`, `RemoveContainer`, `RemoveVolume` extending
  the client interface, with SDK and mock implementations.
- `internal/cli`: full Cobra subcommand tree — `scan` (text/json/csv-*),
  `prune` (`--apply --yes --kind`), `export` (`.json` / `.csv`),
  `cache info|purge`, `version`. Persistent flags `--host --no-cache
  --log-level --readonly`. Process exit codes: `0` ok, `1` general,
  `2` daemon unreachable, `3` read-only-denied.
- `internal/tui`: Bubbletea-based atlas UI with hierarchical navigation,
  layout toggle (by-type ↔ by-project), sort cycle (size → name → count),
  rescan, help overlay, and bar visualization (`█`/`░`, `~` shared marker,
  `?` estimated marker). Launched by running `dodu` with no subcommand.
- `pkg/docker/mock.NewLargeClient(images, containers, volumes)` synthetic
  fixture for benchmarks.
- `test/perf/scan_bench_test.go` (build tag `perf`): `BenchmarkScanLarge`
  exercising 2k images / 500 containers / 1k volumes (~6 ms on reference HW).
- `docs/profiling.md`, `docs/perf-report.md`, `docs/install.md`,
  `docs/quickstart.md`.
- `.goreleaser.yaml` + `.github/workflows/release.yml` for tagged release
  artefacts (linux/darwin × amd64/arm64, tar.gz archives, sha256 checksums).

### Changed
- `cmd/dodu/main.go`: `cli.Execute` now returns the process exit code
  directly (`os.Exit(cli.Execute(...))`) so subcommands can signal specific
  failure classes.
- Added dependencies: `github.com/charmbracelet/bubbletea`,
  `github.com/charmbracelet/lipgloss`, `github.com/charmbracelet/bubbles`,
  `golang.org/x/term`.

[0.3.0]: https://github.com/tyutyutyu/dodu/compare/v0.2.0...v0.3.0

## [0.2.0] - 2026-05-01

### Added
- `pkg/scan`: parallel snapshot collector (`Scanner.Scan`) gathering images,
  containers, volumes, build cache, and per-container log file sizes via
  `errgroup`. Partial collector failures are recorded in `Snapshot.Errors`
  rather than aborting; only ping or context cancellation aborts.
  Configurable `LogConcurrency` (default 16) and `LogStatTimeout` (default 1s).
- `pkg/size`: per-image accounting (`ImageSize{Total,Shared,Exclusive,
  Estimated}`) honouring missing `SharedSize`; per-container accounting
  (writable layer + log file); `ComputeTotals` aggregating images, containers,
  volumes, build cache, logs, and a conservative reclaimable estimate; IEC/SI
  human-readable size formatter.
- `pkg/group`: navigable `Node` tree with `ByType` and `ByProject` builders
  (compose project/service grouping with `<orphan>` bucket), bidirectional
  refs (image↔containers, volume↔containers), and `Sort{BySize,ByName,
  ByCount}`.
- `pkg/cache`: bbolt-backed snapshot cache with gob encoding, daemon-keyed
  hashing (`Key`), versioned entries with corruption fallback, and
  `Policy.Fresh` TTL check (default 60s). Default path honours
  `XDG_CACHE_HOME` then `~/.cache/dodu/snapshots.db`.

### Fixed
- `pkg/docker/mock`: race in `bump`/`CallCount` writes when invoked from
  parallel goroutines (added mutex).

[0.2.0]: https://github.com/tyutyutyu/dodu/compare/v0.1.0...v0.2.0

## [0.1.0] - 2026-05-01

### Added
- `pkg/docker`: thin, mockable Docker Engine SDK wrapper exposing only the
  surface dodu needs (`Ping`, `ListImages`, `ListContainers`, `ListVolumes`,
  `BuildCacheUsage`, `DiskUsage`, `LogFileSize`, `Prune{Images,Containers,
  Volumes,BuildCache}`, `Close`).
- Internal types decouple consumers from the Docker SDK; SDK is imported
  only from `pkg/docker`.
- Sentinel errors (`ErrDaemonUnreachable`, `ErrPermissionDenied`,
  `ErrReadOnly`) with `mapError` translation for socket / network failures.
- `pkg/docker/mock`: programmable test double implementing `docker.Client`
  with per-method `*Func` overrides and call counters.
- Unit tests for error mapping, options, mock invariants, and interface
  conformance (compile-time assertion).

### Changed
- Pinned transitive deps (`grpc`, `genproto`, `otel*`, `x/sys`, `x/time`,
  `go-connections`) to versions compatible with Go 1.23 toolchain.

[0.1.0]: https://github.com/tyutyutyu/dodu/compare/v0.0.1...v0.1.0

## [0.0.1] - 2026-04-30

### Added
- Project skeleton: Go module, package layout (`pkg/{docker,scan,size,group,plan,cache,export}`, `internal/{cli,tui}`, `cmd/dodu`).
- Cobra-based CLI root with `version` subcommand.
- Taskfile targets: `verify`, `lint`, `test`, `build`, `run`, `tidy`, `clean`, `version-bump`.
- `golangci-lint` v2 configuration with formatters (gofumpt, gci).
- GitHub Actions CI workflow (Linux + macOS) running `task verify`.
- Phase-by-phase development prompt files under `.github/prompts/`.
- `PLAN.md` with vision, KPIs, MVP scope, architecture, UX, and roadmap.
- MIT license, `.gitignore`, `CHANGELOG.md`.

[0.0.1]: https://github.com/tyutyutyu/dodu/releases/tag/v0.0.1
