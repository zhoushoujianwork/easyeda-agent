# v1.9.0 limited CLI test cases

[Baseline](baseline.md) · [Report](test-report.md)

M1–R1 name bounded image/CLI tests under the user's approved release scope. They are not
the full electrical-design cases from another release. Historical supporting composition
and schematic-image records keep their original versions and coverage boundaries.

| ID | Input and operation | Passing criterion |
|---|---|---|
| M1 | Decode self-generated transparent PNG/JPEG/SVG and freeze source dimensions and typed created-image states | Known source aspect and physical target dimensions; actual DOCUMENT layer, IDs and bbox available |
| F1 | Parameterized create at width 600 mil, Chinese/space paths; local dry-run and typed action routing regressions | PNG/SVG 600x360, JPEG 600x400; one image per create; command/payload correspondence retained |
| F2 | Save all three pictures, close/reopen PCB, fresh list and typed viewport PNG | All three source artworks visibly render; IDs, layers and geometry persist; PNG transparency remains visible |
| E1 | Compare original board data before, with persisted images and after cleanup | Original component/pad/net geometry, outline, silk, rules and complete copper inventories are unchanged; no design/electrical certification inferred |
| L1 | Move/resize PNG to (3400,2500), 500x300 mil; rotate/mirror; exercise 0/90 degree mirror combinations | Requested state sticks; independent JPEG/SVG states unchanged; mirror anchor matches host bbox and corrected dry-run |
| N1 | Offline negative CLI/handler tests: missing/invalid sizes, invalid encoding/format, forbidden layer; capture dispatch attempts | Rejected before writes; partial/missing verification fails CLI and retains ID. This is offline refusal evidence, not a live rejected-write test |
| R1 | Delete only returned IDs, check siblings after first delete, then save/close/reopen/list/dump/render | Zero embedded pictures after reopen; unchanged original board hash; cleanup is proven without claiming controlled-crash recovery |
| E2E | Full requirement-to-finished-board S0–S6/P0–P10 and manufacturing bitmap silk | Deferred by user; not-run for this release, never counted as passing |

Use `pcb image create/list/modify/delete`, `pcb save`, `doc reload`, `pcb dump --include-copper`
and `pcb snapshot --fit-mode all` with explicit project/doc/window routing. Frozen raw
responses and source files remain in ignored local artifacts; the mirror bbox regression
is public at internal/app/testdata/pcb-reference-image-bboxes.json.
