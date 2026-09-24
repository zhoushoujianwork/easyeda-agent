#!/usr/bin/env python3
"""Offline Cobra contract check for the B00–B10 basic CLI live suite.

This checks that the commands needed by the live suite are present and reject
bad syntax before dispatch. It is deliberately not a substitute for live EDA
readback; write actions remain covered by docs/cli-live-test-detail.md.
"""

import argparse
import datetime as dt
import hashlib
import json
import pathlib
import shutil
import subprocess
import sys


COMMANDS = """
project create|project find|project info|project open|project export-source
doc ls|doc open|doc reload
sch pages|sch page-new|sch page-rename|sch open|sch place|sch list|sch modify
sch wire|sch netflag|sch no-connect|sch prim-delete|sch save|sch export-image|sch netlist
lib search|lib by-lcsc
pcb docs|pcb new-board|pcb board-info|pcb list|pcb add-component|pcb modify|pcb delete
pcb stackup|pcb config|pcb outline-set|pcb outline-get|pcb track|pcb track-list
pcb track-delete|pcb via|pcb via-list|pcb via-delete|pcb region|pcb pour
pcb save|pcb dump|pcb export-dsn
bom export|web reload|audit cost
""".replace("\n", "|").split("|")

SIGNATURES = {
    "project find": ("--name", "--team", "--window"),
    "sch list": ("--include-wires", "--include-page-primitives", "--project", "--doc"),
    "pcb delete": ("--ids", "--project", "--doc"),
    "web reload": ("--project", "--doc", "--timeout"),
}

ACTION_NAMES = (
    "project.create", "project.find", "schematic.components.list",
    "schematic.netflag.create", "pcb.component.delete", "system.page_reload",
)


def run(binary: str, *args: str) -> dict:
    proc = subprocess.run([binary, *args], capture_output=True, text=True, check=False)
    return {"args": list(args), "exitCode": proc.returncode,
            "stdout": proc.stdout, "stderr": proc.stderr}


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", type=pathlib.Path, required=True)
    parser.add_argument("--binary", default="easyeda")
    args = parser.parse_args()
    binary = shutil.which(args.binary)
    if not binary:
        parser.error(f"CLI not found: {args.binary}")
    if args.out.exists():
        parser.error(f"refusing to overwrite: {args.out}")

    version = run(binary, "--version")
    catalog = run(binary, "actions")
    findings = []
    try:
        names = {action["name"] for action in json.loads(catalog["stdout"])}
    except (ValueError, KeyError, TypeError):
        names = set()
        findings.append("typed action catalog is not readable JSON")
    for name in ACTION_NAMES:
        if name not in names:
            findings.append(f"typed action missing: {name}")

    checks = []
    for command in (entry.strip() for entry in COMMANDS if entry.strip()):
        result = run(binary, *command.split(), "--help")
        help_text = result["stdout"] + result["stderr"]
        expected_usage = "easyeda " + command
        missing = [flag for flag in SIGNATURES.get(command, ()) if flag not in help_text]
        passed = result["exitCode"] == 0 and expected_usage in help_text and not missing
        checks.append({"command": command, "passed": passed, "exitCode": result["exitCode"],
                       "missingFlags": missing, "helpSha256": hashlib.sha256(help_text.encode()).hexdigest()})
        if not passed:
            findings.append(f"help contract failed: {command}; missing flags {missing}")

    negative = [run(binary, "project", "no-such-command"),
                run(binary, "sch", "list", "--no-such-flag"),
                run(binary, "pcb", "no-such-command")]
    for result in negative:
        if result["exitCode"] == 0:
            findings.append(f"bad syntax accepted: {result['args']}")

    output = {"checkedAt": dt.datetime.now(dt.timezone.utc).isoformat(),
              "binary": binary, "version": version["stdout"].strip(),
              "actionCount": len(names), "commandChecks": checks,
              "negativeChecks": negative, "findings": findings,
              "passed": not findings}
    args.out.parent.mkdir(parents=True, exist_ok=True)
    args.out.write_text(json.dumps(output, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(json.dumps({"passed": output["passed"], "commands": len(checks),
                      "actions": len(names), "findings": findings, "evidence": str(args.out)}, ensure_ascii=False))
    return 0 if output["passed"] else 1


if __name__ == "__main__":
    sys.exit(main())
