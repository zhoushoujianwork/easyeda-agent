package app

// pcb check — reconstructed DFM (design-for-manufacture) audit.
//
// The PCB sibling of `sch check`. EasyEDA's native `pcb drc` catches
// rule-clearance violations (track-to-pad, width, hole); it does NOT flag the
// manufacturability / reliability hazards that the official DFM tools look for:
// acid-trap acute angles, dangling copper stubs, free-angle traces, track-over-pad
// shorts, flipped/back-side silkscreen, pointless single-layer vias, stacked/
// redundant vias, asymmetric neck-down on 2-pin parts, or duplicated overlapping
// copper. Copper rules recompute purely from the placed primitives (tracks + vias
// + pads); the silkscreen-orientation rule reads text layer+mirror via the
// `pcb.silk.list` connector handler. Mirrors the Go-side geometry approach of
// `pcb layout-lint`.
//
// Pure core (analyzePcbCheck) is unit-tested; the live fetch/render is
// runPcbCheck below. Reuses isGlobalNet (pcb_autoplace.go) and round2
// (cmd_sch_layout.go). Arcs are out of scope for v1 (no pcb.arc.list action);
// auto-routed / short-routed copper is line segments, so coverage is high.

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
)

// Geometry thresholds, all in mil (PCB primitives are native mil — pcb.line.create
// passes coordinates straight to eda.* with no scaling).
const (
	pcbCoincEps    = 2.0  // endpoint↔endpoint / ↔via / ↔pad-center coincidence
	pcbOnSegEps    = 1.5  // point-lies-on-track-body (T-junction / collinear test)
	pcbAcuteDeg    = 90.0 // interior trace angle below this = acid-trap acute corner
	pcbAcuteMinDeg = 5.0  // …but at/below this it's collinear overlap, not a corner (dup-segment covers it)
	pcbViaDupEps   = 2.0  // two vias closer than this = redundant/stacked
	pcbWidthTolMil = 1.0  // absolute width tolerance for 2-pin neck-down symmetry
	pcbWidthTolRel = 0.25 // relative width tolerance (25%)
	pcbDupOverlap  = 2.0  // collinear same-net overlap longer than this = duplicate copper

	pcbCouplingW       = 3.0  // default 3W factor: center-to-center spacing < this×maxWidth = coupling risk
	pcbParallelDeg     = 15.0 // two segments within this of parallel/anti-parallel are "parallel"
	pcbCouplingMinOvlp = 20.0 // parallel overlap must exceed this (mil) to count (ignore incidental)

	pcbOrthoTolDeg = 1.0 // a track this far off the nearest 0/45/90/135° = free-angle routing
	pcbOverPadEps  = 2.0 // pad center within this of a track body (but not its endpoint) = track-over-pad
)

// pcbTrack is one copper line segment (pcb.line.list).
type pcbTrack struct {
	ID     string
	Net    string
	Layer  int
	X1, Y1 float64
	X2, Y2 float64
	Width  float64
}

// pcbViaP is one via (pcb.via.list).
type pcbViaP struct {
	ID   string
	Net  string
	X, Y float64
	Hole float64
	Dia  float64
}

// pcbPadP is one placed pad (pcb.components.list --include-pads).
type pcbPadP struct {
	Designator string
	Number     string
	Net        string
	Layer      int
	X, Y       float64
}

// pcbSilkText is one silkscreen text primitive (pcb.silk.list) — a component
// designator/value attribute or a free string. Layer is the silk layer
// (silkTopLayer / silkBottomLayer); CompLayer is the parent component's side
// (pcbSideTop / pcbSideBottom, 0 = none/unknown) for attributes.
type pcbSilkText struct {
	ID        string
	Kind      string // "attribute" | "string"
	Key       string // attribute key: "Designator" | "Footprint" | "Device"
	Text      string
	Layer     int
	Mirror    bool
	Reverse   bool    // reversed reading (left-right flipped) — reads backwards
	Rotation  float64 // degrees; a designator should read upright (0°)
	CompID    string
	CompLayer int
	X, Y      float64
}

// Silk / component side layer ids (EPCB_LayerId).
const (
	pcbSideTop      = 1
	pcbSideBottom   = 2
	pcbLayerMulti   = 12 // 多层 / MULTI — a region here spans every copper layer at once
	silkTopLayer    = 3
	silkBottomLayer = 4
)

// pcbXY is a coordinate on a finding.
type pcbXY struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// pcbCheckFinding is one DFM issue.
type pcbCheckFinding struct {
	Type       string    `json:"type"`
	Level      string    `json:"level"` // ERROR | WARN | INFO
	Net        string    `json:"net,omitempty"`
	Nets       []string  `json:"nets,omitempty"`
	Layer      int       `json:"layer,omitempty"`
	Designator string    `json:"designator,omitempty"`
	Primitives []string  `json:"primitives,omitempty"`
	AngleDeg   float64   `json:"angleDeg,omitempty"`
	Widths     []float64 `json:"widths,omitempty"`
	Message    string    `json:"message"`
	At         *pcbXY    `json:"at,omitempty"`
}

type pcbCheckSummary struct {
	DanglingEnds      int `json:"danglingEnds"`
	AcuteAngles       int `json:"acuteAngles"`
	NonOrthogonal     int `json:"nonOrthogonal"`
	TrackOverPad      int `json:"trackOverPad"`
	SilkscreenFlipped int `json:"silkscreenFlipped"`
	OverlappingVias   int `json:"overlappingVias"`
	SingleLayerVias   int `json:"singleLayerVias"`
	WidthMismatches   int `json:"widthMismatches"`
	DuplicateSegments int `json:"duplicateSegments"`
	ParallelCoupling  int `json:"parallelCoupling"`
	AntennaKeepout    int `json:"antennaKeepout"`
	NetlessPours      int `json:"netlessPours"`
	ViaCrossesPlane   int `json:"viaCrossesPlane"`
	Errors            int `json:"errors"`
	Warnings          int `json:"warnings"`
	Total             int `json:"total"`
}

type pcbCheckReport struct {
	Passed     bool              `json:"passed"`
	Summary    pcbCheckSummary   `json:"summary"`
	TrackCount int               `json:"trackCount"`
	ViaCount   int               `json:"viaCount"`
	PadCount   int               `json:"padCount"`
	Findings   []pcbCheckFinding `json:"findings"`
}

// analyzePcbCheck is the copper-only DFM core (no silkscreen). Thin wrapper over
// analyzePcbCheckFull; kept for the many unit tests that don't exercise silk.
func analyzePcbCheck(pads []pcbPadP, tracks []pcbTrack, vias []pcbViaP, couplingW float64) pcbCheckReport {
	return analyzePcbCheckFull(pads, tracks, vias, nil, couplingW)
}

// analyzePcbCheckFull is the pure DFM core over placed primitives. couplingW is the
// 3W-rule center-spacing factor (≤0 → default pcbCouplingW). silk feeds the
// silkscreen-orientation rule (flipped/back-side labels).
func analyzePcbCheckFull(pads []pcbPadP, tracks []pcbTrack, vias []pcbViaP, silk []pcbSilkText, couplingW float64) pcbCheckReport {
	rep := pcbCheckReport{TrackCount: len(tracks), ViaCount: len(vias), PadCount: len(pads)}
	if couplingW <= 0 {
		couplingW = pcbCouplingW
	}

	// Drop degenerate zero-length tracks up front (they connect nothing and would
	// pollute angle/dangling math).
	real := tracks[:0:0]
	for _, t := range tracks {
		if math.Hypot(t.X2-t.X1, t.Y2-t.Y1) >= pcbCoincEps {
			real = append(real, t)
		}
	}
	tracks = real

	rep.Findings = append(rep.Findings, findDanglingEnds(tracks, vias, pads)...)
	rep.Findings = append(rep.Findings, findAcuteAngles(tracks)...)
	rep.Findings = append(rep.Findings, findNonOrthogonal(tracks)...)
	rep.Findings = append(rep.Findings, findTrackOverPad(tracks, pads)...)
	rep.Findings = append(rep.Findings, findViaIssues(tracks, vias)...)
	rep.Findings = append(rep.Findings, findWidthMismatch(tracks, pads)...)
	rep.Findings = append(rep.Findings, findDuplicateSegments(tracks)...)
	rep.Findings = append(rep.Findings, findParallelCoupling(tracks, couplingW)...)
	rep.Findings = append(rep.Findings, findSilkscreenFlipped(silk)...)

	for _, f := range rep.Findings {
		switch f.Type {
		case "dangling-end":
			rep.Summary.DanglingEnds++
		case "acute-angle":
			rep.Summary.AcuteAngles++
		case "non-orthogonal":
			rep.Summary.NonOrthogonal++
		case "track-over-pad":
			rep.Summary.TrackOverPad++
		case "overlapping-via":
			rep.Summary.OverlappingVias++
		case "single-layer-via":
			rep.Summary.SingleLayerVias++
		case "width-mismatch":
			rep.Summary.WidthMismatches++
		case "duplicate-segment":
			rep.Summary.DuplicateSegments++
		case "parallel-coupling":
			rep.Summary.ParallelCoupling++
		case "silkscreen-flipped":
			rep.Summary.SilkscreenFlipped++
		}
		switch f.Level {
		case "ERROR":
			rep.Summary.Errors++
			rep.Summary.Warnings++
		case "WARN":
			rep.Summary.Warnings++
		}
	}
	rep.Summary.Total = len(rep.Findings)
	rep.Passed = rep.Summary.Total == 0
	return rep
}

// ── R1: dangling copper stub ────────────────────────────────────────────────
// A track endpoint that anchors to nothing — no pad, no via, no other track —
// is an unfinished / floating copper stub (a routing artifact). Endpoints that
// join a pad center, a via, or any other track (endpoint OR mid-body T-junction)
// are anchored.
func findDanglingEnds(tracks []pcbTrack, vias []pcbViaP, pads []pcbPadP) []pcbCheckFinding {
	var out []pcbCheckFinding
	seen := map[[2]int64]bool{} // dedup by rounded point — one finding per free node
	for i, t := range tracks {
		for _, ep := range [][2]float64{{t.X1, t.Y1}, {t.X2, t.Y2}} {
			px, py := ep[0], ep[1]
			if anchored(px, py, i, t.Layer, t.Net, tracks, vias, pads) {
				continue
			}
			k := [2]int64{int64(math.Round(px * 100)), int64(math.Round(py * 100))}
			if seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, pcbCheckFinding{
				Type: "dangling-end", Level: "WARN", Net: t.Net, Layer: t.Layer,
				Primitives: []string{t.ID}, At: &pcbXY{round2(px), round2(py)},
				Message: "track end connects to nothing (no pad/via/track) — unfinished or floating copper",
			})
		}
	}
	return out
}

// padBodyAnchorTol is the same-net pad anchoring tolerance: the pad list carries
// only centers (no extents), so an endpoint landing INSIDE the pad copper but off
// its center (a legitimate bond — EasyEDA's own connectivity accepts it, verified
// on U1's castellated GND stubs) must still anchor. 30 mil covers typical pad
// half-extents without reaching a neighboring pad (smallest pitch on board ≥ 20 mil
// applies only to FOREIGN nets, which keep the strict epsilon below).
const padBodyAnchorTol = 30.0

// anchored reports whether a track endpoint at (px,py) on copper layer `layer`
// is electrically continued: a pad there (same-net pads anchor within the pad-body
// tolerance, foreign pads only at the exact center — a near-miss on a foreign pad
// is NOT a connection), a via, or ANOTHER track passing through it ON THE SAME
// LAYER. A different-layer track crossing the XY is NOT a connection without a
// via, so it must not anchor the stub.
func anchored(px, py float64, self, layer int, net string, tracks []pcbTrack, vias []pcbViaP, pads []pcbPadP) bool {
	for _, p := range pads {
		tol := pcbCoincEps
		if net != "" && p.Net == net {
			tol = padBodyAnchorTol
		}
		if math.Hypot(p.X-px, p.Y-py) <= tol {
			return true
		}
	}
	for _, v := range vias {
		if math.Hypot(v.X-px, v.Y-py) <= pcbCoincEps {
			return true
		}
	}
	for j, o := range tracks {
		if j == self || o.Layer != layer {
			continue
		}
		if segPtDist(px, py, o.X1, o.Y1, o.X2, o.Y2) <= pcbCoincEps {
			return true
		}
	}
	return false
}

// ── R2: acute-angle (acid-trap) corner ──────────────────────────────────────
// Where two same-net, same-layer segments meet at a shared vertex, the interior
// angle between them in (pcbAcuteMinDeg, 90°) forms a sharp spike where etchant
// collects. 90° and 45° (135° interior) corners are fine; ≤5° is collinear overlap
// (duplicate-segment), not a corner.
//
// Known scope limits (deliberate — low value / false-positive risk, tracked by the
// DFM audit wf_9afc4dbe-b08): only EXACT shared vertices are compared, so a branch
// teeing off mid-trunk (T-junction) and two endpoints coincident within pcbCoincEps
// but not exactly are not evaluated. Routed copper meets at exact vertices, so this
// covers the real cases.
func findAcuteAngles(tracks []pcbTrack) []pcbCheckFinding {
	type inc struct {
		dx, dy float64
		id     string
	}
	type vtx struct {
		net   string
		layer int
		x, y  float64
		segs  []inc
	}
	// bucket incident segment directions by net|layer|point (0.01 mil grid). The
	// key is only an identity handle — metadata lives on the vtx so we never parse
	// the key back (Go's fmt has no %[...] scanset).
	buckets := map[string]*vtx{}
	key := func(net string, layer int, x, y float64) string {
		return fmt.Sprintf("%s|%d|%d|%d", net, layer, int64(math.Round(x*100)), int64(math.Round(y*100)))
	}
	add := func(net string, layer int, x, y, dx, dy float64, id string) {
		k := key(net, layer, x, y)
		v := buckets[k]
		if v == nil {
			v = &vtx{net: net, layer: layer, x: x, y: y}
			buckets[k] = v
		}
		v.segs = append(v.segs, inc{dx, dy, id})
	}
	for _, t := range tracks {
		add(t.Net, t.Layer, t.X1, t.Y1, t.X2-t.X1, t.Y2-t.Y1, t.ID)
		add(t.Net, t.Layer, t.X2, t.Y2, t.X1-t.X2, t.Y1-t.Y2, t.ID)
	}
	// deterministic order
	ks := make([]string, 0, len(buckets))
	for k := range buckets {
		ks = append(ks, k)
	}
	sort.Strings(ks)

	var out []pcbCheckFinding
	for _, k := range ks {
		v := buckets[k]
		if len(v.segs) < 2 {
			continue
		}
		minAng := 999.0
		var ids []string
		for a := 0; a < len(v.segs); a++ {
			for b := a + 1; b < len(v.segs); b++ {
				ang := angleBetween(v.segs[a].dx, v.segs[a].dy, v.segs[b].dx, v.segs[b].dy)
				if ang < minAng {
					minAng = ang
					ids = uniqStr([]string{v.segs[a].id, v.segs[b].id})
				}
			}
		}
		// (pcbAcuteMinDeg, 90°): a real acid-trap corner. ≤5° is collinear/overlapping
		// copper (no corner) — duplicate-segment covers that; 0.5° tol so a clean 90° never trips.
		if minAng > pcbAcuteMinDeg && minAng < pcbAcuteDeg-0.5 {
			out = append(out, pcbCheckFinding{
				Type: "acute-angle", Level: "WARN", Net: v.net, Layer: v.layer,
				Primitives: ids, AngleDeg: round2(minAng),
				At:      &pcbXY{round2(v.x), round2(v.y)},
				Message: fmt.Sprintf("trace bends %.0f° (<90°) — acid-trap acute angle", minAng),
			})
		}
	}
	return out
}

// ── R3: non-orthogonal (free-angle) trace ───────────────────────────────────
// Good routing runs on the 45° grid — segments at 0/45/90/135°. A track at an
// arbitrary angle (e.g. a lazy pad-to-pad diagonal) is a free-angle route: harder
// to inspect, and often a sign the router/hand-route skipped a proper corner. We
// flag any single segment whose heading is >1° off the nearest multiple of 45°.
func findNonOrthogonal(tracks []pcbTrack) []pcbCheckFinding {
	var out []pcbCheckFinding
	for _, t := range tracks {
		ang := math.Atan2(t.Y2-t.Y1, t.X2-t.X1) * 180 / math.Pi
		if ang < 0 {
			ang += 180 // heading is mod 180 (a segment has no direction)
		}
		r := math.Mod(ang, 45) // distance to nearest 45° multiple
		off := math.Min(r, 45-r)
		if off > pcbOrthoTolDeg {
			out = append(out, pcbCheckFinding{
				Type: "non-orthogonal", Level: "WARN", Net: t.Net, Layer: t.Layer,
				Primitives: []string{t.ID}, AngleDeg: round2(ang),
				At:      &pcbXY{round2((t.X1 + t.X2) / 2), round2((t.Y1 + t.Y2) / 2)},
				Message: fmt.Sprintf("trace runs at %.1f° — not on the 0/45/90° grid (free-angle routing)", ang),
			})
		}
	}
	return out
}

// ── R4: track-over-pad (crossing a pad it doesn't terminate on) ──────────────
// A track whose body passes directly over a pad center that is NOT one of its own
// endpoints. On the SAME copper layer this is either a hard short (the pad is on a
// different net) or sloppy routing (same net — the track should have terminated on
// the pad, not run through it). Different-layer pads are ignored (a top track over
// a bottom SMD pad is fine); through-hole cross-layer shorts are a known blind spot
// (pad layer is reported as a single side here).
func findTrackOverPad(tracks []pcbTrack, pads []pcbPadP) []pcbCheckFinding {
	var out []pcbCheckFinding
	for _, t := range tracks {
		for _, p := range pads {
			if p.Layer != t.Layer {
				continue
			}
			// pad is an endpoint of this track → legitimate termination, skip.
			if math.Hypot(p.X-t.X1, p.Y-t.Y1) <= pcbCoincEps || math.Hypot(p.X-t.X2, p.Y-t.Y2) <= pcbCoincEps {
				continue
			}
			if segPtDist(p.X, p.Y, t.X1, t.Y1, t.X2, t.Y2) > pcbOverPadEps {
				continue
			}
			padRef := p.Designator
			if p.Number != "" {
				padRef += "." + p.Number
			}
			if p.Net != t.Net {
				out = append(out, pcbCheckFinding{
					Type: "track-over-pad", Level: "ERROR", Net: t.Net, Layer: t.Layer,
					Designator: p.Designator, Primitives: []string{t.ID},
					At:      &pcbXY{round2(p.X), round2(p.Y)},
					Message: fmt.Sprintf("track (net %s) crosses over pad %s (net %s) on the same layer — short circuit", t.Net, padRef, p.Net),
				})
			} else {
				out = append(out, pcbCheckFinding{
					Type: "track-over-pad", Level: "WARN", Net: t.Net, Layer: t.Layer,
					Designator: p.Designator, Primitives: []string{t.ID},
					At:      &pcbXY{round2(p.X), round2(p.Y)},
					Message: fmt.Sprintf("track passes through same-net pad %s instead of terminating on it", padRef),
				})
			}
		}
	}
	return out
}

// ── R5: via issues — stacked/redundant + pointless single-layer ─────────────
func findViaIssues(tracks []pcbTrack, vias []pcbViaP) []pcbCheckFinding {
	var out []pcbCheckFinding

	// stacked/overlapping vias
	for i := 0; i < len(vias); i++ {
		for j := i + 1; j < len(vias); j++ {
			if math.Hypot(vias[i].X-vias[j].X, vias[i].Y-vias[j].Y) <= pcbViaDupEps {
				nets := uniqStr([]string{vias[i].Net, vias[j].Net})
				out = append(out, pcbCheckFinding{
					Type: "overlapping-via", Level: "WARN", Nets: nets,
					Primitives: []string{vias[i].ID, vias[j].ID},
					At:         &pcbXY{round2(vias[i].X), round2(vias[i].Y)},
					Message:    "two vias occupy the same spot — stacked/redundant",
				})
			}
		}
	}

	// single-layer / dangling via: a via exists to change layers. If the tracks
	// touching it live on fewer than 2 copper layers it serves no purpose. Skip
	// power/GND nets — those are stitching/plane vias that connect to a pour
	// (not a track), so a single touching layer is legitimate.
	for _, v := range vias {
		if isGlobalNet(v.Net) {
			continue
		}
		layers := map[int]bool{}
		for _, t := range tracks {
			if t.Net != v.Net { // a foreign net's track merely crossing the XY isn't served by this via
				continue
			}
			if segPtDist(v.X, v.Y, t.X1, t.Y1, t.X2, t.Y2) <= pcbCoincEps {
				layers[t.Layer] = true
			}
		}
		if len(layers) < 2 {
			out = append(out, pcbCheckFinding{
				Type: "single-layer-via", Level: "WARN", Net: v.Net,
				Primitives: []string{v.ID}, At: &pcbXY{round2(v.X), round2(v.Y)},
				Message: fmt.Sprintf("signal via touches tracks on %d layer(s) — no layer transition (pointless or dangling)", len(layers)),
			})
		}
	}
	return out
}

// ── R6: 2-pin neck-down asymmetry ───────────────────────────────────────────
// A discrete 2-pad part (R/C/L/diode) whose two pads have noticeably different
// entering track widths — asymmetric neck-down, usually an oversight.
func findWidthMismatch(tracks []pcbTrack, pads []pcbPadP) []pcbCheckFinding {
	byDesig := map[string][]pcbPadP{}
	order := []string{}
	for _, p := range pads {
		if _, ok := byDesig[p.Designator]; !ok {
			order = append(order, p.Designator)
		}
		byDesig[p.Designator] = append(byDesig[p.Designator], p)
	}
	sort.Strings(order)

	var out []pcbCheckFinding
	for _, d := range order {
		ps := byDesig[d]
		if len(ps) != 2 {
			continue
		}
		w0, ok0 := maxTrackWidthAt(ps[0].X, ps[0].Y, ps[0].Net, tracks)
		w1, ok1 := maxTrackWidthAt(ps[1].X, ps[1].Y, ps[1].Net, tracks)
		if !ok0 || !ok1 {
			continue
		}
		diff := math.Abs(w0 - w1)
		tol := math.Max(pcbWidthTolMil, pcbWidthTolRel*math.Max(w0, w1))
		if diff > tol {
			out = append(out, pcbCheckFinding{
				Type: "width-mismatch", Level: "INFO", Designator: d, Net: ps[0].Net,
				Widths:  []float64{round2(w0), round2(w1)},
				Message: fmt.Sprintf("2-pin part %s: entering track widths differ (%.1f vs %.1f mil)", d, w0, w1),
			})
		}
	}
	return out
}

// maxTrackWidthAt is the widest track of net `net` whose endpoint lands on the pad
// at (px,py). Restricting to the pad's OWN net keeps an unrelated track that merely
// crosses the pad's XY (e.g. on another layer) from inflating the width — a track
// entering a pad necessarily carries that pad's net.
func maxTrackWidthAt(px, py float64, net string, tracks []pcbTrack) (float64, bool) {
	best := 0.0
	found := false
	for _, t := range tracks {
		if t.Net != net {
			continue
		}
		if math.Hypot(t.X1-px, t.Y1-py) <= pcbCoincEps || math.Hypot(t.X2-px, t.Y2-py) <= pcbCoincEps {
			if t.Width > best {
				best = t.Width
			}
			found = true
		}
	}
	return best, found
}

// ── R7: duplicated / overlapping copper ─────────────────────────────────────
// Two same-net, same-layer collinear segments whose overlap exceeds a tolerance
// — redundant double copper (a router artifact), mergeable.
func findDuplicateSegments(tracks []pcbTrack) []pcbCheckFinding {
	var out []pcbCheckFinding
	for i := 0; i < len(tracks); i++ {
		for j := i + 1; j < len(tracks); j++ {
			a, b := tracks[i], tracks[j]
			if a.Net != b.Net || a.Layer != b.Layer {
				continue
			}
			// collinearOverlap tests b's endpoints against a's line, so it's asymmetric:
			// a short segment sitting on a long slightly-angled one is only detected with
			// the LONGER segment as the reference. Order it so the result is stable.
			if segLen(b) > segLen(a) {
				a, b = b, a
			}
			if ov, ok := collinearOverlap(a, b); ok && ov > pcbDupOverlap {
				out = append(out, pcbCheckFinding{
					Type: "duplicate-segment", Level: "WARN", Net: a.Net, Layer: a.Layer,
					Primitives: []string{a.ID, b.ID},
					Message:    fmt.Sprintf("collinear overlapping copper (%.0f mil overlap) — redundant/mergeable", ov),
				})
			}
		}
	}
	return out
}

// collinearOverlap returns the overlap length of two segments if they are
// collinear (both of b's endpoints lie on a's infinite line), else ok=false.
func collinearOverlap(a, b pcbTrack) (float64, bool) {
	dx, dy := a.X2-a.X1, a.Y2-a.Y1
	la := math.Hypot(dx, dy)
	if la < 1e-9 {
		return 0, false
	}
	// b endpoints must be on a's line
	if pointLineDist(b.X1, b.Y1, a) > pcbOnSegEps || pointLineDist(b.X2, b.Y2, a) > pcbOnSegEps {
		return 0, false
	}
	// project all four endpoints onto a's unit direction
	ux, uy := dx/la, dy/la
	proj := func(x, y float64) float64 { return (x-a.X1)*ux + (y-a.Y1)*uy }
	a0, a1 := 0.0, la
	b0, b1 := proj(b.X1, b.Y1), proj(b.X2, b.Y2)
	if b0 > b1 {
		b0, b1 = b1, b0
	}
	lo := math.Max(math.Min(a0, a1), b0)
	hi := math.Min(math.Max(a0, a1), b1)
	if hi <= lo {
		return 0, false
	}
	return hi - lo, true
}

// ── R8: 3W parallel-coupling ────────────────────────────────────────────────
// Two DIFFERENT-net, same-layer segments running near-parallel with a
// center-to-center gap below couplingW×maxWidth, over a meaningful overlap, are
// a crosstalk / manufacturing-spacing risk (the classic 3W rule). Same-net pairs
// (intentional) and power/GND (poured, not coupled tracks) are skipped.
func findParallelCoupling(tracks []pcbTrack, couplingW float64) []pcbCheckFinding {
	var out []pcbCheckFinding
	for i := 0; i < len(tracks); i++ {
		a := tracks[i]
		if isGlobalNet(a.Net) {
			continue
		}
		for j := i + 1; j < len(tracks); j++ {
			b := tracks[j]
			if a.Net == b.Net || a.Layer != b.Layer || isGlobalNet(b.Net) {
				continue
			}
			gap, ovlp, ok := parallelGap(a, b)
			if !ok || ovlp < pcbCouplingMinOvlp {
				continue
			}
			need := couplingW * math.Max(a.Width, b.Width)
			if gap < need {
				na, nb := a.Net, b.Net
				if nb < na {
					na, nb = nb, na
				}
				out = append(out, pcbCheckFinding{
					Type: "parallel-coupling", Level: "WARN", Nets: []string{na, nb}, Layer: a.Layer,
					Primitives: []string{a.ID, b.ID},
					Message: fmt.Sprintf("parallel traces %.1f mil apart over %.0f mil (< %.1f mil = %.0f×W) — crosstalk/3W risk",
						gap, ovlp, need, couplingW),
				})
			}
		}
	}
	return out
}

// ── R9: silkscreen orientation (flipped / back-side text) ───────────────────
// A silkscreen text is "放反" (placed reversed) when it can't be read from the side
// it belongs to. Two independent failure modes, both ERROR (a mirrored designator
// is a real, ship-stopping artwork defect):
//
//  1. side mismatch — a component's designator sits on the OPPOSITE silk layer from
//     its footprint (component on TOP but its designator on BOTTOM_SILKSCREEN, or
//     vice-versa). The label ends up on the wrong side of the board.
//  2. mirror mismatch — the text's mirror flag doesn't match its silk layer. Top
//     silk must read un-mirrored; bottom silk must be mirrored (so it reads
//     correctly when the board is viewed from the bottom). Either way wrong = the
//     text renders backwards.
//
// Free strings (no parent component) only get the mirror check.
func findSilkscreenFlipped(silk []pcbSilkText) []pcbCheckFinding {
	sideName := func(l int) string {
		switch l {
		case pcbSideTop, silkTopLayer:
			return "top"
		case pcbSideBottom, silkBottomLayer:
			return "bottom"
		}
		return "?"
	}
	var out []pcbCheckFinding
	for _, s := range silk {
		if s.Layer != silkTopLayer && s.Layer != silkBottomLayer {
			continue // not a silkscreen text
		}
		label := s.Text
		if label == "" {
			label = s.ID
		}
		// 1. designator on the wrong silk side vs its component.
		if s.Kind == "attribute" && (s.CompLayer == pcbSideTop || s.CompLayer == pcbSideBottom) {
			wantSilk := silkTopLayer
			if s.CompLayer == pcbSideBottom {
				wantSilk = silkBottomLayer
			}
			if s.Layer != wantSilk {
				out = append(out, pcbCheckFinding{
					Type: "silkscreen-flipped", Level: "ERROR", Layer: s.Layer, Designator: label,
					Primitives: []string{s.ID}, At: &pcbXY{round2(s.X), round2(s.Y)},
					Message: fmt.Sprintf("designator '%s' is on the %s silkscreen but its component sits on the %s side — silkscreen flipped to the wrong side",
						label, sideName(s.Layer), sideName(s.CompLayer)),
				})
				continue
			}
		}
		// 2. mirror/reverse doesn't match the silk layer → text reads backwards.
		//    Top silk must read un-flipped; bottom silk must be flipped (so it reads
		//    right viewed from the bottom). Either Mirror or Reverse = flipped.
		flipped := s.Mirror || s.Reverse
		wantFlipped := s.Layer == silkBottomLayer
		if flipped != wantFlipped {
			state := "mirrored/reversed"
			if !flipped {
				state = "un-mirrored"
			}
			out = append(out, pcbCheckFinding{
				Type: "silkscreen-flipped", Level: "ERROR", Layer: s.Layer, Designator: label,
				Primitives: []string{s.ID}, At: &pcbXY{round2(s.X), round2(s.Y)},
				Message: fmt.Sprintf("silkscreen text '%s' on the %s silk is %s — it reads backwards (放反)",
					label, sideName(s.Layer), state),
			})
			continue
		}
		// 3. designator ORIENTATION: a reference designator (位号) should read UPRIGHT
		//    (0°). Rotated = upside-down (180°) or sideways (90/270°) — awkward to read
		//    on the assembled board. WARN (readable, but non-standard); designators only.
		if s.Kind == "attribute" && s.Key == "Designator" {
			if rot := normDeg(s.Rotation); rot != 0 {
				ori := "sideways (侧向)"
				if rot == 180 {
					ori = "upside-down (上下颠倒)"
				}
				out = append(out, pcbCheckFinding{
					Type: "silkscreen-flipped", Level: "WARN", Layer: s.Layer, Designator: label,
					Primitives: []string{s.ID}, At: &pcbXY{round2(s.X), round2(s.Y)},
					Message: fmt.Sprintf("designator '%s' is rotated %g° (%s) — not upright (放反/朝向不正)", label, rot, ori),
				})
			}
		}
	}
	return out
}

// ── R11: netless copper pour ────────────────────────────────────────────────
// A copper pour with an empty net is DEAD copper — it connects to nothing yet
// occupies board area (issue #34: a net:"" layer-1 pour left by `pcb pour`
// without --net). It's confusing (looks like a real plane), and `pour-fit
// --replace` can't clear it because that only matches same-net pours. Flag every
// pour whose net is empty so it can be removed with `pcb pour-clean --netless`.

type pcbPourP struct {
	ID    string
	Net   string
	Layer int
}

func findNetlessPours(pours []pcbPourP) []pcbCheckFinding {
	var out []pcbCheckFinding
	for _, p := range pours {
		if strings.TrimSpace(p.Net) != "" {
			continue
		}
		f := pcbCheckFinding{
			Type: "netless-pour", Level: "WARN", Layer: p.Layer,
			Message: "netless copper pour (dead copper — bound to no net); remove with `pcb pour-clean --netless` or re-pour with a net",
		}
		if p.ID != "" {
			f.Primitives = []string{p.ID}
		}
		out = append(out, f)
	}
	return out
}

// ── R12: via crossing an inner PLANE (内电层) on a foreign net ──────────────
// Official defect easyeda/pro-api-sdk#32: once an inner layer is 内电层/PLANE, a
// via created AFTERWARDS on a different net does NOT get an anti-pad cut into
// the negative plane — DRC reports Plane Zone to Via + Hole to Plane Zone, and
// `pour-rebuild` alone does not repair it. The via list exposes no anti-pad or
// creation-order data, so this is a BEST-EFFORT guard: every via whose net
// differs from the plane's net is flagged WARN with the fix guidance. Known
// edges: a via placed BEFORE the plane flip (proper anti-pad, clean DRC) is
// flagged too — cross-check with `pcb drc`, only vias that also show Plane Zone
// errors are actually broken; blind/buried vias that don't reach the plane are
// indistinguishable from through vias (all flagged); a PLANE layer with no
// net-bound pour has an unknown net and gets its own WARN instead of via checks.

// pcbPlaneLayer is one inner copper layer of type PLANE (内电层), plus the
// net(s) bound to it via its pour(s).
type pcbPlaneLayer struct {
	Layer int
	Name  string
	Nets  []string
}

// bindPlaneNets attaches each plane's net(s) from the pours on its layer
// (netless pours don't bind — R11 flags those separately).
func bindPlaneNets(planes []pcbPlaneLayer, pours []pcbPourP) []pcbPlaneLayer {
	for i := range planes {
		seen := map[string]bool{}
		for _, p := range pours {
			net := strings.TrimSpace(p.Net)
			if p.Layer != planes[i].Layer || net == "" || seen[net] {
				continue
			}
			seen[net] = true
			planes[i].Nets = append(planes[i].Nets, net)
		}
		sort.Strings(planes[i].Nets)
	}
	return planes
}

func findViaCrossesPlane(vias []pcbViaP, planes []pcbPlaneLayer) []pcbCheckFinding {
	var out []pcbCheckFinding
	for _, pl := range planes {
		name := pl.Name
		if name == "" {
			name = fmt.Sprintf("layer %d", pl.Layer)
		}
		if len(pl.Nets) == 0 {
			out = append(out, pcbCheckFinding{
				Type: "via-crosses-plane", Level: "WARN", Layer: pl.Layer,
				Message: fmt.Sprintf("inner PLANE %s has no net-bound pour — plane net unknown; pour the net while the layer is still SIGNAL, then flip to PLANE and pour-rebuild (`pcb power-planes` does this)", name),
			})
			continue
		}
		netSet := map[string]bool{}
		for _, n := range pl.Nets {
			netSet[n] = true
		}
		for _, v := range vias {
			if netSet[v.Net] {
				continue
			}
			vnet := v.Net
			if strings.TrimSpace(vnet) == "" {
				vnet = "(no net)"
			}
			out = append(out, pcbCheckFinding{
				Type: "via-crosses-plane", Level: "WARN", Net: v.Net, Layer: pl.Layer,
				Primitives: []string{v.ID}, At: &pcbXY{round2(v.X), round2(v.Y)},
				Message: fmt.Sprintf("via (net %s) crosses inner PLANE %s (net %s) — a via created after the plane existed gets NO anti-pad (easyeda/pro-api-sdk#32; DRC: Plane Zone to Via / Hole to Plane Zone); prefer removing it and routing on outer layers, or `easyeda doc reload` then `pcb pour-rebuild`, then confirm with `pcb drc`",
					vnet, name, strings.Join(pl.Nets, ",")),
			})
		}
	}
	return out
}

// fetchPcbPlaneLayers reads the stackup (pcb.layers.list) and returns the
// copper layers whose type is PLANE (内电层). Net binding comes from the pours
// (bindPlaneNets) — the layer item itself carries no net.
func fetchPcbPlaneLayers(cfg *appConfig, window string) ([]pcbPlaneLayer, error) {
	res, err := requestAction(cfg, "pcb.layers.list", window, nil)
	if err != nil {
		return nil, err
	}
	var planes []pcbPlaneLayer
	for _, rl := range mnavSlice(res.Result, "layers") {
		lm, ok := rl.(map[string]any)
		if !ok {
			continue
		}
		if typ, _ := lm["type"].(string); typ != "PLANE" {
			continue
		}
		idf, _ := asFloatOK(lm["id"])
		name, _ := lm["name"].(string)
		planes = append(planes, pcbPlaneLayer{Layer: int(idf), Name: name})
	}
	return planes, nil
}

func fetchPcbPours(cfg *appConfig, window string) ([]pcbPourP, error) {
	res, err := requestAction(cfg, "pcb.pour.list", window, nil)
	if err != nil {
		return nil, err
	}
	rawPours, _ := mnav(res.Result, "pours").([]any)
	var pours []pcbPourP
	for _, rp := range rawPours {
		pm, ok := rp.(map[string]any)
		if !ok {
			continue
		}
		id, _ := pm["primitiveId"].(string)
		net, _ := pm["net"].(string)
		layer, _ := asFloatOK(pm["layer"])
		pours = append(pours, pcbPourP{ID: id, Net: net, Layer: int(layer)})
	}
	return pours, nil
}

// ── R10: antenna keep-out ───────────────────────────────────────────────────
// A component that carries an RF antenna (an ESP WROOM/WROVER module, or a part
// named/designated as an antenna) needs a NO-COPPER keep-out under/around its
// antenna — copper (pour/plane/track) there detunes the antenna. We flag an
// antenna component whose footprint is NOT overlapped by any no-copper keep-out
// region (pcb_PrimitiveRegion carrying no-pours/no-fills/no-wires/no-inner-plane).

type pcbAntComp struct {
	Designator string
	Device     string
	BBox       *layoutBBox
}

type pcbKeepRegion struct {
	BBox              *layoutBBox
	Layer             int  // the region's copper layer (TOP=1 / BOTTOM=2 / inner id)
	NoOuterCopper     bool // excludes wires/fills/pours on its own layer (rules 5/6/7)
	NoInnerElectrical bool // excludes inner planes on ALL inner layers (rule 8)
	Name              string
}

// isAntennaDevice reports whether a device name / designator indicates an
// antenna-bearing part (integrated-antenna modules + discrete antennas).
func isAntennaDevice(device, designator string) bool {
	d := strings.ToUpper(device)
	for _, kw := range []string{"WROOM", "WROVER", "ANTENNA", "ESP32-C3-MINI", "ESP8266"} {
		if strings.Contains(d, kw) {
			return true
		}
	}
	u := strings.ToUpper(designator)
	return strings.HasPrefix(u, "ANT")
}

// findAntennaKeepout requires a no-copper keep-out over the antenna footprint on
// EVERY copper layer — not just "a region exists". A top-only keep-out still lets
// the bottom pour / inner planes fill under the antenna and detune it. copperLayers
// gates the inner-plane requirement (a 2-layer board has none).
func findAntennaKeepout(ants []pcbAntComp, regions []pcbKeepRegion, copperLayers int) []pcbCheckFinding {
	var out []pcbCheckFinding
	for _, a := range ants {
		if a.BBox == nil {
			continue
		}
		topOK, botOK, innerOK := false, false, false
		for _, r := range regions {
			if r.BBox == nil || !boxesOverlap(*a.BBox, *r.BBox) {
				continue
			}
			// A MULTI-layer (12/多层) no-copper keep-out covers EVERY copper layer at
			// once — top, bottom, AND inner — the simplest one-region way to protect an
			// antenna (no need for a per-layer region on each).
			if r.NoOuterCopper && r.Layer == pcbLayerMulti {
				topOK, botOK, innerOK = true, true, true
			}
			if r.NoOuterCopper && r.Layer == pcbSideTop {
				topOK = true
			}
			if r.NoOuterCopper && r.Layer == pcbSideBottom {
				botOK = true
			}
			if r.NoInnerElectrical {
				innerOK = true
			}
		}
		var missing []string
		if !topOK {
			missing = append(missing, "top (L1)")
		}
		if !botOK {
			missing = append(missing, "bottom (L2)")
		}
		if copperLayers > 2 && !innerOK {
			missing = append(missing, "inner plane")
		}
		if len(missing) > 0 {
			dev := a.Device
			if dev == "" {
				dev = "antenna part"
			}
			out = append(out, pcbCheckFinding{
				Type: "antenna-keepout", Level: "WARN", Designator: a.Designator,
				Message: fmt.Sprintf("antenna component %s (%s) lacks a no-copper keep-out on: %s — copper there detunes the antenna (每层都要禁铺铜)",
					a.Designator, dev, strings.Join(missing, ", ")),
			})
		}
	}
	return out
}

// normDeg normalizes an angle to [0,360), rounded to a whole degree (kills float
// noise like 449.99 → 90).
func normDeg(d float64) float64 {
	r := math.Mod(math.Round(d), 360)
	if r < 0 {
		r += 360
	}
	return r
}

// parallelGap reports, for two near-parallel segments, their center-to-center
// perpendicular gap and the length over which they run parallel (their overlap
// projected onto a's direction). ok=false when they are not parallel.
func parallelGap(a, b pcbTrack) (gap, overlap float64, ok bool) {
	adx, ady := a.X2-a.X1, a.Y2-a.Y1
	bdx, bdy := b.X2-b.X1, b.Y2-b.Y1
	la, lb := math.Hypot(adx, ady), math.Hypot(bdx, bdy)
	if la < 1e-9 || lb < 1e-9 {
		return 0, 0, false
	}
	ang := angleBetween(adx, ady, bdx, bdy)
	if ang > pcbParallelDeg && ang < 180-pcbParallelDeg {
		return 0, 0, false // not parallel nor anti-parallel
	}
	// unit direction of a; project b's endpoints + a's endpoints onto it.
	ux, uy := adx/la, ady/la
	proj := func(x, y float64) float64 { return (x-a.X1)*ux + (y-a.Y1)*uy }
	a0, a1 := 0.0, la
	b0, b1 := proj(b.X1, b.Y1), proj(b.X2, b.Y2)
	if b0 > b1 {
		b0, b1 = b1, b0
	}
	lo := math.Max(math.Min(a0, a1), b0)
	hi := math.Min(math.Max(a0, a1), b1)
	overlap = hi - lo
	if overlap <= 0 {
		return 0, 0, false // parallel but don't run alongside each other
	}
	// gap = the MINIMUM separation over the overlap, not the midpoint. Within the 15°
	// parallel window traces can DIVERGE (a wedge): nearly touching at one end, far at
	// the other. The midpoint would miss that — the tight end is the real coupling risk.
	// Sample a's line across the overlap and take the closest approach to b's segment.
	const samples = 16
	gap = math.Inf(1)
	for k := 0; k <= samples; k++ {
		t := lo + (hi-lo)*float64(k)/float64(samples)
		amx, amy := a.X1+ux*t, a.Y1+uy*t
		if d := segPtDist(amx, amy, b.X1, b.Y1, b.X2, b.Y2); d < gap {
			gap = d
		}
	}
	return gap, overlap, true
}

// segLen is a segment's length.
func segLen(t pcbTrack) float64 { return math.Hypot(t.X2-t.X1, t.Y2-t.Y1) }

// ── geometry helpers ────────────────────────────────────────────────────────

// segPtDist is the distance from (px,py) to segment (ax,ay)-(bx,by). Unlike the
// axis-aligned pointSegDist in cmd_sch_autoconnect.go, this handles arbitrary
// (e.g. 45°) segments, which routed copper needs.
func segPtDist(px, py, ax, ay, bx, by float64) float64 {
	dx, dy := bx-ax, by-ay
	l2 := dx*dx + dy*dy
	if l2 < 1e-12 {
		return math.Hypot(px-ax, py-ay)
	}
	t := ((px-ax)*dx + (py-ay)*dy) / l2
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	return math.Hypot(px-(ax+t*dx), py-(ay+t*dy))
}

// pointLineDist is the perpendicular distance from (px,py) to a's infinite line.
func pointLineDist(px, py float64, a pcbTrack) float64 {
	dx, dy := a.X2-a.X1, a.Y2-a.Y1
	l := math.Hypot(dx, dy)
	if l < 1e-9 {
		return math.Hypot(px-a.X1, py-a.Y1)
	}
	return math.Abs((px-a.X1)*dy-(py-a.Y1)*dx) / l
}

// angleBetween returns the angle in degrees [0,180] between two vectors.
func angleBetween(ax, ay, bx, by float64) float64 {
	la, lb := math.Hypot(ax, ay), math.Hypot(bx, by)
	if la < 1e-9 || lb < 1e-9 {
		return 180
	}
	c := (ax*bx + ay*by) / (la * lb)
	if c > 1 {
		c = 1
	} else if c < -1 {
		c = -1
	}
	return math.Acos(c) * 180 / math.Pi
}

func uniqStr(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// ── live fetch / render ──────────────────────────────────────────────────────

// runPcbCheck pulls placed copper (tracks + vias + pads), runs the DFM audit,
// renders it, and (with strict) returns a non-zero exit when there are findings.
func runPcbCheck(cfg *appConfig, window string, couplingW float64, strict, asJSON bool, stdout, stderr io.Writer) error {
	pads, err := fetchPcbPads(cfg, window)
	if err != nil {
		return fmt.Errorf("fetch PCB pads: %w", err)
	}
	tracks, err := fetchPcbTracks(cfg, window)
	if err != nil {
		return fmt.Errorf("fetch PCB tracks: %w", err)
	}
	vias, err := fetchPcbVias(cfg, window)
	if err != nil {
		return fmt.Errorf("fetch PCB vias: %w", err)
	}
	// Silkscreen is OPTIONAL: the silk rule needs the pcb.silk.list connector handler.
	// On an older connector (before it was added) this errors "Unknown action" — degrade
	// gracefully (run the copper rules, skip silk) instead of failing the whole audit.
	silk, err := fetchPcbSilk(cfg, window)
	if err != nil {
		fmt.Fprintf(stderr, "warning: silkscreen-flipped check skipped (%v) — update the connector to enable it\n", err)
		silk = nil
	}

	rep := analyzePcbCheckFull(pads, tracks, vias, silk, couplingW)

	// Antenna keep-out is a LIVE-only rule (needs component bboxes + regions, which
	// the pure core doesn't take). Degrade gracefully if either fetch fails.
	if ants, regions, copperLayers, aerr := fetchAntennaContext(cfg, window, silk); aerr != nil {
		fmt.Fprintf(stderr, "warning: antenna keep-out check skipped (%v)\n", aerr)
	} else {
		for _, f := range findAntennaKeepout(ants, regions, copperLayers) {
			rep.Findings = append(rep.Findings, f)
			rep.Summary.AntennaKeepout++
			rep.Summary.Warnings++
			rep.Summary.Total++
		}
		rep.Passed = rep.Summary.Total == 0
	}

	// Netless-pour + via-crosses-plane are LIVE-only rules (they need the pour
	// list / the stackup, which the pure copper core doesn't take). Degrade
	// gracefully if a fetch fails.
	if pours, perr := fetchPcbPours(cfg, window); perr != nil {
		fmt.Fprintf(stderr, "warning: netless-pour + via-crosses-plane checks skipped (%v)\n", perr)
	} else {
		for _, f := range findNetlessPours(pours) {
			rep.Findings = append(rep.Findings, f)
			rep.Summary.NetlessPours++
			rep.Summary.Warnings++
			rep.Summary.Total++
		}
		if planes, lerr := fetchPcbPlaneLayers(cfg, window); lerr != nil {
			fmt.Fprintf(stderr, "warning: via-crosses-plane check skipped (%v)\n", lerr)
		} else if len(planes) > 0 {
			for _, f := range findViaCrossesPlane(vias, bindPlaneNets(planes, pours)) {
				rep.Findings = append(rep.Findings, f)
				rep.Summary.ViaCrossesPlane++
				rep.Summary.Warnings++
				rep.Summary.Total++
			}
		}
		rep.Passed = rep.Summary.Total == 0
	}

	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			return err
		}
	} else {
		renderPcbCheckReport(rep, stdout)
	}

	if strict && rep.Summary.Warnings > 0 {
		return fmt.Errorf("pcb check: %d issue(s) (--strict)", rep.Summary.Warnings)
	}
	return nil
}

func fetchPcbPads(cfg *appConfig, window string) ([]pcbPadP, error) {
	res, err := requestAction(cfg, "pcb.components.list", window, map[string]any{"includePads": true})
	if err != nil {
		return nil, err
	}
	rawComps, _ := mnav(res.Result, "components").([]any)
	var pads []pcbPadP
	for _, rc := range rawComps {
		cm, ok := rc.(map[string]any)
		if !ok {
			continue
		}
		desig, _ := cm["designator"].(string)
		rawPads, _ := cm["pads"].([]any)
		for _, rp := range rawPads {
			pm, ok := rp.(map[string]any)
			if !ok {
				continue
			}
			net, _ := pm["net"].(string)
			num, _ := pm["padNumber"].(string)
			x, _ := asFloatOK(pm["x"])
			y, _ := asFloatOK(pm["y"])
			layer, _ := asFloatOK(pm["layer"])
			pads = append(pads, pcbPadP{Designator: desig, Number: num, Net: net, Layer: int(layer), X: x, Y: y})
		}
	}
	return pads, nil
}

func fetchPcbTracks(cfg *appConfig, window string) ([]pcbTrack, error) {
	res, err := requestAction(cfg, "pcb.line.list", window, nil)
	if err != nil {
		return nil, err
	}
	rawLines, _ := mnav(res.Result, "lines").([]any)
	var tracks []pcbTrack
	for _, rl := range rawLines {
		lm, ok := rl.(map[string]any)
		if !ok {
			continue
		}
		id, _ := lm["primitiveId"].(string)
		net, _ := lm["net"].(string)
		layer, _ := asFloatOK(lm["layer"])
		x1, _ := asFloatOK(lm["startX"])
		y1, _ := asFloatOK(lm["startY"])
		x2, _ := asFloatOK(lm["endX"])
		y2, _ := asFloatOK(lm["endY"])
		w, _ := asFloatOK(lm["lineWidth"])
		tracks = append(tracks, pcbTrack{ID: id, Net: net, Layer: int(layer), X1: x1, Y1: y1, X2: x2, Y2: y2, Width: w})
	}
	return tracks, nil
}

func fetchPcbVias(cfg *appConfig, window string) ([]pcbViaP, error) {
	res, err := requestAction(cfg, "pcb.via.list", window, nil)
	if err != nil {
		return nil, err
	}
	rawVias, _ := mnav(res.Result, "vias").([]any)
	var vias []pcbViaP
	for _, rv := range rawVias {
		vm, ok := rv.(map[string]any)
		if !ok {
			continue
		}
		id, _ := vm["primitiveId"].(string)
		net, _ := vm["net"].(string)
		x, _ := asFloatOK(vm["x"])
		y, _ := asFloatOK(vm["y"])
		hole, _ := asFloatOK(vm["holeDiameter"])
		dia, _ := asFloatOK(vm["diameter"])
		vias = append(vias, pcbViaP{ID: id, Net: net, X: x, Y: y, Hole: hole, Dia: dia})
	}
	return vias, nil
}

func fetchPcbSilk(cfg *appConfig, window string) ([]pcbSilkText, error) {
	res, err := requestAction(cfg, "pcb.silk.list", window, nil)
	if err != nil {
		return nil, err
	}
	rawTexts, _ := mnav(res.Result, "texts").([]any)
	var silk []pcbSilkText
	for _, rt := range rawTexts {
		tm, ok := rt.(map[string]any)
		if !ok {
			continue
		}
		id, _ := tm["primitiveId"].(string)
		kind, _ := tm["kind"].(string)
		key, _ := tm["key"].(string)
		text, _ := tm["text"].(string)
		layer, _ := asFloatOK(tm["layer"])
		mirror, _ := tm["mirror"].(bool)
		reverse, _ := tm["reverse"].(bool)
		rotation, _ := asFloatOK(tm["rotation"])
		compID, _ := tm["componentId"].(string)
		compLayer, _ := asFloatOK(tm["componentLayer"])
		x, _ := asFloatOK(tm["x"])
		y, _ := asFloatOK(tm["y"])
		silk = append(silk, pcbSilkText{
			ID: id, Kind: kind, Key: key, Text: text, Layer: int(layer), Mirror: mirror,
			Reverse: reverse, Rotation: rotation, CompID: compID, CompLayer: int(compLayer), X: x, Y: y,
		})
	}
	return silk, nil
}

// fetchAntennaContext resolves antenna-bearing components (by device name from the
// silk Device attribute, or an ANT* designator) with their bboxes, plus every
// keep-out region with its bbox + whether it excludes copper.
func fetchAntennaContext(cfg *appConfig, window string, silk []pcbSilkText) ([]pcbAntComp, []pcbKeepRegion, int, error) {
	// copper layer count (gates the inner-plane keep-out requirement).
	copperLayers := 2
	if lres, err := requestAction(cfg, "pcb.layers.list", window, nil); err == nil {
		if n, ok := asFloatOK(mnav(lres.Result, "copperLayerCount")); ok && n > 0 {
			copperLayers = int(n)
		}
	}
	// device name per component id, from the silk Device attribute.
	devByComp := map[string]string{}
	for _, s := range silk {
		if s.Kind == "attribute" && s.Key == "Device" && s.CompID != "" {
			devByComp[s.CompID] = s.Text
		}
	}

	cres, err := requestAction(cfg, "pcb.components.list", window, map[string]any{"includeBBox": true})
	if err != nil {
		return nil, nil, copperLayers, err
	}
	var ants []pcbAntComp
	for _, rc := range mnavSlice(cres.Result, "components") {
		cm, ok := rc.(map[string]any)
		if !ok {
			continue
		}
		id, _ := cm["primitiveId"].(string)
		desig, _ := cm["designator"].(string)
		device := devByComp[id]
		if !isAntennaDevice(device, desig) {
			continue
		}
		ac := pcbAntComp{Designator: desig, Device: device}
		if bb, ok := cm["bbox"].(map[string]any); ok {
			minX, _ := asFloatOK(bb["minX"])
			minY, _ := asFloatOK(bb["minY"])
			maxX, _ := asFloatOK(bb["maxX"])
			maxY, _ := asFloatOK(bb["maxY"])
			ac.BBox = &layoutBBox{MinX: minX, MinY: minY, MaxX: maxX, MaxY: maxY}
		}
		ants = append(ants, ac)
	}

	rres, err := requestAction(cfg, "pcb.region.list", window, nil)
	if err != nil {
		return nil, nil, copperLayers, err
	}
	var regions []pcbKeepRegion
	for _, rr := range mnavSlice(rres.Result, "regions") {
		rm, ok := rr.(map[string]any)
		if !ok {
			continue
		}
		kr := pcbKeepRegion{}
		if name, ok := rm["regionName"].(string); ok {
			kr.Name = name
		}
		if lf, ok := asFloatOK(rm["layer"]); ok {
			kr.Layer = int(lf)
		}
		// no-wires(5)/no-fills(6)/no-pours(7) exclude copper on the region's own layer;
		// no-inner-electrical(8) excludes copper on ALL inner plane layers.
		for _, rt := range toFloatSlice(rm["ruleType"]) {
			switch int(rt) {
			case 5, 6, 7:
				kr.NoOuterCopper = true
			case 8:
				kr.NoInnerElectrical = true
			}
		}
		if bb, ok := rm["bbox"].(map[string]any); ok {
			minX, _ := asFloatOK(bb["minX"])
			minY, _ := asFloatOK(bb["minY"])
			maxX, _ := asFloatOK(bb["maxX"])
			maxY, _ := asFloatOK(bb["maxY"])
			kr.BBox = &layoutBBox{MinX: minX, MinY: minY, MaxX: maxX, MaxY: maxY}
		}
		regions = append(regions, kr)
	}
	return ants, regions, copperLayers, nil
}

// mnavSlice is mnav + a []any assertion (nil on miss).
func mnavSlice(result map[string]any, key string) []any {
	s, _ := mnav(result, key).([]any)
	return s
}

// toFloatSlice coerces a JSON array of numbers to []float64.
func toFloatSlice(v any) []float64 {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]float64, 0, len(arr))
	for _, x := range arr {
		if f, ok := asFloatOK(x); ok {
			out = append(out, f)
		}
	}
	return out
}

func renderPcbCheckReport(rep pcbCheckReport, w io.Writer) {
	s := rep.Summary
	fmt.Fprintf(w, "PCB check (DFM): %d track(s), %d via(s), %d pad(s) — %d issue(s)\n",
		rep.TrackCount, rep.ViaCount, rep.PadCount, s.Total)
	if s.Total == 0 {
		fmt.Fprintln(w, "  ✓ no DFM issues found")
		return
	}
	fmt.Fprintf(w, "  ERROR=%d WARN=%d  |  dangling=%d acute=%d nonOrtho=%d overPad=%d silkFlipped=%d overlapVia=%d singleLayerVia=%d widthMismatch=%d dupSegment=%d coupling=%d antennaKeepout=%d netlessPour=%d viaCrossesPlane=%d\n",
		s.Errors, s.Warnings-s.Errors,
		s.DanglingEnds, s.AcuteAngles, s.NonOrthogonal, s.TrackOverPad, s.SilkscreenFlipped, s.OverlappingVias, s.SingleLayerVias, s.WidthMismatches, s.DuplicateSegments, s.ParallelCoupling, s.AntennaKeepout, s.NetlessPours, s.ViaCrossesPlane)
	for _, f := range rep.Findings {
		loc := ""
		if f.At != nil {
			loc = fmt.Sprintf(" @ (%.0f, %.0f)", f.At.X, f.At.Y)
		}
		net := f.Net
		if net == "" && len(f.Nets) > 0 {
			net = fmt.Sprintf("%v", f.Nets)
		}
		fmt.Fprintf(w, "  %-5s %-17s %s%s  [%s]\n", f.Level, f.Type, f.Message, loc, net)
	}
}
