package app

import "fmt"

// pcb_rules.go — read the board's LIVE DRC rules (clearance / track width / via)
// and normalize them to mil, so the daemon-side heuristics (route-short,
// auto-place, pour, via-stitch) conform to the board's spec instead of hardcoding
// widths/spacing. The rule object from `pcb.drc.rules`
// (eda.pcb_Drc.getCurrentRuleConfiguration) is deeply nested and untyped; the key
// paths below were discovered from a live ceshi dump (2026-07-01). Every field
// falls back independently to a JLCPCB 2-layer baseline (which matches ceshi's own
// live rules), so a missing/renamed path retains that legacy fallback. Present
// numeric rules require explicit units; ambiguous units fail instead of falling back.

const mmToMil = 39.37007874

// pcbRules is the normalized rule set (all values in mil) the planners consume.
type pcbRules struct {
	clearanceMil           float64 // track↔pad/via safe spacing (the binding routing clearance)
	clearanceTrackTrackMil float64 // track↔track safe spacing; must retain the matrix's object-pair dimension
	trackWidthMil          float64 // default routing track width — used for SIGNAL nets
	powerWidthMil          float64 // design width for POWER/GND nets (wider, for current)
	trackWidthMinMil       float64 // minimum legal track width (clamp floor)
	viaDrillMil            float64 // via hole diameter
	viaDiameterMil         float64 // via outer diameter
	copperToEdgeMil        float64 // copper/plane-zone → board-outline clearance (pour inset floor)
	source                 string  // "live" | "fallback"
}

// defaultPcbRules is the daemon's fallback baseline — a sane seed when the live
// rule is missing or a path can't be read. Values are the DOUBLE-LAYER row of the
// canonical reference .agents/skills/easyeda-agent/references/fab-rules-jlcpcb.json
// ("boardTypeRulesLive", a real JLCEDA export): clear 6 (track↔pad) / signal 10 /
// min 5 / via 0.3mm(12mil)/0.6mm(24mil) / copper-to-edge 10 — the real 2-layer
// default, verified to match ceshi's live rule. NOTE: power is DELIBERATELY wider
// than signal (current capacity) — the board rule only gives a uniform default +
// a minimum; the signal/power split is a design convention (fab reference), not a
// manufacturing rule.
func defaultPcbRules() pcbRules {
	return pcbRules{
		clearanceMil: 6, clearanceTrackTrackMil: 4,
		trackWidthMil: 10, powerWidthMil: 20, trackWidthMinMil: 5,
		viaDrillMil: 12, viaDiameterMil: 24, copperToEdgeMil: 10, source: "fallback",
	}
}

// clampWidth floors a width at the rule's minimum legal track width.
func (r pcbRules) clampWidth(w float64) float64 {
	if w < r.trackWidthMinMil {
		return r.trackWidthMinMil
	}
	return w
}

// fetchPcbRules reads the board's live DRC rules via pcb.drc.rules and normalizes
// them; transport errors retain the legacy fallback. Explicit unit errors propagate.
func fetchPcbRules(cfg *appConfig, window string) (pcbRules, error) {
	res, err := requestAction(cfg, "pcb.drc.rules", window, nil)
	if err != nil || res == nil {
		return defaultPcbRules(), nil
	}
	return parsePcbRules(res.Result)
}

// mnav walks nested map[string]any by string keys; returns nil on any miss.
func mnav(v any, keys ...string) any {
	for _, k := range keys {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[k]
	}
	return v
}

// asStrSlice coerces a []any of strings to []string (non-strings → "").
func asStrSlice(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, len(arr))
	for i, e := range arr {
		if s, ok := e.(string); ok {
			out[i] = s
		}
	}
	return out
}

func indexOf(ss []string, target string) int {
	for i, s := range ss {
		if s == target {
			return i
		}
	}
	return -1
}

func asFloatOK(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	default:
		return 0, false
	}
}

// firstDataEntry pulls the first record out of a `data` node that may be a
// map keyed "1"/"2"/… or a JSON array.
func firstDataEntry(v any) map[string]any {
	switch d := v.(type) {
	case map[string]any:
		if e, ok := d["1"].(map[string]any); ok {
			return e
		}
		for _, e := range d { // any first entry
			if m, ok := e.(map[string]any); ok {
				return m
			}
		}
	case []any:
		if len(d) > 0 {
			if m, ok := d[0].(map[string]any); ok {
				return m
			}
		}
	}
	return nil
}

// parsePcbRules extracts clearance/width/via (mil) from a pcb.drc.rules result.
// Paths from the live ceshi dump; each enclosing rule declares mm or mil:
//   - track width: rules.config.Physics.Track.copperThickness1oz.form.data[0].{defaultValue,minValue}
//   - via:         rules.config.Physics."Via Size".viaSize.form.{viaInnerdiameterDefault,viaOuterdiameterDefault}
//   - clearance:   rules.config.Spacing."Safe Spacing".copperThickness1oz.tables."1".content[0][0]  (Track↔Track)
func parsePcbRules(result map[string]any) (pcbRules, error) {
	r := defaultPcbRules()
	cfg := mnav(result, "rules", "config")
	if _, ok := cfg.(map[string]any); !ok {
		return r, nil // fallback
	}
	track := mnav(cfg, "Physics", "Track", "copperThickness1oz")
	via := mnav(cfg, "Physics", "Via Size", "viaSize")
	spacing := mnav(cfg, "Spacing", "Safe Spacing", "copperThickness1oz")
	trackData := firstDataEntry(mnav(track, "form", "data"))
	trackScale, err := pcbRuleUnitScale(track, "rules.config.Physics.Track.copperThickness1oz", trackData["defaultValue"], trackData["minValue"], trackData["maxValue"])
	if err != nil {
		return pcbRules{}, err
	}
	viaScale, err := pcbRuleUnitScale(via, "rules.config.Physics.Via Size.viaSize", mnav(via, "form", "viaInnerdiameterDefault"), mnav(via, "form", "viaOuterdiameterDefault"),
		mnav(via, "form", "viaInnerdiameterMin"), mnav(via, "form", "viaInnerdiameterMax"),
		mnav(via, "form", "viaOuterdiameterMin"), mnav(via, "form", "viaOuterdiameterMax"))
	if err != nil {
		return pcbRules{}, err
	}
	var spacingValues []any
	if content, ok := mnav(spacing, "tables", "1", "content").([]any); ok {
		for _, rawRow := range content {
			if row, ok := rawRow.([]any); ok {
				spacingValues = append(spacingValues, row...)
			}
		}
	}
	spacingScale, err := pcbRuleUnitScale(spacing, "rules.config.Spacing.Safe Spacing.copperThickness1oz", spacingValues...)
	if err != nil {
		return pcbRules{}, err
	}
	got := false

	// Track width (default + min).
	if d1 := firstDataEntry(mnav(cfg, "Physics", "Track", "copperThickness1oz", "form", "data")); d1 != nil {
		if v, ok := asFloatOK(d1["defaultValue"]); ok && v > 0 {
			r.trackWidthMil = round2(v * trackScale)
			got = true
		}
		if v, ok := asFloatOK(d1["minValue"]); ok && v > 0 {
			r.trackWidthMinMil = round2(v * trackScale)
		}
	}

	// Via drill + diameter.
	if form := mnav(cfg, "Physics", "Via Size", "viaSize", "form"); form != nil {
		if v, ok := asFloatOK(mnav(form, "viaInnerdiameterDefault")); ok && v > 0 {
			r.viaDrillMil = round2(v * viaScale)
			got = true
		}
		if v, ok := asFloatOK(mnav(form, "viaOuterdiameterDefault")); ok && v > 0 {
			r.viaDiameterMil = round2(v * viaScale)
		}
	}

	// Clearance — from the Safe Spacing triangular matrix (row/col = object types:
	// Track, SMD Pad, TH Pad, …). Preserve the pair dimension: live JLCEDA rules
	// commonly use 4mil for Track↔Track and 6mil for Track↔SMD-Pad. Routing uses
	// the binding pad value, but `pcb check` must not apply it to Track↔Track.
	if content, ok := mnav(cfg, "Spacing", "Safe Spacing", "copperThickness1oz", "tables", "1", "content").([]any); ok && len(content) > 0 {
		var trackTrack, trackPad float64
		if row0, ok := content[0].([]any); ok && len(row0) > 0 {
			if v, ok := asFloatOK(row0[0]); ok && v > 0 {
				trackTrack = v
			}
		}
		if len(content) > 1 {
			if row1, ok := content[1].([]any); ok && len(row1) > 0 {
				if v, ok := asFloatOK(row1[0]); ok && v > 0 {
					trackPad = v
				}
			}
		}
		if trackTrack > 0 {
			r.clearanceTrackTrackMil = round2(trackTrack * spacingScale)
			got = true
		}
		if trackPad > 0 {
			r.clearanceMil = round2(trackPad * spacingScale)
			got = true
		} else if trackTrack > 0 {
			// Older/smaller matrices may expose only Track↔Track. Keep consumers
			// usable without inventing a less conservative pad rule.
			r.clearanceMil = r.clearanceTrackTrackMil
			got = true
		}
	}

	// Copper-to-edge — Board Outline ↔ Copper/Plane Zone from the same Safe Spacing
	// matrix, located by the row/col labels (robust to matrix size). This is the
	// pour pull-back / inset floor (ceshi live = 10mil; JLCPCB fab min = 8mil).
	ss := mnav(cfg, "Spacing", "Safe Spacing", "copperThickness1oz")
	labels := asStrSlice(mnav(ss, "row"))
	if content, ok := mnav(ss, "tables", "1", "content").([]any); ok {
		bo := indexOf(labels, "Board Outline")
		cz := indexOf(labels, "Copper/Plane Zone")
		if bo >= 0 && bo < len(content) {
			if row, ok := content[bo].([]any); ok && cz >= 0 && cz < len(row) {
				if v, ok := asFloatOK(row[cz]); ok && v > 0 {
					r.copperToEdgeMil = round2(v * spacingScale)
				}
			}
		}
	}

	// Power width is a design convention (fab reference), not in the board rule;
	// keep the recommended value but never let it fall below the signal default.
	if r.trackWidthMil > r.powerWidthMil {
		r.powerWidthMil = r.trackWidthMil
	}

	if got {
		r.source = "live"
	} else {
		r.source = "fallback"
	}
	return r, nil
}

// Positive numeric rule fields require a unit. Missing fields retain the
// existing per-field fallback, but an ambiguous explicit rule must fail closed.
func pcbRuleUnitScale(node any, path string, values ...any) (float64, error) {
	for _, value := range values {
		if n, ok := asFloatOK(value); ok && n > 0 {
			switch mnav(node, "unit") {
			case "mm":
				return mmToMil, nil
			case "mil":
				return 1, nil
			default:
				return 0, fmt.Errorf("%s.unit: expected explicit mm or mil, got %v", path, mnav(node, "unit"))
			}
		}
	}
	return 1, nil
}
