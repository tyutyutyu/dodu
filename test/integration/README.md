# Disposable Docker smoke test

Run from any directory:

```sh
./test/integration/smoke.sh
# Or supply a static Linux binary matching the Docker server architecture:
DODU_BINARY=/path/to/dodu ./test/integration/smoke.sh
```

The script uses `bin/dodu` when present, otherwise builds with `CGO_ENABLED=0`
using Go. The host needs a POSIX shell, standard utilities, and Docker access
that permits privileged containers. It pulls `docker:29-dind` and `alpine:3.21`.

All scan and cleanup commands run inside a disposable Docker daemon with the
`vfs` storage driver and an Alpine userspace. No host Docker socket is mounted
and no ports are published. The exit trap removes only the daemon container
created by this run and its anonymous volumes.

Checks cover nonzero volume accounting, unmeasured NFS volume inventory, JSON scan, dry-run preservation,
readonly refusal, removal of a stopped container and unused volume,
preservation of a running container and its mounted data, audit output, and
JSON export after cleanup. A nonzero exit indicates failure and prints nested
daemon logs. This verifies the Alpine/vfs combination; it does not substitute
for testing other storage drivers, operating systems, or interactive TUI use.
