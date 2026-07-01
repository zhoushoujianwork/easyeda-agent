package app

import (
	"math"
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
							"form": map[string]any{
								"data": map[string]any{
									"1": map[string]any{"defaultValue": 0.254, "minValue": 0.127, "maxValue": 2.54},
								},
							},
						},
					},
					"Via Size": map[string]any{
						"viaSize": map[string]any{
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
							"row": []any{"Track", "SMD Pad", "Copper/Plane Zone", "Board Outline"},
							"tables": map[string]any{
								"1": map[string]any{
									"content": []any{
										[]any{0.10199878},                     // Track↔Track (4mil)
										[]any{0.15200122, 0.15200122},         // SMD Pad↔Track (6mil), ↔Pad
										[]any{0.254, 0.254, 0.254},            // Copper/Plane Zone
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
		t.Errorf("trackWidth=%.2f, want ~10mil", r.trackWidthMil)
	}
	if !near(r.trackWidthMinMil, 5) {
		t.Errorf("trackWidthMin=%.2f, want ~5mil", r.trackWidthMinMil)
	}
	if !near(r.clearanceMil, 6) {
		t.Errorf("clearance=%.2f, want ~6mil (track-to-pad, the binding rule)", r.clearanceMil)
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

// A missing/garbage result falls back to the JLCPCB baseline, not zeros.
func TestParsePcbRules_Fallback(t *testing.T) {
	r := parsePcbRules(map[string]any{"nope": true})
	if r.source != "fallback" {
		t.Errorf("source=%q, want fallback", r.source)
	}
	d := defaultPcbRules()
	if r.clearanceMil != d.clearanceMil || r.trackWidthMil != d.trackWidthMil {
		t.Errorf("fallback mismatch: %+v vs %+v", r, d)
	}
}
