#!/usr/bin/env python3
"""Diff-aware schematic lint: compare a fresh layout against a saved baseline.

Report ONLY what the edit introduced (NEW), confirm what it fixed (FIXED), and
collapse untouched pre-existing problems — so "没动过的地方" stays out of your way.

CORRECTNESS NOTE: connectivity is global — a wire added in one corner can merge
two nets across the page. So we never skip rules by region. We run ALL rules on
the FULL board (lint.py is ~milliseconds even on a 53-part board) and diff the
OUTPUT. The speed-up is for the human/agent (only look at NEW + changed
regions), not for the linter's CPU. Primitive identity is the EasyEDA PrimitiveId
(pid), stable across in-place moves/rotations; delete+recreate reads as
removed+added, which is the honest description.

    diff.py <baseline.json> <current.json> [--all] [--json]

      --all   also list the PRE-EXISTING (unchanged) findings, don't fold them
      --json  emit a structured diff instead of the text report
"""
import json
import os
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
LINT = os.path.join(HERE, 'lint.py')

# Fields that define whether a primitive "changed" (geometry/identity/topology).
FIELDS = {
    'part': ['x', 'y', 'rotation', 'mirror', 'designator', 'net'],
    'flag': ['x', 'y', 'rotation', 'net', 'type'],
    'wire': ['line', 'net'],
}
REASON = {'x': 'moved', 'y': 'moved', 'rotation': 'rotated', 'mirror': 'mirrored',
          'designator': 'redesignated', 'net': 'renamed-net', 'line': 'rewired'}


def load(path):
    with open(path) as f:
        return json.load(f)


def index_primitives(layout):
    """pid -> (kind, record). Sheets are ignored; they carry no connectivity."""
    out = {}
    for p in layout.get('parts', []):
        if p.get('type') == 'part' and 'pid' in p:
            out[p['pid']] = ('part', p)
    for f in layout.get('flags', []):
        if 'pid' in f:
            out[f['pid']] = ('flag', f)
    for w in layout.get('wires', []):
        if 'pid' in w:
            out[w['pid']] = ('wire', w)
    return out


def describe(kind, rec):
    if kind == 'part':
        return f"part {rec.get('designator', '?')} @({rec.get('x')},{rec.get('y')})"
    if kind == 'flag':
        return f"{rec.get('type', 'flag')} '{rec.get('net')}' @({rec.get('x')},{rec.get('y')})"
    return f"wire {rec.get('line')}"


def primitive_diff(base, cur):
    bi, ci = index_primitives(base), index_primitives(cur)
    added = [(pid, *ci[pid]) for pid in ci if pid not in bi]
    removed = [(pid, *bi[pid]) for pid in bi if pid not in ci]
    modified = []
    for pid, (kind, crec) in ci.items():
        if pid not in bi:
            continue
        bkind, brec = bi[pid]
        if kind != bkind:
            modified.append((pid, kind, crec, ['retyped']))
            continue
        reasons = []
        for fld in FIELDS[kind]:
            if brec.get(fld) != crec.get(fld):
                r = REASON.get(fld, fld)
                if r not in reasons:
                    reasons.append(r)
        if reasons:
            modified.append((pid, kind, crec, reasons))
    return added, removed, modified


def findings_of(path):
    # utf-8 固定编码:Windows 中文环境 text=True 会走 GBK 解码崩溃(issue #133 Bug 4)
    out = subprocess.run([sys.executable, LINT, path, '--json'],
                         capture_output=True, encoding='utf-8', errors='replace')
    if out.returncode != 0:
        raise SystemExit(f"lint failed on {path}:\n{out.stderr}")
    return json.loads(out.stdout)['findings']


def key(f):
    return (f['rule'], f['msg'])


def compute(base_path, cur_path):
    base, cur = load(base_path), load(cur_path)
    added, removed, modified = primitive_diff(base, cur)
    bf, cf = findings_of(base_path), findings_of(cur_path)
    bkeys, ckeys = {key(f) for f in bf}, {key(f) for f in cf}
    new = [f for f in cf if key(f) not in bkeys]
    fixed = [f for f in bf if key(f) not in ckeys]
    pre = [f for f in cf if key(f) in bkeys]
    return {'added': added, 'removed': removed, 'modified': modified,
            'new': new, 'fixed': fixed, 'preexisting': pre}


def to_json(d):
    return {
        'changed': {
            'added': [{'pid': p, 'kind': k, 'desc': describe(k, r)} for (p, k, r) in d['added']],
            'removed': [{'pid': p, 'kind': k, 'desc': describe(k, r)} for (p, k, r) in d['removed']],
            'modified': [{'pid': p, 'kind': k, 'desc': describe(k, r), 'how': why}
                         for (p, k, r, why) in d['modified']],
        },
        'new': d['new'], 'fixed': d['fixed'], 'preexisting': d['preexisting'],
    }


def _list_changes(tag, items, with_how=False):
    LIMIT = 30
    for row in items[:LIMIT]:
        if with_how:
            pid, kind, rec, why = row
            print(f"  {tag} {describe(kind, rec)} {','.join(why)}  [{pid}]")
        else:
            pid, kind, rec = row
            print(f"  {tag} {describe(kind, rec)}  [{pid}]")
    if len(items) > LIMIT:
        print(f"  …(+{len(items) - LIMIT} more)")


def report(d, show_all):
    na, nr, nm = len(d['added']), len(d['removed']), len(d['modified'])
    new, fixed, pre = d['new'], d['fixed'], d['preexisting']

    if new:
        head = f"🔴 本次改动引入 {len(new)} 个新问题——必看"
    elif na or nr or nm:
        head = "✅ 有改动，但未引入新问题"
    else:
        head = "✅ 与基线一致，无改动"
    print(f"== diff vs baseline ==  {head}")
    print(f"变更图元: +{na} added  -{nr} removed  ~{nm} modified")
    _list_changes('+', d['added'])
    _list_changes('-', d['removed'])
    _list_changes('~', d['modified'], with_how=True)

    if new:
        print(f"\n🔴 NEW 本次引入 ({len(new)}):")
        for f in new:
            print(f"  - [{f['label']}] {f['msg']}")
    if fixed:
        print(f"\n✅ FIXED 本次修复 ({len(fixed)}):")
        for f in fixed:
            print(f"  - [{f['label']}] {f['msg']}")
    if pre:
        if show_all:
            print(f"\n🔵 PRE-EXISTING 未变 ({len(pre)}):")
            for f in pre:
                print(f"  - [{f['label']}] {f['msg']}")
        else:
            print(f"\n🔵 PRE-EXISTING 未变 ({len(pre)} 项，--all 展开)")


def main():
    args = [a for a in sys.argv[1:] if not a.startswith('--')]
    if len(args) < 2:
        print("usage: diff.py <baseline.json> <current.json> [--all] [--json]", file=sys.stderr)
        return 2
    d = compute(args[0], args[1])
    if '--json' in sys.argv[1:]:
        print(json.dumps(to_json(d), ensure_ascii=False))
    else:
        report(d, '--all' in sys.argv[1:])
    # Exit 1 when the edit introduced a new problem — handy for CI/scripts.
    return 1 if d['new'] else 0


if __name__ == '__main__':
    sys.exit(main())
