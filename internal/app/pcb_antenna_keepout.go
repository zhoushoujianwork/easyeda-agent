package app

import (
	"math"
	"strings"

	"github.com/zhoushoujianwork/easyeda-agent/internal/blocks"
)

// pcb_antenna_keepout.go — auto-generate the all-layer no-copper keep-out for an
// RF/antenna part (defect #4). The keep-out goes ONLY over the module's pad-free
// (antenna) end, never the whole footprint — a full-bbox keep-out would strand the
// module's ground pads. "Which part + how deep" is block-declared (approach A, see
// internal/blocks/data/*.json `keepout`); "which end" is deterministic geometry
// (the end of the long axis with no pads is physically where a PCB antenna sits).

// resolveAntennaDevice picks the device string used for antenna detection: the
// silk Device attribute (what pcb check keys on) when present, else the placed
// part's real device via cpDeviceName (manufacturerId / non-template name). Used
// by BOTH pcb check and `pcb antenna-keepout` so they detect the SAME RF parts.
func resolveAntennaDevice(silkDevice string, cm map[string]any) string {
	if s := strings.TrimSpace(silkDevice); s != "" {
		return s
	}
	return cpDeviceName(cm)
}

// antennaKeepoutFrac returns the block-declared keep-out depth fraction for a
// device, or 0 when no block declares one (→ the generator uses the full pad-free
// strip).
func antennaKeepoutFrac(ks []blocks.AntennaKeepout, device string) float64 {
	d := strings.ToLower(strings.TrimSpace(device))
	if d == "" {
		return 0
	}
	for _, k := range ks {
		if k.Match != "" && strings.Contains(d, strings.ToLower(k.Match)) {
			return k.EndFrac
		}
	}
	return 0
}

// padExtent returns the min/max of pad coordinates along axis (0=X, 1=Y).
func padExtent(pads [][2]float64, axis int) (lo, hi float64, ok bool) {
	lo, hi = math.Inf(1), math.Inf(-1)
	for _, p := range pads {
		v := p[axis]
		lo, hi = math.Min(lo, v), math.Max(hi, v)
	}
	if math.IsInf(lo, 1) {
		return 0, 0, false
	}
	return lo, hi, true
}

// antennaKeepoutRect computes the no-copper keep-out rectangle for an antenna part:
// the pad-free strip at one end of the module's LONG axis. endFrac (>0, block-
// declared) caps the depth to a fraction of the long axis; the pad-free strip
// ALWAYS caps it too, so the keep-out can never reach the pads (no stranded
// grounds). margin expands the three OUTER sides (never the pad-facing side).
// Returns ok=false when there is no usable strip (e.g. no pads).
func antennaKeepoutRect(minX, minY, maxX, maxY float64, pads [][2]float64, endFrac, margin, padClear float64) (x0, y0, x1, y1 float64, ok bool) {
	w, h := maxX-minX, maxY-minY
	if w <= 0 || h <= 0 {
		return
	}
	axis, span, bmin, bmax := 1, h, minY, maxY // default long axis = Y
	if w > h {
		axis, span, bmin, bmax = 0, w, minX, maxX
	}
	lo, hi, pok := padExtent(pads, axis)
	if !pok {
		return
	}
	// The pad-free strip is measured to the nearest pad's CENTER (pad width/height
	// isn't exposed), so pull the keep-out's pad-facing edge back by padClear to
	// clear the pad BODIES — otherwise the region would cover the antenna-facing
	// half of the outermost pad row and could strand its copper.
	lowGap, highGap := lo-bmin, bmax-hi // pad-free strips at each end (to pad centers)
	highEnd := highGap >= lowGap
	strip := lowGap
	if highEnd {
		strip = highGap
	}
	depth := strip - padClear
	if endFrac > 0 {
		depth = math.Min(depth, endFrac*span) // block caps depth; strip already bounds it
	}
	if depth <= 1 {
		return // no meaningful pad-free strip after clearing the pads
	}
	if axis == 1 { // Y long axis
		x0, x1 = minX-margin, maxX+margin
		if highEnd {
			y0, y1 = bmax-depth, bmax+margin
		} else {
			y0, y1 = bmin-margin, bmin+depth
		}
	} else { // X long axis
		y0, y1 = minY-margin, maxY+margin
		if highEnd {
			x0, x1 = bmax-depth, bmax+margin
		} else {
			x0, x1 = bmin-margin, bmin+depth
		}
	}
	return x0, y0, x1, y1, true
}

// ── discrete chip antennas (#123) ───────────────────────────────────────────
// A two-pad ceramic SMD antenna (Johanson 2450AT18A100E, footprint
// ANT-SMD_L3.2-W1.6) has NO pad-free strip — the whole footprint IS the
// radiator, so the strip heuristic above skips it ("no pad-free antenna strip
// found") and the part gets no protection at all. For these the keep-out is the
// full footprint bbox expanded by a datasheet-style clearance margin on every
// side. Its region must NOT carry no-wires: the antenna's own 50Ω feed line has
// to enter the clearance zone to reach the feed pad (#129's live-verified trap
// — no-wires bans the feed and native DRC fires Prohibited Region to Track).

// chipAntennaMaxDimMil separates a chip antenna from a module: the largest
// discrete SMD antennas are ~10mm (394mil); an integrated-antenna module
// (WROOM ≈ 700+ mil) always exceeds this and keeps the strip heuristic.
const chipAntennaMaxDimMil = 400.0

// isChipAntennaSize reports whether a bbox is chip-antenna sized.
func isChipAntennaSize(w, h float64) bool {
	return w > 0 && h > 0 && math.Max(w, h) <= chipAntennaMaxDimMil
}

// chipAntennaKeepoutRect is the discrete-antenna keep-out: bbox + margin on all
// four sides (the clearance the datasheet demands AROUND the radiator).
func chipAntennaKeepoutRect(minX, minY, maxX, maxY, margin float64) (x0, y0, x1, y1 float64, ok bool) {
	if maxX <= minX || maxY <= minY {
		return
	}
	return minX - margin, minY - margin, maxX + margin, maxY + margin, true
}
