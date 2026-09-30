# v1.9.0 limited CLI acceptance report

[Baseline](baseline.md) · [Cases](test-cases.md) · [Release summary](../../release-1.9.md)

The user approved this bounded release on 2026-10-01. This report does not certify a
complete schematic/PCB design or final-package live reacceptance. Independent read-only review passed this bounded scope.

## 现场回读

All current PCB engineering calls used typed CLI in the user-opened Web 4.1.60 test PCB,
explicitly selecting the current 1.8.2 connector window, project and document. Three source
pictures were placed outside the board. After save → typed close/reopen → fresh image list,
the PNG was at (3400,2500), 500x300 mil, rotation 90, mirror true, bbox
[3100,2500]–[3400,3000]; JPEG was 600x400 at (3400,1800); SVG was 600x360 at (3400,1100).
All three retained their creation IDs and DOCUMENT layer 13. Typed viewport captures
showed source artwork/color, the transparent PNG hole and asymmetrical markers.
These captures are viewport PNGs, not object-level manufacturing exports.

| ID | Result | Concrete evidence and bounded conclusion |
|---|---|---|
| M1 | pass | Source file dimensions and png/jpeg/svg-create.json contain fresh actual layer 13, primitive IDs, physical dimensions and bbox; persisted three-item inventory contains only the returned creation IDs; only these IDs are deleted |
| F1 | pass | png-create.json reports 600x360, jpeg-create.json 600x400, svg-create.json 600x360; all filenames contain Chinese/spaces; TestPcbImageCreateWiresDocumentPayload verifies the typed action and proportional dimensions |
| F2 | pass | reloaded-list.json and final-list.json preserve all three creation IDs and source geometry after typed doc reload; final-snapshot.json references the visibly inspected PNG with SHA-256 listed below |
| E1 | pass | pcb-image-272-before.json, with-images-board.json and clean-board.json match components, outline, silk, layers, rules, copper and routedLines exactly; complete copper availability is known, all three semantic hashes match |
| L1 | pass | png-modify.json/png-final-modify.json and png-r90-mfalse.json/png-r0-mtrue.json capture requested fields and actual bounds; final-list.json preserves siblings; public mirror-bbox fixture drives TestPcbImageBBoxWebFixture |
| N1 | pass | TestPcbImageDryRunAndValidation and connector pcb.image refusal tests reject invalid sizes/formats/layers before writes; mutation verification tests preserve IDs on partial results. Offline scope only, no live-negative claim |
| R1 | pass | png/jpeg/svg-delete.json report verified deletion; after-first-delete.json preserves the other two pictures; clean-list.json is count 0 after save/reopen, clean-board.json matches original semantic hash and clean-snapshot.json shows cleanup |
| E2E | not-run | Full design and manufacturing bitmap contours/DFM are explicitly deferred by the user for v1.9.0; no design completeness, final DRC or manufactured result is claimed |

The before/with/clean board semantic SHA-256 is
`8b20ee73703379f18732fc9c912ef3f8fc6662075b90fe4d6d56b393ef57af9a`.
Raw responses live under ignored `artifacts/pcb-image-272/`, with the before dump at
`artifacts/pcb-image-272-before.json`; private context identities and command logs are not
redistributed. Their integrity hashes are included below for independent local review.

## Failure found and corrected

The initial offline mirrored bbox used a center/source-space reflection. Actual Web
readback at rotation 90 and mirror true extends left from the anchor, not right.
Commit 7dc1f28 mirrors the rotated rectangle about the anchor vertical axis and adds the
captured regression fixture. Corrected dry-run returns minX 3100/maxX 3400 and
minY 2500/maxY 3000, matching the persisted PNG.
An old connector project.current call timed out and a project-only save was rejected as
ambiguous before image writes; subsequent calls explicitly selected the available 1.8.2
window. These diagnostic failures are not counted as passing design operations.

## Supporting historical CLI evidence

Schematic image import was previously reported live-verified in c0ec88d / issue #272,
including PNG/JPEG/SVG, intrinsic size, deletion, no added electrical connectivity,
and save/reload. Its original raw evidence is not re-created by this report.
The prior paperless two-page Compose report records 42/44 components and 486/482 successful
steps with saved/reloaded source matching; its SVG export timeout and untested clear branch
remain limitations. Explicit rows/net-label and protection changes have offline regressions;
no universal live certification is inferred from that one earlier scene.

Validation: full `make test`; connector suite 676 passed, final targeted image suite 7 passed;
connector typecheck/build; `make skill-check`; 122 release-script tests and 13 agent-entry
tests passed. Version synchronization and final release build/smoke are separate packaging
checks, not substitutes for scene evidence. Cost was recorded in the local audit ledger.

## 独立复核

Independent agent release_review passed the user-approved limited CLI/image scope after
read-only examination of create/modify/reload/delete responses, before/with/clean PCB data,
typed captures and hashes, the public bbox fixture and implementation. IDs, layer and
geometry persisted; visible color, alpha and asymmetric orientation matched. The 51
original components and all captured design fields/semantic hash remained unchanged;
exact-ID cleanup persisted with fresh inventory zero. The initial empty-list observation
has no retained raw response and is not independently certified.

N1 covers offline negatives only. Historical schematic and paperless evidence supports
only its originally reported scenes. The reviewer performed no new live operations and
did not certify the exact v1.9.0 package live, full design E2E, general Compose, or
manufacturing silk/DFM. All three document hashes and listed raw JSON/capture hashes were
checked. Final build and remote asset verification remain separate checks.

## Frozen raw evidence hashes

| Local evidence file | SHA-256 |
|---|---|
| png-create.json | `bdacf3b189f3b73d410d8ab7986f6657eeba63180ed96ba62ff336aa75ed42d5` |
| jpeg-create.json | `22bb784a8a2838fbc6f817fa7f52b5d5c07b7d0f54bb57e7b44bddc41ad055ce` |
| svg-create.json | `8415de17ff2b13be4eba0f910fae5bb0dd0957ce5c2dd47013c87b54d51dd8e9` |
| png-final-modify.json | `5a0beb53209ddf0013076df2bbb20c7e8e1271e34335d00e78e6d60c3c8e680b` |
| final-list.json | `505ec799b4c54d4a2a2aa2d579978ce2cdbfc0243bf638ca66e42a92d9eb5da6` |
| with-images-board.json | `926c10ab37d73b12def806bab59862c9785f269d22d052ae245be24b74e43568` |
| after-first-delete.json | `fe8dee87d6dd9d1fff70c0853fdc00b653fe10c4482f6ba9045c1e81f0f13bc6` |
| png-delete.json | `2d7a2ebf393596684f71548c977a9407b615ff8a0560d7f7bcb0a83ab4933ed0` |
| jpeg-delete.json | `e76ac6aca7d256b2ae9cf27093f95661e32c27c671377f1d410088fcf26f9e12` |
| svg-delete.json | `63c0f18a61be8f7f17d2cc4bdde31a8ba17d2cf41d3e71d8ac2a5f332f810098` |
| clean-list.json | `bf944a10ce3103b41e5b7f430663d0933431f8d55b927b5b426164942fac45bd` |
| clean-board.json | `44d9f8879847e9166c9e2dd63357d62bb5230a5749916f68776ba09ac0741d30` |
| final-snapshot.json | `bb2b017ddf31133fbe5daaaf0737402e1f43a0e8a541f080bcc75bc416be01e5` |
| clean-snapshot.json | `7e200ebf7f5fda613cd6a23afdc351ad463d7a99192b1215f4c75b2b103f1af6` |

final-snapshot viewport PNG SHA-256: `25370a6e96f9b3cde43fe12b45a0d062001e10800f9902ee07bafbc0d93eda83`.

clean-snapshot viewport PNG SHA-256: `abdc8d0243e1801af266b11861f356eb59a6ddea7fc137f24f205bdea5f1ca85`.
