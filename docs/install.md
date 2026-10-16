# Installing dodu

## Download a binary on Unraid / Linux x86-64

No Go installation is needed. In Unraid's terminal:

```sh
(
set -eu
mkdir -p /tmp/dodu-download && cd /tmp/dodu-download
curl -fLO https://github.com/tyutyutyu/dodu/releases/download/v0.4.0/dodu_0.4.0_linux_amd64.tar.gz
curl -fLO https://github.com/tyutyutyu/dodu/releases/download/v0.4.0/checksums.txt
grep '  dodu_0.4.0_linux_amd64.tar.gz$' checksums.txt | sha256sum -c -
tar -xzf dodu_0.4.0_linux_amd64.tar.gz
chmod +x dodu
./dodu --readonly --no-cache scan
./dodu --readonly
)
```

For the rolling prerelease, replace `v0.4.0` with `nightly` in the URLs and
`dodu_0.4.0_linux_amd64.tar.gz` with `dodu_nightly_linux_amd64.tar.gz` throughout.
These commands use a temporary directory. For persistent installation, store the
verified binary on your chosen persistent share and copy it to an executable
location when needed. `--readonly` prevents deletion during testing.

[Releases](https://github.com/tyutyutyu/dodu/releases) also contain Linux arm64 and
macOS amd64/arm64 archives. Each release has SHA-256 checksums and build provenance.

## Build this checkout

Use Go 1.23 or newer:

```sh
git clone https://github.com/tyutyutyu/dodu.git
cd dodu
CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o bin/dodu ./cmd/dodu
install -m 0755 bin/dodu "$HOME/.local/bin/dodu"
dodu version
```

Create `$HOME/.local/bin` if necessary and include it in `PATH`. For a system-wide
installation, install the binary to `/usr/local/bin` using your system's normal
administrative privileges. No service or package manager is required.

On Alpine and Slackware/Unraid, use the static **Linux** binary for the host CPU.
The same binary runs with musl or glibc because it links neither library.
To cross-build, add `GOOS=linux GOARCH=arm64` (or `amd64`) to the build command.
macOS builds use `GOOS=darwin` with either architecture.

## Docker access

The client must be able to connect to the Docker API. Standard Linux installations
use `/var/run/docker.sock`. For rootless Docker, point `--host` or `DOCKER_HOST`
at its actual socket. For Docker Desktop, use its exposed host socket.
Dodu does not read Docker CLI contexts automatically.

Docker socket access typically grants powerful daemon control; `--readonly` and
`DODU_READONLY=1` constrain dodu's actions, not other clients of that socket.
Remote TCP endpoints honor the Docker SDK's `DOCKER_HOST`, `DOCKER_TLS_VERIFY`,
and `DOCKER_CERT_PATH` environment settings. SSH contexts are not implemented.

## Filesystems

Scanning uses Docker's API and needs no access to Docker's data directory.
An unavailable or unsuitable cache directory falls back to scanning. Use
`--no-cache` to skip cache access entirely, or set `XDG_CACHE_HOME` to local storage.
Audit logging for deletion requires a writable state directory (`XDG_STATE_HOME`).
Exports require a writable destination. Filesystem-specific physical savings,
remote log sizes, and unreported volume-plugin sizes cannot be inferred by dodu.

## Distribution artifacts

Tag pushes publish stable releases; a scheduled workflow replaces the nightly
prerelease. Every workflow can also be run manually. See [release maintenance](releasing.md)
for change fragments, version preparation, and workflow details.
