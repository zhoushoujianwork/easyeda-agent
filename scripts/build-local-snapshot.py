#!/usr/bin/env python3
"""从固定 dev 修订构建独立 DEV 工具包；不安装、发布或修改原仓版本。"""
import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import subprocess
import shutil
import tarfile
import tempfile

VERSION = re.compile(r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-dev\.[1-9][0-9]*")
CORE = ("easyeda_darwin_arm64", "easyeda-agent-connector.eext", "skills.tar.gz", "local-dev.json")


def run(*args, cwd=None, capture=False):
    return subprocess.run(args, cwd=cwd, check=True, text=True,
                          stdout=subprocess.PIPE if capture else None).stdout


def git(repo, *args):
    return run("git", "-C", str(repo), *args, capture=True).strip()


def extract_git_archive(archive, destination):
    """Extract Git's regular files/directories and internal relative symlinks.

    Validate all paths before writing; create links last so no file extraction
    can follow an archive-controlled symlink. No newer tarfile filter API needed.
    """
    root = destination.resolve()
    with tarfile.open(archive) as stream:
        members = stream.getmembers()
        paths = {}
        for item in members:
            path = PurePosixPath(item.name)
            if (not item.name or path.is_absolute() or ".." in path.parts
                    or "\\" in item.name or ":" in item.name or not path.parts):
                raise ValueError(f"unsafe archive path: {item.name}")
            key = path.as_posix()
            if key in paths or not (item.isfile() or item.isdir() or item.issym()):
                raise ValueError(f"duplicate or unsupported archive member: {item.name}")
            paths[key] = item
            if item.issym():
                target = PurePosixPath(item.linkname)
                if (not item.linkname or target.is_absolute()
                        or "\\" in item.linkname or ":" in item.linkname):
                    raise ValueError(f"unsafe archive link: {item.name}")
                try:
                    (root / path.parent / item.linkname).resolve().relative_to(root)
                except (ValueError, RuntimeError) as error:
                    raise ValueError(f"archive link escapes destination: {item.name}") from error
        for name in paths:
            for parent in PurePosixPath(name).parents:
                ancestor = paths.get(parent.as_posix())
                if ancestor is not None and not ancestor.isdir():
                    raise ValueError(f"archive member has non-directory ancestor: {name}")
        for name, item in paths.items():
            path = root / name
            # Also reject pre-existing links in the destination, if reused by a caller.
            if path.is_symlink() or path.resolve() != path:
                raise ValueError(f"archive destination follows a link: {name}")
            if item.issym():
                continue
            path.parent.mkdir(parents=True, exist_ok=True)
            if item.isdir():
                path.mkdir(exist_ok=True)
            else:
                with stream.extractfile(item) as source, path.open("xb") as output:
                    shutil.copyfileobj(source, output)
                path.chmod(item.mode & 0o777)
                os.utime(path, (item.mtime, item.mtime))
        for name, item in paths.items():
            if item.issym():
                path = root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.symlink_to(item.linkname)
        # Resolve the complete link graph too (including chained links and cycles).
        for name, item in paths.items():
            if item.issym():
                try:
                    (root / name).resolve().relative_to(root)
                except (ValueError, RuntimeError) as error:
                    raise ValueError(f"unsafe archive link chain: {name}") from error


def inject_version(source, version):
    for name in ("extension/extension.json", "extension/package.json", "extension/package-lock.json"):
        path = source / name
        data = json.loads(path.read_text())
        data["version"] = version
        if name.endswith("package-lock.json"):
            data["packages"][""]["version"] = version
        path.write_text(json.dumps(data, ensure_ascii=False, indent="\t") + "\n")
    run("python3", "scripts/sync-skill-version.py", version, cwd=source)
    path = source / "extension/CHANGELOG.md"
    text = path.read_text()
    heading = f"## [{version}]"
    if not re.search(rf"^{re.escape(heading)}", text, re.MULTILINE):
        # This is temporary injected metadata, not a claim about the source commit.
        path.write_text(heading + "\n\nLocal DEV snapshot; version injected only in the build copy.\n\n" + text)


def build_assets(source, stage, version):
    run("make", "local-build", f"VERSION=v{version}", f"DIST={stage}", cwd=source)
    run("python3", "scripts/release-check.py", f"v{version}", "--local-dev",
        "--artifacts", str(stage), cwd=source)


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def build_snapshot(repo, version, revision, out, builder=build_assets):
    if not VERSION.fullmatch(version):
        raise ValueError("version must be X.Y.Z-dev.N (N >= 1), without leading v")
    if not re.fullmatch(r"[0-9a-f]{40}", revision):
        raise ValueError("revision must be a fixed full 40-character commit SHA")
    if not out.is_absolute():
        raise ValueError("out must be an absolute directory")
    if os.path.lexists(out):
        raise ValueError(f"out already exists; refusing to overwrite: {out}")
    if git(repo, "rev-parse", "--verify", f"{revision}^{{commit}}") != revision:
        raise ValueError("revision is not the requested commit")
    # Fixed history only: dirty caller files are never copied into the snapshot.
    # Require the requested commit to belong to dev, but allow dev to advance later.
    git(repo, "merge-base", "--is-ancestor", revision, "refs/heads/dev")
    dependencies = repo / "extension/node_modules"
    if not all((dependencies / item).is_file() for item in ("typescript/package.json", "ts-node/package.json", "esbuild/package.json")):
        raise ValueError("existing extension/node_modules dependencies are missing; nothing installed")
    out.parent.mkdir(parents=True, exist_ok=True)
    # A unique sibling gives rename atomicity on the same filesystem. Failed builds
    # are removed by TemporaryDirectory; no partial package is exposed at out.
    with tempfile.TemporaryDirectory(prefix=f".{out.name}-", dir=out.parent) as scratch:
        scratch = Path(scratch)
        source, stage = scratch / "source", scratch / "assets"
        source.mkdir()
        archive = scratch / "source.tar"
        run("git", "-C", str(repo), "archive", "--format=tar", f"--output={archive}", revision)
        extract_git_archive(archive, source)
        run("git", "init", "--quiet", str(source))
        run("git", "-C", str(source), "fetch", "--quiet", "--no-tags", str(repo), revision)
        run("git", "-C", str(source), "update-ref", "HEAD", revision)
        run("git", "-C", str(source), "read-tree", revision)
        inject_version(source, version)
        try:
            (source / "extension/node_modules").symlink_to(dependencies.resolve(), target_is_directory=True)
        except OSError as error:
            raise ValueError("cannot link existing node_modules dependencies; on Windows enable Developer Mode or symlink privileges. Build requires existing make, Go and npm dependencies; nothing installed") from error
        builder(source, stage, version)
        metadata = {"schemaVersion": 1, "kind": "local-dev", "version": version,
                    "sourceRevision": revision, "sourceBranch": "dev", "sourceDirty": False,
                    "versionInjected": True}
        (stage / "local-dev.json").write_text(json.dumps(metadata, indent=2) + "\n")
        checksums = stage / "checksums.txt"
        original = checksums.read_text()
        records = {}
        for line in original.splitlines():
            checksum, name = line.split()
            if name in records or not re.fullmatch(r"[0-9a-f]{64}", checksum) or Path(name).name != name:
                raise ValueError("invalid asset checksum entry")
            if name in ("checksums.txt", "local-dev.json") or digest(stage / name) != checksum:
                raise ValueError(f"invalid/mismatching asset checksum: {name}")
            records[name] = checksum
        if not all(name in records for name in CORE[:3]):
            raise ValueError("core assets missing from checksums")
        checksums.write_text(original.rstrip("\n") + f"\n{digest(stage / 'local-dev.json')}  local-dev.json\n")
        if os.path.lexists(out):
            raise ValueError(f"out appeared during build; refusing to overwrite: {out}")
        stage.rename(out)
    return {"path": str(out), "version": version, "sourceRevision": revision,
            "sha256": {name: digest(out / name) for name in (*CORE, "checksums.txt")}}


def main():
    parser = argparse.ArgumentParser(description=__doc__, epilog="Build environment requires existing Git, make, Go, npm and extension/node_modules dependencies; nothing is installed automatically.")
    parser.add_argument("--version", required=True)
    parser.add_argument("--revision", required=True)
    parser.add_argument("--out", required=True, type=Path)
    args = parser.parse_args()
    try:
        result = build_snapshot(Path(__file__).resolve().parent.parent, args.version, args.revision, args.out)
    except (ValueError, OSError, subprocess.CalledProcessError, tarfile.TarError) as error:
        parser.exit(1, f"local snapshot build failed: {error}\n")
    print(json.dumps(result, indent=2))


if __name__ == "__main__":
    main()
