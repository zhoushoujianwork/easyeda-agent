// Read-only orientation validation against REAL placed flags. Run via
// debug.exec_js against a connected window to confirm the encoded body-rotation
// table (orientation.json) still matches what's actually on a board:
//
//   easyeda exec --window <id> --file tools/schematic-lint/calibrate.js
//
// WHY NOT create synthetic flags: an earlier version created a flag at each
// rotation and measured its bbox. That proved UNRELIABLE — a freshly
// createNetFlag'd flag's bbox lands on the opposite horizontal side from a real,
// settled, on-canvas flag of the same kind+rotation (off-canvas / pre-render /
// stale-.eext quirk). Real, wired, rendered flags are the trustworthy ground
// truth, and reading them needs no mutation.
//
// Method: for every netflag/netport, body direction = bbox-center offset from
// the anchor (y-UP); wire direction = toward the circuit. A correctly-oriented
// flag has its body pointing OPPOSITE the wire (顺着导线朝外). For those, the
// (family, body) → rotation MUST equal the table derived from orientation.json's
// four facts (mirrored below — keep in sync). A real correctly-oriented flag
// whose rotation disagrees with the table is a RULE BUG signal (报给人看).
const CYCLE = ['up', 'left', 'down', 'right'];                 // == orientation.json rotationCycle
const ANCHOR = { power: 'up', ground: 'down', port: 'right' }; // == orientation.json bodyAnchorAtRot0
function tableRot(family, dir) {
  const ai = CYCLE.indexOf(ANCHOR[family]);
  return ((((CYCLE.indexOf(dir) - ai) % 4) + 4) % 4) * 90;
}

const opp = { up: 'down', down: 'up', left: 'right', right: 'left' };
function dir(f, t) {                                            // y-UP: +y is up
  const dx = t[0] - f[0], dy = t[1] - f[1];
  if (dx === 0 && dy === 0) return null;
  return Math.abs(dx) >= Math.abs(dy) ? (dx > 0 ? 'right' : 'left') : (dy > 0 ? 'up' : 'down');
}
function family(type, net) {
  if (type === 'netport') return 'port';
  const n = (net || '').toLowerCase();
  return /gnd|vss|agnd|dgnd|pgnd/.test(n) ? 'ground' : 'power';
}
function pts(line) {
  const r = [];
  if (!line) return r;
  if (Array.isArray(line[0])) { for (const p of line) r.push([p[0], p[1]]); }
  else { for (let i = 0; i < line.length; i += 2) r.push([line[i], line[i + 1]]); }
  return r;
}
function bboxCenter(bb) {
  if (Array.isArray(bb)) return [(bb[0] + bb[2]) / 2, (bb[1] + bb[3]) / 2];
  if (bb) {
    const x0 = bb.x ?? bb.minX, y0 = bb.y ?? bb.minY;
    const w = bb.width ?? (bb.maxX - bb.minX), h = bb.height ?? (bb.maxY - bb.minY);
    return [x0 + w / 2, y0 + h / 2];
  }
  return null;
}

const out = { flags: [], misoriented: [], tableConflicts: [], portUnconfirmed: [], unwired: 0, ok: 0, notes: [] };

// wire-endpoint index
const wires = await eda.sch_PrimitiveWire.getAll();
const ep = {};
for (const w of wires) {
  const P = pts(w.getState_Line ? w.getState_Line() : null);
  for (let i = 0; i + 1 < P.length; i++) {
    const a = P[i], b = P[i + 1];
    if (a[0] === b[0] && a[1] === b[1]) continue;
    (ep[a[0] + ',' + a[1]] = ep[a[0] + ',' + a[1]] || []).push(b);
    (ep[b[0] + ',' + b[1]] = ep[b[0] + ',' + b[1]] || []).push(a);
  }
}

const comps = await eda.sch_PrimitiveComponent.getAll();
for (const c of comps) {
  const t = c.getState_ComponentType();
  if (t !== 'netflag' && t !== 'netport') continue;
  const pid = c.getState_PrimitiveId();
  const x = c.getState_X(), y = c.getState_Y(), rot = c.getState_Rotation(), net = c.getState_Net();
  const fam = family(t, net);
  let body = null;
  try {
    const ctr = bboxCenter(await eda.sch_Primitive.getPrimitivesBBox([pid]));
    body = ctr ? dir([x, y], ctr) : null;
  } catch (e) { out.notes.push(`bbox ${net}: ${e.message}`); }
  const nb = ep[x + ',' + y];
  const wireDir = nb && nb.length ? dir([x, y], nb[0]) : null;
  const expectBody = wireDir ? opp[wireDir] : null;
  const oriented = body !== null && expectBody !== null && body === expectBody;
  const wantRot = body ? tableRot(fam, body) : null;
  const rec = { net, family: fam, rot, body, wireDir, expectBody, oriented, wantRot };
  out.flags.push(rec);
  if (wireDir === null) out.unwired++;
  else if (!oriented) out.misoriented.push(rec);              // board issue, not table issue
  else if (rot !== wantRot) {
    // The bbox-center method is validated for power/ground (label-box symbols,
    // confirmed on ceshi: 10/10). For net_port (an ARROW symbol) the bbox center
    // may sit on the opposite side, so a port "conflict" is UNCONFIRMED — it can
    // mean either the table's port row is wrong OR the bbox misreads ports. Needs
    // a visual to resolve; don't treat it as a hard rule-bug.
    if (fam === 'port') out.portUnconfirmed.push(rec);
    else out.tableConflicts.push(rec);                       // power/ground: hard RULE BUG signal
  }
  else out.ok++;
}

if (out.tableConflicts.length) {
  out.summary = `FAIL — ${out.tableConflicts.length} power/ground flag(s) disagree with the table `
    + `(hard signal — the table or connect_pin is wrong); ${out.portUnconfirmed.length} net_port unconfirmed`;
} else if (out.portUnconfirmed.length) {
  out.summary = `WARN — ${out.ok} power/ground flags agree with the table, but ${out.portUnconfirmed.length} `
    + `net_port(s) disagree. UNCONFIRMED: the bbox method isn't validated for arrow-shaped port symbols. `
    + `Resolve with a visual before changing the port row of orientation.json / connect_pin.`;
} else {
  out.summary = `PASS — ${out.ok} correctly-oriented flags all agree with orientation.json `
    + `(${out.misoriented.length} mis-oriented board flags, ${out.unwired} unwired, ignored)`;
}
return out;
