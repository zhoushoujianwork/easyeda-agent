# v1.9.0 reference pictures and CLI improvements

Publication is approved. Independent limited-scope review, release-build and offline
release-smoke passed; remote publication and asset checks are the remaining steps.

This release adds schematic and PCB reference-picture import and management, fixes mirrored
PCB preview bounds, and includes protected paperless composition improvements and updated
PCB Skill guidance. PCB pictures are embedded on DOCUMENT layer 13; manufacturing silk
contours are a separate capability. Users need the new connector for the image actions.

The user's 2026-10-01 approval limits acceptance to the verified images and this version's
CLI changes. PNG/JPEG/SVG persistence and exact cleanup were tested on Web 4.1.60;
full design E2E and bitmap-to-manufacturing-silk remain deferred, and #272 stays open.
Earlier schematic-image/paperless evidence keeps its original runtime and limitations.

[Acceptance report](evidence/v1.9.0/test-report.md) · [Baseline](evidence/v1.9.0/baseline.md) ·
[Test cases](evidence/v1.9.0/test-cases.md). Build and publication follow [the release workflow](../release-workflow.md).
