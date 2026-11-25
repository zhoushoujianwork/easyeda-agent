package app

// pcb_layout_sim.go — `easyeda pcb layout simulate` (offline).
//
// Takes a `pcb dump --include-copper` snapshot plus optional hand-placed poses
// for NON-satellite parts (chips, connectors, antennas), re-runs the auto-place
// planner (v2 packer + RF chain, see pcb_autoplace.go) around those poses and
// returns the resulting layout with a ratsnest and a few coarse metrics. It is
// pure computation: no connection to EasyEDA, no write to the board. The Desk
// app drives it on every drop and applies `moves` through its own typed write
// path; the contract is easyeda.pcb.layout-sim/v1 (units mil, y-up, degrees CCW,
// the same frame as `pcb dump`).

import (
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

const (
	simSchema  = "easyeda.pcb.layout-sim/v1"
	simGridMil = 50.0 // congestion grid cell; cells are [i*50,(i+1)*50) from the dump origin
	simAngEps  = 0.01 // deg
	simPosEps  = 0.01 // mil
)

// ── input ────────────────────────────────────────────────────────────────────

type lsPose struct {
	PrimitiveID string   `json:"primitiveId"`
	X           *float64 `json:"x"`
	Y           *float64 `json:"y"`
	Rotation    *float64 `json:"rotation"`
}

type lsPoseFile struct {
	BoardSemanticSHA256 string   `json:"boardSemanticSha256"`
	Poses               []lsPose `json:"poses"`
}

// ── output ───────────────────────────────────────────────────────────────────

type lsOutline struct {
	MinX   float64      `json:"minX"`
	MinY   float64      `json:"minY"`
	MaxX   float64      `json:"maxX"`
	MaxY   float64      `json:"maxY"`
	Points [][2]float64 `json:"points"`
}

type lsPad struct {
	Num string  `json:"num"`
	Net string  `json:"net"`
	X   float64 `json:"x"`
	Y   float64 `json:"y"`
	W   float64 `json:"w"`
	H   float64 `json:"h"`
}

type lsComponent struct {
	PrimitiveID string      `json:"primitiveId"`
	Designator  string      `json:"designator"`
	Name        string      `json:"name"`
	Role        string      `json:"role"`
	Owner       string      `json:"owner"`
	Layer       int         `json:"layer"`
	X           float64     `json:"x"`
	Y           float64     `json:"y"`
	Rotation    float64     `json:"rotation"`
	BBox        *layoutBBox `json:"bbox,omitempty"`
	Pads        []lsPad     `json:"pads"`
	Moved       bool        `json:"moved"`
}

type lsMove struct {
	PrimitiveID string  `json:"primitiveId"`
	Designator  string  `json:"designator"`
	X           float64 `json:"x"`
	Y           float64 `json:"y"`
	Rotation    float64 `json:"rotation"`
	SetRotation bool    `json:"setRotation"`
	Via         string  `json:"via"`
}

type lsRat struct {
	Net string     `json:"net"`
	A   [2]float64 `json:"a"`
	B   [2]float64 `json:"b"`
}

type lsMetrics struct {
	RatsnestMil    float64 `json:"ratsnestMil"`
	Crossings      int     `json:"crossings"`
	CongestionPeak int     `json:"congestionPeak"`
	Unplaced       int     `json:"unplaced"`
}

type lsIssue struct {
	Severity   string `json:"severity"`
	Designator string `json:"designator"`
	Message    string `json:"message"`
	Fix        string `json:"fix"`
}

type lsResult struct {
	Schema              string        `json:"schema"`
	BoardSemanticSHA256 string        `json:"boardSemanticSha256"`
	Outline             lsOutline     `json:"outline"`
	Keepouts            []layoutBBox  `json:"keepouts"`
	Components          []lsComponent `json:"components"`
	Moves               []lsMove      `json:"moves"`
	Ratsnest            []lsRat       `json:"ratsnest"`
	Metrics             lsMetrics     `json:"metrics"`
	Issues              []lsIssue     `json:"issues"`
}

// ── command ──────────────────────────────────────────────────────────────────

func newPcbLayoutSimulateCmd(stdout, stderr io.Writer) *cobra.Command {
	var boardPath, posesPath, outPath string
	cmd := &cobra.Command{
		Use:   "simulate",
		Short: "Re-plan satellites around hand-placed chips/connectors/antennas and report the result (offline)",
		Long: `Simulate a placement change without touching the board.

--board is a fresh 'easyeda pcb dump --include-copper' JSON (it must carry a
non-empty semanticSha256). --poses optionally places NON-satellite parts (main
chips, connectors, antennas, other unlocked parts) at a new anchor position and a
rotation that is a multiple of 90 degrees. The simulator rotates and translates
each posed part's anchor, bbox and pads about its anchor, then runs the same
planner as 'pcb auto-place' (v2 cost-based packer + RF matching chain) around the
posed parts and writes the final layout to --out (easyeda.pcb.layout-sim/v1).

Satellites (what the planner moves: caps, resistors, LEDs, crystals, RF matching
parts), locked parts and unknown ids cannot be posed: the command fails naming
them. Without --poses the board is simulated as-is, with satellites re-planned
around the current chip positions.

poses.json:
  {"boardSemanticSha256":"<must equal the board's semanticSha256>",
   "poses":[{"primitiveId":"...","x":1200,"y":-300,"rotation":90}]}

result.json (units mil, y-up, degrees CCW, same frame as pcb dump):
  outline, keepouts          board bounds / keep-out boxes the planner used
  components                 final pose of EVERY part: role (main|satellite|
                             connector|antenna|fixed|other), owner (a satellite's
                             chip; a chip owns itself; an antenna's RF-chain chip),
                             x/y/rotation, bbox, pads, moved
  moves                      parts whose final pose differs from the dump (posed
                             parts via "pose", re-planned satellites via the
                             planner's via), sorted by designator
  ratsnest                   per-net minimum spanning tree over pad centres
  metrics                    ratsnestMil (total length), crossings (segment pairs
                             of different nets that properly cross),
                             congestionPeak (max over a 50 mil grid of pad centres
                             plus ratsnest segments touching a cell), unplaced
                             (satellites the planner could not place)
  issues                     planner diagnostics as P1 (unplaced / no free slot),
                             P2 (over a distance limit, off the RF feed line) or
                             P3 (information), sorted by severity

Planner inputs are the 'pcb auto-place' defaults (gap 40, pitch 30 mil) with three
differences: 'pcb auto-place' derives gap/pitch from the live DRC rules, which
this offline command does not read; main chips are never re-spaced (they move
only by an explicit pose); and PCB virtual-group fixing is unavailable offline
(only locked parts are fixed). Keep-outs come from the dump's copper.regions and
the board bounds from its outline; a dump without them simulates without them
and a P3 issue says so.

This command is pure local computation: it never connects to EasyEDA and never
moves a component. Output is deterministic: identical inputs give identical bytes.`,
		Example: `  easyeda pcb dump --include-copper --out board.json
  easyeda pcb layout simulate --board board.json --out sim.json
  easyeda pcb layout simulate --board board.json --poses poses.json --out sim.json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if boardPath == "" || outPath == "" {
				return fmt.Errorf("--board and --out are required")
			}
			inputs := []string{boardPath}
			if posesPath != "" {
				inputs = append(inputs, posesPath)
			}
			if err := rejectLayoutOutputAliases(inputs, []string{outPath}); err != nil {
				return err
			}
			snap, _, err := readSolveBoard(boardPath)
			if err != nil {
				return err
			}
			if strings.TrimSpace(snap.SemanticSHA256) == "" {
				return fmt.Errorf("board snapshot has no semanticSha256; take a fresh `easyeda pcb dump --include-copper`")
			}
			var poses lsPoseFile
			if posesPath != "" {
				raw, err := os.ReadFile(posesPath)
				if err != nil {
					return fmt.Errorf("read poses: %w", err)
				}
				if err := decodeStrict(raw, &poses); err != nil {
					return fmt.Errorf("decode poses: %w", err)
				}
			}
			res, err := simulatePcbLayout(snap, &poses, posesPath != "")
			if err != nil {
				return err
			}
			if err := writeJSONAtomic(outPath, res); err != nil { // creates the directory, replaces atomically
				return err
			}
			fmt.Fprintf(stderr, "pcb layout simulate: %d components, %d moves, %d issues → %s\n", len(res.Components), len(res.Moves), len(res.Issues), outPath)
			fmt.Fprintln(stdout, outPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&boardPath, "board", "", "fresh pcb dump --include-copper JSON (must carry semanticSha256)")
	cmd.Flags().StringVar(&posesPath, "poses", "", "optional poses JSON: non-satellite parts placed by hand (bound to the board's semanticSha256)")
	cmd.Flags().StringVar(&outPath, "out", "", "result JSON output path (easyeda.pcb.layout-sim/v1)")
	return cmd
}

// ── geometry ─────────────────────────────────────────────────────────────────

func simNormDeg(r float64) float64 {
	r = math.Mod(r, 360)
	if r < 0 {
		r += 360
	}
	if r >= 360-1e-9 {
		r = 0
	}
	return r
}

// simAngleDiff is the smallest absolute difference between two angles (deg).
func simAngleDiff(a, b float64) float64 {
	d := math.Abs(simNormDeg(a) - simNormDeg(b))
	return math.Min(d, 360-d)
}

// simRotate rotates (x,y) by deg (y-up CCW), exactly for quarter turns.
func simRotate(x, y, deg float64) (float64, float64) {
	q := math.Round(deg / 90)
	if math.Abs(deg-q*90) < 1e-9 {
		switch ((int(q) % 4) + 4) % 4 {
		case 0:
			return x, y
		case 1:
			return -y, x
		case 2:
			return -x, -y
		default:
			return y, -x
		}
	}
	return rotateVec(x, y, deg)
}

// simTransform rotates c's anchor-relative geometry (bbox corners, pads) by
// newRot-c.rotation about the anchor and translates the anchor to (nx,ny). A pad
// extent is swapped on an odd quarter turn. The bbox stays axis-aligned (AABB of
// the rotated corners), exact for quarter turns.
func simTransform(c apComp, nx, ny, newRot float64) apComp {
	d := newRot - c.rotation
	out := c
	out.x, out.y, out.rotation = nx, ny, newRot
	at := func(px, py float64) (float64, float64) {
		vx, vy := simRotate(px-c.x, py-c.y, d)
		return nx + vx, ny + vy
	}
	if c.hasBBox {
		minX, minY := math.Inf(1), math.Inf(1)
		maxX, maxY := math.Inf(-1), math.Inf(-1)
		for _, corner := range [4][2]float64{{c.minX, c.minY}, {c.maxX, c.minY}, {c.maxX, c.maxY}, {c.minX, c.maxY}} {
			x, y := at(corner[0], corner[1])
			minX, maxX = math.Min(minX, x), math.Max(maxX, x)
			minY, maxY = math.Min(minY, y), math.Max(maxY, y)
		}
		out.minX, out.minY, out.maxX, out.maxY = minX, minY, maxX, maxY
	}
	out.pads = make([]apPad, len(c.pads))
	swap := math.Abs(d-math.Round(d/90)*90) < 1e-9 && int(math.Round(d/90))%2 != 0
	for i, p := range c.pads {
		p.x, p.y = at(p.x, p.y)
		if swap {
			p.w, p.h = p.h, p.w
		}
		out.pads[i] = p
	}
	return out
}

// ── designator order ─────────────────────────────────────────────────────────

var simDesRe = regexp.MustCompile(`^([^0-9]*)([0-9]+)(.*)$`)

// simDesLess orders designators naturally (C2 < C10) and totally.
func simDesLess(a, b string) bool {
	ma, mb := simDesRe.FindStringSubmatch(a), simDesRe.FindStringSubmatch(b)
	if ma != nil && mb != nil {
		if ma[1] != mb[1] {
			return ma[1] < mb[1]
		}
		na, _ := strconv.Atoi(ma[2])
		nb, _ := strconv.Atoi(mb[2])
		if na != nb {
			return na < nb
		}
		if ma[3] != mb[3] {
			return ma[3] < mb[3]
		}
	}
	return a < b
}

// ── simulation ───────────────────────────────────────────────────────────────

func simulatePcbLayout(snap *boardSnapshot, pf *lsPoseFile, havePoses bool) (*lsResult, error) {
	if snap == nil || strings.TrimSpace(snap.SemanticSHA256) == "" {
		return nil, fmt.Errorf("board snapshot has no semanticSha256; take a fresh `easyeda pcb dump --include-copper`")
	}
	n := len(snap.Components)
	ap := make([]apComp, n)
	byID := make(map[string]int, n)
	for i, c := range snap.Components {
		if strings.TrimSpace(c.ID) == "" {
			return nil, fmt.Errorf("component %q (#%d) has no primitiveId", c.Designator, i)
		}
		if _, dup := byID[c.ID]; dup {
			return nil, fmt.Errorf("primitiveId %q appears twice in the board snapshot", c.ID)
		}
		byID[c.ID] = i
		ap[i] = boardCompToAP(c)
	}

	// Planner inputs: same defaults as `pcb auto-place`; keep-outs and board
	// bounds from the dump instead of live calls.
	opt := defaultApOptions()
	opt.multiGap = 0 // chips move only by an explicit pose, never by re-spacing
	var issues []lsIssue
	keepouts := []layoutBBox{}
	if snap.Copper != nil && (snap.Copper.Availability["regions"] == "available" || len(snap.Copper.Regions) > 0) {
		for _, r := range parseAntennaKeepRegions(map[string]any{"regions": snap.Copper.Regions}) {
			if r.BBox != nil {
				keepouts = append(keepouts, *r.BBox)
			}
		}
	} else {
		issues = append(issues, lsIssue{Severity: "P3", Message: "the dump has no readable copper.regions: satellites were not kept out of any keep-out region", Fix: "take a fresh `easyeda pcb dump --include-copper`"})
	}
	sort.Slice(keepouts, func(i, j int) bool {
		a, b := keepouts[i], keepouts[j]
		if a.MinX != b.MinX {
			return a.MinX < b.MinX
		}
		if a.MinY != b.MinY {
			return a.MinY < b.MinY
		}
		if a.MaxX != b.MaxX {
			return a.MaxX < b.MaxX
		}
		return a.MaxY < b.MaxY
	})
	opt.keepouts = keepouts
	outline := lsOutline{Points: [][2]float64{}}
	if o := snap.Outline; o != nil && o.BBox.MaxX > o.BBox.MinX && o.BBox.MaxY > o.BBox.MinY {
		b := o.BBox
		opt.board = &layoutBBox{MinX: b.MinX, MinY: b.MinY, MaxX: b.MaxX, MaxY: b.MaxY}
		outline = lsOutline{MinX: round4(b.MinX), MinY: round4(b.MinY), MaxX: round4(b.MaxX), MaxY: round4(b.MaxY), Points: [][2]float64{}}
		for _, p := range o.Points {
			outline.Points = append(outline.Points, [2]float64{round4(p[0]), round4(p[1])})
		}
	} else {
		issues = append(issues, lsIssue{Severity: "P3", Message: "the dump has no usable board outline: satellites were not bounded by the board", Fix: "take a fresh `easyeda pcb dump --include-copper` with the board outline set"})
	}

	// Classify on the unposed board (pose-independent) and validate the poses.
	_, _, pre := planAutoPlaceDetailed(ap, opt)
	movable := map[int]bool{}
	for _, i := range pre.Movable {
		movable[i] = true
	}
	posed := ap
	posedIdx := map[int]bool{}
	if havePoses {
		if pf.BoardSemanticSHA256 != snap.SemanticSHA256 {
			return nil, fmt.Errorf("poses boardSemanticSha256 %q does not match the board's semanticSha256 %q; re-sync: take a fresh `easyeda pcb dump --include-copper` and recompute the poses", pf.BoardSemanticSHA256, snap.SemanticSHA256)
		}
		posed = append([]apComp(nil), ap...)
		for _, ps := range pf.Poses {
			i, ok := byID[ps.PrimitiveID]
			if !ok {
				return nil, fmt.Errorf("pose for unknown primitiveId %q", ps.PrimitiveID)
			}
			c := snap.Components[i]
			if posedIdx[i] {
				return nil, fmt.Errorf("two poses for %s (%s)", c.Designator, c.ID)
			}
			switch {
			case c.Locked:
				return nil, fmt.Errorf("pose for %s (%s): the part is locked", c.Designator, c.ID)
			case movable[i]:
				return nil, fmt.Errorf("pose for %s (%s): a satellite cannot be posed; the planner places it around its chip", c.Designator, c.ID)
			}
			if ps.X == nil || ps.Y == nil || ps.Rotation == nil {
				return nil, fmt.Errorf("pose for %s (%s) needs x, y and rotation", c.Designator, c.ID)
			}
			x, y, r := *ps.X, *ps.Y, *ps.Rotation
			for _, v := range []float64{x, y, r} {
				if math.IsNaN(v) || math.IsInf(v, 0) {
					return nil, fmt.Errorf("pose for %s (%s) has a non-finite number", c.Designator, c.ID)
				}
			}
			if math.Abs(r-math.Round(r/90)*90) > 1e-6 {
				return nil, fmt.Errorf("pose for %s (%s): rotation %v is not a multiple of 90", c.Designator, c.ID, r)
			}
			posed[i] = simTransform(ap[i], x, y, simNormDeg(r))
			posedIdx[i] = true
		}
	}

	moves, diags, info := planAutoPlaceDetailed(posed, opt)

	// Final poses: posed parts as given, satellites as planned.
	final := append([]apComp(nil), posed...)
	planned := map[int]apMove{}
	for _, m := range moves {
		i, ok := byID[m.ID]
		if !ok {
			continue
		}
		rot := posed[i].rotation
		if m.SetRot {
			rot = m.NewRot
		}
		final[i] = simTransform(posed[i], m.NewX, m.NewY, rot)
		planned[i] = m
	}
	changed := func(i int) bool {
		o, f := ap[i], final[i]
		return math.Abs(o.x-f.x) > simPosEps || math.Abs(o.y-f.y) > simPosEps || simAngleDiff(o.rotation, f.rotation) > simAngEps
	}

	mainSet := map[int]bool{}
	for _, i := range info.Mains {
		mainSet[i] = true
	}
	role := func(i int) string {
		c := ap[i]
		switch {
		case mainSet[i]:
			return "main"
		case movable[i]:
			return "satellite"
		case isAntennaComp(c):
			return "antenna"
		case connectorDesRe.MatchString(strings.TrimSpace(c.designator)):
			return "connector"
		case c.locked:
			return "fixed"
		}
		return "other"
	}
	owner := func(i int) string {
		if mainSet[i] {
			return ap[i].designator
		}
		if m, ok := planned[i]; ok {
			return m.Main
		}
		if ci, ok := info.AntChip[i]; ok {
			return ap[ci].designator
		}
		return ""
	}

	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		ca, cb := snap.Components[order[a]], snap.Components[order[b]]
		if ca.Designator != cb.Designator {
			return simDesLess(ca.Designator, cb.Designator)
		}
		return ca.ID < cb.ID
	})

	res := &lsResult{Schema: simSchema, BoardSemanticSHA256: snap.SemanticSHA256, Outline: outline, Keepouts: keepouts,
		Components: []lsComponent{}, Moves: []lsMove{}, Ratsnest: []lsRat{}}
	for _, i := range order {
		src, f := snap.Components[i], final[i]
		moved := changed(i)
		// A part that is only translated keeps its dump rotation as written
		// (-90 stays -90); a re-oriented one reports the normalised final angle.
		turned := simAngleDiff(ap[i].rotation, f.rotation) > simAngEps
		rot := src.Rotation
		if turned {
			rot = simNormDeg(f.rotation)
		}
		comp := lsComponent{PrimitiveID: src.ID, Designator: src.Designator, Name: src.Name, Role: role(i), Owner: owner(i),
			Layer: src.Layer, X: round4(f.x), Y: round4(f.y), Rotation: round4(rot), Moved: moved, Pads: []lsPad{}}
		if f.hasBBox {
			comp.BBox = &layoutBBox{MinX: round4(f.minX), MinY: round4(f.minY), MaxX: round4(f.maxX), MaxY: round4(f.maxY)}
		}
		for _, p := range f.pads {
			comp.Pads = append(comp.Pads, lsPad{Num: p.num, Net: p.net, X: round4(p.x), Y: round4(p.y), W: round4(p.w), H: round4(p.h)})
		}
		res.Components = append(res.Components, comp)
		if !moved {
			continue
		}
		via := "pose"
		if m, ok := planned[i]; ok {
			via = m.Via
		}
		res.Moves = append(res.Moves, lsMove{PrimitiveID: src.ID, Designator: src.Designator, X: round4(f.x), Y: round4(f.y),
			Rotation: round4(rot), SetRotation: turned, Via: via})
	}

	var pts []lsPadPt
	for _, c := range res.Components {
		for _, p := range c.Pads {
			pts = append(pts, lsPadPt{Net: p.Net, X: p.X, Y: p.Y})
		}
	}
	res.Ratsnest = simBuildRatsnest(pts)
	for _, e := range res.Ratsnest {
		res.Metrics.RatsnestMil += math.Hypot(e.A[0]-e.B[0], e.A[1]-e.B[1])
	}
	res.Metrics.RatsnestMil = round2(res.Metrics.RatsnestMil)
	res.Metrics.Crossings = simCountCrossings(res.Ratsnest)
	res.Metrics.CongestionPeak = simCongestionPeak(pts, res.Ratsnest)
	for _, i := range pre.Movable {
		if _, ok := planned[i]; !ok {
			res.Metrics.Unplaced++
		}
	}

	for _, d := range diags {
		issues = append(issues, simIssueFor(d))
	}
	sort.SliceStable(issues, func(a, b int) bool {
		x, y := issues[a], issues[b]
		if x.Severity != y.Severity {
			return x.Severity < y.Severity
		}
		if x.Designator != y.Designator {
			return simDesLess(x.Designator, y.Designator)
		}
		return x.Message < y.Message
	})
	res.Issues = append([]lsIssue{}, issues...)
	return res, nil
}

// ── issues ───────────────────────────────────────────────────────────────────

// simSeverity maps a planner diagnostic to P1 (a part is unplaced), P2 (placed
// but over a limit or off the RF feed line) or P3 (information). The RF-line
// messages come first because their text also says "no free slot on the line":
// those parts WERE placed.
func simSeverity(reason string) string {
	switch {
	case strings.Contains(reason, "off the RF feed line"), strings.Contains(reason, "left the RF feed line"), strings.Contains(reason, "left the feed line"):
		return "P2"
	case strings.Contains(reason, "no free slot"), strings.Contains(reason, "no shared net path"), strings.Contains(reason, "no main chip"):
		return "P1"
	case strings.Contains(reason, "limit"):
		return "P2"
	}
	return "P3"
}

func simIssueFor(d apDiag) lsIssue {
	is := lsIssue{Severity: simSeverity(d.Reason), Designator: d.Designator, Message: d.Reason}
	switch is.Severity {
	case "P1", "P2":
		if _, hint, ok := strings.Cut(d.Reason, " — "); ok {
			is.Fix = hint
		} else if strings.Contains(d.Reason, "no shared net path") {
			is.Fix = "connect the part to a net that reaches a main chip, or place it by hand"
		} else if strings.Contains(d.Reason, "no main chip") {
			is.Fix = "add a main chip (>= 8 distinct pins) to anchor the satellites against"
		}
	}
	return is
}

// ── ratsnest, crossings, congestion ──────────────────────────────────────────

type lsPadPt struct {
	Net  string
	X, Y float64
}

// simBuildRatsnest builds the per-net minimum spanning tree over pad centres (an
// empty net name is unconnected and skipped; global nets are included). Pads of a
// net are taken in coordinate order so the tree is independent of input order.
func simBuildRatsnest(pads []lsPadPt) []lsRat {
	byNet := map[string][]pcbLPad{}
	for _, p := range pads {
		if strings.TrimSpace(p.Net) == "" {
			continue
		}
		byNet[p.Net] = append(byNet[p.Net], pcbLPad{Net: p.Net, X: p.X, Y: p.Y})
	}
	nets := make([]string, 0, len(byNet))
	for n := range byNet {
		nets = append(nets, n)
	}
	sort.Strings(nets)
	out := []lsRat{}
	for _, net := range nets {
		ps := byNet[net]
		sort.SliceStable(ps, func(i, j int) bool {
			if ps[i].X != ps[j].X {
				return ps[i].X < ps[j].X
			}
			return ps[i].Y < ps[j].Y
		})
		for _, e := range netMST(net, ps) {
			a, b := [2]float64{e.Ax, e.Ay}, [2]float64{e.Bx, e.By}
			if b[0] < a[0] || (b[0] == a[0] && b[1] < a[1]) {
				a, b = b, a
			}
			out = append(out, lsRat{Net: net, A: a, B: b})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		x, y := out[i], out[j]
		if x.Net != y.Net {
			return x.Net < y.Net
		}
		if x.A != y.A {
			return x.A[0] < y.A[0] || (x.A[0] == y.A[0] && x.A[1] < y.A[1])
		}
		return x.B[0] < y.B[0] || (x.B[0] == y.B[0] && x.B[1] < y.B[1])
	})
	return out
}

// simCountCrossings counts pairs of ratsnest segments of DIFFERENT nets that
// properly cross (shared endpoints and touching do not count).
func simCountCrossings(rat []lsRat) int {
	links := make([]ratLink, len(rat))
	for i, r := range rat {
		links[i] = ratLink{Net: r.Net, Ax: r.A[0], Ay: r.A[1], Bx: r.B[0], By: r.B[1]}
	}
	n := 0
	for i := 0; i < len(links); i++ {
		for j := i + 1; j < len(links); j++ {
			if links[i].Net == links[j].Net {
				continue
			}
			if _, _, ok := segCross(links[i], links[j]); ok {
				n++
			}
		}
	}
	return n
}

// simCongestionPeak is the maximum, over a 50 mil grid (cells [i*50,(i+1)*50)
// from the dump origin), of the pad centres in a cell plus the ratsnest segments
// touching it. Deliberately simple: a placement-time hint, not a routing model.
func simCongestionPeak(pads []lsPadPt, rat []lsRat) int {
	cells := map[[2]int]int{}
	idx := func(v float64) int { return int(math.Floor(v / simGridMil)) }
	for _, p := range pads {
		cells[[2]int{idx(p.X), idx(p.Y)}]++
	}
	for _, r := range rat {
		ax, ay, bx, by := r.A[0], r.A[1], r.B[0], r.B[1]
		if bx < ax {
			ax, ay, bx, by = bx, by, ax, ay
		}
		for ci := idx(ax); ci <= idx(bx); ci++ {
			xl, xr := math.Max(ax, float64(ci)*simGridMil), math.Min(bx, float64(ci+1)*simGridMil)
			yl, yr := ay, by
			if bx > ax {
				yl = ay + (by-ay)*(xl-ax)/(bx-ax)
				yr = ay + (by-ay)*(xr-ax)/(bx-ax)
			}
			lo, hi := idx(math.Min(yl, yr)), idx(math.Max(yl, yr))
			for cj := lo; cj <= hi; cj++ {
				cells[[2]int{ci, cj}]++
			}
		}
	}
	peak := 0
	for _, v := range cells {
		peak = max(peak, v)
	}
	return peak
}
