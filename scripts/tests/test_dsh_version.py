"""Offline regression for the DSH bundle version sync helper; all writes stay in temp dirs."""

import importlib.util
import json
from pathlib import Path
import tempfile
import unittest


def load_script(name):
    spec = importlib.util.spec_from_file_location(
        name.replace("-", "_"), Path(__file__).resolve().parents[1] / f"{name}.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


sync = load_script("sync-dsh-version")


class SyncDshVersionTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)

    def write(self, name, data):
        (self.root / name).write_text(json.dumps(data, indent=2) + "\n")

    def write_pair(self, manifest="1.13.1", lock="1.13.1", lock_packages="1.13.1"):
        self.write("package.json", {"name": "easyeda-agent-dsh", "version": manifest})
        self.write("package-lock.json", {"name": "easyeda-agent-dsh", "version": lock,
                                         "packages": {"": {"version": lock_packages}}})

    def test_sync_updates_manifest_and_both_lock_versions(self):
        self.write_pair()
        changes = sync.sync_version(self.root, "v1.19.0")
        self.assertEqual(len(changes), 2)
        self.assertEqual(json.loads((self.root / "package.json").read_text())["version"], "1.19.0")
        lock = json.loads((self.root / "package-lock.json").read_text())
        self.assertEqual(lock["version"], "1.19.0")
        self.assertEqual(lock["packages"][""]["version"], "1.19.0")
        self.assertEqual((self.root / "package.json").read_text(), json.dumps(
            {"name": "easyeda-agent-dsh", "version": "1.19.0"}, indent=2) + "\n")

    def test_second_run_is_a_no_op(self):
        self.write_pair()
        sync.sync_version(self.root, "1.19.0")
        before = (self.root / "package.json").read_bytes(), (self.root / "package-lock.json").read_bytes()
        self.assertEqual(sync.sync_version(self.root, "1.19.0"), [])
        self.assertEqual(before, ((self.root / "package.json").read_bytes(),
                                  (self.root / "package-lock.json").read_bytes()))

    def test_check_detects_drift_without_writing(self):
        self.write_pair()
        before = (self.root / "package.json").read_bytes()
        with self.assertRaisesRegex(ValueError, "package.json: 版本"):
            sync.sync_version(self.root, "1.19.0", check=True)
        self.assertEqual(before, (self.root / "package.json").read_bytes())

    def test_check_requires_the_lock_root_package_version(self):
        self.write_pair(manifest="1.19.0", lock="1.19.0", lock_packages="1.13.1")
        with self.assertRaisesRegex(ValueError, "package-lock.json"):
            sync.sync_version(self.root, "1.19.0", check=True)
        sync.sync_version(self.root, "1.19.0")
        self.assertEqual(sync.sync_version(self.root, "1.19.0", check=True), [])

    def test_missing_manifest_fails(self):
        with self.assertRaisesRegex(ValueError, "不存在"):
            sync.sync_version(self.root, "1.19.0")


if __name__ == "__main__":
    unittest.main()
