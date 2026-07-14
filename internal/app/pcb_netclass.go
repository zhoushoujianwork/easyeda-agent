package app

// pcb_netclass.go — net-role classification and the canonical net-class → track
// width ladder (规范线宽). This upgrades the old two-bucket power/signal split
// (isGlobalNet ? powerWidth : signalWidth) into role-aware widths so a 3V3 branch,
// a +5V trunk, a VBUS/VIN connector-input rail and a signal each get their own
// spec width, per the official-board benchmark §7.8 ladder
// (pcb-layout-conventions.md): signal / power-branch / power-trunk / high-current.
//
// Like defaultPcbRules(), the ladder lives inline here as the daemon's authoritative
// source of truth; skills/easyeda-agent/references/fab-rules-jlcpcb.json carries a
// mirrored "netClasses" doc section for humans. The board's LIVE rules still seed
// the signal width and the legal-minimum floor — the ladder only adds the role
// steps ON TOP of what the live rule already gives.
//
// The classifier is a NAME/voltage heuristic. A circuit block CAN declare a per-net
// track_width_mil / net_class (internal/blocks/data/*.json signals map, the "sink to
// blocks" rule) but NOTHING consumes that declaration yet — wiring it in as the
// authoritative override is phase-2 work; until then this heuristic decides alone.

import (
	"math"
	"regexp"
	"strings"
)

// Net-class roles, ordered narrowest → widest. gnd is nominally the widest when
// routed, but a GND net normally belongs in a pour/plane, not a track (see
// power-not-poured / route-short --skip-power).
const (
	roleSignal      = "signal"       // ordinary logic/analog signal
	rolePowerBranch = "power-branch" // regulated downstream rail (3V3/1V8/VCC/VDD, < 5V)
	rolePowerTrunk  = "power-trunk"  // main rail (+5V-class, 5–9V)
	roleHighCurrent = "high-current" // connector-input / battery / bus rail (VBUS/VIN/VBAT/VSYS, ≥ 9V)
	roleGnd         = "gnd"          // ground — prefer pour/plane over a routed track
)

// netClassRolesByWidth is the display/iteration order (narrow → wide).
var netClassRolesByWidth = []string{roleSignal, rolePowerBranch, rolePowerTrunk, roleHighCurrent, roleGnd}

// railInputRe matches connector-input / battery / bus rails that carry the full
// board current regardless of their nominal voltage (a 5V VBUS from USB still feeds
// the whole board) → high-current.
var railInputRe = regexp.MustCompile(`(?i)^[+-]?(?:vbus|vin|vbat|vsys)\b`)

// railVoltageRe extracts a rail voltage encoded in the net name: "+5V"→5, "3V3"→3.3,
// "1V8"→1.8, "+12V"→12, "5V0"→5. The digits after the V are the fractional part.
var railVoltageRe = regexp.MustCompile(`(?i)(\d+)v(\d*)`)

// railVoltage parses a rail voltage (volts) from a net name; ok=false when the name
// carries no numeric voltage (e.g. VCC, VDD, VREF).
func railVoltage(net string) (float64, bool) {
	m := railVoltageRe.FindStringSubmatch(net)
	if m == nil {
		return 0, false
	}
	whole := m[1]
	v := 0.0
	for _, r := range whole {
		v = v*10 + float64(r-'0')
	}
	if frac := m[2]; frac != "" {
		// "3V3" → 3.3, "5V0" → 5.0 — treat the trailing digits as the decimal part.
		f := 0.0
		scale := 1.0
		for _, r := range frac {
			scale *= 10
			f = f*10 + float64(r-'0')
		}
		v += f / scale
	}
	return v, true
}

// netRole classifies a net into a spec-width role. It is the fallback heuristic used
// when no block declares an explicit width for the net; block data overrides it.
func netRole(net string) string {
	n := strings.TrimSpace(net)
	if n == "" {
		return roleSignal
	}
	if isGndNetName(n) {
		return roleGnd
	}
	if !isGlobalNet(n) {
		return roleSignal
	}
	// A power rail — split by input-rail name first, then by voltage.
	if railInputRe.MatchString(n) {
		return roleHighCurrent
	}
	if v, ok := railVoltage(n); ok {
		switch {
		case v >= 9:
			return roleHighCurrent
		case v >= 5:
			return rolePowerTrunk
		default:
			return rolePowerBranch // 3V3 / 1V8 / 1V2 …
		}
	}
	// Voltage-less power name (VCC/VDD/VREF/VOUT …) → treat as a regulated branch.
	return rolePowerBranch
}

// netClassWidthTable returns the canonical role→width (mil) ladder, seeded from the
// board's live rules. Signal tracks the board's live default; each power role steps
// up per the §7.8 ladder (branch 10 / trunk 15 / high-current 20); every width is
// floored at the fab's legal minimum and the ladder is kept monotonic
// (signal ≤ branch ≤ trunk ≤ high-current) so a loose live default never inverts it.
func netClassWidthTable(r pcbRules) map[string]float64 {
	sig := r.clampWidth(r.trackWidthMil)
	branch := r.clampWidth(math.Max(10, sig))
	trunk := r.clampWidth(math.Max(15, branch))
	// High-current ties to the board's power width (default 20), never below trunk.
	high := r.clampWidth(math.Max(r.powerWidthMil, trunk))
	return map[string]float64{
		roleSignal:      sig,
		rolePowerBranch: branch,
		rolePowerTrunk:  trunk,
		roleHighCurrent: high,
		roleGnd:         high, // if ever routed; normally poured
	}
}
