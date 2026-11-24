package app

// pcb net-path — read-only, pad-to-pad routed-copper evidence.
//
// The command deliberately reuses the existing list actions instead of adding a
// connector handler: components.list(includePads), line.list (lines + arcs), and
// via.list.  It builds an undirected physical-copper graph in Go and finds one
// deterministic path between each requested pair of ordered waypoints.
//
// Scope is intentionally narrower than native DRC connectivity. Copper pours,
// fills, and PLANE layers are not inferred; a miss means "not proven by listed
// routed copper", not necessarily "the finished board is electrically open".

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

const netPathGeomEps = 0.001 // mil; absorbs JSON/host floating-point round-off only

type pcbNetPathOptions struct {
	From    string
	To      string
	Through []string
	Net     string
	Layer   *int
}

type pcbNetPathStep struct {
	Kind        string           `json:"kind"`
	PrimitiveID string           `json:"primitiveId,omitempty"`
	Ref         string           `json:"ref,omitempty"`
	Layer       int              `json:"layer,omitempty"`
	WidthMil    float64          `json:"widthMil,omitempty"`
	At          *pcbNetPathPoint `json:"at,omitempty"`
	Start       *pcbNetPathPoint `json:"start,omitempty"`
	End         *pcbNetPathPoint `json:"end,omitempty"`
	ArcAngle    float64          `json:"arcAngle,omitempty"`
}

type pcbNetPathPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type pcbNetPathLeg struct {
	From       string           `json:"from"`
	To         string           `json:"to"`
	Connected  bool             `json:"connected"`
	Reason     string           `json:"reason,omitempty"`
	Layers     []int            `json:"layers,omitempty"`
	WidthsMil  []float64        `json:"widthsMil,omitempty"`
	MinWidth   *float64         `json:"minWidthMil,omitempty"`
	MaxWidth   *float64         `json:"maxWidthMil,omitempty"`
	ViaCount   int              `json:"viaCount"`
	Primitives int              `json:"primitiveCount"`
	LengthMil  float64          `json:"lengthMil"`
	TurnCount  int              `json:"turnCount"`
	Path       []pcbNetPathStep `json:"path,omitempty"`
}

type pcbNetPathReport struct {
	MeasurementPathKind string           `json:"measurementPathKind"`
	Connected           bool             `json:"connected"`
	Reason              string           `json:"reason,omitempty"`
	Net                 string           `json:"net"`
	From                string           `json:"from"`
	To                  string           `json:"to"`
	Through             []string         `json:"through,omitempty"`
	WaypointOrder       []string         `json:"waypointOrder"`
	RequestedLayer      *int             `json:"requestedLayer,omitempty"`
	Layers              []int            `json:"layers,omitempty"`
	LayerSequence       []int            `json:"layerSequence,omitempty"`
	WidthsMil           []float64        `json:"widthsMil,omitempty"`
	MinWidth            *float64         `json:"minWidthMil,omitempty"`
	MaxWidth            *float64         `json:"maxWidthMil,omitempty"`
	ViaCount            int              `json:"viaCount"`
	Primitives          int              `json:"primitiveCount"`
	LengthMil           float64          `json:"lengthMil"`
	TurnCount           int              `json:"turnCount"`
	Path                []pcbNetPathStep `json:"path,omitempty"`
	Legs                []pcbNetPathLeg  `json:"legs"`
	Scope               string           `json:"scope"`
	ExcludedCopper      []string         `json:"excludedCopper"`
	Limitations         []string         `json:"limitations"`
}

type pcbNetPathNode struct {
	kind string
	id   string
	ref  string
	net  string

	layer          int
	width          float64
	x, y           float64
	x1, y1, x2, y2 float64
	w, h, dia      float64
	rotation       float64
	shape          string
	shapeRound     float64
	shapeSides     int
	arcAngle       float64
}

func newPcbNetPathCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var opts pcbNetPathOptions
	var asJSON bool
	var layer int
	c := &cobra.Command{
		Use:   "net-path",
		Short: "Prove a pad-to-pad path through listed tracks/arcs/vias (read-only; pours excluded)",
		Long: `Build a physical-copper graph from pcb.components.list --include-pads,
pcb.line.list, and pcb.via.list, then prove one continuous path between two pads.

Pad references use DESIGNATOR.PAD, for example U2.3 or CARD1.4. Repeat --through
(or pass a comma-separated list) to require one non-repeating copper path to visit
the pads in order, such as C3.1 -> C4.1 -> U2.3. Pairwise reachability with branch
backtracking does not pass this ordered proof.
The report includes the actual primitive path, copper layers, widths, and unique via
count. The optional --net is an assertion; it must match every requested pad.
--layer constrains graph construction itself to that copper layer. Physical vias and
all tracks/arcs on other layers are excluded, so --layer 1 proves a TOP-only path
rather than finding an arbitrary path and checking its layers afterwards.

Scope: routed line/arc/via copper and placed pad copper only. Copper pours, filled
regions, and PLANE connectivity are deliberately not inferred, so a failed result
means "not proven by listed routed copper". Run native pcb drc separately for the
board-wide connection verdict. Arc bodies are traversed, but junctions to an arc are
recognized only at the arc endpoints exposed by pcb.line.list. Unsupported pad
geometry, unavailable arc readback, and overlapping primitives that make an ordered
topology ambiguous return unknown/error instead of PASS.`,
		Args: cobra.NoArgs,
		Example: `  easyeda pcb net-path --from C3.1 --through C4.1 --to U2.3 --layer 1
  easyeda pcb net-path --from U5.6 --to CN1.1 --net CANL --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("layer") {
				if !netPathCopperLayer(layer) {
					return fmt.Errorf("--layer must be a copper layer id (TOP=1, BOTTOM=2, INNER=15..44)")
				}
				opts.Layer = &layer
			}
			return runPcbNetPath(cfg, *window, opts, asJSON, stdout, stderr)
		},
	}
	c.Flags().StringVar(&opts.From, "from", "", "start pad as DESIGNATOR.PAD (required)")
	c.Flags().StringVar(&opts.To, "to", "", "destination pad as DESIGNATOR.PAD (required)")
	c.Flags().StringSliceVar(&opts.Through, "through", nil, "ordered intermediate pad(s); repeat or comma-separate")
	c.Flags().StringVar(&opts.Net, "net", "", "assert the expected net name")
	c.Flags().IntVar(&layer, "layer", 0, "constrain proof to one copper layer (for example 1=TOP); excludes physical vias")
	c.Flags().BoolVar(&asJSON, "json", false, "emit a machine-readable report")
	_ = c.MarkFlagRequired("from")
	_ = c.MarkFlagRequired("to")
	return c
}

func runPcbNetPath(cfg *appConfig, window string, opts pcbNetPathOptions, asJSON bool, stdout, stderr io.Writer) error {
	compRes, err := requestAction(cfg, "pcb.components.list", window, map[string]any{"includePads": true})
	if err != nil {
		return fmt.Errorf("pcb net-path: read pads: %w", err)
	}
	pads, err := parseNetPathPads(compRes.Result)
	if err != nil {
		return fmt.Errorf("pcb net-path: read pads: %w", err)
	}
	waypoints, net, err := resolveNetPathWaypoints(pads, opts)
	if err != nil {
		return fmt.Errorf("pcb net-path: %w", err)
	}

	linePayload := map[string]any{"net": net}
	if opts.Layer != nil {
		linePayload["layer"] = *opts.Layer
	}
	lineRes, err := requestAction(cfg, "pcb.line.list", window, linePayload)
	if err != nil {
		return fmt.Errorf("pcb net-path: read tracks/arcs for net %q: %w", net, err)
	}
	tracks, arcs, err := parseNetPathLines(lineRes.Result)
	if err != nil {
		return fmt.Errorf("pcb net-path: read tracks/arcs for net %q: %w", net, err)
	}
	var vias []pcbViaP
	if opts.Layer == nil {
		viaRes, err := requestAction(cfg, "pcb.via.list", window, map[string]any{"net": net})
		if err != nil {
			return fmt.Errorf("pcb net-path: read vias for net %q: %w", net, err)
		}
		vias, err = parseNetPathVias(viaRes.Result)
		if err != nil {
			return fmt.Errorf("pcb net-path: read vias for net %q: %w", net, err)
		}
	}

	rep, err := analyzePcbNetPath(pads, tracks, arcs, vias, opts)
	if err != nil {
		return fmt.Errorf("pcb net-path: %w", err)
	}
	// The live pre-resolution and pure analyzer must agree; this catches a future
	// parser drift before it can print evidence for the wrong pad/net.
	if rep.Net != net || !equalStrings(rep.WaypointOrder, waypoints) {
		return fmt.Errorf("pcb net-path: internal waypoint resolution changed during analysis")
	}

	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			return err
		}
	} else {
		renderPcbNetPath(rep, stdout)
	}
	if !rep.Connected {
		return fmt.Errorf("pcb net-path: no continuous listed-copper path for %s", strings.Join(rep.WaypointOrder, " -> "))
	}
	return nil
}

func parseNetPathPads(result map[string]any) ([]pcbPadP, error) {
	components, err := netPathRequiredSlice(result, "components")
	if err != nil {
		return nil, err
	}
	var pads []pcbPadP
	for ci, rc := range components {
		cm, ok := rc.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("components[%d] is not an object", ci)
		}
		designator := strings.TrimSpace(asString(cm["designator"]))
		componentLabel := designator
		if componentLabel == "" {
			componentLabel = fmt.Sprintf("components[%d]", ci)
		}
		if state, declared := pcbStringMap(cm["availability"])["pads"]; declared && state != "available" {
			return nil, fmt.Errorf("component %s: pad inventory is %s", componentLabel, state)
		}
		if issue := pcbCaptureErrors(cm["captureErrors"])["pads"]; issue != nil {
			return nil, fmt.Errorf("component %s: pad inventory capture failed: %v", componentLabel, issue)
		}
		if rawFailures, exists := cm["padFailures"]; exists && rawFailures != nil {
			failures, ok := rawFailures.([]any)
			if !ok || len(failures) > 0 {
				return nil, fmt.Errorf("component %s: pad inventory has serialization failures", componentLabel)
			}
		}
		componentPads, err := netPathRequiredSlice(cm, "pads")
		if err != nil {
			return nil, fmt.Errorf("component %s: %w", componentLabel, err)
		}
		for pi, rp := range componentPads {
			pm, ok := rp.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("component %s pads[%d] is not an object", componentLabel, pi)
			}
			prefix := fmt.Sprintf("component %s pads[%d]", componentLabel, pi)
			id := strings.TrimSpace(asString(pm["primitiveId"]))
			if id == "" {
				return nil, fmt.Errorf("%s.primitiveId is missing", prefix)
			}
			number := strings.TrimSpace(asString(pm["padNumber"]))
			layer, err := netPathNumber(pm, "layer", prefix)
			if err != nil || layer != math.Trunc(layer) || (int(layer) != 1 && int(layer) != 2 && int(layer) != pcbLayerMulti) {
				return nil, fmt.Errorf("%s.layer must be TOP(1), BOTTOM(2), or MULTI(12)", prefix)
			}
			x, err := netPathNumber(pm, "x", prefix)
			if err != nil {
				return nil, err
			}
			y, err := netPathNumber(pm, "y", prefix)
			if err != nil {
				return nil, err
			}
			rotation, err := netPathNumber(pm, "rotation", prefix)
			if err != nil {
				return nil, err
			}
			p := pcbPadP{ID: id, Designator: designator, Number: number, Net: asString(pm["net"]), Layer: int(layer), X: x, Y: y, Rotation: rotation}
			if w, ok := netPathOptionalFinite(pm["width"]); ok && w > 0 {
				p.W = w
			}
			if h, ok := netPathOptionalFinite(pm["height"]); ok && h > 0 {
				p.H = h
			}
			if err := parseNetPathPadShape(pm, &p, prefix); err != nil {
				return nil, err
			}
			pads = append(pads, p)
		}
	}
	return pads, nil
}

func parseNetPathLines(result map[string]any) ([]pcbTrack, []pcbArc, error) {
	lines, err := netPathRequiredSlice(result, "lines")
	if err != nil {
		return nil, nil, err
	}
	arcsRaw, err := netPathRequiredSlice(result, "arcs")
	if err != nil {
		return nil, nil, fmt.Errorf("%w (connector may be too old to expose arc data)", err)
	}
	arcsAvailable, ok := result["arcsAvailable"].(bool)
	if !ok || !arcsAvailable {
		return nil, nil, fmt.Errorf("result.arcsAvailable is not true; arc readback is unavailable and absence of arcs cannot be proven")
	}
	var tracks []pcbTrack
	for i, rl := range lines {
		m, ok := rl.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("lines[%d] is not an object", i)
		}
		prefix := fmt.Sprintf("lines[%d]", i)
		id := strings.TrimSpace(asString(m["primitiveId"]))
		if id == "" {
			return nil, nil, fmt.Errorf("%s.primitiveId is missing", prefix)
		}
		layer, err := netPathCopperLayerNumber(m, prefix)
		if err != nil {
			return nil, nil, err
		}
		x1, err := netPathNumber(m, "startX", prefix)
		if err != nil {
			return nil, nil, err
		}
		y1, err := netPathNumber(m, "startY", prefix)
		if err != nil {
			return nil, nil, err
		}
		x2, err := netPathNumber(m, "endX", prefix)
		if err != nil {
			return nil, nil, err
		}
		y2, err := netPathNumber(m, "endY", prefix)
		if err != nil {
			return nil, nil, err
		}
		w, err := netPathNumber(m, "lineWidth", prefix)
		if err != nil || w <= 0 {
			return nil, nil, fmt.Errorf("%s.lineWidth must be a finite positive number", prefix)
		}
		tracks = append(tracks, pcbTrack{ID: id, Net: asString(m["net"]), Layer: layer, X1: x1, Y1: y1, X2: x2, Y2: y2, Width: w})
	}
	var arcs []pcbArc
	for i, ra := range arcsRaw {
		m, ok := ra.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("arcs[%d] is not an object", i)
		}
		prefix := fmt.Sprintf("arcs[%d]", i)
		id := strings.TrimSpace(asString(m["primitiveId"]))
		if id == "" {
			return nil, nil, fmt.Errorf("%s.primitiveId is missing", prefix)
		}
		layer, err := netPathCopperLayerNumber(m, prefix)
		if err != nil {
			return nil, nil, err
		}
		x1, err := netPathNumber(m, "startX", prefix)
		if err != nil {
			return nil, nil, err
		}
		y1, err := netPathNumber(m, "startY", prefix)
		if err != nil {
			return nil, nil, err
		}
		x2, err := netPathNumber(m, "endX", prefix)
		if err != nil {
			return nil, nil, err
		}
		y2, err := netPathNumber(m, "endY", prefix)
		if err != nil {
			return nil, nil, err
		}
		w, err := netPathNumber(m, "lineWidth", prefix)
		if err != nil || w <= 0 {
			return nil, nil, fmt.Errorf("%s.lineWidth must be a finite positive number", prefix)
		}
		a, err := netPathNumber(m, "arcAngle", prefix)
		if err != nil || math.Abs(a) <= netPathGeomEps {
			return nil, nil, fmt.Errorf("%s.arcAngle must be a finite non-zero number", prefix)
		}
		arcs = append(arcs, pcbArc{ID: id, Net: asString(m["net"]), Layer: layer, X1: x1, Y1: y1, X2: x2, Y2: y2, Width: w, ArcAngle: a})
	}
	return tracks, arcs, nil
}

func parseNetPathVias(result map[string]any) ([]pcbViaP, error) {
	raw, err := netPathRequiredSlice(result, "vias")
	if err != nil {
		return nil, err
	}
	var vias []pcbViaP
	for i, rv := range raw {
		m, ok := rv.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("vias[%d] is not an object", i)
		}
		prefix := fmt.Sprintf("vias[%d]", i)
		id := strings.TrimSpace(asString(m["primitiveId"]))
		if id == "" {
			return nil, fmt.Errorf("%s.primitiveId is missing", prefix)
		}
		x, err := netPathNumber(m, "x", prefix)
		if err != nil {
			return nil, err
		}
		y, err := netPathNumber(m, "y", prefix)
		if err != nil {
			return nil, err
		}
		hole, err := netPathNumber(m, "holeDiameter", prefix)
		if err != nil || hole <= 0 {
			return nil, fmt.Errorf("%s.holeDiameter must be a finite positive number", prefix)
		}
		dia, err := netPathNumber(m, "diameter", prefix)
		if err != nil || dia <= 0 {
			return nil, fmt.Errorf("%s.diameter must be a finite positive number", prefix)
		}
		if dia <= hole {
			return nil, fmt.Errorf("%s.diameter must be greater than holeDiameter", prefix)
		}
		vias = append(vias, pcbViaP{ID: id, Net: asString(m["net"]), X: x, Y: y, Hole: hole, Dia: dia})
	}
	return vias, nil
}

func netPathRequiredSlice(result map[string]any, key string) ([]any, error) {
	v, ok := result[key]
	if !ok {
		return nil, fmt.Errorf("result.%s is missing; connectivity data is unavailable", key)
	}
	a, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("result.%s is not an array; connectivity data is unavailable", key)
	}
	return a, nil
}

func netPathOptionalFinite(v any) (float64, bool) {
	n, ok := asFloatOK(v)
	return n, ok && !math.IsNaN(n) && !math.IsInf(n, 0)
}

func netPathNumber(m map[string]any, key, prefix string) (float64, error) {
	n, ok := netPathOptionalFinite(m[key])
	if !ok {
		return 0, fmt.Errorf("%s.%s must be a finite number", prefix, key)
	}
	return n, nil
}

func netPathCopperLayerNumber(m map[string]any, prefix string) (int, error) {
	n, err := netPathNumber(m, "layer", prefix)
	if err != nil || n != math.Trunc(n) || !netPathCopperLayer(int(n)) {
		return 0, fmt.Errorf("%s.layer must be a copper layer id (TOP=1, BOTTOM=2, INNER=15..44)", prefix)
	}
	return int(n), nil
}

func parseNetPathPadShape(m map[string]any, p *pcbPadP, prefix string) error {
	if issue := pcbPadCaptureIssue(m); issue != "" {
		p.ShapeOK = false
		p.ShapeIssue = issue
		return fmt.Errorf("%s: %s", prefix, issue)
	}
	if special, exists := m["specialPad"]; exists && special != nil {
		a, ok := special.([]any)
		if !ok {
			return fmt.Errorf("%s.specialPad is malformed", prefix)
		}
		if len(a) > 0 {
			p.SpecialPad = true
			p.ShapeIssue = "specialPad geometry is not supported by pcb net-path"
		}
	}
	raw, exists := m["shape"]
	if !exists || raw == nil {
		p.ShapeIssue = "shape field is missing (connector/readback capability unknown)"
		return nil
	}
	shape, ok := raw.([]any)
	if !ok || len(shape) < 2 {
		return fmt.Errorf("%s.shape is malformed", prefix)
	}
	kind, ok := shape[0].(string)
	if !ok || strings.TrimSpace(kind) == "" {
		return fmt.Errorf("%s.shape[0] must be a shape name", prefix)
	}
	p.Shape = strings.ToUpper(strings.TrimSpace(kind))
	positive := func(index int, name string) (float64, error) {
		if index >= len(shape) {
			return 0, fmt.Errorf("%s.shape %s is missing", prefix, name)
		}
		n, ok := netPathOptionalFinite(shape[index])
		if !ok || n <= 0 {
			return 0, fmt.Errorf("%s.shape %s must be a finite positive number", prefix, name)
		}
		return n, nil
	}
	switch p.Shape {
	case "RECT", "ELLIPSE", "OVAL":
		w, err := positive(1, "width")
		if err != nil {
			return err
		}
		h, err := positive(2, "height")
		if err != nil {
			return err
		}
		p.ShapeW, p.ShapeH = w, h
		if p.Shape == "RECT" {
			if len(shape) < 4 {
				return fmt.Errorf("%s.shape RECT corner radius is missing", prefix)
			}
			r, ok := netPathOptionalFinite(shape[3])
			if !ok || r < 0 || r > math.Min(w, h)/2+netPathGeomEps {
				return fmt.Errorf("%s.shape RECT corner radius is invalid", prefix)
			}
			p.ShapeRound = math.Min(r, math.Min(w, h)/2)
		}
		p.ShapeOK = !p.SpecialPad
	case "NGON":
		d, err := positive(1, "diameter")
		if err != nil {
			return err
		}
		sides, err := positive(2, "side count")
		if err != nil {
			return err
		}
		if sides != math.Trunc(sides) || sides < 3 {
			return fmt.Errorf("%s.shape NGON side count must be an integer >= 3", prefix)
		}
		p.ShapeW, p.ShapeH, p.ShapeSides = d, d, int(sides)
		p.ShapeOK = !p.SpecialPad
	case "POLYGON":
		// Validate the tuple container while keeping the geometry fail-closed.
		if shape[1] == nil {
			return fmt.Errorf("%s.shape POLYGON source is missing", prefix)
		}
		p.ShapeIssue = "POLYGON pad geometry is not supported by pcb net-path"
		// Pad POLYGON sources are absolute board mil (live dump, see
		// testdata/pcb_polygon_pad). Straight-edged sources are exact copper for
		// clearance; ARC edges stay unknown rather than being chord-flattened.
		if contours, err := pcbRouteExactContours(shape[1]); err == nil {
			p.Contours = contours
			// W/H are consumed as an envelope centered on the pad origin; a
			// polygon need not be centered, so cover its farthest vertex.
			if w, h := pcbPolygonCenteredExtent(contours, p.X, p.Y); p.W <= 0 || p.H <= 0 {
				p.W, p.H = w, h
			}
		} else {
			p.ShapeIssue += "; exact clearance unavailable: " + err.Error()
		}
	case "":
		return fmt.Errorf("%s.shape name is empty", prefix)
	default:
		p.ShapeIssue = fmt.Sprintf("pad shape %q is not supported by pcb net-path", p.Shape)
	}
	return nil
}

func resolveNetPathWaypoints(pads []pcbPadP, opts pcbNetPathOptions) ([]string, string, error) {
	raw := append([]string{opts.From}, opts.Through...)
	raw = append(raw, opts.To)
	if len(raw) < 2 {
		return nil, "", fmt.Errorf("--from and --to are required")
	}
	refs := make([]string, 0, len(raw))
	seen := map[string]bool{}
	net := ""
	for _, r := range raw {
		d, n, err := splitPadRef(r)
		if err != nil {
			return nil, "", err
		}
		matches := make([]pcbPadP, 0, 1)
		for _, p := range pads {
			if strings.EqualFold(strings.TrimSpace(p.Designator), d) && strings.EqualFold(strings.TrimSpace(p.Number), n) {
				matches = append(matches, p)
			}
		}
		if len(matches) == 0 {
			return nil, "", fmt.Errorf("pad %s.%s was not found", d, n)
		}
		if len(matches) > 1 {
			return nil, "", fmt.Errorf("pad %s.%s is ambiguous (%d matches)", d, n, len(matches))
		}
		canon := strings.TrimSpace(matches[0].Designator) + "." + strings.TrimSpace(matches[0].Number)
		key := strings.ToLower(canon)
		if seen[key] {
			return nil, "", fmt.Errorf("waypoint %s is repeated", canon)
		}
		seen[key] = true
		pn := strings.TrimSpace(matches[0].Net)
		if pn == "" {
			return nil, "", fmt.Errorf("pad %s has no net; a copper path cannot be proven", canon)
		}
		if net == "" {
			net = pn
		} else if pn != net {
			return nil, "", fmt.Errorf("waypoint nets differ: %s is %q, expected %q", canon, pn, net)
		}
		refs = append(refs, canon)
	}
	if want := strings.TrimSpace(opts.Net); want != "" && want != net {
		return nil, "", fmt.Errorf("--net %q does not match waypoint net %q", want, net)
	}
	return refs, net, nil
}

func splitPadRef(s string) (string, string, error) {
	s = strings.TrimSpace(s)
	i := strings.LastIndexAny(s, ".:")
	if i <= 0 || i == len(s)-1 {
		return "", "", fmt.Errorf("invalid pad reference %q; use DESIGNATOR.PAD (for example U2.3)", s)
	}
	d, n := strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1:])
	if d == "" || n == "" {
		return "", "", fmt.Errorf("invalid pad reference %q; use DESIGNATOR.PAD", s)
	}
	return d, n, nil
}

func analyzePcbNetPath(pads []pcbPadP, tracks []pcbTrack, arcs []pcbArc, vias []pcbViaP, opts pcbNetPathOptions) (pcbNetPathReport, error) {
	refs, net, err := resolveNetPathWaypoints(pads, opts)
	if err != nil {
		return pcbNetPathReport{}, err
	}
	refSet := make(map[string]bool, len(refs))
	for _, ref := range refs {
		refSet[strings.ToLower(ref)] = true
	}
	var unknownSameNet []string
	for _, p := range pads {
		if p.Net != net || p.ShapeOK {
			continue
		}
		ref := netPathPadLabel(p)
		issue := p.ShapeIssue
		if issue == "" {
			issue = "pad shape could not be proven"
		}
		if refSet[strings.ToLower(ref)] {
			return pcbNetPathReport{}, fmt.Errorf("path is unknown: waypoint %s has unsupported copper geometry: %s", ref, issue)
		}
		unknownSameNet = append(unknownSameNet, ref+" ("+issue+")")
	}
	if len(refs) > 2 {
		if reason, err := orderedNetPathCopperAmbiguity(tracks, arcs, vias, net, opts.Layer); err != nil {
			return pcbNetPathReport{}, fmt.Errorf("ordered waypoint topology is unknown: %w", err)
		} else if reason != "" {
			return pcbNetPathReport{}, fmt.Errorf("ordered waypoint topology is unknown: %s; physical copper union is not canonicalized", reason)
		}
	}
	rep := pcbNetPathReport{
		Net: net, From: refs[0], To: refs[len(refs)-1], WaypointOrder: refs,
		Scope:          "placed pad copper + listed tracks/arcs/vias; copper pours, fills, and PLANE connectivity excluded",
		ExcludedCopper: []string{"pours", "filled regions", "PLANE layers"},
		Limitations: []string{
			"does not infer connectivity through copper pours, filled regions, or PLANE layers",
			"arc primitives are measured on their reconstructed centerlines; ambiguous or unsupported interior contacts make path measurement unknown",
			"rotated RECT/rounded RECT and OVAL contacts use shape geometry; ELLIPSE, NGON, and pad-to-pad contacts use conservative inscribed geometry that may miss edge landings but cannot invent them",
		},
	}
	if opts.Layer != nil {
		layer := *opts.Layer
		rep.RequestedLayer = &layer
		rep.Scope = fmt.Sprintf("placed pad copper + listed tracks/arcs constrained to layer %d; physical vias, copper pours, fills, and PLANE connectivity excluded", layer)
		rep.ExcludedCopper = append(rep.ExcludedCopper, "physical vias (--layer constraint)")
		rep.ExcludedCopper = append(rep.ExcludedCopper, fmt.Sprintf("tracks/arcs outside layer %d", layer))
		rep.Limitations = append(rep.Limitations, fmt.Sprintf("--layer %d builds a layer-restricted graph; physical vias and all other-layer tracks/arcs are excluded", layer))
	} else {
		rep.Limitations = append(rep.Limitations, "vias are modeled as through vias because pcb.via.list exposes no blind/buried layer pair")
		rep.Limitations = append(rep.Limitations, "direct via-on-pad overlap is not treated as connected; a listed track/arc stub must bridge the pad and via")
	}
	if len(refs) > 2 {
		rep.Through = append([]string(nil), refs[1:len(refs)-1]...)
	}

	nodes, byRef := buildNetPathNodes(pads, tracks, arcs, vias, net, opts.Layer)
	adj := make([][]int, len(nodes))
	strongAdj := make([][]int, len(nodes))
	for i := 0; i < len(nodes); i++ {
		for j := i + 1; j < len(nodes); j++ {
			if netPathNodesTouch(nodes[i], nodes[j], opts.Layer) {
				adj[i] = append(adj[i], j)
				adj[j] = append(adj[j], i)
				a, b := nodes[i], nodes[j]
				routed := func(n pcbNetPathNode) bool { return n.kind == "track" || n.kind == "arc" }
				strong := true
				if routed(a) && routed(b) {
					points, e := netPathRouteCenterlineIntersections(a, b)
					strong = e == nil && len(points) == 1
				}
				if strong {
					strongAdj[i] = append(strongAdj[i], j)
					strongAdj[j] = append(strongAdj[j], i)
				}
			}
		}
	}

	waypointNodes := make([]int, len(refs))
	for i, ref := range refs {
		idx, ok := byRef[strings.ToLower(ref)]
		if !ok {
			return pcbNetPathReport{}, fmt.Errorf("internal pad graph is missing waypoint %s", ref)
		}
		waypointNodes[i] = idx
	}
	// Prefer a complete path through exact centerline intersections. Copper
	// width can create incidental shortcuts near an explicit junction; taking
	// those shortcuts first loses a perfectly measurable routed path.
	findPath := func(graph [][]int) ([]int, bool) {
		if len(waypointNodes) == 2 {
			return bfsNetPath(graph, waypointNodes[0], waypointNodes[1]), false
		}
		return orderedSimpleNetPath(graph, waypointNodes)
	}
	combined, exhausted := findPath(strongAdj)
	if exhausted {
		return pcbNetPathReport{}, fmt.Errorf("ordered strong waypoint search exceeded its safety bound; path is unknown, not failed")
	}
	if len(combined) > 0 {
		rep.MeasurementPathKind = "exact-centerline"
	} else {
		combined, exhausted = findPath(adj)
		if exhausted {
			return pcbNetPathReport{}, fmt.Errorf("ordered waypoint search exceeded its safety bound; path is unknown, not failed")
		}
		rep.MeasurementPathKind = "copper-contact-fallback"
	}

	rep.Connected = len(combined) > 0
	if !rep.Connected {
		rep.Reason = "no single continuous listed-copper path visits the requested pads in order without reusing copper primitives"
		if opts.Layer != nil {
			rep.Reason = fmt.Sprintf("no single continuous path constrained to layer %d visits the requested pads in order without reusing copper primitives", *opts.Layer)
		}
	}
	positions := map[int]int{}
	for i, n := range combined {
		positions[n] = i
	}
	for i := 0; i+1 < len(refs); i++ {
		var path []int
		if rep.Connected {
			start, end := positions[waypointNodes[i]], positions[waypointNodes[i+1]]
			path = combined[start : end+1]
		} else {
			// Diagnostic only: report whether each neighboring pair is reachable on
			// its own. Overall remains FAIL unless one simple path carries every
			// waypoint in order; concatenated backtracking is not accepted as proof.
			path = bfsNetPath(adj, waypointNodes[i], waypointNodes[i+1])
		}
		leg := pcbNetPathLeg{From: refs[i], To: refs[i+1], Connected: len(path) > 0}
		if len(path) == 0 {
			leg.Reason = "no continuous path in listed track/arc/via copper (pours and PLANE connectivity are excluded)"
			if opts.Layer != nil {
				leg.Reason = fmt.Sprintf("no continuous path in listed copper constrained to layer %d (physical vias, pours, and PLANE connectivity are excluded)", *opts.Layer)
			}
		} else {
			leg.Path = netPathSteps(nodes, path)
			fillNetPathStats(&leg.Layers, nil, &leg.WidthsMil, &leg.MinWidth, &leg.MaxWidth, &leg.ViaCount, &leg.Primitives, nodes, path)
			leg.LengthMil, leg.TurnCount, err = measureNetPathCenterline(nodes, path)
			if err != nil {
				return pcbNetPathReport{}, fmt.Errorf("path measurement is unknown for %s -> %s: %w", refs[i], refs[i+1], err)
			}
			if !rep.Connected {
				leg.Reason = "this leg is individually reachable, but the full waypoint order has no single non-repeating copper path"
			}
		}
		rep.Legs = append(rep.Legs, leg)
	}
	if rep.Connected {
		rep.Path = netPathSteps(nodes, combined)
		fillNetPathStats(&rep.Layers, &rep.LayerSequence, &rep.WidthsMil, &rep.MinWidth, &rep.MaxWidth, &rep.ViaCount, &rep.Primitives, nodes, combined)
		rep.LengthMil, rep.TurnCount, err = measureNetPathCenterline(nodes, combined)
		if err != nil {
			return pcbNetPathReport{}, fmt.Errorf("path measurement is unknown for %s: %w", strings.Join(refs, " -> "), err)
		}
	}
	if len(unknownSameNet) > 0 {
		rep.Limitations = append(rep.Limitations, fmt.Sprintf("%d unsupported same-net pad(s) were excluded from the proof graph: %s", len(unknownSameNet), strings.Join(unknownSameNet, "; ")))
		if !rep.Connected {
			return pcbNetPathReport{}, fmt.Errorf("path is unknown, not failed: unsupported same-net pad geometry could change reachability: %s", strings.Join(unknownSameNet, "; "))
		}
	}
	return rep, nil
}

type netPathPoint struct{ x, y float64 }

// netPathCopperCurve is a routed primitive's centerline. Tracks are exact two-
// point curves. Arcs are flattened with a bounded sagitta error so the ordered
// proof can detect copper-area overlap instead of comparing primitive IDs or
// chords. ApproxErr is the maximum centerline deviation in mil.
type netPathCopperCurve struct {
	kind      string
	id        string
	layer     int
	width     float64
	points    []netPathPoint
	approxErr float64
}

// orderedNetPathCopperAmbiguity protects --through's non-repeating-primitive
// proof. Two primitive IDs are not independent graph edges when their physical
// copper overlaps away from one ordinary endpoint/T junction. In that case a
// graph path can manufacture a waypoint order that the copper topology does not
// establish, so the result must be unknown rather than PASS or FAIL.
func orderedNetPathCopperAmbiguity(tracks []pcbTrack, arcs []pcbArc, vias []pcbViaP, net string, requestedLayer *int) (string, error) {
	curves := make([]netPathCopperCurve, 0, len(tracks)+len(arcs))
	for _, t := range tracks {
		if t.Net != net || (requestedLayer != nil && t.Layer != *requestedLayer) {
			continue
		}
		if math.Hypot(t.X2-t.X1, t.Y2-t.Y1) <= netPathGeomEps {
			return "", fmt.Errorf("track %s has a degenerate centerline that cannot be normalized", t.ID)
		}
		curves = append(curves, netPathCopperCurve{
			kind: "track", id: t.ID, layer: t.Layer, width: t.Width,
			points: []netPathPoint{{t.X1, t.Y1}, {t.X2, t.Y2}},
		})
	}
	for _, a := range arcs {
		if a.Net != net || (requestedLayer != nil && a.Layer != *requestedLayer) {
			continue
		}
		points, approximation, err := flattenNetPathArc(a)
		if err != nil {
			return "", fmt.Errorf("arc %s cannot be normalized: %w", a.ID, err)
		}
		curves = append(curves, netPathCopperCurve{
			kind: "arc", id: a.ID, layer: a.Layer, width: a.Width,
			points: points, approxErr: approximation,
		})
	}

	for i := 0; i < len(curves); i++ {
		for j := i + 1; j < len(curves); j++ {
			a, b := curves[i], curves[j]
			if a.layer != b.layer || !netPathCurveBoundsMayOverlap(a, b) {
				continue
			}
			overlaps, ordinaryJunction, detail, err := netPathCurvePairOverlap(a, b)
			if err != nil {
				return "", fmt.Errorf("%s %s and %s %s: %w", a.kind, a.id, b.kind, b.id, err)
			}
			if overlaps && !ordinaryJunction {
				return fmt.Sprintf("same-layer %s %s and %s %s have %s", a.kind, a.id, b.kind, b.id, detail), nil
			}
		}
	}

	if requestedLayer == nil {
		var selected []pcbViaP
		for _, v := range vias {
			if v.Net == net {
				selected = append(selected, v)
			}
		}
		for i := 0; i < len(selected); i++ {
			for j := i + 1; j < len(selected); j++ {
				a, b := selected[i], selected[j]
				distance := math.Hypot(a.X-b.X, a.Y-b.Y)
				limit := a.Dia/2 + b.Dia/2
				if distance < limit-netPathGeomEps {
					return fmt.Sprintf("through vias %s and %s have overlapping copper annuli", a.ID, b.ID), nil
				}
				if distance <= limit+netPathGeomEps {
					return "", fmt.Errorf("through vias %s and %s are tangent within %.3g mil geometry tolerance", a.ID, b.ID, netPathGeomEps)
				}
			}
		}
	}
	return "", nil
}

// flattenNetPathArc follows the connector's verified ARC convention: a positive
// sweep advances counter-clockwise from start to end. Chords are short enough
// that their maximum sagitta is <= netPathGeomEps/4. A hard segment cap prevents
// a huge/near-full circle from silently degrading either precision or runtime.
func flattenNetPathArc(a pcbArc) ([]netPathPoint, float64, error) {
	absSweep := math.Abs(a.ArcAngle)
	chordX, chordY := a.X2-a.X1, a.Y2-a.Y1
	chord := math.Hypot(chordX, chordY)
	if absSweep <= netPathGeomEps || absSweep >= 360-netPathGeomEps || chord <= netPathGeomEps {
		return nil, 0, fmt.Errorf("requires distinct endpoints and |arcAngle| between 0 and 360 degrees")
	}
	sineHalf := math.Sin(absSweep * math.Pi / 360)
	if sineHalf <= 0 || math.IsNaN(sineHalf) || math.IsInf(sineHalf, 0) {
		return nil, 0, fmt.Errorf("invalid sweep %.9g degrees", a.ArcAngle)
	}
	radius := chord / (2 * sineHalf)
	midX, midY := (a.X1+a.X2)/2, (a.Y1+a.Y2)/2
	offsetSq := radius*radius - chord*chord/4
	if offsetSq < -netPathGeomEps*netPathGeomEps {
		return nil, 0, fmt.Errorf("radius reconstruction is inconsistent")
	}
	centerOffset := math.Sqrt(math.Max(0, offsetSq))
	normalX, normalY := -chordY/chord, chordX/chord
	type centerCandidate struct{ mismatch, x, y float64 }
	best := centerCandidate{mismatch: math.Inf(1)}
	for _, sign := range []float64{1, -1} {
		cx, cy := midX+sign*centerOffset*normalX, midY+sign*centerOffset*normalY
		start := math.Atan2(a.Y1-cy, a.X1-cx) * 180 / math.Pi
		end := math.Atan2(a.Y2-cy, a.X2-cx) * 180 / math.Pi
		directed := end - start
		if a.ArcAngle < 0 {
			directed = start - end
		}
		directed = math.Mod(directed, 360)
		if directed < 0 {
			directed += 360
		}
		candidate := centerCandidate{mismatch: math.Abs(directed - absSweep), x: cx, y: cy}
		if candidate.mismatch < best.mismatch {
			best = candidate
		}
	}
	if best.mismatch > 1e-4 || math.IsInf(radius, 0) || math.IsNaN(radius) {
		return nil, 0, fmt.Errorf("directed sweep does not match endpoints (mismatch %.6g degrees)", best.mismatch)
	}

	const targetSagitta = netPathGeomEps / 4
	cosArg := 1 - targetSagitta/radius
	cosArg = math.Max(-1, math.Min(1, cosArg))
	maxStep := 2 * math.Acos(cosArg)
	if maxStep <= 0 || math.IsNaN(maxStep) {
		return nil, 0, fmt.Errorf("cannot establish a bounded polyline approximation")
	}
	steps := int(math.Ceil(absSweep * math.Pi / 180 / maxStep))
	if steps < 1 {
		steps = 1
	}
	const maxArcSegments = 4096
	if steps > maxArcSegments {
		return nil, 0, fmt.Errorf("needs %d segments for %.4g mil error bound (limit %d)", steps, targetSagitta, maxArcSegments)
	}
	startRadians := math.Atan2(a.Y1-best.y, a.X1-best.x)
	points := make([]netPathPoint, 0, steps+1)
	points = append(points, netPathPoint{a.X1, a.Y1})
	for step := 1; step < steps; step++ {
		angle := startRadians + a.ArcAngle*math.Pi/180*float64(step)/float64(steps)
		points = append(points, netPathPoint{best.x + radius*math.Cos(angle), best.y + radius*math.Sin(angle)})
	}
	points = append(points, netPathPoint{a.X2, a.Y2})
	actualSagitta := radius * (1 - math.Cos(absSweep*math.Pi/180/float64(steps)/2))
	return points, actualSagitta, nil
}

func netPathCurveBoundsMayOverlap(a, b netPathCopperCurve) bool {
	aminX, aminY, amaxX, amaxY := netPathCurveBounds(a)
	bminX, bminY, bmaxX, bmaxY := netPathCurveBounds(b)
	margin := a.width/2 + b.width/2 + a.approxErr + b.approxErr + netPathGeomEps
	return aminX <= bmaxX+margin && bminX <= amaxX+margin && aminY <= bmaxY+margin && bminY <= amaxY+margin
}

func netPathCurveBounds(c netPathCopperCurve) (minX, minY, maxX, maxY float64) {
	minX, minY = math.Inf(1), math.Inf(1)
	maxX, maxY = math.Inf(-1), math.Inf(-1)
	for _, p := range c.points {
		minX, minY = math.Min(minX, p.x), math.Min(minY, p.y)
		maxX, maxY = math.Max(maxX, p.x), math.Max(maxY, p.y)
	}
	return
}

func netPathCurvePairOverlap(a, b netPathCopperCurve) (overlaps, ordinaryJunction bool, detail string, err error) {
	pairs := (len(a.points) - 1) * (len(b.points) - 1)
	const maxCurveSegmentPairs = 2_000_000
	if pairs > maxCurveSegmentPairs {
		return false, false, "", fmt.Errorf("requires %d segment comparisons (limit %d)", pairs, maxCurveSegmentPairs)
	}
	minDistance := math.Inf(1)
	properCross := false
	positiveCollinearOverlap := false
	for i := 0; i+1 < len(a.points); i++ {
		ap, aq := a.points[i], a.points[i+1]
		for j := 0; j+1 < len(b.points); j++ {
			bp, bq := b.points[j], b.points[j+1]
			minDistance = math.Min(minDistance, segSegDist(ap.x, ap.y, aq.x, aq.y, bp.x, bp.y, bq.x, bq.y))
			if segSegCross(ap.x, ap.y, aq.x, aq.y, bp.x, bp.y, bq.x, bq.y) {
				properCross = true
			}
			if netPathPositiveCollinearOverlap(ap, aq, bp, bq) {
				positiveCollinearOverlap = true
			}
		}
	}
	radius := a.width/2 + b.width/2
	errorBound := a.approxErr + b.approxErr + netPathGeomEps
	if minDistance > radius+errorBound {
		return false, false, "", nil
	}

	junctions := netPathEndpointJunctions(a, b)
	if len(junctions) == 1 && !properCross && !positiveCollinearOverlap {
		return true, true, "one endpoint/T junction", nil
	}
	if positiveCollinearOverlap {
		return true, false, "positive-length centerline overlap", nil
	}
	if properCross {
		return true, false, "interior centerline crossing with overlapping copper", nil
	}
	if len(junctions) > 1 {
		return true, false, fmt.Sprintf("overlapping copper at %d distinct endpoint/T junctions", len(junctions)), nil
	}
	if minDistance >= radius-errorBound {
		return true, false, "copper contact within the geometry-error band", nil
	}
	return true, false, "copper-area overlap without a single endpoint/T junction", nil
}

func netPathEndpointJunctions(a, b netPathCopperCurve) []netPathPoint {
	tolerance := netPathGeomEps + a.approxErr + b.approxErr
	var out []netPathPoint
	add := func(p netPathPoint) {
		for _, q := range out {
			if math.Hypot(p.x-q.x, p.y-q.y) <= 2*tolerance {
				return
			}
		}
		out = append(out, p)
	}
	for _, p := range []netPathPoint{a.points[0], a.points[len(a.points)-1]} {
		if netPathPointCurveDistance(p, b) <= tolerance {
			add(p)
		}
	}
	for _, p := range []netPathPoint{b.points[0], b.points[len(b.points)-1]} {
		if netPathPointCurveDistance(p, a) <= tolerance {
			add(p)
		}
	}
	return out
}

func netPathPointCurveDistance(p netPathPoint, c netPathCopperCurve) float64 {
	best := math.Inf(1)
	for i := 0; i+1 < len(c.points); i++ {
		q, r := c.points[i], c.points[i+1]
		best = math.Min(best, segPtDist(p.x, p.y, q.x, q.y, r.x, r.y))
	}
	return best
}

func netPathPositiveCollinearOverlap(a, b, c, d netPathPoint) bool {
	abx, aby := b.x-a.x, b.y-a.y
	cdx, cdy := d.x-c.x, d.y-c.y
	abLen, cdLen := math.Hypot(abx, aby), math.Hypot(cdx, cdy)
	if abLen <= netPathGeomEps || cdLen <= netPathGeomEps {
		return false
	}
	if math.Abs(abx*cdy-aby*cdx) > netPathGeomEps*abLen*cdLen {
		return false
	}
	if math.Abs(abx*(c.y-a.y)-aby*(c.x-a.x)) > netPathGeomEps*abLen {
		return false
	}
	ux, uy := abx/abLen, aby/abLen
	c0 := (c.x-a.x)*ux + (c.y-a.y)*uy
	c1 := (d.x-a.x)*ux + (d.y-a.y)*uy
	lo := math.Max(0, math.Min(c0, c1))
	hi := math.Min(abLen, math.Max(c0, c1))
	return hi-lo > netPathGeomEps
}

func buildNetPathNodes(pads []pcbPadP, tracks []pcbTrack, arcs []pcbArc, vias []pcbViaP, net string, requestedLayer *int) ([]pcbNetPathNode, map[string]int) {
	var nodes []pcbNetPathNode
	var ps []pcbPadP
	for _, p := range pads {
		if p.Net == net && p.ShapeOK {
			ps = append(ps, p)
		}
	}
	sort.Slice(ps, func(i, j int) bool {
		return strings.ToLower(netPathPadLabel(ps[i])) < strings.ToLower(netPathPadLabel(ps[j]))
	})
	byRef := map[string]int{}
	for _, p := range ps {
		ref := netPathPadLabel(p)
		n := pcbNetPathNode{kind: "pad", id: p.ID, ref: ref, net: p.Net, layer: p.Layer, x: p.X, y: p.Y, w: p.ShapeW, h: p.ShapeH, rotation: p.Rotation, shape: p.Shape, shapeRound: p.ShapeRound, shapeSides: p.ShapeSides}
		if p.Designator != "" && p.Number != "" {
			byRef[strings.ToLower(ref)] = len(nodes)
		}
		nodes = append(nodes, n)
	}
	ts := append([]pcbTrack(nil), tracks...)
	sort.Slice(ts, func(i, j int) bool { return ts[i].ID < ts[j].ID })
	for _, t := range ts {
		if t.Net == net && (requestedLayer == nil || t.Layer == *requestedLayer) {
			nodes = append(nodes, pcbNetPathNode{kind: "track", id: t.ID, net: t.Net, layer: t.Layer, width: t.Width, x1: t.X1, y1: t.Y1, x2: t.X2, y2: t.Y2})
		}
	}
	as := append([]pcbArc(nil), arcs...)
	sort.Slice(as, func(i, j int) bool { return as[i].ID < as[j].ID })
	for _, a := range as {
		if a.Net == net && (requestedLayer == nil || a.Layer == *requestedLayer) {
			nodes = append(nodes, pcbNetPathNode{kind: "arc", id: a.ID, net: a.Net, layer: a.Layer, width: a.Width, x1: a.X1, y1: a.Y1, x2: a.X2, y2: a.Y2, arcAngle: a.ArcAngle})
		}
	}
	vs := append([]pcbViaP(nil), vias...)
	sort.Slice(vs, func(i, j int) bool { return vs[i].ID < vs[j].ID })
	for _, v := range vs {
		if v.Net == net && requestedLayer == nil {
			nodes = append(nodes, pcbNetPathNode{kind: "via", id: v.ID, net: v.Net, x: v.X, y: v.Y, dia: v.Dia})
		}
	}
	return nodes, byRef
}

func netPathPadLabel(p pcbPadP) string {
	if strings.TrimSpace(p.Designator) != "" && strings.TrimSpace(p.Number) != "" {
		return strings.TrimSpace(p.Designator) + "." + strings.TrimSpace(p.Number)
	}
	return p.ID
}

func netPathNodesTouch(a, b pcbNetPathNode, requestedLayer *int) bool {
	if a.net == "" || a.net != b.net {
		return false
	}
	if requestedLayer != nil {
		if a.kind == "via" || b.kind == "via" {
			return false
		}
		if a.kind == "pad" && !padLayerMatches(a.layer, *requestedLayer) {
			return false
		}
		if b.kind == "pad" && !padLayerMatches(b.layer, *requestedLayer) {
			return false
		}
	}
	if a.kind > b.kind {
		return netPathNodesTouch(b, a, requestedLayer)
	}
	switch a.kind + ":" + b.kind {
	case "arc:arc":
		if a.layer != b.layer {
			return false
		}
		return netPathRoutedNodesTouch(a, b)
	case "arc:pad":
		if !padLayerMatches(b.layer, a.layer) {
			return false
		}
		p, _, err := netPathProjectPointToRoute(a, netPathPoint{x: b.x, y: b.y})
		return err == nil && pointTouchesPad(p.x, p.y, b, a.width/2)
	case "arc:track":
		if a.layer != b.layer {
			return false
		}
		return netPathRoutedNodesTouch(a, b)
	case "arc:via":
		_, distance, err := netPathProjectPointToRoute(a, netPathPoint{x: b.x, y: b.y})
		return err == nil && distance <= a.width/2+b.dia/2+netPathGeomEps
	case "pad:pad":
		if !padLayersCompatible(a.layer, b.layer) {
			return false
		}
		return padsTouch(a, b)
	case "pad:track":
		if !padLayerMatches(a.layer, b.layer) {
			return false
		}
		return trackTouchesPad(b, a)
	case "pad:via":
		// The host does not register a via merely overlapping a pad as connected
		// (live-verified via-in-pad behavior). Require a listed track/arc stub to
		// bridge pad↔via; otherwise this command would fabricate continuity.
		return false
	case "track:track":
		if a.layer != b.layer {
			return false
		}
		return netPathRoutedNodesTouch(a, b)
	case "track:via":
		return segPtDist(b.x, b.y, a.x1, a.y1, a.x2, a.y2) <= a.width/2+b.dia/2+netPathGeomEps
	case "via:via":
		return math.Hypot(a.x-b.x, a.y-b.y) <= a.dia/2+b.dia/2+netPathGeomEps
	}
	return false
}

func padLayerMatches(padLayer, copperLayer int) bool {
	return padLayer == pcbLayerMulti || padLayer == copperLayer
}
func padLayersCompatible(a, b int) bool { return a == pcbLayerMulti || b == pcbLayerMulti || a == b }
func netPathCopperLayer(layer int) bool {
	return layer == 1 || layer == 2 || (layer >= 15 && layer <= 44)
}

func trackTouchesPad(track, pad pcbNetPathNode) bool {
	x1, y1 := netPathPadLocal(pad, track.x1, track.y1)
	x2, y2 := netPathPadLocal(pad, track.x2, track.y2)
	r := track.width/2 + netPathGeomEps
	switch pad.shape {
	case "RECT":
		corner := pad.shapeRound
		return rectSegDist(-pad.w/2+corner, -pad.h/2+corner, pad.w/2-corner, pad.h/2-corner, x1, y1, x2, y2) <= r+corner
	case "OVAL":
		px1, py1, px2, py2, radius := netPathOvalSpine(pad)
		return segSegDist(x1, y1, x2, y2, px1, py1, px2, py2) <= r+radius
	case "ELLIPSE", "NGON":
		// Use a circle fully contained in the copper. This may miss a legal edge
		// landing on a non-circular ellipse/polygon, but can never invent one.
		return segPtDist(0, 0, x1, y1, x2, y2) <= r+netPathPadInnerRadius(pad)
	}
	return false
}

func pointTouchesPad(x, y float64, pad pcbNetPathNode, radius float64) bool {
	lx, ly := netPathPadLocal(pad, x, y)
	r := radius + netPathGeomEps
	switch pad.shape {
	case "RECT":
		corner := pad.shapeRound
		return rectPtDist(-pad.w/2+corner, -pad.h/2+corner, pad.w/2-corner, pad.h/2-corner, lx, ly) <= r+corner
	case "OVAL":
		x1, y1, x2, y2, pr := netPathOvalSpine(pad)
		return segPtDist(lx, ly, x1, y1, x2, y2) <= r+pr
	case "ELLIPSE", "NGON":
		return math.Hypot(lx, ly) <= r+netPathPadInnerRadius(pad)
	}
	return false
}

func padsTouch(a, b pcbNetPathNode) bool {
	// Direct pad-to-pad contact is uncommon. Prove only overlap of inscribed
	// circles, a conservative subset valid for every supported pad shape.
	return math.Hypot(a.x-b.x, a.y-b.y) <= netPathPadInnerRadius(a)+netPathPadInnerRadius(b)+netPathGeomEps
}

func netPathPadLocal(pad pcbNetPathNode, x, y float64) (float64, float64) {
	r := -pad.rotation * math.Pi / 180
	c, s := math.Cos(r), math.Sin(r)
	dx, dy := x-pad.x, y-pad.y
	return dx*c - dy*s, dx*s + dy*c
}

func netPathPadInnerRadius(pad pcbNetPathNode) float64 {
	switch pad.shape {
	case "NGON":
		if pad.shapeSides >= 3 {
			return pad.w / 2 * math.Cos(math.Pi/float64(pad.shapeSides))
		}
	case "RECT", "ELLIPSE", "OVAL":
		return math.Min(pad.w, pad.h) / 2
	}
	return 0
}

func netPathOvalSpine(pad pcbNetPathNode) (x1, y1, x2, y2, radius float64) {
	radius = math.Min(pad.w, pad.h) / 2
	if pad.w >= pad.h {
		half := (pad.w - pad.h) / 2
		return -half, 0, half, 0, radius
	}
	half := (pad.h - pad.w) / 2
	return 0, -half, 0, half, radius
}

func endpointsNear(a, b pcbNetPathNode, radius float64) bool {
	for _, p := range [][2]float64{{a.x1, a.y1}, {a.x2, a.y2}} {
		for _, q := range [][2]float64{{b.x1, b.y1}, {b.x2, b.y2}} {
			if math.Hypot(p[0]-q[0], p[1]-q[1]) <= radius {
				return true
			}
		}
	}
	return false
}

func bfsNetPath(adj [][]int, start, goal int) []int {
	if start == goal {
		return []int{start}
	}
	prev := make([]int, len(adj))
	for i := range prev {
		prev[i] = -2
	}
	prev[start] = -1
	q := []int{start}
	for len(q) > 0 {
		v := q[0]
		q = q[1:]
		for _, n := range adj[v] {
			if prev[n] != -2 {
				continue
			}
			prev[n] = v
			if n == goal {
				var rev []int
				for at := goal; at >= 0; at = prev[at] {
					rev = append(rev, at)
				}
				for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
					rev[i], rev[j] = rev[j], rev[i]
				}
				return rev
			}
			q = append(q, n)
		}
	}
	return nil
}

// orderedSimpleNetPath finds one non-repeating graph path that visits every
// requested pad in order. Checking each leg independently is insufficient on an
// undirected net: any connected branch can be walked in arbitrary order by
// backtracking, which would falsely "prove" a daisy-chain topology. The bounded
// DFS rejects future waypoints before their turn and never reuses a primitive.
func orderedSimpleNetPath(adj [][]int, waypoints []int) ([]int, bool) {
	if len(waypoints) < 2 {
		return nil, false
	}
	required := make(map[int]int, len(waypoints))
	for i, n := range waypoints {
		required[n] = i
	}
	dists := make([][]int, len(waypoints))
	for i := 1; i < len(waypoints); i++ {
		dists[i] = netPathDistances(adj, waypoints[i])
	}
	visited := make([]bool, len(adj))
	visited[waypoints[0]] = true
	path := []int{waypoints[0]}
	const maxStates = 500000
	states := 0
	exhausted := false
	var search func(node, next int) bool
	search = func(node, next int) bool {
		states++
		if states > maxStates {
			exhausted = true
			return false
		}
		if next < len(waypoints) && node == waypoints[next] {
			next++
		}
		if next == len(waypoints) {
			return true
		}
		candidates := make([]int, 0, len(adj[node]))
		for _, n := range adj[node] {
			if visited[n] {
				continue
			}
			if pos, isWaypoint := required[n]; isWaypoint && pos > next {
				continue // encountering a later waypoint now would violate order
			}
			candidates = append(candidates, n)
		}
		sort.SliceStable(candidates, func(i, j int) bool {
			di, dj := dists[next][candidates[i]], dists[next][candidates[j]]
			if di != dj {
				return di < dj
			}
			return candidates[i] < candidates[j]
		})
		for _, n := range candidates {
			visited[n] = true
			path = append(path, n)
			if search(n, next) {
				return true
			}
			path = path[:len(path)-1]
			visited[n] = false
			if exhausted {
				return false
			}
		}
		return false
	}
	if search(waypoints[0], 1) {
		return append([]int(nil), path...), false
	}
	return nil, exhausted
}

func netPathDistances(adj [][]int, goal int) []int {
	const unreachable = int(^uint(0) >> 1)
	d := make([]int, len(adj))
	for i := range d {
		d[i] = unreachable
	}
	d[goal] = 0
	q := []int{goal}
	for len(q) > 0 {
		v := q[0]
		q = q[1:]
		for _, n := range adj[v] {
			if d[n] != unreachable {
				continue
			}
			d[n] = d[v] + 1
			q = append(q, n)
		}
	}
	return d
}

func netPathSteps(nodes []pcbNetPathNode, path []int) []pcbNetPathStep {
	out := make([]pcbNetPathStep, 0, len(path))
	for _, i := range path {
		n := nodes[i]
		s := pcbNetPathStep{Kind: n.kind, PrimitiveID: n.id, Ref: n.ref, Layer: n.layer, WidthMil: n.width}
		switch n.kind {
		case "pad", "via":
			s.At = &pcbNetPathPoint{X: round2(n.x), Y: round2(n.y)}
		case "track", "arc":
			s.Start = &pcbNetPathPoint{X: round2(n.x1), Y: round2(n.y1)}
			s.End = &pcbNetPathPoint{X: round2(n.x2), Y: round2(n.y2)}
			s.ArcAngle = round2(n.arcAngle)
		}
		out = append(out, s)
	}
	return out
}

func fillNetPathStats(layers, sequence *[]int, widths *[]float64, minW, maxW **float64, viaCount, primitives *int, nodes []pcbNetPathNode, path []int) {
	layerSet := map[int]bool{}
	widthSet := map[float64]bool{}
	viaSet := map[string]bool{}
	primSet := map[string]bool{}
	var seq []int
	for _, i := range path {
		n := nodes[i]
		if n.kind == "track" || n.kind == "arc" {
			layerSet[n.layer] = true
			if len(seq) == 0 || seq[len(seq)-1] != n.layer {
				seq = append(seq, n.layer)
			}
			if n.width > 0 {
				widthSet[round2(n.width)] = true
			}
			primSet[n.kind+":"+n.id] = true
		} else if n.kind == "via" {
			viaSet[n.id] = true
			primSet[n.kind+":"+n.id] = true
		}
	}
	for l := range layerSet {
		*layers = append(*layers, l)
	}
	sort.Ints(*layers)
	if sequence != nil {
		*sequence = seq
	}
	for w := range widthSet {
		*widths = append(*widths, w)
	}
	sort.Float64s(*widths)
	if len(*widths) > 0 {
		a, b := (*widths)[0], (*widths)[len(*widths)-1]
		*minW = &a
		*maxW = &b
	}
	*viaCount = len(viaSet)
	*primitives = len(primSet)
}

type netPathArcCenterline struct {
	center netPathPoint
	radius float64
	start  float64
	sweep  float64
}

type netPathRouteContact struct {
	a netPathPoint
	b netPathPoint
}

type netPathTraversal struct {
	length     float64
	startAngle float64
	endAngle   float64
	curved     bool
}

// measureNetPathCenterline measures only the portion of each routed primitive
// actually traversed by the selected graph path. Each previous/next graph node
// is projected onto the track or arc centerline to establish its entry/exit.
// Pad interiors and vertical barrel length remain excluded. A copper-area
// contact that does not establish one unique centerline junction is unknown;
// summing the whole primitive in that case would manufacture an "actual" value.
func measureNetPathCenterline(nodes []pcbNetPathNode, path []int) (float64, int, error) {
	length := 0.0
	turns := 0
	lastAngle := 0.0
	haveDirection := false
	for pos, idx := range path {
		if idx < 0 || idx >= len(nodes) {
			return 0, 0, fmt.Errorf("path node index %d is out of range", idx)
		}
		n := nodes[idx]
		if n.kind == "via" {
			haveDirection = false
			continue
		}
		if n.kind != "track" && n.kind != "arc" {
			continue
		}
		if pos == 0 || pos+1 >= len(path) {
			return 0, 0, fmt.Errorf("%s %s has no complete entry/exit in the selected path", n.kind, n.id)
		}
		entryNeighbor, exitNeighbor := nodes[path[pos-1]], nodes[path[pos+1]]
		entry, err := netPathJunctionPointOnRoute(n, entryNeighbor)
		if err != nil {
			return 0, 0, fmt.Errorf("%s %s entry from %s %s: %w", n.kind, n.id, entryNeighbor.kind, entryNeighbor.id, err)
		}
		exit, err := netPathJunctionPointOnRoute(n, exitNeighbor)
		if err != nil {
			return 0, 0, fmt.Errorf("%s %s exit to %s %s: %w", n.kind, n.id, exitNeighbor.kind, exitNeighbor.id, err)
		}
		traversal, err := netPathMeasureTraversal(n, entry, exit)
		if err != nil {
			return 0, 0, fmt.Errorf("%s %s traversed subsection: %w", n.kind, n.id, err)
		}
		length += traversal.length
		if traversal.length <= netPathGeomEps {
			continue
		}
		if haveDirection && netPathUndirectedAngleDelta(lastAngle, traversal.startAngle) > 1e-6 {
			turns++
		}
		if traversal.curved {
			turns++
		}
		lastAngle, haveDirection = traversal.endAngle, true
	}
	return round4(length), turns, nil
}

func netPathMeasureTraversal(route pcbNetPathNode, entry, exit netPathPoint) (netPathTraversal, error) {
	switch route.kind {
	case "track":
		length := math.Hypot(exit.x-entry.x, exit.y-entry.y)
		if length <= netPathGeomEps {
			return netPathTraversal{}, nil
		}
		angle := math.Atan2(exit.y-entry.y, exit.x-entry.x)
		return netPathTraversal{length: length, startAngle: angle, endAngle: angle}, nil
	case "arc":
		arc, err := netPathArcCenterlineForNode(route)
		if err != nil {
			return netPathTraversal{}, err
		}
		entryParam, err := netPathArcParamAtPoint(arc, entry)
		if err != nil {
			return netPathTraversal{}, fmt.Errorf("entry point: %w", err)
		}
		exitParam, err := netPathArcParamAtPoint(arc, exit)
		if err != nil {
			return netPathTraversal{}, fmt.Errorf("exit point: %w", err)
		}
		travelSweep := arc.sweep * (exitParam - entryParam)
		length := arc.radius * math.Abs(travelSweep)
		if length <= netPathGeomEps {
			return netPathTraversal{}, nil
		}
		direction := 1.0
		if travelSweep < 0 {
			direction = -1
		}
		entryRadial := arc.start + arc.sweep*entryParam
		exitRadial := arc.start + arc.sweep*exitParam
		return netPathTraversal{
			length:     length,
			startAngle: entryRadial + direction*math.Pi/2,
			endAngle:   exitRadial + direction*math.Pi/2,
			curved:     true,
		}, nil
	default:
		return netPathTraversal{}, fmt.Errorf("unsupported routed primitive kind %q", route.kind)
	}
}

func netPathUndirectedAngleDelta(a, b float64) float64 {
	d := math.Mod(math.Abs(a-b), math.Pi)
	if d < 0 {
		d += math.Pi
	}
	return math.Min(d, math.Pi-d)
}

func netPathJunctionPointOnRoute(route, neighbor pcbNetPathNode) (netPathPoint, error) {
	switch neighbor.kind {
	case "pad", "via":
		point, _, err := netPathProjectPointToRoute(route, netPathPoint{x: neighbor.x, y: neighbor.y})
		return point, err
	case "track", "arc":
		contacts, err := netPathRoutedContactCandidates(route, neighbor)
		if err != nil {
			return netPathPoint{}, err
		}
		if len(contacts) == 0 {
			return netPathPoint{}, fmt.Errorf("touching copper has no supported centerline junction")
		}
		if len(contacts) > 1 {
			return netPathPoint{}, fmt.Errorf("touching copper has %d distinct centerline junction candidates: route=(%.4f,%.4f)-(%.4f,%.4f) neighbor=(%.4f,%.4f)-(%.4f,%.4f) contacts=%v", len(contacts), route.x1, route.y1, route.x2, route.y2, neighbor.x1, neighbor.y1, neighbor.x2, neighbor.y2, contacts)
		}
		return contacts[0].a, nil
	default:
		return netPathPoint{}, fmt.Errorf("unsupported adjacent primitive kind %q", neighbor.kind)
	}
}

func netPathProjectPointToRoute(route pcbNetPathNode, point netPathPoint) (netPathPoint, float64, error) {
	switch route.kind {
	case "track":
		dx, dy := route.x2-route.x1, route.y2-route.y1
		denom := dx*dx + dy*dy
		if denom <= netPathGeomEps*netPathGeomEps {
			return netPathPoint{}, 0, fmt.Errorf("degenerate track centerline")
		}
		t := ((point.x-route.x1)*dx + (point.y-route.y1)*dy) / denom
		t = math.Max(0, math.Min(1, t))
		projected := netPathPoint{x: route.x1 + t*dx, y: route.y1 + t*dy}
		return projected, math.Hypot(point.x-projected.x, point.y-projected.y), nil
	case "arc":
		arc, err := netPathArcCenterlineForNode(route)
		if err != nil {
			return netPathPoint{}, 0, err
		}
		dx, dy := point.x-arc.center.x, point.y-arc.center.y
		distanceToCenter := math.Hypot(dx, dy)
		if distanceToCenter <= netPathGeomEps {
			return netPathPoint{}, 0, fmt.Errorf("point at arc center has no unique projection")
		}
		angle := math.Atan2(dy, dx)
		delta := netPathDirectedAngleDelta(arc.start, angle, arc.sweep)
		angularTolerance := netPathGeomEps / math.Max(arc.radius, netPathGeomEps)
		if delta <= math.Abs(arc.sweep)+angularTolerance {
			delta = math.Min(delta, math.Abs(arc.sweep))
			if arc.sweep < 0 {
				angle = arc.start - delta
			} else {
				angle = arc.start + delta
			}
			projected := netPathPoint{x: arc.center.x + arc.radius*math.Cos(angle), y: arc.center.y + arc.radius*math.Sin(angle)}
			return projected, math.Hypot(point.x-projected.x, point.y-projected.y), nil
		}
		start := netPathPoint{x: route.x1, y: route.y1}
		end := netPathPoint{x: route.x2, y: route.y2}
		startDistance := math.Hypot(point.x-start.x, point.y-start.y)
		endDistance := math.Hypot(point.x-end.x, point.y-end.y)
		if math.Abs(startDistance-endDistance) <= netPathGeomEps {
			return netPathPoint{}, 0, fmt.Errorf("point has equally near arc endpoints; projection is ambiguous")
		}
		if startDistance < endDistance {
			return start, startDistance, nil
		}
		return end, endDistance, nil
	default:
		return netPathPoint{}, 0, fmt.Errorf("unsupported routed primitive kind %q", route.kind)
	}
}

func netPathArcCenterlineForNode(n pcbNetPathNode) (netPathArcCenterline, error) {
	if n.kind != "arc" {
		return netPathArcCenterline{}, fmt.Errorf("primitive is %q, not arc", n.kind)
	}
	absSweep := math.Abs(n.arcAngle) * math.Pi / 180
	chordX, chordY := n.x2-n.x1, n.y2-n.y1
	chord := math.Hypot(chordX, chordY)
	if absSweep <= netPathGeomEps*math.Pi/180 || absSweep >= 2*math.Pi-netPathGeomEps*math.Pi/180 || chord <= netPathGeomEps {
		return netPathArcCenterline{}, fmt.Errorf("requires distinct endpoints and |arcAngle| between 0 and 360 degrees")
	}
	sineHalf := math.Sin(absSweep / 2)
	if sineHalf <= 0 || math.IsNaN(sineHalf) || math.IsInf(sineHalf, 0) {
		return netPathArcCenterline{}, fmt.Errorf("invalid sweep %.9g degrees", n.arcAngle)
	}
	radius := chord / (2 * sineHalf)
	mid := netPathPoint{x: (n.x1 + n.x2) / 2, y: (n.y1 + n.y2) / 2}
	offsetSq := radius*radius - chord*chord/4
	if offsetSq < -netPathGeomEps*netPathGeomEps {
		return netPathArcCenterline{}, fmt.Errorf("radius reconstruction is inconsistent")
	}
	offset := math.Sqrt(math.Max(0, offsetSq))
	normalX, normalY := -chordY/chord, chordX/chord
	type candidate struct {
		mismatch float64
		center   netPathPoint
		start    float64
	}
	best := candidate{mismatch: math.Inf(1)}
	for _, sign := range []float64{1, -1} {
		center := netPathPoint{x: mid.x + sign*offset*normalX, y: mid.y + sign*offset*normalY}
		start := math.Atan2(n.y1-center.y, n.x1-center.x)
		end := math.Atan2(n.y2-center.y, n.x2-center.x)
		directed := netPathDirectedAngleDelta(start, end, n.arcAngle)
		c := candidate{mismatch: math.Abs(directed - absSweep), center: center, start: start}
		if c.mismatch < best.mismatch {
			best = c
		}
	}
	if best.mismatch > 1e-6 || math.IsNaN(radius) || math.IsInf(radius, 0) {
		return netPathArcCenterline{}, fmt.Errorf("directed sweep does not match endpoints (mismatch %.6g radians)", best.mismatch)
	}
	sweep := absSweep
	if n.arcAngle < 0 {
		sweep = -sweep
	}
	return netPathArcCenterline{center: best.center, radius: radius, start: best.start, sweep: sweep}, nil
}

func netPathDirectedAngleDelta(start, end, direction float64) float64 {
	delta := end - start
	if direction < 0 {
		delta = start - end
	}
	delta = math.Mod(delta, 2*math.Pi)
	if delta < 0 {
		delta += 2 * math.Pi
	}
	return delta
}

func netPathArcParamAtPoint(arc netPathArcCenterline, point netPathPoint) (float64, error) {
	dx, dy := point.x-arc.center.x, point.y-arc.center.y
	radial := math.Hypot(dx, dy)
	if math.Abs(radial-arc.radius) > 4*netPathGeomEps {
		return 0, fmt.Errorf("point is %.6g mil off the arc centerline", math.Abs(radial-arc.radius))
	}
	delta := netPathDirectedAngleDelta(arc.start, math.Atan2(dy, dx), arc.sweep)
	tolerance := 4 * netPathGeomEps / math.Max(arc.radius, netPathGeomEps)
	if delta > math.Abs(arc.sweep)+tolerance {
		return 0, fmt.Errorf("point is outside the declared arc sweep")
	}
	return math.Min(delta, math.Abs(arc.sweep)) / math.Abs(arc.sweep), nil
}

func netPathRoutedNodesTouch(a, b pcbNetPathNode) bool {
	curveA, err := netPathNodeCurve(a)
	if err != nil {
		return false
	}
	curveB, err := netPathNodeCurve(b)
	if err != nil {
		return false
	}
	limit := a.width/2 + b.width/2 + curveA.approxErr + curveB.approxErr + netPathGeomEps
	for i := 0; i+1 < len(curveA.points); i++ {
		for j := 0; j+1 < len(curveB.points); j++ {
			if segSegDist(curveA.points[i].x, curveA.points[i].y, curveA.points[i+1].x, curveA.points[i+1].y, curveB.points[j].x, curveB.points[j].y, curveB.points[j+1].x, curveB.points[j+1].y) <= limit {
				return true
			}
		}
	}
	return false
}

func netPathNodeCurve(n pcbNetPathNode) (netPathCopperCurve, error) {
	switch n.kind {
	case "track":
		if math.Hypot(n.x2-n.x1, n.y2-n.y1) <= netPathGeomEps {
			return netPathCopperCurve{}, fmt.Errorf("degenerate track centerline")
		}
		return netPathCopperCurve{kind: n.kind, id: n.id, layer: n.layer, width: n.width, points: []netPathPoint{{x: n.x1, y: n.y1}, {x: n.x2, y: n.y2}}}, nil
	case "arc":
		points, approximation, err := flattenNetPathArc(pcbArc{ID: n.id, Net: n.net, Layer: n.layer, X1: n.x1, Y1: n.y1, X2: n.x2, Y2: n.y2, Width: n.width, ArcAngle: n.arcAngle})
		if err != nil {
			return netPathCopperCurve{}, err
		}
		return netPathCopperCurve{kind: n.kind, id: n.id, layer: n.layer, width: n.width, points: points, approxErr: approximation}, nil
	default:
		return netPathCopperCurve{}, fmt.Errorf("unsupported routed primitive kind %q", n.kind)
	}
}

func netPathRoutedContactCandidates(a, b pcbNetPathNode) ([]netPathRouteContact, error) {
	intersections, err := netPathRouteCenterlineIntersections(a, b)
	if err != nil {
		return nil, err
	}
	// A unique exact centerline intersection defines the routed junction.
	// Short 45-degree neighbors can additionally overlap within their widths;
	// endpoint projections of that copper must not invent a second junction.
	// Positive-length collinear overlap was rejected above, and multiple true
	// intersections remain multiple candidates below.
	if len(intersections) == 1 {
		return []netPathRouteContact{{a: intersections[0], b: intersections[0]}}, nil
	}
	contacts := make([]netPathRouteContact, 0, len(intersections)+4)
	add := func(contact netPathRouteContact) {
		for _, existing := range contacts {
			if math.Hypot(contact.a.x-existing.a.x, contact.a.y-existing.a.y) <= 4*netPathGeomEps && math.Hypot(contact.b.x-existing.b.x, contact.b.y-existing.b.y) <= 4*netPathGeomEps {
				return
			}
		}
		contacts = append(contacts, contact)
	}
	for _, point := range intersections {
		add(netPathRouteContact{a: point, b: point})
	}
	limit := a.width/2 + b.width/2 + netPathGeomEps
	for _, endpoint := range []netPathPoint{{x: a.x1, y: a.y1}, {x: a.x2, y: a.y2}} {
		projected, distance, err := netPathProjectPointToRoute(b, endpoint)
		if err != nil {
			return nil, err
		}
		if distance <= limit {
			add(netPathRouteContact{a: endpoint, b: projected})
		}
	}
	for _, endpoint := range []netPathPoint{{x: b.x1, y: b.y1}, {x: b.x2, y: b.y2}} {
		projected, distance, err := netPathProjectPointToRoute(a, endpoint)
		if err != nil {
			return nil, err
		}
		if distance <= limit {
			add(netPathRouteContact{a: projected, b: endpoint})
		}
	}
	return contacts, nil
}

func netPathRouteCenterlineIntersections(a, b pcbNetPathNode) ([]netPathPoint, error) {
	switch a.kind + ":" + b.kind {
	case "track:track":
		return netPathTrackTrackIntersections(a, b)
	case "track:arc":
		return netPathTrackArcIntersections(a, b)
	case "arc:track":
		return netPathTrackArcIntersections(b, a)
	case "arc:arc":
		return netPathArcArcIntersections(a, b)
	default:
		return nil, fmt.Errorf("unsupported routed contact %s:%s", a.kind, b.kind)
	}
}

func netPathTrackTrackIntersections(a, b pcbNetPathNode) ([]netPathPoint, error) {
	p := netPathPoint{x: a.x1, y: a.y1}
	r := netPathPoint{x: a.x2 - a.x1, y: a.y2 - a.y1}
	q := netPathPoint{x: b.x1, y: b.y1}
	s := netPathPoint{x: b.x2 - b.x1, y: b.y2 - b.y1}
	cross := func(u, v netPathPoint) float64 { return u.x*v.y - u.y*v.x }
	denom := cross(r, s)
	qp := netPathPoint{x: q.x - p.x, y: q.y - p.y}
	tolerance := netPathGeomEps * math.Max(1, math.Hypot(r.x, r.y)*math.Hypot(s.x, s.y))
	if math.Abs(denom) <= tolerance {
		if math.Abs(cross(qp, r)) > netPathGeomEps*math.Max(1, math.Hypot(r.x, r.y)) {
			return nil, nil
		}
		denomA := r.x*r.x + r.y*r.y
		if denomA <= netPathGeomEps*netPathGeomEps {
			return nil, fmt.Errorf("degenerate track centerline")
		}
		t0 := ((b.x1-a.x1)*r.x + (b.y1-a.y1)*r.y) / denomA
		t1 := ((b.x2-a.x1)*r.x + (b.y2-a.y1)*r.y) / denomA
		lo := math.Max(0, math.Min(t0, t1))
		hi := math.Min(1, math.Max(t0, t1))
		if hi < lo-netPathGeomEps {
			return nil, nil
		}
		if (hi-lo)*math.Hypot(r.x, r.y) > netPathGeomEps {
			return nil, fmt.Errorf("collinear centerlines overlap for a positive length")
		}
		t := math.Max(0, math.Min(1, (lo+hi)/2))
		return []netPathPoint{{x: p.x + t*r.x, y: p.y + t*r.y}}, nil
	}
	t := cross(qp, s) / denom
	u := cross(qp, r) / denom
	if t < -netPathGeomEps || t > 1+netPathGeomEps || u < -netPathGeomEps || u > 1+netPathGeomEps {
		return nil, nil
	}
	t = math.Max(0, math.Min(1, t))
	return []netPathPoint{{x: p.x + t*r.x, y: p.y + t*r.y}}, nil
}

func netPathTrackArcIntersections(track, arcNode pcbNetPathNode) ([]netPathPoint, error) {
	arc, err := netPathArcCenterlineForNode(arcNode)
	if err != nil {
		return nil, err
	}
	dx, dy := track.x2-track.x1, track.y2-track.y1
	fx, fy := track.x1-arc.center.x, track.y1-arc.center.y
	a := dx*dx + dy*dy
	if a <= netPathGeomEps*netPathGeomEps {
		return nil, fmt.Errorf("degenerate track centerline")
	}
	b := 2 * (fx*dx + fy*dy)
	c := fx*fx + fy*fy - arc.radius*arc.radius
	discriminant := b*b - 4*a*c
	if discriminant < -netPathGeomEps {
		return nil, nil
	}
	discriminant = math.Max(0, discriminant)
	roots := []float64{(-b - math.Sqrt(discriminant)) / (2 * a)}
	if discriminant > netPathGeomEps {
		roots = append(roots, (-b+math.Sqrt(discriminant))/(2*a))
	}
	var out []netPathPoint
	for _, t := range roots {
		if t < -netPathGeomEps || t > 1+netPathGeomEps {
			continue
		}
		t = math.Max(0, math.Min(1, t))
		point := netPathPoint{x: track.x1 + t*dx, y: track.y1 + t*dy}
		if _, err := netPathArcParamAtPoint(arc, point); err == nil {
			out = netPathAppendDistinctPoint(out, point)
		}
	}
	return out, nil
}

func netPathArcArcIntersections(aNode, bNode pcbNetPathNode) ([]netPathPoint, error) {
	a, err := netPathArcCenterlineForNode(aNode)
	if err != nil {
		return nil, err
	}
	b, err := netPathArcCenterlineForNode(bNode)
	if err != nil {
		return nil, err
	}
	dx, dy := b.center.x-a.center.x, b.center.y-a.center.y
	d := math.Hypot(dx, dy)
	if d <= netPathGeomEps && math.Abs(a.radius-b.radius) <= netPathGeomEps {
		return nil, nil // endpoint candidates below distinguish one join from overlap
	}
	if d <= netPathGeomEps || d > a.radius+b.radius+netPathGeomEps || d < math.Abs(a.radius-b.radius)-netPathGeomEps {
		return nil, nil
	}
	x := (a.radius*a.radius - b.radius*b.radius + d*d) / (2 * d)
	hSq := a.radius*a.radius - x*x
	if hSq < -netPathGeomEps {
		return nil, nil
	}
	h := math.Sqrt(math.Max(0, hSq))
	base := netPathPoint{x: a.center.x + x*dx/d, y: a.center.y + x*dy/d}
	points := []netPathPoint{{x: base.x - h*dy/d, y: base.y + h*dx/d}}
	if h > netPathGeomEps {
		points = append(points, netPathPoint{x: base.x + h*dy/d, y: base.y - h*dx/d})
	}
	var out []netPathPoint
	for _, point := range points {
		if _, err := netPathArcParamAtPoint(a, point); err != nil {
			continue
		}
		if _, err := netPathArcParamAtPoint(b, point); err != nil {
			continue
		}
		out = netPathAppendDistinctPoint(out, point)
	}
	return out, nil
}

func netPathAppendDistinctPoint(points []netPathPoint, point netPathPoint) []netPathPoint {
	for _, existing := range points {
		if math.Hypot(point.x-existing.x, point.y-existing.y) <= 4*netPathGeomEps {
			return points
		}
	}
	return append(points, point)
}

func renderPcbNetPath(rep pcbNetPathReport, out io.Writer) {
	status := "PASS"
	if !rep.Connected {
		status = "FAIL"
	}
	fmt.Fprintf(out, "PCB net path: %s  net=%s  %s\n", status, rep.Net, strings.Join(rep.WaypointOrder, " -> "))
	fmt.Fprintf(out, "scope: %s\n", rep.Scope)
	if rep.Reason != "" {
		fmt.Fprintf(out, "reason: %s\n", rep.Reason)
	}
	if rep.RequestedLayer != nil {
		fmt.Fprintf(out, "requested layer: %s (physical vias excluded)\n", formatNetPathLayers([]int{*rep.RequestedLayer}))
	}
	if rep.Connected {
		fmt.Fprintf(out, "layers: %s; sequence: %s; widths: %s; vias: %d; copper primitives: %d; length: %.4fmil; turns: %d\n", formatNetPathLayers(rep.Layers), formatNetPathLayers(rep.LayerSequence), formatNetPathWidths(rep.WidthsMil), rep.ViaCount, rep.Primitives, rep.LengthMil, rep.TurnCount)
	}
	for i, leg := range rep.Legs {
		if !leg.Connected {
			fmt.Fprintf(out, "leg %d FAIL  %s -> %s: %s\n", i+1, leg.From, leg.To, leg.Reason)
			continue
		}
		fmt.Fprintf(out, "leg %d PASS  %s -> %s  layers=%s widths=%s vias=%d length=%.4fmil turns=%d\n", i+1, leg.From, leg.To, formatNetPathLayers(leg.Layers), formatNetPathWidths(leg.WidthsMil), leg.ViaCount, leg.LengthMil, leg.TurnCount)
		for _, s := range leg.Path {
			switch s.Kind {
			case "pad":
				fmt.Fprintf(out, "  PAD   %-12s L%d @ (%.2f, %.2f)\n", s.Ref, s.Layer, s.At.X, s.At.Y)
			case "via":
				fmt.Fprintf(out, "  VIA   %-12s @ (%.2f, %.2f)\n", s.PrimitiveID, s.At.X, s.At.Y)
			default:
				fmt.Fprintf(out, "  %-5s %-12s L%d %.2fmil (%.2f, %.2f) -> (%.2f, %.2f)\n", strings.ToUpper(s.Kind), s.PrimitiveID, s.Layer, s.WidthMil, s.Start.X, s.Start.Y, s.End.X, s.End.Y)
			}
		}
	}
	for _, l := range rep.Limitations {
		fmt.Fprintf(out, "limit: %s\n", l)
	}
}

func formatNetPathLayers(in []int) string {
	if len(in) == 0 {
		return "none"
	}
	parts := make([]string, len(in))
	for i, l := range in {
		switch l {
		case 1:
			parts[i] = "TOP(1)"
		case 2:
			parts[i] = "BOTTOM(2)"
		default:
			parts[i] = fmt.Sprintf("L%d", l)
		}
	}
	return strings.Join(parts, " -> ")
}
func formatNetPathWidths(in []float64) string {
	if len(in) == 0 {
		return "none"
	}
	parts := make([]string, len(in))
	for i, w := range in {
		parts[i] = fmt.Sprintf("%gmil", w)
	}
	return strings.Join(parts, ",")
}
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// pcbPolygonCenteredExtent is the smallest axis-aligned box centered on (x,y)
// that contains every contour vertex: twice the farthest |dx| and |dy|.
func pcbPolygonCenteredExtent(contours [][][2]float64, x, y float64) (float64, float64) {
	var dx, dy float64
	for _, contour := range contours {
		for _, p := range contour {
			dx = math.Max(dx, math.Abs(p[0]-x))
			dy = math.Max(dy, math.Abs(p[1]-y))
		}
	}
	return 2 * dx, 2 * dy
}
