#!/usr/bin/env python3
"""Fill the LCSC C-number into an EasyEDA BOM export.

EasyEDA's `create({libraryUuid, uuid})` instantiates a part whose `SupplierId` is
`<MPN>.1`, NOT the device's LCSC C-number, and `setState_SupplierId` does not
persist (the field is device-bound). So the exported BOM's "Supplier Part" column
is not directly orderable. We DO know the real C-numbers — they're in
`tools/standard-parts.json` (and recoverable via `lib_Device.search`). This tool
joins them in: for every BOM row whose Manufacturer Part matches a standard part,
it rewrites "Supplier Part" to the C-number (and fills an empty Value).

    bom-enrich.py <bom.tsv/csv> [--out enriched.tsv] [--parts standard-parts.json]

Reads the EasyEDA BOM (tab-separated, UTF-16/UTF-8), writes UTF-8. Reports the
match rate and any unmatched MPNs (candidates to add to standard-parts.json).
"""
import json
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))


def read_text(path):
    raw = open(path, 'rb').read()
    for enc in ('utf-16', 'utf-16-le', 'utf-8-sig', 'utf-8'):
        try:
            return raw.decode(enc)
        except UnicodeDecodeError:
            continue
    return raw.decode('utf-8', 'replace')


def load_mpn_map(parts_path):
    spec = json.load(open(parts_path))
    out = {}
    for p in spec.get('parts', {}).values():
        mpn = p.get('mpn')
        if mpn:
            out[mpn.strip().lower()] = p
    return out


def main():
    args = [a for a in sys.argv[1:] if not a.startswith('--')]
    if not args:
        print('usage: bom-enrich.py <bom.csv> [--out file] [--parts standard-parts.json]', file=sys.stderr)
        return 2
    bom_path = args[0]
    out_path = None
    # standard-parts.json is canonical data in the easyeda-conventions skill.
    parts_path = os.path.join(
        HERE, '..', '..', 'easyeda-conventions', 'references', 'standard-parts.json')
    av = sys.argv[1:]
    if '--out' in av:
        out_path = av[av.index('--out') + 1]
    if '--parts' in av:
        parts_path = av[av.index('--parts') + 1]

    mpn_map = load_mpn_map(parts_path)
    lines = read_text(bom_path).splitlines()
    if not lines:
        print('empty BOM', file=sys.stderr)
        return 1
    # EasyEDA BOM is tab-separated.
    rows = [ln.split('\t') for ln in lines]
    header = rows[0]
    col = {name: i for i, name in enumerate(header)}
    mpn_i = col.get('Manufacturer Part')
    sup_i = col.get('Supplier Part')
    val_i = col.get('Value')
    if mpn_i is None or sup_i is None:
        print(f"BOM missing expected columns; header = {header}", file=sys.stderr)
        return 1

    matched, total, unmatched = 0, 0, []
    for r in rows[1:]:
        if len(r) <= max(mpn_i, sup_i):
            continue
        total += 1
        mpn = r[mpn_i].strip()
        hit = mpn_map.get(mpn.lower())
        if hit:
            r[sup_i] = hit['lcsc']                     # Supplier Part -> C-number
            if val_i is not None and val_i < len(r) and not r[val_i].strip():
                r[val_i] = hit.get('value', '')        # fill empty Value
            matched += 1
        else:
            unmatched.append(mpn)

    out_path = out_path or (os.path.splitext(bom_path)[0] + '.enriched.tsv')
    with open(out_path, 'w', encoding='utf-8') as f:
        f.write('\n'.join('\t'.join(r) for r in rows) + '\n')

    print(f"enriched {matched}/{total} rows with LCSC C-numbers -> {out_path}")
    if unmatched:
        print(f"unmatched MPNs (add to standard-parts.json): {sorted(set(unmatched))}")
    return 0


if __name__ == '__main__':
    sys.exit(main())
