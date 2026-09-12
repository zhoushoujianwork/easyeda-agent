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
    "easyeda-agent-connector.eext", "skills.tar.gz", "install.sh",
]


def skill_version(text: str) -> str:
    match = re.search(r'^  version:\s*"([^"]+)"$', text, re.MULTILINE)
    if not match:
        raise ValueError("SKILL.md metadata.version missing")
    return match.group(1)


def check_sources(repo: Path, tag: str, local_dev: bool = False) -> str:
    pattern = r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)"
    if local_dev:
        pattern += r"-dev\.[1-9][0-9]*"
    if not re.fullmatch(pattern, tag):
        expected = "vX.Y.Z-dev.N (N >= 1)" if local_dev else "vX.Y.Z"
        raise ValueError(f"VERSION must be a complete {'local development version' if local_dev else 'release tag'}: {expected}")
    version = tag[1:]
    for name in ["extension/extension.json", "extension/package.json", "extension/package-lock.json"]:
        data = json.loads((repo / name).read_text())
        if data.get("version") != version:
            raise ValueError(f"{name}: version {data.get('version')!r}, expected {version}")
        if name.endswith("package-lock.json") and data.get("packages", {}).get("", {}).get("version") != version:
            raise ValueError(f"{name}: packages[''].version must also be {version}")
    if skill_version((repo / "skills/easyeda-agent/SKILL.md").read_text()) != version:
        raise ValueError(f"SKILL.md version must be {version}; run scripts/sync-skill-version.py {version}")
    changelog = (repo / "extension/CHANGELOG.md").read_text()
    if not re.search(rf"^##\s*\[{re.escape(version)}\]", changelog, re.MULTILINE):
        raise ValueError(f"extension/CHANGELOG.md has no ## [{version}] entry")
    return version


def check_connector(path: Path, version: str, uuid: str) -> None:
    with zipfile.ZipFile(path) as archive:
        manifest = json.loads(archive.read("extension.json"))
        if manifest.get("version") != version or manifest.get("uuid") != uuid:
            raise ValueError(f"{path}: packaged connector version/UUID differs from the requested source")
        if "dist/index.js" not in archive.namelist():
            raise ValueError(f"{path}: compiled connector entry is missing")


def write_checksums(dist: Path) -> None:
    records = []
    for name in ASSETS:
        path = dist / name
        if not path.is_file() or path.stat().st_size == 0:
            raise ValueError(f"missing/empty release asset: {path}")
        records.append(f"{hashlib.sha256(path.read_bytes()).hexdigest()}  {name}\n")
    (dist / "checksums.txt").write_text("".join(records))


def check_artifacts(repo: Path, dist: Path, version: str) -> None:
    expected = {}
    for line in (dist / "checksums.txt").read_text().splitlines():
        digest, name = line.split()
        if name in expected or name not in ASSETS or not re.fullmatch(r"[0-9a-f]{64}", digest):
            raise ValueError(f"invalid checksum asset entry: {line}")
        expected[name] = digest
    if set(expected) != set(ASSETS):
        raise ValueError("checksums.txt must name all eight release assets with bare filenames")
    for name in ASSETS:
        if hashlib.sha256((dist / name).read_bytes()).hexdigest() != expected[name]:
            raise ValueError(f"checksum mismatch: {name}")
    manifest = json.loads((repo / "extension/extension.json").read_text())
    check_connector(dist / "easyeda-agent-connector.eext", version, manifest["uuid"])
    with tarfile.open(dist / "skills.tar.gz", "r:gz") as archive:
        item = archive.extractfile("easyeda-agent/SKILL.md")
        if item is None or skill_version(item.read().decode()) != version:
            raise ValueError("packaged SKILL.md has the wrong version")
    if (dist / "install.sh").read_bytes() != (repo / "install.sh").read_bytes():
        raise ValueError("packaged install.sh differs from source")
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
    parser.add_argument("--artifacts", type=Path)
    args = parser.parse_args()
    try:
        version = check_sources(args.repo, args.version, args.local_dev)
        if args.connector:
            uuid = json.loads((args.repo / "extension/extension.json").read_text())["uuid"]
            check_connector(args.connector, version, uuid)
        if args.write_checksums:
            write_checksums(args.write_checksums)
        if args.artifacts:
            check_artifacts(args.repo, args.artifacts, version)
        print(f"Release check passed: {args.version}" + (f"; artifacts in {args.artifacts}" if args.artifacts else ""))
    except (OSError, ValueError, KeyError, tarfile.TarError, zipfile.BadZipFile, subprocess.CalledProcessError) as error:
        parser.exit(1, f"release check failed: {error}\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
