"""Offline release preparation regression; all writes stay in temporary repos."""

import importlib.util
import hashlib
import json
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest
import zipfile


def load_script(name):
    spec = importlib.util.spec_from_file_location(name.replace("-", "_"), Path(__file__).resolve().parents[1] / f"{name}.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


pack = load_script("pack-skill")
release = load_script("release-check")
smoke = load_script("release-smoke")


class TrackedSkillPackageTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.repo = Path(self.temp.name)
        self.skill = self.repo / pack.SKILL
        (self.skill / "references").mkdir(parents=True)
        self.run_git("init", "-q")
        self.run_git("config", "user.name", "Release fixture")
        self.run_git("config", "user.email", "fixture@example.invalid")
        (self.skill / "SKILL.md").write_text('---\nmetadata:\n  version: "1.4.2"\n---\n[Guide](references/guide.md)\n')
        (self.skill / "references/guide.md").write_text("Release fixture\n")
        self.run_git("add", ".agents/skills")
        self.run_git("commit", "-qm", "fixture")

    def run_git(self, *args):
        return subprocess.check_output(["git", "-C", str(self.repo), *args], stderr=subprocess.STDOUT)

    def test_only_tracked_and_staged_files_are_packaged(self):
        (self.skill / "scratch.md").write_text("Do not ship\n")
        (self.skill / "references/new.md").write_text("Reviewed addition\n")
        self.run_git("add", ".agents/skills/easyeda-agent/references/new.md")
        output = self.repo / "dist/skills.tar.gz"
        self.assertEqual(pack.pack_skill(self.repo, output), 3)
        with tarfile.open(output) as archive:
            self.assertEqual(set(archive.getnames()), {
                "easyeda-agent/SKILL.md", "easyeda-agent/references/guide.md", "easyeda-agent/references/new.md",
            })
            self.assertTrue(all(item.isfile() for item in archive.getmembers()))
        original = output.read_bytes()
        pack.pack_skill(self.repo, output)
        self.assertEqual(output.read_bytes(), original)

    def test_local_link_to_untracked_file_fails(self):
        (self.skill / "scratch.md").write_text("Unreviewed\n")
        (self.skill / "references/guide.md").write_text("[Scratch](../scratch.md)\n")
        with self.assertRaisesRegex(ValueError, "not included in the tracked package"):
            pack.check_skill(self.repo)

    def test_source_tree_doc_link_does_not_escape_installed_skill(self):
        (self.repo / "docs").mkdir()
        (self.repo / "docs/design.md").write_text("Repository-only detail\n")
        (self.skill / "references/guide.md").write_text("[Design](../../../docs/design.md)\n")
        with self.assertRaisesRegex(ValueError, "escapes the installed skill"):
            pack.check_skill(self.repo)

    def test_symlink_is_not_silently_dropped_by_updater(self):
        path = self.skill / "references/guide.md"
        path.unlink()
        path.symlink_to(self.repo / "outside.md")
        (self.repo / "outside.md").write_text("outside\n")
        with self.assertRaisesRegex(ValueError, "symlinks are unsupported|escapes the installed skill"):
            pack.check_skill(self.repo)

    def test_missing_tracked_file_fails(self):
        (self.skill / "references/guide.md").unlink()
        with self.assertRaisesRegex(ValueError, "missing|not included"):
            pack.check_skill(self.repo)

    def test_web_links_and_anchors_need_no_network(self):
        (self.skill / "references/guide.md").write_text("[Web](https://example.invalid/docs) [Here](#part)\n")
        self.assertEqual(len(pack.check_skill(self.repo)), 2)


class ReleaseVersionAndAssetTests(unittest.TestCase):
    def test_local_prerelease_is_not_a_publishable_release(self):
        for file in ["extension/extension.json", "extension/package.json", "extension/package-lock.json", ".agents/skills/easyeda-agent/SKILL.md", "extension/CHANGELOG.md"]:
            path = self.repo / file
            path.write_text(path.read_text().replace("1.4.2", "1.4.3-dev.1"))
        self.assertEqual(release.check_sources(self.repo, "v1.4.3-dev.1", local_dev=True), "1.4.3-dev.1")
        with self.assertRaises(ValueError):
            release.check_sources(self.repo, "v1.4.3-dev.1")
        with self.assertRaises(ValueError):
            release.check_sources(self.repo, "v1.4.3", local_dev=True)

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.repo = Path(self.temp.name)
        (self.repo / "extension").mkdir()
        (self.repo / ".agents/skills/easyeda-agent").mkdir(parents=True)
        self.uuid = "a" * 32
        self.write_json("extension/extension.json", {"version": "1.4.2", "uuid": self.uuid})
        self.write_json("extension/package.json", {"version": "1.4.2"})
        self.write_json("extension/package-lock.json", {"version": "1.4.2", "packages": {"": {"version": "1.4.2"}}})
        (self.repo / ".agents/skills/easyeda-agent/SKILL.md").write_text('---\nmetadata:\n  version: "1.4.2"\n---\n')
        (self.repo / "extension/CHANGELOG.md").write_text("# Changes\n\n## [1.4.2]\n\nNew release\n")

    def write_json(self, path, data):
        (self.repo / path).write_text(json.dumps(data))

    def set_version(self, version):
        for file in ["extension/extension.json", "extension/package.json", "extension/package-lock.json",
                     ".agents/skills/easyeda-agent/SKILL.md", "extension/CHANGELOG.md"]:
            path = self.repo / file
            path.write_text(path.read_text().replace("1.4.2", version))

    def make_evidence(self, result="pass", review="pass", tracked=True):
        directory = self.repo / "docs/releases/evidence/v1.6.0"
        directory.mkdir(parents=True)
        case_ids = ("M1", "F1", "F2", "E1", "L1", "L2", "N1", "R1", "E2E")
        files = {
            "test-report.md": ("# Test report\n## 现场回读\nFresh live objects after save/reload.\n"
                               + "".join(f"| {case_id} | pass | Fresh readback and frozen evidence sha256 for {case_id}. |\n"
                                         for case_id in case_ids)
                               + "## 独立复核\nBlind agent checked object-level evidence.\n").encode(),
            "baseline.md": b"# Baseline\nRaw input and expected thresholds.\n",
            "test-cases.md": ("# Test cases\n"
                              + "".join(f"| {case_id} | Raw input | Expected result |\n" for case_id in case_ids)).encode(),
        }
        for name, data in files.items():
            (directory / name).write_bytes(data)
        manifest = {"schemaVersion": 1, "version": "v1.6.0", "result": result,
                    "independentReview": review,
                    "sha256": {name: hashlib.sha256(data).hexdigest() for name, data in files.items()}}
        (directory / "manifest.json").write_text(json.dumps(manifest))
        if tracked:
            subprocess.check_call(["git", "init", "-q", str(self.repo)])
            subprocess.check_call(["git", "-C", str(self.repo), "add", "docs/releases/evidence/v1.6.0"])
        return directory

    def test_sources_are_checked_without_mutation(self):
        before = {p: p.read_bytes() for p in self.repo.rglob("*") if p.is_file()}
        self.assertEqual(release.check_sources(self.repo, "v1.4.2"), "1.4.2")
        self.assertEqual(before, {p: p.read_bytes() for p in self.repo.rglob("*") if p.is_file()})
        with self.assertRaisesRegex(ValueError, "complete release tag"):
            release.check_sources(self.repo, "1.4.2")

    def test_drift_and_missing_changelog_fail(self):
        self.write_json("extension/package-lock.json", {"version": "1.4.2", "packages": {"": {"version": "0.8.17"}}})
        with self.assertRaisesRegex(ValueError, "packages"):
            release.check_sources(self.repo, "v1.4.2")
        self.write_json("extension/package-lock.json", {"version": "1.4.2", "packages": {"": {"version": "1.4.2"}}})
        (self.repo / "extension/CHANGELOG.md").write_text("## [1.4.1]\n")
        with self.assertRaisesRegex(ValueError, "no ##"):
            release.check_sources(self.repo, "v1.4.2")

    def test_connector_must_contain_exact_target_manifest(self):
        path = self.repo / "stale.eext"
        with zipfile.ZipFile(path, "w") as archive:
            archive.writestr("extension.json", json.dumps({"version": "1.4.1", "uuid": self.uuid}))
            archive.writestr("dist/index.js", "compiled")
        with self.assertRaisesRegex(ValueError, "version/UUID differs"):
            release.check_connector(path, "1.4.2", self.uuid)

    def test_checksums_have_exact_bare_asset_names(self):
        dist = self.repo / "dist"
        dist.mkdir()
        for name in release.ASSETS:
            (dist / name).write_bytes(name.encode())
        release.write_checksums(dist)
        rows = [line.split() for line in (dist / "checksums.txt").read_text().splitlines()]
        self.assertEqual([row[1] for row in rows], release.ASSETS)
        self.assertTrue(all(len(row[0]) == 64 for row in rows))
        (dist / release.ASSETS[0]).unlink()
        with self.assertRaisesRegex(ValueError, "missing/empty"):
            release.write_checksums(dist)

    def test_minor_release_requires_reviewed_tracked_evidence(self):
        self.set_version("1.6.0")
        with self.assertRaisesRegex(ValueError, "evidence missing"):
            release.check_sources(self.repo, "v1.6.0")
        directory = self.make_evidence(result="in-progress")
        with self.assertRaisesRegex(ValueError, "pass result"):
            release.check_sources(self.repo, "v1.6.0")
        manifest_path = directory / "manifest.json"
        manifest = json.loads(manifest_path.read_text())
        manifest["result"] = "pass"
        manifest_path.write_text(json.dumps(manifest))
        self.assertEqual(release.check_sources(self.repo, "v1.6.0"), "1.6.0")
        (directory / "test-report.md").write_text("changed after review\n")
        with self.assertRaisesRegex(ValueError, "SHA256 differs"):
            release.check_sources(self.repo, "v1.6.0")

    def test_declared_pass_cannot_hide_failed_report_case(self):
        self.set_version("1.6.0")
        directory = self.make_evidence()
        report = directory / "test-report.md"
        report.write_text(report.read_text().replace("| F2 | pass |", "| F2 | fail |"))
        manifest = directory / "manifest.json"
        data = json.loads(manifest.read_text())
        data["sha256"]["test-report.md"] = hashlib.sha256(report.read_bytes()).hexdigest()
        manifest.write_text(json.dumps(data))
        with self.assertRaisesRegex(ValueError, "case F2 is not pass"):
            release.check_sources(self.repo, "v1.6.0")

    def test_minor_evidence_is_packaged_and_checked_as_release_asset(self):
        self.set_version("1.6.0")
        directory = self.make_evidence()
        self.assertEqual(release.check_sources(self.repo, "v1.6.0"), "1.6.0")
        dist = self.repo / "dist"
        dist.mkdir()
        for name in release.ASSETS:
            (dist / name).write_bytes(name.encode())
        bundle = dist / release.EVIDENCE_ASSET
        release.package_evidence(self.repo, "v1.6.0", bundle)
        with zipfile.ZipFile(bundle) as archive:
            self.assertEqual(archive.namelist(), ["manifest.json", *release.EVIDENCE_FILES])
        release.write_checksums(dist, "v1.6.0")
        self.assertIn(release.EVIDENCE_ASSET, (dist / "checksums.txt").read_text())
        bundle.write_bytes(bundle.read_bytes() + b"tampered")
        checksums = (dist / "checksums.txt").read_text()
        self.assertNotEqual(hashlib.sha256(bundle.read_bytes()).hexdigest(),
                            next(line.split()[0] for line in checksums.splitlines()
                                 if line.endswith(release.EVIDENCE_ASSET)))
        release.package_evidence(self.repo, "v1.6.0", bundle)
        release.check_evidence_archive(bundle, release.evidence_files(self.repo, "v1.6.0"))
        (directory / "baseline.md").write_bytes(b"unreviewed baseline")
        with self.assertRaisesRegex(ValueError, "SHA256 differs"):
            release.check_evidence_archive(bundle, release.evidence_files(self.repo, "v1.6.0"))

    def test_dev_build_removes_stale_minor_evidence_asset(self):
        self.set_version("1.6.0-dev.1")
        self.assertEqual(release.check_sources(self.repo, "v1.6.0-dev.1", local_dev=True), "1.6.0-dev.1")
        dist = self.repo / "dist"
        dist.mkdir()
        stale = dist / release.EVIDENCE_ASSET
        stale.write_text("old release")
        release.package_evidence(self.repo, "v1.6.0-dev.1", stale)
        self.assertFalse(stale.exists())

    def test_scoped_basic_evidence_is_strict_in_source_and_archive_checks(self):
        self.set_version("1.6.0")
        directory = self.make_evidence()
        manifest = json.loads((directory / "manifest.json").read_text())
        manifest.update(schemaVersion=2, acceptanceScope="basic-cli",
                        deferredScope="advanced-cli", deferredUntil="next-release")
        ids = sorted(release.BASIC_CASES | release.ADVANCED_CASES)
        files = {
            "baseline.md": b"Personal workspace; existing custom config; advanced validation next release.\n",
            "test-cases.md": "".join(f"| {cid} | Input | Expected result |\n" for cid in ids).encode(),
            "test-report.md": ("## 现场回读\n"
                               + "".join(f"| {cid} | {'pass' if cid.startswith('B') else 'not-run'} | "
                                         f"Frozen readback evidence or explicit next-release deferral: {cid}. |\n"
                                         for cid in ids)
                               + "## 独立复核\nReviewed basic cases only.\n").encode(),
        }
        dist = self.repo / "dist"
        dist.mkdir()
        for name in release.ASSETS:
            (dist / name).write_bytes(name.encode())

        def write(data, documents):
            data = dict(data, sha256={name: hashlib.sha256(content).hexdigest()
                                     for name, content in documents.items()})
            for name, content in documents.items():
                (directory / name).write_bytes(content)
            (directory / "manifest.json").write_text(json.dumps(data))
            # Deliberately construct the archive independently, so smoke must
            # reject bad evidence even if it was not made by our packager.
            with zipfile.ZipFile(dist / release.EVIDENCE_ASSET, "w") as archive:
                archive.writestr("manifest.json", json.dumps(data))
                for name in release.EVIDENCE_FILES:
                    archive.writestr(name, documents[name])
            release.write_checksums(dist, "v1.6.0")

        write(manifest, files)
        self.assertEqual(release.check_sources(self.repo, "v1.6.0"), "1.6.0")
        self.assertIn(release.EVIDENCE_ASSET, smoke.check_assets(dist, "v1.6.0"))
        mutations = [
            (dict(manifest, acceptanceScope="anything"), files),
            (dict(manifest, schemaVersion=1), files),
            (dict(manifest, deferredUntil=""), files),
            (dict(manifest, independentReview="in-progress"), files),
        ]
        for old, new in [(b"| B05 | pass |", b"| B05 | fail |"),
                         (b"| B05 | pass |", b"| B05 | not-run |"),
                         (b"| A00 | not-run |", b"| A00 | pass |")]:
            mutations.append((manifest, dict(files, **{
                "test-report.md": files["test-report.md"].replace(old, new)})))
        for cid in ("B10", "A06"):
            mutations.append((manifest, {name: b"\n".join(
                line for line in content.splitlines() if not line.startswith(f"| {cid} |".encode()))
                for name, content in files.items()}))
        for number, (data, documents) in enumerate(mutations):
            with self.subTest(mutation=number):
                write(data, documents)
                with self.assertRaises(ValueError):
                    release.check_sources(self.repo, "v1.6.0")
                with self.assertRaises(ValueError):
                    smoke.check_assets(dist, "v1.6.0")

    def test_historical_minor_release_is_not_retroactively_gated(self):
        self.set_version("1.5.0")
        self.assertEqual(release.check_sources(self.repo, "v1.5.0"), "1.5.0")
        self.assertNotIn(release.EVIDENCE_ASSET, release.release_assets("v1.5.0"))


if __name__ == "__main__":
    unittest.main()
