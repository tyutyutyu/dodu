#!/usr/bin/env python3
"""Validate release inputs, generate notes, and verify packaged release assets."""
import argparse
import datetime
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess


STABLE_TAG = re.compile(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\Z")
PLATFORMS = ("linux_amd64", "linux_arm64", "darwin_amd64", "darwin_arm64")


def git(*args):
    return subprocess.check_output(["git", *args], text=True).strip()


def release_metadata(tag):
    if tag != "nightly" and not STABLE_TAG.fullmatch(tag):
        raise ValueError("tag must be nightly or vMAJOR.MINOR.PATCH")
    version = Path("VERSION").read_text().strip()
    if not STABLE_TAG.fullmatch("v" + version):
        raise ValueError("VERSION must contain MAJOR.MINOR.PATCH")
    commit = git("rev-parse", "HEAD")
    nightly = tag == "nightly"
    if not nightly:
        if tag != "v" + version:
            raise ValueError("release tag does not match VERSION")
        if git("rev-parse", f"refs/tags/{tag}^{{commit}}") != commit:
            raise ValueError("checkout does not match the release tag")
        if list(Path(".changes/unreleased").glob("*.y*ml")):
            raise ValueError("batch pending change fragments before tagging a release")
    notes = Path(f".changes/v{version}.md")
    if not notes.is_file() or not notes.read_text().strip():
        raise ValueError(f"missing release notes: {notes}")
    now = datetime.datetime.now(datetime.timezone.utc)
    build_version = f"{version}-nightly.{now:%Y%m%d%H%M%S}.g{commit[:12]}" if nightly else version
    return {"tag": tag, "version": build_version, "base_version": version,
            "channel": "nightly" if nightly else "stable", "commit": commit,
            "built_at": now.isoformat(), "archive_version": "nightly" if nightly else version}


def render_notes(meta):
    repo = os.environ.get("GITHUB_REPOSITORY", "tyutyutyu/dodu")
    if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repo):
        raise ValueError("invalid GitHub repository")
    base = f"https://github.com/{repo}"
    nightly = meta["channel"] == "nightly"
    heading = "Nightly development build" if nightly else f"dodu {meta['tag']}"
    lines = [f"# {heading}", "", f"- Commit: [{meta['commit']}]({base}/commit/{meta['commit']})",
             f"- Built: {meta['built_at']}", f"- Binary version: `{meta['version']}`"]
    if os.environ.get("GITHUB_RUN_ID"):
        lines.append(f"- Workflow: {base}/actions/runs/{os.environ['GITHUB_RUN_ID']}")
    if nightly:
        lines += ["", "This prerelease is replaced by the next successful nightly. It may contain unreleased changes."]
    lines += ["", "## Changes", "", Path(f".changes/v{meta['base_version']}.md").read_text().strip()]
    if nightly:
        fragments = sorted(Path(".changes/unreleased").glob("*.y*ml"))
        if fragments:
            lines += ["", "### Pending change fragments"]
            for path in fragments:
                lines += ["", f"#### {path.name}", "", "```yaml", path.read_text().strip(), "```"]
        tags = [tag for tag in git("tag", "--merged", meta["commit"], "--sort=-version:refname").splitlines()
                if STABLE_TAG.fullmatch(tag)]
        baseline = tags[0] if tags else None
        revision = f"{baseline}..{meta['commit']}" if baseline else meta["commit"]
        history = git("log", "--no-decorate", "--format=%H %s%n%b", revision)
        lines += ["", f"## Commits since {baseline or 'repository creation'}", ""]
        # Indented Markdown prevents arbitrary commit text from closing a code fence.
        lines += ["    " + line for line in history.splitlines()] if history else ["No commits since the latest stable tag."]
        if baseline:
            lines += ["", f"[Full comparison]({base}/compare/{baseline}...{meta['commit']})"]
    label = meta["archive_version"]
    archive = f"dodu_{label}_linux_amd64.tar.gz"
    download = f"{base}/releases/download/{meta['tag']}"
    lines += ["", "## Install on Unraid / Linux x86-64", "", "No Go installation is needed. Run in a terminal:",
              "", "```sh", "(", "set -eu", "mkdir -p /tmp/dodu-download && cd /tmp/dodu-download",
              f"curl -fLO {download}/{archive}", f"curl -fLO {download}/checksums.txt",
              f"grep '  {archive}$' checksums.txt | sha256sum -c -",
              f"tar -xzf {archive}", "chmod +x dodu", "./dodu --readonly --no-cache scan", "./dodu --readonly", ")", "```",
              "", "Other platforms: Linux arm64 and macOS amd64/arm64. Each archive contains the binary, license, and documentation.",
              "", "`build-info.json` identifies the source commit and build. Sizes follow Docker logical accounting; physical savings vary by filesystem."]
    return "\n".join(lines) + "\n"


def prepare(tag, output):
    meta = release_metadata(tag)
    Path(output).write_text(render_notes(meta))
    Path("release-metadata.json").write_text(json.dumps(meta, indent=2) + "\n")
    env = {"DODU_BUILD_VERSION": meta["version"],
           "DODU_RELEASE_TAG": tag, "DODU_RELEASE_CHANNEL": meta["channel"]}
    if meta["channel"] == "stable":
        env["GORELEASER_CURRENT_TAG"] = tag
    if os.environ.get("GITHUB_ENV"):
        with open(os.environ["GITHUB_ENV"], "a") as dest:
            dest.writelines(f"{key}={value}\n" for key, value in env.items())
    if os.environ.get("GITHUB_OUTPUT"):
        with open(os.environ["GITHUB_OUTPUT"], "a") as dest:
            dest.writelines(f"{key}={value}\n" for key, value in meta.items())
    print(json.dumps(meta, indent=2))


def verify_assets(dist, meta):
    dist = Path(dist)
    build = json.loads((dist / "metadata.json").read_text())
    if build["version"] != meta["version"] or build["commit"] != meta["commit"]:
        raise ValueError("packaged version/commit does not match release metadata")
    names = {f"dodu_{meta['archive_version']}_{platform}.tar.gz" for platform in PLATFORMS}
    recorded = {}
    for line in (dist / "checksums.txt").read_text().splitlines():
        digest, name = line.split(maxsplit=1)
        name = name.removeprefix("*")
        if Path(name).name != name or not re.fullmatch(r"[0-9a-f]{64}", digest):
            raise ValueError("invalid checksum entry")
        if name in recorded:
            raise ValueError("duplicate checksum entry")
        recorded[name] = digest
    if set(recorded) != names:
        raise ValueError("release must contain checksums for exactly four platform archives")
    for name, digest in recorded.items():
        if hashlib.sha256((dist / name).read_bytes()).hexdigest() != digest:
            raise ValueError(f"checksum mismatch: {name}")
    manifest = dict(meta, repository=os.environ.get("GITHUB_REPOSITORY", "tyutyutyu/dodu"),
                    run_id=os.environ.get("GITHUB_RUN_ID", "local"), artifacts=recorded)
    manifest["built_at"] = build["date"]
    data = (json.dumps(manifest, indent=2) + "\n").encode()
    (dist / "build-info.json").write_bytes(data)
    with (dist / "checksums.txt").open("a") as dest:
        dest.write(f"{hashlib.sha256(data).hexdigest()}  build-info.json\n")
    print("Verified all four release archives and wrote build provenance.")


def publication_needed(tag, commit):
    if tag != "nightly":
        if not STABLE_TAG.fullmatch(tag):
            raise ValueError("tag must be nightly or vMAJOR.MINOR.PATCH")
        return True, "Stable release requested."
    previous = subprocess.run(["git", "rev-parse", "--verify", "--quiet", "refs/tags/nightly^{commit}"],
                              text=True, capture_output=True)
    if previous.returncode or previous.stdout.strip() != commit:
        return True, "Source differs from the nightly tag, or no nightly tag exists."
    repo = os.environ["GH_REPO"]
    response = subprocess.run(["gh", "api", "--include", f"repos/{repo}/releases/tags/nightly"],
                              text=True, capture_output=True)
    headers, _, body = response.stdout.partition("\n\n")
    status = re.match(r"HTTP/\S+ (\d+)", headers)
    if status and status[1] == "404":
        return True, "Nightly tag exists without a published release; retrying."
    if response.returncode or not status or status[1] != "200":
        raise RuntimeError("Cannot check the previous nightly release: " + response.stderr)
    release = json.loads(body)
    required = {f"dodu_nightly_{platform}.tar.gz" for platform in PLATFORMS} | {"checksums.txt", "build-info.json"}
    uploaded = {asset["name"] for asset in release["assets"] if asset["state"] == "uploaded" and asset["size"] > 0}
    if release["draft"] or release["target_commitish"] != commit or not required <= uploaded:
        return True, "Previous nightly publication is incomplete; retrying."
    return False, f"No changes since the published nightly ({commit}); skipping build and publication."


def should_publish(tag):
    commit = git("rev-parse", "HEAD")
    publish, reason = publication_needed(tag, commit)
    if os.environ.get("GITHUB_OUTPUT"):
        with open(os.environ["GITHUB_OUTPUT"], "a") as dest:
            dest.write(f"publish={str(publish).lower()}\ncommit={commit}\n")
    if os.environ.get("GITHUB_STEP_SUMMARY"):
        with open(os.environ["GITHUB_STEP_SUMMARY"], "a") as dest:
            dest.write(reason + "\n")
    print(json.dumps({"publish": publish, "commit": commit, "reason": reason}))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    prep = sub.add_parser("prepare")
    prep.add_argument("tag")
    prep.add_argument("--output", default="release-notes.md")
    check = sub.add_parser("verify-assets")
    check.add_argument("--dist", default="dist")
    decision = sub.add_parser("should-publish")
    decision.add_argument("tag")
    args = parser.parse_args()
    if args.command == "prepare":
        prepare(args.tag, args.output)
    elif args.command == "should-publish":
        should_publish(args.tag)
    else:
        verify_assets(args.dist, json.loads(Path("release-metadata.json").read_text()))


if __name__ == "__main__":
    main()
