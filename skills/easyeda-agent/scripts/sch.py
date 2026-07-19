#!/usr/bin/env python3
"""Stable schematic executor — wraps the core CLI ops into a churn-resilient API for the
data-driven self-adjust loop (place → read coords → judge spacing/wire-length/connectivity
→ move → re-read). Consolidates the patterns debugged on box-v2 (SOP-W/F/D + union-find +
snapshot retrieval) so the AI doesn't re-derive them each time.

Usage (from Bash / a scratch script):
    import sys; sys.path.insert(0, 'skills/easyeda-agent/scripts')
    import sch; sch.PROJECT = 'motobox2026'
    st  = sch.read()                       # page state {parts,wires,flags}
    pid = sch.place(lib, uuid, 300, 400, 'R1')
    sch.wire(220, 300, 400, 300, st['pin_xy'])   # orthogonal, pin-free (SOP-W)
    sch.rail_flag(280, 400, 'GND', 'ground', 'down')   # oriented rail flag (SOP-F)
    for c in sch.connectivity(): print(c)  # union-find {net, pins[]}
    print(sch.snapshot())                  # fresh png path (.easyeda/artifacts/)

⚠️ Canvas-freeze: after API edits the EDA canvas may not auto-redraw → snapshot() can be a
stale frame. Judge STATE by read()/connectivity() (data is reliable); use snapshot() for the
human's visual check and touch the page in EDA to force a redraw first.
"""
import glob
import json
import os
import subprocess
import time

PROJECT = os.environ.get('EASYEDA_PROJECT', '')
EASYEDA = 'easyeda'
NEGATE = True   # 2026-06 build stores createNetFlag/Port rotation negated
BODY_ROT = {'power': {'up': 0, 'left': 90, 'down': 180, 'right': 270},
            'ground': {'up': 180, 'left': 270, 'down': 0, 'right': 90},
            'port': {'up': 90, 'left': 180, 'down': 270, 'right': 0}}
_OFF = {'up': (0, 20), 'down': (0, -20), 'left': (-20, 0), 'right': (20, 0)}


def _cli(args, timeout=90, retries=5):
    proj = ['--project=' + PROJECT] if PROJECT else []
    for _ in range(retries):
        try:
            # utf-8 固定编码:Windows 中文环境 text=True 会走 GBK 解码崩溃(issue #133 Bug 4)
            p = subprocess.run([EASYEDA] + args + proj, capture_output=True,
                               encoding='utf-8', errors='replace', timeout=timeout)
            out = p.stdout.strip()
            if not out:
                time.sleep(1.5); continue
            d = json.loads(out)
            if not d.get('ok'):
                err = str(d.get('error', ''))
                if 'connect' in err.lower() or 'window' in err.lower():
                    time.sleep(1.5); continue
                return {'_err': d.get('error')}
            r = d.get('result', {})
            return r.get('value', r) if isinstance(r, dict) else r
        except Exception:
            time.sleep(1.5)
    return {'_err': 'retries exhausted'}


def ejs(code, timeout=90):
    return _cli(['call', 'debug.exec_js', '--payload', json.dumps({'code': code})], timeout)


# ── read / state ────────────────────────────────────────────────────────────
def read():
    """Active-page state. Returns {parts:[{d,x,y,r,pins:[{n,name,x,y}]}], wires:[Line], flags:[{net,x,y,r}],
    pin_xy:[(x,y)…]}. pin_xy is every pin coord (pass to wire() so routes avoid crossing pins)."""
    code = ("const parts=[],flags=[];for(const c of await eda.sch_PrimitiveComponent.getAll()){"
            "const t=c.getState_ComponentType();"
            "if(t==='part'){const pins=[];try{for(const p of await eda.sch_PrimitiveComponent.getAllPinsByPrimitiveId(c.getState_PrimitiveId()))"
            "pins.push({n:p.getState_PinNumber(),name:p.getState_PinName?p.getState_PinName():null,x:p.getState_X(),y:p.getState_Y()});}catch(e){}"
            "parts.push({d:c.getState_Designator(),x:c.getState_X(),y:c.getState_Y(),r:c.getState_Rotation(),pins});}"
            "else if(t!=='sheet'){flags.push({net:c.getState_Net?c.getState_Net():null,x:c.getState_X(),y:c.getState_Y(),r:c.getState_Rotation()});}}"
            "const wires=[];for(const w of await eda.sch_PrimitiveWire.getAll())wires.push(w.getState_Line());return {parts,flags,wires};")
    s = ejs(code, 120)
    if isinstance(s, dict) and '_err' not in s:
        s['pin_xy'] = [(p['x'], p['y']) for pt in s['parts'] for p in pt['pins'] if p['x'] is not None]
    return s


def pin_of(state, designator, num_or_name):
    for p in state['parts']:
        if p['d'] == designator:
            for pin in p['pins']:
                if str(pin['n']) == str(num_or_name) or (pin['name'] and pin['name'] == num_or_name):
                    return (pin['x'], pin['y'])
    return None


# ── mutate: place / move / delete ───────────────────────────────────────────
def place(lib, uuid, x, y, designator):
    return ejs(f"const c=await eda.sch_PrimitiveComponent.create({{libraryUuid:'{lib}',uuid:'{uuid}'}},{int(x)},{int(y)});"
               f"const pid=c.getState_PrimitiveId();try{{await eda.sch_PrimitiveComponent.modify(pid,{{designator:'{designator}'}});}}catch(e){{}}return pid;")


def move(designator, x, y):
    return ejs(f"for(const c of await eda.sch_PrimitiveComponent.getAll())if(c.getState_Designator()==='{designator}')"
               f"{{await eda.sch_PrimitiveComponent.modify(c.getState_PrimitiveId(),{{x:{int(x)},y:{int(y)}}});return 1;}}return 0;")


def delete(designators):
    return ejs("const D=" + json.dumps(designators) + ";const ids=[];for(const c of await eda.sch_PrimitiveComponent.getAll())"
               "if(D.includes(c.getState_Designator()))ids.push(c.getState_PrimitiveId());"
               "if(ids.length)await eda.sch_PrimitiveComponent.delete(ids);return ids.length;")


def clear_wires_flags():
    return ejs("const w=[];for(const x of await eda.sch_PrimitiveWire.getAll())w.push(x.getState_PrimitiveId());"
               "if(w.length)await eda.sch_PrimitiveWire.delete(w);const f=[];"
               "for(const c of await eda.sch_PrimitiveComponent.getAll()){const t=c.getState_ComponentType();"
               "if(t!=='part'&&t!=='sheet')f.push(c.getState_PrimitiveId());}"
               "if(f.length)await eda.sch_PrimitiveComponent.delete(f);return [w.length,f.length];")


# ── wire / flag / decouple (SOP-W / F / D) ──────────────────────────────────
def _route(ax, ay, bx, by, pins):
    """Orthogonal route avoiding crossing any other pin (SOP-W). Returns flat point list."""
    def clear_h(y, x1, x2):
        lo, hi = sorted([x1, x2]); return not any(round(py) == round(y) and lo < round(px) < hi for px, py in pins)
    def clear_v(x, y1, y2):
        lo, hi = sorted([y1, y2]); return not any(round(px) == round(x) and lo < round(py) < hi for px, py in pins)
    if ax == bx or ay == by:
        return [ax, ay, bx, by]
    if clear_h(ay, ax, bx) and clear_v(bx, ay, by):
        return [ax, ay, bx, ay, bx, by]
    if clear_v(ax, ay, by) and clear_h(by, ax, bx):
        return [ax, ay, ax, by, bx, by]
    return [ax, ay, bx, ay, bx, by]


def wire(ax, ay, bx, by, pins=None):
    """One orthogonal wire ax,ay→bx,by, avoiding other pins. Endpoints must land on pin coords."""
    pts = _route(ax, ay, bx, by, pins or [])
    return ejs("await eda.sch_PrimitiveWire.create(" + json.dumps(pts) + ");return 1;")


def wire_net(pin_coords, pins=None):
    """Connect a net's pins by CHAINING pin→pin (SOP-W: anchored, never star-to-free-junction).
    pin_coords sorted nearest-first gives short hops."""
    pc = list(pin_coords)
    for i in range(len(pc) - 1):
        wire(pc[i][0], pc[i][1], pc[i + 1][0], pc[i + 1][1], pins)


def rail_flag(px, py, net, family, direction, offset=20):
    """pin→outward stub→oriented power/ground flag (family='power'/'ground') or net port (family='port').
    Rails only; signals use wire(). Orientation auto-derived + negation-compensated."""
    rot = BODY_ROT[family][direction]
    applied = (360 - rot) % 360 if NEGATE else rot
    dx, dy = _OFF[direction]; ex, ey = px + dx, py + dy
    enum = {'power': 'Power', 'ground': 'Ground', 'port': 'BI'}[family]
    fn = 'createNetFlag' if family in ('power', 'ground') else 'createNetPort'
    return ejs(f"await eda.sch_PrimitiveWire.create([{px},{py},{ex},{ey}]);"
               f"await eda.sch_PrimitiveComponent.{fn}('{enum}','{net}',{ex},{ey},{applied});return 1;")


def decouple(state, ic, vcc_name, cap_lib, cap_uuid, designator, rail='3V3', side='right', dist=110):
    """SOP-D: place a decap ~dist off the IC's vcc pin (pin-free side), wire VCC→cap, GND flag + rail flag."""
    vcc = pin_of(state, ic, vcc_name)
    if not vcc:
        return {'_err': f'no pin {vcc_name} on {ic}'}
    sx = vcc[0] + dist if side == 'right' else vcc[0] - dist
    place(cap_lib, cap_uuid, sx, vcc[1], designator)
    time.sleep(0.4)
    cp = read()
    near = pin_of(cp, designator, '1'); far = pin_of(cp, designator, '2')
    if near and far and near[0] > far[0]:
        near, far = far, near       # near = closer to VCC
    if near:
        wire(vcc[0], vcc[1], near[0], near[1], cp['pin_xy'])
        rail_flag(near[0], near[1], rail, 'power', 'up')
    if far:
        rail_flag(far[0], far[1], 'GND', 'ground', 'down')
    return {'near': near, 'far': far, 'dist': abs(vcc[0] - (near[0] if near else vcc[0]))}


# ── verify: connectivity (union-find) + snapshot ────────────────────────────
def connectivity(state=None):
    """Union-find over pins+wires (Line parsed as 4-num segments); flags label nodes.
    Returns [{net, pins:[…]}] — the authoritative connectivity (wire getState_Net is unreliable)."""
    s = state or read()
    par = {}
    def find(k):
        par.setdefault(k, k)
        while par[k] != k:
            par[k] = par[par[k]]; k = par[k]
        return k
    def key(x, y): return (round(float(x)), round(float(y)))
    def flat(w):
        o = []
        for e in (w if isinstance(w, list) else [w]):
            o.extend(e) if isinstance(e, list) else o.append(e)
        return [float(n) for n in o]
    for w in s['wires']:
        n = flat(w)
        for i in range(0, len(n) - 3, 4):
            par[find(key(n[i], n[i + 1]))] = find(key(n[i + 2], n[i + 3]))
    label = {}
    for f_ in s['flags']:
        label[find(key(f_['x'], f_['y']))] = f_['net']
    comp = {}
    for p in s['parts']:
        for pin in p['pins']:
            if pin['x'] is None:
                continue
            comp.setdefault(find(key(pin['x'], pin['y'])), []).append(f"{p['d']}.{pin['n']}")
    return [{'net': label.get(root, ''), 'pins': sorted(v)} for root, v in comp.items()]


def snapshot():
    """Typed snapshot → returns the fresh png path under .easyeda/artifacts/.
    ⚠️ canvas-freeze: may be stale after API edits — touch the page in EDA first."""
    _cli(['view', 'fit']); time.sleep(1.5)
    _cli(['sch', 'snapshot']); time.sleep(1.5)
    arts = glob.glob('.easyeda/artifacts/*.png')
    return max(arts, key=os.path.getmtime) if arts else None
