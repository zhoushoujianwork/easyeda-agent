package app

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func reflowFixture(comps ...boardComp) (*boardSnapshot, pcbReflowSpec) {
	available := true
	return &boardSnapshot{
		Components: comps,
		Outline:    &boardOutline{Source: "polygon", Format: "polyline", BBox: layoutBBox{MinX: 0, MinY: 0, MaxX: 300, MaxY: 300}, Points: [][2]float64{{0, 0}, {300, 0}, {300, 300}, {0, 300}}},
		Rules:      &boardRules{Source: "live", ClearanceMil: 2, ClearanceTrackTrackMil: 2, TrackWidthMinMil: 2, CopperToEdgeMil: 2, ViaDrillMil: 5, ViaDiameterMil: 10},
		Copper:     &boardCopperSnapshot{Availability: map[string]string{"routing": "available", "vias": "available", "regions": "available", "fills": "available", "pours": "available", "poured": "available"}, Lines: []any{}, Arcs: []any{}, ArcsAvailable: &available, Vias: []any{}, Regions: []any{}, Fills: []any{}, Pours: []any{}, Poured: []any{}},
	}, pcbReflowSpec{MaxStates: 500, MaxShiftXMil: 0, MaxShiftYMil: 20, StepMil: 20, MaxCandidates: 1}
}

func reflowPart(ref string, x, y float64) boardComp {
	return boardComp{ID: "id-" + ref, Designator: ref, X: x, Y: y, Layer: 1, BBox: &layoutBBox{MinX: x - 8, MaxX: x + 8, MinY: y - 8, MaxY: y + 8}, Pads: []boardPad{{ID: "pad-" + ref, Number: "1", Net: "net-" + ref, X: x, Y: y, Layer: 1, W: 4, H: 4, Shape: []any{"RECT", 4.0, 4.0, 0.0}}}}
}

func reflowLine(id, net string, x1, y1, x2, y2 float64) any {
	return map[string]any{"primitiveId": id, "net": net, "layer": float64(1), "startX": x1, "startY": y1, "endX": x2, "endY": y2, "lineWidth": 2.0}
}

func TestPCBReflowRecursivelyMovesWholeBlockingGroups(t *testing.T) {
	x, a, cap, b := reflowPart("X", 100, 110), reflowPart("A", 100, 70), reflowPart("C", 130, 70), reflowPart("B", 100, 40)
	snap, spec := reflowFixture(x, a, cap, b)
	spec.Groups = []pcbReflowGroupSpec{{ID: "group-a", AnchorRef: "A", Refs: []string{"A", "C"}}, {ID: "group-b", AnchorRef: "B", Refs: []string{"B"}}}
	target := pcbLayoutVariant{label: "seed", comps: map[string]boardComp{"X": mustTranslateBoardComp(t, x, 0, -30)}}
	before, _ := json.Marshal(snap)
	variants, report, err := resolvePCBReflow(spec, target, snap, 4)
	if err != nil || len(variants) != 1 {
		t.Fatalf("reflow failed: %v %+v", err, report)
	}
	v := variants[0]
	if len(v.comps) != 4 || v.comps["X"].Y != 80 || v.comps["A"].Y != 50 || v.comps["C"].Y != 50 || v.comps["B"].Y != 20 {
		t.Fatalf("not an intact recursive move: %+v", v.comps)
	}
	if len(report.Candidates[0].Moves) != 2 || report.Candidates[0].Moves[1].Reason == "" {
		t.Fatal("missing movement/causal evidence")
	}
	after, _ := json.Marshal(snap)
	if string(before) != string(after) || target.comps["X"].Y != 80 {
		t.Fatal("reflow mutated its inputs")
	}
}

func TestPCBReflowFixedBlockerBacktracksToAnotherPosition(t *testing.T) {
	x, a, wall := reflowPart("X", 100, 110), reflowPart("A", 100, 70), reflowPart("FIXED", 100, 45)
	snap, spec := reflowFixture(x, a, wall)
	spec.MaxShiftXMil = 20
	spec.FixedRefs = []string{"FIXED"}
	spec.Groups = []pcbReflowGroupSpec{{ID: "a", AnchorRef: "A", Refs: []string{"A"}}}
	target := pcbLayoutVariant{label: "seed", comps: map[string]boardComp{"X": mustTranslateBoardComp(t, x, 0, -40)}}
	variants, report, err := resolvePCBReflow(spec, target, snap, 4)
	if err != nil || len(variants) != 1 {
		t.Fatalf("fallback failed: %v %+v", err, report)
	}
	if variants[0].comps["A"].X != 80 || variants[0].comps["A"].Y != 70 {
		t.Fatalf("expected leftward fallback: %+v", variants[0].comps["A"])
	}
	if _, exists := variants[0].comps["FIXED"]; exists {
		t.Fatal("moved a fixed obstacle")
	}
}

func TestPCBReflowBudgetAndCyclesDoNotClaimImpossible(t *testing.T) {
	x, a, b := reflowPart("X", 100, 110), reflowPart("A", 100, 70), reflowPart("B", 100, 40)
	snap, spec := reflowFixture(x, a, b)
	spec.Groups = []pcbReflowGroupSpec{{ID: "a", AnchorRef: "A", Refs: []string{"A"}}, {ID: "b", AnchorRef: "B", Refs: []string{"B"}}}
	spec.MaxStates = 1
	target := pcbLayoutVariant{label: "seed", comps: map[string]boardComp{"X": mustTranslateBoardComp(t, x, 0, -30)}}
	v, report, err := resolvePCBReflow(spec, target, snap, 4)
	if err != nil || len(v) != 0 || !report.Exhausted || report.Status != "incomplete" || report.States != 1 {
		t.Fatalf("budget misreported: %v %+v", err, report)
	}
	spec.MaxStates = 500
	spec.MaxShiftYMil = 0
	v, report, err = resolvePCBReflow(spec, target, snap, 4)
	if err != nil || len(v) != 0 || report.States != 1 || report.Status != "no-candidate-in-search" {
		t.Fatalf("zero-range cycle not bounded: %v %+v", err, report)
	}
}

func TestPCBReflowFixedAxisAndLockedMemberBlockEntireGroup(t *testing.T) {
	x, a, cap := reflowPart("X", 100, 110), reflowPart("A", 100, 70), reflowPart("C", 130, 70)
	snap, spec := reflowFixture(x, a, cap)
	spec.MaxShiftXMil = 20
	spec.Groups = []pcbReflowGroupSpec{{ID: "a", AnchorRef: "A", Refs: []string{"A", "C"}}}
	spec.FixedAxes = map[string][]string{"A": {"y", "rotation"}}
	target := pcbLayoutVariant{label: "seed", comps: map[string]boardComp{"X": mustTranslateBoardComp(t, x, 0, -40)}}
	v, report, err := resolvePCBReflow(spec, target, snap, 4)
	if err != nil || len(v) != 1 || v[0].comps["A"].Y != 70 || v[0].comps["C"].Y != 70 {
		t.Fatalf("axis constraint failed: %v %+v", err, report)
	}
	snap.Components[2].Locked = true
	v, report, err = resolvePCBReflow(spec, target, snap, 4)
	if err != nil || len(v) != 0 || !strings.Contains(report.FixedGroups["a"], "locked") {
		t.Fatalf("locked satellite detached: %v %+v", err, report)
	}
}

func TestPCBReflowUnknownOrUnownedCopperFreezesModule(t *testing.T) {
	x, a := reflowPart("X", 100, 110), reflowPart("A", 100, 70)
	snap, spec := reflowFixture(x, a)
	spec.Groups = []pcbReflowGroupSpec{{ID: "a", AnchorRef: "A", Refs: []string{"A"}}}
	target := pcbLayoutVariant{label: "seed", comps: map[string]boardComp{"X": mustTranslateBoardComp(t, x, 0, -40)}}
	snap.Copper.Lines = []any{reflowLine("unowned", "net-A", 100, 70, 140, 70)}
	v, report, err := resolvePCBReflow(spec, target, snap, 4)
	if err != nil || len(v) != 0 || !strings.Contains(report.FixedGroups["a"], "unowned attached") {
		t.Fatalf("unowned copper ignored: %v %+v", err, report)
	}
	snap.Copper.Lines = []any{}
	snap.Copper.Availability["poured"] = "unknown"
	v, report, err = resolvePCBReflow(spec, target, snap, 4)
	if err != nil || len(v) != 0 || report.Status != "incomplete" {
		t.Fatalf("unknown copper accepted: %v %+v", err, report)
	}
}

func TestPCBReflowTranslatesInternalCopperAndRecomputesFixedEndpoint(t *testing.T) {
	x, a, cap, fixed := reflowPart("X", 100, 110), reflowPart("A", 100, 70), reflowPart("C", 130, 70), reflowPart("FIXED", 220, 70)
	cap.Pads[0].Net, fixed.Pads[0].Net = "net-A", "net-A"
	snap, spec := reflowFixture(x, a, cap, fixed)
	snap.Copper.Lines = []any{reflowLine("inside", "net-A", 100, 70, 130, 70), reflowLine("outside", "net-A", 130, 70, 220, 70)}
	spec.Groups = []pcbReflowGroupSpec{{ID: "a", AnchorRef: "A", Refs: []string{"A", "C"}, InternalPrimitiveIDs: []string{"inside"}, ExternalConnections: []pcbReflowExternalConnection{{ID: "supply", From: "C.1", To: "FIXED.1", Net: "net-A", Layer: 1, WidthMil: 2, ReplacePrimitiveIDs: []string{"outside"}}}}}
	target := pcbLayoutVariant{label: "seed", comps: map[string]boardComp{"X": mustTranslateBoardComp(t, x, 0, -40)}}
	v, report, err := resolvePCBReflow(spec, target, snap, 4)
	if err != nil || len(v) != 1 {
		t.Fatalf("copper reflow: %v %+v", err, report)
	}
	copper := report.Candidates[0].Copper
	if len(copper.ReplacePrimitiveIDs) != 2 || len(copper.Routes) != 2 {
		t.Fatalf("copper plan incomplete: %+v", copper)
	}
	for _, route := range copper.Routes {
		if route.Role == "reflow-internal" && (route.Points[0] != [2]float64{100, 50} || route.Points[1] != [2]float64{130, 50}) {
			t.Fatalf("internal copper not rigid: %+v", route)
		}
		if route.Role == "reflow-external" && (route.Points[0] != [2]float64{130, 50} || route.Points[len(route.Points)-1] != [2]float64{220, 70}) {
			t.Fatalf("fixed endpoint translated: %+v", route)
		}
	}
}

func TestPCBReflowNonSymmetricRotationTransformsPadsAndCopper(t *testing.T) {
	x, a := reflowPart("X", 100, 110), reflowPart("A", 100, 70)
	a.BBox = &layoutBBox{MinX: 96, MinY: 65, MaxX: 115, MaxY: 77}
	a.Pads[0].X, a.Pads[0].Y = 105, 72
	a.Pads[0].W, a.Pads[0].H = 6, 4
	a.Pads[0].Shape = []any{"RECT", 6.0, 4.0, 0.0}
	snap, spec := reflowFixture(x, a)
	snap.Copper.Lines = []any{reflowLine("inside", "net-A", 105, 72, 113, 72)}
	spec.MaxShiftYMil, spec.StepMil = 30, 30
	spec.Groups = []pcbReflowGroupSpec{{ID: "a", AnchorRef: "A", Refs: []string{"A"}, AllowedRotationsDeg: []float64{90}, InternalPrimitiveIDs: []string{"inside"}}}
	target := pcbLayoutVariant{label: "seed", comps: map[string]boardComp{"X": mustTranslateBoardComp(t, x, 0, -40)}}
	v, report, err := resolvePCBReflow(spec, target, snap, 2)
	if err != nil || len(v) != 1 {
		t.Fatalf("rotation: %v %+v", err, report)
	}
	got := v[0].comps["A"]
	if got.X != 100 || got.Y != 40 || got.Rotation != 90 || got.Pads[0].X != 98 || got.Pads[0].Y != 45 || got.Pads[0].W != 4 || got.Pads[0].H != 6 {
		t.Fatalf("non-symmetric transform is wrong: %+v", got)
	}
	route := report.Candidates[0].Copper.Routes[0]
	if route.Points[0] != [2]float64{98, 45} || route.Points[1] != [2]float64{98, 53} {
		t.Fatalf("copper/pad rotation diverged: %+v", route)
	}
}

func TestPCBReflowRejectsDuplicateOwnershipAndInvalidSearch(t *testing.T) {
	x, a := reflowPart("X", 100, 110), reflowPart("A", 100, 70)
	snap, spec := reflowFixture(x, a)
	target := pcbLayoutVariant{label: "seed", comps: map[string]boardComp{"X": x}}
	spec.Groups = []pcbReflowGroupSpec{{ID: "a", AnchorRef: "A", Refs: []string{"A"}}, {ID: "b", AnchorRef: "A", Refs: []string{"A"}}}
	if _, _, err := resolvePCBReflow(spec, target, snap, 2); err == nil {
		t.Fatal("duplicate physical ownership accepted")
	}
	spec.Groups = spec.Groups[:1]
	for _, step := range []float64{0, -1, math.NaN(), .00001} {
		spec.StepMil = step
		if _, _, err := resolvePCBReflow(spec, target, snap, 2); err == nil {
			t.Fatalf("invalid/unbounded step %g accepted", step)
		}
	}
}

func TestPCBReflowMovedPadCannotLandOnOldOtherNetTrackEndpoint(t *testing.T) {
	x, a := reflowPart("X", 100, 110), reflowPart("A", 100, 70)
	snap, spec := reflowFixture(x, a)
	spec.Groups = []pcbReflowGroupSpec{{ID: "a", AnchorRef: "A", Refs: []string{"A"}}}
	snap.Copper.Lines = []any{reflowLine("obstacle", "OTHER", 100, 50, 140, 50)}
	target := pcbLayoutVariant{label: "seed", comps: map[string]boardComp{"X": mustTranslateBoardComp(t, x, 0, -30)}}
	v, report, err := resolvePCBReflow(spec, target, snap, 4)
	if err != nil || len(v) != 0 || !strings.Contains(strings.Join(report.Rejected, ";"), "moved pad A.1 conflicts with track obstacle") {
		t.Fatalf("old track endpoint short escaped exact check: %v %+v", err, report)
	}
}

func TestPCBReflowCopperEmptyMoveStillChecksOtherNetStaticArea(t *testing.T) {
	x, a := reflowPart("X", 100, 110), reflowPart("A", 100, 70)
	snap, spec := reflowFixture(x, a)
	spec.Groups = []pcbReflowGroupSpec{{ID: "a", AnchorRef: "A", Refs: []string{"A"}}}
	snap.Copper.Fills = []any{map[string]any{"primitiveId": "fill", "net": "OTHER", "layer": float64(1), "geometryAvailable": true, "source": pointsPolygonSource([][2]float64{{96, 46}, {104, 46}, {104, 54}, {96, 54}})}}
	target := pcbLayoutVariant{label: "seed", comps: map[string]boardComp{"X": mustTranslateBoardComp(t, x, 0, -30)}}
	v, report, err := resolvePCBReflow(spec, target, snap, 4)
	if err != nil || len(v) != 0 {
		t.Fatalf("static fill collision accepted: %v %+v", err, report)
	}
	if !strings.Contains(strings.Join(report.Rejected, ";"), "conflicts with") {
		t.Fatalf("missing exact fill collision: %+v", report)
	}
}

func TestPCBReflowNoComponentsRegionForcesAlternatePlacement(t *testing.T) {
	x, a := reflowPart("X", 100, 110), reflowPart("A", 100, 70)
	snap, spec := reflowFixture(x, a)
	spec.MaxShiftXMil = 20
	spec.Groups = []pcbReflowGroupSpec{{ID: "a", AnchorRef: "A", Refs: []string{"A"}}}
	snap.Copper.Regions = []any{map[string]any{"primitiveId": "region", "layer": float64(1), "geometryAvailable": true, "ruleTypeNames": []any{"no-components"}, "source": pointsPolygonSource([][2]float64{{90, 40}, {110, 40}, {110, 55}, {90, 55}})}}
	target := pcbLayoutVariant{label: "seed", comps: map[string]boardComp{"X": mustTranslateBoardComp(t, x, 0, -40)}}
	v, report, err := resolvePCBReflow(spec, target, snap, 4)
	if err != nil || len(v) != 1 || v[0].comps["A"].X != 80 || v[0].comps["A"].Y != 70 {
		t.Fatalf("no-components ignored: %v %+v", err, report)
	}
}

func TestPCBReflowSameNetViaInPadRequiresOwnership(t *testing.T) {
	x, a := reflowPart("X", 100, 110), reflowPart("A", 100, 70)
	snap, spec := reflowFixture(x, a)
	spec.Groups = []pcbReflowGroupSpec{{ID: "a", AnchorRef: "A", Refs: []string{"A"}}}
	snap.Copper.Vias = []any{map[string]any{"primitiveId": "via", "net": "net-A", "x": 100.0, "y": 70.0, "holeDiameter": 5.0, "diameter": 10.0}}
	target := pcbLayoutVariant{label: "seed", comps: map[string]boardComp{"X": mustTranslateBoardComp(t, x, 0, -40)}}
	v, report, err := resolvePCBReflow(spec, target, snap, 4)
	if err != nil || len(v) != 0 || !strings.Contains(report.FixedGroups["a"], "unowned attached via") {
		t.Fatalf("via-in-pad forgotten: %v %+v", err, report)
	}
}

func TestPCBReflowTranslatedBoardProducesTranslatedSolution(t *testing.T) {
	x, a, b := reflowPart("X", 100, 110), reflowPart("A", 100, 70), reflowPart("B", 100, 40)
	snap, spec := reflowFixture(x, a, b)
	spec.Groups = []pcbReflowGroupSpec{{ID: "a", AnchorRef: "A", Refs: []string{"A"}}, {ID: "b", AnchorRef: "B", Refs: []string{"B"}}}
	target := pcbLayoutVariant{label: "seed", comps: map[string]boardComp{"X": mustTranslateBoardComp(t, x, 0, -30)}}
	v, report, err := resolvePCBReflow(spec, target, snap, 4)
	if err != nil || len(v) != 1 {
		t.Fatalf("baseline: %v %+v", err, report)
	}
	for i, c := range snap.Components {
		snap.Components[i] = mustTranslateBoardComp(t, c, 71, 31)
	}
	for i, p := range snap.Outline.Points {
		snap.Outline.Points[i] = [2]float64{p[0] + 71, p[1] + 31}
	}
	snap.Outline.BBox = layoutBBox{MinX: 71, MinY: 31, MaxX: 371, MaxY: 331}
	target.comps["X"] = mustTranslateBoardComp(t, target.comps["X"], 71, 31)
	moved, report, err := resolvePCBReflow(spec, target, snap, 4)
	if err != nil || len(moved) != 1 {
		t.Fatalf("translated: %v %+v", err, report)
	}
	for ref, c := range v[0].comps {
		got := moved[0].comps[ref]
		if got.X != c.X+71 || got.Y != c.Y+31 {
			t.Fatalf("absolute-coordinate assumption for %s", ref)
		}
	}
}

func TestPCBReflowReservationTriggersWithoutComponentOverlap(t *testing.T) {
	x, a, cap := reflowPart("X", 100, 110), reflowPart("A", 100, 70), reflowPart("C", 130, 70)
	cap.Pads[0].Net = "net-A"
	snap, spec := reflowFixture(x, a, cap)
	spec.Groups = []pcbReflowGroupSpec{{ID: "a", AnchorRef: "A", Refs: []string{"A", "C"}, InternalPrimitiveIDs: []string{"inside"}}}
	snap.Copper.Lines = []any{reflowLine("inside", "net-A", 100, 70, 130, 70)}
	spec.Reservations = []pcbReflowReservation{{ID: "escape", Net: "ESCAPE", Layer: 1, WidthMil: 2, ClearanceMil: 2, Points: [][2]float64{{115, 60}, {115, 90}}}}
	target := pcbLayoutVariant{label: "seed", comps: map[string]boardComp{"X": x}}
	v, report, err := resolvePCBReflow(spec, target, snap, 4)
	if err != nil || len(v) != 1 || v[0].comps["A"].Y != 50 || v[0].comps["C"].Y != 50 {
		t.Fatalf("reservation did not trigger whole module move: %v %+v", err, report)
	}
	if !strings.Contains(report.Candidates[0].Moves[0].Reason, "reservation escape blocked by track inside") {
		t.Fatalf("wrong blocking evidence: %+v", report)
	}
}

func TestPCBReflowReservationsMustCoexist(t *testing.T) {
	x := reflowPart("X", 200, 200)
	snap, spec := reflowFixture(x)
	spec.Reservations = []pcbReflowReservation{{ID: "one", Net: "A", Layer: 1, WidthMil: 2, Points: [][2]float64{{50, 60}, {100, 60}}}, {ID: "two", Net: "B", Layer: 1, WidthMil: 2, Points: [][2]float64{{75, 50}, {75, 100}}}}
	target := pcbLayoutVariant{label: "seed", comps: map[string]boardComp{"X": x}}
	v, report, err := resolvePCBReflow(spec, target, snap, 4)
	if err != nil || len(v) != 0 || !strings.Contains(strings.Join(report.Rejected, ";"), "conflict jointly") {
		t.Fatalf("individually valid but crossing reservations accepted: %v %+v", err, report)
	}
}

func TestPCBReflowIndependentRebuildRejectsIncompleteGroupAndIgnoresSubmittedCopper(t *testing.T) {
	x, a, cap := reflowPart("X", 100, 110), reflowPart("A", 100, 70), reflowPart("C", 130, 70)
	cap.Pads[0].Net = "net-A"
	snap, spec := reflowFixture(x, a, cap)
	spec.Groups = []pcbReflowGroupSpec{{ID: "a", AnchorRef: "A", Refs: []string{"A", "C"}, InternalPrimitiveIDs: []string{"inside"}}}
	snap.Copper.Lines = []any{reflowLine("inside", "net-A", 100, 70, 130, 70)}
	target := pcbLayoutVariant{label: "seed", comps: map[string]boardComp{"X": mustTranslateBoardComp(t, x, 0, -40)}}
	v, report, err := resolvePCBReflow(spec, target, snap, 4)
	if err != nil || len(v) != 1 {
		t.Fatalf("seed: %v %+v", err, report)
	}
	evidence := report.Candidates[0]
	evidence.Copper.Routes = nil // A candidate cannot delete evidence and make the checker skip copper.
	poses, copper, err := rebuildPCBReflowCandidate(spec, target, evidence, snap, 4)
	if err != nil || len(copper.Routes) != 1 || len(poses.comps) != 3 {
		t.Fatalf("independent reconstruction used submitted copper: %v %+v", err, copper)
	}
	evidence.Moves[0].Refs = []string{"A"}
	if _, _, err := rebuildPCBReflowCandidate(spec, target, evidence, snap, 4); err == nil {
		t.Fatal("accepted a detached capacitor")
	}
	evidence.Moves[0].Refs = []string{"A", "C"}
	evidence.Moves[0].DYMil = -21
	if _, _, err := rebuildPCBReflowCandidate(spec, target, evidence, snap, 4); err == nil {
		t.Fatal("accepted out-of-range/off-grid move")
	}
}
