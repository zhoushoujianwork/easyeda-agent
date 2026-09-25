import copy
import importlib.util
import json
from pathlib import Path
import unittest


SCRIPT = Path(__file__).resolve().parents[1] / "cli-live-smoke.py"
spec = importlib.util.spec_from_file_location("cli_live_smoke", SCRIPT)
smoke = importlib.util.module_from_spec(spec)
spec.loader.exec_module(smoke)


class TargetHealthTests(unittest.TestCase):
    def setUp(self):
        self.health = {
            "status": "found",
            "found": {"raw": {"version": "v1.6.0-dev.21", "windows": [{
                "windowId": "target", "connectorVersion": "1.6.0-dev.21",
                "context": {"projectUuid": "project", "documentUuid": "page", "documentType": "schematic"},
            }]}},
            "versionGate": {"cli": "v1.6.0-dev.21", "verdict": "ok"},
            "hostCompatibility": {"verdict": "ok", "findings": [{"windowId": "target", "severity": "ok"}]},
        }

    def check(self):
        return smoke.check_health(json.dumps(self.health), "v1.6.0-dev.21", "project", "page", "schematic")

    def assert_blocked(self):
        with self.assertRaises(smoke.CheckError) as caught:
            self.check()
        self.assertEqual(caught.exception.status, "blocked")

    def test_single_target(self):
        self.assertEqual(self.check(), "target")

    def test_unrelated_old_connector_and_host_do_not_change_target(self):
        self.health["found"]["raw"]["windows"].append({
            "windowId": "other", "connectorVersion": "1.2.8",
            "context": {"projectUuid": "other-project", "documentUuid": "other-page", "documentType": "schematic"},
        })
        self.health["versionGate"]["verdict"] = "block"
        self.health["hostCompatibility"] = {"verdict": "block", "findings": [
            {"windowId": "other", "severity": "block"}, {"windowId": "target", "severity": "ok"},
        ]}
        self.assertEqual(self.check(), "target")

    def test_target_old_connector_still_blocks(self):
        self.health["found"]["raw"]["windows"][0]["connectorVersion"] = "1.2.8"
        self.assert_blocked()

    def test_cli_and_daemon_must_still_match(self):
        original = copy.deepcopy(self.health)
        for path in ("cli", "daemon"):
            with self.subTest(component=path):
                self.health = copy.deepcopy(original)
                if path == "cli":
                    self.health["versionGate"]["cli"] = "v1.7.0"
                else:
                    self.health["found"]["raw"]["version"] = "v1.7.0"
                self.assert_blocked()

    def test_missing_or_duplicate_target_blocks(self):
        windows = self.health["found"]["raw"]["windows"]
        for replacements in ([], windows * 2):
            with self.subTest(count=len(replacements)):
                self.health["found"]["raw"]["windows"] = replacements
                self.assert_blocked()

    def test_wrong_document_identity_blocks(self):
        context = self.health["found"]["raw"]["windows"][0]["context"]
        for field in ("projectUuid", "documentUuid", "documentType"):
            original = context[field]
            with self.subTest(field=field):
                context[field] = "wrong"
                self.assert_blocked()
                context[field] = original

    def test_target_host_requires_one_passing_finding(self):
        for findings in ([], [{"windowId": "other", "severity": "ok"}],
                         [{"windowId": "target", "severity": "block"}],
                         [{"windowId": "target", "severity": "warn"}],
                         [{"windowId": "target", "severity": "ok"}] * 2):
            with self.subTest(findings=findings):
                self.health["hostCompatibility"]["findings"] = findings
                self.assert_blocked()

    def test_missing_window_id_blocks(self):
        del self.health["found"]["raw"]["windows"][0]["windowId"]
        self.assert_blocked()

    def test_missing_daemon_blocks(self):
        self.health["status"] = "not-found"
        self.assert_blocked()


if __name__ == "__main__":
    unittest.main()
