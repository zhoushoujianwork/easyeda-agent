#!/usr/bin/env python3
"""Read-only installed-CLI → daemon → Web connector smoke check.

The script deliberately stops before document reads when the runtime identity is
wrong. Every typed call is sequential because the EasyEDA window has a document
transition guard. It never writes an EDA design object.
"""

import argparse
import datetime as dt
import json
import pathlib
import re
import subprocess
import sys
import time


class CheckError(Exception):
    def __init__(self, status, message):
        super().__init__(message)
        self.status = status


def parse_json_output(output):
    decoder = json.JSONDecoder()
    for match in re.finditer(r"[\[{]", output):
        try:
            value, _ = decoder.raw_decode(output[match.start():])
        except json.JSONDecodeError:
            continue
        return value
    raise CheckError("fail", "CLI response contains no JSON")


def run_command(cli, name, args, out_dir, timeout):
    command = [cli, *args]
    started_at = dt.datetime.now(dt.timezone.utc).isoformat()
    start = time.monotonic()
    try:
        result = subprocess.run(command, text=True, capture_output=True, timeout=timeout, check=False)
    except subprocess.TimeoutExpired as exc:
        evidence = {"command": command, "startedAt": started_at,
                    "durationSeconds": round(time.monotonic() - start, 3), "exitCode": None,
                    "stdout": exc.stdout.decode(errors="replace") if isinstance(exc.stdout, bytes) else (exc.stdout or ""),
                    "stderr": exc.stderr.decode(errors="replace") if isinstance(exc.stderr, bytes) else (exc.stderr or ""),
                    "error": str(exc)}
        (out_dir / f"{name}.json").write_text(json.dumps(evidence, ensure_ascii=False, indent=2) + "\n")
        raise CheckError("blocked", f"{name}: {exc}") from exc
    except OSError as exc:
        evidence = {"command": command, "startedAt": started_at,
                    "durationSeconds": round(time.monotonic() - start, 3), "exitCode": None,
                    "stdout": "", "stderr": "", "error": str(exc)}
        (out_dir / f"{name}.json").write_text(json.dumps(evidence, ensure_ascii=False, indent=2) + "\n")
        raise CheckError("blocked", f"{name}: {exc}") from exc
    evidence = {
        "command": command,
        "startedAt": started_at,
        "durationSeconds": round(time.monotonic() - start, 3),
        "exitCode": result.returncode,
        "stdout": result.stdout,
        "stderr": result.stderr,
    }
    (out_dir / f"{name}.json").write_text(json.dumps(evidence, ensure_ascii=False, indent=2) + "\n")
    if result.returncode != 0:
        raise CheckError("fail", f"{name}: exit {result.returncode}: {result.stderr.strip() or result.stdout.strip()}")
    return result.stdout


def same_version(actual, expected):
    return str(actual).removeprefix("v") == expected.removeprefix("v")


def check_health(raw, expected, project, doc, doc_type):
    health = parse_json_output(raw)
    found = health.get("found") or {}
    daemon = found.get("raw") or {}
    gate = health.get("versionGate") or {}
    if health.get("status") != "found":
        raise CheckError("blocked", "daemon health is unavailable")
    if not same_version(gate.get("cli", ""), expected):
        raise CheckError("blocked", f"health CLI version {gate.get('cli')} differs from {expected}")
    if not same_version(daemon.get("version", ""), expected):
        raise CheckError("blocked", f"daemon version {daemon.get('version')} differs from {expected}")
    matches = []
    for window in daemon.get("windows") or []:
        context = window.get("context") or {}
        if context.get("projectUuid") == project and context.get("documentUuid") == doc and context.get("documentType") == doc_type:
            matches.append(window)
    if len(matches) != 1:
        raise CheckError("blocked", f"expected one {doc_type} window for {project}/{doc}, found {len(matches)}")
    if not same_version(matches[0].get("connectorVersion", ""), expected):
        raise CheckError("blocked", f"connector version {matches[0].get('connectorVersion')} differs from {expected}")
    window_id = matches[0].get("windowId")
    # Aggregate verdicts include unrelated projects. Keep exact target checks
    # without treating another window's old connector/host as this one's version.
    host_findings = [finding for finding in (health.get("hostCompatibility") or {}).get("findings", [])
                     if window_id and finding.get("windowId") == window_id]
    if len(host_findings) != 1 or host_findings[0].get("severity") != "ok":
        raise CheckError("blocked", "target EasyEDA host compatibility is missing, ambiguous or outside the supported V4 baseline")
    return window_id


def check_response(raw, project, doc, doc_type, name):
    response = parse_json_output(raw)
    context = response.get("context") or {}
    if response.get("ok") is not True:
        raise CheckError("fail", f"{name}: typed response is not ok")
    if (context.get("projectUuid") != project or context.get("documentUuid") != doc
            or context.get("documentType") != doc_type):
        raise CheckError("fail", f"{name}: response context points to a different project/document")
    if response.get("result") is None:
        raise CheckError("fail", f"{name}: typed response has no result")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cli", default="easyeda", help="installed easyeda CLI path")
    parser.add_argument("--expected-version", required=True, help="exact installed dev version")
    parser.add_argument("--project", required=True, help="test project UUID")
    parser.add_argument("--doc", required=True, help="active test document UUID")
    parser.add_argument("--type", choices=("schematic", "pcb"), required=True)
    parser.add_argument("--out", type=pathlib.Path, required=True, help="new evidence directory")
    parser.add_argument("--timeout", type=int, default=180, help="per-command timeout in seconds")
    args = parser.parse_args()

    if args.out.exists() and any(args.out.iterdir()):
        parser.error("--out must be a new or empty directory; existing evidence is immutable")
    args.out.mkdir(parents=True, exist_ok=True)
    summary = {"status": "not-run", "expectedVersion": args.expected_version, "projectUuid": args.project,
               "documentUuid": args.doc, "documentType": args.type, "checks": []}
    try:
        version = run_command(args.cli, "00-version", ["--version"], args.out, args.timeout).strip()
        summary["checks"].append("version")
        if not same_version(version.split()[-1], args.expected_version):
            raise CheckError("blocked", f"CLI version {version} differs from {args.expected_version}")
        first_health = run_command(args.cli, "01-health-before", ["health"], args.out, args.timeout)
        summary["windowId"] = check_health(first_health, args.expected_version, args.project, args.doc, args.type)
        summary["checks"].append("health-before")
        actions = parse_json_output(run_command(args.cli, "02-actions", ["actions"], args.out, args.timeout))
        if not actions:
            raise CheckError("fail", "typed action catalog is empty")
        summary["checks"].append("actions")
        for index, command in enumerate((["project", "info"], ["project", "doc"],
                                         ["sch", "list"] if args.type == "schematic" else ["pcb", "list"])):
            name = f"0{index + 3}-" + "-".join(command)
            raw = run_command(args.cli, name, [*command, "--project", args.project, "--doc", args.doc], args.out, args.timeout)
            check_response(raw, args.project, args.doc, args.type, name)
            summary["checks"].append(name)
        last_health = run_command(args.cli, "06-health-after", ["health"], args.out, args.timeout)
        check_health(last_health, args.expected_version, args.project, args.doc, args.type)
        summary["checks"].append("health-after")
        summary["status"] = "pass"
    except CheckError as exc:
        summary["status"] = exc.status
        summary["reason"] = str(exc)
    (args.out / "summary.json").write_text(json.dumps(summary, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps({"status": summary["status"], "reason": summary.get("reason"),
                      "evidence": str(args.out / "summary.json")}, ensure_ascii=False))
    return 0 if summary["status"] == "pass" else 1


if __name__ == "__main__":
    sys.exit(main())
