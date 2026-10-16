# dodu

Docker disk atlas: a terminal browser and CLI for images, containers, volumes,
build cache, and readable local log files. Inspect storage, export snapshots,
and preview cleanup before explicitly applying it.

## Download

Get a prebuilt binary from [Releases](https://github.com/tyutyutyu/dodu/releases/latest)
or the [nightly prerelease](https://github.com/tyutyutyu/dodu/releases/tag/nightly).
Go is not needed to run it.

## Build and run

Requires Go 1.23 or newer to build and access to a Docker Engine to scan.

```sh
git clone https://github.com/tyutyutyu/dodu.git
cd dodu
CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o bin/dodu ./cmd/dodu
./bin/dodu scan
./bin/dodu                      # interactive atlas
```

The static Linux binary has no libc, package-manager, or init-system dependency.
It can run on Alpine, Slackware, and other Linux distributions of the
matching architecture. Docker reports storage sizes through its API; dodu does
not read overlay2, Btrfs, ZFS, XFS, or other driver internals. The cache is optional
and scanning continues if the cache filesystem cannot support it.

## Commands

```sh
dodu scan --json                 # or --format text/csv-images/csv-volumes/...
dodu export snapshot.json
dodu export images.csv
dodu prune                      # fresh scan and dry-run only
dodu prune --kind container --apply --yes
dodu --readonly                 # browsing and export; no cleanup
dodu cache info
dodu cache purge
dodu version
```

Use `--host unix:///path/to/docker.sock` or `DOCKER_HOST` for a nondefault endpoint.
`--no-cache` bypasses the 60-second display cache. Cleanup always scans afresh.
`DODU_READONLY=1` disables cleanup in both CLI and TUI.

**TUI keys:** arrows or `j/k` move, Enter/`l` opens, Esc/`h` goes back,
`g` switches type/project grouping, `s` sorts, Tab toggles details, `r` rescans,
`d` marks one object, `p` previews cleanup, `x` refreshes the preview before
requiring typed `yes`, `e` exports JSON to the current directory, `?` shows help,
and `q` quits.

## Measurement and cleanup limits

- Sizes are Docker's logical accounting, not physical filesystem allocation.
  Compression, snapshots, sparse files, reflinks, and plugin-managed storage can
  make physical free-space changes differ from the estimate.
- Shared image layers count once in the daemon image total. Build cache may
  share storage with images; category totals are not independent disk partitions.
- Remote, inaccessible, and non-file logs are unmeasured. The log subtotal includes
  only readable local files, not rotated or externally managed logs.
- Unknown volume sizes remain marked as estimates. Docker plugins may not expose
  usage. An incomplete scan is displayed with errors and cannot authorize cleanup.
- Cleanup does not force-remove objects. Active containers and their images,
  and referenced volumes, are protected. Docker enforces concurrent-use checks.
- Applied cleanup requires a writable audit path under
  `$XDG_STATE_HOME/dodu/audit.log` or `~/.local/state/dodu/audit.log`.
  Deletion failures return a nonzero exit status; reclaimed bytes are estimates
  except where Docker returns a measured value.

See [installation](docs/install.md), [quickstart](docs/quickstart.md), [releasing](docs/releasing.md), and
[verification](docs/verification.md). [PLAN.md](PLAN.md) and phase prompts preserve
the original roadmap; their original design and performance targets are not guarantees.

## Development

```sh
go test -race ./...
go vet ./...
go mod tidy -diff
# With Task installed:
task verify
```

`task verify` uses the pinned v2 linter independently of any system v1 installation.
Static analysis uses Go 1.23.12, supported by that linter. Runtime binaries do not
require Go or Task. Release builds cover Linux/macOS on amd64 and arm64.

## License

[MIT](LICENSE)
