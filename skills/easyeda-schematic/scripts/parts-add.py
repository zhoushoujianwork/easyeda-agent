#!/usr/bin/env python3
"""Append parts resolved by `easyeda lib by-lcsc` / `lib search` into the curated
standard-parts.json cache, so a one-time online lookup becomes a reproducible,
BOM-ready standard part for the next design (the library-first workflow).

This is the write-back half of absorb-item A1 (see docs/ecosystem-survey.md): the
in-EDA lib_Device search/getByLcscIds already returns
{uuid, libraryUuid, lcsc, name, value, footprintName, manufacturerId, …} — exactly
the fields standard-parts.json curates. This tool DOES NOT talk to the daemon; it
consumes the JSON the CLI already prints, so it stays offline + testable:

    easyeda lib by-lcsc --lcsc C6186 | parts-add.py --key ldo.ams1117_3v3
    easyeda lib search --query "100nF 0402" --limit 1 | parts-add.py
    parts-add.py --from result.json --basic --dry-run

It accepts the daemon /action response ({ok, result:{components:[…]}}), a bare
{components:[…]}, or a bare list. Each component becomes a standard-parts entry
{value, mpn, lcsc, manufacturer, deviceUuid, footprint, basic, desc}. Idempotent:
a component already cached (by lcsc OR deviceUuid) is reported and skipped. The
curated file has ONE top-level libraryUuid; a component from a different library
gets a per-part `libraryUuid` override (and a warning) so it still resolves on place.

NOTE: `basic` (JLC basic-vs-extended) is NOT auto-detected — the connector
projection drops the JLC class field — so it is whatever `--basic` was (default
false). Pass `--basic` for JLC basic parts, or fix the entry by hand afterward.
"""
import json
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
DEFAULT_PARTS = os.path.join(
    HERE, '..', '..', 'easyeda-conventions', 'references', 'standard-parts.json')

KNOWN_VALUE_FLAGS = {'--parts', '--from', '--key', '--desc'}
KNOWN_BOOL_FLAGS = {'--basic', '--dry-run', '-h', '--help'}


def arg(name, default=None):
    av = sys.argv[1:]
    return av[av.index(name) + 1] if name in av and av.index(name) + 1 < len(av) else default


def flag(name):
    return name in sys.argv[1:]


def validate_flags():
    """Reject unknown/typo'd flags so e.g. `--dry-rn` can't silently real-write."""
    av = sys.argv[1:]
    i = 0
    while i < len(av):
        t = av[i]
        if t in KNOWN_VALUE_FLAGS:
            i += 2
            continue
        if t in KNOWN_BOOL_FLAGS:
            i += 1
            continue
        kind = 'unknown flag' if t.startswith('-') else 'unexpected argument'
        known = sorted(KNOWN_VALUE_FLAGS | KNOWN_BOOL_FLAGS)
        print(f'parts-add.py: {kind} {t!r} (known: {known})', file=sys.stderr)
        return False
    return True


def slug(s):
    s = re.sub(r'[^a-z0-9]+', '_', (s or '').lower()).strip('_')
    return s or 'part'


def extract_components(data):
    """Pull the components list out of any of the accepted input shapes."""
    if isinstance(data, list):
        return data
    if isinstance(data, dict):
        if isinstance(data.get('components'), list):
            return data['components']
        res = data.get('result')
        if isinstance(res, dict) and isinstance(res.get('components'), list):
            return res['components']
    return []


def to_entry(c, basic, desc_override):
    """Map a lib search/by-lcsc component record → a standard-parts entry."""
    return {
        'value': c.get('value') or c.get('name'),
        'mpn': c.get('manufacturerId') or c.get('mpn') or c.get('name'),
        'lcsc': c.get('lcsc') or c.get('supplierId'),
        'manufacturer': c.get('manufacturer'),
        'deviceUuid': c.get('uuid') or c.get('deviceUuid'),
        'footprint': c.get('footprintName') or c.get('footprint'),
        'basic': bool(basic),
        'desc': desc_override or (c.get('description') or ''),
    }


def main():
    if flag('-h') or flag('--help'):
        print(__doc__)
        return 0
    if not validate_flags():
        return 2

    parts_path = arg('--parts', DEFAULT_PARTS)
    src = arg('--from')
    raw = open(src, encoding='utf-8').read() if src else sys.stdin.read()
    if not raw.strip():
        print('parts-add.py: no input (pipe `easyeda lib by-lcsc …` or use --from)', file=sys.stderr)
        return 2
    try:
        data = json.loads(raw)
    except json.JSONDecodeError as e:
        print(f'parts-add.py: input is not valid JSON: {e}', file=sys.stderr)
        return 2

    components = extract_components(data)
    if not components:
        print('parts-add.py: no components found in input', file=sys.stderr)
        return 1

    spec = json.load(open(parts_path, encoding='utf-8'))
    parts = spec.setdefault('parts', {})
    top_lib = spec.get('libraryUuid')
    have_lcsc = {p.get('lcsc') for p in parts.values() if p.get('lcsc')}
    have_uuid = {p.get('deviceUuid') for p in parts.values() if p.get('deviceUuid')}

    requested_key = arg('--key')
    basic = flag('--basic')
    desc_override = arg('--desc')
    dry = flag('--dry-run')

    if requested_key and len(components) > 1:
        print('parts-add.py: --key is only valid with a single component', file=sys.stderr)
        return 2

    added, skipped, warnings = [], [], []
    for c in components:
        entry = to_entry(c, basic, desc_override)
        if not entry['deviceUuid']:
            warnings.append(f"skip (no deviceUuid): {entry.get('mpn')}")
            continue
        # Dedupe by lcsc OR deviceUuid — the latter is always present and is the
        # real placement key, so lcsc-less parts still de-duplicate on re-run.
        if entry['lcsc'] and entry['lcsc'] in have_lcsc:
            skipped.append(entry['lcsc'])
            continue
        if entry['deviceUuid'] in have_uuid:
            skipped.append(entry['lcsc'] or entry['deviceUuid'])
            continue

        # The curated file assumes ONE library; preserve cross-library parts with a
        # per-entry override so { libraryUuid, deviceUuid } still resolves on place.
        clib = c.get('libraryUuid')
        if clib and top_lib and clib != top_lib:
            entry['libraryUuid'] = clib
            warnings.append(f"{entry['lcsc']}: libraryUuid {clib} != top-level {top_lib} (stored per-part)")
        elif clib and not top_lib:
            entry['libraryUuid'] = clib

        key = requested_key or slug(entry.get('mpn') or entry.get('value'))
        base, n = key, 2
        while key in parts:
            key, n = f'{base}_{n}', n + 1
        parts[key] = entry
        if entry['lcsc']:
            have_lcsc.add(entry['lcsc'])
        have_uuid.add(entry['deviceUuid'])
        added.append((key, entry['lcsc'], entry['value']))

    for w in warnings:
        print(f'  warn: {w}', file=sys.stderr)
    if skipped:
        print(f'  already cached (skipped): {sorted(skipped)}', file=sys.stderr)

    if not added:
        print('parts-add.py: nothing new to add', file=sys.stderr)
        return 0

    if dry:
        print('would add (dry-run):')
        for key, lcsc, value in added:
            print(f'  {key}: {value} [{lcsc}]')
        return 0

    with open(parts_path, 'w', encoding='utf-8') as f:
        json.dump(spec, f, indent='\t', ensure_ascii=False)
        f.write('\n')
    print(f'added {len(added)} part(s) to {os.path.relpath(parts_path, os.getcwd())}:')
    for key, lcsc, value in added:
        print(f'  {key}: {value} [{lcsc}]')
    return 0


if __name__ == '__main__':
    sys.exit(main())
