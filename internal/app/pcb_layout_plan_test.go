package app

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func lpBBox(x0, y0, x1, y1 float64) *layoutBBox {
	return &layoutBBox{MinX: x0, MinY: y0, MaxX: x1, MaxY: y1}
}

func lpComp(id, ref string, x, y, rot float64, box *layoutBBox, pads ...boardPad) boardComp {
	return boardComp{ID: id, Designator: ref, Layer: 1, X: x, Y: y, Rotation: rot, BBox: box, Pads: pads}
}

func lpSnapshot(comps ...boardComp) *boardSnapshot {
	return &boardSnapshot{Components: comps, Outline: &boardOutline{BBox: layoutBBox{MinX: 0, MinY: 0, MaxX: 1000, MaxY: 800}, Source: "bbox", Format: "fixture"}, Rules: &boardRules{ClearanceMil: 5}}
}

func TestTransformBoardCompUsesFootprintAnchorForAsymmetricGeometry(t *testing.T) {
	c := lpComp("p1", "D1", 100, 100, 0, lpBBox(90, 80, 130, 120), boardPad{ID: "pad1", Number: "1", X: 120, Y: 100, W: 10, H: 20})
	got, err := transformBoardComp(c, 100, 100, 50, 20, 90)
	if err != nil {
		t.Fatal(err)
	}
	if got.X != 150 || got.Y != 120 || got.Rotation != 90 {
		t.Fatalf("pose=%+v", got)
	}
	if got.BBox == nil || got.BBox.MinX != 130 || got.BBox.MinY != 110 || got.BBox.MaxX != 170 || got.BBox.MaxY != 150 {
		t.Fatalf("bbox=%+v; asymmetric anchor offset was not rotated", got.BBox)
	}
	if len(got.Pads) != 1 || got.Pads[0].X != 150 || got.Pads[0].Y != 140 || got.Pads[0].W != 20 || got.Pads[0].H != 10 {
		t.Fatalf("pad=%+v", got.Pads)
	}
}

func TestRigidVariantsTranslateAndRotateWholeModule(t *testing.T) {
	a := lpComp("a", "U1", 200, 200, 0, lpBBox(180, 180, 220, 220))
	b := lpComp("b", "C1", 260, 200, 90, lpBBox(250, 190, 270, 210))
	mod := pcbLayoutModuleSpec{ID: "m", Strategy: "rigid", CopperPolicy: "ignore", AnchorRef: "U1", Members: []pcbLayoutMemberSpec{{Ref: "U1", AllowedRotationsDeg: []float64{90}}, {Ref: "C1", AllowedRotationsDeg: []float64{180}}}, Search: pcbLayoutSearchSpec{OffsetsMil: []pcbLayoutOffset{{XMil: 100, YMil: 20}}, RotationDeltasDeg: []float64{90}}}
	vars, err := generateRigidVariants(mod, map[string]boardComp{"U1": a, "C1": b})
	if err != nil {
		t.Fatal(err)
	}
	if len(vars) != 1 {
		t.Fatalf("variants=%d", len(vars))
	}
	ga, gb := vars[0].comps["U1"], vars[0].comps["C1"]
	if ga.X != 300 || ga.Y != 220 || gb.X != 300 || gb.Y != 280 {
		t.Fatalf("anchors U1=%v,%v C1=%v,%v", ga.X, ga.Y, gb.X, gb.Y)
	}
	if math.Abs(math.Hypot(gb.X-ga.X, gb.Y-ga.Y)-60) > 1e-6 {
		t.Fatal("rigid transform changed member spacing")
	}
}

func TestLayoutPlanRejectsFixedAxesAndLockedMembers(t *testing.T) {
	for _, tc := range []struct {
		name string
		comp boardComp
		axes []string
		off  pcbLayoutOffset
		want string
	}{
		{"fixed-x", lpComp("a", "U1", 200, 200, 0, lpBBox(180, 180, 220, 220)), []string{"x"}, pcbLayoutOffset{XMil: 10}, "fixes x"},
		{"fixed-y", lpComp("a", "U1", 200, 200, 0, lpBBox(180, 180, 220, 220)), []string{"y"}, pcbLayoutOffset{YMil: 10}, "fixes y"},
		{"locked", func() boardComp {
			c := lpComp("a", "U1", 200, 200, 0, lpBBox(180, 180, 220, 220))
			c.Locked = true
			return c
		}(), nil, pcbLayoutOffset{XMil: 10}, "locked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := pcbLayoutPlanInput{SchemaVersion: 1, Units: "mil", CoordinateSemantic: "footprint-anchor", Modules: []pcbLayoutModuleSpec{{ID: "m", Strategy: "rigid", CopperPolicy: "ignore", AnchorRef: "U1", Members: []pcbLayoutMemberSpec{{Ref: "U1", FixedAxes: tc.axes}}, Search: pcbLayoutSearchSpec{OffsetsMil: []pcbLayoutOffset{tc.off}}}}}
			rep, err := planPCBLayoutModule(in, lpSnapshot(tc.comp), "m", 3)
			if err == nil || len(rep.Rejected) != 1 || !containsText(rep.Rejected[0].Reasons, tc.want) {
				t.Fatalf("err=%v rejected=%+v", err, rep.Rejected)
			}
		})
	}
}

func TestPinSatelliteAssignmentsUseDeclaredOwnerPadsOnSameNet(t *testing.T) {
	u := lpComp("u", "U6", 500, 400, 0, lpBBox(400, 300, 600, 500),
		boardPad{ID: "u1", Number: "1", Net: "+3V3", X: 405, Y: 350, W: 10, H: 10},
		boardPad{ID: "u5", Number: "5", Net: "+3V3", X: 595, Y: 450, W: 10, H: 10})
	c1 := lpComp("c1", "C1", 700, 350, 0, lpBBox(690, 330, 710, 370), boardPad{ID: "c1p1", Number: "1", Net: "+3V3", X: 690, Y: 350, W: 10, H: 10}, boardPad{ID: "c1p2", Number: "2", Net: "GND", X: 710, Y: 350, W: 10, H: 10})
	c2 := lpComp("c2", "C2", 300, 450, 0, lpBBox(290, 430, 310, 470), boardPad{ID: "c2p1", Number: "1", Net: "+3V3", X: 310, Y: 450, W: 10, H: 10}, boardPad{ID: "c2p2", Number: "2", Net: "GND", X: 290, Y: 450, W: 10, H: 10})
	in := pcbLayoutPlanInput{SchemaVersion: 1, Units: "mil", CoordinateSemantic: "footprint-anchor", MinGapMil: 0.1, Modules: []pcbLayoutModuleSpec{{ID: "mcu", Strategy: "pin-satellites", CopperPolicy: "ignore", AnchorRef: "U6", Members: []pcbLayoutMemberSpec{{Ref: "U6", FixedAxes: []string{"x", "y", "rotation"}}, {Ref: "C1", AllowedRotationsDeg: []float64{0, 180}}, {Ref: "C2", AllowedRotationsDeg: []float64{0, 180}}}, Search: pcbLayoutSearchSpec{GapsMil: []float64{10}}, PinAssignments: []pcbLayoutPinAssignment{{MemberRef: "C1", MemberPad: "1", OwnerRef: "U6", OwnerPad: "1"}, {MemberRef: "C2", MemberPad: "1", OwnerRef: "U6", OwnerPad: "5"}}}}}
	rep, err := planPCBLayoutModule(in, lpSnapshot(u, c1, c2), "mcu", 3)
	if err != nil {
		t.Fatalf("plan: %v rejected=%+v", err, rep.Rejected)
	}
	if len(rep.Candidates) != 1 || len(rep.Candidates[0].Measurements.PinDistances) != 2 {
		t.Fatalf("rep=%+v", rep)
	}
	for _, d := range rep.Candidates[0].Measurements.PinDistances {
		if d.Member == "C1" && d.OwnerPad != "1" {
			t.Fatalf("C1 rebound to wrong same-net pad: %+v", d)
		}
		if d.Member == "C2" && d.OwnerPad != "5" {
			t.Fatalf("C2 rebound to wrong same-net pad: %+v", d)
		}
	}
}

func TestLayoutPlanMeasurementsNameClosestGapPairs(t *testing.T) {
	u := lpComp("u", "U1", 110, 110, 0, lpBBox(100, 100, 120, 120))
	c := lpComp("c", "C1", 140, 110, 0, lpBBox(130, 100, 150, 120))
	near := lpComp("near", "R1", 170, 110, 0, lpBBox(160, 100, 180, 120))
	far := lpComp("far", "R2", 300, 110, 0, lpBBox(290, 100, 310, 120))
	mod := pcbLayoutModuleSpec{ID: "m", Members: []pcbLayoutMemberSpec{{Ref: "U1"}, {Ref: "C1"}}}
	variant := pcbLayoutVariant{comps: map[string]boardComp{"U1": u, "C1": c}}
	base := map[string]boardComp{"R2": far, "C1": c, "R1": near, "U1": u}
	keepouts := []pcbLayoutKeepout{{ID: "K1", MinX: 90, MinY: 100, MaxX: 95, MaxY: 120, Layers: []string{"top"}}}
	m := measurePCBLayoutCandidate(mod, variant, base, lpSnapshot(u, c, far, near), map[string]bool{"U1": true, "C1": true}, keepouts)
	if m.ClosestInternalGap == nil || m.ClosestInternalGap.From != "C1" || m.ClosestInternalGap.To != "U1" || m.ClosestInternalGap.DistanceMil != 10 {
		t.Fatalf("closest internal=%+v", m.ClosestInternalGap)
	}
	if m.ClosestExternalComponentGap == nil || m.ClosestExternalComponentGap.From != "C1" || m.ClosestExternalComponentGap.To != "R1" || m.ClosestExternalComponentGap.DistanceMil != 10 {
		t.Fatalf("closest external=%+v", m.ClosestExternalComponentGap)
	}
	if m.ClosestKeepoutGap == nil || m.ClosestKeepoutGap.From != "U1" || m.ClosestKeepoutGap.To != "K1" || m.ClosestKeepoutGap.DistanceMil != 5 {
		t.Fatalf("closest keepout=%+v", m.ClosestKeepoutGap)
	}
}

func TestPinAssignmentRejectsAmbiguousPadNumber(t *testing.T) {
	u := lpComp("u", "U6", 500, 400, 0, lpBBox(400, 300, 600, 500), boardPad{ID: "a", Number: "1", Net: "VCC", X: 400, Y: 350}, boardPad{ID: "b", Number: "1", Net: "VCC", X: 400, Y: 450})
	c := lpComp("c", "C1", 200, 200, 0, lpBBox(190, 190, 210, 210), boardPad{ID: "c1", Number: "1", Net: "VCC", X: 190, Y: 200})
	mod := pcbLayoutModuleSpec{ID: "m", Strategy: "pin-satellites", CopperPolicy: "ignore", AnchorRef: "U6", Members: []pcbLayoutMemberSpec{{Ref: "U6"}, {Ref: "C1"}}, Search: pcbLayoutSearchSpec{GapsMil: []float64{10}}, PinAssignments: []pcbLayoutPinAssignment{{MemberRef: "C1", MemberPad: "1", OwnerRef: "U6", OwnerPad: "1"}}}
	_, err := generatePinSatelliteVariants(mod, map[string]boardComp{"U6": u, "C1": c})
	if err == nil || !containsText([]string{err.Error()}, "ambiguous") {
		t.Fatalf("err=%v", err)
	}
}

func TestLayoutPlanKeepoutAndLayerAwareObstacle(t *testing.T) {
	part := lpComp("p", "LED1", 100, 100, 0, lpBBox(90, 90, 110, 110))
	bottom := lpComp("b", "B1", 210, 100, 0, lpBBox(195, 85, 225, 115))
	bottom.Layer = 2
	in := pcbLayoutPlanInput{SchemaVersion: 1, Units: "mil", CoordinateSemantic: "footprint-anchor", MinGapMil: 5, Modules: []pcbLayoutModuleSpec{{ID: "led", Strategy: "rigid", CopperPolicy: "ignore", AnchorRef: "LED1", Members: []pcbLayoutMemberSpec{{Ref: "LED1"}}, Search: pcbLayoutSearchSpec{OffsetsMil: []pcbLayoutOffset{{XMil: 100}}}}}}
	rep, err := planPCBLayoutModule(in, lpSnapshot(part, bottom), "led", 1)
	if err != nil {
		t.Fatalf("bottom-side obstacle must not block top part: %v %+v", err, rep.Rejected)
	}
	in.Keepouts = []pcbLayoutKeepout{{ID: "lcd", MinX: 190, MinY: 80, MaxX: 230, MaxY: 120, Layers: []string{"top"}}}
	rep, err = planPCBLayoutModule(in, lpSnapshot(part, bottom), "led", 1)
	if err == nil || len(rep.Rejected) != 1 || !containsText(rep.Rejected[0].Reasons, "keepout lcd") {
		t.Fatalf("err=%v rep=%+v", err, rep)
	}
}

func TestLayoutPlanDeterministicAcrossBoardOrder(t *testing.T) {
	a := lpComp("a", "U1", 200, 200, 0, lpBBox(180, 180, 220, 220))
	b := lpComp("b", "C1", 260, 200, 0, lpBBox(250, 190, 270, 210))
	in := pcbLayoutPlanInput{SchemaVersion: 1, Units: "mil", CoordinateSemantic: "footprint-anchor", Modules: []pcbLayoutModuleSpec{{ID: "m", Strategy: "rigid", CopperPolicy: "ignore", AnchorRef: "U1", Members: []pcbLayoutMemberSpec{{Ref: "U1"}, {Ref: "C1"}}, Search: pcbLayoutSearchSpec{OffsetsMil: []pcbLayoutOffset{{XMil: 20}, {YMil: 20}}}}}}
	r1, e1 := planPCBLayoutModule(in, lpSnapshot(a, b), "m", 3)
	r2, e2 := planPCBLayoutModule(in, lpSnapshot(b, a), "m", 3)
	if e1 != nil || e2 != nil {
		t.Fatalf("errors %v %v", e1, e2)
	}
	j1, _ := json.Marshal(r1)
	j2, _ := json.Marshal(r2)
	if string(j1) != string(j2) {
		t.Fatalf("output depends on board component order\n%s\n%s", j1, j2)
	}
}

func TestDecodeLayoutPlanRejectsTrailingJSONAndInvalidGeometryValues(t *testing.T) {
	base := `{"schemaVersion":1,"units":"mil","coordinateSemantic":"footprint-anchor","modules":[{"id":"m","strategy":"rigid","copperPolicy":"ignore","anchorRef":"U1","members":[{"ref":"U1"}],"search":{}}]}`
	if _, err := decodePCBLayoutPlanInput([]byte(base + `{}`)); err == nil || !strings.Contains(err.Error(), "multiple JSON values") {
		t.Fatalf("trailing JSON err=%v", err)
	}
	badRotation := strings.Replace(base, `"search":{}`, `"search":{"rotationDeltasDeg":[45]}`, 1)
	if _, err := decodePCBLayoutPlanInput([]byte(badRotation)); err == nil || !strings.Contains(err.Error(), "0/90/180/270") {
		t.Fatalf("rotation err=%v", err)
	}
	badKeepout := strings.Replace(base, `"modules"`, `"keepouts":[{"id":"K","minXMil":0,"minYMil":0,"maxXMil":10,"maxYMil":10,"layers":["inner"]}],"modules"`, 1)
	if _, err := decodePCBLayoutPlanInput([]byte(badKeepout)); err == nil || !strings.Contains(err.Error(), "invalid layer") {
		t.Fatalf("keepout err=%v", err)
	}
}

func TestEdgeHybridRequiresCompleteFollowersAndAnchor(t *testing.T) {
	led := lpComp("led", "LED1", 200, 100, 0, lpBBox(180, 80, 220, 120), boardPad{ID: "l1", Number: "1", Net: "N", X: 200, Y: 120})
	r := lpComp("r", "R1", 200, 200, 0, lpBBox(190, 190, 210, 210), boardPad{ID: "r1", Number: "1", Net: "N", X: 200, Y: 190})
	c := lpComp("c", "C1", 250, 200, 0, lpBBox(240, 190, 260, 210))
	mod := pcbLayoutModuleSpec{ID: "edge", Strategy: "edge", CopperPolicy: "ignore", AnchorRef: "LED1", Members: []pcbLayoutMemberSpec{{Ref: "LED1"}, {Ref: "R1"}, {Ref: "C1"}}, Search: pcbLayoutSearchSpec{Edges: []string{"bottom"}, EdgeMember: "LED1", GapsMil: []float64{8}}, PinAssignments: []pcbLayoutPinAssignment{{MemberRef: "R1", MemberPad: "1", OwnerRef: "LED1", OwnerPad: "1"}}}
	all := map[string]boardComp{"LED1": led, "R1": r, "C1": c}
	if _, err := validatePCBLayoutModule(mod, all); err == nil || !strings.Contains(err.Error(), "must be a pin-assignment follower") {
		t.Fatalf("missing follower err=%v", err)
	}
	mod.Members[2].FixedAxes = []string{"x", "y", "rotation"}
	if _, err := validatePCBLayoutModule(mod, all); err != nil {
		t.Fatalf("explicit root rejected: %v", err)
	}
	mod.AnchorRef = "R1"
	if _, err := validatePCBLayoutModule(mod, all); err == nil || !strings.Contains(err.Error(), "anchorRef == edgeMember") {
		t.Fatalf("anchor err=%v", err)
	}
}

func TestLayoutPlanRejectsMissingMemberGeometryAndExternalObstacleGeometry(t *testing.T) {
	part := lpComp("p", "U1", 100, 100, 0, lpBBox(90, 90, 110, 110))
	in := pcbLayoutPlanInput{SchemaVersion: 1, Units: "mil", CoordinateSemantic: "footprint-anchor", Modules: []pcbLayoutModuleSpec{{ID: "m", Strategy: "rigid", CopperPolicy: "ignore", AnchorRef: "U1", Members: []pcbLayoutMemberSpec{{Ref: "U1"}}, Search: pcbLayoutSearchSpec{OffsetsMil: []pcbLayoutOffset{{XMil: 20}}}}}}
	part.BBox = nil
	if _, err := planPCBLayoutModule(in, lpSnapshot(part), "m", 1); err == nil || !strings.Contains(err.Error(), "no bbox") {
		t.Fatalf("member bbox err=%v", err)
	}
	part.BBox = lpBBox(90, 90, 110, 110)
	unknown := lpComp("o", "X1", 400, 400, 0, nil)
	rep, err := planPCBLayoutModule(in, lpSnapshot(part, unknown), "m", 1)
	if err == nil || len(rep.Rejected) == 0 || !containsText(rep.Rejected[0].Reasons, "has no bbox") || !containsText(rep.MissingGeometry, "X1:bbox") {
		t.Fatalf("external geometry err=%v rep=%+v", err, rep)
	}
}

func TestLayoutPlanTreatsThroughHoleComponentAsCrossLayerObstacle(t *testing.T) {
	part := lpComp("p", "U1", 100, 100, 0, lpBBox(90, 90, 110, 110))
	bottomTH := lpComp("j", "J1", 200, 100, 0, lpBBox(190, 90, 210, 110), boardPad{ID: "jp", Number: "1", Layer: pcbLayerMulti, X: 200, Y: 100})
	bottomTH.Layer = 2
	in := pcbLayoutPlanInput{SchemaVersion: 1, Units: "mil", CoordinateSemantic: "footprint-anchor", MinGapMil: 5, Modules: []pcbLayoutModuleSpec{{ID: "m", Strategy: "rigid", CopperPolicy: "ignore", AnchorRef: "U1", Members: []pcbLayoutMemberSpec{{Ref: "U1"}}, Search: pcbLayoutSearchSpec{OffsetsMil: []pcbLayoutOffset{{XMil: 100}}}}}}
	rep, err := planPCBLayoutModule(in, lpSnapshot(part, bottomTH), "m", 1)
	if err == nil || len(rep.Rejected) != 1 || !containsText(rep.Rejected[0].Reasons, "obstacle J1") {
		t.Fatalf("through-hole obstacle err=%v rep=%+v", err, rep)
	}
}

func TestEdgeDistanceUsesPolygonCenterlineNotRenderedBBox(t *testing.T) {
	o := &boardOutline{BBox: layoutBBox{MinX: -5, MinY: -5, MaxX: 1005, MaxY: 805}, Points: [][2]float64{{0, 0}, {1000, 0}, {1000, 800}, {0, 800}}, Source: "polygon"}
	edge, d := edgeDistanceForBBox(o, layoutBBox{MinX: 100, MinY: 20, MaxX: 150, MaxY: 70})
	if edge != "bottom" || math.Abs(d-20) > 1e-6 {
		t.Fatalf("edge=%s distance=%v", edge, d)
	}
}

func TestRigidStrategyValidatesDeclaredPinOwnership(t *testing.T) {
	u := lpComp("u", "U1", 100, 100, 0, lpBBox(90, 90, 110, 110), boardPad{ID: "u1", Number: "1", Net: "A", X: 100, Y: 100})
	c := lpComp("c", "C1", 150, 100, 0, lpBBox(140, 90, 160, 110), boardPad{ID: "c1", Number: "1", Net: "B", X: 150, Y: 100})
	mod := pcbLayoutModuleSpec{ID: "m", Strategy: "rigid", CopperPolicy: "ignore", AnchorRef: "U1", Members: []pcbLayoutMemberSpec{{Ref: "U1"}, {Ref: "C1"}}, PinAssignments: []pcbLayoutPinAssignment{{MemberRef: "C1", MemberPad: "1", OwnerRef: "U1", OwnerPad: "1"}}}
	if _, err := validatePCBLayoutModule(mod, map[string]boardComp{"U1": u, "C1": c}); err == nil || !strings.Contains(err.Error(), "does not share") {
		t.Fatalf("pin ownership err=%v", err)
	}
}

func TestLayoutPlanRequiresPrimitiveIDAndExplicitCopperPolicy(t *testing.T) {
	c := lpComp("", "U1", 100, 100, 0, lpBBox(90, 90, 110, 110))
	mod := pcbLayoutModuleSpec{ID: "m", Strategy: "rigid", CopperPolicy: "ignore", AnchorRef: "U1", Members: []pcbLayoutMemberSpec{{Ref: "U1"}}}
	if _, err := validatePCBLayoutModule(mod, map[string]boardComp{"U1": c}); err == nil || !strings.Contains(err.Error(), "primitiveId") {
		t.Fatalf("primitiveId err=%v", err)
	}
	c.ID = "u"
	mod.CopperPolicy = ""
	if _, err := validatePCBLayoutModule(mod, map[string]boardComp{"U1": c}); err == nil || !strings.Contains(err.Error(), "explicit copperPolicy") {
		t.Fatalf("copperPolicy err=%v", err)
	}
}

func TestEquivalentOwnerPadsValidateAndMeasureNearestPad(t *testing.T) {
	u := lpComp("u", "U2", 100, 100, 0, lpBBox(80, 80, 120, 120),
		boardPad{ID: "u2", Number: "2", Net: "VOUT", X: 90, Y: 100},
		boardPad{ID: "u4", Number: "4", Net: "VOUT", X: 110, Y: 100})
	c := lpComp("c", "C1", 150, 100, 0, lpBBox(140, 90, 160, 110), boardPad{ID: "c1", Number: "1", Net: "VOUT", X: 145, Y: 100})
	mod := pcbLayoutModuleSpec{ID: "m", Strategy: "rigid", CopperPolicy: "ignore", AnchorRef: "U2", Members: []pcbLayoutMemberSpec{{Ref: "U2"}, {Ref: "C1"}}, PinAssignments: []pcbLayoutPinAssignment{{MemberRef: "C1", MemberPad: "1", OwnerRef: "U2", OwnerPads: []string{"2", "4"}}}}
	members, err := validatePCBLayoutModule(mod, map[string]boardComp{"U2": u, "C1": c})
	if err != nil {
		t.Fatal(err)
	}
	v := pcbLayoutVariant{comps: members}
	m := measurePCBLayoutCandidate(mod, v, members, lpSnapshot(u, c), map[string]bool{"U2": true, "C1": true}, nil)
	if len(m.PinDistances) != 1 || m.PinDistances[0].OwnerPad != "2|4" || m.PinDistances[0].DistanceMil != 35 {
		t.Fatalf("measurement=%+v", m.PinDistances)
	}
}

func TestFacingMeasurementAndPartialPropagation(t *testing.T) {
	c := lpComp("u", "J1", 500, 700, 90, lpBBox(480, 680, 520, 720))
	mod := pcbLayoutModuleSpec{ID: "edge", Strategy: "rigid", CopperPolicy: "ignore", AnchorRef: "J1", Members: []pcbLayoutMemberSpec{{Ref: "J1", LocalFacing: "right"}}}
	snap := lpSnapshot(c)
	snap.Partial = []string{"keepout geometry unavailable"}
	v := pcbLayoutVariant{comps: map[string]boardComp{"J1": c}}
	m := measurePCBLayoutCandidate(mod, v, map[string]boardComp{"J1": c}, snap, map[string]bool{"J1": true}, nil)
	if len(m.Facings) != 1 || m.Facings[0].ActualFacing != "top" || !m.Facings[0].OutwardAligned {
		t.Fatalf("facing=%+v", m.Facings)
	}
	if !containsText(m.Limitations, "keepout geometry unavailable") {
		t.Fatalf("limitations=%v", m.Limitations)
	}
}

func TestBBoxContainmentRejectsConcaveOutlineCrossing(t *testing.T) {
	o := &boardOutline{BBox: layoutBBox{MinX: 0, MinY: 0, MaxX: 100, MaxY: 100}, Points: [][2]float64{{0, 0}, {100, 0}, {100, 100}, {60, 100}, {60, 40}, {40, 40}, {40, 100}, {0, 100}}, Source: "polygon"}
	if bboxInsideBoardOutline(o, layoutBBox{MinX: 30, MinY: 50, MaxX: 70, MaxY: 60}) {
		t.Fatal("bbox crosses a concave notch even though all four corners are in material")
	}
}

func containsText(lines []string, needle string) bool {
	for _, s := range lines {
		if len(needle) == 0 || stringContains(s, needle) {
			return true
		}
	}
	return false
}
func stringContains(s, sub string) bool {
	if len(sub) > len(s) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func mustTranslateBoardComp(t *testing.T, c boardComp, dx, dy float64) boardComp {
	t.Helper()
	got, err := translateBoardComp(c, dx, dy)
	if err != nil {
		t.Fatal(err)
	}
	return got
}
