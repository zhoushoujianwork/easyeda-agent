#!/usr/bin/env python3
"""Offline smoke of a release package and a native CLI, outside any checkout.

--assets verifies all eight release assets, then runs only the current platform's
CLI in a temporary directory. --binary + --skill-dir tests a native CI build
without downloading a release. Never installs, starts a daemon, or calls EDA.
"""

import argparse
import copy
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import platform
import re
import shutil
import subprocess
import tarfile
import tempfile
from urllib.parse import unquote, urlsplit
import zipfile

ASSETS = (
    "easyeda_darwin_amd64", "easyeda_darwin_arm64", "easyeda_linux_amd64",
    "easyeda_linux_arm64", "easyeda_windows_amd64.exe",
    "easyeda-agent-connector.eext", "skills.tar.gz", "install.sh", "install.ps1",
)
EVIDENCE_ASSET = "test-evidence.zip"
EVIDENCE_FILES = ("manifest.json", "test-report.md", "baseline.md", "test-cases.md")
REQUIRED_CASES = frozenset(("M1", "F1", "F2", "E1", "L1", "N1", "R1", "E2E"))
BASIC_CASES = frozenset(f"B{i:02d}" for i in range(11))
ADVANCED_CASES = frozenset(f"A{i:02d}" for i in range(7))
# These helpers are directly executable in the public package. Other Python
# helpers are intentionally invoked via python3 and need only read permission.
EXECUTABLE_HELPERS = (
    "scripts/lint.sh", "scripts/bulk-connect.py", "scripts/bulk-place.py",
    "scripts/parts-add.py", "scripts/blocks-pin-audit.py", "scripts/audit-baseline.py",
)


def require(condition, message):
    if not condition:
        raise ValueError(message)


def digest(path):
    with path.open("rb") as stream:
        checksum = hashlib.sha256()
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            checksum.update(chunk)
        return checksum.hexdigest()


def evidence_case_rows(data):
    rows = {}
    for line in data.decode("utf-8").splitlines():
        if not line.startswith("|") or not line.endswith("|"):
            continue
        cells = [cell.strip() for cell in line[1:-1].split("|")]
        if len(cells) >= 3 and cells[0] != "ID" and re.fullmatch(r"[A-Z][A-Z0-9]{1,7}", cells[0]):
            require(cells[0] not in rows, f"duplicate acceptance case: {cells[0]}")
            rows[cells[0]] = cells
    return rows


def basic_scope(manifest):
    if manifest.get("schemaVersion") == 1:
        require(not any(key in manifest for key in ("acceptanceScope", "deferredScope", "deferredUntil")),
                "legacy evidence cannot override its full acceptance scope")
        return False
    require(manifest.get("schemaVersion") == 2 and manifest.get("acceptanceScope") == "basic-cli"
            and manifest.get("deferredScope") == "advanced-cli"
            and manifest.get("deferredUntil") == "next-release",
            "schema 2 requires explicit basic-cli scope and advanced-cli deferred to next-release")
    return True


def check_assets(directory, tag=None):
    match = re.fullmatch(r"v(\d+)\.([1-9]\d*)\.0", tag or "")
    minor = bool(match and (int(match.group(1)), int(match.group(2))) >= (1, 6))
    assets = (*ASSETS, EVIDENCE_ASSET) if minor else ASSETS
    expected = {}
    for line in (directory / "checksums.txt").read_text(encoding="utf-8").splitlines():
        match = re.fullmatch(r"([0-9a-f]{64})  (\S+)", line)
        require(match is not None, f"invalid checksum entry: {line!r}")
        checksum, name = match.groups()
        require(name in assets and name not in expected, f"unexpected/duplicate checksum asset: {name}")
        expected[name] = checksum
    require(set(expected) == set(assets), f"checksums.txt must cover all {len(assets)} release assets")
    for name, checksum in expected.items():
        path = directory / name
        require(path.is_file() and not path.is_symlink() and path.stat().st_size > 0, f"missing/empty asset: {name}")
        require(digest(path) == checksum, f"checksum mismatch: {name}")
    if minor:
        with zipfile.ZipFile(directory / EVIDENCE_ASSET) as archive:
            require(archive.namelist() == list(EVIDENCE_FILES), "acceptance evidence archive files differ")
            manifest = json.loads(archive.read("manifest.json"))
            require(isinstance(manifest, dict)
                    and manifest.get("version") == tag and manifest.get("result") == "pass"
                    and manifest.get("independentReview") == "pass", "acceptance evidence verdict missing")
            basic = basic_scope(manifest)
            require(isinstance(manifest.get("sha256"), dict)
                    and set(manifest["sha256"]) == set(EVIDENCE_FILES[1:]),
                    "acceptance evidence document hashes missing")
            for name in EVIDENCE_FILES[1:]:
                require(digest_bytes(archive.read(name)) == manifest["sha256"][name],
                        f"acceptance evidence hash mismatch: {name}")
            cases = evidence_case_rows(archive.read("test-cases.md"))
            report_bytes = archive.read("test-report.md")
            report = evidence_case_rows(report_bytes)
            required = BASIC_CASES | ADVANCED_CASES if basic else REQUIRED_CASES
            require(required.issubset(cases) and set(cases) == set(report)
                    and (not basic or set(cases) == required),
                    "acceptance report does not cover every required case")
            for case_id, cells in report.items():
                status = cells[1].lower()
                if basic and case_id in ADVANCED_CASES:
                    require(status == "not-run", f"deferred acceptance case {case_id} must remain not-run")
                else:
                    require(status == "pass" or not basic and case_id == "L2" and status == "not-applicable",
                            f"acceptance case {case_id} is not pass")
                require(len(cells[2]) >= 30, f"acceptance case {case_id} lacks readback/evidence")
            require("## 现场回读" in report_bytes.decode("utf-8")
                    and "## 独立复核" in report_bytes.decode("utf-8"),
                    "acceptance report lacks live readback or independent review")
    return list(assets)


def digest_bytes(data):
    return hashlib.sha256(data).hexdigest()


def check_connector(path, version):
    with zipfile.ZipFile(path) as archive:
        names = archive.namelist()
        require(len(names) == len(set(names)), "duplicate connector ZIP entries")
        manifest = json.loads(archive.read("extension.json"))
        require(isinstance(manifest, dict), "connector manifest must be a JSON object")
        require(manifest.get("name") == "easyeda-agent-connector", "wrong connector name")
        require(manifest.get("version") == version, "connector version mismatch")
        require(re.fullmatch(r"[0-9a-fA-F]{32}", manifest.get("uuid", "")), "invalid connector UUID")
        require(manifest.get("entry") == "./dist/index", "unexpected connector entry")
        require(bool(archive.read("dist/index.js")), "empty compiled connector entry")
        require(bool(manifest.get("engines", {}).get("eda")), "missing connector EDA engine range")
        return {"version": version, "uuid": manifest["uuid"], "runtimeTested": False}


def extract_skill(archive_path, destination):
    """Extract only regular package files, retaining modes even on Windows."""
    modes = {}
    with tarfile.open(archive_path, "r:gz") as archive:
        for member in archive:
            path = PurePosixPath(member.name)
            require(not path.is_absolute() and path.parts and path.parts[0] == "easyeda-agent"
                    and ".." not in path.parts and "\\" not in member.name and ":" not in member.name
                    and path.as_posix() == member.name.rstrip("/"),
                    f"unsafe skill archive path: {member.name}")
            require(member.name not in modes, f"duplicate skill archive entry: {member.name}")
            require(member.isdir() or member.isfile(), f"unsupported skill archive entry: {member.name}")
            modes[member.name] = member.mode
            target = destination.joinpath(*path.parts)
            if member.isdir():
                target.mkdir(parents=True, exist_ok=True)
            else:
                target.parent.mkdir(parents=True, exist_ok=True)
                with archive.extractfile(member) as source, target.open("wb") as output:
                    shutil.copyfileobj(source, output)
                target.chmod(member.mode & 0o777)
    return destination / "easyeda-agent", modes


def check_skill(root, version, archive_modes=None):
    root = root.resolve()
    frontmatter = (root / "SKILL.md").read_text(encoding="utf-8").split("---", 2)
    require(len(frontmatter) == 3 and not frontmatter[0].strip(), "SKILL.md frontmatter missing")
    match = re.search(r'^  version:\s*"([^"]+)"$', frontmatter[1], re.MULTILINE)
    require(match and match.group(1) == version, "Skill metadata.version mismatch")
    count = links = 0
    for path in root.rglob("*"):
        require(not path.is_symlink(), f"Skill symlink unsupported: {path.relative_to(root)}")
        if not path.is_file():
            continue
        count += 1
        if path.suffix != ".md":
            continue
        for match in re.finditer(r"\[[^\]]*\]\(([^)]+)\)", path.read_text(encoding="utf-8")):
            link = match.group(1).strip().split(' "')[0].strip("<>")
            parsed = urlsplit(link)
            if parsed.scheme or parsed.netloc or not parsed.path:
                continue
            target = (path.parent / unquote(parsed.path)).resolve()
            require(target.is_relative_to(root), f"Skill link escapes package: {link}")
            require(target.exists(), f"broken Skill link in {path.relative_to(root)}: {link}")
            links += 1
    for relative in EXECUTABLE_HELPERS:
        path = root / relative
        require(path.is_file(), f"missing Skill helper: {relative}")
        require(path.read_bytes().startswith(b"#!"), f"Skill helper has no shebang: {relative}")
        mode = archive_modes.get(f"easyeda-agent/{relative}") if archive_modes is not None else path.stat().st_mode
        if archive_modes is not None or os.name != "nt":
            require(mode is not None and mode & 0o111, f"Skill helper is not executable: {relative}")
    return {"version": version, "files": count, "localLinks": links,
            "executableModes": "verified" if archive_modes is not None or os.name != "nt" else "unavailable-on-windows-filesystem"}


def native_asset(system=None, machine=None):
    system = (system or platform.system()).lower()
    machine = (machine or platform.machine()).lower()
    arch = {"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}.get(machine)
    name = f"easyeda_{system}_{arch}" + (".exe" if system == "windows" else "")
    require(name in ASSETS, f"no native release asset for {system}/{machine}; pass --binary for a native build")
    return name


def compose_fixture():
    """Synthetic three-pin device: VDD, GND, explicit NC; no real project IDs."""
    return {
        "schemaVersion": 1, "sheet": {"minX": 50, "minY": -20, "maxX": 530, "maxY": 580},
        "connectivity": {
            "schemaVersion": "1.4", "projectId": "smoke-project", "documentId": "smoke-sheet",
            "components": [{"id": "stable-u7", "ref": "U7", "device": {
                "libraryUuid": "1" * 32, "deviceUuid": "2" * 32, "name": "synthetic-three-pin"},
                "pins": [{"number": "1", "name": "VDD"}, {"number": "2", "name": "GND"},
                         {"number": "3", "name": "RESERVED", "noConnected": True}]}],
            "nets": [{"id": "stable-vdd", "name": "+3V3"}, {"id": "stable-gnd", "name": "GND"}],
            "connections": [{"componentId": "stable-u7", "pinNumber": "1", "netId": "stable-vdd", "kind": "pin_net"},
                            {"componentId": "stable-u7", "pinNumber": "2", "netId": "stable-gnd", "kind": "pin_net"}],
            "modules": [{"id": "module-one", "name": "POWER", "coreComponents": ["stable-u7"]}]},
        "modules": [{"id": "module-one", "title": "POWER", "placements": [{
            "primitiveId": "obsolete-id", "designator": "U7", "x": 100, "y": 100,
            "bbox": {"minX": 90, "minY": 80, "maxX": 110, "maxY": 120},
            "pins": [{"number": "1", "name": "VDD", "net": "+3V3", "x": 70, "y": 110},
                     {"number": "2", "name": "GND", "net": "GND", "x": 130, "y": 90},
                     {"number": "3", "name": "RESERVED", "net": "", "x": 130, "y": 110}]}],
            "wires": [{"net": "+3V3", "points": [[70, 110], [50, 110]]},
                      {"net": "GND", "points": [[130, 90], [150, 90]]}],
            "flags": [{"net": "+3V3", "kind": "power", "pinX": 50, "pinY": 110, "direction": "up", "offset": 15},
                      {"net": "GND", "kind": "ground", "pinX": 150, "pinY": 90, "direction": "down", "offset": 15}]}],
    }


def smoke_cli(binary, tag, work):
    source_binary = binary.resolve(strict=True)
    # Release downloads do not carry executable mode bits. Reproduce the
    # installer in temporary storage without chmod-ing the downloaded asset.
    binary = work / ("easyeda.exe" if os.name == "nt" else "easyeda")
    shutil.copyfile(source_binary, binary)
    binary.chmod(0o755)
    invocations = []

    def run(*args, error=None):
        # Offline commands must not consult any active user daemon even if they
        # regress. Do not change HOME or the user's installation/configuration.
        command = [str(binary), "--host", "127.0.0.1", "--ports", "1-1", *map(str, args)]
        result = subprocess.run(command, cwd=work, capture_output=True, text=True, encoding="utf-8", timeout=30)
        label = " ".join(map(str, args[:2]))
        if error:
            require(result.returncode != 0 and error in result.stderr,
                    f"{label}: expected rejection containing {error!r}, got {result.returncode}: {result.stderr}")
        else:
            require(result.returncode == 0, f"{label} failed ({result.returncode}): {result.stderr}")
        invocations.append(label)
        return result.stdout

    require(run("--version").strip() == f"easyeda-agent {tag}", "native CLI version mismatch")
    for args in [(), ("sch",), ("sch", "compose"), ("sch", "connectivity"), ("sch", "apply"), ("update",)]:
        require("Usage:" in run(*args, "--help"), f"missing help: {args}")
    actions = json.loads(run("actions"))
    require(isinstance(actions, list) and any(a.get("name") == "schematic.components.list" for a in actions), "embedded action catalog missing schematic.components.list")
    blocks = json.loads(run("blocks", "ls", "--json"))
    require(isinstance(blocks, list) and blocks and blocks[0].get("id"), "empty embedded block library")
    detail = json.loads(run("blocks", "show", blocks[0]["id"]))
    require(detail.get("id") == blocks[0]["id"] and detail.get("parts"), "embedded block detail incomplete")
    api = json.loads(run("api", "show", "sch_PrimitiveComponent", "--json"))
    require(isinstance(api, list) and any(m.get("method") == "getAll" for m in api), "embedded official API index incomplete")
    source = compose_fixture()
    input_path, output_path = work / "composition.json", work / "composed.json"
    input_path.write_text(json.dumps(source), encoding="utf-8")
    run("sch", "compose", "--from", input_path, "--out", output_path)
    first = output_path.read_bytes()
    plan = json.loads(first)
    component = plan["connectivity"]["components"][0]
    require(component["id"] == "stable-u7" and component["ref"] == "U7" and component["pins"][2]["noConnected"], "compose changed component identity or NC")
    require(plan["connectivity"]["nets"] == source["connectivity"]["nets"] and plan["connectivity"]["connections"] == source["connectivity"]["connections"], "compose changed electrical topology")
    require(len(plan["layout"]["frames"]) == 1 and plan["rows"] == 1, "compose omitted module frame")
    run("sch", "compose", "--from", input_path, "--out", output_path)
    require(first == output_path.read_bytes(), "compose is not deterministic")
    broken = copy.deepcopy(source)
    broken["modules"][0]["placements"][0]["pins"].pop()
    input_path.write_text(json.dumps(broken), encoding="utf-8")
    output_path.unlink()
    run("sch", "compose", "--from", input_path, "--out", output_path, error="pin set differs")
    require(not output_path.exists(), "invalid composition wrote an output plan")
    source["unexpected"] = True
    input_path.write_text(json.dumps(source), encoding="utf-8")
    run("sch", "compose", "--from", input_path, "--out", output_path, error="unknown field")
    require(not output_path.exists(), "unknown fields wrote an output plan")
    return {"binary": str(source_binary), "ranTemporaryCopy": True, "platform": platform.system(), "architecture": platform.machine(),
            "commandsPassed": len(invocations), "actions": len(actions), "blocks": len(blocks),
            "apiMethods": len(api), "compose": "positive, deterministic, NC/identity preservation, missing-pin/unknown-field rejection"}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", required=True, help="exact expected tag, e.g. v1.4.2")
    parser.add_argument("--assets", type=Path, help="release assets directory with checksums.txt")
    parser.add_argument("--binary", type=Path, help="explicit native CLI (defaults to matching release asset)")
    parser.add_argument("--skill-dir", type=Path, help="unpacked public Skill (defaults to extracting release archive)")
    parser.add_argument("--local-dev", action="store_true", help="require vX.Y.Z-dev.N; never publish")
    args = parser.parse_args()
    if not args.assets and not (args.binary and args.skill_dir):
        parser.error("pass --assets, or both --binary and --skill-dir")
    # Same version contract as release-check.py: a published tag is exact, a local
    # development build carries the -dev.N suffix. Keeping the release form strict
    # means an unreleasable version can never pass as one.
    pattern = r"v[0-9]+\.[0-9]+\.[0-9]+"
    if args.local_dev:
        pattern += r"-dev\.[1-9][0-9]*"
    if not re.fullmatch(pattern, args.version):
        expected = "vX.Y.Z-dev.N (N >= 1)" if args.local_dev else "vX.Y.Z"
        parser.error(f"--version must be an exact release tag {expected}")
    report = {"version": args.version, "offline": True, "edaRuntimeTested": False}
    try:
        with tempfile.TemporaryDirectory(prefix="easyeda-release-smoke-") as temporary:
            work = Path(temporary)
            if args.assets:
                assets = args.assets.resolve()
                report["assetsIntegrityVerified"] = check_assets(assets, args.version)
                report["connector"] = check_connector(assets / "easyeda-agent-connector.eext", args.version[1:])
                skill, modes = extract_skill(assets / "skills.tar.gz", work / "archive")
                report["packagedSkill"] = check_skill(skill, args.version[1:], modes)
                binary = args.binary or assets / native_asset()
            else:
                binary = args.binary
                report["assetsIntegrityVerified"] = []
            if args.skill_dir:
                report["providedSkill"] = check_skill(args.skill_dir, args.version[1:])
            run_dir = work / "empty-working-directory"
            run_dir.mkdir()
            report["nativeCLI"] = smoke_cli(binary, args.version, run_dir)
            # Be precise: checksums cannot demonstrate cross-platform execution.
            report["otherPlatformsRuntimeTested"] = False
            print(json.dumps(report, indent=2, ensure_ascii=False))
    except (OSError, ValueError, KeyError, TypeError, tarfile.TarError, zipfile.BadZipFile,
            subprocess.SubprocessError) as error:
        parser.exit(1, f"release smoke failed: {error}\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
