#!/usr/bin/env python3
"""Validate an already-versioned release; never modify sources or publish."""

import argparse
import hashlib
import json
import platform
from pathlib import Path
import re
import subprocess
import tarfile
import zipfile

ASSETS = [
    "easyeda_darwin_amd64", "easyeda_darwin_arm64",
    "easyeda_linux_amd64", "easyeda_linux_arm64", "easyeda_windows_amd64.exe",
    "easyeda-agent-connector.eext", "skills.tar.gz", "install.sh", "install.ps1",
]
EVIDENCE_ASSET = "test-evidence.zip"
EVIDENCE_FILES = ("test-report.md", "baseline.md", "test-cases.md")
REQUIRED_CASES = frozenset(("M1", "F1", "F2", "E1", "L1", "N1", "R1", "E2E"))
BASIC_CASES = frozenset(f"B{i:02d}" for i in range(11))
ADVANCED_CASES = frozenset(f"A{i:02d}" for i in range(7))
# Installer scripts are published verbatim; the packaged copy must match the source.
INSTALLERS = ["install.sh", "install.ps1"]


def skill_version(text: str) -> str:
    match = re.search(r'^  version:\s*"([^"]+)"$', text, re.MULTILINE)
    if not match:
        raise ValueError("SKILL.md metadata.version missing")
    return match.group(1)


def is_minor_release(tag: str) -> bool:
    match = re.fullmatch(r"v(\d+)\.(\d+)\.0", tag)
    return bool(match and int(match.group(2)) > 0
                and (int(match.group(1)), int(match.group(2))) >= (1, 6))


def release_assets(tag: str) -> list[str]:
    return ASSETS + ([EVIDENCE_ASSET] if is_minor_release(tag) else [])


def case_rows(data: bytes) -> dict[str, list[str]]:
    rows = {}
    for line in data.decode("utf-8").splitlines():
        if not line.startswith("|") or not line.endswith("|"):
            continue
        cells = [cell.strip() for cell in line[1:-1].split("|")]
        if len(cells) >= 3 and cells[0] != "ID" and re.fullmatch(r"[A-Z][A-Z0-9]{1,7}", cells[0]):
            if cells[0] in rows:
                raise ValueError(f"duplicate acceptance case row: {cells[0]}")
            rows[cells[0]] = cells
    return rows


def basic_scope(manifest: dict) -> bool:
    if manifest.get("schemaVersion") == 1:
        if any(key in manifest for key in ("acceptanceScope", "deferredScope", "deferredUntil")):
            raise ValueError("legacy evidence cannot override its full acceptance scope")
        return False
    if (manifest.get("schemaVersion") != 2 or manifest.get("acceptanceScope") != "basic-cli"
            or manifest.get("deferredScope") != "advanced-cli"
            or manifest.get("deferredUntil") != "next-release"):
        raise ValueError("schema 2 requires explicit basic-cli scope and advanced-cli deferred to next-release")
    return True


def check_case_results(contents: dict[str, bytes], basic: bool = False) -> None:
    cases = case_rows(contents["test-cases.md"])
    report = case_rows(contents["test-report.md"])
    required = BASIC_CASES | ADVANCED_CASES if basic else REQUIRED_CASES
    if (not required.issubset(cases) or set(cases) != set(report)
            or basic and set(cases) != required):
        raise ValueError("acceptance report must cover every test case required by its declared scope")
    for case_id, cells in report.items():
        status = cells[1].lower()
        if basic and case_id in ADVANCED_CASES:
            if status != "not-run":
                raise ValueError(f"deferred acceptance case {case_id} must remain not-run")
        elif status != "pass" and not (not basic and case_id == "L2" and status == "not-applicable"):
            raise ValueError(f"acceptance case {case_id} is not pass: {cells[1]}")
        if len(cells[2]) < 30:
            raise ValueError(f"acceptance case {case_id} needs a concrete readback/evidence reference")
    text = contents["test-report.md"].decode("utf-8")
    if "## 现场回读" not in text or "## 独立复核" not in text:
        raise ValueError("acceptance report must include live readback and independent review sections")


def evidence_files(repo: Path, tag: str) -> dict[str, bytes]:
    root = Path("docs/releases/evidence") / tag
    names = ("manifest.json", *EVIDENCE_FILES)
    contents = {}
    for name in names:
        relative = root / name
        path = repo / relative
        if path.is_symlink() or not path.is_file():
            raise ValueError(f"minor release evidence missing or symlinked: {relative}")
        try:
            subprocess.check_output(["git", "-C", str(repo), "ls-files", "--error-unmatch", "--", str(relative)],
                                    stderr=subprocess.DEVNULL)
        except subprocess.CalledProcessError as error:
            raise ValueError(f"minor release evidence must be Git tracked: {relative}") from error
        contents[name] = path.read_bytes()
        if not contents[name]:
            raise ValueError(f"minor release evidence is empty: {relative}")
    manifest = json.loads(contents["manifest.json"])
    if (not isinstance(manifest, dict)
            or manifest.get("version") != tag or manifest.get("result") != "pass"
            or manifest.get("independentReview") != "pass"):
        raise ValueError(f"{root}/manifest.json: exact version, pass result and independent review required")
    basic = basic_scope(manifest)
    if not isinstance(manifest.get("sha256"), dict) or set(manifest["sha256"]) != set(EVIDENCE_FILES):
        raise ValueError(f"{root}/manifest.json: sha256 must cover report, baseline and test cases")
    for name in EVIDENCE_FILES:
        if hashlib.sha256(contents[name]).hexdigest() != manifest["sha256"][name]:
            raise ValueError(f"{root}/{name}: SHA256 differs from reviewed manifest")
    check_case_results(contents, basic)
    return contents


def package_evidence(repo: Path, tag: str, output: Path) -> None:
    if not is_minor_release(tag):
        output.unlink(missing_ok=True)
        return
    contents = evidence_files(repo, tag)
    output.parent.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(output, "w", compression=zipfile.ZIP_DEFLATED) as archive:
        for name, data in contents.items():
            info = zipfile.ZipInfo(name, date_time=(1980, 1, 1, 0, 0, 0))
            info.compress_type = zipfile.ZIP_DEFLATED
            info.external_attr = 0o100644 << 16
            archive.writestr(info, data)


def check_evidence_archive(path: Path, contents: dict[str, bytes]) -> None:
    with zipfile.ZipFile(path) as archive:
        if archive.namelist() != list(contents):
            raise ValueError(f"{path}: unexpected or missing acceptance evidence files")
        for name, expected in contents.items():
            if archive.read(name) != expected:
                raise ValueError(f"{path}: {name} differs from reviewed source")


def check_sources(repo: Path, tag: str, local_dev: bool = False) -> str:
    pattern = r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)"
    if local_dev:
        pattern += r"-dev\.[1-9][0-9]*"
    if not re.fullmatch(pattern, tag):
        expected = "vX.Y.Z-dev.N (N >= 1)" if local_dev else "vX.Y.Z"
        raise ValueError(f"VERSION must be a complete {'local development version' if local_dev else 'release tag'}: {expected}")
    version = tag[1:]
    for name in ["extension/extension.json", "extension/package.json", "extension/package-lock.json"]:
        data = json.loads((repo / name).read_text(encoding="utf-8"))
        if data.get("version") != version:
            raise ValueError(f"{name}: version {data.get('version')!r}, expected {version}")
        if name.endswith("package-lock.json") and data.get("packages", {}).get("", {}).get("version") != version:
            raise ValueError(f"{name}: packages[''].version must also be {version}")
    if skill_version((repo / ".agents/skills/easyeda-agent/SKILL.md").read_text(encoding="utf-8")) != version:
        raise ValueError(f"SKILL.md version must be {version}; run scripts/sync-skill-version.py {version}")
    changelog = (repo / "extension/CHANGELOG.md").read_text(encoding="utf-8")
    if not re.search(rf"^##\s*\[{re.escape(version)}\]", changelog, re.MULTILINE):
        raise ValueError(f"extension/CHANGELOG.md has no ## [{version}] entry")
    if is_minor_release(tag):
        evidence_files(repo, tag)
    return version


def check_connector(path: Path, version: str, uuid: str) -> None:
    with zipfile.ZipFile(path) as archive:
        manifest = json.loads(archive.read("extension.json"))
        if manifest.get("version") != version or manifest.get("uuid") != uuid:
            raise ValueError(f"{path}: packaged connector version/UUID differs from the requested source")
        if "dist/index.js" not in archive.namelist():
            raise ValueError(f"{path}: compiled connector entry is missing")


def write_checksums(dist: Path, tag: str = "") -> None:
    records = []
    for name in release_assets(tag):
        path = dist / name
        if not path.is_file() or path.stat().st_size == 0:
            raise ValueError(f"missing/empty release asset: {path}")
        records.append(f"{hashlib.sha256(path.read_bytes()).hexdigest()}  {name}\n")
    # newline="\n": checksums.txt is consumed by install.sh / internal/selfupdate,
    # so it must stay LF even when the release is cut on Windows.
    with (dist / "checksums.txt").open("w", encoding="utf-8", newline="\n") as stream:
        stream.write("".join(records))


def check_artifacts(repo: Path, dist: Path, version: str) -> None:
    assets = release_assets(f"v{version}")
    expected = {}
    for line in (dist / "checksums.txt").read_text(encoding="utf-8").splitlines():
        digest, name = line.split()
        if name in expected or name not in assets or not re.fullmatch(r"[0-9a-f]{64}", digest):
            raise ValueError(f"invalid checksum asset entry: {line}")
        expected[name] = digest
    if set(expected) != set(assets):
        raise ValueError(f"checksums.txt must name all {len(assets)} release assets with bare filenames")
    for name in assets:
        if hashlib.sha256((dist / name).read_bytes()).hexdigest() != expected[name]:
            raise ValueError(f"checksum mismatch: {name}")
    manifest = json.loads((repo / "extension/extension.json").read_text(encoding="utf-8"))
    if is_minor_release(f"v{version}"):
        check_evidence_archive(dist / EVIDENCE_ASSET, evidence_files(repo, f"v{version}"))
    check_connector(dist / "easyeda-agent-connector.eext", version, manifest["uuid"])
    with tarfile.open(dist / "skills.tar.gz", "r:gz") as archive:
        item = archive.extractfile("easyeda-agent/SKILL.md")
        if item is None or skill_version(item.read().decode()) != version:
            raise ValueError("packaged SKILL.md has the wrong version")
    for name in INSTALLERS:
        if (dist / name).read_bytes() != (repo / name).read_bytes():
            raise ValueError(f"packaged {name} differs from source")
    # install.ps1 is executed by `irm | iex`: a BOM would break the first token and
    # Windows PowerShell 5.1 decodes a BOM-less script with the system ANSI codepage.
    installer_ps1 = (dist / "install.ps1").read_bytes()
    if not installer_ps1.isascii():
        raise ValueError("install.ps1 must be pure ASCII (no BOM, no literal non-ASCII text)")
    os_name = platform.system().lower()
    arch = {"x86_64": "amd64", "aarch64": "arm64", "arm64": "arm64"}.get(platform.machine().lower())
    if arch and os_name in {"darwin", "linux"}:
        binary = (dist / f"easyeda_{os_name}_{arch}").resolve()
        actual = subprocess.check_output([str(binary), "--version"], text=True).strip()
        if actual != f"easyeda-agent v{version}":
            raise ValueError(f"native release CLI version mismatch: {actual}")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("version")
    parser.add_argument("--local-dev", action="store_true", help="require vX.Y.Z-dev.N; never publish")
    parser.add_argument("--repo", type=Path, default=Path(__file__).resolve().parent.parent)
    parser.add_argument("--connector", type=Path)
    parser.add_argument("--write-checksums", type=Path)
    parser.add_argument("--package-evidence", type=Path)
    parser.add_argument("--artifacts", type=Path)
    args = parser.parse_args()
    try:
        version = check_sources(args.repo, args.version, args.local_dev)
        if args.connector:
            uuid = json.loads((args.repo / "extension/extension.json").read_text(encoding="utf-8"))["uuid"]
            check_connector(args.connector, version, uuid)
        if args.package_evidence:
            package_evidence(args.repo, args.version, args.package_evidence)
        if args.write_checksums:
            write_checksums(args.write_checksums, args.version)
        if args.artifacts:
            check_artifacts(args.repo, args.artifacts, version)
        print(f"Release check passed: {args.version}" + (f"; artifacts in {args.artifacts}" if args.artifacts else ""))
    except (OSError, ValueError, KeyError, tarfile.TarError, zipfile.BadZipFile, subprocess.CalledProcessError) as error:
        parser.exit(1, f"release check failed: {error}\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
