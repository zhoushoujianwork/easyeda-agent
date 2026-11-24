#!/usr/bin/env python3
"""在发布准备阶段显式同步 DSH bundle（根包）的版本。

根 `package.json` 是 DSH 插件包与市场 tarball 的清单，`package-lock.json` 里的
`packages[""].version` 也必须一致。它曾在多次发版中停在 1.13.1，而 Release 已经
到 v1.19.0；`release-check` 现在会拒绝这种漂移，本脚本是修复入口。

CLI、connector、Skill 和 DSH bundle 使用同一发布版本。先准备元数据、Changelog 和
Skill，再用 make release-check 校验；make release/release-build 不再自动 bump、
调用本脚本写版本或提交源码。

用法:
    python3 scripts/sync-dsh-version.py 1.19.0         # 准备时显式写入
    python3 scripts/sync-dsh-version.py 1.19.0 --check # 只校验，不一致时非零退出
"""

import argparse
import json
import pathlib
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent


def sync_version(root: pathlib.Path, version: str, check: bool = False) -> list[str]:
    """把根 package.json 与 package-lock.json 的版本对齐；返回实际的改动描述。"""
    want = version.lstrip("v")
    manifest_path = root / "package.json"
    lock_path = root / "package-lock.json"
    changes: list[str] = []
    for path in (manifest_path, lock_path):
        if not path.is_file():
            raise ValueError(f"{path} 不存在")
        data = json.loads(path.read_text(encoding="utf-8"))
        current = [data.get("version")]
        if path == lock_path:
            current.append(data.get("packages", {}).get("", {}).get("version"))
        if any(value != want for value in current):
            if check:
                raise ValueError(f"{path.name}: 版本 {current!r},期望 {want}")
            data["version"] = want
            if path == lock_path:
                data.setdefault("packages", {}).setdefault("", {})["version"] = want
            path.write_text(json.dumps(data, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
            changes.append(f"{path.name}: {current[-1]} → {want}")
    return changes


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("version", help="版本号,不带 v 前缀(如 1.19.0);带了也会被剥掉")
    ap.add_argument("--check", action="store_true", help="只校验是否已同步,不写入")
    args = ap.parse_args()
    try:
        changes = sync_version(ROOT, args.version, check=args.check)
    except (OSError, ValueError) as error:
        print(f"error: {error}", file=sys.stderr)
        if args.check:
            print(f"  → 跑 `python3 scripts/sync-dsh-version.py {args.version.lstrip('v')}` 同步", file=sys.stderr)
        return 1
    if changes:
        for line in changes:
            print(f"  {line}")
    else:
        print(f"  DSH bundle 版本已是 {args.version.lstrip('v')}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
