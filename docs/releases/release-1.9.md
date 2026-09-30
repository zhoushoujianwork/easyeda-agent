# v1.9.0 reference pictures and CLI improvements

Published on 2026-10-01: [GitHub v1.9.0](https://github.com/zhoushoujianwork/easyeda-agent/releases/tag/v1.9.0).
The annotated tag freezes commit `feadb52bae1721acec52fbaf6de123e239bd4e08`.
Independent limited-scope review, release-build and offline release-smoke passed.
All 11 remote assets matched local names, sizes and SHA-256; the Release is non-draft.
SkillHub workflow [36755633129](https://github.com/zhoushoujianwork/easyeda-agent/actions/runs/36755633129)
submitted skillId 173258 successfully; platform review status was not returned.
ClawHub confirmed `easyeda-agent@1.9.0` publication (k97a2evwrtw4t6zxkm376s83kn8fd13n).
The JLC extension marketplace still requires manual submission and is not claimed updated.

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
