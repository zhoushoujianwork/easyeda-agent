#!/usr/bin/env python3
"""Run one deterministic acceptance pass for a multi-page schematic."""

import argparse
import json
import re
import subprocess
import sys
from pathlib import Path


FULL_GATE_STAGES = {"layout-lint", "clusters", "check", "bridge-check", "drc"}


class AcceptanceError(RuntimeError):
    pass


def parse_args(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--project", help="EasyEDA project name or UUID")
    parser.add_argument("--spec", type=Path, help="S0 design spec with pages[]")
    parser.add_argument("--golden", type=Path, help="Golden nets[].members and nc[] JSON")
    parser.add_argument("--artifacts", type=Path, default=Path(".easyeda/artifacts"))
    parser.add_argument("--report-out", type=Path, help="Write the JSON report to this path")
    parser.add_argument("--export", action="store_true", help="Export PAGE-final.png for each page")
    parser.add_argument("--skip-gate", action="store_true", help="Skip per-page strict gate")
    parser.add_argument(
        "--allow-titleblock-gap",
        action="store_true",
        help="Allow only the exact v1.1.1 missing-titleblock gate failure",
    )
    parser.add_argument("--json", action="store_true", help="Emit a JSON report")
    parser.add_argument("--easyeda", default="easyeda", help="Path to the easyeda CLI")
    return parser.parse_args(argv)


def run_command(base, args, allow_failure=False, expect_json=True):
    command = [base] + list(args)
    completed = subprocess.run(command, text=True, capture_output=True, check=False)
    payload = None
    if expect_json:
        try:
            payload = json.loads(completed.stdout)
        except json.JSONDecodeError as exc:
            raise AcceptanceError(
                f"invalid JSON from {' '.join(command)}: {exc}; "
                f"stderr={completed.stderr.strip()}"
            ) from exc
    if completed.returncode and not allow_failure:
        detail = completed.stderr.strip() or completed.stdout.strip()
        raise AcceptanceError(
            f"command failed ({completed.returncode}): {' '.join(command)}: {detail}"
        )
    return completed.returncode, payload, completed.stderr.strip()


def with_project(args, project):
    values = list(args)
    if project:
        values.extend(["--project", project])
    return values


def unwrap_result(payload):
    if isinstance(payload, dict) and isinstance(payload.get("result"), dict):
        return payload["result"]
    return payload


def load_json(path):
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise AcceptanceError(f"cannot read {path}: {exc}") from exc


def find_window(health, project):
    windows = health.get("found", {}).get("raw", {}).get("windows", [])
    if not windows:
        raise AcceptanceError("easyeda health found no connected window")
    if project:
        for window in windows:
            context = window.get("context", {})
            if project in (context.get("projectName"), context.get("projectUuid")):
                return window
        available = [
            window.get("context", {}).get("projectName") or "<unknown>"
            for window in windows
        ]
        raise AcceptanceError(
            f"no connected window matches project {project!r}; available={available}"
        )
    return windows[0]


def compare_page_sets(actual, expected):
    actual_duplicates = duplicate_values(actual)
    expected_duplicates = duplicate_values(expected)
    return {
        "actual": actual,
        "expected": expected,
        "actualDuplicates": actual_duplicates,
        "expectedDuplicates": expected_duplicates,
        "matched": not actual_duplicates
        and not expected_duplicates
        and set(actual) == set(expected),
    }


def expected_pin_map(golden):
    records = []
    for net in golden.get("nets", []):
        for member in net.get("members", []):
            records.append((member, net.get("name")))
    records.extend((pin, None) for pin in golden.get("nc", []))
    return records


def actual_pin_records(read_payload):
    records = []
    for component in unwrap_result(read_payload).get("components", []):
        if component.get("componentType") != "part":
            continue
        designator = component.get("designator")
        for pin in component.get("pins", []):
            records.append((f"{designator}.{pin.get('number')}", pin.get("net")))
    return records


def duplicate_values(values):
    counts = {}
    for value in values:
        counts[value] = counts.get(value, 0) + 1
    return sorted(
        (value for value, count in counts.items() if count > 1), key=lambda value: str(value)
    )


def duplicate_keys(records):
    return duplicate_values([key for key, _ in records])


def compare_pins(actual, expected):
    actual_map = dict(actual)
    expected_map = dict(expected)
    missing = sorted(key for key in expected_map if key not in actual_map)
    unexpected = sorted(key for key in actual_map if key not in expected_map)
    mismatched = [
        {"pin": key, "expected": expected_map[key], "actual": actual_map[key]}
        for key in sorted(expected_map.keys() & actual_map.keys())
        if expected_map[key] != actual_map[key]
    ]
    actual_duplicates = duplicate_keys(actual)
    expected_duplicates = duplicate_keys(expected)
    return {
        "actualPinCount": len(actual),
        "expectedPinCount": len(expected),
        "actualNC": sum(1 for _, net in actual if net is None),
        "expectedNC": sum(1 for _, net in expected if net is None),
        "missingPins": missing,
        "unexpectedPins": unexpected,
        "mismatchedNets": mismatched,
        "actualDuplicatePins": actual_duplicates,
        "expectedDuplicatePins": expected_duplicates,
        "matched": not (
            missing or unexpected or mismatched or actual_duplicates or expected_duplicates
        ),
    }


def titleblock_only_failure(gate):
    gate = unwrap_result(gate)
    if gate.get("verdict") != "fail":
        return False
    stages = gate.get("stages", [])
    if {stage.get("name") for stage in stages} != FULL_GATE_STAGES:
        return False
    check = next(stage for stage in stages if stage.get("name") == "check")
    if check.get("status") != "fail":
        return False
    if any(stage.get("status") != "pass" for stage in stages if stage is not check):
        return False
    detail = unwrap_result(check.get("detail") or {})
    findings = detail.get("findings") or []
    return bool(findings) and all(
        item.get("type") == "missing-titleblock" for item in findings
    )


def safe_page_name(name):
    cleaned = re.sub(r"[^A-Za-z0-9_.-]+", "_", name).strip("._")
    return cleaned or "PAGE"


def main(argv=None):
    args = parse_args(argv)
    errors = []
    report = {
        "ok": False,
        "pages": [],
        "pageSet": {},
        "nets": {},
        "pins": None,
        "exports": [],
        "errors": errors,
    }

    _, health, _ = run_command(args.easyeda, ["health"])
    window = find_window(health, args.project)
    if not window.get("connectorVersionOk"):
        raise AcceptanceError("connector version does not match the CLI")
    raw_health = health.get("found", {}).get("raw", {})
    if args.allow_titleblock_gap and (
        raw_health.get("version") != "v1.1.1"
        or window.get("connectorVersion") != "1.1.1"
    ):
        raise AcceptanceError(
            "--allow-titleblock-gap is scoped to CLI/connector v1.1.1; "
            "rerun without it"
        )
    context = window.get("context", {})
    original_page = (
        context.get("documentUuid") if context.get("documentType") == "schematic" else None
    )

    try:
        _, pages_payload, _ = run_command(
            args.easyeda, with_project(["sch", "pages"], args.project)
        )
        pages = unwrap_result(pages_payload).get("pages", [])
        if not pages:
            raise AcceptanceError("project has no schematic pages")

        actual_names = [page.get("name") for page in pages]
        if args.spec:
            run_command(
                args.easyeda,
                ["spec", "validate", str(args.spec), "--strict"],
                expect_json=False,
            )
            spec = load_json(args.spec)
            expected_names = [page.get("name") for page in spec.get("pages", [])]
            report["pageSet"] = compare_page_sets(actual_names, expected_names)
            if not report["pageSet"]["matched"]:
                errors.append({"type": "page-set-mismatch", "detail": report["pageSet"]})
        else:
            report["pageSet"] = {
                "actual": actual_names,
                "expected": None,
                "actualDuplicates": duplicate_values(actual_names),
                "expectedDuplicates": [],
                "matched": not duplicate_values(actual_names),
            }
            if not report["pageSet"]["matched"]:
                errors.append({"type": "duplicate-pages", "detail": report["pageSet"]})

        nets_rc, nets_payload, nets_stderr = run_command(
            args.easyeda,
            with_project(["sch", "nets", "--all", "--strict", "--json"], args.project),
            allow_failure=True,
        )
        nets = unwrap_result(nets_payload)
        report["nets"] = {
            "ok": nets_rc == 0 and bool(nets.get("ok")),
            "totalNets": nets.get("totalNets"),
            "stderr": nets_stderr or None,
        }
        if not report["nets"]["ok"]:
            errors.append({"type": "project-nets-failed", "detail": nets_payload})

        all_actual_pins = []
        if args.export:
            args.artifacts.mkdir(parents=True, exist_ok=True)

        for page in pages:
            name = page.get("name")
            uuid = page.get("uuid")
            page_report = {"name": name, "uuid": uuid, "gate": "skipped"}

            run_command(
                args.easyeda,
                with_project(["sch", "open", "--page", uuid], args.project),
            )

            if not args.skip_gate:
                _, gate_payload, gate_stderr = run_command(
                    args.easyeda,
                    with_project(
                        ["sch", "gate", "--strict", "--doc", uuid, "--json"],
                        args.project,
                    ),
                    allow_failure=True,
                )
                gate = unwrap_result(gate_payload)
                if gate.get("verdict") == "pass":
                    page_report["gate"] = "pass"
                elif args.allow_titleblock_gap and titleblock_only_failure(gate):
                    page_report["gate"] = "known-titleblock-gap"
                else:
                    page_report["gate"] = gate.get("verdict", "invalid")
                    page_report["blockers"] = gate.get("blockers", [])
                    page_report["gateStderr"] = gate_stderr or None
                    errors.append({"type": "page-gate-failed", "page": name, "gate": gate})

            _, read_payload, _ = run_command(
                args.easyeda,
                with_project(
                    ["sch", "read", "--page", uuid, "--stay", "--no-check"],
                    args.project,
                ),
            )
            page_pins = actual_pin_records(read_payload)
            all_actual_pins.extend(page_pins)
            page_report["partPins"] = len(page_pins)

            if args.export:
                output = args.artifacts / f"{safe_page_name(name)}-final.png"
                run_command(
                    args.easyeda,
                    with_project(
                        [
                            "sch",
                            "export-image",
                            "--scope",
                            "page",
                            "--format",
                            "png",
                            "--theme",
                            "Black on White",
                            "--out",
                            str(output),
                        ],
                        args.project,
                    ),
                    expect_json=False,
                )
                if not output.is_file() or output.stat().st_size == 0:
                    errors.append(
                        {"type": "empty-export", "page": name, "path": str(output)}
                    )
                else:
                    report["exports"].append(
                        {
                            "page": name,
                            "path": str(output.resolve()),
                            "bytes": output.stat().st_size,
                        }
                    )

            report["pages"].append(page_report)

        if args.golden:
            golden = load_json(args.golden)
            report["pins"] = compare_pins(all_actual_pins, expected_pin_map(golden))
            if not report["pins"]["matched"]:
                errors.append({"type": "golden-pin-net-mismatch", "detail": report["pins"]})

        report["ok"] = not errors
    finally:
        if original_page:
            run_command(
                args.easyeda,
                with_project(["sch", "open", "--page", original_page], args.project),
                allow_failure=True,
            )

    rendered_report = json.dumps(report, ensure_ascii=False, indent=2)
    if args.report_out:
        args.report_out.parent.mkdir(parents=True, exist_ok=True)
        args.report_out.write_text(rendered_report + "\n", encoding="utf-8")

    if args.json:
        print(rendered_report)
    else:
        gate_counts = {}
        for page in report["pages"]:
            gate = page["gate"]
            gate_counts[gate] = gate_counts.get(gate, 0) + 1
        page_set = "match" if report["pageSet"].get("matched") else "FAIL"
        print(f"pages: {len(report['pages'])} page-set={page_set}")
        print(f"gates: {json.dumps(gate_counts, ensure_ascii=False, sort_keys=True)}")
        nets_status = "pass" if report["nets"].get("ok") else "FAIL"
        print(f"nets: {report['nets'].get('totalNets')} strict={nets_status}")
        if report["pins"] is not None:
            pins = report["pins"]
            golden_status = "match" if pins["matched"] else "FAIL"
            print(
                f"pins: {pins['actualPinCount']}/{pins['expectedPinCount']} "
                f"nc={pins['actualNC']}/{pins['expectedNC']} golden={golden_status}"
            )
        if args.export:
            print(f"exports: {len(report['exports'])}/{len(report['pages'])}")
        print("RESULT: PASS" if report["ok"] else f"RESULT: FAIL ({len(errors)} issue(s))")
        for error in errors:
            detail = error.get("page") or error.get("detail") or ""
            print(f"- {error['type']}: {detail}")

    return 0 if report["ok"] else 1


if __name__ == "__main__":
    try:
        sys.exit(main())
    except AcceptanceError as exc:
        print(f"acceptance error: {exc}", file=sys.stderr)
        sys.exit(2)
