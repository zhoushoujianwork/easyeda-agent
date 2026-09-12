package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func zonesFixture() SchematicZonesInput {
	a, b := standaloneLayoutFixture(), standaloneLayoutFixture()
	b.Components[0].ID = "second-core"
	b.Components[0].Measurement.Designator = "U2"
	b.Components[1].ID = "second-passive"
	b.Components[1].Measurement.Designator = "R2"
	return SchematicZonesInput{SchemaVersion: 1, Components: append(a.Components, b.Components...), NetPolicies: a.NetPolicies, Zones: []SchematicZone{
		{ID: "first", Title: "First", CoreComponentID: "anchor", ComponentIDs: []string{"anchor", "peripheral"}},
		{ID: "second", Title: "Second", CoreComponentID: "second-core", ComponentIDs: []string{"second-core", "second-passive"}},
	}}
}

func TestSchematicZonesIndependentAndImmutable(t *testing.T) {
	in := zonesFixture()
	before, _ := json.Marshal(in)
	out, err := PlanSchematicZones(in)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(in)
	if !bytes.Equal(before, after) {
		t.Fatal("mutated evidence")
	}
	if len(out.Zones) != 2 {
		t.Fatal("merged cores through common rails")
	}
	for _, z := range out.Zones {
		if z.Frame.ID != z.ID || z.Frame.LineType != 1 || z.Frame.FontSize != 20 || !boxInside(z.ContentBounds, z.Frame.Rect) {
			t.Fatal("missing independent frame")
		}
		if err := checkSchFrameTitleOccupancy(z.Frame, z.Frame.titleBounds()); err != nil {
			t.Fatal(err)
		}
		if len(z.Layout.Placements) != 2 || z.Layout.Placements[0].X != 0 || z.Layout.Placements[0].Y != 0 || !plBoxValid(z.ContentBounds) {
			t.Fatal("bad independent layout")
		}
		for _, c := range z.Layout.Placements {
			if c.BBox.MinX < z.ContentBounds.MinX || c.BBox.MaxX > z.ContentBounds.MaxX {
				t.Fatal("bounds lost content")
			}
		}
	}
	again, err := PlanSchematicZones(in)
	if err != nil || !reflect.DeepEqual(out, again) {
		t.Fatal("nondeterministic")
	}
	out.Zones[0].Layout.PinStates["anchor"]["3"] = "unconnected"
	if in.Components[0].PinStates["3"] != "nc" {
		t.Fatal("aliased pin states")
	}
}

func TestSchematicZonesRejectOwnershipAndBudget(t *testing.T) {
	for _, edit := range []func(*SchematicZonesInput){
		func(in *SchematicZonesInput) {
			in.Zones[1].ComponentIDs = append(in.Zones[1].ComponentIDs, "peripheral")
		},
		func(in *SchematicZonesInput) { in.Zones[0].ComponentIDs = []string{"anchor"} },
		func(in *SchematicZonesInput) { in.Zones[0].CoreComponentID = "second-core" },
		func(in *SchematicZonesInput) { in.NetPolicies["SUPPLY"] = "direct" },
		func(in *SchematicZonesInput) {
			in.Attachments = []SchematicLayoutPeripheral{{ComponentID: "peripheral", AttachTo: &SchematicLayoutAttach{ComponentID: "second-core", PinNumber: "1"}}}
		},
		func(in *SchematicZonesInput) { in.MaxCandidates = 1 },
	} {
		in := zonesFixture()
		edit(&in)
		if out, err := PlanSchematicZones(in); err == nil || out != nil {
			t.Fatal("accepted invalid zone data")
		}
	}
}

func TestLayoutZonesCLIAndMissingGeometry(t *testing.T) {
	raw, _ := json.Marshal(zonesFixture())
	if _, err := decodeSchematicZonesInput(raw); err != nil {
		t.Fatal(err)
	}
	bad := strings.Replace(string(raw), `"mirror":false,`, "", 1)
	if _, err := decodeSchematicZonesInput([]byte(bad)); err == nil {
		t.Fatal("invented missing mirror")
	}
	var buf bytes.Buffer
	c := newSchLayoutPlanCmd(&buf)
	if c.Flags().Lookup("zones") == nil {
		t.Fatal("missing CLI switch")
	}
	dir := t.TempDir()
	from, out := filepath.Join(dir, "in.json"), filepath.Join(dir, "out.json")
	if err := os.WriteFile(from, raw, 0600); err != nil {
		t.Fatal(err)
	}
	c.SetArgs([]string{"--zones", "--from", from, "--out", out})
	if err := c.Execute(); err != nil {
		t.Fatal(err)
	}
	good, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var result SchematicZonesResult
	if err := json.Unmarshal(good, &result); err != nil || len(result.Zones) != 2 {
		t.Fatal("invalid CLI zones result", err)
	}
	if err := os.WriteFile(from, []byte(bad), 0600); err != nil {
		t.Fatal(err)
	}
	c = newSchLayoutPlanCmd(&buf)
	c.SetArgs([]string{"--zones", "--from", from, "--out", out})
	if err := c.Execute(); err == nil {
		t.Fatal("accepted incomplete CLI evidence")
	}
	actual, _ := os.ReadFile(out)
	if !bytes.Equal(good, actual) {
		t.Fatal("failed run overwrote good output")
	}
}

func TestSchematicZonesShareBudget(t *testing.T) {
	in := zonesFixture()
	out, err := PlanSchematicZones(in)
	if err != nil {
		t.Fatal(err)
	}
	in.MaxCandidates = out.Zones[0].Layout.CandidatesUsed
	if _, err = PlanSchematicZones(in); err == nil || !strings.Contains(err.Error(), "budget exhausted") {
		t.Fatal("budget reset per zone", err)
	}
}

func TestSchematicZonesOptimizationIsolatedWithCompleteVariantBridge(t *testing.T) {
	in := zonesFixture()
	in.MaxCandidates = 100000
	in.Optimization = &SchematicLayoutOptimization{MaxVariants: 4, MaxAttempts: 24}
	out, err := PlanSchematicZones(in)
	if err != nil {
		t.Fatal(err)
	}
	// No spacing supplied: optimization itself must isolate local budgets.
	in.Components[1].AllowedRotations = []float64{0, 90, 180, 270}
	changed, err := PlanSchematicZones(in)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out.Zones[1], changed.Zones[1]) {
		t.Fatal("first-zone optimization changed second-zone geometry or budget")
	}
	total := 0
	for _, z := range changed.Zones {
		total += z.Layout.CandidatesUsed
		if z.Layout.OptimizationReport == nil || len(z.Layout.Variants) != 0 || len(z.Variants) == 0 || len(z.Variants) > 4 || z.Variants[0].ID != "baseline" || z.SelectedVariantID == "" {
			t.Fatal("zone adapter lost report, selection, baseline or candidate boundary")
		}
	}
	if total != changed.CandidatesUsed {
		t.Fatal("zone totals lost optimization cost")
	}
	raw, _ := json.Marshal(changed)
	if err := validateRenderMeasurementsJSON(raw); err != nil {
		t.Fatal("generated variants rejected by raw CLI guard", err)
	}
	var render SchematicRenderInput
	if err := json.Unmarshal(raw, &render); err != nil {
		t.Fatal(err)
	}
	if err := validateSchematicZoneVariants(render); err != nil {
		t.Fatal(err)
	}
	if _, err := RenderSchematicLayoutSVG(render); err != nil {
		t.Fatal(err)
	}
}

func TestDirectDetourAvoidsInterveningForeignPin(t *testing.T) {
	p := powerLayoutPlan{Placements: []powerLayoutPlacement{{Designator: "U1", BBox: layoutBBox{-20, -20, 0, 40}, Pins: []powerLayoutPin{
		{Number: "1", Net: "SIGNAL", X: 10, Y: 30}, {Number: "2", Net: "FOREIGN", X: 10, Y: 20}, {Number: "3", Net: "SIGNAL", X: 10, Y: 10},
	}}}}
	a, b := p.Placements[0].Pins[0], p.Placements[0].Pins[2]
	straight := p
	straight.Wires = libRoutes(a, b)[0]
	if validateLibGeometry(&straight) == nil {
		t.Fatal("foreign pin not blocked")
	}
	if err := libJoinDirectNets(&p, map[string]string{"SIGNAL": "direct", "FOREIGN": "module_port"}); err != nil {
		t.Fatal(err)
	}
	if err := validateLibGeometry(&p); err != nil {
		t.Fatal(err)
	}
	if len(p.Wires) != 3 {
		t.Fatalf("expected two bends, got %d segments", len(p.Wires))
	}
	if len(libIslands(&p)) != 2 {
		t.Fatal("lost connectivity/isolation")
	}
	for _, w := range p.Wires {
		if w.Points[0] == w.Points[1] {
			t.Fatal("zero-length segment")
		}
	}
}

func TestAttachmentAlignmentUsesPinsNotAnchors(t *testing.T) {
	pair := libAttachmentPair{host: powerLayoutPin{X: 0, Y: 0}, own: powerLayoutPin{Number: "1"}, side: "right"}
	a := powerLayoutPlan{Placements: []powerLayoutPlacement{{X: 50, Y: 100, Pins: []powerLayoutPin{{Number: "1", X: 20, Y: 0}}}}}
	b := powerLayoutPlan{Placements: []powerLayoutPlacement{{X: 50, Y: 0, Pins: []powerLayoutPin{{Number: "1", X: 20, Y: 10}}}}}
	if !libAlignedCandidateLess(&a, &b, pair) || libAlignedCandidateLess(&b, &a, pair) {
		t.Fatal("compared anchors rather than pin axis")
	}
}
