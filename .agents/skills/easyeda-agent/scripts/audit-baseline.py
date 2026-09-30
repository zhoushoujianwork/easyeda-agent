#!/usr/bin/env python3
"""从 audit log 离线刨「暴露面健康度」基线 —— 不需要连编辑器,不需要跑真机。

    .agents/skills/easyeda-agent/scripts/audit-baseline.py            # 全部历史
    .agents/skills/easyeda-agent/scripts/audit-baseline.py 2026-08    # 只看某月/某天

读 ~/.easyeda-agent/audit/*.jsonl(daemon 每次 dispatch 落一行),输出三张表:

1. **调用分布 + 失败率**(按 action 分组)。判读法:把 action 按调用量排序后分
   头部/长尾两档比失败率 —— **长尾失败率显著高于头部 = 有「用得少所以坏了没人
   知道」的角落**。2026-08-03 首测:原理图头部 8 个 action 占 93.3% 调用、2.5%
   失败率,长尾 15 个占 1.55% 调用却有 12.6% 失败率(5 倍)。
2. **错路回退**:某 action 失败后 60s 内 agent 改调了哪个 action。同一个失败对应
   多条不同回退路径 = 该失败没有规定处置方式,agent 在猜。
3. **逐日多样性**:某天用了多少种 action —— 端到端跑完当天的种类数就是
   该批次的调用覆盖指标，用于比较回归批次，不能代替成功率。

**用 0 次成功来找死命令**:表里 `调用 N / 失败 N`(失败率 100%)的行是从未工作过
的命令。首测抓到 `schematic.titleblock.modify` 32 次调用 0 次成功。

注意读的是 typed action 名而非 CLI 子命令名 —— 一条复合 CLI 命令(如
`sch autolayout`)会展开成多个底层 action,纯 Go 侧命令则完全不出现在这里。
"""
import json, sys, glob, os
from collections import Counter, defaultdict
from datetime import datetime

# 与 daemon 写侧同源:EASYEDA_AUDIT_DIR 覆写,否则 ~/.easyeda-agent/audit。
# (写侧在 internal/daemon/audit.go;两边不一致会读出一个空日志。)
audit_dir = os.environ.get("EASYEDA_AUDIT_DIR") or os.path.expanduser("~/.easyeda-agent/audit")
files = sorted(glob.glob(os.path.join(audit_dir, "*.jsonl")))
only = sys.argv[1] if len(sys.argv) > 1 else None
if only:
    files = [f for f in files if only in f]

calls = Counter()          # action -> 次数
fails = Counter()          # action -> 失败次数
errcodes = defaultdict(Counter)  # action -> errorCode -> 次数
per_day = defaultdict(Counter)   # day -> action -> 次数
seq = []                   # (ts, action, ok, errorCode) 按序,用于回退分析
total = skipped = 0

for path in files:
    day = os.path.basename(path).replace(".jsonl", "")
    with open(path, "r", encoding="utf-8", errors="replace") as fh:
        for line in fh:
            line = line.strip()
            if not line:
                continue
            try:
                rec = json.loads(line)
            except Exception:
                skipped += 1
                continue
            act = rec.get("action")
            if not act:
                continue
            total += 1
            ok = bool(rec.get("ok"))
            calls[act] += 1
            per_day[day][act] += 1
            if not ok:
                fails[act] += 1
                errcodes[act][rec.get("errorCode") or "?"] += 1
            seq.append((rec.get("ts", ""), act, ok, rec.get("errorCode") or ""))

print(f"# 总记录 {total}  解析失败 {skipped}  文件 {len(files)}\n")

def table(pred, title):
    rows = [(a, c) for a, c in calls.items() if pred(a)]
    rows.sort(key=lambda r: -r[1])
    if not rows:
        return
    print(f"## {title}  ({len(rows)} 个 action, {sum(c for _, c in rows)} 次调用)")
    print(f"{'action':<42}{'调用':>7}{'失败':>7}{'失败率':>8}  主因")
    for a, c in rows:
        f = fails.get(a, 0)
        top = errcodes[a].most_common(1)
        top = f"{top[0][0]}×{top[0][1]}" if top else ""
        print(f"{a:<42}{c:>7}{f:>7}{f/c*100:>7.1f}%  {top}")
    print()

table(lambda a: a.startswith("schematic."), "原理图 action")
table(lambda a: a.startswith("pcb."), "PCB action (对照)")
table(lambda a: not a.startswith(("schematic.", "pcb.")), "其他 action")

# ---- 错路回退:同一 action 失败后 60s 内换用另一个 action ----
print("## 错路回退候选(某 action 失败后 60s 内改调他者,取 top 25 组合)")
def ts(s):
    try:
        return datetime.fromisoformat(s.replace("Z", "+00:00")).timestamp()
    except Exception:
        return None
pivots = Counter()
for i, (t, a, ok, ec) in enumerate(seq):
    if ok or not a.startswith("schematic."):
        continue
    t0 = ts(t)
    if t0 is None:
        continue
    for j in range(i + 1, min(i + 40, len(seq))):
        t1 = ts(seq[j][0])
        if t1 is None or t1 - t0 > 60:
            break
        b = seq[j][1]
        if b != a and b.startswith("schematic.") and seq[j][2]:
            pivots[(a, ec, b)] += 1
            break
for (a, ec, b), n in pivots.most_common(25):
    print(f"{n:>5}  {a} [{ec}] → {b}")
print()

# ---- 逐日 schematic action 多样性(E2E 那几天的"用了多少种命令") ----
print("## 逐日原理图 action 多样性(种类数 / 调用数 / 失败数)")
for day in sorted(per_day):
    sch = {a: c for a, c in per_day[day].items() if a.startswith("schematic.")}
    if not sch:
        continue
    nfail = sum(1 for t, a, ok, _ in seq if a.startswith("schematic.") and not ok and t.startswith(day))
    print(f"{day}  种类 {len(sch):>3}  调用 {sum(sch.values()):>6}  失败 {nfail:>5}")
