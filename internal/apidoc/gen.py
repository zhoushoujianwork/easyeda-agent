#!/usr/bin/env python3
"""Generate api-index.json from @jlceda/pro-api-types index.d.ts.

The official type package (Apache-2.0, pinned in extension/node_modules) is the
authoritative `eda.*` API surface. This walks its `declare global { class X_Y {…} }`
blocks and emits one searchable record per method:

    { "ns": "eda.dmt_Schematic", "method": "createSchematic",
      "sig": "createSchematic(boardName?: string): Promise<string | undefined>",
      "summary": "创建原理图", "stability": "beta" }

`easyeda api search/ls` (cmd_api.go) embeds and searches the result. Re-run after
bumping pro-api-types:

    python3 internal/apidoc/gen.py            # writes internal/apidoc/api-index.json
    python3 internal/apidoc/gen.py --dts <path> --out <path>
"""
import json
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
DEFAULT_DTS = os.path.join(
    HERE, '..', '..', 'extension', 'node_modules', '@jlceda', 'pro-api-types', 'index.d.ts')
DEFAULT_OUT = os.path.join(HERE, 'api-index.json')

# A class header. NO trailing `{` requirement: 57 of the 127 classes carry an
# `implements ISCH_PrimitiveAPI` / `extends …` clause, and the old brace-anchored
# regex silently missed every one of them — their methods were then attributed to
# whichever plain `class X {` came before (368 methods lumped into eda.sch_Netlist,
# 449 into eda.pcb_Net; issue #133 Bug 2 chased runtime-undefined names because of
# it). `$` is allowed for the bundler's `X$1` duplicate suffixes.
CLASS_RE = re.compile(r'^\s*class\s+([A-Za-z0-9_$]+)')
# A member declaration: `name(...` (method) — capture the name; the rest may span lines.
METHOD_RE = re.compile(r'^\s*([A-Za-z_][A-Za-z0-9_]*)\s*\(')
STABILITY_RE = re.compile(r'@(alpha|beta|deprecated|internal)\b')
# One line of the runtime surface map (`sch_PrimitiveWire: SCH_PrimitiveWire;`)
# inside the `eda` declaration. The PROPERTY name is the runtime truth — the
# namespace records must use it, and classes absent from this map (data shapes
# like ISCH_PrimitiveWire) are not callable and must not pollute the index.
# The type may be a UNION (`SCH_PrimitiveComponent | SCH_PrimitiveComponent3` —
# one runtime object documented as two overload classes): capture the whole type
# expression and merge every member class's methods under the one property.
EDA_PROP_RE = re.compile(r'^\s*([a-z][A-Za-z0-9_]*):\s*([A-Z][A-Za-z0-9_$|\s]*[A-Za-z0-9_$]);')


def main():
    dts = DEFAULT_DTS
    out = DEFAULT_OUT
    av = sys.argv[1:]
    if '--dts' in av:
        dts = av[av.index('--dts') + 1]
    if '--out' in av:
        out = av[av.index('--out') + 1]

    with open(dts, encoding='utf-8') as f:
        lines = f.readlines()

    # Pass output: method records keyed by the CLASS they were declared in; the
    # runtime property map (collected from `class EDA`) then decides which classes
    # are callable and under what `eda.<prop>` name.
    by_class = {}
    eda_props = []  # (property, ClassName) in declaration order
    cur_cls = None
    # Pending JSDoc state for the next member.
    doc_summary = None
    doc_stability = None
    in_doc = False
    doc_lines = []
    # Reserved words that look like methods but aren't API surface.
    skip = {'constructor', 'if', 'for', 'while', 'switch', 'catch', 'function', 'return'}

    for raw in lines:
        line = raw.rstrip('\n')
        stripped = line.strip()

        # Class / namespace boundary.
        m = CLASS_RE.match(line)
        if m:
            cur_cls = m.group(1)
            doc_summary, doc_stability, in_doc, doc_lines = None, None, False, []
            continue

        # Inside `class EDA` every `prop: ClassName;` line IS the runtime surface.
        if cur_cls == 'EDA':
            pm = EDA_PROP_RE.match(line)
            if pm:
                eda_props.append((pm.group(1), pm.group(2)))
                continue

        # JSDoc block.
        if stripped.startswith('/**'):
            in_doc = True
            doc_lines = []
            if '*/' in stripped:
                in_doc = False
            continue
        if in_doc:
            doc_lines.append(stripped)
            if '*/' in stripped:
                in_doc = False
                # First non-tag content line is the summary.
                summary = None
                joined = ' '.join(doc_lines)
                stab = STABILITY_RE.search(joined)
                doc_stability = stab.group(1) if stab else None
                for dl in doc_lines:
                    t = dl.lstrip('*').strip()
                    if t and not t.startswith('@') and t != '/' and not t.endswith('*/') and t != '*/':
                        summary = t
                        break
                doc_summary = summary
            continue

        # Member declaration inside a class.
        if cur_cls:
            mm = METHOD_RE.match(line)
            if mm and mm.group(1) not in skip:
                method = mm.group(1)
                # Signature: from this line to the first ';' (handle multi-line).
                sig = stripped
                # If the declaration doesn't end here, leave it as the opening — enough
                # for search; full multi-line sigs are rare and noisy.
                sig = re.sub(r'\s+', ' ', sig).rstrip()
                by_class.setdefault(cur_cls, []).append({
                    'method': method,
                    'sig': sig,
                    'summary': doc_summary or '',
                    'stability': doc_stability or '',
                })
            # Consume the pending doc whether or not it matched a method.
            if stripped and not stripped.startswith('*'):
                doc_summary, doc_stability = None, None

    if not eda_props:
        sys.exit('no `class EDA` surface map found — pro-api-types layout changed, fix gen.py')

    # Emit records ONLY for classes reachable from the runtime surface, named by
    # their runtime property (`eda.sch_PrimitiveWire`), so `api search` results are
    # names `debug exec_js` can actually call. De-dup (overloads) by (ns, method, sig).
    seen = set()
    deduped = []
    unmapped_props = []
    for prop, typeexpr in eda_props:
        ns = f'eda.{prop}'
        classes = [c.strip() for c in typeexpr.split('|') if c.strip()]
        matched = False
        for cls in classes:
            members = by_class.get(cls)
            if members is None:
                continue
            matched = True
            for r in members:
                key = (ns, r['method'], r['sig'])
                if key in seen:
                    continue
                seen.add(key)
                deduped.append({'ns': ns, **r})
        if not matched:
            unmapped_props.append(f'{prop}: {typeexpr}')
    if unmapped_props:
        print(f'warn: {len(unmapped_props)} eda propert(ies) reference classes with no parsed body: '
              + ', '.join(unmapped_props), file=sys.stderr)

    namespaces = sorted({r['ns'] for r in deduped})
    payload = {
        'source': '@jlceda/pro-api-types',
        'namespaceCount': len(namespaces),
        'methodCount': len(deduped),
        'records': deduped,
    }
    with open(out, 'w', encoding='utf-8') as f:
        json.dump(payload, f, ensure_ascii=False, indent=0)
        f.write('\n')
    print(f'wrote {out}: {len(namespaces)} namespaces, {len(deduped)} methods')


if __name__ == '__main__':
    main()
