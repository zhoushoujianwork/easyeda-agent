package app

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

func crystalPlanFailureText(rep pcbLayoutPlanReport, err error) string {
	parts := []string{fmt.Sprint(err)}
	for _, rejected := range rep.Rejected {
		parts = append(parts, rejected.Reasons...)
	}
	return strings.Join(parts, "\n")
}

func TestCrystalOffsetSearchEscapesObstacleAndRebuildsBundle(t *testing.T) {
	in, snap := crystalGuardFixture()
	in.Modules[0].Search.GapsMil = []float64{40}
	in.Modules[0].Search.RotationDeltasDeg = []float64{0}
	obstacle := lpComp("obstacle", "FIXED", 387, 430, 0, lpBBox(380, 420, 395, 440))
	snap.Components = append(snap.Components, obstacle)
	base, err := planPCBLayoutModule(in, snap, "crystal-guard", 3)
	if err == nil || len(base.Candidates) != 0 {
		t.Fatal("centered baseline unexpectedly passed obstacle")
	}
	in.Modules[0].Search.CrystalOffsets = &pcbCrystalOffsetSearch{MaxXMil: 100, MaxAwayMil: 80, StepMil: 20}
	rep, err := planPCBLayoutModule(in, snap, "crystal-guard", 3)
	if err != nil {
		t.Fatalf("offset search: %s", crystalPlanFailureText(rep, err))
	}
	for _, candidate := range rep.Candidates {
		if len(candidate.Placements) != 3 || candidate.Bundle == nil {
			t.Fatal("incomplete moved module")
		}
		for _, placement := range candidate.Placements {
			if placement.Ref == "FIXED" || placement.Ref == "U6" {
				t.Fatal("moved fixed non-member")
			}
		}
		if len(candidate.Bundle.Regions) != 2 || len(candidate.Bundle.Vias) < 4 {
			t.Fatal("protection was not regenerated")
		}
	}
}

func TestCrystalOffsetSearchRejectsInvalidAndOversizedSearch(t *testing.T) {
	for _, search := range []pcbCrystalOffsetSearch{{MaxXMil: -1, StepMil: 5}, {MaxXMil: 100, StepMil: 0}, {MaxXMil: 1000, MaxAwayMil: 1000, StepMil: .1}} {
		in, _ := crystalGuardFixture()
		in.Modules[0].Search.CrystalOffsets = &search
		if validatePCBLayoutInputNumbers(in.Modules[0]) == nil {
			t.Fatalf("accepted %+v", search)
		}
	}
	in, _ := crystalGuardFixture()
	in.Modules[0].Search.OffsetsMil = []pcbLayoutOffset{{YMil: 1}}
	if validatePCBLayoutInputNumbers(in.Modules[0]) == nil {
		t.Fatal("offset toward owner accepted")
	}
}

func TestCrystalOffsetSearchTranslatesWithMeasuredBoard(t *testing.T) {
	in, snap := crystalGuardFixture()
	mod := in.Modules[0]
	mod.Search.CrystalOffsets = &pcbCrystalOffsetSearch{MaxXMil: 60, MaxAwayMil: 40, StepMil: 20}
	all, members := map[string]boardComp{}, map[string]boardComp{}
	for _, c := range snap.Components {
		all[c.Designator] = c
	}
	for _, m := range mod.Members {
		members[m.Ref] = all[m.Ref]
	}
	a, err := generateCrystalGuardVariants(mod, members, all, snap)
	if err != nil {
		t.Fatal(err)
	}
	for ref, c := range all {
		all[ref] = mustTranslateBoardComp(t, c, 137, -71)
	}
	for _, m := range mod.Members {
		members[m.Ref] = all[m.Ref]
	}
	b, err := generateCrystalGuardVariants(mod, members, all, snap)
	if err != nil || len(a) != len(b) {
		t.Fatalf("translation changed search: %d/%d %v", len(a), len(b), err)
	}
	for i := range a {
		for ref, c := range a[i].comps {
			moved := b[i].comps[ref]
			if math.Abs(moved.X-c.X-137) > 1e-6 || math.Abs(moved.Y-c.Y+71) > 1e-6 {
				t.Fatalf("absolute-coordinate dependency at %s", ref)
			}
		}
	}
}

func crystalGuardFixture() (pcbLayoutPlanInput, *boardSnapshot) {
	u6 := lpComp("u6", "U6", 500, 600, 0, lpBBox(400, 500, 600, 700),
		boardPad{ID: "u6-2", Number: "2", Net: "OSC_IN", Layer: 1, X: 470, Y: 500, W: 10, H: 20},
		boardPad{ID: "u6-3", Number: "3", Net: "OSC_OUT", Layer: 1, X: 530, Y: 500, W: 10, H: 20},
		boardPad{ID: "u6-33", Number: "33", Net: "GND", Layer: 1, X: 500, Y: 600, W: 100, H: 100})
	u6.Locked = true
	x1 := lpComp("x1", "X1", 200, 200, 0, lpBBox(150, 160, 250, 240),
		boardPad{ID: "x1-1", Number: "1", Net: "OSC_IN", Layer: 1, X: 170, Y: 220, W: 30, H: 24},
		boardPad{ID: "x1-2", Number: "2", Net: "GND", Layer: 1, X: 230, Y: 180, W: 30, H: 24},
		boardPad{ID: "x1-3", Number: "3", Net: "OSC_OUT", Layer: 1, X: 230, Y: 220, W: 30, H: 24},
		boardPad{ID: "x1-4", Number: "4", Net: "GND", Layer: 1, X: 170, Y: 180, W: 30, H: 24})
	c21 := lpComp("c21", "C21", 100, 100, 0, lpBBox(80, 90, 120, 110),
		boardPad{ID: "c21-1", Number: "1", Net: "OSC_IN", Layer: 1, X: 110, Y: 100, W: 14, H: 14},
		boardPad{ID: "c21-2", Number: "2", Net: "GND", Layer: 1, X: 90, Y: 100, W: 14, H: 14})
	c20 := lpComp("c20", "C20", 800, 100, 0, lpBBox(780, 90, 820, 110),
		boardPad{ID: "c20-1", Number: "1", Net: "OSC_OUT", Layer: 1, X: 790, Y: 100, W: 14, H: 14},
		boardPad{ID: "c20-2", Number: "2", Net: "GND", Layer: 1, X: 810, Y: 100, W: 14, H: 14})
	for _, comp := range []*boardComp{&u6, &x1, &c20, &c21} {
		for i := range comp.Pads {
			p := &comp.Pads[i]
			p.Shape = []any{"RECT", p.W, p.H, 0.0}
		}
	}
	snap := lpSnapshot(u6, x1, c20, c21)
	snap.Outline = &boardOutline{BBox: layoutBBox{MinX: 0, MinY: 0, MaxX: 1000, MaxY: 900}, Points: [][2]float64{{0, 0}, {1000, 0}, {1000, 900}, {0, 900}}, Source: "polygon", Format: "polyline"}
	snap.Rules = &boardRules{ClearanceMil: 5, ClearanceTrackTrackMil: 5, CopperToEdgeMil: 10, ViaDrillMil: 10, ViaDiameterMil: 20}
	available := true
	snap.Copper = &boardCopperSnapshot{
		Availability:  map[string]string{"routing": "available", "vias": "available", "pours": "available", "poured": "available", "regions": "available", "fills": "available"},
		ArcsAvailable: &available,
		Lines: []any{
			map[string]any{"primitiveId": "old-in", "net": "OSC_IN", "layer": float64(1), "startX": 470.0, "startY": 500.0, "endX": 200.0, "endY": 200.0, "lineWidth": 8.0},
			map[string]any{"primitiveId": "old-out", "net": "OSC_OUT", "layer": float64(1), "startX": 530.0, "startY": 500.0, "endX": 220.0, "endY": 200.0, "lineWidth": 8.0},
		},
		Arcs: []any{}, Vias: []any{}, Pours: []any{}, Poured: []any{}, Regions: []any{}, Fills: []any{},
	}
	semantic, _ := boardSnapshotSemanticSHA256(snap)
	snap.SemanticSHA256 = semantic
	in := pcbLayoutPlanInput{
		SchemaVersion: 2, Units: "mil", CoordinateSemantic: "footprint-anchor", MinGapMil: 5,
		Modules: []pcbLayoutModuleSpec{{
			ID: "crystal-guard", Strategy: "crystal-guard", CopperPolicy: "module-owned", AnchorRef: "X1",
			Members: []pcbLayoutMemberSpec{{Ref: "X1"}, {Ref: "C20"}, {Ref: "C21"}},
			Search:  pcbLayoutSearchSpec{GapsMil: []float64{40, 55}, RotationDeltasDeg: []float64{0, 90}},
			CrystalGuard: &pcbCrystalGuardSpec{
				CrystalRef: "X1", OwnerRef: "U6", OwnerSide: "bottom",
				Ports: []pcbCrystalGuardPort{
					{Net: "OSC_IN", OwnerPad: "2", CrystalPad: "1", CapacitorRef: "C21", CapacitorSignalPad: "1", CapacitorGroundPad: "2", CapacitorSide: "left", CapacitorRotationDeg: 0},
					{Net: "OSC_OUT", OwnerPad: "3", CrystalPad: "3", CapacitorRef: "C20", CapacitorSignalPad: "1", CapacitorGroundPad: "2", CapacitorSide: "right", CapacitorRotationDeg: 0},
				},
				GroundNet: "GND", CrystalGroundPads: []string{"2", "4"}, GroundAnchors: []string{"U6.33"},
				SignalLayer: 1, SignalWidthMil: 8, ComponentGapMil: 15,
				GuardLayer: 1, GuardWidthMil: 8, GuardGapMil: 10, KeepoutMarginMil: 10,
				FencePitchMil: 80, FenceMarginMil: 10, ViaHoleMil: 10, ViaDiameterMil: 20, LocalPourMarginMil: 10,
				ReplacePrimitiveIDs: []string{"old-in", "old-out"},
			},
		}},
	}
	return in, snap
}

func TestCrystalGuardPlansWholeModuleWithoutMovingOwner(t *testing.T) {
	in, snap := crystalGuardFixture()
	rep, err := planPCBLayoutModule(in, snap, "crystal-guard", 3)
	if err != nil {
		t.Fatalf("plan: %v rejected=%+v", err, rep.Rejected)
	}
	if len(rep.Candidates) < 2 {
		t.Fatalf("candidates=%d rejected=%+v", len(rep.Candidates), rep.Rejected)
	}
	for _, candidate := range rep.Candidates {
		if candidate.Bundle == nil || len(candidate.Bundle.Regions) != 2 || len(candidate.Bundle.Pours) != 8 {
			t.Fatalf("incomplete bundle: %+v", candidate.Bundle)
		}
		pourLayers := map[int]int{}
		for _, pour := range candidate.Bundle.Pours {
			pourLayers[pour.Layer]++
			var keepout [][2]float64
			for _, region := range candidate.Bundle.Regions {
				if region.Layer == pour.Layer && containsString(region.RuleTypes, "no-pours") {
					keepout = region.Points
				}
			}
			if !simplePolygonsStrictlyDisjoint(pour.Points, keepout) {
				t.Fatalf("local ring pour %s intersects/touches no-pours on layer %d", pour.ID, pour.Layer)
			}
		}
		if pourLayers[1] != 4 || pourLayers[2] != 4 {
			t.Fatalf("ring pour layers=%v", pourLayers)
		}
		if len(candidate.Placements) != 3 {
			t.Fatalf("placements=%v", candidate.Placements)
		}
		for _, p := range candidate.Placements {
			if p.Ref == "U6" {
				t.Fatal("fixed MCU leaked into movable module")
			}
		}
		bottomEntries, anchorStubs := 0, 0
		for _, route := range candidate.Bundle.GroundRoutes {
			if route.Role == "ground-entry-bottom" && route.Layer == 2 {
				bottomEntries++
			}
			if route.Role == "ground-anchor-stub" && route.Layer == 1 && route.From == "U6.33" {
				anchorStubs++
			}
		}
		if bottomEntries != 2 || anchorStubs != 2 {
			t.Fatalf("ground anchor routes bottom/stub=%d/%d", bottomEntries, anchorStubs)
		}
		if candidate.Bundle.Metrics["signalLengthMil"] <= 0 || candidate.Bundle.Metrics["signalTurns"] < 0 {
			t.Fatalf("metrics=%v", candidate.Bundle.Metrics)
		}
		keepout := pointsBBox(candidate.Bundle.Regions[0].Points)
		strokeMargin := candidate.Bundle.Metrics["sensitiveSignalStrokeMarginMil"]
		if strokeMargin != in.Modules[0].CrystalGuard.SignalWidthMil/2+snap.Rules.ClearanceMil {
			t.Fatalf("sensitive stroke margin=%v, want half width + live clearance", strokeMargin)
		}
		for _, route := range candidate.Bundle.SignalRoutes {
			if route.Role != "signal-main" {
				continue
			}
			required := expandLayoutBBox(pointsBBox(route.Points), strokeMargin+in.Modules[0].CrystalGuard.KeepoutMarginMil)
			if keepout.MinX > required.MinX+netPathGeomEps || keepout.MinY > required.MinY+netPathGeomEps || keepout.MaxX < required.MaxX-netPathGeomEps || keepout.MaxY < required.MaxY-netPathGeomEps {
				t.Fatalf("no-pours bbox %+v does not cover signal-main stroke %+v for %s", keepout, required, route.ID)
			}
		}
		entryWindows := crystalGuardSignalWindows(candidate.Bundle.SignalRoutes, candidate.Bundle.Envelope.MaxY,
			in.Modules[0].CrystalGuard.SignalWidthMil/2+in.Modules[0].CrystalGuard.GuardWidthMil/2+snap.Rules.ClearanceMil)
		if len(entryWindows) != 1 {
			t.Fatalf("guard must retain one merged owner-side signal entry, got %v", entryWindows)
		}
	}
	// A larger owner-to-crystal gap must be recomputed rather than copied from
	// the first candidate's fixed coordinates.
	if len(rep.Candidates) >= 3 {
		a, b := rep.Candidates[0], rep.Candidates[2]
		if math.Abs(a.Placements[0].YMil-b.Placements[0].YMil) <= netPathGeomEps {
			t.Fatalf("different gap variants reused the same module pose: %s / %s", a.Variant, b.Variant)
		}
	}
}

func TestCrystalGuardTracksViasBuildsOutsideThenMovesWithoutPours(t *testing.T) {
	in, snap := crystalGuardFixture()
	in.Modules[0].CrystalGuard.GroundImplementation = "tracks-vias"
	in.Modules[0].CrystalGuard.LocalPourMarginMil = 0
	rep, err := planPCBLayoutModule(in, snap, "crystal-guard", 1)
	if err != nil || len(rep.Candidates) != 1 {
		t.Fatalf("tracks-vias plan failed: %v rejected=%+v", err, rep.Rejected)
	}
	c := rep.Candidates[0]
	if c.Bundle.GroundImplementation != "tracks-vias" || len(c.Bundle.Pours) != 0 || len(c.Bundle.UnreservedPours) != 0 || len(c.Bundle.AffectedBaselinePours) != 0 {
		t.Fatalf("forbidden local pour declaration: %+v", c.Bundle)
	}
	if len(c.Placements) != 3 || len(c.Bundle.SignalRoutes) != 4 || len(c.Bundle.GroundRoutes) == 0 || len(c.Bundle.Regions) != 2 || len(c.Bundle.Vias) < 4 {
		t.Fatalf("offline assembled module is incomplete: %+v", c.Bundle)
	}
	if err := verifyCrystalTracksViasProtection(c, "GND"); err != nil {
		t.Fatal(err)
	}
	for _, step := range c.Apply.Steps {
		if step.Action == "pcb.pour.create" || step.Action == "pcb.pour.rebuild" {
			t.Fatalf("forbidden pour action: %+v", step)
		}
	}
	tampered := c
	b := *c.Bundle
	tampered.Bundle = &b
	b.Pours = []pcbModulePour{{ID: "forbidden", Net: "GND", Layer: 1, Points: rectPoints(layoutBBox{MinX: 1, MinY: 1, MaxX: 2, MaxY: 2})}}
	if err := verifyCrystalTracksViasProtection(tampered, "GND"); err == nil {
		t.Fatal("tracks-vias candidate accepted a local pour")
	}
}

func TestCrystalGuardReplacementSetIsExactAcrossTracksAndArcs(t *testing.T) {
	nets := map[string]bool{"OSC_IN": true, "OSC_OUT": true}
	if got, err := validateCrystalReplacementSet(nil, nets, nil, nil); err != nil || len(got) != 0 {
		t.Fatalf("clean baseline empty replacement set failed: %v %v", got, err)
	}
	if _, err := validateCrystalReplacementSet([]string{"not-live"}, nets, nil, nil); err == nil || !strings.Contains(err.Error(), "extra=[not-live]") {
		t.Fatalf("clean baseline accepted a phantom replacement id: %v", err)
	}

	in, snap := crystalGuardFixture()
	snap.Copper.Arcs = append(snap.Copper.Arcs, map[string]any{
		"primitiveId": "old-in-arc", "net": "OSC_IN", "layer": 1.0,
		"startX": 200.0, "startY": 200.0, "endX": 210.0, "endY": 210.0,
		"lineWidth": 8.0, "arcAngle": 90.0,
	})
	in.Modules[0].CrystalGuard.ReplacePrimitiveIDs = []string{"old-out", "old-in-arc", "old-in"}
	rep, err := planPCBLayoutModule(in, snap, "crystal-guard", 1)
	if err != nil {
		t.Fatalf("exact mixed track/arc replacement failed: %v rejected=%+v", err, rep.Rejected)
	}
	want := []string{"old-in", "old-in-arc", "old-out"}
	if got := rep.Candidates[0].Bundle.ReplacePrimitiveIDs; canonicalJSON(got) != canonicalJSON(want) {
		t.Fatalf("replacement ids=%v want deterministic exact set %v", got, want)
	}
	deleteFound := false
	for _, action := range rep.Candidates[0].Actions {
		if action.Action != "pcb.route.delete" {
			continue
		}
		deleteFound = true
		if _, hasKindGuard := action.Payload["kind"]; hasKindGuard {
			t.Fatalf("mixed track/arc replacement must not carry a track-only kind guard: %+v", action.Payload)
		}
	}
	if !deleteFound {
		t.Fatal("candidate has no exact OSC route-delete action")
	}

	in.Modules[0].CrystalGuard.ReplacePrimitiveIDs = []string{"old-in", "old-out"}
	rep, err = planPCBLayoutModule(in, snap, "crystal-guard", 1)
	if text := crystalPlanFailureText(rep, err); err == nil || !strings.Contains(text, "missing=[old-in-arc]") {
		t.Fatalf("subset replacement did not fail closed: %s", text)
	}

	in.Modules[0].CrystalGuard.ReplacePrimitiveIDs = []string{"old-in", "old-in-arc", "old-out", "not-owned"}
	rep, err = planPCBLayoutModule(in, snap, "crystal-guard", 1)
	if text := crystalPlanFailureText(rep, err); err == nil || !strings.Contains(text, "extra=[not-owned]") {
		t.Fatalf("extra replacement id did not fail closed: %s", text)
	}
}

func TestCrystalGuardCleanBaselineOmitsEmptyUnlockAndDelete(t *testing.T) {
	candidate := pcbLayoutCandidate{
		Actions: []pcbLayoutTypedAction{{ID: "save", Action: "pcb.save"}},
		Apply:   playbook{Steps: []playbookStep{{ID: "save", Action: "pcb.save"}}},
	}
	rebuildCrystalApply(&candidate, &pcbCrystalGuardSpec{GroundNet: "GND"}, &pcbLayoutModuleBundle{})
	for _, action := range candidate.Actions {
		if action.Action == "pcb.track.lock" || action.Action == "pcb.route.delete" {
			t.Fatalf("clean baseline emitted empty mutation: %+v", action)
		}
	}
	if len(candidate.Actions) != 1 || candidate.Actions[0].Action != "pcb.save" {
		t.Fatalf("unexpected clean-baseline action list: %+v", candidate.Actions)
	}
}

func TestCrystalGuardFailsClosedForUnknownCopperAndWrongGround(t *testing.T) {
	in, snap := crystalGuardFixture()
	snap.Copper.Availability["poured"] = "unknown"
	if _, err := planPCBLayoutModule(in, snap, "crystal-guard", 1); err == nil || !strings.Contains(err.Error(), "poured is unknown") {
		t.Fatalf("unknown poured geometry err=%v", err)
	}
	in, snap = crystalGuardFixture()
	in.Modules[0].CrystalGuard.Ports[0].CapacitorGroundPad = "1"
	if _, err := planPCBLayoutModule(in, snap, "crystal-guard", 1); err == nil || !strings.Contains(err.Error(), "capacitor ground") {
		t.Fatalf("wrong ground assignment err=%v", err)
	}
}

func TestCrystalGuardFailsClosedForUnknownPadAndAreaCopper(t *testing.T) {
	in, snap := crystalGuardFixture()
	snap.Components[0].Pads[0].Shape = nil
	rep, err := planPCBLayoutModule(in, snap, "crystal-guard", 1)
	if text := crystalPlanFailureText(rep, err); err == nil || !strings.Contains(text, "geometry is unknown") {
		t.Fatalf("unknown pad geometry failure=%s", text)
	}

	in, snap = crystalGuardFixture()
	snap.Copper.Fills = []any{map[string]any{
		"primitiveId": "fill-unknown", "net": "SIG", "layer": 1.0,
		"geometryAvailable": false, "source": nil,
	}}
	rep, err = planPCBLayoutModule(in, snap, "crystal-guard", 1)
	if text := crystalPlanFailureText(rep, err); err == nil || !strings.Contains(text, "static fill fill-unknown geometry is unknown") {
		t.Fatalf("unknown static fill geometry failure=%s", text)
	}
}

func TestCrystalGuardRejectsExistingArcStaticFillAndPouredCopper(t *testing.T) {
	polygon := []any{0.0, 0.0, "L", 1000.0, 0.0, 1000.0, 900.0, 0.0, 900.0, 0.0, 0.0}
	tests := []struct {
		name string
		add  func(*boardSnapshot)
		want string
	}{
		{
			name: "arc",
			add: func(s *boardSnapshot) {
				s.Copper.Arcs = append(s.Copper.Arcs, map[string]any{
					"primitiveId": "arc-block", "net": "BLOCK", "layer": 1.0,
					"startX": 100.0, "startY": 450.0, "endX": 900.0, "endY": 450.0,
					"lineWidth": 1000.0, "arcAngle": 180.0,
				})
			},
			want: "other-net arc arc-block",
		},
		{
			name: "static-fill",
			add: func(s *boardSnapshot) {
				s.Copper.Fills = append(s.Copper.Fills, map[string]any{
					"primitiveId": "fill-block", "net": "BLOCK", "layer": 1.0,
					"geometryAvailable": true, "source": polygon,
				})
			},
			want: "other-net static fill",
		},
		{
			name: "materialized-pour",
			add: func(s *boardSnapshot) {
				s.Copper.Poured = append(s.Copper.Poured, map[string]any{
					"primitiveId": "poured-block", "net": "BLOCK", "layer": 1.0,
					"fills": []any{map[string]any{"id": "island", "source": polygon}},
				})
			},
			want: "other-net poured copper",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in, snap := crystalGuardFixture()
			tc.add(snap)
			rep, err := planPCBLayoutModule(in, snap, "crystal-guard", 1)
			if text := crystalPlanFailureText(rep, err); err == nil || !strings.Contains(text, tc.want) {
				t.Fatalf("failure=%s, want %q", text, tc.want)
			}
		})
	}
}

func TestCrystalGuardFenceMarginMustExpandOutsideEnvelope(t *testing.T) {
	in, snap := crystalGuardFixture()
	in.Modules[0].CrystalGuard.FenceMarginMil = 0
	if _, err := planPCBLayoutModule(in, snap, "crystal-guard", 1); err == nil || !strings.Contains(err.Error(), "fenceMarginMil") {
		t.Fatalf("zero fence margin err=%v", err)
	}

	in, snap = crystalGuardFixture()
	rep, err := planPCBLayoutModule(in, snap, "crystal-guard", 1)
	if err != nil || len(rep.Candidates) != 1 {
		t.Fatalf("plan err=%v rejected=%+v", err, rep.Rejected)
	}
	bundle := rep.Candidates[0].Bundle
	margin := in.Modules[0].CrystalGuard.FenceMarginMil
	for _, via := range bundle.Vias {
		if via.Role != "fence" {
			continue
		}
		onExpandedEdge := math.Abs(via.X-(bundle.Envelope.MinX-margin)) <= netPathGeomEps ||
			math.Abs(via.X-(bundle.Envelope.MaxX+margin)) <= netPathGeomEps ||
			math.Abs(via.Y-(bundle.Envelope.MinY-margin)) <= netPathGeomEps ||
			math.Abs(via.Y-(bundle.Envelope.MaxY+margin)) <= netPathGeomEps
		if !onExpandedEdge {
			t.Fatalf("fence via (%v,%v) shrank inside envelope %+v with margin %v", via.X, via.Y, bundle.Envelope, margin)
		}
	}
}

func TestCrystalGuardRecomputesExternalRoutesAfterQuarterTurn(t *testing.T) {
	in, snap := crystalGuardFixture()
	in.Modules[0].Search.GapsMil = []float64{40}
	in.Modules[0].Search.RotationDeltasDeg = []float64{0, 90}
	mod := in.Modules[0]
	all := snap.byDesignator()
	members := map[string]boardComp{"X1": all["X1"], "C20": all["C20"], "C21": all["C21"]}
	variants, err := generateCrystalGuardVariants(mod, members, all, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(variants) != 2 {
		t.Fatalf("variants=%d", len(variants))
	}
	firstCrystal, secondCrystal := variants[0].comps["X1"], variants[1].comps["X1"]
	if !sameRotation(firstCrystal.Rotation, 0) || !sameRotation(secondCrystal.Rotation, 90) {
		t.Fatalf("crystal rotations=%v/%v", firstCrystal.Rotation, secondCrystal.Rotation)
	}
	for _, ref := range []string{"C20", "C21"} {
		if !sameRotation(variants[1].comps[ref].Rotation, 90) {
			t.Fatalf("%s did not rotate with the local assembly: %+v", ref, variants[1].comps[ref])
		}
	}
	r0 := crystalMainSignalRoutes(mod.CrystalGuard, all["U6"], firstCrystal, snap.Rules.ClearanceMil)
	r90 := crystalMainSignalRoutes(mod.CrystalGuard, all["U6"], secondCrystal, snap.Rules.ClearanceMil)
	if canonicalJSON(r0) == canonicalJSON(r90) {
		t.Fatalf("external fixed-owner routes were not recomputed after rotation: %v", r0)
	}
	for i, port := range mod.CrystalGuard.Ports {
		ownerPad, _ := findBoardPadExact(all["U6"], port.OwnerPad, "")
		crystalPad, _ := findBoardPadExact(secondCrystal, port.CrystalPad, "")
		if r90[i][0] != ([2]float64{ownerPad.X, ownerPad.Y}) || r90[i][len(r90[i])-1] != ([2]float64{crystalPad.X, crystalPad.Y}) {
			t.Fatalf("rotated route %d endpoints=%v want %v -> %v", i, r90[i], ownerPad, crystalPad)
		}
	}
}
