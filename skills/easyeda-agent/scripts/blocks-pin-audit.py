#!/usr/bin/env python3
"""Audit every circuit block's pin references against REAL symbol pins.

A block references pins by FUNCTION NAME (`CH340.TXD`) so it survives designator
churn — but nothing checked those names against the actual symbols, so blocks
shipped `verified` while silently mis-wiring. One example cost a whole day:
`ch340c_usb_serial` referenced `J_USB.VBUS`, which is ambiguous on a USB-C 16P
(two VBUS pins), so `block-apply` never connected VBUS at all — the USB port was
not powered, and the block was marked verified because it had been validated by
HAND-wiring, which bypasses the block's own pin references entirely.

Two modes:

  --probe    Place every part the library references (needs a live EasyEDA window)
             and read its real pins back, refreshing the pin table snapshot.
             Resumable: re-run after a connection hiccup and it continues.
  (default)  Offline: judge every block pin reference against the snapshot.
             Exits non-zero if any reference is FANOUT or MISSING, so it gates.

Verdicts:
  unique   exactly one pin matches (by name or number) — fine as written
  fanout   several pins share that function name — needs the `*` suffix, which
           bonds them all (a connector's redundant VBUS/GND/shield, a chip's
           doubled power pins, a crystal's two case grounds)
  missing  no pin matches — the name is simply wrong; real candidates are shown
  unknown  that part has no probed pins yet (unmeasured, not a defect)
"""
import argparse, difflib, json, os, subprocess, sys

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.abspath(os.path.join(HERE, '..', '..', '..'))
BLOCKS = os.path.join(REPO, 'internal', 'blocks', 'data')
STDPARTS = os.path.join(HERE, '..', 'references', 'standard-parts.json')
SNAPSHOT = os.path.join(HERE, '..', 'references', 'symbol-pins.json')
FANOUT = '*'


def load_refs():
    """Extract (role, pin, part_key) for every internal_nets member of every block."""
    refs, parts = {}, set()
    for fname in sorted(os.listdir(BLOCKS)):
        if not fname.endswith('.json') or fname.startswith('_'):
            continue
        b = json.load(open(os.path.join(BLOCKS, fname)))
        bid = b.get('id') or fname
        roles = {r: (v.get('part') if isinstance(v, dict) else None)
                 for r, v in (b.get('parts') or {}).items()}
        nets = b.get('internal_nets')
        if not isinstance(nets, list):
            continue  # `"pending"` — topology not settled yet
        items = []
        for net in nets:
            if not isinstance(net, list):
                continue
            for m in net:
                if not isinstance(m, str) or m.startswith('PORT:') or '.' not in m:
                    continue
                role, pin = m.split('.', 1)
                pk = roles.get(role)
                if pk:
                    parts.add(pk)
                items.append((role, pin, pk))
        refs[bid] = items
    return refs, sorted(parts)


def probe(project):
    """Place each referenced part once and read its real pins. Resumable."""
    std = json.load(open(STDPARTS))
    lib, parts = std['libraryUuid'], std['parts']
    _, wanted_all = load_refs()

    table = {}
    if os.path.exists(SNAPSHOT):
        table = json.load(open(SNAPSHOT)).get('parts', {})
        print(f'resuming from snapshot: {len(table)} part(s) already probed')

    skipped = [p for p in wanted_all if p not in parts or not parts[p].get('deviceUuid')]
    if skipped:
        print(f'!! {len(skipped)} part(s) have no deviceUuid in standard-parts.json '
              f'and cannot be probed: {", ".join(skipped)}')
    wanted = [p for p in wanted_all
              if p in parts and parts[p].get('deviceUuid') and p not in table]
    print(f'to probe: {len(wanted)}')

    def run(args, timeout=180):
        return subprocess.run(args, capture_output=True, text=True, timeout=timeout)

    BATCH = 12  # a small page keeps the pin read fast and the clear reliable
    for start in range(0, len(wanted), BATCH):
        chunk = wanted[start:start + BATCH]
        run(['easyeda', 'sch', 'clear', '--project', project])
        run(['easyeda', 'sch', 'save', '--project', project])

        placed = {}
        for i, pk in enumerate(chunk):
            x, y = 200 + (i % 4) * 400, 200 + (i // 4) * 400
            r = run(['easyeda', 'sch', 'place', '--lib', lib,
                     '--uuid', parts[pk]['deviceUuid'],
                     '--x', str(x), '--y', str(y), '--project', project])
            if r.returncode == 0:
                placed[pk] = (x, y)

        # The list call is the fragile step; retry rather than lose the batch.
        data = None
        for _ in range(3):
            r = run(['easyeda', 'sch', 'list', '--project', project, '--include-pins'])
            try:
                d = json.loads(r.stdout)
                if isinstance(d.get('result', {}).get('components'), list):
                    data = d
                    break
            except Exception:
                pass
        if data is None:
            print(f'  batch {start}: list failed after retries — skipped')
            continue

        # Match by the coordinate we placed at: the response's device.uuid is a
        # 16-hex symbol id, NOT the 32-hex deviceUuid we passed, so uuid matching
        # never lines up.
        by_xy = {(round(float(c.get('x', -1))), round(float(c.get('y', -1)))): c
                 for c in data['result']['components'] if c.get('componentType') == 'part'}
        for pk, xy in placed.items():
            c = by_xy.get(xy)
            if c:
                table[pk] = [{'n': p.get('pinNumber'), 'name': p.get('pinName')}
                             for p in (c.get('pins') or [])]
        json.dump({'_doc': 'Real symbol pins, read back from placed parts. Refresh with '
                           'blocks-pin-audit.py --probe (needs a live EasyEDA window).',
                   'parts': table}, open(SNAPSHOT, 'w'), ensure_ascii=False, indent=1)
        print(f'  batch {start}-{start + len(chunk)}: {len(placed)} probed')

    run(['easyeda', 'sch', 'clear', '--project', project])
    run(['easyeda', 'sch', 'save', '--project', project])
    print(f'snapshot: {len(table)} part(s) → {SNAPSHOT}')


def audit():
    if not os.path.exists(SNAPSHOT):
        print(f'no pin snapshot at {SNAPSHOT} — run with --probe first', file=sys.stderr)
        return 2
    table = json.load(open(SNAPSHOT))['parts']
    refs, _ = load_refs()

    def classify(pk, pin):
        pins = table.get(pk)
        if pins is None:
            return 'unknown', []
        starred = pin.endswith(FANOUT)
        want = pin[:-1] if starred else pin
        hits = [p for p in pins if p.get('name') == want or p.get('n') == want]
        if not hits:
            names = sorted({p['name'] for p in pins if p.get('name')})
            return 'missing', difflib.get_close_matches(want, names, n=4, cutoff=0.4) or names[:6]
        if len(hits) > 1:
            return ('unique' if starred else 'fanout'), [h.get('n') for h in hits]
        return 'unique', [hits[0].get('n')]

    stats = {'unique': 0, 'fanout': 0, 'missing': 0, 'unknown': 0}
    bad = []
    for bid, items in sorted(refs.items()):
        for role, pin, pk in items:
            if not pk:
                continue
            verdict, info = classify(pk, pin)
            stats[verdict] += 1
            if verdict in ('fanout', 'missing'):
                bad.append((bid, role, pin, pk, verdict, info))

    print(f"refs judged: {sum(stats.values())}  unique={stats['unique']} "
          f"fanout={stats['fanout']} missing={stats['missing']} unknown={stats['unknown']}")
    cur = None
    for bid, role, pin, pk, verdict, info in bad:
        if bid != cur:
            print(f'\n{bid}')
            cur = bid
        if verdict == 'fanout':
            print(f'  FANOUT   {role}.{pin:<14} → pins {info}   ⇒ write "{role}.{pin}*"')
        else:
            print(f'  MISSING  {role}.{pin:<14} ({pk})   real candidates: {info}')
    if bad:
        print(f'\n{len(bad)} bad reference(s). FANOUT → add the `*` suffix; '
              f'MISSING → use the symbol\'s real pin name.')
    return 1 if bad else 0


if __name__ == '__main__':
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument('--probe', action='store_true',
                    help='refresh the pin snapshot from a live EasyEDA window')
    ap.add_argument('--project', default='ceshi', help='project to probe in (default: ceshi)')
    a = ap.parse_args()
    if a.probe:
        probe(a.project)
        sys.exit(0)
    sys.exit(audit())
