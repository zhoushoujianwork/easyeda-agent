import copy
import contextlib
import importlib.util
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock


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

    def check(self, window_id=None):
        return smoke.check_health(json.dumps(self.health), "v1.6.0-dev.21", "project", "page", "schematic", window_id)

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

    def test_explicit_window_disambiguates_same_document(self):
        other = copy.deepcopy(self.health["found"]["raw"]["windows"][0])
        other["windowId"] = "other"
        other["connectorVersion"] = "1.2.8"
        self.health["found"]["raw"]["windows"].append(other)
        self.assertEqual(self.check("target"), "target")
        self.assert_blocked()

    def test_explicit_window_never_falls_back(self):
        for window_id in ("missing", ""):
            with self.subTest(window_id=window_id), self.assertRaises(smoke.CheckError) as caught:
                self.check(window_id)
            self.assertEqual(caught.exception.status, "blocked")

    def test_explicit_window_still_checks_document(self):
        self.health["found"]["raw"]["windows"][0]["context"]["documentUuid"] = "wrong"
        with self.assertRaises(smoke.CheckError) as caught:
            self.check("target")
        self.assertEqual(caught.exception.status, "blocked")


class MainRoutingTests(unittest.TestCase):
    def run_smoke(self, *, explicit=True, document_type="schematic", change_window=False,
                  initial_window="target"):
        calls = []
        context = {"projectUuid": "project", "documentUuid": "page", "documentType": document_type}

        def fake_run(cli, name, args, out_dir, timeout):
            calls.append(args)
            if args == ["--version"]:
                return "easyeda v1.7.1-dev.4"
            if args == ["health"]:
                window_id = "replacement" if change_window and name == "06-health-after" else initial_window
                return json.dumps({"status": "found", "versionGate": {"cli": "v1.7.1-dev.4"},
                                   "found": {"raw": {"version": "v1.7.1-dev.4", "windows": [{
                                       "windowId": window_id, "connectorVersion": "1.7.1-dev.4", "context": context}]}},
                                   "hostCompatibility": {"findings": [{"windowId": window_id, "severity": "ok"}]}})
            if args == ["actions"]:
                return '["test.action"]'
            return json.dumps({"ok": True, "context": context, "result": {}})

        with tempfile.TemporaryDirectory() as directory:
            argv = [str(SCRIPT), "--expected-version", "v1.7.1-dev.4", "--project", "project", "--doc", "page",
                    "--type", document_type, "--out", directory]
            if explicit:
                argv += ["--window", "target"]
            with mock.patch.object(smoke.sys, "argv", argv), mock.patch.object(smoke, "run_command", fake_run), contextlib.redirect_stdout(io.StringIO()):
                code = smoke.main()
            summary = json.loads((Path(directory) / "summary.json").read_text())
        return code, calls, summary

    def test_every_document_read_routes_to_resolved_window(self):
        for explicit in (False, True):
            for document_type in ("schematic", "pcb"):
                with self.subTest(explicit=explicit, document_type=document_type):
                    code, calls, summary = self.run_smoke(explicit=explicit, document_type=document_type)
                    self.assertEqual(code, 0)
                    reads = [args for args in calls if args[:1] in (["project"], ["sch"], ["pcb"])]
                    self.assertEqual(len(reads), 3)
                    for args in reads:
                        self.assertEqual(args[-6:], ["--window", "target", "--project", "project", "--doc", "page"])
                    self.assertEqual(summary["windowId"], "target")
                    self.assertEqual(summary["status"], "pass")

    def test_missing_explicit_window_stops_before_document_reads(self):
        code, calls, summary = self.run_smoke(initial_window="other")
        self.assertEqual(code, 1)
        self.assertEqual(calls, [["--version"], ["health"]])
        self.assertEqual(summary["status"], "blocked")

    def test_window_change_after_reads_is_not_success(self):
        code, calls, summary = self.run_smoke(change_window=True)
        self.assertEqual(code, 1)
        self.assertEqual(summary["status"], "blocked")
        self.assertIn("window target", summary["reason"])


if __name__ == "__main__":
    unittest.main()
