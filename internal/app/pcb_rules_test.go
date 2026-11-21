package app

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The rule structure mirrors a real live ceshi dump (2026-07-01): mm values under
// deeply-nested, space-laden keys. parsePcbRules must convert them to mil.
func ceshiRuleResult() map[string]any {
	return map[string]any{
		"rules": map[string]any{
			"config": map[string]any{
				"Physics": map[string]any{
					"Track": map[string]any{
						"copperThickness1oz": map[string]any{
							"unit": "mm",
							"form": map[string]any{
								"data": map[string]any{
									"1": map[string]any{"defaultValue": 0.254, "minValue": 0.127, "maxValue": 2.54},
								},
							},
						},
					},
					"Via Size": map[string]any{
						"viaSize": map[string]any{
							"unit": "mm",
							"form": map[string]any{
								"viaInnerdiameterDefault": 0.30499812,
								"viaOuterdiameterDefault": 0.61000132,
							},
						},
					},
				},
				"Spacing": map[string]any{
					"Safe Spacing": map[string]any{
						"copperThickness1oz": map[string]any{
							"unit": "mm",
							"row":  []any{"Track", "SMD Pad", "Copper/Plane Zone", "Board Outline"},
							"tables": map[string]any{
								"1": map[string]any{
									"content": []any{
										[]any{0.10199878},                       // Track↔Track (4mil)
										[]any{0.15200122, 0.15200122},           // SMD Pad↔Track (6mil), ↔Pad
										[]any{0.254, 0.254, 0.254},              // Copper/Plane Zone
										[]any{0.29972, 0.29972, 0.254, 0.29972}, // Board Outline ↔ … ↔ CopperZone=0.254 (10mil)
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 0.05 }

func TestParsePcbRules_Live(t *testing.T) {
	r, err := parsePcbRules(ceshiRuleResult())
	if err != nil {
		t.Fatal(err)
	}
	if r.source != "live" {
		t.Errorf("source=%q, want live", r.source)
	}
	if !near(r.trackWidthMil, 10) {
		t.Errorf("trackWidth(signal)=%.2f, want ~10mil", r.trackWidthMil)
	}
	if !near(r.powerWidthMil, 20) {
		t.Errorf("powerWidth=%.2f, want ~20mil (wider than signal)", r.powerWidthMil)
	}
	if !near(r.trackWidthMinMil, 5) {
		t.Errorf("trackWidthMin=%.2f, want ~5mil", r.trackWidthMinMil)
	}
	if !near(r.clearanceMil, 6) {
		t.Errorf("clearance=%.2f, want ~6mil (track-to-pad, the binding rule)", r.clearanceMil)
	}
	if !near(r.clearanceTrackTrackMil, 4) {
		t.Errorf("track-to-track clearance=%.2f, want ~4mil (pair-specific live rule)", r.clearanceTrackTrackMil)
	}
	if !near(r.viaDrillMil, 12) {
		t.Errorf("viaDrill=%.2f, want ~12mil", r.viaDrillMil)
	}
	if !near(r.viaDiameterMil, 24) {
		t.Errorf("viaDiameter=%.2f, want ~24mil", r.viaDiameterMil)
	}
	// #309: native DRC judges routed copper by Board Outline↔Track (0.29972mm),
	// not by the zone cell that used to be reported here.
	if !near(r.copperToEdgeMil, 11.8) {
		t.Errorf("copperToEdge=%.2f, want ~11.8mil (Board Outline↔Track)", r.copperToEdgeMil)
	}
	if !near(r.zoneToEdgeMil, 10) {
		t.Errorf("zoneToEdge=%.2f, want ~10mil (Board Outline↔Copper/Plane Zone)", r.zoneToEdgeMil)
	}
	if r.holeToTrackMil != 0 || r.slotToTrackMil != 0 {
		t.Errorf("hole/slot rules absent from this matrix must stay unmeasured: %+v", r)
	}
}

// A missing/garbage result falls back to the JLCPCB baseline, not zeros.
func TestParsePcbRules_Fallback(t *testing.T) {
	r, err := parsePcbRules(map[string]any{"nope": true})
	if err != nil {
		t.Fatal(err)
	}
	if r.source != "fallback" {
		t.Errorf("source=%q, want fallback", r.source)
	}
	d := defaultPcbRules()
	if r.clearanceMil != d.clearanceMil || r.clearanceTrackTrackMil != d.clearanceTrackTrackMil || r.trackWidthMil != d.trackWidthMil {
		t.Errorf("fallback mismatch: %+v vs %+v", r, d)
	}
}

// Synthetic unit variants of the existing mm fixture; not Windows/live evidence.
func ruleUnitFixture(track, via, spacing string) map[string]any {
	result := ceshiRuleResult()
	for _, node := range []struct {
		path []string
		unit string
	}{
		{[]string{"Physics", "Track", "copperThickness1oz"}, track},
		{[]string{"Physics", "Via Size", "viaSize"}, via},
		{[]string{"Spacing", "Safe Spacing", "copperThickness1oz"}, spacing},
	} {
		rule := mnav(mnav(result, "rules", "config"), node.path...).(map[string]any)
		rule["unit"] = node.unit
		if node.unit == "mil" {
			scaleRuleLengths(rule)
		}
	}
	return result
}
func scaleRuleLengths(v any) {
	switch x := v.(type) {
	case map[string]any:
		for k, v := range x {
			if n, ok := v.(float64); ok {
				x[k] = n / 0.0254
			} else {
				scaleRuleLengths(v)
			}
		}
	case []any:
		for i, v := range x {
			if n, ok := v.(float64); ok {
				x[i] = n / 0.0254
			} else {
				scaleRuleLengths(v)
			}
		}
	}
}
func TestParsePcbRules_UnitEquivalence(t *testing.T) {
	want, err := parsePcbRules(ruleUnitFixture("mm", "mm", "mm"))
	if err != nil {
		t.Fatal(err)
	}
	for _, units := range [][3]string{{"mil", "mil", "mil"}, {"mm", "mil", "mm"}, {"mil", "mm", "mil"}} {
		got, err := parsePcbRules(ruleUnitFixture(units[0], units[1], units[2]))
		if err != nil {
			t.Fatal(err)
		}
		got.spacingSource = want.spacingSource // names the declared unit, by design
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("units=%v: got %+v want %+v", units, got, want)
		}
	}
}

func TestParsePcbRules_InvalidUnits(t *testing.T) {
	for _, path := range [][]string{{"Physics", "Track", "copperThickness1oz"}, {"Physics", "Via Size", "viaSize"}, {"Spacing", "Safe Spacing", "copperThickness1oz"}} {
		for _, bad := range []any{nil, "", "um", 42, []any{"mm"}, map[string]any{"unit": "mil"}} {
			result := ceshiRuleResult()
			node := mnav(mnav(result, "rules", "config"), path...).(map[string]any)
			node["unit"] = bad
			if _, err := parsePcbRules(result); err == nil || !strings.Contains(err.Error(), strings.Join(path, ".")+".unit") {
				t.Fatalf("path=%v unit=%v err=%v", path, bad, err)
			}
		}
		result := ceshiRuleResult()
		delete(mnav(mnav(result, "rules", "config"), path...).(map[string]any), "unit")
		if _, err := parsePcbRules(result); err == nil {
			t.Fatalf("missing unit at %v accepted", path)
		}
	}
}
func TestParsePcbRules_MissingFieldsRetainFallback(t *testing.T) {
	result := map[string]any{"rules": map[string]any{"config": map[string]any{"Physics": map[string]any{"Track": map[string]any{"copperThickness1oz": map[string]any{"unit": "unknown"}}}}}}
	got, err := parsePcbRules(result)
	if err != nil || !reflect.DeepEqual(got, defaultPcbRules()) {
		t.Fatalf("got %+v err=%v", got, err)
	}
}
func TestParsePcbRules_ExactMilAndClamp(t *testing.T) {
	result := ruleUnitFixture("mil", "mil", "mil")
	cfg := mnav(result, "rules", "config")
	d := firstDataEntry(mnav(cfg, "Physics", "Track", "copperThickness1oz", "form", "data"))
	d["defaultValue"] = 10.0
	d["minValue"] = 5.0
	via := mnav(cfg, "Physics", "Via Size", "viaSize", "form").(map[string]any)
	via["viaInnerdiameterDefault"] = 12.0
	via["viaOuterdiameterDefault"] = 24.0
	rows := mnav(cfg, "Spacing", "Safe Spacing", "copperThickness1oz", "tables", "1", "content").([]any)
	rows[0].([]any)[0] = 4.0
	rows[1].([]any)[0] = 6.0
	rows[3].([]any)[0] = 12.0
	rows[3].([]any)[2] = 10.0
	got, err := parsePcbRules(result)
	if err != nil {
		t.Fatal(err)
	}
	if got.spacingMil["Board Outline↔Track"] != 12 || got.spacingMil["Track↔Board Outline"] != 12 || got.spacingMil["Board Outline↔Copper/Plane Zone"] != 10 {
		t.Fatalf("spacing cells = %v", got.spacingMil)
	}
	want := defaultPcbRules()
	want.source = "live"
	want.copperToEdgeMil, want.zoneToEdgeMil = 12, 10
	want.spacingMil = got.spacingMil
	want.spacingSource = pcbSafeSpacingPath + " (unit mil)"
	// Every field came from the board rule, so each is recorded as measured.
	want.measured = []string{pcbRuleFieldTrackWidth, pcbRuleFieldTrackWidthMin, pcbRuleFieldViaDrill, pcbRuleFieldViaDiameter, pcbRuleFieldClearanceTrackTrack, pcbRuleFieldClearance, pcbRuleFieldZoneToEdge, pcbRuleFieldCopperToEdge}
	if !reflect.DeepEqual(got, want) || got.clampWidth(2) != 5 || got.clampWidth(10) != 10 {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestParsePcbRules_PartialNumericNodesRequireUnits(t *testing.T) {
	for _, tc := range []struct {
		path []string
		node map[string]any
	}{
		{[]string{"Physics", "Track", "copperThickness1oz"}, map[string]any{"form": map[string]any{"data": []any{map[string]any{"minValue": 5.0}}}}},
		{[]string{"Physics", "Track", "copperThickness1oz"}, map[string]any{"form": map[string]any{"data": []any{map[string]any{"maxValue": 20.0}}}}},
		{[]string{"Physics", "Via Size", "viaSize"}, map[string]any{"form": map[string]any{"viaOuterdiameterDefault": 24.0}}},
		{[]string{"Spacing", "Safe Spacing", "copperThickness1oz"}, map[string]any{"row": []any{"Copper/Plane Zone", "Board Outline"}, "tables": map[string]any{"1": map[string]any{"content": []any{[]any{}, []any{10.0}}}}}},
	} {
		result := ceshiRuleResult()
		cfg := mnav(result, "rules", "config")
		parent := mnav(cfg, tc.path[:len(tc.path)-1]...).(map[string]any)
		parent[tc.path[len(tc.path)-1]] = tc.node
		if _, err := parsePcbRules(result); err == nil {
			t.Fatalf("partial numeric node without unit accepted: %v", tc.path)
		}
	}
}

// #309 full JLCEDA Web 3.2.203 rule export (12×13 Safe Spacing matrix). Native
// DRC judged a track at 11.8mil from the outline while the dump said 10mil.
func TestParsePcbRules_FullMatrixObjectPairs(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "extension", "src", "testdata", "pcb-config-web-3.2.203.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file map[string]any
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	r, err := parsePcbRules(map[string]any{"rules": map[string]any{"config": file["ruleConfiguration"]}})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name      string
		got, want float64
	}{
		{"copperToEdge (Track/Via↔Board Outline 0.29972mm)", r.copperToEdgeMil, 11.8},
		{"zoneToEdge (Copper/Plane Zone↔Board Outline 0.254mm)", r.zoneToEdgeMil, 10},
		{"holeToTrack (Hole↔Track 0.1759966mm)", r.holeToTrackMil, 6.93},
		{"slotToTrack (Slot Region↔Track 0.2999994mm)", r.slotToTrackMil, 11.81},
		{"Via↔Board Outline cell", r.spacingMil["Via↔Board Outline"], 11.8},
	} {
		if math.Abs(c.got-c.want) > 0.01 {
			t.Errorf("%s = %.3f mil, want %.2f", c.name, c.got, c.want)
		}
	}
	if r.spacingSource != pcbSafeSpacingPath+" (unit mm)" {
		t.Errorf("spacingSource = %q", r.spacingSource)
	}
	b := rulesToBoard(r)
	if back := b.toPcbRules(); back.zoneToEdgeMil != r.zoneToEdgeMil || back.holeToTrackMil != r.holeToTrackMil || back.copperToEdgeMil != r.copperToEdgeMil {
		t.Errorf("snapshot round trip lost rule cells: %+v", back)
	}
	// Snapshots that predate per-pair cells keep their recorded value for pours.
	if old := (&boardRules{CopperToEdgeMil: 10, Source: "live"}).toPcbRules(); old.zoneToEdgeMil != 10 || old.holeToTrackMil != 0 {
		t.Errorf("legacy snapshot rules = %+v", old)
	}
}
