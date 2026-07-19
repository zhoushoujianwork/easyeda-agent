#!/usr/bin/env python3
"""bulk-place — manifest 驱动的整页批量放置（place + 位号回写 + 增量存盘）。

用法:
    EASYEDA_PROJECT=<工程名> bulk-place.py <manifest.json> <PAGE_NAME>

manifest 形状（页 → 模块 → 位号 → {lcsc}；`_origins`/`_grid` 控制初始网格坐标，
后续用 `sch autolayout --spec` 再按模块分区整理）:

    {
      "_grid": {"dx": 85, "dy": 85, "cols": 4},
      "_origins": {"P1_POWER": {"BUCK": [380, 90], "INPUT": [70, 90]}},
      "P1_POWER": {
        "BUCK":  {"U2": {"lcsc": "C2687968"}, "R4": {"lcsc": "C170333"}},
        "INPUT": {"F1": {"lcsc": "C163132"}}
      }
    }

实测要点（box-v2 / 139 件，2026-07-09）:
- LCSC 号在脚本内用 `lib by-lcsc` 批量解析（去重后一次调用），不依赖外部缓存文件;
- 位号回写按「放置坐标 == sch list 坐标」回配 —— 所以放置坐标必须唯一;
  若同坐标已有器件则跳过（幂等重跑安全）。上游缺 `sch place --designator`
  （issue #68），有了以后本脚本可以简化为单遍;
- doc switch 后必须 settle（issue #67）: 轮询 sch list 直到返回稳定的器件数;
- 每 10 件 `sch save` 一次（防抖 autosave 只是兜底）。
"""
import json
import os
import subprocess
import sys
import time

PROJECT = os.environ.get("EASYEDA_PROJECT", "")


def run(args, timeout=90, retries=3):
    proj = ["--project", PROJECT] if PROJECT else []
    for attempt in range(retries):
        # encoding 固定 utf-8:easyeda CLI 输出恒为 UTF-8,text=True 在 Windows
        # 中文环境会用系统 GBK 解码而崩溃(issue #133 Bug 4)
        p = subprocess.run(["easyeda"] + proj + args, capture_output=True,
                           encoding="utf-8", errors="replace", timeout=timeout)
        if p.returncode == 0 or attempt == retries - 1:
            return p.returncode, p.stdout, p.stderr
        time.sleep(1.5)
    return 1, "", "unreachable"


def jparse(out):
    try:
        return json.loads(out)
    except Exception:
        i = out.find("{")
        try:
            return json.JSONDecoder().raw_decode(out[i:])[0]
        except Exception:
            return {}


def list_parts():
    rc, out, _ = run(["sch", "list"])
    d = jparse(out)
    return [c for c in (d.get("result") or {}).get("components", [])
            if c.get("componentType") == "part"]


def settle(page, timeout_s=15):
    """doc switch 后等页面真正就绪(issue #67): 连续两次 sch list 器件数一致。"""
    rc, out, _ = run(["doc", "switch", page])
    if rc != 0:
        raise SystemExit(f"doc switch {page} failed: {out or ''}")
    prev = -1
    for _ in range(int(timeout_s / 1.5)):
        n = len(list_parts())
        if n == prev:
            return n
        prev = n
        time.sleep(1.5)
    return prev


def resolve_lcsc(lcscs):
    out_map = {}
    todo = sorted(set(lcscs))
    for i in range(0, len(todo), 40):
        batch = todo[i:i + 40]
        rc, out, err = run(["lib", "by-lcsc", "--lcsc", ",".join(batch)], timeout=180)
        d = jparse(out)
        for c in ((d.get("result") or {}).get("components") or []):
            out_map[c["lcsc"]] = (c["libraryUuid"], c["uuid"])
        nf = (d.get("result") or {}).get("notFound") or []
        if nf:
            print(f"  ⚠ by-lcsc notFound: {nf} (改用 lib search 手动解析后补进 manifest)")
    return out_map


def main():
    manifest_path, page = sys.argv[1], sys.argv[2]
    m = json.load(open(manifest_path))
    grid = m.get("_grid", {"dx": 85, "dy": 85, "cols": 4})
    origins = (m.get("_origins") or {}).get(page) or {}
    modules = m[page]

    lcsc_map = resolve_lcsc(
        spec["lcsc"] for parts in modules.values() for spec in parts.values())

    settle(page)
    existing = {(round(c["x"]), round(c["y"])): c["primitiveId"] for c in list_parts()}

    plan = []
    for module, parts in modules.items():
        ox, oy = origins.get(module, (60, 60))
        for i, (des, spec) in enumerate(parts.items()):
            x = ox + (i % grid["cols"]) * grid["dx"]
            y = oy + (i // grid["cols"]) * grid["dy"]
            plan.append((des, spec["lcsc"], x, y))

    placed, failed = 0, []
    for des, lcsc, x, y in plan:
        if (x, y) in existing:
            continue
        if lcsc not in lcsc_map:
            failed.append((des, lcsc, "unresolved"))
            continue
        lib, uuid = lcsc_map[lcsc]
        rc, out, err = run(["sch", "place", "--lib", lib, "--uuid", uuid,
                            "--x", str(x), "--y", str(y)])
        if rc != 0:
            failed.append((des, lcsc, (err or out)[-100:]))
            continue
        placed += 1
        if placed % 10 == 0:
            run(["sch", "save"])
            print(f"  ...{placed} placed, saved")
    run(["sch", "save"])
    print(f"placed {placed}, failed {len(failed)}: {failed}")

    bycoord = {(round(c["x"]), round(c["y"])): c["primitiveId"] for c in list_parts()}
    renamed, misses = 0, []
    for des, lcsc, x, y in plan:
        pid = bycoord.get((x, y))
        if not pid:
            misses.append(des)
            continue
        rc, _, _ = run(["sch", "modify", "--id", pid,
                        "--patch", json.dumps({"designator": des})])
        renamed += 0 if rc else 1
    run(["sch", "save"])
    print(f"designators set {renamed}, misses: {misses}")
    if failed or misses:
        sys.exit(1)


if __name__ == "__main__":
    main()
