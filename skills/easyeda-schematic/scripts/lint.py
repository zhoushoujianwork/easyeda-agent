#!/usr/bin/env python3
"""Data-only schematic linter: read full layout JSON, report problem points."""
import json, sys
from collections import defaultdict, Counter
import orient

_args = [a for a in sys.argv[1:] if not a.startswith('--')]
JSON_OUT = '--json' in sys.argv[1:]   # structured findings for diff.py; text otherwise
data = json.load(open(_args[0]))
parts  = [p for p in data['parts'] if p.get('type') == 'part']
sheets = [p for p in data['parts'] if p.get('type') == 'sheet']
flags  = data['flags']
wires  = data['wires']

def segs(line):
    if not line: return []
    pts = [tuple(p) for p in line] if isinstance(line[0], list) else list(zip(line[0::2], line[1::2]))
    return list(zip(pts, pts[1:]))

# ---- connectivity index ----
wire_endpoints = defaultdict(list)   # point -> list of neighbor points (along a wire)
all_zero_wires = []
for w in wires:
    for (a, b) in segs(w.get('line') or []):
        if a == b:
            all_zero_wires.append((w['pid'], a)); continue
        wire_endpoints[a].append(b)
        wire_endpoints[b].append(a)

pin_pts = {}     # (x,y) -> "DES.pin"
for p in parts:
    for pin in p.get('pins', []):
        pin_pts[(pin['x'], pin['y'])] = f"{p.get('designator')}.{pin['num']}({pin['name']})"

flag_pts = {(f['x'], f['y']): f for f in flags}

def direction(frm, to):
    dx, dy = to[0]-frm[0], to[1]-frm[1]
    if dx == 0 and dy == 0: return None
    # EasyEDA is y-UP: +y renders upward on screen (verified via bbox calibration).
    return ('right' if dx>0 else 'left') if abs(dx) >= abs(dy) else ('up' if dy>0 else 'down')

# Orientation table — DERIVED from the canonical spec (orientation.json), never
# hand-edited here. Body points outward along the stub. The connector's
# connect_pin (extension/src/actions.ts deriveBodyRotation) derives the same
# table from the same four facts; tests/run.py asserts they agree. §3.5.
BODY_ROT = orient.load_body_rotation()
def family(f):
    if f['type'] == 'netport': return 'port'
    n = (f.get('net') or '').lower()
    return 'ground' if any(g in n for g in ('gnd','vss','agnd','dgnd','pgnd')) else 'power'

problems = defaultdict(list)

# ---- Check 1: netflag/netport orientation (顺着导线) ----
for f in flags:
    pt = (f['x'], f['y'])
    nbrs = wire_endpoints.get(pt)
    if not nbrs:
        problems['flag_no_wire'].append(f"{f['type']} {f['net']} @{pt} 没有 wire 连到它（悬空标识）")
        continue
    toward = direction(pt, nbrs[0])           # wire leaves toward circuit
    body = {'up':'down','down':'up','left':'right','right':'left'}[toward]  # body points opposite
    fam = family(f)
    want = BODY_ROT[fam].get(body)
    if want is not None and f['rotation'] != want:
        problems['orientation'].append(
            f"{f['type']:>7} {f['net']:>9} @{pt}  rot={f['rotation']:>3} 应为 {want:>3}  "
            f"(导线朝{toward}, body应朝{body}, {fam})")

# ---- Check 2: flag overlaps a pin (DRC fatal) ----
for f in flags:
    if (f['x'], f['y']) in pin_pts:
        problems['flag_on_pin'].append(f"{f['type']} {f['net']} @({f['x']},{f['y']}) 与引脚 {pin_pts[(f['x'],f['y'])]} 重叠 → DRC fatal")

# ---- Check 3: floating pins (no wire, no flag at pin) ----
for p in parts:
    for pin in p.get('pins', []):
        pt = (pin['x'], pin['y'])
        if pt not in wire_endpoints and pt not in flag_pts:
            problems['floating_pin'].append(f"{p['designator']}.{pin['num']}({pin['name']}) @{pt} 悬空（无导线/标识）")

# ---- Check 4: zero-length wires ----
for pid, pt in all_zero_wires:
    problems['zero_wire'].append(f"wire {pid} @{pt} 零长度（DRC 会报错）")

# ---- Check 5: off-grid ----
for p in parts + flags:
    if p['x'] % 5 or p['y'] % 5:
        problems['off_grid'].append(f"{p.get('designator') or p.get('type')} @({p['x']},{p['y']}) 不在 5 网格上")

# ---- Check 6: bbox overlap between parts ----
def overlap(a, b):
    return not (a[2] < b[0] or b[2] < a[0] or a[3] < b[1] or b[3] < a[1])
bb = [(p['designator'], p['bbox']) for p in parts if p.get('bbox')]
for i in range(len(bb)):
    for j in range(i+1, len(bb)):
        if overlap(bb[i][1], bb[j][1]):
            problems['bbox_overlap'].append(f"{bb[i][0]} 与 {bb[j][0]} bbox 重叠")

# ---- Check 7: duplicate designators ----
desigs = Counter(p['designator'] for p in parts if p.get('designator'))
for dd, n in desigs.items():
    if n > 1:
        problems['dup_designator'].append(f"位号 {dd} 出现 {n} 次")

# ---- Check 8: net-ports of same net close on one page (use a wire/label) ----
ports = [f for f in flags if f['type'] == 'netport']
byport = defaultdict(list)
for p in ports: byport[p['net']].append(p)
for net, ps in byport.items():
    for i in range(len(ps)):
        for j in range(i+1, len(ps)):
            dist = abs(ps[i]['x']-ps[j]['x']) + abs(ps[i]['y']-ps[j]['y'])
            if dist <= 300:
                problems['netport_hop'].append(
                    f"net-port '{net}' @({ps[i]['x']},{ps[i]['y']}) 与 @({ps[j]['x']},{ps[j]['y']}) 仅隔 {dist} → 同页应改导线/net label")

# ---- Check 9: different-net flags collinear through a component (visual false-short) ----
for p in parts:
    cx, cy, bb = p['x'], p['y'], p.get('bbox')
    if not bb: continue
    near = [f for f in flags if abs(f['x']-cx) <= 20 and bb[1]-80 <= f['y'] <= bb[3]+80]
    above = [f for f in near if f['y'] < cy]
    below = [f for f in near if f['y'] > cy]
    if above and below and {f['net'] for f in above} != {f['net'] for f in below}:
        problems['collinear_flags'].append(
            f"{p['designator']} @({cx},{cy}): 上 {[f['net'] for f in above]} / 下 {[f['net'] for f in below]} 在 x≈{cx} 共线 → 视觉像导线穿过元件")

# ---- Check 10: dangling wire ends (空连: degree-1 wire vertex with no pin/flag) ----
deg = defaultdict(int)
for w in wires:
    for (a, b) in segs(w.get('line') or []):
        if a == b: continue
        deg[a] += 1; deg[b] += 1
for pt, dgr in deg.items():
    if dgr == 1 and pt not in pin_pts and pt not in flag_pts:
        problems['dangling_wire'].append(f"导线端点 @{pt} 空连（degree=1，无引脚/标识）")

# ---- net trace (union-find) for power/ground audit ----
uf = {}
def ufind(x):
    uf.setdefault(x, x)
    while uf[x] != x: uf[x] = uf[uf[x]]; x = uf[x]
    return x
def uunion(a, b): ufind(a); ufind(b); uf[ufind(a)] = ufind(b)
for w in wires:
    pl = segs(w.get('line') or [])
    for (a, b) in pl:
        if a != b: uunion(a, b)
for pt in list(pin_pts) + list(flag_pts):
    ufind(pt)
net_members = defaultdict(lambda: {'pins': set(), 'flags': set()})
net_pts = defaultdict(set)   # root -> set of (x,y) pin/flag points on that net (for span/proximity)
for pt in list(uf):
    root = ufind(pt)
    if pt in pin_pts: net_members[root]['pins'].add(pin_pts[pt]); net_pts[root].add(pt)
    if pt in flag_pts: net_members[root]['flags'].add(f"{flag_pts[pt]['net']}"); net_pts[root].add(pt)

def is_power(n): return any(p in n.lower() for p in ('vcc','vdd','3v3','5v','+','vbat','vbus'))
def is_gnd(n):   return any(g in n.lower() for g in ('gnd','vss','agnd'))

# ---- Check 11: single-pin nets (一个引脚连到孤立网络 = 实质空连) ----
# ---- Check 12: unnamed multi-pin signal nets (没命名, 可读性/跨页隐患) ----
for root, m in net_members.items():
    pins, fset = m['pins'], m['flags']
    if not pins and not fset: continue
    has_pwr = any(is_power(n) for n in fset)
    has_gnd = any(is_gnd(n) for n in fset)
    sig_flags = [n for n in fset if not is_power(n) and not is_gnd(n)]
    if len(pins) == 1 and not fset:
        problems['single_pin_net'].append(f"引脚 {next(iter(pins))} 所在网络只有它自己、且无电源/地/标识 → 空连")
    if len(pins) >= 2 and not fset:
        problems['unnamed_net'].append(f"网络 {{{', '.join(sorted(pins))}}} 多引脚但无命名标识（建议加 net label / 电源地）")

# ==== auto-layout SOP enforcement (auto-layout-sop.md) ====
# These catch the bulk-realization defect (scattered decaps + flag-every-pin), which
# only manifests at board scale — gate on total pin count so tiny focused fixtures
# (which legitimately use a couple of flags) don't trip them.
total_pins = sum(len(p.get('pins', [])) for p in parts)
SOP_SCALE = total_pins >= 30
if SOP_SCALE:
    # IC pins on a power net = the "VCC pads" decoupling should hug.
    ic_pins = {(pin['x'], pin['y']): p['designator']
               for p in parts if len(p.get('pins', [])) > 4 for pin in p['pins']}
    power_pin_pts = set()
    for root, m in net_members.items():
        if any(is_power(n) for n in m['flags']):
            power_pin_pts |= {pt for pt in net_pts[root] if pt in ic_pins}

    # ---- Check 13: decoupling not near its IC VCC pad (§6 / SOP Step 2) ----
    for p in parts:
        if not str(p.get('designator', '')).startswith('C') or len(p.get('pins', [])) != 2:
            continue
        if not power_pin_pts:
            continue
        on_power = any(any(is_power(n) for n in net_members[ufind((pin['x'], pin['y']))]['flags'])
                       for pin in p['pins'])
        if not on_power:
            continue
        cx, cy = p['x'], p['y']
        dmin = min(abs(cx - px) + abs(cy - py) for px, py in power_pin_pts)
        if dmin > 120:
            problems['decap_far'].append(
                f"{p['designator']} 距最近 IC 电源引脚 {dmin}u > 120u MUST → 去耦未就近 (§6 / SOP Step2，应贴 VCC 焊盘)")

    # ---- Check 14: over-flagging — flag count ≈ pin count (SOP Step 3) ----
    if len(flags) / total_pins > 0.6:
        problems['flag_density'].append(
            f"标识/端口 {len(flags)} / 引脚 {total_pins} = {len(flags)/total_pins:.0%} > 60% "
            f"→ 过度按名接线(flag-every-pin),簇内/短网络应改本地导线 (SOP Step3 决策表)")

    # ---- Check 15: short ≥2-pin signal net realized as scattered flags vs a local wire ----
    # Group by net NAME, not union-find root: a "by-name" net is split into one root per
    # pin (pin→stub→flag, no wire between them), so per-root it looks like single-pin nets.
    sig_by_name = defaultdict(lambda: {'pins': set(), 'pts': set()})
    for root, m in net_members.items():
        for n in m['flags']:
            if is_power(n) or is_gnd(n):
                continue
            sig_by_name[n]['pins'] |= m['pins']
            sig_by_name[n]['pts'] |= net_pts[root]
    for n, d in sig_by_name.items():
        if not (2 <= len(d['pins']) <= 3) or len(d['pts']) < 2:
            continue
        pts = d['pts']
        span = (max(x for x, y in pts) - min(x for x, y in pts)) + (max(y for x, y in pts) - min(y for x, y in pts))
        if span < 250:
            problems['local_net_as_flag'].append(
                f"信号网 '{n}' ({len(d['pins'])}脚, 跨度 {span}u < 250) 由分散标识连 → 同簇应本地 pin→wire→pin (SOP §3.6)")

order = ['flag_on_pin','zero_wire','dangling_wire','floating_pin','single_pin_net',
         'flag_no_wire','orientation','bbox_overlap','dup_designator','decap_far',
         'netport_hop','local_net_as_flag','flag_density','collinear_flags','unnamed_net','off_grid']
labels = {
  'flag_on_pin':'🔴 标识与引脚重叠 (DRC fatal)','zero_wire':'🔴 零长度导线',
  'dangling_wire':'🔴 导线空连端点','floating_pin':'🟠 悬空引脚',
  'single_pin_net':'🟠 单引脚孤网 (空连)','flag_no_wire':'🟠 标识无导线',
  'orientation':'🟡 朝向不顺导线','bbox_overlap':'🟠 元件 bbox 重叠',
  'dup_designator':'🟠 重复位号','netport_hop':'🟡 net-port 同页近距离',
  'collinear_flags':'🟡 异网标识共线穿元件','unnamed_net':'🔵 多引脚网络未命名',
  'off_grid':'🔵 不在 5 网格',
  'decap_far':'🟠 去耦未就近 IC 电源脚 (§6/SOP)','flag_density':'🟡 过度按名接线 (flag≈pin)',
  'local_net_as_flag':'🟡 短信号网用标识应改本地线',
}
# Structured findings — (rule, msg) is a stable identity for diff.py: the same
# problem yields the same deterministic msg across runs.
findings = [{'rule': k, 'label': labels[k], 'msg': m} for k in order for m in problems[k]]
summary = {'parts': len(parts), 'flags': len(flags), 'wires': len(wires), 'sheets': len(sheets),
           'nets': len([m for m in net_members.values() if m['pins'] or m['flags']]),
           'problems': len(findings)}

if JSON_OUT:
    print(json.dumps({'summary': summary, 'findings': findings}, ensure_ascii=False))
else:
    print(f"== 概览 ==  parts={summary['parts']} flags={summary['flags']} wires={summary['wires']} sheets={summary['sheets']} nets≈{summary['nets']}")
    if not findings:
        print("\n✅ 无问题")
    for k in order:
        if problems[k]:
            print(f"\n{labels[k]} ({len(problems[k])}):")
            for line in problems[k]:
                print(f"  - {line}")
