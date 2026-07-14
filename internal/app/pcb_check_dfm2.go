package app

// pcb_check_dfm2.go — the second DFM rule batch, each enforcing a section of
// docs/pcb-design-rules.md (the fact-standard the JSON references mirror for
// humans; the CODE is what actually gates). Five rules:
//
//   silk-over-pad     §11.2 丝印不压焊盘 — silk text over exposed copper is fab-clipped
//   decap-too-far     §3.1  去耦电容紧贴 IC 电源引脚 (≤2mm)
//   via-in-pad        §2.3  禁止过孔打在焊盘上 (solder wicking; needs filled-via)
//   copper-near-edge  §5.1  铜到板边间距 (copper-to-edge floor)
//   fiducial-missing  §9    SMT 板需要 ≥3 个不共线 Mark 点
//
// All are pure-Go over already-fetched primitives except copper-near-edge,
// which needs the live board outline (wired in runPcbCheck like the other
// LIVE-only rules).

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

const (
	// decap-too-far: pad-center↔pad-center distance budget. The doc's 2mm is a
	// pad-EDGE distance; center-to-center adds the cap's own half-length, so the
	// threshold carries ~0.5mm slack (100 mil ≈ 2.54mm).
	pcbDecapMaxMil = 100.0
	// via-in-pad: via center within this of a same-net pad center = ON the pad.
	// Matches nominalPadHalf (the clearance rule's nominal pad half-extent).
	pcbViaInPadEps = nominalPadHalf
	// fiducial-missing only fires on a board that plausibly goes through SMT —
	// gauged by top-layer pad count (a hand-soldered proto doesn't need marks).
	pcbFidMinPads = 30
	// silk-over-pad text-extent estimate (font size isn't in pcb.silk.list):
	// default silk font is 40mil tall; a char is ~0.6× that wide.
	pcbSilkEstH     = 40.0
	pcbSilkEstChar  = 24.0
	pcbSilkPadSlack = 6.0 // inflate the text box by this before testing pad centers
)

// docRule appends the design-rules-manual reference to a finding message so the
// agent (and a human) can jump from the finding to the governing section. The
// manual ships INSIDE the skill (references/pcb-design-rules.md — the canonical
// copy; docs/pcb-design-rules.md in the repo is a pointer to it).
func docRule(section, title string) string {
	return fmt.Sprintf(" [规范 §%s %s — pcb-design-rules.md]", section, title)
}

// padOnSilkSide reports whether a pad's copper faces the given silk layer
// (top silk 3 ↔ copper 1, bottom silk 4 ↔ copper 2; multi-layer/through-hole
// pads face both sides).
func padOnSilkSide(padLayer, silkLayer int) bool {
	switch silkLayer {
	case silkTopLayer:
		return padLayer == pcbSideTop || padLayer >= 11
	case silkBottomLayer:
		return padLayer == pcbSideBottom || padLayer >= 11
	}
	return false
}

// ── R18: silk-over-pad (§11.2 丝印不压焊盘) ─────────────────────────────────
// Silk text printed over an exposed pad is clipped by the fab (solder mask
// opening wins) and can wick into the joint. Text extent is ESTIMATED from the
// string length at the default 40mil font (pcb.silk.list carries no font size),
// so this is a WARN, not an ERROR.
func findSilkOverPad(silk []pcbSilkText, pads []pcbPadP) []pcbCheckFinding {
	var out []pcbCheckFinding
	for _, s := range silk {
		txt := strings.TrimSpace(s.Text)
		if txt == "" {
			continue
		}
		hw := float64(len([]rune(txt))) * pcbSilkEstChar / 2
		hh := pcbSilkEstH / 2
		// 90°/270° rotation swaps the box extents.
		if r := math.Mod(math.Abs(s.Rotation), 180); r > 45 && r < 135 {
			hw, hh = hh, hw
		}
		for _, p := range pads {
			if !padOnSilkSide(p.Layer, s.Layer) {
				continue
			}
			if math.Abs(p.X-s.X) > hw+pcbSilkPadSlack || math.Abs(p.Y-s.Y) > hh+pcbSilkPadSlack {
				continue
			}
			ref := p.Designator
			if p.Number != "" {
				ref += "." + p.Number
			}
			label := txt
			if s.Kind == "attribute" && s.Key != "" {
				label = fmt.Sprintf("%s %q", s.Key, txt)
			}
			out = append(out, pcbCheckFinding{
				Type: "silk-over-pad", Level: "WARN", Layer: s.Layer,
				Designator: p.Designator, Primitives: []string{s.ID},
				At: &pcbXY{round2(p.X), round2(p.Y)},
				Message: fmt.Sprintf("silk text %s overlaps pad %s — fab clips silk on exposed copper; move it (`pcb silk-align` / `pcb silk-set`)%s",
					label, ref, docRule("11.2", "丝印不压焊盘")),
			})
			break // one finding per silk text is enough
		}
	}
	return out
}

// ── R19: decap-too-far (§3.1 去耦电容紧贴 IC) ───────────────────────────────
// A decoupling cap (C* with one pad on a power rail and one on GND) parked far
// from the IC pin it decouples is inductance, not decoupling. For each such cap,
// measure its power-rail pad to the NEAREST same-rail IC (U*) pad; over budget →
// WARN. Rails with no IC pad at all are skipped (bulk/input caps decouple a
// connector, not a chip).
func findDecapTooFar(pads []pcbPadP) []pcbCheckFinding {
	capRe := regexp.MustCompile(`(?i)^C\d`)
	icRe := regexp.MustCompile(`(?i)^U\d`)

	// power-rail pads of ICs, bucketed by net.
	icPadsByNet := map[string][]pcbPadP{}
	byDesig := map[string][]pcbPadP{}
	var order []string
	for _, p := range pads {
		net := strings.TrimSpace(p.Net)
		if icRe.MatchString(p.Designator) && net != "" && isGlobalNet(net) && !isGndNetName(net) {
			icPadsByNet[net] = append(icPadsByNet[net], p)
		}
		if capRe.MatchString(p.Designator) {
			if _, ok := byDesig[p.Designator]; !ok {
				order = append(order, p.Designator)
			}
			byDesig[p.Designator] = append(byDesig[p.Designator], p)
		}
	}
	sort.Strings(order)

	var out []pcbCheckFinding
	for _, d := range order {
		ps := byDesig[d]
		if len(ps) != 2 {
			continue // decap heuristic: a plain 2-pad cap
		}
		var pwr *pcbPadP
		gnd := false
		for i := range ps {
			net := strings.TrimSpace(ps[i].Net)
			switch {
			case isGndNetName(net):
				gnd = true
			case net != "" && isGlobalNet(net):
				pwr = &ps[i]
			}
		}
		if !gnd || pwr == nil {
			continue // not a rail-to-GND decap
		}
		ics := icPadsByNet[strings.TrimSpace(pwr.Net)]
		if len(ics) == 0 {
			continue // no IC on this rail — bulk/input cap, not a decap
		}
		best := math.Inf(1)
		bestRef := ""
		for _, ip := range ics {
			if d := math.Hypot(ip.X-pwr.X, ip.Y-pwr.Y); d < best {
				best = d
				bestRef = ip.Designator
				if ip.Number != "" {
					bestRef += "." + ip.Number
				}
			}
		}
		if best <= pcbDecapMaxMil {
			continue
		}
		out = append(out, pcbCheckFinding{
			Type: "decap-too-far", Level: "WARN", Net: pwr.Net, Designator: d,
			At: &pcbXY{round2(pwr.X), round2(pwr.Y)},
			Message: fmt.Sprintf("decoupling cap %s sits %.0fmil (%.1fmm) from the nearest %s pin %s — a decap must hug its IC (≤2mm)%s",
				d, best, best*0.0254, pwr.Net, bestRef, docRule("3.1", "去耦电容紧贴IC")),
		})
	}
	return out
}

// ── R20: via-in-pad (§2.3 禁止过孔打在焊盘上) ───────────────────────────────
// A via drilled in a same-net pad wicks solder down the barrel (starved joint)
// unless the fab fills+caps it — and this project's power-planes stitching bit
// exactly this (via-on-pad ≠ connected). Cross-net via↔pad is the clearance
// rule's ERROR; this rule owns the SAME-net case.
func findViaInPad(vias []pcbViaP, pads []pcbPadP) []pcbCheckFinding {
	var out []pcbCheckFinding
	for _, v := range vias {
		net := strings.TrimSpace(v.Net)
		if net == "" {
			continue
		}
		for _, p := range pads {
			if strings.TrimSpace(p.Net) != net {
				continue
			}
			if math.Hypot(v.X-p.X, v.Y-p.Y) > pcbViaInPadEps {
				continue
			}
			ref := p.Designator
			if p.Number != "" {
				ref += "." + p.Number
			}
			out = append(out, pcbCheckFinding{
				Type: "via-in-pad", Level: "WARN", Net: net, Designator: p.Designator,
				Primitives: []string{v.ID}, At: &pcbXY{round2(v.X), round2(v.Y)},
				Message: fmt.Sprintf("via sits ON pad %s (net %s) — solder wicks down the barrel; offset the via and enter with a short stub (dog-bone)%s",
					ref, net, docRule("2.3", "禁止过孔打在焊盘上")),
			})
			break // one finding per via
		}
	}
	return out
}

// ── R21: copper-near-edge (§5.1 铜到板边间距) ───────────────────────────────
// Routed copper (tracks/vias) closer to the board outline than the
// copper-to-edge floor risks exposed/burred copper after routing. Findings are
// aggregated per net (worst offender + count), like width-under-spec. The
// outline is its BBOX — good for rectangular boards; interior cutouts are the
// clearance rule's slot check. d<0 (copper outside the outline) counts too.
func findCopperNearEdge(tracks []pcbTrack, vias []pcbViaP, outline *layoutBBox, edgeClr float64) []pcbCheckFinding {
	if outline == nil || edgeClr <= 0 {
		return nil
	}
	// distance from an interior point to the nearest outline edge (min of four
	// linear functions → its minimum over a straight segment is at an endpoint).
	edgeDist := func(x, y float64) float64 {
		return math.Min(math.Min(x-outline.MinX, outline.MaxX-x), math.Min(y-outline.MinY, outline.MaxY-y))
	}
	type offender struct {
		count int
		worst float64
		at    pcbXY
		prim  string
	}
	byNet := map[string]*offender{}
	var order []string
	note := func(net string, d, x, y float64, id string) {
		o := byNet[net]
		if o == nil {
			o = &offender{worst: math.Inf(1)}
			byNet[net] = o
			order = append(order, net)
		}
		o.count++
		if d < o.worst {
			o.worst = d
			o.at = pcbXY{round2(x), round2(y)}
			o.prim = id
		}
	}
	for _, t := range tracks {
		net := strings.TrimSpace(t.Net)
		if net == "" {
			continue // net-less lines are the outline itself / mechanical
		}
		for _, ep := range [][2]float64{{t.X1, t.Y1}, {t.X2, t.Y2}} {
			if d := edgeDist(ep[0], ep[1]) - t.Width/2; d < edgeClr {
				note(net, d, ep[0], ep[1], t.ID)
				break
			}
		}
	}
	for _, v := range vias {
		net := strings.TrimSpace(v.Net)
		if net == "" {
			continue
		}
		if d := edgeDist(v.X, v.Y) - v.Dia/2; d < edgeClr {
			note(net, d, v.X, v.Y, v.ID)
		}
	}
	sort.Strings(order)
	var out []pcbCheckFinding
	for _, n := range order {
		o := byNet[n]
		f := pcbCheckFinding{
			Type: "copper-near-edge", Level: "WARN", Net: n,
			At: &pcbXY{o.at.X, o.at.Y},
			Message: fmt.Sprintf("net %s: %d copper primitive(s) within %.0fmil of the board edge (worst %.1fmil) — keep copper ≥%.0fmil from the outline%s",
				n, o.count, edgeClr, math.Max(o.worst, 0), edgeClr, docRule("5.1", "铜到板边间距")),
		}
		if o.prim != "" {
			f.Primitives = []string{o.prim}
		}
		out = append(out, f)
	}
	return out
}

// ── R22: fiducial-missing (§9 Mark 点) ──────────────────────────────────────
// An SMT-assembled board needs ≥3 non-collinear fiducials for the pick-and-place
// vision system. Fiducials are recognized by designator (FID*/MARK*/MK*). INFO,
// not WARN: JLC's economic SMT adds panel-rail fiducials itself, so a bare board
// without local marks is often fine — but a production panel wants its own.
func findFiducialMissing(pads []pcbPadP) []pcbCheckFinding {
	fidRe := regexp.MustCompile(`(?i)^(FID|MARK|MK)\d*$`)
	topPads := 0
	fids := map[string]bool{}
	for _, p := range pads {
		if fidRe.MatchString(p.Designator) {
			fids[p.Designator] = true
			continue
		}
		if p.Layer == pcbSideTop {
			topPads++
		}
	}
	if topPads < pcbFidMinPads || len(fids) >= 3 {
		return nil
	}
	return []pcbCheckFinding{{
		Type: "fiducial-missing", Level: "INFO",
		Message: fmt.Sprintf("board has %d top-layer pads (SMT-scale) but only %d fiducial(s) (FID*/MARK*) — SMT wants ≥3 non-collinear 1mm marks (JLC panel rails add their own; local marks needed for fine-pitch)%s",
			topPads, len(fids), docRule("9", "Mark点设计")),
	}}
}
