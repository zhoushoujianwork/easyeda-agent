# v1.9.0 limited CLI acceptance baseline

[Release summary](../../release-1.9.md) · [Cases](test-cases.md) · [Report](test-report.md)

On 2026-10-01 the user explicitly approved v1.9.0 and the proposed acceptance scope:
verified image-import functionality and this version's CLI changes; full-design E2E and
bitmap-to-manufacturing-silk conversion remain follow-up work. This is a version-bound
schema 3 decision, not a waiver for future releases.

The current PCB evidence was collected in the user-opened in-app browser on Web EasyEDA
Pro 4.1.60, connector 1.8.2, git-describe CLI/daemon based on 82c69a3 then 7dc1f28.
Multiple connector versions were reported; every engineering operation explicitly selected
the 1.8.2 window, project and PCB. No engineering GUI or debug JS was used.
The v1.9.0 package carries this same image implementation and corrected CLI bbox logic;
version metadata and release documentation change afterward. The final numbered package
is built and checked offline; do not claim the 1.9.0 package itself ran the earlier live calls.

The authorized test PCB starts with 51 components, its existing outline, silk, rules,
copper layers and a fully available copper inventory. The initial CLI observation reported zero pictures, but its raw response was not retained.
The persisted three-item inventory contains only the returned creation IDs; cleanup deletes
only those IDs and proves a fresh empty inventory.
Self-generated transparent 200x120 PNG, 150x100 JPEG and 100x60 SVG files had Chinese/space
filenames. They were placed outside the board as reference pictures, not manufacturing art.
Original design geometry is never supplied as a golden answer for a design E2E test.

Acceptance thresholds: exact layer 13; expected size/aspect, source top-left anchor,
rotation/mirror and fresh bbox; same IDs and geometry after explicit save/close/reopen;
visible color/alpha/artwork in typed viewport captures; exact-ID cleanup persisted;
all original components, outline, silk, rules and copper inventories unchanged.
Board semantic SHA-256 before, with persisted pictures and after persisted cleanup:
`8b20ee73703379f18732fc9c912ef3f8fc6662075b90fe4d6d56b393ef57af9a`.

Schematic image evidence is the previously reported c0ec88d / issue #272 live result.
Prior two-page paperless Compose evidence is retained with its original runtime and
limitations in docs/reviews/2026-09-29-paperless-compose-live.md. These are supporting
historical evidence, not fresh v1.9.0 whole-feature or electrical-design reacceptance.

The release does not certify fabrication silk contours, DFM, full-design E2E, arbitrary
paperless replacement, or the still-failing default solver in #273. Previous page rename,
region/pour width and priority limitations and all prior design failures remain tracked.
