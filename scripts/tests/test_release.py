"""Release contract tests; no network or GitHub credentials required."""
import hashlib
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
RELEASE = ROOT / "scripts/release.py"
PUBLISH = ROOT / "scripts/publish-release.sh"
PLATFORMS = ("linux_amd64", "linux_arm64", "darwin_amd64", "darwin_arm64")


class RepositoryCase(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.repo = Path(self.temp.name)
        self.env = os.environ.copy()
        for key in ("GITHUB_ENV", "GITHUB_OUTPUT", "GITHUB_REPOSITORY", "GITHUB_RUN_ID"):
            self.env.pop(key, None)
        self.env["GIT_CONFIG_NOSYSTEM"] = "1"
        self.env["GIT_CONFIG_GLOBAL"] = os.devnull
        self.git("init", "--quiet")
        self.git("config", "user.email", "release-test@example.invalid")
        self.git("config", "user.name", "Release Test")
        (self.repo / ".changes/unreleased").mkdir(parents=True)
        (self.repo / "VERSION").write_text("0.1.0\n")
        (self.repo / ".changes/v0.1.0.md").write_text("## Added\n\nPortable release archives.\n")
        self.commit("Initial release source")

    def run_command(self, *args, **kwargs):
        return subprocess.run(args, cwd=self.repo, env=self.env,
                              text=True, capture_output=True, **kwargs)

    def git(self, *args):
        result = self.run_command("git", *args)
        self.assertEqual(result.returncode, 0, result.stderr)
        return result.stdout.strip()

    def commit(self, message):
        self.git("add", ".")
        self.git("commit", "--quiet", "-m", message)
        return self.git("rev-parse", "HEAD")

    def prepare(self, tag):
        return self.run_command(sys.executable, str(RELEASE), "prepare", tag)


class ReleaseMetadataTests(RepositoryCase):
    def test_nightly_does_not_override_git_tag_before_first_release(self):
        envfile = self.repo / "github-env"
        self.env["GITHUB_ENV"] = str(envfile)
        result = self.prepare("nightly")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn("GORELEASER_CURRENT_TAG", envfile.read_text())

    def test_stable_exports_validated_tag(self):
        self.git("tag", "v0.1.0")
        envfile = self.repo / "github-env"
        self.env["GITHUB_ENV"] = str(envfile)
        result = self.prepare("v0.1.0")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("GORELEASER_CURRENT_TAG=v0.1.0", envfile.read_text())

    def test_stable_tag_uses_exact_version_commit_and_notes(self):
        self.git("tag", "-a", "v0.1.0", "-m", "Release 0.1.0")
        result = self.prepare("v0.1.0")
        self.assertEqual(result.returncode, 0, result.stderr)
        metadata = json.loads((self.repo / "release-metadata.json").read_text())
        self.assertEqual(metadata["commit"], self.git("rev-parse", "HEAD"))
        self.assertEqual(metadata["version"], "0.1.0")
        self.assertEqual(metadata["channel"], "stable")
        self.assertEqual(metadata["archive_version"], "0.1.0")
        notes = (self.repo / "release-notes.md").read_text()
        self.assertIn("Portable release archives.", notes)
        self.assertIn("/releases/download/v0.1.0/dodu_0.1.0_linux_amd64.tar.gz", notes)

    def test_stable_tag_must_match_version(self):
        self.git("tag", "v0.2.0")
        result = self.prepare("v0.2.0")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("release tag does not match VERSION", result.stderr)
        self.assertFalse((self.repo / "release-metadata.json").exists())

    def test_stable_tag_must_point_to_checkout(self):
        self.git("tag", "v0.1.0")
        (self.repo / "later.txt").write_text("later change\n")
        self.commit("A later commit")
        result = self.prepare("v0.1.0")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("checkout does not match the release tag", result.stderr)

    def test_stable_tag_must_exist(self):
        result = self.prepare("v0.1.0")
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.repo / "release-metadata.json").exists())

    def test_stable_tag_rejects_pending_yaml_fragments(self):
        self.git("tag", "v0.1.0")
        for extension in ("yaml", "yml"):
            with self.subTest(extension=extension):
                fragment = self.repo / f".changes/unreleased/pending.{extension}"
                fragment.write_text("kind: fixed\nbody: Pending fix\n")
                result = self.prepare("v0.1.0")
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("batch pending change fragments", result.stderr)
                fragment.unlink()

    def test_invalid_tag_cannot_inject_commands_or_environment_lines(self):
        for tag in ("v01.0.0", "v0.1.0-rc.1", "v0.1.0; touch INJECTED",
                    "$(touch INJECTED)", "nightly\nEVIL=1", "../nightly"):
            with self.subTest(tag=tag):
                result = self.prepare(tag)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("tag must be nightly or vMAJOR.MINOR.PATCH", result.stderr)
                self.assertFalse((self.repo / "INJECTED").exists())
                self.assertFalse((self.repo / "release-metadata.json").exists())

    def test_missing_or_empty_release_notes_fail(self):
        self.git("tag", "v0.1.0")
        notes = self.repo / ".changes/v0.1.0.md"
        for contents in ("", None):
            with self.subTest(contents=contents):
                if contents is None:
                    notes.unlink()
                else:
                    notes.write_text(contents)
                result = self.prepare("v0.1.0")
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("missing release notes", result.stderr)

    def test_nightly_notes_preserve_history_and_ignore_moving_tag(self):
        self.git("tag", "v0.0.9")
        (self.repo / "change.txt").write_text("first\n")
        first = self.commit("First improvement\n\nConstraint: Keep static binaries\nTested: Unit suite")
        (self.repo / "change.txt").write_text("second\n")
        second = self.commit("Second improvement\n\nDetailed release context")
        self.git("tag", "nightly")
        (self.repo / ".changes/unreleased/feature.yaml").write_text("kind: added\nbody: New feature\n")
        self.env["GITHUB_RUN_ID"] = "12345"
        self.env["GITHUB_REPOSITORY"] = "example/dodu"
        result = self.prepare("nightly")
        self.assertEqual(result.returncode, 0, result.stderr)
        meta = json.loads((self.repo / "release-metadata.json").read_text())
        self.assertEqual(meta["commit"], second)
        self.assertEqual(meta["archive_version"], "nightly")
        self.assertRegex(meta["version"], rf"^0\.1\.0-nightly\.[0-9]{{14}}\.g{second[:12]}$")
        notes = (self.repo / "release-notes.md").read_text()
        for text in (first, second, "Constraint: Keep static binaries", "Tested: Unit suite",
                     "Detailed release context", "Commits since v0.0.9", "New feature",
                     "/compare/v0.0.9...", "/actions/runs/12345",
                     "/releases/download/nightly/dodu_nightly_linux_amd64.tar.gz"):
            self.assertIn(text, notes)
        self.assertNotIn("Commits since nightly", notes)
        self.assertNotIn("Initial release source", notes)

    def test_first_nightly_works_without_any_tags(self):
        result = self.prepare("nightly")
        self.assertEqual(result.returncode, 0, result.stderr)
        notes = (self.repo / "release-notes.md").read_text()
        self.assertIn("Commits since repository creation", notes)
        self.assertIn("Initial release source", notes)


class AssetTests(RepositoryCase):
    def setUp(self):
        super().setUp()
        result = self.prepare("nightly")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.dist = self.repo / "dist"
        self.dist.mkdir()
        meta = json.loads((self.repo / "release-metadata.json").read_text())
        (self.dist / "metadata.json").write_text(json.dumps({
            "version": meta["version"], "commit": meta["commit"], "date": meta["built_at"],
        }))
        self.checksums = {}
        for platform in PLATFORMS:
            name = f"dodu_nightly_{platform}.tar.gz"
            with tarfile.open(self.dist / name, "w:gz") as archive:
                data = f"binary fixture for {platform}\n".encode()
                entry = tarfile.TarInfo("dodu")
                entry.size = len(data)
                entry.mode = 0o755
                archive.addfile(entry, io.BytesIO(data))
            self.checksums[name] = hashlib.sha256((self.dist / name).read_bytes()).hexdigest()
        self.write_checksums()

    def write_checksums(self):
        (self.dist / "checksums.txt").write_text("".join(
            f"{digest}  {name}\n" for name, digest in self.checksums.items()))

    def verify(self):
        return self.run_command(sys.executable, str(RELEASE), "verify-assets")

    def test_four_archives_generate_checksummed_build_provenance(self):
        result = self.verify()
        self.assertEqual(result.returncode, 0, result.stderr)
        manifest = json.loads((self.dist / "build-info.json").read_text())
        self.assertEqual(manifest["commit"], self.git("rev-parse", "HEAD"))
        self.assertEqual(manifest["artifacts"], self.checksums)
        for line in (self.dist / "checksums.txt").read_text().splitlines():
            digest, name = line.split()
            self.assertEqual(hashlib.sha256((self.dist / name).read_bytes()).hexdigest(), digest)
        self.assertEqual(len((self.dist / "checksums.txt").read_text().splitlines()), 5)

    def test_goreleaser_version_mismatch_cannot_emit_build_provenance(self):
        metadata_path = self.dist / "metadata.json"
        metadata = json.loads(metadata_path.read_text())
        metadata["version"] = "0.1.0-next"
        metadata_path.write_text(json.dumps(metadata))
        checksums_before = (self.dist / "checksums.txt").read_text()
        result = self.verify()
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.dist / "build-info.json").exists())
        self.assertEqual((self.dist / "checksums.txt").read_text(), checksums_before)

    def test_goreleaser_commit_mismatch_cannot_emit_build_provenance(self):
        metadata_path = self.dist / "metadata.json"
        metadata = json.loads(metadata_path.read_text())
        metadata["commit"] = "0" * 40
        metadata_path.write_text(json.dumps(metadata))
        checksums_before = (self.dist / "checksums.txt").read_text()
        result = self.verify()
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.dist / "build-info.json").exists())
        self.assertEqual((self.dist / "checksums.txt").read_text(), checksums_before)

    def test_missing_platform_checksum_fails(self):
        self.checksums.pop(next(iter(self.checksums)))
        self.write_checksums()
        result = self.verify()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("exactly four platform archives", result.stderr)
        self.assertFalse((self.dist / "build-info.json").exists())

    def test_missing_archive_file_fails(self):
        (self.dist / next(iter(self.checksums))).unlink()
        result = self.verify()
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.dist / "build-info.json").exists())

    def test_tampered_archive_fails(self):
        (self.dist / next(iter(self.checksums))).write_bytes(b"tampered content")
        result = self.verify()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("checksum mismatch", result.stderr)

    def test_traversal_and_absolute_checksum_paths_fail(self):
        for name in ("../outside.tar.gz", "/tmp/outside.tar.gz", "subdir/archive.tar.gz"):
            with self.subTest(name=name):
                (self.dist / "checksums.txt").write_text(f"{'a' * 64}  {name}\n")
                result = self.verify()
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("invalid checksum entry", result.stderr)
                self.assertFalse((self.dist / "build-info.json").exists())

    def test_duplicate_checksum_and_invalid_digest_fail(self):
        first = next(iter(self.checksums))
        valid = f"{self.checksums[first]}  {first}\n"
        for contents, error in ((valid + valid, "duplicate checksum entry"),
                                (f"invalid  {first}\n", "invalid checksum entry")):
            with self.subTest(error=error):
                (self.dist / "checksums.txt").write_text(contents)
                result = self.verify()
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(error, result.stderr)


MOCK_GH = r'''#!/usr/bin/env python3
import json
import os
from pathlib import Path
import sys

args = sys.argv[1:]
with Path(os.environ["MOCK_GH_LOG"]).open("a") as log:
    log.write(json.dumps(args) + "\n")
scenario = os.environ["MOCK_GH_SCENARIO"]
if args[:2] == ["release", "view"]:
    if "--jq" in args:
        print("true" if scenario == "stable-draft" else "false")
    elif scenario in ("stable-new", "nightly-new") and not Path(os.environ["MOCK_GH_UPLOADED"]).exists():
        sys.exit(1)
    else:
        print(json.dumps({"id": "old-release", "isDraft": scenario == "stable-draft"}))
elif args[:1] == ["api"] and "--method" not in args:
    sys.exit(1 if scenario == "nightly-new" else 0)
elif args[:2] == ["release", "create"]:
    if scenario == "upload-failure":
        sys.exit(1)
    assert "--draft" in args, "Assets must be uploaded to a draft"
    assets = [arg for arg in args if arg.startswith("dist/")]
    assert len(assets) == 6 and all(Path(arg).is_file() for arg in assets)
    Path(os.environ["MOCK_GH_UPLOADED"]).write_text("uploaded")
elif args[:2] == ["release", "edit"]:
    assert Path(os.environ["MOCK_GH_UPLOADED"]).exists(), "Publishing before assets uploaded"
'''


class PublicationTests(RepositoryCase):
    def setUp(self):
        super().setUp()
        mockbin = self.repo / "mock-bin"
        mockbin.mkdir()
        executable = mockbin / "gh"
        executable.write_text(MOCK_GH)
        executable.chmod(0o755)
        self.log = self.repo / "gh-calls.jsonl"
        self.env.update({"PATH": str(mockbin) + os.pathsep + self.env["PATH"],
                         "MOCK_GH_LOG": str(self.log),
                         "MOCK_GH_UPLOADED": str(self.repo / "uploaded"),
                         "GH_REPO": "example/dodu"})
        (self.repo / "dist").mkdir()
        for name in [f"dodu_nightly_{platform}.tar.gz" for platform in PLATFORMS] + ["checksums.txt", "build-info.json"]:
            (self.repo / "dist" / name).write_text("verified fixture\n")
        (self.repo / "release-notes.md").write_text("Verified changes\n")

    def publish(self, scenario, channel="nightly", tag="nightly"):
        self.env.update({"MOCK_GH_SCENARIO": scenario, "DODU_RELEASE_CHANNEL": channel,
                         "DODU_RELEASE_TAG": tag})
        return self.run_command("bash", str(PUBLISH))

    def calls(self):
        return [json.loads(line) for line in self.log.read_text().splitlines()] if self.log.exists() else []

    def test_published_stable_release_is_never_deleted_or_overwritten(self):
        result = self.publish("stable-published", "stable", "v0.1.0")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("already published", result.stderr)
        self.assertTrue(self.calls())
        self.assertTrue(all(call[:2] == ["release", "view"] for call in self.calls()))

    def test_stable_draft_can_be_retried_then_published(self):
        result = self.publish("stable-draft", "stable", "v0.1.0")
        self.assertEqual(result.returncode, 0, result.stderr)
        calls = self.calls()
        self.assertIn(["release", "delete", "v0.1.0", "--yes"], calls)
        create = next(call for call in calls if call[:2] == ["release", "create"])
        publish = next(call for call in calls if call[:2] == ["release", "edit"])
        self.assertIn("--draft", create)
        self.assertIn("--verify-tag", create)
        self.assertIn("--draft=false", publish)
        self.assertIn("--latest", publish)
        self.assertNotIn("--prerelease", publish)
        self.assertLess(calls.index(create), calls.index(publish))

    def test_nightly_replaces_tag_and_publishes_only_after_draft_assets(self):
        result = self.publish("nightly-existing")
        self.assertEqual(result.returncode, 0, result.stderr)
        calls = self.calls()
        update = next(call for call in calls if "PATCH" in call)
        self.assertIn("repos/example/dodu/git/refs/tags/nightly", update)
        self.assertIn("sha=" + self.git("rev-parse", "HEAD"), update)
        self.assertIn("force=true", update)
        create = next(call for call in calls if call[:2] == ["release", "create"])
        publish = next(call for call in calls if call[:2] == ["release", "edit"])
        self.assertIn("--draft", create)
        for call in (create, publish):
            self.assertIn("--prerelease", call)
            self.assertIn("--latest=false", call)
        self.assertIn("--draft=false", publish)
        self.assertLess(calls.index(create), calls.index(publish))

    def test_first_nightly_creates_tag(self):
        result = self.publish("nightly-new")
        self.assertEqual(result.returncode, 0, result.stderr)
        calls = self.calls()
        create_ref = next(call for call in calls if "POST" in call)
        self.assertIn("ref=refs/tags/nightly", create_ref)
        self.assertFalse(any(call[:2] == ["release", "delete"] for call in calls))

    def test_failed_upload_is_never_published(self):
        result = self.publish("upload-failure")
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(any(call[:2] == ["release", "edit"] for call in self.calls()))

    def test_invalid_channel_or_tag_never_calls_github(self):
        for channel, tag in (("stable", "v0.1.0; touch INJECTED"),
                             ("nightly", "v0.1.0"), ("stable", "nightly")):
            with self.subTest(channel=channel, tag=tag):
                result = self.publish("stable-new", channel, tag)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("Invalid release channel/tag", result.stderr)
                self.assertEqual(self.calls(), [])
                self.assertFalse((self.repo / "INJECTED").exists())


class NightlyChangeTests(RepositoryCase):
    def setUp(self):
        super().setUp()
        self.git("tag", "nightly")
        self.release = {"target_commitish": self.git("rev-parse", "HEAD"), "draft": False,
                        "prerelease": True, "assets": [
                            {"name": name, "state": "uploaded", "size": 1} for name in
                            [f"dodu_nightly_{p}.tar.gz" for p in PLATFORMS] + ["checksums.txt", "build-info.json"]]}
        mockbin = self.repo / "mock-bin"
        mockbin.mkdir()
        mock = mockbin / "gh"
        mock.write_text("#!/usr/bin/env python3\nimport os\nfrom pathlib import Path\n"
                        "print('HTTP/2.0 ' + os.environ.get('MOCK_STATUS', '200') + ' status\\n')\n"
                        "print(Path(os.environ['MOCK_RELEASE']).read_text())\n")
        mock.chmod(0o755)
        self.payload = self.repo / "mock-release.json"
        self.output = self.repo / "github-output"
        self.env.update({"PATH": str(mockbin) + os.pathsep + self.env["PATH"],
                         "GH_REPO": "example/dodu", "MOCK_RELEASE": str(self.payload),
                         "GITHUB_OUTPUT": str(self.output)})

    def decision(self, tag="nightly"):
        self.payload.write_text(json.dumps(self.release))
        return self.run_command(sys.executable, str(RELEASE), "should-publish", tag)

    def assertDecision(self, expected):
        result = self.decision()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(result.stdout)["publish"], expected)
        self.assertIn(f"publish={str(expected).lower()}", self.output.read_text())

    def test_unchanged_complete_nightly_is_skipped(self):
        self.assertDecision(False)

    def test_new_commit_is_published(self):
        self.commit("New source change")
        self.assertDecision(True)

    def test_first_nightly_is_published(self):
        self.git("tag", "-d", "nightly")
        self.assertDecision(True)

    def test_annotated_nightly_is_compared_to_commit(self):
        self.git("tag", "-f", "-a", "nightly", "-m", "Nightly")
        self.assertDecision(False)

    def test_incomplete_publication_can_retry_unchanged_commit(self):
        for update in ({"draft": True}, {"assets": []}, {"target_commitish": "older"}):
            with self.subTest(update=update):
                original = self.release.copy()
                self.release.update(update)
                self.assertDecision(True)
                self.release = original

    def test_tag_without_release_can_retry(self):
        self.env["MOCK_STATUS"] = "404"
        self.assertDecision(True)

    def test_api_errors_fail_instead_of_reporting_unchanged(self):
        self.env["MOCK_STATUS"] = "403"
        result = self.decision()
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.output.exists())

    def test_stable_release_is_not_skipped(self):
        self.env["MOCK_STATUS"] = "403"
        result = self.decision("v0.1.0")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(json.loads(result.stdout)["publish"])


if __name__ == "__main__":
    unittest.main()
