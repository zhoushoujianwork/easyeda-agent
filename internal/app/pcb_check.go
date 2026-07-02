package app

// pcb check — reconstructed DFM (design-for-manufacture) audit.
//
// The PCB sibling of `sch check`. EasyEDA's native `pcb drc` catches
// rule-clearance violations (track-to-pad, width, hole); it does NOT flag the
// manufacturability / reliability hazards that the official DFM tools look for:
// acid-trap acute angles, dangling copper stubs, pointless single-layer vias,
// stacked/redundant vias, asymmetric neck-down on 2-pin parts, or duplicated
// overlapping copper. This recomputes all of that purely from the placed
// primitives (tracks + vias + pads) — no new connector handler, mirrors the
// Go-side geometry approach of `pcb layout-lint`.
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
)

// Geometry thresholds, all in mil (PCB primitives are native mil — pcb.line.create
// passes coordinates straight to eda.* with no scaling).
const (
	pcbCoincEps    = 2.0  // endpoint↔endpoint / ↔via / ↔pad-center coincidence
	pcbOnSegEps    = 1.5  // point-lies-on-track-body (T-junction / collinear test)
	pcbAcuteDeg    = 90.0 // interior trace angle below this = acid-trap acute corner
	pcbViaDupEps   = 2.0  // two vias closer than this = redundant/stacked
	pcbWidthTolMil = 1.0  // absolute width tolerance for 2-pin neck-down symmetry
	pcbWidthTolRel = 0.25 // relative width tolerance (25%)
	pcbDupOverlap  = 2.0  // collinear same-net overlap longer than this = duplicate copper
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
	OverlappingVias   int `json:"overlappingVias"`
	SingleLayerVias   int `json:"singleLayerVias"`
	WidthMismatches   int `json:"widthMismatches"`
	DuplicateSegments int `json:"duplicateSegments"`
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

// analyzePcbCheck is the pure DFM core over placed primitives.
func analyzePcbCheck(pads []pcbPadP, tracks []pcbTrack, vias []pcbViaP) pcbCheckReport {
	rep := pcbCheckReport{TrackCount: len(tracks), ViaCount: len(vias), PadCount: len(pads)}

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
	rep.Findings = append(rep.Findings, findViaIssues(tracks, vias)...)
	rep.Findings = append(rep.Findings, findWidthMismatch(tracks, pads)...)
	rep.Findings = append(rep.Findings, findDuplicateSegments(tracks)...)

	for _, f := range rep.Findings {
		switch f.Type {
		case "dangling-end":
			rep.Summary.DanglingEnds++
		case "acute-angle":
			rep.Summary.AcuteAngles++
		case "overlapping-via":
			rep.Summary.OverlappingVias++
		case "single-layer-via":
			rep.Summary.SingleLayerVias++
		case "width-mismatch":
			rep.Summary.WidthMismatches++
		case "duplicate-segment":
			rep.Summary.DuplicateSegments++
		}
		if f.Level == "WARN" || f.Level == "ERROR" {
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
			if anchored(px, py, i, tracks, vias, pads) {
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

func anchored(px, py float64, self int, tracks []pcbTrack, vias []pcbViaP, pads []pcbPadP) bool {
	for _, p := range pads {
		if math.Hypot(p.X-px, p.Y-py) <= pcbCoincEps {
			return true
		}
	}
	for _, v := range vias {
		if math.Hypot(v.X-px, v.Y-py) <= pcbCoincEps {
			return true
		}
	}
	for j, o := range tracks {
		if j == self {
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
// angle between them below 90° forms a sharp spike where etchant collects. 90°
// and 45° (135° interior) corners are fine; only < 90° is flagged.
func findAcuteAngles(tracks []pcbTrack) []pcbCheckFinding {
	type inc struct {
		dx, dy float64
		id     string
	}
	type vtx struct {
		net    string
		layer  int
		x, y   float64
		segs   []inc
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
		if minAng < pcbAcuteDeg-0.5 { // 0.5° tolerance so a clean 90° never trips
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

// ── R3: via issues — stacked/redundant + pointless single-layer ─────────────
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

// ── R4: 2-pin neck-down asymmetry ───────────────────────────────────────────
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
		w0, ok0 := maxTrackWidthAt(ps[0].X, ps[0].Y, tracks)
		w1, ok1 := maxTrackWidthAt(ps[1].X, ps[1].Y, tracks)
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

func maxTrackWidthAt(px, py float64, tracks []pcbTrack) (float64, bool) {
	best := 0.0
	found := false
	for _, t := range tracks {
		if math.Hypot(t.X1-px, t.Y1-py) <= pcbCoincEps || math.Hypot(t.X2-px, t.Y2-py) <= pcbCoincEps {
			if t.Width > best {
				best = t.Width
			}
			found = true
		}
	}
	return best, found
}

// ── R5: duplicated / overlapping copper ─────────────────────────────────────
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
func runPcbCheck(cfg *appConfig, window string, strict, asJSON bool, stdout, stderr io.Writer) error {
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

	rep := analyzePcbCheck(pads, tracks, vias)

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

func renderPcbCheckReport(rep pcbCheckReport, w io.Writer) {
	s := rep.Summary
	fmt.Fprintf(w, "PCB check (DFM): %d track(s), %d via(s), %d pad(s) — %d issue(s)\n",
		rep.TrackCount, rep.ViaCount, rep.PadCount, s.Total)
	if s.Total == 0 {
		fmt.Fprintln(w, "  ✓ no DFM issues found")
		return
	}
	fmt.Fprintf(w, "  dangling=%d acute=%d overlapVia=%d singleLayerVia=%d widthMismatch=%d dupSegment=%d\n",
		s.DanglingEnds, s.AcuteAngles, s.OverlappingVias, s.SingleLayerVias, s.WidthMismatches, s.DuplicateSegments)
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
