package app

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcbrouting"
)

// PCB identities and units belong to the host adapter, not the search package.
type pcbRouteRequest struct {
	SchemaVersion int     `json:"schemaVersion"`
	Units         string  `json:"units"`
	Net           string  `json:"net"`
	Layer         int     `json:"layer"`
	WidthMil      float64 `json:"widthMil"`
	From          string  `json:"from"`
	To            string  `json:"to"`
	StepMil       float64 `json:"stepMil"`
	MaxDetourMil  float64 `json:"maxDetourMil"`
	MaxStates     int     `json:"maxStates"`
}

type pcbRouteCandidate struct {
	Net      string       `json:"net"`
	Layer    int          `json:"layer"`
	WidthMil float64      `json:"widthMil"`
	Points   [][2]float64 `json:"points"`
}

type pcbRouteReport struct {
	SchemaVersion int                `json:"schemaVersion"`
	Status        string             `json:"status"`
	BoardSHA256   string             `json:"boardSha256"`
	RequestSHA256 string             `json:"requestSha256"`
	PlanSHA256    string             `json:"planSha256,omitempty"`
	Preview       string             `json:"preview"`
	Candidate     *pcbRouteCandidate `json:"candidate,omitempty"`
	Search        *pcbrouting.Result `json:"search,omitempty"`
	LengthMil     float64            `json:"lengthMil"`
	Bends         int                `json:"bends"`
	Reasons       []string           `json:"reasons,omitempty"`
	Limitations   []string           `json:"limitations"`
}

func newPCBRouteReport(board, request []byte) pcbRouteReport {
	return pcbRouteReport{SchemaVersion: 1, Status: "incomplete", BoardSHA256: sha256String(board), RequestSHA256: sha256String(request), Limitations: []string{
		"offline single-layer endpoint-pair proof only; no editor writes or saved-state evidence",
		"no global optimality, whole-board routing, differential-pair, impedance or return-path proof",
	}}
}

func preparePCBRouteRequest(in pcbRouteRequest, snap *boardSnapshot) (pcbrouting.Request, pcbrouting.SegmentClear, string, error) {
	r := pcbrouting.Request{Step: in.StepMil, MaxDetour: in.MaxDetourMil, MaxStates: in.MaxStates}
	fail := func(status, reason string) (pcbrouting.Request, pcbrouting.SegmentClear, string, error) {
		return r, nil, status, fmt.Errorf("%s", reason)
	}
	if in.SchemaVersion != 1 || in.Units != "mil" || strings.TrimSpace(in.Net) == "" || in.From == "" || in.To == "" || in.From == in.To || !allFinite(in.WidthMil) || in.WidthMil <= 0 {
		return fail("fail", "route request requires schemaVersion 1, units mil, net, distinct from/to pads and finite positive widthMil")
	}
	if err := r.Validate(); err != nil {
		return fail("fail", err.Error())
	}
	if in.Layer != 1 && in.Layer != 2 {
		return fail("incomplete", "route solver currently supports TOP/BOTTOM only")
	}
	if snap == nil || len(snap.Partial) > 0 || snap.Components == nil || snap.CopperLayers < 1 || in.Layer == 2 && snap.CopperLayers < 2 {
		return fail("incomplete", "route solver needs a complete measured snapshot and stackup")
	}
	g, status, err := preparePCBRouteGeometry(snap)
	if err != nil {
		return fail(status, err.Error())
	}
	if err := validateCrystalFenceContour(snap.Outline.Points); err != nil {
		return fail("incomplete", "invalid board outline: "+err.Error())
	}
	if len(g.arcs) != 0 {
		return fail("incomplete", "exact arc-track clearance is unsupported by route solve/check")
	}
	if in.WidthMil < snap.Rules.TrackWidthMinMil {
		return fail("fail", "requested widthMil is below the measured minimum")
	}
	a, aok := g.pads[in.From]
	z, zok := g.pads[in.To]
	if !aok || !zok || g.ambiguous[in.From] || g.ambiguous[in.To] {
		return fail("incomplete", "route endpoints require unique measured designator.pad identities")
	}
	if a.Net != in.Net || z.Net != in.Net {
		return fail("fail", "route endpoint net differs from the independent request")
	}
	if !padLayerMatches(a.Layer, in.Layer) || !padLayerMatches(z.Layer, in.Layer) {
		return fail("incomplete", "endpoint needs an unsupported layer transition")
	}
	r.From, r.To = [2]float64{a.X, a.Y}, [2]float64{z.X, z.Y}
	if err := r.Validate(); err != nil {
		return fail("fail", err.Error())
	}
	// These stricter capability checks are local to the new command. Existing
	// planners retain their own contracts. In particular, never turn arc chords,
	// thermal-spoke strokes or missing cutout geometry into free space.
	extras, err := pcbBoardCopperExtras(snap, pcbRouteExactContours, false)
	if err != nil {
		return fail("incomplete", err.Error())
	}
	slots, strokes := extras.slots, extras.strokes
	for _, raw := range snap.Copper.Regions {
		m := raw.(map[string]any)
		for _, rule := range m["ruleType"].([]any) {
			if asFloat(rule) == 5 {
				layer := int(asFloat(m["layer"]))
				if layer != pcbLayerMulti && !netPathCopperLayer(layer) {
					return fail("incomplete", "no-wires region layer is unsupported")
				}
				if _, err := pcbRouteExactContours(m["source"]); err != nil {
					return fail("incomplete", err.Error())
				}
			}
		}
	}
	clear, err := g.clear(pcbEscapeDemand{Net: in.Net, Layer: in.Layer, WidthMil: in.WidthMil}, nil)
	if err != nil {
		return fail("incomplete", err.Error())
	}
	return r, func(a, z [2]float64) bool {
		if !clear(a, z) {
			return false
		}
		for _, slot := range slots {
			if copperAreaSegmentDistance(slot, a, z) < in.WidthMil/2+pcbSlotClearanceMil(snap.Rules)-netPathGeomEps {
				return false
			}
		}
		for _, stroke := range strokes {
			if stroke.area.Net != in.Net && stroke.area.Layer == in.Layer && stroke.segmentDistance(a, z) < in.WidthMil/2+snap.Rules.ClearanceMil-netPathGeomEps {
				return false
			}
		}
		return true
	}, "", nil
}

// pcbStrokedArea is materialized copper whose boundary is drawn with a stroke:
// copper extends width/2 beyond the contour. An unfilled area is only its
// stroked outline (e.g. thermal spokes); its closing edge is kept, which can
// only over-estimate copper.
type pcbStrokedArea struct {
	area   pcbCopperArea
	width  float64
	filled bool
}

func (s pcbStrokedArea) segmentDistance(a, z [2]float64) float64 {
	if s.filled {
		return copperAreaSegmentDistance(s.area, a, z) - s.width/2
	}
	best := math.Inf(1)
	for _, contour := range s.area.Contours {
		for i, p := range contour {
			q := contour[(i+1)%len(contour)]
			best = math.Min(best, segSegDist(a[0], a[1], z[0], z[1], p[0], p[1], q[0], q[1]))
		}
	}
	return best - s.width/2
}

type pcbBoardExtras struct {
	slots   []pcbCopperArea // MULTI-layer cutouts: no copper on any layer
	strokes []pcbStrokedArea
}

// pcbSlotClearanceMil is copper-to-cutout spacing: never below the board-edge
// rule, the copper clearance or the 8 mil milling floor.
func pcbSlotClearanceMil(r *boardRules) float64 {
	return math.Max(8, math.Max(r.ClearanceMil, r.CopperToEdgeMil))
}

// pcbBoardCopperExtras reads the slot cutouts and stroke widths that
// parseCopperAreaObstacles drops. Unknown stroke width, fill semantics or
// cutout geometry is an error (incomplete), never free space. allowUnfilled
// admits poured outline strokes such as thermal spokes.
func pcbBoardCopperExtras(snap *boardSnapshot, contours func(any) ([][][2]float64, error), allowUnfilled bool) (pcbBoardExtras, error) {
	var out pcbBoardExtras
	for i, raw := range snap.Copper.Fills {
		m, ok := raw.(map[string]any)
		if !ok {
			return out, fmt.Errorf("static fills[%d] is not an object", i)
		}
		layer := int(asFloat(m["layer"]))
		if !netPathCopperLayer(layer) && layer != pcbLayerMulti {
			continue
		}
		if m["geometryAvailable"] != true {
			return out, fmt.Errorf("fill/cutout geometry is unknown")
		}
		stroke, ok := asFloatOK(m["lineWidth"])
		if !ok || !allFinite(stroke) || stroke < 0 || m["fillMode"] != "solid" {
			return out, fmt.Errorf("fill/cutout needs known solid fill mode and nonnegative stroke width")
		}
		cs, err := contours(m["source"])
		if err != nil {
			return out, err
		}
		id := asString(m["primitiveId"])
		if layer == pcbLayerMulti {
			if stroke != 0 {
				return out, fmt.Errorf("stroked cutout boundary is unsupported")
			}
			out.slots = append(out.slots, pcbCopperArea{ID: id, Kind: "slot", Layer: layer, Contours: cs})
		} else {
			out.strokes = append(out.strokes, pcbStrokedArea{area: pcbCopperArea{ID: id, Kind: "static fill", Net: asString(m["net"]), Layer: layer, Contours: cs}, width: stroke, filled: true})
		}
	}
	for i, raw := range snap.Copper.Poured {
		m, ok := raw.(map[string]any)
		if !ok {
			return out, fmt.Errorf("materialized poured[%d] is not an object", i)
		}
		fills, ok := m["fills"].([]any)
		if !ok {
			return out, fmt.Errorf("materialized poured %s fills are unknown", asString(m["primitiveId"]))
		}
		for j, rawFill := range fills {
			fill, ok := rawFill.(map[string]any)
			if !ok {
				return out, fmt.Errorf("materialized poured fill[%d] is not an object", j)
			}
			filled, known := fill["fill"].(bool)
			if !known || !filled && !allowUnfilled {
				return out, fmt.Errorf("poured fill/stroke semantics unknown or unsupported")
			}
			width, ok := asFloatOK(fill["lineWidth"])
			if !ok || !allFinite(width) || width < 0 {
				return out, fmt.Errorf("poured polygon stroke width unknown or unsupported")
			}
			cs, err := contours(fill["source"])
			if err != nil {
				return out, err
			}
			id := strings.TrimSpace(asString(fill["id"]))
			if id == "" {
				id = fmt.Sprintf("%s:%d", asString(m["primitiveId"]), j)
			}
			out.strokes = append(out.strokes, pcbStrokedArea{area: pcbCopperArea{ID: id, Kind: "poured copper", Net: asString(m["net"]), Layer: int(asFloat(m["layer"])), Contours: cs}, width: width, filled: filled})
		}
	}
	return out, nil
}

// No curved contours are approximated for this proof. The broader host parser
// can flatten ARC for other consumers; here only exact polygonal edges qualify.
func pcbRouteExactContours(raw any) ([][][2]float64, error) {
	var checkTokens func(any) error
	checkTokens = func(v any) error {
		switch x := v.(type) {
		case []any:
			for _, child := range x {
				if err := checkTokens(child); err != nil {
					return err
				}
			}
		case string:
			if x != "L" {
				return fmt.Errorf("route solver requires exact polygonal contours; %s is unsupported", x)
			}
		}
		return nil
	}
	if err := checkTokens(raw); err != nil {
		return nil, err
	}
	contours, err := polygonSourceContours(raw)
	if err != nil {
		return nil, err
	}
	for _, contour := range contours {
		if err := validateCrystalFenceContour(contour); err != nil {
			return nil, err
		}
	}
	return contours, nil
}

func solvePCBRoute(ctx context.Context, in pcbRouteRequest, snap *boardSnapshot, report *pcbRouteReport) error {
	r, clear, status, err := preparePCBRouteRequest(in, snap)
	if err != nil {
		report.Status = status
		report.Reasons = []string{err.Error()}
		return err
	}
	result, err := pcbrouting.Solve(ctx, r, clear)
	report.Search = &result
	if err != nil {
		report.Reasons = []string{err.Error()}
		return err
	}
	if result.Status != pcbrouting.Found {
		report.Reasons = []string{result.Reason}
		return fmt.Errorf("route search incomplete: %s", result.Reason)
	}
	report.Status = "pass"
	report.Candidate = &pcbRouteCandidate{Net: in.Net, Layer: in.Layer, WidthMil: in.WidthMil, Points: result.Points}
	report.LengthMil, report.Bends = result.Length, result.Bends
	return nil
}

func checkPCBRoute(ctx context.Context, in pcbRouteRequest, snap *boardSnapshot, plan pcbRouteReport, report *pcbRouteReport) error {
	fail := func(status, reason string) error {
		report.Status = status
		report.Reasons = []string{reason}
		return fmt.Errorf("%s", reason)
	}
	r, clear, status, err := preparePCBRouteRequest(in, snap)
	if err != nil {
		return fail(status, err.Error())
	}
	if plan.SchemaVersion != 1 || plan.BoardSHA256 != report.BoardSHA256 || plan.RequestSHA256 != report.RequestSHA256 {
		return fail("fail", "plan provenance differs from board/request inputs")
	}
	c := plan.Candidate
	if c != nil {
		report.Candidate = &pcbRouteCandidate{Net: c.Net, Layer: c.Layer, WidthMil: c.WidthMil, Points: append([][2]float64(nil), c.Points...)}
	}
	if plan.Status != "pass" || c == nil || c.Net != in.Net || c.Layer != in.Layer || c.WidthMil != in.WidthMil {
		return fail("fail", "plan candidate is missing or differs from requested net/layer/width")
	}
	if err := pcbrouting.Check(ctx, r, c.Points, clear); err != nil {
		return fail("fail", err.Error())
	}
	report.Status = "pass"
	report.LengthMil, report.Bends = pcbrouting.Metrics(c.Points)
	return nil
}
