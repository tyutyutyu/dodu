#!/bin/sh
# Run cleanup checks only against a disposable nested daemon, never the host.
set -eu

repo=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
work=$(mktemp -d "${TMPDIR:-/tmp}/dodu-smoke.XXXXXX")
cidfile=$work/daemon.cid
cleanup() {
    status=$?
    trap - EXIT HUP INT TERM
    if [ -s "$cidfile" ]; then
        daemon=$(cat "$cidfile")
        if [ "$status" -ne 0 ]; then docker logs "$daemon" >&2 || :; fi
        docker rm -fv "$daemon" >/dev/null 2>&1 || :
    fi
    rm -rf "$work"
    exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' HUP TERM

binary=${DODU_BINARY:-$repo/bin/dodu}
if [ ! -f "$binary" ]; then
    command -v go >/dev/null 2>&1 || {
        echo 'Set DODU_BINARY to a static Linux dodu binary, or install Go.' >&2
        exit 1
    }
    binary=$work/dodu
    (cd "$repo" && CGO_ENABLED=0 GOOS=linux go build -o "$binary" ./cmd/dodu)
fi

# No socket mounts or published ports: all test operations use docker exec.
docker create --privileged --cidfile "$cidfile" \
    --label dodu.integration=smoke \
    -e DOCKER_TLS_CERTDIR= \
    docker:29-dind --storage-driver=vfs \
    --host=unix:///var/run/docker.sock >/dev/null
daemon=$(cat "$cidfile")
docker start "$daemon" >/dev/null
tries=0
until docker exec "$daemon" docker --host unix:///var/run/docker.sock info >/dev/null 2>&1; do
    tries=$((tries + 1))
    if [ "$tries" -ge 90 ]; then echo 'Nested daemon failed to become ready.' >&2; exit 1; fi
    sleep 1
done
docker cp "$binary" "$daemon:/usr/local/bin/dodu" >/dev/null
docker exec "$daemon" chmod +x /usr/local/bin/dodu

# Run the remainder inside the isolated Alpine daemon container.
docker exec -i "$daemon" sh -eu <<'INNER'
export DOCKER_HOST=unix:///var/run/docker.sock
export XDG_STATE_HOME=/tmp/dodu-state
export XDG_CACHE_HOME=/tmp/dodu-cache
unset DODU_READONLY

docker pull alpine:3.21 >/dev/null
docker volume create dodu-smoke-used >/dev/null
docker volume create dodu-smoke-unused >/dev/null
# Creating an unmounted NFS volume needs no reachable NFS server. Docker's
# disk-usage endpoint omits it, but it must remain in dodu's inventory.
docker volume create --driver local --opt type=nfs --opt o=addr=127.0.0.1 \
    --opt device=:/dodu-smoke dodu-smoke-nfs >/dev/null
docker run --rm -v dodu-smoke-unused:/data alpine:3.21 \
    sh -c 'dd if=/dev/zero of=/data/payload bs=1024 count=64 2>/dev/null'
docker run --name dodu-smoke-stopped alpine:3.21 \
    sh -c 'dd if=/dev/zero of=/payload bs=1024 count=64 2>/dev/null'
docker run -d --name dodu-smoke-running -v dodu-smoke-used:/data alpine:3.21 \
    sh -c 'dd if=/dev/zero of=/data/payload bs=1024 count=64 2>/dev/null; exec sleep 3600' >/dev/null

dodu --no-cache scan --format csv-volumes > /tmp/volumes.csv
awk -F, '$1 == "dodu-smoke-unused" && $4 >= 65536 { found=1 } END { exit !found }' /tmp/volumes.csv
awk -F, '$1 == "dodu-smoke-nfs" && $4 == -1 { found=1 } END { exit !found }' /tmp/volumes.csv

dodu --no-cache scan --json > /tmp/scan.json
grep -q '"schema_version"' /tmp/scan.json
if grep -q '"errors"' /tmp/scan.json; then cat /tmp/scan.json; exit 1; fi

dodu prune --kind container,volume > /tmp/dry-run.txt
grep -q '(dry-run)' /tmp/dry-run.txt
docker container inspect dodu-smoke-stopped >/dev/null
docker volume inspect dodu-smoke-unused >/dev/null

if DODU_READONLY=1 dodu prune --kind container,volume --apply --yes > /tmp/readonly.txt 2>&1; then
    echo 'Readonly cleanup unexpectedly succeeded.' >&2
    exit 1
fi
grep -Eq 'read.only|READONLY' /tmp/readonly.txt
docker container inspect dodu-smoke-stopped >/dev/null
docker volume inspect dodu-smoke-unused >/dev/null

dodu prune --kind container,volume --apply --yes
if docker container inspect dodu-smoke-stopped >/dev/null 2>&1; then
    echo 'Stopped container survived cleanup.' >&2; exit 1
fi
if docker volume inspect dodu-smoke-unused >/dev/null 2>&1; then
    echo 'Unused volume survived cleanup.' >&2; exit 1
fi
[ "$(docker inspect -f '{{.State.Running}}' dodu-smoke-running)" = true ]
docker volume inspect dodu-smoke-used >/dev/null
docker exec dodu-smoke-running test -s /data/payload
[ -s "$XDG_STATE_HOME/dodu/audit.log" ]

dodu --no-cache export /tmp/result.json
grep -q '"schema_version"' /tmp/result.json
grep -q 'dodu-smoke-running' /tmp/result.json
grep -q 'dodu-smoke-nfs' /tmp/result.json
if grep -Eq 'dodu-smoke-stopped|dodu-smoke-unused' /tmp/result.json; then
    echo 'Export still contains removed objects.' >&2; exit 1
fi
printf '%s\n' 'PASS: Alpine/static binary, vfs volume accounting, dry-run, readonly, guarded cleanup, audit, JSON export.'
INNER
