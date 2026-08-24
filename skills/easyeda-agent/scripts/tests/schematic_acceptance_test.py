#!/usr/bin/env python3
"""Offline tests for the multi-page schematic acceptance helpers."""

import importlib.util
import unittest
from pathlib import Path


SCRIPT = Path(__file__).resolve().parents[1] / "schematic-acceptance.py"
SPEC = importlib.util.spec_from_file_location("schematic_acceptance", SCRIPT)
ACCEPTANCE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(ACCEPTANCE)


def gate_with(check_findings, extra_failure=None):
    stages = [
        {"name": "layout-lint", "status": "pass"},
        {"name": "clusters", "status": "pass"},
        {
            "name": "check",
            "status": "fail",
            "detail": {"findings": check_findings},
        },
        {"name": "bridge-check", "status": "pass"},
        {"name": "drc", "status": "pass"},
    ]
    if extra_failure:
        next(stage for stage in stages if stage["name"] == extra_failure)["status"] = "fail"
    return {"verdict": "fail", "stages": stages}


class PageSetTests(unittest.TestCase):
    def test_page_order_is_ignored(self):
        result = ACCEPTANCE.compare_page_sets(["MCU", "POWER"], ["POWER", "MCU"])
        self.assertTrue(result["matched"])

    def test_duplicate_pages_are_rejected(self):
        result = ACCEPTANCE.compare_page_sets(["MCU", "MCU"], ["MCU"])
        self.assertFalse(result["matched"])
        self.assertEqual(result["actualDuplicates"], ["MCU"])


class WindowSelectionTests(unittest.TestCase):
    def test_requested_project_never_falls_back_to_another_window(self):
        health = {
            "found": {
                "raw": {
                    "windows": [{"context": {"projectName": "another-project"}}]
                }
            }
        }
        with self.assertRaises(ACCEPTANCE.AcceptanceError):
            ACCEPTANCE.find_window(health, "target-project")


class PinComparisonTests(unittest.TestCase):
    def test_pin_and_nc_mismatches_are_rejected(self):
        result = ACCEPTANCE.compare_pins(
            [("U1.1", "3V3"), ("U1.2", "GND")],
            [("U1.1", "3V3"), ("U1.2", None)],
        )
        self.assertFalse(result["matched"])
        self.assertEqual(result["mismatchedNets"][0]["pin"], "U1.2")


class TitleblockAllowlistTests(unittest.TestCase):
    def test_exact_titleblock_gap_is_allowed(self):
        gate = gate_with([{"type": "missing-titleblock"}])
        self.assertTrue(ACCEPTANCE.titleblock_only_failure(gate))

    def test_other_finding_remains_blocking(self):
        gate = gate_with(
            [{"type": "missing-titleblock"}, {"type": "floating-pin"}]
        )
        self.assertFalse(ACCEPTANCE.titleblock_only_failure(gate))

    def test_other_failed_stage_remains_blocking(self):
        gate = gate_with([{"type": "missing-titleblock"}], "drc")
        self.assertFalse(ACCEPTANCE.titleblock_only_failure(gate))

    def test_missing_or_skipped_stage_is_not_a_full_gate(self):
        gate = gate_with([{"type": "missing-titleblock"}])
        gate["stages"][-1]["status"] = "skipped"
        self.assertFalse(ACCEPTANCE.titleblock_only_failure(gate))


if __name__ == "__main__":
    unittest.main()
