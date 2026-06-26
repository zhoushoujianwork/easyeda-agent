# 立创/JLC 比对选型 (mall comparison part selection)

Roadmap item ③. Turns "pick a part" from an arbitrary `library.search` first-match
into a **data-driven choice**: the cheapest, in-stock, JLC-**basic**, spec-matching
part — so the BOM is manufacturable without surprise feeder fees or stockouts.

## Data sources (live, no API key, browser User-Agent)

| Source | Endpoint | Gives |
|---|---|---|
| **JLCPCB SMT** (primary) | `POST jlcpcb.com/api/overseas-pcb-order/v1/shoppingCart/smtGood/selectSmtComponentList` | `componentLibraryType` (**base**/expand), `stockCount`, tiered `componentPrices`, `preferredComponentFlag`, `attributes`, MPN, LCSC C#, category |
| LCSC wmsc (optional) | `GET wmsc.lcsc.com/ftps/wm/product/detail?productCode=C…` | richer specs/catalog/datasheet |

Both verified reachable. The JLC SMT call gives the buy-side signals; that is the
backbone of 比对选型. **Basic parts only appear when you pass
`componentLibraryType:"base"`** — JLC's default search returns extended parts in the
top page, so the selector queries base + general and merges.

## Where it lives

Tool-side (`skills/easyeda-schematic/scripts/parts-select.py`) or daemon-side — **NOT the connector**: the
EasyEDA webview can't make these cross-origin fetches; the daemon/tool can.

## Ranking (`skills/easyeda-schematic/scripts/parts-select.py`)

1. **Relevance gate** — normalize value text (`10kohm`/`10kΩ`/`10k` → `10k`,
   `µ`→`u`) and require the candidate to match the query's value, so a cheap basic
   220pF can't win when you asked for 10k. Keep only the top-relevance candidates.
2. **Basic** (`componentLibraryType=base`) — dominant bonus (avoids the per-extended
   feeder fee).
3. **Preferred** flag — quality/availability signal.
4. **In stock** ≥ build qty — out-of-stock heavily penalized.
5. **Cheapest** unit price at the build qty — tiebreaker.

```
parts-select.py "100nF 0402 X7R" --qty 100        # → recommends C1525 (BASIC, 22M stock)
parts-select.py "10uF 0805" --json                 # → C440198 ; machine-readable for the agent
```

## Integration — closes the standardization loop

```
need a part ──▶ parts-select (pick optimal LCSC C#)
            ──▶ library.search by that C#/MPN  →  EasyEDA {libraryUuid, deviceUuid}
            ──▶ component.place
            ──▶ add to standard-parts.json (now a DATA-DRIVEN, in-stock, basic choice)
            ──▶ export.bom + bom-enrich  →  orderable BOM with the C#
```

This makes [`standard-parts.json`](../skills/easyeda-schematic/references/standard-parts.json) selections justified
by live stock/price/basic data instead of a guess. Validated: 100nF→C1525,
10µF→C440198, AMS1117→C6186 — all matched the curated standard library.

## Known limitations / refinements

- **Basic-search keyword matching is weak** — e.g. `10kohm 0402` did not surface the
  basic 10k (C25744) in the base-filtered page, so the selector recommended a correct
  but *extended* 10k. Refinements: add a category filter, normalize the value into
  JLC's indexed form, or cross-check `standard-parts.json` for a known basic.
- **Spec-attribute matching** — currently value-token overlap; could compare the
  `attributes` array (voltage / tolerance / temperature) against an explicit spec.
- **Caching & rate limits** — cache JLC responses; the APIs are unofficial and may
  change headers/shape.
- **Promote to a typed action** — `schematic.library.select` (daemon-side) so the
  agent gets a ranked pick directly instead of running a tool.
- **LCSC mall breadth** — JLC SMT covers assembly-stocked parts; for non-assembly
  catalog parts, add the LCSC wmsc search path.
