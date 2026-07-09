#!/usr/bin/env python3
"""Standard circuit-block library tool (电路块库 ls / show / validate).

The circuit-block library (`references/blocks/*.json`, one block per file) is the
community-built, credited set of KNOWN-GOOD peripheral subcircuits (CH340 USB-serial,
ESP32 auto-download, ESP32-S3 module, buttons, USB-hub, buck …). Their INTERNAL
topology is fixed and copy-verbatim; only the boundary nets (ports) get rebound to
the host design, and pins are referenced by FUNCTIONAL NAME so reuse needs zero
pin-renumbering. This tool globs the dir and assembles them (the single loader seam).

This tool is the local, network-free companion to the JSON (like parts-select.py
is for standard-parts.json). It does NOT touch EasyEDA — schematic instantiation is
the phase-2 write path `easyeda sch block apply` (typed action). Here we only:

    blocks.py ls [--category X] [--validated|--draft]   # list blocks
    blocks.py show <block.id>                            # full block detail
    blocks.py validate [--strict]                        # lint JSON vs schema + 贡献标准

`validate` is the contribution gate a PR must pass (see
references/standard-blocks-contributing.md). --strict also fails on unvalidated
(draft) blocks, for a release-time "everything is proven" check.

Exit codes: 0 ok, 1 validation errors, 2 usage error.
"""
import argparse
import glob
import json
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
REF = os.path.normpath(os.path.join(HERE, "..", "references"))
BLOCKS_DIR = os.path.join(REF, "blocks")           # one block per file
SCHEMA_JSON = os.path.join(BLOCKS_DIR, "_schema.json")
PARTS_JSON = os.path.join(REF, "standard-parts.json")

CATEGORIES = {"usb-serial", "power", "mcu", "mcu-support", "button", "protection", "rf", "comms"}
SEVERITIES = {"must", "should"}
BLOCK_SECTIONS = ("parts", "internal_nets", "ports", "schematic_notes", "pcb_layout")


def load(path):
    with open(path, encoding="utf-8") as f:
        return json.load(f)


def die(msg, code=2):
    print(f"error: {msg}", file=sys.stderr)
    sys.exit(code)


def load_blocks():
    """Assemble the library from references/blocks/*.json — one block per file.

    This is the single loader seam: consumers never see that the library is many
    files. Files whose basename starts with '_' (e.g. _schema.json) are metadata,
    not blocks. Each block file's `id` is authoritative; the basename should equal
    `id` minus the `block.` prefix (validate enforces this). Returns a dict shaped
    like the old aggregate — {"blocks": {id: obj}} — plus `_paths` (id→file) so
    validate can check the filename↔id contract.
    """
    if not os.path.isdir(BLOCKS_DIR):
        die(f"not found: {BLOCKS_DIR}")
    blocks, paths = {}, {}
    for path in sorted(glob.glob(os.path.join(BLOCKS_DIR, "*.json"))):
        base = os.path.basename(path)
        if base.startswith("_"):
            continue  # _schema.json and friends are metadata, not blocks
        try:
            b = load(path)
        except json.JSONDecodeError as e:
            die(f"{base} is not valid JSON: {e}", 1)
        bid = b.get("id")
        if not bid:
            die(f"{base} has no 'id' field", 1)
        if bid in blocks:
            die(f"duplicate block id '{bid}' ({base} and {os.path.basename(paths[bid])})", 1)
        blocks[bid] = b
        paths[bid] = path
    return {"blocks": blocks, "_paths": paths}


def part_keys():
    """Role keys available in standard-parts.json (for cross-ref validation)."""
    if not os.path.exists(PARTS_JSON):
        return None  # can't cross-check
    try:
        return set(load(PARTS_JSON).get("parts", {}).keys())
    except json.JSONDecodeError:
        return None


def is_pending(nets):
    """internal_nets is the 'pending' sentinel (optionally with an inline note)."""
    return isinstance(nets, str) and nets.strip().lower().startswith("pending")


def is_draft(b):
    return not b.get("validated") or is_pending(b.get("internal_nets"))


# ── ls ─────────────────────────────────────────────────────────────────────
def cmd_ls(args):
    doc = load_blocks()
    blocks = doc.get("blocks", {})
    rows = []
    for bid, b in blocks.items():
        if args.category and b.get("category") != args.category:
            continue
        draft = is_draft(b)
        if args.validated and draft:
            continue
        if args.draft and not draft:
            continue
        rows.append((bid, b, draft))
    if args.json:
        print(json.dumps([{"id": r[0], **r[1], "draft": r[2]} for r in rows],
                         ensure_ascii=False, indent=2))
        return 0
    if not rows:
        print("(no matching blocks)")
        return 0
    width = max(len(r[0]) for r in rows)
    print(f"{'BLOCK':<{width}}  {'STATUS':<8} {'CATEGORY':<12} AUTHOR    DESC")
    for bid, b, draft in sorted(rows):
        status = "draft" if draft else "✓ ready"
        author = b.get("author") or "-"
        print(f"{bid:<{width}}  {status:<8} {b.get('category',''):<12} "
              f"{author:<9} {b.get('desc','')}")
    print(f"\n{len(rows)} block(s). `blocks.py show <id>` for detail.")
    return 0


# ── show ───────────────────────────────────────────────────────────────────
def cmd_show(args):
    doc = load_blocks()
    b = doc.get("blocks", {}).get(args.id)
    if b is None:
        ids = ", ".join(sorted(doc.get("blocks", {}).keys()))
        die(f"unknown block '{args.id}'. known: {ids}")
    if args.json:
        print(json.dumps(b, ensure_ascii=False, indent=2))
        return 0
    print(f"# {args.id}\n{b.get('desc','')}\n")
    meta = [("category", b.get("category")), ("author", b.get("author")),
            ("contributors", ", ".join(b.get("contributors") or []) or "-"),
            ("added", b.get("added")), ("updated", b.get("updated")),
            ("source", b.get("source")),
            ("validated", b.get("validated") or "✗ NOT YET (draft)")]
    for k, v in meta:
        print(f"  {k:<13}: {v}")
    print("\n## parts (role → standard-parts key)")
    for role, p in b.get("parts", {}).items():
        alt = f"  alt={p['alt']}" if p.get("alt") else ""
        ov = f"  ={p['value_override']}" if p.get("value_override") else ""
        print(f"  {role:<8} {p.get('part',''):<22}{ov}{alt}")
        if p.get("note"):
            print(f"           ↳ {p['note']}")
    nets = b.get("internal_nets")
    print("\n## internal_nets")
    if is_pending(nets):
        print(f"  {nets}")
    else:
        for net in nets:
            print("  " + " = ".join(net))
    print("\n## ports (rebind to host)")
    for name, port in b.get("ports", {}).items():
        print(f"  {name:<10} {port.get('dir',''):<6} at {port.get('at',''):<14} "
              f"→ {port.get('default_net','')}  ({port.get('desc','')})")
    if b.get("schematic_notes"):
        print("\n## schematic_notes (原理图链接注意)")
        for n in b["schematic_notes"]:
            print(f"  • {n}")
    if b.get("pcb_layout"):
        print("\n## pcb_layout (PCB 电气特性)")
        for r in b["pcb_layout"]:
            val = f" [{r['value']}]" if r.get("value") else ""
            print(f"  ({r.get('severity','?')}) {r.get('rule','')}: "
                  f"{r.get('constraint','')}{val}  @{r.get('target','')}")
    return 0


# ── validate ───────────────────────────────────────────────────────────────
def cmd_validate(args):
    doc = load_blocks()  # exits 1 on bad JSON
    errs, warns = [], []
    pkeys = part_keys()
    blocks = doc.get("blocks", {})
    paths = doc.get("_paths", {})
    if not blocks:
        errs.append("no blocks defined")
    if not os.path.exists(SCHEMA_JSON):
        errs.append(f"missing {os.path.basename(SCHEMA_JSON)} (shared _doc/_schema/libraryUuid)")

    for bid, b in blocks.items():
        def e(m): errs.append(f"[{bid}] {m}")
        def w(m): warns.append(f"[{bid}] {m}")

        if not bid.startswith("block."):
            e("id must start with 'block.'")
        # filename↔id contract: <id minus block.>.json
        expect_base = bid[len("block."):] + ".json" if bid.startswith("block.") else None
        actual_base = os.path.basename(paths.get(bid, ""))
        if expect_base and actual_base and actual_base != expect_base:
            e(f"filename '{actual_base}' should be '{expect_base}' (id minus block.)")
        for f in ("desc", "category", "source"):
            if not b.get(f):
                e(f"missing required field '{f}'")
        if b.get("category") and b["category"] not in CATEGORIES:
            e(f"category '{b['category']}' not in {sorted(CATEGORIES)}")

        draft = is_draft(b)
        # attribution: required once a block is validated (non-draft)
        if not draft:
            for f in ("author", "added", "updated"):
                if not b.get(f):
                    e(f"validated block missing attribution field '{f}'")

        # parts → standard-parts.json cross-ref
        for role, p in b.get("parts", {}).items():
            if not isinstance(p, dict) or not p.get("part"):
                e(f"part role '{role}' missing 'part' key")
                continue
            refs = [p["part"]] + list(p.get("alt") or [])
            if pkeys is not None:
                for k in refs:
                    if k not in pkeys:
                        e(f"part role '{role}' references '{k}' "
                          f"not in standard-parts.json")

        # internal_nets: pin refs must use ROLE.pin where ROLE is a declared part
        nets = b.get("internal_nets")
        if is_pending(nets):
            if b.get("validated"):
                e("internal_nets is 'pending' but block is marked validated")
        elif isinstance(nets, list):
            roles = set(b.get("parts", {}).keys())
            for net in nets:
                for ref in net:
                    if ref.startswith("PORT:"):
                        continue
                    role = ref.split(".", 1)[0]
                    if role not in roles:
                        w(f"internal_nets ref '{ref}' — role '{role}' "
                          f"not in parts (typo? or external anchor)")
        else:
            e("internal_nets must be a list or the string 'pending'")

        # ports
        for name, port in b.get("ports", {}).items():
            if port.get("dir") not in ("in", "out", "bidir"):
                e(f"port '{name}' dir must be in/out/bidir")
            if not port.get("at"):
                e(f"port '{name}' missing 'at'")

        # pcb_layout rules must be structured with severity
        for i, r in enumerate(b.get("pcb_layout") or []):
            if not isinstance(r, dict):
                e(f"pcb_layout[{i}] must be an object")
                continue
            if r.get("severity") not in SEVERITIES:
                e(f"pcb_layout[{i}] severity must be must/should")
            for f in ("rule", "target", "constraint"):
                if not r.get(f):
                    e(f"pcb_layout[{i}] missing '{f}'")

        if draft and args.strict:
            e("draft block (validated:null / internal_nets:pending) — "
              "--strict requires all blocks proven")

    for m in warns:
        print(f"WARN  {m}")
    for m in errs:
        print(f"ERROR {m}", file=sys.stderr)
    n = len(blocks)
    ready = sum(1 for b in blocks.values() if not is_draft(b))
    print(f"\n{n} block(s): {ready} ready, {n - ready} draft; "
          f"{len(errs)} error(s), {len(warns)} warn(s).")
    return 1 if errs else 0


def main():
    ap = argparse.ArgumentParser(description="Standard circuit-block library tool")
    sub = ap.add_subparsers(dest="cmd", required=True)

    p_ls = sub.add_parser("ls", help="list blocks")
    p_ls.add_argument("--category", choices=sorted(CATEGORIES))
    p_ls.add_argument("--validated", action="store_true", help="only ready blocks")
    p_ls.add_argument("--draft", action="store_true", help="only draft blocks")
    p_ls.add_argument("--json", action="store_true")
    p_ls.set_defaults(func=cmd_ls)

    p_show = sub.add_parser("show", help="show one block in full")
    p_show.add_argument("id")
    p_show.add_argument("--json", action="store_true")
    p_show.set_defaults(func=cmd_show)

    p_val = sub.add_parser("validate", help="lint JSON vs schema + contribution rules")
    p_val.add_argument("--strict", action="store_true",
                       help="also fail on draft (unvalidated) blocks")
    p_val.set_defaults(func=cmd_validate)

    args = ap.parse_args()
    sys.exit(args.func(args))


if __name__ == "__main__":
    main()
