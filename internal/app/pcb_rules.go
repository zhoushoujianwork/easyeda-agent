package app

// pcb_rules.go — read the board's LIVE DRC rules (clearance / track width / via)
// and normalize them to mil, so the daemon-side heuristics (route-short,
// auto-place, pour, via-stitch) conform to the board's spec instead of hardcoding
// widths/spacing. The rule object from `pcb.drc.rules`
// (eda.pcb_Drc.getCurrentRuleConfiguration) is deeply nested and untyped; the key
// paths below were discovered from a live ceshi dump (2026-07-01). Every field
// falls back independently to a JLCPCB 2-layer baseline (which matches ceshi's own
// live rules), so a missing/renamed path degrades gracefully instead of breaking.

const mmToMil = 39.37007874

// pcbRules is the normalized rule set (all values in mil) the planners consume.
type pcbRules struct {
	clearanceMil     float64 // track↔pad safe spacing (the binding routing clearance)
	trackWidthMil    float64 // default routing track width
	trackWidthMinMil float64 // minimum legal track width (clamp floor)
	viaDrillMil      float64 // via hole diameter
	viaDiameterMil   float64 // via outer diameter
	copperToEdgeMil  float64 // copper/plane-zone → board-outline clearance (pour inset floor)
	source           string  // "live" | "fallback"
}

// defaultPcbRules is the JLCPCB fabrication baseline — a sane seed when the live
// rule is missing or a path can't be read. Values track the canonical reference
// skills/easyeda-agent/references/fab-rules-jlcpcb.json ("recommended" column) and
// converge with ceshi's live rule (clear 6 / width 10 / min 5 / via 0.3–0.6mm /
// copper-to-edge 8mil), so read-live and fallback agree.
func defaultPcbRules() pcbRules {
	return pcbRules{
		clearanceMil: 6, trackWidthMil: 10, trackWidthMinMil: 5,
		viaDrillMil: 12, viaDiameterMil: 24, copperToEdgeMil: 8, source: "fallback",
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
// them; any error degrades to the JLCPCB baseline (never blocks the caller).
func fetchPcbRules(cfg *appConfig, window string) pcbRules {
	res, err := requestAction(cfg, "pcb.drc.rules", window, nil)
	if err != nil || res == nil {
		return defaultPcbRules()
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
// Paths (from the live ceshi dump), values in mm → converted to mil:
//   - track width: rules.config.Physics.Track.copperThickness1oz.form.data[0].{defaultValue,minValue}
//   - via:         rules.config.Physics."Via Size".viaSize.form.{viaInnerdiameterDefault,viaOuterdiameterDefault}
//   - clearance:   rules.config.Spacing."Safe Spacing".copperThickness1oz.tables."1".content[0][0]  (Track↔Track)
func parsePcbRules(result map[string]any) pcbRules {
	r := defaultPcbRules()
	cfg := mnav(result, "rules", "config")
	if _, ok := cfg.(map[string]any); !ok {
		return r // fallback
	}
	got := false

	// Track width (default + min).
	if d1 := firstDataEntry(mnav(cfg, "Physics", "Track", "copperThickness1oz", "form", "data")); d1 != nil {
		if v, ok := asFloatOK(d1["defaultValue"]); ok && v > 0 {
			r.trackWidthMil = round2(v * mmToMil)
			got = true
		}
		if v, ok := asFloatOK(d1["minValue"]); ok && v > 0 {
			r.trackWidthMinMil = round2(v * mmToMil)
		}
	}

	// Via drill + diameter.
	if form := mnav(cfg, "Physics", "Via Size", "viaSize", "form"); form != nil {
		if v, ok := asFloatOK(mnav(form, "viaInnerdiameterDefault")); ok && v > 0 {
			r.viaDrillMil = round2(v * mmToMil)
			got = true
		}
		if v, ok := asFloatOK(mnav(form, "viaOuterdiameterDefault")); ok && v > 0 {
			r.viaDiameterMil = round2(v * mmToMil)
		}
	}

	// Clearance — from the Safe Spacing triangular matrix (row/col = object types:
	// Track, SMD Pad, TH Pad, …). Use the BINDING routing clearance = the max of
	// Track↔Track (content[0][0]) and Track↔SMD-Pad (content[1][0]); DRC checks
	// tracks against OTHER nets' pads at the pad value (6mil on ceshi), which is the
	// constraint that actually bites when routing near parts — not the smaller
	// track-track value (4mil).
	if content, ok := mnav(cfg, "Spacing", "Safe Spacing", "copperThickness1oz", "tables", "1", "content").([]any); ok && len(content) > 0 {
		var clr float64
		if row0, ok := content[0].([]any); ok && len(row0) > 0 {
			if v, ok := asFloatOK(row0[0]); ok && v > clr {
				clr = v
			}
		}
		if len(content) > 1 {
			if row1, ok := content[1].([]any); ok && len(row1) > 0 {
				if v, ok := asFloatOK(row1[0]); ok && v > clr {
					clr = v // Track↔SMD-Pad
				}
			}
		}
		if clr > 0 {
			r.clearanceMil = round2(clr * mmToMil)
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
					r.copperToEdgeMil = round2(v * mmToMil)
				}
			}
		}
	}

	if got {
		r.source = "live"
	} else {
		r.source = "fallback"
	}
	return r
}
