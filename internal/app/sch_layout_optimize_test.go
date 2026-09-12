package app

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func schematicOptimizationFixture() SchematicLayoutInput {
	in := standaloneLayoutFixture()
	in.Optimization = &SchematicLayoutOptimization{}
	in.Components[1].Measurement = SchematicPlacement{Designator: "R1", X: 300, Y: 100,
		BBox: SchematicBox{295, 80, 305, 120}, Pins: []SchematicPin{{Number: "1", Net: "SUPPLY", X: 300, Y: 130}, {Number: "2", Net: "RETURN", X: 300, Y: 70}}}
	in.Components[1].AllowedRotations = []float64{0, 90, 180, 270}
	return in
}

func TestSchematicOptimizationPreservesBaselineAndRigidEvidence(t *testing.T) {
	in := schematicOptimizationFixture()
	before, _ := json.Marshal(in)
	plain := in
	plain.Optimization = nil
	baseline, err := PlanSchematicLayout(plain)
	if err != nil {
		t.Fatal(err)
	}
	out, err := PlanSchematicLayout(in)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(in)
	if !bytes.Equal(before, after) || len(out.Variants) < 2 || len(out.Variants) > 4 || out.Variants[0].ID != "baseline" {
		t.Fatalf("mutation or missing bounded variants: %#v", out)
	}
	first := out.Variants[0].Layout
	if !reflect.DeepEqual(first.Placements, baseline.Placements) || !reflect.DeepEqual(first.Wires, baseline.Wires) || !reflect.DeepEqual(first.Flags, baseline.Flags) {
		t.Fatal("baseline geometry was overwritten")
	}
	_, allowed, _ := schematicOptimizationSettings(in)
	if err := validateSchematicOptimizationEvidence(in, out, allowed); err != nil {
		t.Fatal(err)
	}
	rotated := false
	for _, variant := range out.Variants {
		if variant.Layout == nil || len(variant.Layout.Variants) != 0 || !reflect.DeepEqual(variant.Layout.AllowedRotations, out.AllowedRotations) {
			t.Fatal("variant is incomplete, recursive, or changed rotation proof")
		}
		if err := validateSchematicOptimizationEvidence(in, variant.Layout, allowed); err != nil {
			t.Fatal(err)
		}
		for _, p := range variant.Layout.Placements {
			rotated = rotated || p.Designator == "R1" && p.Rotation != 0
		}
	}
	if !rotated || schematicOptimizationDimensions(out)[0] >= schematicOptimizationDimensions(first)[0] {
		t.Fatalf("fixture did not exercise beneficial rotation: before=%v after=%v", schematicOptimizationDimensions(first), schematicOptimizationDimensions(out))
	}
	if out.CandidatesUsed > 20000 || out.CandidatesUsed <= baseline.CandidatesUsed {
		t.Fatal("optimization did not share the total budget")
	}
	again, err := PlanSchematicLayout(in)
	if err != nil || !reflect.DeepEqual(out, again) {
		t.Fatal("optimization is not deterministic", err)
	}
	first.AllowedRotations["peripheral"][0] = 123
	if out.AllowedRotations["peripheral"][0] == 123 || in.Components[1].AllowedRotations[0] == 123 {
		t.Fatal("variant proof aliases main or source")
	}
}

func TestSchematicOptimizationRejectsUnapprovedPoseOrLimits(t *testing.T) {
	for _, edit := range []func(*SchematicLayoutInput){
		func(in *SchematicLayoutInput) { in.Components[1].AllowedRotations = []float64{90} },
		func(in *SchematicLayoutInput) { in.Components[1].AllowedRotations = []float64{0, 45} },
		func(in *SchematicLayoutInput) { in.Components[1].AllowedRotations = []float64{0, 0} },
		func(in *SchematicLayoutInput) { in.Components[0].AllowedRotations = []float64{0, 90} },
		func(in *SchematicLayoutInput) { in.Optimization.MaxVariants = 5 },
		func(in *SchematicLayoutInput) { in.Optimization.MaxAttempts = 65 },
		func(in *SchematicLayoutInput) { in.Optimization.MaxAttempts = -1 },
	} {
		in := schematicOptimizationFixture()
		edit(&in)
		if out, err := PlanSchematicLayout(in); out != nil || err == nil {
			t.Fatal("invalid optimization accepted")
		}
	}
}

func TestSchematicOptimizationBudgetExhaustionRetainsBaseline(t *testing.T) {
	in := schematicOptimizationFixture()
	plain := in
	plain.Optimization = nil
	baseline, err := PlanSchematicLayout(plain)
	if err != nil {
		t.Fatal(err)
	}
	in.MaxCandidates = baseline.CandidatesUsed
	out, err := PlanSchematicLayout(in)
	if err != nil || len(out.Variants) != 1 || out.CandidatesUsed > in.MaxCandidates {
		t.Fatal("failed refinement discarded baseline", err)
	}
	if !reflect.DeepEqual(out.Placements, baseline.Placements) || !reflect.DeepEqual(out.Wires, baseline.Wires) {
		t.Fatal("exhaustion changed legal baseline")
	}
	in.MaxCandidates = 1
	if out, err := PlanSchematicLayout(in); out != nil || err == nil || !strings.Contains(err.Error(), "search") {
		t.Fatal("baseline failure must remain failure")
	}
}

func TestSchematicOptimizationMovesSuccessfulLockedLayoutInward(t *testing.T) {
	in := standaloneLayoutFixture()
	baseline, err := PlanSchematicLayout(in)
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately provide a complete, legal but roomy incumbent. No rotation
	// permissions are granted: an area gain here must be a real XY refinement.
	roomy := append([]SchematicPlacement(nil), baseline.Placements...)
	roomy[1] = plTranslate(roomy[1], 40, 0)
	budget := 20000
	finished, err := finishSchematicOptimizationPlacements(roomy, in.NetPolicies, &budget)
	if err != nil {
		t.Fatal(err)
	}
	finished.SchemaVersion, finished.ComponentIDs, finished.PinStates = 1, baseline.ComponentIDs, baseline.PinStates
	finished.CandidatesUsed = baseline.CandidatesUsed
	p := powerLayoutPlan{Placements: finished.Placements, Wires: finished.Wires, Flags: finished.Flags}
	finished.Score = libCandidateScore(&p)
	in.Optimization = &SchematicLayoutOptimization{}
	opts, allowed, err := schematicOptimizationSettings(in)
	if err != nil {
		t.Fatal(err)
	}
	measured, members := map[string]powerLayoutPlacement{}, []string{}
	for _, c := range in.Components {
		measured[c.ID], members = c.Measurement, append(members, c.ID)
	}
	out := optimizeSchematicLayout(in, finished, measured, members, nil, *opts, allowed, &budget)
	if out.OptimizationReport.AttemptsUsed == 0 || out.OptimizationReport.AcceptedCandidates == 0 || schematicOptimizationDimensions(out)[0] >= schematicOptimizationDimensions(finished)[0] {
		t.Fatalf("successful incumbent was not refined inward: before=%v after=%v report=%+v", schematicOptimizationDimensions(finished), schematicOptimizationDimensions(out), out.OptimizationReport)
	}
	if err := validateSchematicOptimizationEvidence(in, out, allowed); err != nil {
		t.Fatal(err)
	}
	for _, c := range out.Placements {
		if c.Rotation != 0 {
			t.Fatal("locked refinement rotated a component")
		}
	}
}

func TestSchematicOptimizationBaselineOnlyDoesNotSpendOrAlias(t *testing.T) {
	in := schematicOptimizationFixture()
	in.Optimization.MaxVariants = 1
	out, err := PlanSchematicLayout(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Variants) != 1 || out.OptimizationReport.AttemptsUsed != 0 || out.OptimizationReport.StopReason != "baseline-only" {
		t.Fatal("maxVariants=1 tried hidden alternatives")
	}
	base := out.Variants[0].Layout
	if out.CandidatesUsed != base.CandidatesUsed {
		t.Fatal("baseline-only spent refinement budget")
	}
	_, allowed, _ := schematicOptimizationSettings(in)
	for _, mutate := range []func(*SchematicLayoutResult){
		func(r *SchematicLayoutResult) { r.Placements[1].Mirror = !r.Placements[1].Mirror },
		func(r *SchematicLayoutResult) { r.Placements[1].Pins[0].Net = "FOREIGN" },
		func(r *SchematicLayoutResult) { r.Placements[1].BBox.MaxX += 5 },
		func(r *SchematicLayoutResult) { r.PinStates["anchor"]["3"] = "unconnected" },
		func(r *SchematicLayoutResult) { r.Placements[0] = plTranslate(r.Placements[0], 5, 0) },
	} {
		bad := cloneSchematicOptimizationResult(base)
		mutate(bad)
		if validateSchematicOptimizationEvidence(in, bad, allowed) == nil {
			t.Fatal("optimization proof accepted changed electrical or rigid evidence")
		}
	}
}

func TestSchematicOptimizationRebuildPreservesLongPhysicalRailIsland(t *testing.T) {
	baseline := physicalVariantFixture(t).Zones[0].Variants[0].Layout
	for _, policy := range []string{"local_power", "local_ground", "module_port", "direct"} {
		budget := 10000
		finished, err := finishSchematicOptimizationPlacements(baseline.Placements, map[string]string{"N": policy}, &budget, baseline)
		if err != nil {
			t.Fatalf("%s rebuild: %v", policy, err)
		}
		finished.ComponentIDs = baseline.ComponentIDs
		if err := validateSchematicVariantConnectivityPreserved(baseline, finished); err != nil {
			t.Fatalf("%s lost required direct island: %v", policy, err)
		}
		if len(finished.Wires) == 0 || budget >= 10000 {
			t.Fatal("mandatory reroute was omitted or not budgeted")
		}
	}
	// Negative control: the optional rail joiner intentionally ignores pairs
	// farther than 80 raw; complete naming alone silently accepts two labels.
	budget := 10000
	optional, err := finishSchematicOptimizationPlacements(baseline.Placements, map[string]string{"N": "local_power"}, &budget)
	if err != nil {
		t.Fatal(err)
	}
	optional.ComponentIDs = baseline.ComponentIDs
	if validateSchematicVariantConnectivityPreserved(baseline, optional) == nil {
		t.Fatal("negative control did not expose label-only rail degradation")
	}
}

func TestSchematicOptimizationProposalQuotaScalesButPreservesReserve(t *testing.T) {
	for _, test := range []struct {
		baselineCost, remaining, reserved, want int
	}{
		{9000, 30000, 5000, 18000}, // A demonstrated hard zone must not hit a tiny fixed cap.
		{9000, 16000, 4000, 12000}, // Even one expensive proposal cannot consume the reserve.
		{10, 30000, 5000, 512},
		{9000, 4000, 4000, 0},
		{9000, 3000, 4000, 0},
		{9000, 1, 0, 1},
	} {
		if got := schematicOptimizationProposalQuota(test.baselineCost, test.remaining, test.reserved); got != test.want {
			t.Fatalf("quota(%d,%d,%d)=%d, want %d", test.baselineCost, test.remaining, test.reserved, got, test.want)
		}
	}
}
