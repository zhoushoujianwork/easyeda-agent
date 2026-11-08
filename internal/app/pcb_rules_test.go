package app

import (
	"math"
	"testing"
)

// The rule structure mirrors a real live ceshi dump (2026-07-01): mm values under
// deeply-nested, space-laden keys. parsePcbRules must convert them to mil using
// each rule's declared unit.
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
	r := parsePcbRules(ceshiRuleResult())
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
	if !near(r.copperToEdgeMil, 10) {
		t.Errorf("copperToEdge=%.2f, want ~10mil (BoardOutline↔Copper/Plane Zone)", r.copperToEdgeMil)
	}
}

// #303: EasyEDA Pro also returns rule objects whose values are already mil
// (`unit:"mil"`). The parser must not multiply them by mmToMil — 6mil became
// 236.22mil, 10mil became 393.7mil, and route-short/pcb check reported the whole
// board as violating a 236mil spacing rule.
func milRuleResult() map[string]any {
	return map[string]any{
		"rules": map[string]any{
			"config": map[string]any{
				"Physics": map[string]any{
					"Track": map[string]any{
						"copperThickness1oz": map[string]any{
							"unit": "mil",
							"form": map[string]any{
								"data": map[string]any{
									"1": map[string]any{"defaultValue": 10.0, "minValue": 5.0, "maxValue": 20.0},
								},
							},
						},
					},
					"Via Size": map[string]any{
						"viaSize": map[string]any{
							"unit": "mil",
							"form": map[string]any{
								"viaInnerdiameterDefault": 12.0,
								"viaOuterdiameterDefault": 24.0,
							},
						},
					},
				},
				"Spacing": map[string]any{
					"Safe Spacing": map[string]any{
						"copperThickness1oz": map[string]any{
							"unit": "mil",
							"row":  []any{"Track", "SMD Pad", "Copper/Plane Zone", "Board Outline"},
							"tables": map[string]any{
								"1": map[string]any{
									"content": []any{
										[]any{4.0},                    // Track↔Track 4mil
										[]any{6.0, 6.0},               // Track↔SMD Pad 6mil
										[]any{10.0, 10.0, 10.0},       // Copper/Plane Zone
										[]any{10.0, 10.0, 10.0, 10.0}, // Board Outline ↔ CopperZone 10mil
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

func TestParsePcbRules_MilUnitIsNotScaled(t *testing.T) {
	r := parsePcbRules(milRuleResult())
	if r.source != "live" {
		t.Fatalf("source=%q, want live", r.source)
	}
	for _, tc := range []struct {
		name string
		got  float64
		want float64
	}{
		{"clearance", r.clearanceMil, 6},
		{"track-to-track", r.clearanceTrackTrackMil, 4},
		{"trackWidth", r.trackWidthMil, 10},
		{"trackWidthMin", r.trackWidthMinMil, 5},
		{"viaDrill", r.viaDrillMil, 12},
		{"viaDiameter", r.viaDiameterMil, 24},
		{"copperToEdge", r.copperToEdgeMil, 10},
	} {
		if !near(tc.got, tc.want) {
			t.Errorf("%s=%.2f, want ~%.2fmil (mil-unit rules must pass through unchanged)", tc.name, tc.got, tc.want)
		}
	}
	if !near(r.powerWidthMil, 20) {
		t.Errorf("powerWidth=%.2f, want ~20mil (design convention above the signal width)", r.powerWidthMil)
	}
}

// The unit is per rule object, not per board: a metric track rule and an imperial
// spacing rule in the same configuration must each convert on their own scale.
func TestParsePcbRules_PerRuleUnit(t *testing.T) {
	res := ceshiRuleResult()
	spacing := mnav(res, "rules", "config", "Spacing", "Safe Spacing", "copperThickness1oz").(map[string]any)
	spacing["unit"] = "mil"
	content := spacing["tables"].(map[string]any)["1"].(map[string]any)["content"].([]any)
	content[0].([]any)[0] = 4.0
	content[1].([]any)[0] = 6.0
	content[1].([]any)[1] = 6.0
	content[3].([]any)[2] = 10.0

	r := parsePcbRules(res)
	if !near(r.trackWidthMil, 10) || !near(r.trackWidthMinMil, 5) {
		t.Errorf("mm track rule = %.2f/%.2f, want ~10/5mil", r.trackWidthMil, r.trackWidthMinMil)
	}
	if !near(r.clearanceMil, 6) || !near(r.clearanceTrackTrackMil, 4) || !near(r.copperToEdgeMil, 10) {
		t.Errorf("mil spacing rule = %.2f/%.2f/%.2f, want ~6/4/10mil",
			r.clearanceMil, r.clearanceTrackTrackMil, r.copperToEdgeMil)
	}
}

// An unknown unit must leave the fallback baseline intact rather than guessing a
// scale. This is the failure mode behind #303: silently assuming mm.
func TestParsePcbRules_UnknownUnitFallsBack(t *testing.T) {
	res := milRuleResult()
	cfg := mnav(res, "rules", "config")
	for _, rule := range []any{
		mnav(cfg, "Physics", "Track", "copperThickness1oz"),
		mnav(cfg, "Physics", "Via Size", "viaSize"),
		mnav(cfg, "Spacing", "Safe Spacing", "copperThickness1oz"),
	} {
		rule.(map[string]any)["unit"] = "inch"
	}
	r := parsePcbRules(res)
	if r.source != "fallback" {
		t.Errorf("source=%q, want fallback when no rule declares a known unit", r.source)
	}
	d := defaultPcbRules()
	if r.clearanceMil != d.clearanceMil || r.trackWidthMil != d.trackWidthMil || r.viaDrillMil != d.viaDrillMil {
		t.Errorf("unknown-unit fields should keep fallback defaults: %+v vs %+v", r, d)
	}
}

// A missing/garbage result falls back to the JLCPCB baseline, not zeros.
func TestParsePcbRules_Fallback(t *testing.T) {
	r := parsePcbRules(map[string]any{"nope": true})
	if r.source != "fallback" {
		t.Errorf("source=%q, want fallback", r.source)
	}
	d := defaultPcbRules()
	if r.clearanceMil != d.clearanceMil || r.clearanceTrackTrackMil != d.clearanceTrackTrackMil || r.trackWidthMil != d.trackWidthMil {
		t.Errorf("fallback mismatch: %+v vs %+v", r, d)
	}
}
