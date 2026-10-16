# Quickstart

## 1. Install

See [docs/install.md](install.md).

## 2. Scan your daemon

```bash
dodu scan
```

This produces a human-readable summary of images, containers, volumes, build
cache and per-container log sizes, plus an estimate of how much disk is
reclaimable. Add `--format json` (or `csv-images`, `csv-containers`,
`csv-volumes`, `csv-build_cache`) to pipe machine-readable output.

## 3. Browse interactively

Run `dodu` with no subcommand to launch the TUI:

```bash
dodu
```

Keys at a glance:

| Key            | Action                                   |
|----------------|------------------------------------------|
| `j` / `k`      | Move down / up                           |
| `l` / `enter`  | Open child                               |
| `h` / `esc`    | Back                                     |
| `g`            | Toggle layout (by-type ↔ by-project)     |
| `s`            | Cycle sort (size → name → count)         |
| `r`            | Re-scan                                  |
| `Tab`          | Toggle details                           |
| `d`            | Mark an individual object                 |
| `p`            | Preview a guarded cleanup plan            |
| `x`            | Refresh preview, then require typed yes    |
| `e`            | Export JSON to a new file                  |
| `?`            | Toggle help overlay                       |
| `q`            | Quit                                     |

## 4. Plan a clean-up (dry run by default)

```bash
dodu prune                        # dry-run, lists what would be removed
dodu prune --kind image,volume    # only consider those kinds
```

## 5. Apply with explicit confirmation

```bash
dodu prune --apply --yes
```

The plan blocks running containers, in-use volumes, and tagged images that
back live containers. An audit log is appended to
`$XDG_STATE_HOME/dodu/audit.log` (or `~/.local/state/dodu/audit.log`).

To enforce dry-run discipline globally:

```bash
export DODU_READONLY=1            # `prune --apply` exits with code 3
```

## 6. Export a snapshot

```bash
dodu export snapshot.json
dodu export images.csv            # default CSV table is "images"
```

## 7. Inspect or clear the cache

```bash
dodu cache info
dodu cache purge
```

The cache stores display snapshots for up to 60 seconds, keyed by daemon ID and
version. Use `--no-cache` or TUI `r` to refresh. Prune always scans fresh.
Cache failure falls back to scanning; applied cleanup requires a writable audit
log. Sizes follow Docker logical accounting, not filesystem physical allocation.
Log totals include only readable local files; remote/inaccessible logs are
unmeasured. See [verification](verification.md) for tested environments.
