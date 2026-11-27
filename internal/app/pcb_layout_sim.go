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

// lsOwnerFile pins satellites to a main: designator → main designator, bound to
// the board by its semanticSha256 like the poses.
type lsOwnerFile struct {
	BoardSemanticSHA256 string            `json:"boardSemanticSha256"`
	Owners              map[string]string `json:"owners"`
}

// lsSimOptions are the optional simulate behaviours; the zero value is the plain
// "re-plan every satellite" run.
type lsSimOptions struct {
	Owners      *lsOwnerFile    // --owners: pinned owners (nil = computed by the planner)
	KeepUnmoved bool            // --keep-unmoved: only re-plan what the poses disturb
	Anchors     map[string]bool // --anchor: designators forced into the main set
	ExcludeMain map[string]bool // --exclude-main: designators barred from it
	Spread      bool            // --spread: re-distribute the spreadable main chips over the board first
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
	PrimitiveID string `json:"primitiveId"`
	Designator  string `json:"designator"`
	Name        string `json:"name"`
	Role        string `json:"role"`
	// Reason says why a part is "fixed" beyond its own lock: "locked-routing".
	Reason string `json:"reason,omitempty"`
	Owner  string `json:"owner"`
	// DisplacedBy is the posed part or placed satellite that first forced this
	// satellite out of its dump pose under --keep-unmoved.
	DisplacedBy string      `json:"displacedBy,omitempty"`
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

// lsRoutingNet is the routing found on one net of the dump.
type lsRoutingNet struct {
	Net    string   `json:"net"`
	Tracks int      `json:"tracks"`
	Vias   int      `json:"vias"`
	Locked int      `json:"locked"` // locked tracks + vias
	Parts  []string `json:"parts"`  // designators with a pad on the net
}

// lsRouting describes the dump's copper routing (pose independent). Availability
// "unknown" means the dump could not be read for it: it never means "no routing".
type lsRouting struct {
	Availability string         `json:"availability"`
	Tracks       int            `json:"tracks"`
	Vias         int            `json:"vias"`
	Nets         []lsRoutingNet `json:"nets"`
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
	Routing             lsRouting     `json:"routing"`
	Ripup               lsRipup       `json:"ripup"`
	Issues              []lsIssue     `json:"issues"`
}

// ── command ──────────────────────────────────────────────────────────────────

func newPcbLayoutSimulateCmd(stdout, stderr io.Writer) *cobra.Command {
	var boardPath, posesPath, ownersPath, outPath, anchorCSV, excludeMainCSV string
	var keepUnmoved, spread bool
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

Small chips count as main chips here and in 'pcb auto-place': a part whose
designator prefix is U, IC, VR, REG or PS with at least 3 distinct pins (an LDO,
a DC-DC) owns its own satellites. R/C/L/D/LED/FB/Y/X/F/TP/Q parts are never
promoted. Every main on a rail keeps at least one cap of that rail (taken from a
main holding two or more; pinned caps never move).

--owners owners.json pins satellites to a main so the planner never re-derives
the owner from the nearest pad (take the first one from a result's
components[].owner):
  {"boardSemanticSha256":"<must equal the board's semanticSha256>",
   "owners":{"C5":"U2","C7":"U1"}}
A pin to a part that is not a main (e.g. demoted) is ignored with a P3 issue;
unknown designators are a P3 issue too; an empty owner is no pin. RF chain members
follow their antenna.

--keep-unmoved re-plans only what the poses disturb: satellites whose owner was
posed (and the RF chain of a posed chip/antenna) are re-planned, every other
satellite stays at its dump pose and is an obstacle. A staying satellite whose
bbox overlaps a posed part, or a newly placed satellite, is freed too and is
re-planned around ITS OWN owner only (up to 8 rounds, then a P2 issue);
components[].displacedBy names the part that first forced it free. Without the
flag every satellite is re-planned.

--spread re-distributes the main chips over the whole board before the satellites
are planned. Spreadable mains are the planner's mains (after --anchor /
--exclude-main) that are not locked, have no locked routing and have a bbox; their
x/y come from the spread (rotation as on the posed board; a pose of a main is
replaced, "via" is "spread"). Connectors, antennas, fixed/locked parts and
keep-outs never move and are obstacles (a pose of a connector is honoured). Each
main is given its bbox plus a ring that holds the satellites it owns; footprints are
packed in rows in the board's outline bbox, the most strongly connected chips next
to each other (shared nets weigh 1/(parts-1)), then the set is centred. If the
footprints do not fit, plain bboxes plus 2 gaps are tried; if even that fails the
chips stay and a P1 issue says so. The poses are written like user poses, so a later
run given them without --spread (same --owners, --keep-unmoved) gives the same
layout. Use it with --keep-unmoved on a laid-out board.

routing (from the dump, pose independent): availability (available|unknown;
unknown = the dump could not read tracks/vias, never "no routing"), tracks (lines
+ arcs on copper layers), vias, and per net {net, tracks, vias, locked, parts}
for nets with any. A part whose pad has a locked track end or locked via of the
same net on it is role "fixed" with reason "locked-routing" and cannot be posed or
re-planned (other parts on that net stay free).

--anchor / --exclude-main override which parts are mains, exactly as in
'pcb auto-place': a demoted small IC becomes a satellite, a demoted high-pin
part stays put.

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
  easyeda pcb layout simulate --board board.json --poses poses.json --out sim.json
  easyeda pcb layout simulate --board board.json --poses poses.json --owners owners.json --keep-unmoved --out sim.json
  easyeda pcb layout simulate --board board.json --owners owners.json --keep-unmoved --spread --out sim.json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if boardPath == "" || outPath == "" {
				return fmt.Errorf("--board and --out are required")
			}
			inputs := []string{boardPath}
			if posesPath != "" {
				inputs = append(inputs, posesPath)
			}
			if ownersPath != "" {
				inputs = append(inputs, ownersPath)
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
			var so lsSimOptions
			so.KeepUnmoved, so.Spread = keepUnmoved, spread
			so.Anchors, so.ExcludeMain = designatorSet(anchorCSV), designatorSet(excludeMainCSV)
			if ownersPath != "" {
				raw, err := os.ReadFile(ownersPath)
				if err != nil {
					return fmt.Errorf("read owners: %w", err)
				}
				so.Owners = &lsOwnerFile{}
				if err := decodeStrict(raw, so.Owners); err != nil {
					return fmt.Errorf("decode owners: %w", err)
				}
			}
			res, err := simulatePcbLayoutOpts(snap, &poses, posesPath != "", so)
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
	cmd.Flags().StringVar(&ownersPath, "owners", "", "optional owners JSON: satellite → main pins (bound to the board's semanticSha256)")
	cmd.Flags().BoolVar(&keepUnmoved, "keep-unmoved", false, "re-plan only satellites disturbed by the poses; the rest stay at their dump pose")
	cmd.Flags().BoolVar(&spread, "spread", false, "re-distribute the movable main chips over the whole board (connection order, shelf-packed with a satellite ring), then plan satellites as usual; poses of mains are replaced")
	cmd.Flags().StringVar(&anchorCSV, "anchor", "", "designator(s) forced to be main parts (CSV), as in pcb auto-place")
	cmd.Flags().StringVar(&excludeMainCSV, "exclude-main", "", "designator(s) barred from the main set (CSV): a small IC becomes a satellite, a high-pin part stays put")
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
	return simulatePcbLayoutOpts(snap, pf, havePoses, lsSimOptions{})
}

func simulatePcbLayoutOpts(snap *boardSnapshot, pf *lsPoseFile, havePoses bool, so lsSimOptions) (*lsResult, error) {
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

	// Routing facts come from the dump alone. A part is fixed when locked copper
	// (a locked track/arc end or a locked via) lands on one of its pads of the same
	// net: the planner sees it as locked (never moved, an obstacle) and it cannot
	// be posed. A locked via elsewhere on GND does not freeze every GND part.
	routing, lockedPts := simRoutingFacts(snap)
	lockedRoute := make([]bool, n)
	for i := range ap {
		if simPadsTouchLocked(ap[i].pads, lockedPts) {
			lockedRoute[i] = true
			ap[i].locked = true
		}
	}

	// Planner inputs: same defaults as `pcb auto-place`; keep-outs and board
	// bounds from the dump instead of live calls.
	opt := defaultApOptions()
	opt.multiGap = 0 // chips move only by an explicit pose, never by re-spacing
	opt.anchors, opt.excludeMain = so.Anchors, so.ExcludeMain
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

	// Pinned owners (--owners): validated against the board, then honoured by the
	// planner's assignment passes.
	var pins map[string]string
	if so.Owners != nil {
		if so.Owners.BoardSemanticSHA256 != snap.SemanticSHA256 {
			return nil, fmt.Errorf("owners boardSemanticSha256 %q does not match the board's semanticSha256 %q; re-sync: take a fresh `easyeda pcb dump --include-copper` and recompute the owners", so.Owners.BoardSemanticSHA256, snap.SemanticSHA256)
		}
		var err error
		if pins, err = simNormalizeOwners(so.Owners.Owners); err != nil {
			return nil, err
		}
		opt.owners = pins
	}

	// Classify on the unposed board (pose-independent) and validate the poses.
	_, _, pre := planAutoPlaceDetailed(ap, opt)
	movable := map[int]bool{}
	for _, i := range pre.Movable {
		movable[i] = true
	}
	issues = append(issues, simOwnerIssues(snap, pins, pre, ap)...)
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
			case lockedRoute[i]:
				return nil, fmt.Errorf("pose for %s (%s): part has locked routing", c.Designator, c.ID)
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

	// --spread: poses for the spreadable mains, written exactly like user poses so
	// everything below is the unchanged posed pipeline (a later run given these
	// poses without --spread yields the same layout).
	spreadVia := map[int]bool{}
	if so.Spread {
		var poly [][2]float64
		if o := snap.Outline; o != nil && opt.board != nil {
			poly = o.Points
		}
		if pos, extra := simSpreadMains(posed, opt, pre, movable, lockedRoute, poly); pos != nil {
			if !havePoses {
				posed = append([]apComp(nil), ap...)
			}
			spreadIdx := make([]int, 0, len(pos))
			for i := range pos {
				spreadIdx = append(spreadIdx, i)
			}
			sort.Ints(spreadIdx)
			for _, i := range spreadIdx {
				posed[i] = simTransform(ap[i], pos[i][0], pos[i][1], simNormDeg(posed[i].rotation))
				posedIdx[i] = true
				spreadVia[i] = true
			}
		} else {
			issues = append(issues, extra...)
		}
	}

	var moves []apMove
	var diags []apDiag
	var info apPlanInfo
	var displaced map[int]string // satellite index → part that forced it free (--keep-unmoved)
	if so.KeepUnmoved {
		// Owners are fixed on the unposed board, whether or not a file said so: the
		// file's pins stay (an unreachable pin stays unplaced instead of hopping to
		// another main), the rest come from the unposed plan.
		opt.owners = map[string]string{}
		for k, v := range pins {
			opt.owners[k] = v
		}
		for s, mi := range pre.Owner {
			opt.owners[strings.ToUpper(strings.TrimSpace(ap[s].designator))] = strings.ToUpper(strings.TrimSpace(ap[mi].designator))
		}
		var extra []lsIssue
		moves, diags, info, displaced, extra = simPlanKeepUnmoved(ap, posed, opt, pre, posedIdx)
		issues = append(issues, extra...)
	} else {
		moves, diags, info = planAutoPlaceDetailed(posed, opt)
	}

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
		case lockedRoute[i]:
			return "fixed"
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
		if mi, ok := info.Owner[i]; ok && so.KeepUnmoved { // left in place, or freed but unplaced
			return ap[mi].designator
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
			DisplacedBy: simIf(moved, displaced[i]), Layer: src.Layer, X: round4(f.x), Y: round4(f.y), Rotation: round4(rot), Moved: moved, Pads: []lsPad{}}
		if lockedRoute[i] {
			comp.Reason = "locked-routing"
		}
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
		if spreadVia[i] {
			via = "spread"
		}
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
		if _, ok := planned[i]; !ok && !info.Stayed[i] {
			res.Metrics.Unplaced++
		}
	}
	res.Routing = routing
	movedIDs := map[string]bool{}
	for _, m := range res.Moves {
		movedIDs[m.PrimitiveID] = true
	}
	res.Ripup = simRipup(snap, routing.Availability == "available", movedIDs)

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

// ── routing facts ────────────────────────────────────────────────────────────

// simRoutingFacts reads the dump's copper routing: tracks (lines + arcs on copper
// layers), vias, and per net the counts, the locked ones and the parts with a pad
// on it. It also returns the end points of locked copper (track/arc ends, via
// centres) with their net. Availability is
// "unknown" when the dump has no copper section or its lines/vias were not read
// ("routing"/"vias" not "available", or arcsAvailable explicitly false): a missing
// read is never "no routing", so counts stay 0 and nets empty. (A real dump with
// no arcsAvailable already carries routing "unknown", see fetchCopper.)
func simRoutingFacts(snap *boardSnapshot) (lsRouting, []lsPadPt) {
	out := lsRouting{Availability: "unknown", Nets: []lsRoutingNet{}}
	var locked []lsPadPt
	c := snap.Copper
	if c == nil || c.Availability["routing"] != "available" || c.Availability["vias"] != "available" ||
		(c.ArcsAvailable != nil && !*c.ArcsAvailable) {
		return out, locked // lines, vias or arcs unread: absence of routing cannot be shown
	}
	out.Availability = "available"
	byNet := map[string]*lsRoutingNet{}
	add := func(m map[string]any, isVia bool) {
		net, isLocked := asString(m["net"]), asBool(m["locked"])
		if isLocked {
			for _, k := range [][2]string{{"x", "y"}, {"startX", "startY"}, {"endX", "endY"}} {
				x, okx := finiteFloat(m[k[0]])
				y, oky := finiteFloat(m[k[1]])
				if okx && oky {
					locked = append(locked, lsPadPt{Net: strings.TrimSpace(net), X: x, Y: y})
				}
			}
		}
		if isVia {
			out.Vias++
		} else {
			out.Tracks++
		}
		net = strings.TrimSpace(net)
		if net == "" {
			return // netless copper counts in the totals but belongs to no net row
		}
		n := byNet[net]
		if n == nil {
			n = &lsRoutingNet{Net: net, Parts: []string{}}
			byNet[net] = n
		}
		if isVia {
			n.Vias++
		} else {
			n.Tracks++
		}
		if isLocked {
			n.Locked++
		}
	}
	for _, group := range [][]any{c.Lines, c.Arcs} {
		for _, raw := range group {
			m, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			layer, ok := finiteFloat(m["layer"])
			if !ok || layer != math.Trunc(layer) || !netPathCopperLayer(int(layer)) {
				continue // outline, silk and other non-copper line primitives are not routing
			}
			add(m, false)
		}
	}
	for _, raw := range c.Vias {
		if m, ok := raw.(map[string]any); ok {
			add(m, true)
		}
	}
	parts := map[string]map[string]bool{}
	for _, comp := range snap.Components {
		for _, pad := range comp.Pads {
			net := strings.TrimSpace(pad.Net)
			if byNet[net] == nil {
				continue
			}
			if parts[net] == nil {
				parts[net] = map[string]bool{}
			}
			parts[net][comp.Designator] = true
		}
	}
	for net, n := range byNet {
		for des := range parts[net] {
			n.Parts = append(n.Parts, des)
		}
		sort.Slice(n.Parts, func(i, j int) bool { return simDesLess(n.Parts[i], n.Parts[j]) })
		out.Nets = append(out.Nets, *n)
	}
	sort.Slice(out.Nets, func(i, j int) bool { return out.Nets[i].Net < out.Nets[j].Net })
	return out, locked
}

// simPadsTouchLocked reports whether a locked copper point of a pad's net lies on
// that pad (its copper extent plus simLockTolMil; a pad of unknown size counts
// within simLockTolMil of its centre).
func simPadsTouchLocked(pads []apPad, pts []lsPadPt) bool {
	for _, p := range pads {
		net := strings.TrimSpace(p.net)
		if net == "" {
			continue
		}
		for _, q := range pts {
			if q.Net == net && math.Abs(q.X-p.x) <= p.w/2+simLockTolMil && math.Abs(q.Y-p.y) <= p.h/2+simLockTolMil {
				return true
			}
		}
	}
	return false
}

const simLockTolMil = 1.0

// ── owner pins ───────────────────────────────────────────────────────────────

// simNormalizeOwners upper-cases and trims the designators of an owners file. An
// empty owner means "no pin" and is skipped (a result's components[].owner is ""
// for connectors and fixed parts, so a file built from it may carry such entries);
// an empty part designator, or two entries for one part that disagree, is an error.
func simNormalizeOwners(in map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(in))
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		part, owner := strings.ToUpper(strings.TrimSpace(k)), strings.ToUpper(strings.TrimSpace(in[k]))
		if part == "" {
			return nil, fmt.Errorf("owners entry %q has an empty part designator", k)
		}
		if owner == "" {
			continue
		}
		if prev, dup := out[part]; dup && prev != owner {
			return nil, fmt.Errorf("owners lists %s twice with different owners (%s, %s)", part, prev, owner)
		}
		out[part] = owner
	}
	return out, nil
}

// simOwnerIssues reports pins the planner cannot honour: a designator that is not
// on the board, or an owner that is not a main on it (e.g. it was demoted). Both
// are P3: the pin is ignored for that part and the planner derives the owner.
func simOwnerIssues(snap *boardSnapshot, pins map[string]string, pre apPlanInfo, ap []apComp) []lsIssue {
	if len(pins) == 0 {
		return nil
	}
	onBoard := map[string]string{} // upper-cased designator → designator as written
	for _, c := range snap.Components {
		onBoard[strings.ToUpper(strings.TrimSpace(c.Designator))] = c.Designator
	}
	mains := map[string]bool{}
	for _, i := range pre.Mains {
		mains[strings.ToUpper(strings.TrimSpace(ap[i].designator))] = true
	}
	keys := make([]string, 0, len(pins))
	for k := range pins {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []lsIssue
	for _, k := range keys {
		owner := pins[k]
		switch {
		case onBoard[k] == "":
			out = append(out, lsIssue{Severity: "P3", Designator: k, Message: fmt.Sprintf("owners: %s is not on this board; the pin is ignored", k), Fix: "drop it from --owners or take fresh owners from a new simulate result"})
		case !mains[owner]:
			why := "is not a main chip on this board"
			if onBoard[owner] == "" {
				why = "is not on this board"
			}
			out = append(out, lsIssue{Severity: "P3", Designator: onBoard[k], Message: fmt.Sprintf("owners: %s's owner %s %s; the pin is ignored and the planner picks the owner", onBoard[k], owner, why), Fix: "pin it to a main chip or drop it from --owners"})
		}
	}
	return out
}

// ── minimal disturbance (--keep-unmoved) ─────────────────────────────────────

// simKeepRounds caps the free-the-colliders loop of --keep-unmoved.
const simKeepRounds = 8

// simReflowMil: a chip whose zone (its body and its satellites, at their dump
// poses) comes within this distance of a posed part's old zone or new body has all
// of its satellites re-planned, so room a move frees (or crowds) is used again.
const simReflowMil = 200.0

func simIf(ok bool, s string) string {
	if ok {
		return s
	}
	return ""
}

func simBox(c apComp) (layoutBBox, bool) {
	return layoutBBox{MinX: c.minX, MinY: c.minY, MaxX: c.maxX, MaxY: c.maxY}, c.hasBBox
}

func simUnion(a, b layoutBBox) layoutBBox {
	return layoutBBox{MinX: math.Min(a.MinX, b.MinX), MinY: math.Min(a.MinY, b.MinY), MaxX: math.Max(a.MaxX, b.MaxX), MaxY: math.Max(a.MaxY, b.MaxY)}
}

// simPlanKeepUnmoved plans only the satellites the poses disturb. Owners were
// pinned by the caller (opt.owners). A satellite is free when its owner (or, for
// an RF chain member, its antenna) was posed; every other assigned satellite stays
// at its dump pose and is an obstacle for the packer. A staying satellite whose
// bbox overlaps (positive area, same board side) a posed part's new bbox is freed
// before the first plan; after each plan, staying satellites overlapping a newly
// placed satellite's bbox are freed and the plan is redone, until nothing changes
// (at most simKeepRounds plans, then a P2 issue). An RF chain moves as a whole: a
// freed member frees its siblings. displaced names, per freed satellite, the
// smallest designator (simDesLess) among the parts that overlapped it in the first
// round it was freed. Before that, every chip near a posed part's old zone or new
// body (simReflowMil) has all its satellites freed too ("reflow"), named after the
// smallest such posed designator; `orig` is the board before the poses.
func simPlanKeepUnmoved(orig, posed []apComp, opt apOptions, pre apPlanInfo, posedIdx map[int]bool) ([]apMove, []apDiag, apPlanInfo, map[int]string, []lsIssue) {
	free := map[int]bool{}
	displaced := map[int]string{}
	chainOf := map[int][]int{} // antenna → its chain members
	for _, s := range pre.Movable {
		if ant, ok := pre.ChainAnt[s]; ok {
			chainOf[ant] = append(chainOf[ant], s)
		}
	}
	for _, s := range pre.Movable {
		if owner, ok := pre.Owner[s]; ok && posedIdx[owner] {
			free[s] = true
		} else if ant, ok := pre.ChainAnt[s]; ok && posedIdx[ant] {
			free[s] = true
		}
	}
	type src struct {
		des  string
		box  layoutBBox
		side int
	}
	// release frees every staying satellite overlapping one of the boxes.
	release := func(boxes []src) bool {
		hit := map[int]string{}
		for _, s := range pre.Movable {
			c := posed[s]
			if _, owned := pre.Owner[s]; !owned || free[s] || !c.hasBBox {
				continue
			}
			sb := layoutBBox{MinX: c.minX, MinY: c.minY, MaxX: c.maxX, MaxY: c.maxY}
			for _, b := range boxes {
				if sameSide(c.side(), b.side) && apOverlaps(sb, b.box) {
					if cur, ok := hit[s]; !ok || simDesLess(b.des, cur) {
						hit[s] = b.des
					}
				}
			}
		}
		for s, by := range hit {
			group := []int{s}
			if ant, ok := pre.ChainAnt[s]; ok {
				group = chainOf[ant]
				for _, m := range group { // the smallest collider over the whole chain
					if o, ok := hit[m]; ok && simDesLess(o, by) {
						by = o
					}
				}
			}
			for _, m := range group {
				if !free[m] {
					free[m] = true
					displaced[m] = by
				}
			}
		}
		return len(hit) > 0
	}
	idx := make(map[string]int, len(posed))
	for i, c := range posed {
		idx[c.id] = i
	}
	var posedBoxes []src
	for i := range posed {
		if c := posed[i]; posedIdx[i] && c.hasBBox {
			posedBoxes = append(posedBoxes, src{strings.TrimSpace(c.designator), layoutBBox{MinX: c.minX, MinY: c.minY, MaxX: c.maxX, MaxY: c.maxY}, c.side()})
		}
	}
	release(posedBoxes)

	// Reflow: zones at dump poses (a chip plus its satellites), compared with where
	// each posed part was (with its satellites) and where it is now.
	zone := func(m int) (layoutBBox, bool) {
		z, ok := simBox(orig[m])
		for s, owner := range pre.Owner {
			if owner != m {
				continue
			}
			if b, has := simBox(orig[s]); has {
				if ok {
					z = simUnion(z, b)
				} else {
					z, ok = b, true
				}
			}
		}
		return z, ok
	}
	var moved []src
	for i := range posed {
		if !posedIdx[i] {
			continue
		}
		des := strings.TrimSpace(posed[i].designator)
		if z, ok := zone(i); ok {
			moved = append(moved, src{des, z, orig[i].side()})
		}
		if b, ok := simBox(posed[i]); ok {
			moved = append(moved, src{des, b, posed[i].side()})
		}
	}
	for _, m := range pre.Mains {
		if posedIdx[m] {
			continue
		}
		z, ok := zone(m)
		if !ok {
			continue
		}
		near := apInflate(z, simReflowMil)
		by := ""
		for _, b := range moved {
			if sameSide(orig[m].side(), b.side) && apOverlaps(near, b.box) && (by == "" || simDesLess(b.des, by)) {
				by = b.des
			}
		}
		if by == "" {
			continue
		}
		for s, owner := range pre.Owner {
			if owner == m && !free[s] {
				free[s] = true
				displaced[s] = by
			}
		}
	}

	var issues []lsIssue
	for round := 1; ; round++ {
		o := opt
		o.stay = map[int]bool{}
		for _, s := range pre.Movable {
			if _, owned := pre.Owner[s]; owned && !free[s] {
				o.stay[s] = true
			}
		}
		moves, diags, info := planAutoPlaceDetailed(posed, o)
		var placed []src
		for _, m := range moves {
			i, ok := idx[m.ID]
			if !ok {
				continue
			}
			rot := posed[i].rotation
			if m.SetRot {
				rot = m.NewRot
			}
			if f := simTransform(posed[i], m.NewX, m.NewY, rot); f.hasBBox {
				// Moves are rounded to 0.01 mil: a slot flush against a staying
				// satellite must not count as overlapping it by rounding noise.
				box := apInflate(layoutBBox{MinX: f.minX, MinY: f.minY, MaxX: f.maxX, MaxY: f.maxY}, -simPosEps)
				placed = append(placed, src{strings.TrimSpace(f.designator), box, f.side()})
			}
		}
		if !release(placed) {
			return moves, diags, info, displaced, issues
		}
		if round >= simKeepRounds {
			issues = append(issues, lsIssue{Severity: "P2", Message: fmt.Sprintf("--keep-unmoved did not settle within %d rounds: staying satellites may still overlap newly placed ones", simKeepRounds), Fix: "re-run, or move the posed part so fewer satellites are displaced"})
			return moves, diags, info, displaced, issues
		}
	}
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
		} else if strings.Contains(d.Reason, "pinned owner") {
			is.Fix = "pin it to a main chip it connects to, or drop the pin from --owners"
		} else if strings.Contains(d.Reason, "no shared net path") {
			is.Fix = "connect the part to a net that reaches a main chip, or place it by hand"
		} else if strings.Contains(d.Reason, "no main chip") {
			is.Fix = "add a main chip (>= 8 distinct pins, or a U/IC/VR/REG/PS part with >= 3) to anchor the satellites against"
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
