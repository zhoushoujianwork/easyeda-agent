package app

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func cloneVariantInput(t *testing.T, in SchematicRenderInput) SchematicRenderInput {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var copy SchematicRenderInput
	if err := json.Unmarshal(raw, &copy); err != nil {
		t.Fatal(err)
	}
	return copy
}

func refreshTestVariantFrame(t *testing.T, id, title string, v *SchematicZoneVariant) {
	t.Helper()
	p := powerLayoutPlan{Placements: v.Layout.Placements, Wires: v.Layout.Wires, Flags: v.Layout.Flags}
	v.ContentBounds = powerLayoutContentBounds(&p)
	f, err := measureSchModuleFrameObstacles(id, title, powerLayoutContentObstacles(&p), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	v.Frame = f
}

// These are independently constructed abstract measured bodies, not a copied
// exercise circuit. All NC states are explicit; geometry is intentionally roomy.
func variantSafetyFixture(t *testing.T) SchematicRenderInput {
	t.Helper()
	base := &SchematicLayoutResult{SchemaVersion: 1,
		ComponentIDs:     map[string]string{"U1": "core", "C1": "passive"},
		PinStates:        map[string]map[string]string{"core": {"1": "nc", "2": "nc"}, "passive": {"1": "nc", "2": "nc"}},
		AllowedRotations: map[string][]float64{"core": {0}, "passive": {0, 90, 180, 270}},
		Placements: []SchematicPlacement{
			{PrimitiveID: "body-core", Designator: "U1", Value: "CORE", BBox: SchematicBox{-20, -20, 20, 20},
				TextBBoxes: []SchematicBox{{-20, 40, 20, 50}}, Pins: []SchematicPin{{Number: "1", Name: "A", X: -30}, {Number: "2", Name: "B", X: 30}}},
			{PrimitiveID: "body-passive", Designator: "C1", Value: "10nF", X: 150, BBox: SchematicBox{145, -10, 155, 10},
				TextBBoxes: []SchematicBox{{165, -10, 190, 0}}, Pins: []SchematicPin{{Number: "1", Name: "P", X: 150, Y: 20}, {Number: "2", Name: "N", X: 150, Y: -20}}},
		},
	}
	v := SchematicZoneVariant{ID: "base", Layout: base}
	refreshTestVariantFrame(t, "zone", "Z", &v)
	in := SchematicRenderInput{SchemaVersion: 1, Zones: []SchematicRenderZone{{ID: "zone", Title: "Z", CoreComponentID: "core", Layout: base,
		Frame: &v.Frame, ContentBounds: &v.ContentBounds, Variants: []SchematicZoneVariant{v}, SelectedVariantID: "base"}}}
	alt := cloneVariantInput(t, in).Zones[0].Variants[0]
	alt.ID = "turned"
	alt.Layout.Placements[1] = plTranslate(plRotate(alt.Layout.Placements[1], 1), 50, 100)
	refreshTestVariantFrame(t, "zone", "Z", &alt)
	in.Zones[0].Variants = append(in.Zones[0].Variants, alt)
	return in
}

func TestZoneVariantsValidateAuthorizedRigidGeometryWithoutMutation(t *testing.T) {
	in := variantSafetyFixture(t)
	before, _ := json.Marshal(in)
	if err := validateSchematicZoneVariants(in); err != nil {
		t.Fatal(err)
	}
	// A symmetric body is not sufficient proof: the non-symmetric text box and
	// named pin coordinates above also have to undergo that same quarter turn.
	if _, err := RenderSchematicLayoutSVG(in); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(in)
	if !bytes.Equal(before, after) {
		t.Fatal("variant validation mutated source evidence")
	}
	in.Zones[0].SelectedVariantID = ""
	if err := validateSchematicZoneVariants(in); err != nil {
		t.Fatal("an omitted selection may match any listed geometry", err)
	}
	// Diagnostic counters are not authority and are not part of selected shape.
	in = cloneVariantInput(t, in) // Detach the shared main/base pointer first.
	in.Zones[0].Variants[0].Layout.Score = [4]float64{-100000, 5, 8, 2}
	in.Zones[0].Variants[0].Layout.CandidatesUsed = 999
	if err := validateSchematicZoneVariants(in); err != nil {
		t.Fatal("trusted diagnostic scores instead of geometry", err)
	}
}

func TestZoneVariantsPermitMirroredRigidRotationButNotMirrorChanges(t *testing.T) {
	in := variantSafetyFixture(t)
	in.Zones[0].Layout.Placements[1].Mirror = true
	in.Zones[0].Variants[0].Layout.Placements[1].Mirror = true
	in.Zones[0].Variants[1].Layout.Placements[1].Mirror = true
	if err := validateSchematicZoneVariants(in); err != nil {
		t.Fatal("same mirrored source geometry should remain a rigid transform", err)
	}
	in.Zones[0].Variants[1].Layout.Placements[1].Mirror = false
	if err := validateSchematicZoneVariants(in); err == nil {
		t.Fatal("accepted changed mirror")
	}
}

func TestZoneVariantsRejectMetadataAndSelectionForgery(t *testing.T) {
	base := variantSafetyFixture(t)
	for name, mutate := range map[string]func(*SchematicRenderInput){
		"too-many": func(in *SchematicRenderInput) {
			for i := 0; i < 3; i++ {
				in.Zones[0].Variants = append(in.Zones[0].Variants, in.Zones[0].Variants[0])
			}
		},
		"empty-id":        func(in *SchematicRenderInput) { in.Zones[0].Variants[1].ID = "  " },
		"duplicate-id":    func(in *SchematicRenderInput) { in.Zones[0].Variants[1].ID = "base" },
		"missing-layout":  func(in *SchematicRenderInput) { in.Zones[0].Variants[1].Layout = nil },
		"missing-core":    func(in *SchematicRenderInput) { in.Zones[0].CoreComponentID = "" },
		"unknown-core":    func(in *SchematicRenderInput) { in.Zones[0].CoreComponentID = "unknown" },
		"selected-absent": func(in *SchematicRenderInput) { in.Zones[0].SelectedVariantID = "absent" },
		"selected-wrong":  func(in *SchematicRenderInput) { in.Zones[0].SelectedVariantID = "turned" },
		"main-not-listed": func(in *SchematicRenderInput) {
			in.Zones[0].SelectedVariantID = ""
			in.Zones[0].Variants = in.Zones[0].Variants[1:]
		},
		"nested-main": func(in *SchematicRenderInput) {
			in.Zones[0].Layout.Variants = []SchematicLayoutVariant{{ID: "nested", Layout: &SchematicLayoutResult{}}}
		},
		"nested-candidate": func(in *SchematicRenderInput) {
			in.Zones[0].Variants[1].Layout.Variants = []SchematicLayoutVariant{{ID: "nested", Layout: &SchematicLayoutResult{}}}
		},
		"fake-bounds": func(in *SchematicRenderInput) { in.Zones[0].Variants[1].ContentBounds.MaxX += 10 },
		"frame-title": func(in *SchematicRenderInput) { in.Zones[0].Variants[1].Frame.Title = "other" },
		"frame-id":    func(in *SchematicRenderInput) { in.Zones[0].Variants[1].Frame.ID = "other" },
		"frame-clip":  func(in *SchematicRenderInput) { in.Zones[0].Variants[1].Frame.Rect.MaxX -= 100 },
		"fake-title-width": func(in *SchematicRenderInput) {
			in.Zones[0].Variants[1].Frame.TitleLayout.Width = 1
		},
		"blocked-diagnostic": func(in *SchematicRenderInput) { in.Zones[0].Status = "blocked"; in.Diagnostic = true },
		"unknown-allowed-id": func(in *SchematicRenderInput) {
			in.Zones[0].Variants[1].Layout.AllowedRotations["extra"] = []float64{0}
		},
		"duplicate-angle": func(in *SchematicRenderInput) {
			in.Zones[0].Variants[1].Layout.AllowedRotations["passive"] = []float64{0, 360}
		},
		"invalid-angle": func(in *SchematicRenderInput) {
			in.Zones[0].Variants[1].Layout.AllowedRotations["passive"] = []float64{45}
		},
		"nonfinite-angle": func(in *SchematicRenderInput) {
			in.Zones[0].Variants[1].Layout.AllowedRotations["passive"] = []float64{math.NaN()}
		},
	} {
		t.Run(name, func(t *testing.T) {
			in := cloneVariantInput(t, base)
			mutate(&in)
			if err := validateSchematicZoneVariants(in); err == nil {
				t.Fatal("accepted forged candidate metadata")
			}
		})
	}
}

func TestZoneVariantsRejectChangedCircuitAndNonRigidGeometry(t *testing.T) {
	base := variantSafetyFixture(t)
	for name, mutate := range map[string]func(*SchematicLayoutResult){
		"stable-id":    func(l *SchematicLayoutResult) { l.ComponentIDs["C1"] = "different" },
		"ref":          func(l *SchematicLayoutResult) { l.Placements[1].Designator = "C9" },
		"value":        func(l *SchematicLayoutResult) { l.Placements[1].Value = "1uF" },
		"primitive-id": func(l *SchematicLayoutResult) { l.Placements[1].PrimitiveID = "different" },
		"mirror":       func(l *SchematicLayoutResult) { l.Placements[1].Mirror = true },
		"pin-name":     func(l *SchematicLayoutResult) { l.Placements[1].Pins[0].Name = "changed" },
		"pin-number":   func(l *SchematicLayoutResult) { l.Placements[1].Pins[0].Number = "3" },
		"pin-net":      func(l *SchematicLayoutResult) { l.Placements[1].Pins[0].Net = "unexpected" },
		"pin-state":    func(l *SchematicLayoutResult) { l.PinStates["passive"]["1"] = "unconnected" },
		"extra-state":  func(l *SchematicLayoutResult) { l.PinStates["passive"]["3"] = "nc" },
		"missing-pin":  func(l *SchematicLayoutResult) { l.Placements[1].Pins = l.Placements[1].Pins[:1] },
		"bbox-scale":   func(l *SchematicLayoutResult) { l.Placements[1].BBox.MaxY += 5 },
		"pin-distort":  func(l *SchematicLayoutResult) { l.Placements[1].Pins[0].X -= 5 },
		"text-distort": func(l *SchematicLayoutResult) { l.Placements[1].TextBBoxes[0].MaxX += 5 },
		"text-drop":    func(l *SchematicLayoutResult) { l.Placements[1].TextBBoxes = nil },
		"core-translate": func(l *SchematicLayoutResult) {
			l.Placements[0] = plTranslate(l.Placements[0], 5, 0)
		},
		"core-turn": func(l *SchematicLayoutResult) {
			l.Placements[0] = plRotate(l.Placements[0], 1)
		},
		"self-authorized": func(l *SchematicLayoutResult) { l.AllowedRotations["core"] = []float64{0, 90} },
	} {
		t.Run(name, func(t *testing.T) {
			in := cloneVariantInput(t, base)
			v := &in.Zones[0].Variants[1]
			mutate(v.Layout)
			// Recompute the frame so a rejection cannot be attributed merely to a
			// stale bounds cache; the circuit/rigid-transform invariant must hold.
			refreshTestVariantFrame(t, "zone", "Z", v)
			if err := validateSchematicZoneVariants(in); err == nil {
				t.Fatal("accepted circuit change or non-rigid geometry")
			}
		})
	}
}

func TestZoneVariantsRejectUnauthorizedTurnsAndMissingNamedTrees(t *testing.T) {
	in := variantSafetyFixture(t)
	in.Zones[0].Layout.AllowedRotations = nil
	for i := range in.Zones[0].Variants {
		in.Zones[0].Variants[i].Layout.AllowedRotations = nil
	}
	if err := validateSchematicZoneVariants(in); err == nil {
		t.Fatal("rotated a peripheral without allowedRotations authority")
	}
	in = renderFixture(t)
	z := &in.Zones[0]
	z.CoreComponentID = "anchor"
	v := SchematicZoneVariant{ID: "base", Layout: z.Layout}
	refreshTestVariantFrame(t, z.ID, z.Title, &v)
	z.Frame, z.ContentBounds = &v.Frame, &v.ContentBounds
	z.Variants = []SchematicZoneVariant{v}
	if err := validateSchematicZoneVariants(in); err != nil {
		t.Fatal(err)
	}
	bad := cloneVariantInput(t, in).Zones[0].Variants[0]
	bad.ID, bad.Layout.Wires, bad.Layout.Flags = "unfinished", nil, nil
	refreshTestVariantFrame(t, z.ID, z.Title, &bad)
	z.Variants = append(z.Variants, bad)
	in.Diagnostic = true
	if err := validateSchematicZoneVariants(in); err == nil {
		t.Fatal("diagnostics let an unselected incomplete candidate bypass naming checks")
	}
}

func TestZoneVariantsNoAlternativesPreservesPriorContract(t *testing.T) {
	in := SchematicRenderInput{Zones: []SchematicRenderZone{{SelectedVariantID: "previously-selected"}}}
	if err := validateSchematicZoneVariants(in); err != nil {
		t.Fatal("selection metadata alone is not an authentication gate", err)
	}
}

func physicalVariantFixture(t *testing.T) SchematicRenderInput {
	t.Helper()
	base := &SchematicLayoutResult{SchemaVersion: 1,
		ComponentIDs: map[string]string{"U1": "core", "R1": "passive"},
		PinStates:    map[string]map[string]string{"core": {}, "passive": {}},
		Placements: []SchematicPlacement{
			{Designator: "U1", BBox: SchematicBox{-10, -10, 10, 10}, TextBBoxes: []SchematicBox{{-10, 40, 10, 50}}, Pins: []SchematicPin{{Number: "1", Net: "N", X: 20}}},
			{Designator: "R1", X: 200, BBox: SchematicBox{190, -10, 210, 10}, TextBBoxes: []SchematicBox{{190, 40, 210, 50}}, Pins: []SchematicPin{{Number: "1", Net: "N", X: 180}}},
		},
		Wires: []SchematicWire{{Net: "N", Points: [][2]float64{{20, 0}, {180, 0}}}},
		Flags: []SchematicMarker{{Net: "N", Kind: "net_port_bi", PinX: 100, Direction: "up", Offset: 20}},
	}
	v := SchematicZoneVariant{ID: "direct", Layout: base}
	refreshTestVariantFrame(t, "zone", "Z", &v)
	in := SchematicRenderInput{SchemaVersion: 1, Zones: []SchematicRenderZone{{ID: "zone", Title: "Z", CoreComponentID: "core", Layout: base, Frame: &v.Frame, ContentBounds: &v.ContentBounds, Variants: []SchematicZoneVariant{v}}}}
	alt := cloneVariantInput(t, in).Zones[0].Variants[0]
	alt.ID, alt.Layout.Wires = "labels-only", nil
	alt.Layout.Flags = []SchematicMarker{
		{Net: "N", Kind: "net_port_bi", PinX: 20, Direction: "right", Offset: 20},
		{Net: "N", Kind: "net_port_bi", PinX: 180, Direction: "left", Offset: 20},
	}
	refreshTestVariantFrame(t, "zone", "Z", &alt)
	in.Zones[0].Variants = append(in.Zones[0].Variants, alt)
	return in
}

func TestZoneVariantsRejectSameNameLabelsReplacingDirectPhysicalIsland(t *testing.T) {
	in := physicalVariantFixture(t)
	baseline, split := in.Zones[0].Variants[0], in.Zones[0].Variants[1]
	for _, v := range []SchematicZoneVariant{baseline, split} {
		plain := SchematicRenderInput{SchemaVersion: 1, Zones: []SchematicRenderZone{{ID: "zone", Title: "Z", Layout: v.Layout, Frame: &v.Frame}}}
		if _, err := RenderSchematicLayoutSVG(plain); err != nil {
			t.Fatal("physical-connectivity fixture has invalid geometry", err)
		}
		if err := validateCompleteLayoutPreview(plain); err != nil {
			t.Fatal("each pin should reach a correctly named tree; this alone is insufficient", err)
		}
	}
	if err := validateSchematicVariantConnectivityPreserved(baseline.Layout, split.Layout); err == nil || !strings.Contains(err.Error(), "island was split") {
		t.Fatal("same-name labels replaced a physical direct connection", err)
	}
	if err := validateSchematicZoneVariants(in); err == nil || !strings.Contains(err.Error(), "island was split") {
		t.Fatal("unselected candidate bypassed physical preservation", err)
	}
	// Selecting the already-degraded candidate must not redefine the original
	// baseline: original topology remains variants[0], never selectedVariantId.
	z := &in.Zones[0]
	z.Layout, z.Frame, z.ContentBounds, z.SelectedVariantID = split.Layout, &split.Frame, &split.ContentBounds, split.ID
	if err := validateSchematicZoneVariants(in); err == nil || !strings.Contains(err.Error(), "island was split") {
		t.Fatal("selected candidate rewrote its physical baseline", err)
	}
	if err := validateSchematicVariantConnectivityPreserved(split.Layout, baseline.Layout); err != nil {
		t.Fatal("joining formerly separated same-net islands should be allowed", err)
	}
}

func TestVariantPhysicalIslandsIncludeFlagLeadsAndKeepStablePinOrder(t *testing.T) {
	in := physicalVariantFixture(t)
	baseline := in.Zones[0].Variants[0].Layout
	// A marker lead itself bridges two wire segments. Ignoring the lead would
	// incorrectly turn this into two islands despite real drawn connectivity.
	baseline.Wires = []SchematicWire{
		{Net: "N", Points: [][2]float64{{20, 0}, {80, 0}}},
		{Net: "N", Points: [][2]float64{{100, 0}, {180, 0}}},
	}
	baseline.Flags = []SchematicMarker{{Net: "N", Kind: "net_port_bi", PinX: 80, Direction: "right", Offset: 20}}
	islands, err := schematicVariantPhysicalIslands(baseline)
	if err != nil {
		t.Fatal(err)
	}
	if len(islands) != 1 || islands[0].Net != "N" || len(islands[0].Pins) != 2 || islands[0].Pins[0].Designator != "U1" || islands[0].Pins[1].Designator != "R1" {
		t.Fatalf("real flag lead or stable source order was lost: %+v", islands)
	}
	split := in.Zones[0].Variants[1].Layout
	if err := validateSchematicVariantConnectivityPreserved(baseline, split); err == nil {
		t.Fatal("did not preserve the connection completed by a real marker lead")
	}
	islands, err = schematicVariantPhysicalIslands(split)
	if err != nil || len(islands) != 2 {
		t.Fatal("same-name labels incorrectly merged physically separate islands", err)
	}
}
