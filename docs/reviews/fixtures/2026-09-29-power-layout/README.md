# Power schematic layout search reproduction (2026-09-29)

These are offline `easyeda sch lib-layout` inputs extracted from a two-page vehicle/battery power schematic. The project and page UUIDs were replaced with synthetic IDs; measured component bodies, pins, visible designator boxes, connectivity, module ownership and layout policy were preserved. No credentials or user-specific paths are included. These fixtures are for solver debugging, not proof that either circuit is geometrically impossible.

| Input | Contents | SHA-256 |
| --- | --- | --- |
| `input.json` | 20-part TPS54560B-Q1 buck zone | `f34e28e1aa566eefb8f8dcc3f274755e59be5dc4cd03a3ff2685a464fae1d7e1` |
| `boost-input.json` | 13-part TPS61022 battery boost zone | `4d7ff35c7d7f790b90ee74a206fc1229dc26b9067b901179ceb7309a9f95f81a` |

Reproduce without an EDA window, using easyeda-agent v1.8.0:

```sh
easyeda sch lib-layout --from docs/reviews/fixtures/2026-09-29-power-layout/input.json --out /tmp/buck-composition.json
easyeda sch lib-layout --from docs/reviews/fixtures/2026-09-29-power-layout/boost-input.json --out /tmp/boost-composition.json
```

Both inputs set `maxCandidates: 3000`. The buck exits 1 after 3,000 candidates, 20 backtracks and 3 relocation attempts, with `cmp-R6` placement `candidate-budget` as the last conflict. The boost exits 1 after 3,000 candidates and 12 backtracks with `candidate search budget exhausted`. Raising the isolated buck input to 80,000 did not return within three minutes; that run was stopped, so it is a latency observation, not a completed failure. The earlier closed issue #262 covered a different 51-part/10-zone regression.

The source symbols and pins were measured in EasyEDA Pro Web 4.1.60. Component model and pin data are in each JSON. The full source project's BOM and part-lock file remain with that project; this public fixture contains only the two failing zones.
