#!/usr/bin/env python3
"""抄图训练验收器:抄版 sch read 输出 vs golden spec 逐 pin 机械对照。

用法: copy-check.py <golden-spec.json> <copy-sch-read.json>
判定: 每个 designator 的每个「golden 里有网络的 pin」在抄版里网络一致(名称级)。
      抄版多连/漏连/网络名不一致都列出。100% 一致 exit 0,否则 exit 1。
"""
import json, sys

golden = json.load(open(sys.argv[1]))
copy_r = json.load(open(sys.argv[2]))["result"]
copy = {}
for c in copy_r["components"]:
    d = c.get("designator")
    if d:
        copy[d] = {p["number"]: p.get("net") for p in c.get("pins", [])}

ok = miss_part = mismatch = extra = 0
issues = []
for d, v in sorted(golden.items()):
    if d not in copy:
        miss_part += 1
        issues.append(f"缺器件 {d}")
        continue
    for pin, net in v["nets"].items():
        got = copy[d].get(pin)
        if got == net:
            ok += 1
        else:
            mismatch += 1
            issues.append(f"{d}:{pin} 应为 {net!r} 实为 {got!r}")
    # 抄版多连(golden 该 pin 无网络但抄版有)
    for pin, got in copy[d].items():
        if got and pin not in v["nets"]:
            extra += 1
            issues.append(f"{d}:{pin} 多连 {got!r}(golden 为 NC)")

total = ok + mismatch
print(f"对照: {ok}/{total} pin 一致 | 缺件 {miss_part} | 不一致 {mismatch} | 多连 {extra}")
for i in issues[:40]:
    print("  ✗", i)
if issues and len(issues) > 40:
    print(f"  … 共 {len(issues)} 条")
sys.exit(0 if not issues else 1)
