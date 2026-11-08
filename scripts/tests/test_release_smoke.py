import contextlib
import errno
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import patch
import zipfile

SCRIPT = Path(__file__).resolve().parents[1] / "release-smoke.py"
spec = importlib.util.spec_from_file_location("release_smoke", SCRIPT)
smoke = importlib.util.module_from_spec(spec)
spec.loader.exec_module(smoke)


class ReleaseSmokeTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)

    def assets(self):
        for name in smoke.ASSETS:
            (self.root / name).write_bytes(name.encode())
        self.checksums()

    def checksums(self):
        (self.root / "checksums.txt").write_text("".join(
            f"{hashlib.sha256((self.root / name).read_bytes()).hexdigest()}  {name}\n"
            for name in smoke.ASSETS), encoding="utf-8")

    def skill(self):
        root = self.root / "easyeda-agent"
        root.mkdir()
        (root / "SKILL.md").write_text('---\nmetadata:\n  version: "1.4.2"\n---\n[guide](references/guide.md)\n', encoding="utf-8")
        (root / "references").mkdir()
        (root / "references/guide.md").write_text('[home](../SKILL.md)\n[web](https://example.com/never-fetched)\n', encoding="utf-8")
        (root / "scripts").mkdir()
        for name in smoke.EXECUTABLE_HELPERS:
            path = root / name
            path.write_text('#!/usr/bin/env python3\n', encoding="utf-8")
            path.chmod(0o755)
        return root

    def archive(self, entries):
        path = self.root / "skills.tar.gz"
        with tarfile.open(path, "w:gz") as archive:
            for name, kind in entries:
                member = tarfile.TarInfo(name)
                member.mode = 0o755
                if kind == "symlink":
                    member.type, member.linkname = tarfile.SYMTYPE, "../../outside"
                    archive.addfile(member)
                else:
                    member.size = 1
                    archive.addfile(member, io.BytesIO(b"x"))
        return path

    def test_complete_checksums_and_corruption(self):
        self.assets()
        self.assertEqual(smoke.check_assets(self.root), list(smoke.ASSETS))
        (self.root / "easyeda_windows_amd64.exe").write_bytes(b"corrupted")
        with self.assertRaisesRegex(ValueError, "checksum mismatch"):
            smoke.check_assets(self.root)

    def metadata(self, value=None):
        value = value if value is not None else {
            "schemaVersion": 1, "kind": "local-dev", "version": "1.18.0-dev.1",
            "sourceRevision": "a" * 40, "sourceBranch": "dev",
            "sourceDirty": False, "versionInjected": True}
        path = self.root / "local-dev.json"
        path.write_text(json.dumps(value))
        self.checksums()
        with (self.root / "checksums.txt").open("a") as stream:
            stream.write(f"{smoke.digest(path)}  local-dev.json\n")
        return value

    def test_local_snapshot_and_legacy_assets(self):
        self.assets()
        self.assertEqual(smoke.check_assets(self.root, "v1.18.0-dev.1", True), list(smoke.ASSETS))
        self.metadata()
        self.assertEqual(smoke.check_assets(self.root, "v1.18.0-dev.1", True),
                         [*smoke.ASSETS, "local-dev.json"])
        for tag, local in [("v1.18.0-dev.1", False), ("v1.18.1", False),
                           ("v1.18.1", True), (None, True)]:
            with self.subTest(tag=tag, local=local), self.assertRaises(ValueError):
                smoke.check_assets(self.root, tag, local)
        self.checksums()  # Even unlisted metadata must be rejected in release mode.
        with self.assertRaises(ValueError):
            smoke.check_assets(self.root, "v1.18.1")

    def test_invalid_local_metadata_with_valid_hash(self):
        self.assets()
        baseline = self.metadata()
        changes = {"schemaVersion": [True, 1.0, 2], "kind": ["release"],
                   "version": ["1.18.0-dev.2"], "sourceRevision": ["A" * 40, "a" * 39, 1],
                   "sourceBranch": ["main"], "sourceDirty": [0, True],
                   "versionInjected": [1, False]}
        for key, values in changes.items():
            for value in values:
                with self.subTest(key=key, value=value):
                    self.metadata(dict(baseline, **{key: value}))
                    with self.assertRaises(ValueError):
                        smoke.check_assets(self.root, "v1.18.0-dev.1", True)
        self.metadata([])
        with self.assertRaises(ValueError):
            smoke.check_assets(self.root, "v1.18.0-dev.1", True)

    def test_local_metadata_integrity_and_manifest(self):
        self.assets()
        path = self.root / "local-dev.json"
        for fault in ["unlisted", "missing", "empty", "symlink", "dangling", "corrupt", "duplicate", "unknown"]:
            with self.subTest(fault=fault):
                if path.is_symlink() or path.exists():
                    path.unlink()
                self.metadata()
                manifest = self.root / "checksums.txt"
                if fault == "unlisted":
                    self.checksums()
                elif fault in {"missing", "symlink", "dangling"}:
                    path.unlink()
                    if fault != "missing":
                        try:
                            path.symlink_to(self.root / ("install.sh" if fault == "symlink" else "absent"))
                        except OSError as error:
                            if os.name == "nt" and (error.errno in {errno.EACCES, errno.EPERM}
                                                    or getattr(error, "winerror", None) == 1314):
                                self.skipTest("Windows runner lacks symbolic-link creation privileges")
                            raise
                elif fault == "empty":
                    path.write_bytes(b"")
                elif fault == "corrupt":
                    path.write_text("{}")
                else:
                    with manifest.open("a") as stream:
                        stream.write(manifest.read_text().splitlines()[-1] + "\n" if fault == "duplicate"
                                     else "0" * 64 + "  unknown.json\n")
                with self.assertRaises(ValueError):
                    smoke.check_assets(self.root, "v1.18.0-dev.1", True)

    def test_local_metadata_symlink_rejection_without_os_privileges(self):
        self.assets()
        self.metadata()
        original = Path.is_symlink
        with patch.object(Path, "is_symlink", lambda path: path.name == "local-dev.json" or original(path)):
            with self.assertRaisesRegex(ValueError, "missing/empty asset: local-dev.json"):
                smoke.check_assets(self.root, "v1.18.0-dev.1", True)

    def test_minor_release_checks_attached_evidence(self):
        self.assets()
        ids = ("M1", "F1", "F2", "E1", "L1", "L2", "N1", "R1", "E2E")
        files = {
            "test-report.md": ("# Report\n## 现场回读\nFresh saved objects.\n"
                               + "".join(f"| {case_id} | pass | Frozen fresh readback and evidence sha256 for {case_id}. |\n"
                                         for case_id in ids)
                               + "## 独立复核\nBlind evidence review.\n").encode(),
            "baseline.md": b"# Baseline\nRaw customer input.\n",
            "test-cases.md": ("# Cases\n"
                              + "".join(f"| {case_id} | Input | Expected |\n" for case_id in ids)).encode(),
        }
        manifest = {"schemaVersion": 1, "version": "v1.6.0", "result": "pass",
                    "independentReview": "pass",
                    "sha256": {name: hashlib.sha256(data).hexdigest() for name, data in files.items()}}
        bundle = self.root / smoke.EVIDENCE_ASSET
        with zipfile.ZipFile(bundle, "w") as archive:
            archive.writestr("manifest.json", json.dumps(manifest))
            for name, data in files.items():
                archive.writestr(name, data)
        checksum = hashlib.sha256(bundle.read_bytes()).hexdigest()
        with (self.root / "checksums.txt").open("a") as stream:
            stream.write(f"{checksum}  {smoke.EVIDENCE_ASSET}\n")
        self.assertEqual(smoke.check_assets(self.root, "v1.6.0")[-1], smoke.EVIDENCE_ASSET)
        with self.assertRaisesRegex(ValueError, "unexpected/duplicate"):
            smoke.check_assets(self.root, "v1.6.0-dev.1")
        manifest["result"] = "in-progress"
        with zipfile.ZipFile(bundle, "w") as archive:
            archive.writestr("manifest.json", json.dumps(manifest))
            for name, data in files.items():
                archive.writestr(name, data)
        checksum = hashlib.sha256(bundle.read_bytes()).hexdigest()
        rows = (self.root / "checksums.txt").read_text().splitlines()
        rows[-1] = f"{checksum}  {smoke.EVIDENCE_ASSET}"
        (self.root / "checksums.txt").write_text("\n".join(rows) + "\n")
        with self.assertRaisesRegex(ValueError, "verdict missing"):
            smoke.check_assets(self.root, "v1.6.0")

    def test_checksum_missing_duplicate_unknown_and_traversal(self):
        self.assets()
        path = self.root / "checksums.txt"
        original = path.read_text(encoding="utf-8")
        for changed in [original.splitlines()[0] + "\n", original + original.splitlines()[0] + "\n",
                        original.replace("install.sh", "../install.sh"), original.replace("install.sh", "unknown.sh")]:
            with self.subTest(manifest=changed[-90:]):
                path.write_text(changed, encoding="utf-8")
                with self.assertRaises(ValueError):
                    smoke.check_assets(self.root)

    def test_connector_identity_version_and_entry(self):
        path = self.root / "connector.eext"
        manifest = {"name": "easyeda-agent-connector", "version": "1.4.2", "uuid": "a" * 32,
                    "entry": "./dist/index", "engines": {"eda": "~3.2.0"}}
        for key, bad in [(None, None), ("version", "1.3.2"), ("uuid", ""), ("entry", "missing"), ("name", "other")]:
            with self.subTest(key=key):
                value = dict(manifest)
                if key:
                    value[key] = bad
                with zipfile.ZipFile(path, "w") as archive:
                    archive.writestr("extension.json", json.dumps(value))
                    archive.writestr("dist/index.js", "compiled-code")
                if key:
                    with self.assertRaises(ValueError):
                        smoke.check_connector(path, "1.4.2")
                else:
                    self.assertFalse(smoke.check_connector(path, "1.4.2")["runtimeTested"])

    def test_skill_links_version_and_permissions(self):
        root = self.skill()
        result = smoke.check_skill(root, "1.4.2")
        self.assertEqual(result["localLinks"], 2)
        with self.assertRaisesRegex(ValueError, "version mismatch"):
            smoke.check_skill(root, "1.4.1")
        guide = root / "references/guide.md"
        for text, expected in [("[lost](missing.md)", "broken Skill link"), ("[outside](../../outside.md)", "escapes package")]:
            guide.write_text(text, encoding="utf-8")
            with self.assertRaisesRegex(ValueError, expected):
                smoke.check_skill(root, "1.4.2")
        guide.write_text("ok", encoding="utf-8")
        modes = {f"easyeda-agent/{name}": 0o755 for name in smoke.EXECUTABLE_HELPERS}
        modes["easyeda-agent/scripts/lint.sh"] = 0o644
        with self.assertRaisesRegex(ValueError, "not executable"):
            smoke.check_skill(root, "1.4.2", modes)

    def test_extract_rejects_paths_links_and_duplicates(self):
        bad = [[("../outside", "file")], [("easyeda-agent/../../outside", "file")],
               [("easyeda-agent/escape", "symlink")], [("/easyeda-agent/a", "file")],
               [("easyeda-agent/a", "file"), ("easyeda-agent/a", "file")],
               [("easyeda-agent/C:/outside", "file")], [("easyeda-agent/./a", "file")]]
        for entries in bad:
            with self.subTest(entries=entries):
                with self.assertRaises(ValueError):
                    smoke.extract_skill(self.archive(entries), self.root / "unpack")
        self.assertFalse((self.root / "outside").exists())

    def test_extract_preserves_archive_modes(self):
        root, modes = smoke.extract_skill(self.archive([("easyeda-agent/SKILL.md", "file")]), self.root / "unpack")
        self.assertEqual((root / "SKILL.md").read_bytes(), b"x")
        self.assertEqual(modes["easyeda-agent/SKILL.md"], 0o755)

    def test_native_platform_selection_is_explicit(self):
        self.assertEqual(smoke.native_asset("Windows", "AMD64"), "easyeda_windows_amd64.exe")
        self.assertEqual(smoke.native_asset("Darwin", "arm64"), "easyeda_darwin_arm64")
        self.assertEqual(smoke.native_asset("Linux", "aarch64"), "easyeda_linux_arm64")
        with self.assertRaisesRegex(ValueError, "no native release asset"):
            smoke.native_asset("Windows", "ARM64")

    def test_cli_fails_on_wrong_version_before_running_other_commands(self):
        binary = self.root / "downloaded-cli"
        binary.write_bytes(b"test-placeholder")
        completed = smoke.subprocess.CompletedProcess([], 0, "easyeda-agent v1.3.2\n", "")
        with patch.object(smoke.subprocess, "run", return_value=completed) as run:
            with self.assertRaisesRegex(ValueError, "version mismatch"):
                smoke.smoke_cli(binary, "v1.4.2", self.root)
            self.assertEqual(run.call_count, 1)
            command = run.call_args.args[0]
            self.assertEqual(command[0], str(self.root / ("easyeda.exe" if os.name == "nt" else "easyeda")))
            self.assertEqual(binary.read_bytes(), b"test-placeholder")
            self.assertIn("1-1", command)
            self.assertFalse(run.call_args.kwargs.get("shell", False))

    def test_version_form_follows_release_check(self):
        # CI smokes whatever extension.json carries, which between releases is a
        # -dev.N build. --local-dev selects that form, mirroring release-check.py;
        # without it the release form stays strict, so an unreleasable version can
        # never pass as a release.
        def version_rejected(version, local_dev):
            argv = ["release-smoke.py", "--version", version,
                    "--binary", str(self.root / "absent-cli"),
                    "--skill-dir", str(self.root / "absent-skill")]
            if local_dev:
                argv.append("--local-dev")
            with patch("sys.argv", argv), contextlib.redirect_stderr(io.StringIO()), \
                    contextlib.redirect_stdout(io.StringIO()):
                with self.assertRaises(SystemExit) as raised:
                    smoke.main()
            # argparse exits 2 when it rejects --version. A version that passes the
            # check goes on to the absent assets and exits 1 instead.
            return raised.exception.code == 2

        self.assertFalse(version_rejected("v1.5.2", local_dev=False))
        self.assertFalse(version_rejected("v1.5.3-dev.3", local_dev=True))
        self.assertTrue(version_rejected("v1.5.3-dev.3", local_dev=False))
        self.assertTrue(version_rejected("v1.5.2", local_dev=True))
        self.assertTrue(version_rejected("v1.5.3-dev.0", local_dev=True))


if __name__ == "__main__":
    unittest.main()
