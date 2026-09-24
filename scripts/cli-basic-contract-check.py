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
import re
import shutil
import subprocess
import sys


COMMANDS = """
project|doc|sch|pcb|lib|bom|apply|audit
project create|project find|project info|project open|project export-source
doc ls|doc open|doc reload
sch pages|sch page-new|sch page-rename|sch page-delete|sch open|sch place|sch list|sch modify
sch wire|sch netflag|sch no-connect|sch prim-delete|sch save|sch export-image|sch netlist
lib search|lib by-lcsc|lib device get|lib symbol get|lib footprint get
pcb docs|pcb new-board|pcb board-info|pcb list|pcb add-component|pcb modify|pcb delete
pcb stackup|pcb stackup show|pcb stackup set
pcb config|pcb config get|pcb config track|pcb config clearance|pcb config via|pcb config bind
pcb drc-rules-set|pcb outline-set|pcb outline-get|pcb outline-clear|pcb track|pcb track-list
pcb track-delete|pcb via|pcb via-list|pcb via-delete|pcb region
pcb region create|pcb region list|pcb region delete|pcb pour|pcb pour-list|pcb pour-delete
pcb save|pcb dump|pcb export-dsn
bom export|web reload|audit cost
""".replace("\n", "|").split("|")

SIGNATURES = {
    "project find": ("--name", "--team", "--window", "--timeout"),
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


def audit_snapshot(directory: pathlib.Path) -> dict:
    return {str(path): hashlib.sha256(path.read_bytes()).hexdigest()
            for path in sorted(directory.glob("*.jsonl"))}


def skill_signatures(root: pathlib.Path, help_by_command: dict) -> list:
    """Check documented basic command flags without executing any example.

    Scope is the explicit basic-case catalog above, not every advanced command
    mentioned by the public Skill. Keep source lines and fragments for review.
    """
    commands = sorted((c for c in help_by_command if " " in c), key=len, reverse=True)
    groups = {c for c in commands if any(other.startswith(c + " ") for other in commands)}
    checks = []
    for path in sorted(root.rglob("*.md")):
        lines = path.read_text(encoding="utf-8").splitlines()
        for index, line in enumerate(lines):
            for match in re.finditer(r"\beasyeda\s+([a-z][a-z-]*(?:\s+[a-z][a-z-]*)*)", line):
                command = next((c for c in commands if match[1] == c or match[1].startswith(c + " ")), None)
                if command is None:
                    continue
                fragment = line[match.start():].split("`")[0]
                cursor = index
                while fragment.rstrip().endswith("\\") and cursor + 1 < len(lines):
                    cursor += 1
                    fragment = fragment.rstrip()[:-1] + " " + lines[cursor].strip()
                # A group mention is useful only when it has no unknown child.
                if command in groups and match[1] != command:
                    checks.append({"file": str(path), "line": index + 1, "command": command,
                                   "fragment": fragment, "passed": False,
                                   "missingFlags": [], "reason": "uncatalogued child command"})
                    continue
                flags = sorted(set(re.findall(r"(?<!\w)--[a-z][a-z0-9-]*", fragment)))
                available = set(re.findall(r"(?<!\w)--[a-z][a-z0-9-]*", help_by_command[command]))
                missing = sorted(set(flags) - available)
                checks.append({"file": str(path), "line": index + 1, "command": command,
                               "fragment": fragment, "flags": flags,
                               "missingFlags": missing, "passed": not missing})
    return checks


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", type=pathlib.Path, required=True)
    parser.add_argument("--binary", default="easyeda")
    parser.add_argument("--skill-root", type=pathlib.Path,
                        default=pathlib.Path(__file__).resolve().parents[1] / ".agents/skills/easyeda-agent")
    parser.add_argument("--audit-dir", type=pathlib.Path,
                        default=pathlib.Path.home() / ".easyeda-agent/audit")
    args = parser.parse_args()
    binary = shutil.which(args.binary)
    if not binary:
        parser.error(f"CLI not found: {args.binary}")
    if args.out.exists():
        parser.error(f"refusing to overwrite: {args.out}")
    if not (args.skill_root / "SKILL.md").is_file():
        parser.error(f"Skill entry not found: {args.skill_root}")

    audit_before = audit_snapshot(args.audit_dir)
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
    help_by_command = {}
    for command in (entry.strip() for entry in COMMANDS if entry.strip()):
        result = run(binary, *command.split(), "--help")
        help_text = result["stdout"] + result["stderr"]
        help_by_command[command] = help_text
        expected_usage = "easyeda " + command
        missing = [flag for flag in SIGNATURES.get(command, ()) if flag not in help_text]
        passed = result["exitCode"] == 0 and expected_usage in help_text and not missing
        checks.append({"command": command, "passed": passed, "exitCode": result["exitCode"],
                       "missingFlags": missing, "stdout": result["stdout"], "stderr": result["stderr"],
                       "helpSha256": hashlib.sha256(help_text.encode()).hexdigest()})
        if not passed:
            findings.append(f"help contract failed: {command}; missing flags {missing}")

    negative = [run(binary, "project", "no-such-command"),
                run(binary, "sch", "list", "--no-such-flag"),
                run(binary, "pcb", "no-such-command")]
    for result in negative:
        if result["exitCode"] == 0:
            findings.append(f"bad syntax accepted: {result['args']}")

    skill_checks = skill_signatures(args.skill_root, help_by_command)
    if not skill_checks:
        findings.append("no basic command references found in Skill")
    for result in skill_checks:
        if not result["passed"]:
            findings.append(f"Skill signature mismatch: {result['file']}:{result['line']}: {result['command']}")
    audit_after = audit_snapshot(args.audit_dir)
    if not audit_before:
        findings.append("audit evidence unavailable; cannot prove zero dispatch")
    elif audit_before != audit_after:
        findings.append("audit changed during offline checks; isolate the window and inspect concurrent dispatch")

    output = {"checkedAt": dt.datetime.now(dt.timezone.utc).isoformat(),
              "binary": binary, "version": version["stdout"].strip(),
              "binarySha256": hashlib.sha256(pathlib.Path(binary).read_bytes()).hexdigest(),
              "checkerSha256": hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest(),
              "versionCheck": version, "catalogCheck": catalog, "requiredActions": ACTION_NAMES,
              "actionCount": len(names), "commandChecks": checks,
              "negativeChecks": negative, "findings": findings,
              "skillChecks": skill_checks,
              "skillScope": "basic commands in COMMANDS; checks command/flag names, not example parameter semantics",
              "auditBefore": audit_before, "auditAfter": audit_after,
              "auditUnchanged": bool(audit_before) and audit_before == audit_after,
              "passed": not findings}
    args.out.parent.mkdir(parents=True, exist_ok=True)
    args.out.write_text(json.dumps(output, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(json.dumps({"passed": output["passed"], "commands": len(checks),
                      "actions": len(names), "skillReferences": len(skill_checks),
                      "auditUnchanged": output["auditUnchanged"],
                      "findings": findings, "evidence": str(args.out)}, ensure_ascii=False))
    return 0 if output["passed"] else 1


if __name__ == "__main__":
    sys.exit(main())
