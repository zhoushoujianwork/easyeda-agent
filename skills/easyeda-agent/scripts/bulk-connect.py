#!/usr/bin/env python3
"""bulk-connect — 连接 spec 驱动的整页电气实现 + 期望网表验证门 + 悬空脚修复循环。

用法:
    EASYEDA_PROJECT=<工程名> bulk-connect.py <spec.json>                 # 执行 + 验证
    EASYEDA_PROJECT=<工程名> bulk-connect.py <spec.json> --verify-only   # 只验证
    EASYEDA_PROJECT=<工程名> bulk-connect.py <spec.json> --repair-floaters  # 悬空脚重连循环

spec 形状:
    {
      "page":  "P1_POWER",
      "rails": [ {"pin": "C4:1", "kind": "power", "net": "+5V"},
                 {"pin": "C4:2", "kind": "gnd",   "net": "GND"} ],
      "ports": [ {"pin": "R4:2", "net": "U2_FB"} ],
      "nc":    { "U3": ["15", "9"] }
    }

策略（box-v2 / 51 件整页实测收敛，2026-07-09）:
- **全部连接走 pin→短桩→netflag/netport**（autoconnect），内部信号网起可读网名
  （U2_FB / SW1 …）用 netport 同名互连。**不要用长导线连远脚**——器件按行列
  排布时长线极易与同行引脚/其他线共线，EasyEDA 把共线相触导线合并成一条 →
  大面积隐性短路（实测两次全页短接，见 issue #64）。
- 验证只信数据：`sch read` 把每个 spec 组（rail/port 同名成员）读回真实 net，
  不一致逐条列出；`sch check --json` 是**裸对象输出**（无 result 信封，issue #66），
  且 doc switch 后有竞态要 settle + 重试（issue #67）。
- `--repair-floaters`: 从 sch check 的 floating-pin(pinDetails 带坐标) 出发，按
  spec 决定 kind/net，方向×偏移在 occupancy（引脚+线顶点+flag 锚点）里搜第一个
  干净落点，`sch connect` 显式落笔。循环直到无悬空或不再收敛。
"""
import json
import os
import subprocess
import sys
import time

PROJECT = os.environ.get("EASYEDA_PROJECT", "")


def run(args, timeout=120, retries=3):
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


def settle(page, timeout_s=15):
    rc, out, _ = run(["doc", "switch", page])
    if rc != 0:
        raise SystemExit(f"doc switch {page} failed")
    prev = -1
    for _ in range(int(timeout_s / 1.5)):
        rc, out, _ = run(["sch", "list"])
        n = len([c for c in (jparse(out).get("result") or {}).get("components", [])
                 if c.get("componentType") == "part"])
        if n and n == prev:
            return n
        prev = n
        time.sleep(1.5)
    return prev


def check_findings(max_tries=5):
    """sch check --json: 顶层裸对象(issue #66) + 就绪竞态重试(issue #67)。"""
    for _ in range(max_tries):
        rc, out, _ = run(["sch", "check", "--json"])
        d = jparse(out)
        if d.get("summary") is not None:
            return d
        time.sleep(2.5)
    return {}


def occupancy():
    """引脚 + 线顶点 + flag 锚点的占用集，供落笔方向搜索。"""
    occ = set()
    rc, out, _ = run(["sch", "list", "--include-pins"])
    for c in (jparse(out).get("result") or {}).get("components", []):
        for p in c.get("pins", []):
            if p.get("x") is not None:
                occ.add((round(p["x"]), round(p["y"])))
    code = ("const pts=[];for(const w of await eda.sch_PrimitiveWire.getAll()){const l=w.getState_Line();"
            "const f=Array.isArray(l[0])?l.flat():l;for(let i=0;i<f.length;i+=2)pts.push([f[i],f[i+1]]);}"
            "for(const c of await eda.sch_PrimitiveComponent.getAll()){const t=c.getState_ComponentType();"
            "if(t!=='part'&&t!=='sheet')pts.push([c.getState_X(),c.getState_Y()]);}return pts;")
    rc, out, _ = run(["call", "debug.exec_js", "--payload", json.dumps({"code": code})])
    for pt in ((jparse(out).get("result") or {}).get("value") or []):
        occ.add((round(pt[0]), round(pt[1])))
    return occ


def connect_free(occ, x, y, kind, net):
    """在占用集里找第一个干净的方向×偏移并落笔。"""
    for d0, dx, dy in [("down", 0, 1), ("up", 0, -1), ("left", -1, 0), ("right", 1, 0)]:
        for off in (20, 30, 40, 50):
            ex, ey = x + dx * off, y + dy * off
            if (round(ex), round(ey)) in occ:
                continue
            if any((round(x + dx * t), round(y + dy * t)) in occ for t in range(5, off, 5)):
                continue
            rc, _, _ = run(["sch", "connect", "--x", str(x), "--y", str(y),
                            "--kind", kind, "--net", net,
                            "--direction", d0, "--offset", str(off)])
            if rc == 0:
                occ.add((round(ex), round(ey)))
                return f"{d0}{off}"
            return None
    return None


def verify(spec):
    rc, out, _ = run(["sch", "read"], 240)
    r = jparse(out).get("result") or {}
    pin2net = {}
    for c in r.get("components", []):
        for p in c.get("pins", []):
            pin2net[f"{c.get('designator')}:{p.get('number')}"] = p.get("net")
    bad = []
    for rail in spec.get("rails", []):
        n = pin2net.get(rail["pin"])
        if n != rail["net"]:
            bad.append(f"RAIL {rail['pin']} expect {rail['net']} got {n}")
    for port in spec.get("ports", []):
        n = pin2net.get(port["pin"])
        if n != port["net"]:
            bad.append(f"PORT {port['pin']} expect {port['net']} got {n}")
    floats = r.get("floatingPins") or []
    print(f"VERIFY: {len(bad)} group problems; floatingPins={len(floats)} {floats[:8]}")
    for b in bad[:30]:
        print("  !", b)
    return not bad


def repair_floaters(spec):
    pin_net = {}
    for r in spec.get("rails", []):
        pin_net[r["pin"]] = ({"gnd": "gnd", "power": "power"}.get(r["kind"], r["kind"]), r["net"])
    for p in spec.get("ports", []):
        pin_net[p["pin"]] = ("netport", p["net"])
    nc = {f"{d}:{n}" for d, pl in (spec.get("nc") or {}).items() for n in pl}
    for rnd in range(4):
        d = check_findings()
        fl = []
        for f in d.get("findings", []):
            if f.get("type") == "floating-pin":
                for pd in f.get("pinDetails", []):
                    ref = f"{f.get('designator')}:{pd.get('number')}"
                    if ref not in nc:
                        fl.append((ref, pd.get("x"), pd.get("y")))
        print(f"repair round{rnd}: floating={len(fl)}")
        if not fl:
            return True
        occ = occupancy()
        progressed = False
        for ref, x, y in fl:
            if ref not in pin_net:
                print("  no-spec:", ref)
                continue
            kind, net = pin_net[ref]
            r = connect_free(occ, x, y, kind, net)
            print(f"  {ref} -> {net}: {r or 'FAIL'}")
            progressed = progressed or bool(r)
        run(["sch", "save"])
        if not progressed:
            return False
        time.sleep(2)
    return False


def main():
    spec = json.load(open(sys.argv[1]))
    mode = sys.argv[2] if len(sys.argv) > 2 else ""
    settle(spec["page"])

    if mode == "--repair-floaters":
        ok = repair_floaters(spec)
        verify(spec)
        sys.exit(0 if ok else 1)

    if mode != "--verify-only":
        conns = [dict(pin=r["pin"], kind=r["kind"], net=r["net"]) for r in spec.get("rails", [])]
        conns += [dict(pin=p["pin"], kind="netport", net=p["net"]) for p in spec.get("ports", [])]
        if conns:
            acspec = {"connections": conns,
                      "rules": {"avoidTitleBlock": True, "avoidPinFanout": True,
                                "staggerLabels": True, "offsetRange": [18, 80],
                                "offsetStep": 6, "minLabelGap": 12}}
            fn = f"/tmp/ac_{spec['page']}.json"
            json.dump(acspec, open(fn, "w"), ensure_ascii=False)
            rc, out, err = run(["sch", "autoconnect", "--spec", fn, "--json"], 600)
            res = jparse(out).get("result") or {}
            results = res.get("results") or res.get("connections") or []
            states = {}
            for r0 in results:
                st = r0.get("state") or r0.get("status") or ("ok" if r0.get("selected") else "?")
                states[st] = states.get(st, 0) + 1
            print(f"autoconnect: {len(conns)} requested ->", states)
            run(["sch", "save"])
        for des, pl in (spec.get("nc") or {}).items():
            run(["sch", "no-connect", "--designator", des, "--pin", ",".join(pl)])
        run(["sch", "save"])

    ok = verify(spec)
    sys.exit(0 if ok else 1)


if __name__ == "__main__":
    main()
