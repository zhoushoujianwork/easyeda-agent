# 立创/JLC 比对选型 (mall comparison part selection)

Roadmap item ③. Turns "pick a part" from an arbitrary `library.search` first-match
into a **data-driven choice**: the cheapest, in-stock, JLC-**basic**, spec-matching
part — so the BOM is manufacturable without surprise feeder fees or stockouts.

## 选型前先查块

**标准外围(RS-485 / buck / USB 串口 / GNSS / 充电 / microSD…)先 `easyeda blocks search <关键词>`** ——
命中后块的 `parts` map 直接给出 `standard-parts.json` 的 role,**选型这步免做**;只有块里没有、或板级
专有件才走下面的比对选型/排名流程。

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

Tool-side (`skills/easyeda-agent/scripts/parts-select.py`) or daemon-side — **NOT the connector**: the
EasyEDA webview can't make these cross-origin fetches; the daemon/tool can.

## Ranking (`skills/easyeda-agent/scripts/parts-select.py`)

Tuple sort — each tier breaks ties of the one above:

1. **Relevance gate** — normalize value text (`10kohm`/`10kΩ`/`10k` → `10k`,
   `µ`→`u`) and require the candidate to match the query's value, so a cheap basic
   220pF can't win when you asked for 10k. Keep only the top-relevance candidates.
2. **Buildable** — `stockCount ≥ build qty`, so the pick can actually be ordered. A
   basic part with too little stock yields to an in-stock one (marked `!` in the
   table); the build qty makes this stock-aware (10k basic wins at qty 100, yields at
   qty 5000 when its stock can't cover it).
3. **Basic** (`componentLibraryType=base`) — avoids the per-extended feeder fee.
4. **Preferred** flag — quality/availability signal.
5. **Cheapest** unit price at the build qty — final tiebreaker.

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

This makes [`standard-parts.json`](./standard-parts.json) selections justified
by live stock/price/basic data instead of a guess. Validated: 100nF→C1525,
10µF→C440198, AMS1117→C6186 — all matched the curated standard library.

## Known limitations / refinements

- **Basic-search page depth** — *fixed*: the base-filtered query fetches a generous
  page (50), so a wanted basic that JLC ranks low (e.g. 10k C25744) still surfaces and
  the relevance gate picks it. A category filter would harden this further.
- **Spec-attribute matching** — currently value-token overlap; could compare the
  `attributes` array (voltage / tolerance / temperature) against an explicit spec.
- **Caching & rate limits** — cache JLC responses; the APIs are unofficial and may
  change headers/shape.
- **Promote to a typed action** — `schematic.library.select` (daemon-side) so the
  agent gets a ranked pick directly instead of running a tool.
- **LCSC mall breadth** — JLC SMT covers assembly-stocked parts; for non-assembly
  catalog parts, add the LCSC wmsc search path.
