package app

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// power-layout is intentionally an offline, narrowly scoped planner. Its input
// is measured geometry with authoritative (or explicitly preserved desired)
// pin nets. It never infers connectivity from old wire positions.
type powerLayoutOptions struct {
	Core, InputCap   string
	OutputCaps       []string
	Doc              string
	At               *[2]float64
	PreservePosition bool
}

type powerLayoutPin struct {
	Number string  `json:"number"`
	Name   string  `json:"name"`
	Net    string  `json:"net"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
}

type powerLayoutPlacement struct {
	PrimitiveID string           `json:"primitiveId"`
	Designator  string           `json:"designator"`
	Value       string           `json:"value,omitempty"`
	X           float64          `json:"x"`
	Y           float64          `json:"y"`
	Rotation    float64          `json:"rotation"`
	Mirror      bool             `json:"mirror"`
	BBox        layoutBBox       `json:"bbox"`
	TextBBoxes  []layoutBBox     `json:"textBboxes,omitempty"`
	Pins        []powerLayoutPin `json:"pins"`
}

type powerLayoutWire struct {
	Net    string       `json:"net"`
	Points [][2]float64 `json:"points"`
}

type powerLayoutFlag struct {
	Net       string  `json:"net"`
	Kind      string  `json:"kind"`
	PinX      float64 `json:"pinX"`
	PinY      float64 `json:"pinY"`
	Direction string  `json:"direction"`
	Offset    float64 `json:"offset"`
}

type powerLayoutPlan struct {
	SchemaVersion   int                    `json:"schemaVersion"`
	DocumentID      string                 `json:"documentId"`
	Placements      []powerLayoutPlacement `json:"placements"`
	Wires           []powerLayoutWire      `json:"wires"`
	Flags           []powerLayoutFlag      `json:"flags"`
	ExpectedPinNets map[string]string      `json:"expectedPinNets"`
	Frames          []schFrameSpec         `json:"frames"`
}

type powerLayoutSnapshot struct {
	Components []struct {
		PrimitiveID   string         `json:"primitiveId"`
		Designator    string         `json:"designator"`
		Value         string         `json:"value"`
		OtherProperty map[string]any `json:"otherProperty"`
		ComponentType string         `json:"componentType"`
		X             *float64       `json:"x"`
		Y             *float64       `json:"y"`
		Rotation      *float64       `json:"rotation"`
		Mirror        *bool          `json:"mirror"`
		BBox          *layoutBBox    `json:"bbox"`
		PinsAvailable *bool          `json:"pinsAvailable"`
		NetAmbiguous  bool           `json:"netAmbiguous"`
		Pins          []struct {
			Number string   `json:"pinNumber"`
			Name   string   `json:"pinName"`
			Net    *string  `json:"net"`
			X      *float64 `json:"x"`
			Y      *float64 `json:"y"`
			NC     bool     `json:"noConnected"`
		} `json:"pins"`
	} `json:"components"`
}

func newSchPowerLayoutCmd(stdout, stderr io.Writer) *cobra.Command {
	var from, out, core, inputCap, outputCaps, doc, at, playbookOut string
	var framesOnly bool
	c := &cobra.Command{
		Use:   "power-layout",
		Short: "Plan a four-part fixed AMS1117 power circuit offline from measured geometry",
		Long: `Compute component XY/rotation, direct VIN/VOUT wires, local power/ground
symbols and expected pin geometry from a saved components.list snapshot.
Input requires real bbox/pins/rotation/mirror and authoritative or explicitly
preserved desired pin nets. Supports exactly one AMS1117 and three capacitors.
The calibrated core must have VIN on the left, pin 4 on the right, and GND
below pin 2/VIN. No editor calls or mutations are performed. --out writes the
layout plan; --playbook additionally writes the ordered sch apply operations
with fresh geometry guards and post-apply pin/net verification. Each plan also
contains a dashed pink module frame and a 0.2-inch title fitted into an upper or
lower gap. Optional top-level titleMetrics:{title,fontSize,width,height} supplies
a matching native text measurement; otherwise width is estimated and verified
after Apply. --frames-only compiles
only frame/title conversion and verification; all planned pins and parts must
already be in their target positions and on their expected nets.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if from == "" {
				return fmt.Errorf("--from is required")
			}
			if framesOnly && playbookOut == "" {
				return fmt.Errorf("--frames-only requires --playbook")
			}
			raw, err := os.ReadFile(from)
			if err != nil {
				return err
			}
			o := powerLayoutOptions{Core: core, InputCap: inputCap, OutputCaps: strings.Split(outputCaps, ","), Doc: doc, PreservePosition: framesOnly}
			if at != "" {
				v := strings.Split(at, ",")
				if len(v) != 2 {
					return fmt.Errorf("--at must be x,y")
				}
				var xy [2]float64
				for i := range xy {
					xy[i], err = strconv.ParseFloat(strings.TrimSpace(v[i]), 64)
					if err != nil {
						return fmt.Errorf("--at: %w", err)
					}
				}
				o.At = &xy
			}
			plan, err := planPowerLayout(raw, o)
			if err != nil {
				return err
			}
			if playbookOut != "" {
				var pb *playbook
				if framesOnly {
					pb, err = powerLayoutFramePlaybook(plan, raw)
				} else {
					pb, err = powerLayoutPlaybook(plan, raw)
				}
				if err != nil {
					return err
				}
				pbData, err := json.MarshalIndent(pb, "", "  ")
				if err != nil {
					return err
				}
				if err = os.WriteFile(playbookOut, append(pbData, '\n'), 0644); err != nil {
					return err
				}
				fmt.Fprintf(stderr, "power-layout: wrote apply playbook %s\n", playbookOut)
			}
			data, err := json.MarshalIndent(plan, "", "  ")
			if err != nil {
				return err
			}
			data = append(data, '\n')
			if out != "" {
				if err = os.WriteFile(out, data, 0644); err != nil {
					return err
				}
				fmt.Fprintf(stderr, "power-layout: wrote %s; %d placements, %d direct wires, %d local symbols\n", out, len(plan.Placements), len(plan.Wires), len(plan.Flags))
				return nil
			}
			_, err = stdout.Write(data)
			return err
		},
	}
	c.Flags().StringVar(&from, "from", "", "measured components.list JSON with bbox, pins and pin nets")
	c.Flags().StringVar(&out, "out", "", "write offline layout plan JSON (default stdout)")
	c.Flags().StringVar(&playbookOut, "playbook", "", "also write a guarded ordered sch apply playbook")
	c.Flags().BoolVar(&framesOnly, "frames-only", false, "with --playbook: verify the existing circuit and apply only frame/title data")
	c.Flags().StringVar(&core, "core", "U1", "fixed AMS1117 designator")
	c.Flags().StringVar(&inputCap, "input-cap", "C2", "input capacitor designator")
	c.Flags().StringVar(&outputCaps, "output-caps", "C1,C3", "two output capacitor designators, nearest first")
	c.Flags().StringVar(&doc, "doc", "", "document UUID (must match snapshot context when present)")
	c.Flags().StringVar(&at, "at", "", "target core anchor x,y on the 5-unit grid; default module starts at the sheet's top-left (frames-only preserves placement)")
	return c
}

func plFinite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func plGrid(v float64) bool {
	return plFinite(v) && math.Abs(v/schAnchorGrid-math.Round(v/schAnchorGrid)) < 1e-7
}
func plCeil(v float64) float64  { return math.Ceil((v-1e-8)/schAnchorGrid) * schAnchorGrid }
func plFloor(v float64) float64 { return math.Floor((v+1e-8)/schAnchorGrid) * schAnchorGrid }
func plBoxValid(b layoutBBox) bool {
	return plFinite(b.MinX) && plFinite(b.MinY) && plFinite(b.MaxX) && plFinite(b.MaxY) && b.MinX < b.MaxX && b.MinY < b.MaxY
}
func plPin(c powerLayoutPlacement, number string) powerLayoutPin {
	for _, p := range c.Pins {
		if p.Number == number {
			return p
		}
	}
	return powerLayoutPin{}
}

func planPowerLayout(raw []byte, o powerLayoutOptions) (*powerLayoutPlan, error) {
	var env struct {
		Result  json.RawMessage `json:"result"`
		Context struct {
			Doc string `json:"documentUuid"`
		} `json:"context"`
		DocumentID   string           `json:"documentId"`
		TitleMetrics *schTitleMetrics `json:"titleMetrics"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	doc := env.Context.Doc
	if doc == "" {
		doc = env.DocumentID
	}
	if o.Doc != "" {
		if doc != "" && doc != o.Doc {
			return nil, fmt.Errorf("document mismatch: snapshot %s, requested %s", doc, o.Doc)
		}
		doc = o.Doc
	}
	if doc == "" {
		return nil, fmt.Errorf("snapshot document identity missing; supply --doc")
	}
	if len(env.Result) > 0 {
		raw = env.Result
	}
	var src powerLayoutSnapshot
	if err := json.Unmarshal(raw, &src); err != nil {
		return nil, err
	}
	if len(o.OutputCaps) != 2 {
		return nil, fmt.Errorf("exactly two output capacitors are supported")
	}
	refs := []string{o.Core, o.InputCap, strings.TrimSpace(o.OutputCaps[0]), strings.TrimSpace(o.OutputCaps[1])}
	wanted := map[string]bool{}
	for _, r := range refs {
		if r == "" || wanted[r] {
			return nil, fmt.Errorf("four distinct part designators are required")
		}
		wanted[r] = true
	}
	parts := map[string]powerLayoutPlacement{}
	ids := map[string]bool{}
	var sheet *layoutBBox
	for _, c := range src.Components {
		if c.ComponentType == "sheet" {
			if c.BBox == nil || !plBoxValid(*c.BBox) {
				return nil, fmt.Errorf("sheet bbox is missing or invalid")
			}
			if sheet != nil {
				return nil, fmt.Errorf("multiple sheets are unsupported")
			}
			b := *c.BBox
			sheet = &b
			continue
		}
		if c.ComponentType != "part" {
			switch c.ComponentType {
			case "netflag", "netport", "netlabel", "shortSymbol":
				continue
			default:
				return nil, fmt.Errorf("unsupported component type %q; require a four-part page", c.ComponentType)
			}
		}
		if !wanted[c.Designator] || parts[c.Designator].PrimitiveID != "" {
			return nil, fmt.Errorf("page must contain exactly the selected four parts; unexpected or duplicate %q", c.Designator)
		}
		if c.PrimitiveID == "" || ids[c.PrimitiveID] {
			return nil, fmt.Errorf("missing or duplicate primitiveId for %s", c.Designator)
		}
		ids[c.PrimitiveID] = true
		if c.X == nil || c.Y == nil || c.Rotation == nil || c.Mirror == nil || c.BBox == nil || !plBoxValid(*c.BBox) || c.PinsAvailable == nil || !*c.PinsAvailable || c.NetAmbiguous {
			return nil, fmt.Errorf("%s: incomplete or ambiguous measured bbox/pin/pose data", c.Designator)
		}
		if !plGrid(*c.X) || !plGrid(*c.Y) || !plFinite(*c.Rotation) || math.Abs(*c.Rotation/90-math.Round(*c.Rotation/90)) > 1e-7 {
			return nil, fmt.Errorf("%s: off-grid or invalid pose", c.Designator)
		}
		// API trigonometry can return e.g. 204.99999999999997 for a grid
		// coordinate. Accept only a tiny tolerance, then canonicalize before
		// exact orthogonality/segment checks; do not silently snap real errors.
		value := c.Value
		if value == "" {
			value, _ = c.OtherProperty["Value"].(string)
		}
		p := powerLayoutPlacement{PrimitiveID: c.PrimitiveID, Designator: c.Designator, Value: value, X: snapAnchor(*c.X), Y: snapAnchor(*c.Y), Rotation: math.Round(*c.Rotation/90) * 90, Mirror: *c.Mirror, BBox: *c.BBox}
		numbers := map[string]bool{}
		for _, pin := range c.Pins {
			if pin.Number == "" || numbers[pin.Number] || pin.X == nil || pin.Y == nil || !plGrid(*pin.X) || !plGrid(*pin.Y) || pin.Net == nil || *pin.Net == "" || pin.NC {
				return nil, fmt.Errorf("%s: missing/off-grid/duplicate/unconnected pin data", c.Designator)
			}
			numbers[pin.Number] = true
			p.Pins = append(p.Pins, powerLayoutPin{Number: pin.Number, Name: pin.Name, Net: *pin.Net, X: snapAnchor(*pin.X), Y: snapAnchor(*pin.Y)})
		}
		sort.Slice(p.Pins, func(i, j int) bool { return p.Pins[i].Number < p.Pins[j].Number })
		parts[c.Designator] = p
	}
	if len(parts) != 4 || sheet == nil {
		return nil, fmt.Errorf("require exactly four parts and one measured sheet")
	}
	core := parts[o.Core]
	if len(core.Pins) != 4 {
		return nil, fmt.Errorf("fixed AMS1117 core must have four pins")
	}
	for n, name := range map[string]string{"1": "GND", "2": "VOUT", "3": "VIN", "4": "VOUT"} {
		p := plPin(core, n)
		if strings.ToUpper(p.Name) != name {
			return nil, fmt.Errorf("core pin %s must be %s (measured %q)", n, name, p.Name)
		}
	}
	gnd, outNet, inNet := plPin(core, "1").Net, plPin(core, "4").Net, plPin(core, "3").Net
	if gnd == outNet || gnd == inNet || outNet == inNet || plPin(core, "2").Net != outNet {
		return nil, fmt.Errorf("core topology mismatch: VIN, VOUT and GND must be distinct, pins 2/4 must share VOUT")
	}
	if !strings.Contains(strings.ToUpper(gnd), "GND") {
		return nil, fmt.Errorf("GND pin must use a ground net")
	}
	for _, r := range refs[1:] {
		c := parts[r]
		n := outNet
		if r == o.InputCap {
			n = inNet
		}
		if len(c.Pins) != 2 || plPin(c, "1").Net != n || plPin(c, "2").Net != gnd {
			return nil, fmt.Errorf("%s topology mismatch: pin1 must be %s and pin2 %s", r, n, gnd)
		}
	}
	if o.At != nil {
		if !plGrid(o.At[0]) || !plGrid(o.At[1]) {
			return nil, fmt.Errorf("target anchor must be finite and on 5-unit grid")
		}
		core = plTranslate(core, o.At[0]-core.X, o.At[1]-core.Y)
	}
	p1, p2, p3, p4 := plPin(core, "1"), plPin(core, "2"), plPin(core, "3"), plPin(core, "4")
	if !(p3.X < core.BBox.MinX && p4.X > core.BBox.MaxX && p1.X == p2.X && p2.X == p3.X && p1.Y < p2.Y && p2.Y < p3.Y) {
		return nil, fmt.Errorf("unsupported core pin orientation: calibrate VIN-left/VOUT4-right with GND below VOUT2/VIN before planning")
	}
	plan := &powerLayoutPlan{SchemaVersion: 1, DocumentID: doc, Placements: []powerLayoutPlacement{core}, ExpectedPinNets: map[string]string{}}
	input, err := plVertical(parts[o.InputCap])
	if err != nil {
		return nil, err
	}
	// A component bbox excludes its designator/value. Reserve that right-hand
	// text column before the duplicate VOUT marker, including the same 20-unit
	// cluster clearance used by the live gate. The marker name is centered on
	// its stub; reserve its half-width on the capacitor-facing side.
	duplicateReach := 4*schAnchorGrid + math.Max(markerBBoxProfile("power", outNet).Far, plPowerTextWidth(outNet)/2)
	inputGap := plCeil(math.Max(2*schStubLen, plPowerCapRightReach(input)+bslPartGap+duplicateReach))
	ip := plPin(input, "1")
	input = plTranslate(input, plFloor(p3.X-inputGap)-ip.X, p3.Y-ip.Y)
	plan.Placements = append(plan.Placements, input)
	plan.Wires = append(plan.Wires, powerLayoutWire{Net: inNet, Points: [][2]float64{{plPin(input, "1").X, p3.Y}, {p3.X, p3.Y}}})
	previous := p4
	var previousCap *powerLayoutPlacement
	for _, r := range refs[2:] {
		c, err := plVertical(parts[r])
		if err != nil {
			return nil, err
		}
		pin := plPin(c, "1")
		gap := plCeil(math.Max(2*schStubLen, c.BBox.MaxX-c.BBox.MinX+4*schAnchorGrid))
		if previousCap != nil {
			// Consecutive output capacitors share a rail, but the left one's
			// two annotation lines must end before the right one's symbol.
			leftReach := pin.X - c.BBox.MinX
			gap = math.Max(gap, plCeil(plPowerCapRightReach(*previousCap)+bslPartGap+leftReach))
		}
		c = plTranslate(c, previous.X+gap-pin.X, p4.Y-pin.Y)
		next := plPin(c, "1")
		plan.Placements = append(plan.Placements, c)
		plan.Wires = append(plan.Wires, powerLayoutWire{Net: outNet, Points: [][2]float64{{previous.X, p4.Y}, {next.X, p4.Y}}})
		previous = next
		placedCap := c
		previousCap = &placedCap
	}
	plan.Flags = append(plan.Flags, powerLayoutFlag{inNet, "power", plPin(input, "1").X, p3.Y, "up", schStubLen}, powerLayoutFlag{outNet, "power", previous.X, previous.Y, "up", schStubLen}, powerLayoutFlag{outNet, "power", p2.X, p2.Y, "left", 4 * schAnchorGrid})
	// Ground pin exits left before turning down; the calibrated ordering puts
	// the turn below both left-side power pins and avoids every crossing.
	groundX := p1.X - 4*schAnchorGrid
	plan.Wires = append(plan.Wires, powerLayoutWire{Net: gnd, Points: [][2]float64{{p1.X, p1.Y}, {groundX, p1.Y}}})
	plan.Flags = append(plan.Flags, powerLayoutFlag{gnd, "ground", groundX, p1.Y, "down", schStubLen})
	for _, c := range plan.Placements[1:] {
		pin := plPin(c, "2")
		plan.Flags = append(plan.Flags, powerLayoutFlag{gnd, "ground", pin.X, pin.Y, "down", schStubLen})
	}
	for _, c := range plan.Placements {
		for _, p := range c.Pins {
			plan.ExpectedPinNets[c.Designator+"."+p.Number] = p.Net
		}
	}
	var frameSheet *layoutBBox
	if o.At != nil || o.PreservePosition {
		frameSheet = sheet
	}
	frame, err := measureSchModuleFrameObstacles("POWER", "POWER / AMS1117-3.3", powerLayoutContentObstacles(plan), env.TitleMetrics, frameSheet)
	if err != nil {
		return nil, err
	}
	plan.Frames = []schFrameSpec{frame}
	if o.At == nil && !o.PreservePosition {
		packed, err := planSchModuleRows(plan.Frames, *sheet, schModulePageMargin, schModuleGap)
		if err != nil {
			return nil, err
		}
		translatePowerLayout(plan, packed[0].DX, packed[0].DY)
		plan.Frames[0] = packed[0].Frame
	}
	if err := validatePowerLayout(plan, *sheet); err != nil {
		return nil, err
	}
	if !boxInside(plan.Frames[0].Rect, *sheet) {
		return nil, fmt.Errorf("module frame/title outside sheet; revise the input layout (no automatic pagination)")
	}
	return plan, nil
}

// Reuse the project's calibrated 6 units/character + 8 units text padding,
// excluding the netport body. Pin-to-body distance is measured independently.
func plPowerTextWidth(text string) float64 { return acPortTotalLen(text) - acPortBodyLen }
func plPowerCapRightReach(c powerLayoutPlacement) float64 {
	textWidth := math.Max(plPowerTextWidth(c.Designator), plPowerTextWidth(c.Value))
	return c.BBox.MaxX - plPin(c, "1").X + 2*schAnchorGrid + textWidth
}

func plTranslate(c powerLayoutPlacement, dx, dy float64) powerLayoutPlacement {
	c.TextBBoxes = append([]layoutBBox(nil), c.TextBBoxes...)
	for i, b := range c.TextBBoxes {
		c.TextBBoxes[i] = layoutBBox{b.MinX + dx, b.MinY + dy, b.MaxX + dx, b.MaxY + dy}
	}
	c.X += dx
	c.Y += dy
	c.BBox.MinX += dx
	c.BBox.MaxX += dx
	c.BBox.MinY += dy
	c.BBox.MaxY += dy
	c.Pins = append([]powerLayoutPin(nil), c.Pins...)
	for i := range c.Pins {
		c.Pins[i].X += dx
		c.Pins[i].Y += dy
	}
	return c
}

func plRotate(c powerLayoutPlacement, quarters int) powerLayoutPlacement {
	rotate := func(x, y float64) (float64, float64) {
		x -= c.X
		y -= c.Y
		for i := 0; i < quarters; i++ {
			x, y = -y, x
		}
		return x + c.X, y + c.Y
	}
	c.Pins = append([]powerLayoutPin(nil), c.Pins...)
	for i := range c.Pins {
		c.Pins[i].X, c.Pins[i].Y = rotate(c.Pins[i].X, c.Pins[i].Y)
	}
	b := layoutBBox{MinX: math.Inf(1), MinY: math.Inf(1), MaxX: math.Inf(-1), MaxY: math.Inf(-1)}
	for _, v := range [][2]float64{{c.BBox.MinX, c.BBox.MinY}, {c.BBox.MinX, c.BBox.MaxY}, {c.BBox.MaxX, c.BBox.MinY}, {c.BBox.MaxX, c.BBox.MaxY}} {
		x, y := rotate(v[0], v[1])
		b.MinX = math.Min(b.MinX, x)
		b.MaxX = math.Max(b.MaxX, x)
		b.MinY = math.Min(b.MinY, y)
		b.MaxY = math.Max(b.MaxY, y)
	}
	c.BBox = b
	c.TextBBoxes = append([]layoutBBox(nil), c.TextBBoxes...)
	for i, box := range c.TextBBoxes {
		x1, y1 := rotate(box.MinX, box.MinY)
		x2, y2 := rotate(box.MaxX, box.MaxY)
		c.TextBBoxes[i] = layoutBBox{math.Min(x1, x2), math.Min(y1, y2), math.Max(x1, x2), math.Max(y1, y2)}
	}
	c.Rotation = math.Mod(c.Rotation+float64(quarters)*90, 360)
	return c
}

func plVertical(c powerLayoutPlacement) (powerLayoutPlacement, error) {
	for q := 0; q < 4; q++ {
		v := plRotate(c, q)
		a, b := plPin(v, "1"), plPin(v, "2")
		if math.Abs(a.X-b.X) < 1e-7 && a.Y > b.Y {
			return v, nil
		}
	}
	return c, fmt.Errorf("%s: capacitor pins cannot form a vertical shunt", c.Designator)
}

func validatePowerLayout(plan *powerLayoutPlan, sheet layoutBBox) error {
	if err := validatePlacementText(plan, sheet); err != nil {
		return err
	}
	for i, c := range plan.Placements {
		if !plGrid(c.X) || !plGrid(c.Y) || !boxInside(c.BBox, sheet) {
			return fmt.Errorf("%s: planned component outside sheet or off-grid", c.Designator)
		}
		for _, other := range plan.Placements[:i] {
			if boxesGapOverlap(c.BBox, other.BBox, schAnchorGrid) {
				return fmt.Errorf("component body collision: %s/%s", c.Designator, other.Designator)
			}
		}
	}
	segments := append([]powerLayoutWire(nil), plan.Wires...)
	for _, f := range plan.Flags {
		end := [2]float64{f.PinX, f.PinY}
		switch f.Direction {
		case "up":
			end[1] += f.Offset
		case "down":
			end[1] -= f.Offset
		case "left":
			end[0] -= f.Offset
		case "right":
			end[0] += f.Offset
		default:
			return fmt.Errorf("unknown flag direction")
		}
		segments = append(segments, powerLayoutWire{Net: f.Net, Points: [][2]float64{{f.PinX, f.PinY}, end}})
	}
	for i, s := range segments {
		if len(s.Points) != 2 {
			return fmt.Errorf("planner wires must be single segments")
		}
		a, b := s.Points[0], s.Points[1]
		if a == b || (a[0] != b[0] && a[1] != b[1]) {
			return fmt.Errorf("zero-length or nonorthogonal wire")
		}
		for _, p := range s.Points {
			if !plGrid(p[0]) || !plGrid(p[1]) || p[0] < sheet.MinX || p[0] > sheet.MaxX || p[1] < sheet.MinY || p[1] > sheet.MaxY {
				return fmt.Errorf("wire outside sheet or off-grid")
			}
		}
		for _, c := range plan.Placements {
			if plSegmentBox(a, b, c.BBox) {
				return fmt.Errorf("%s wire passes through %s body", s.Net, c.Designator)
			}
			for _, p := range c.Pins {
				if p.Net != s.Net && plOnSegment([2]float64{p.X, p.Y}, a, b) {
					return fmt.Errorf("%s wire crosses foreign pin %s.%s", s.Net, c.Designator, p.Number)
				}
			}
		}
		for _, other := range segments[:i] {
			if other.Net != s.Net && plSegmentsMeet(a, b, other.Points[0], other.Points[1]) {
				return fmt.Errorf("wire crossing joins %s and %s", s.Net, other.Net)
			}
		}
	}
	return nil
}

func plOnSegment(p, a, b [2]float64) bool {
	return math.Abs((p[0]-a[0])*(b[1]-a[1])-(p[1]-a[1])*(b[0]-a[0])) < 1e-7 && p[0] >= math.Min(a[0], b[0])-1e-7 && p[0] <= math.Max(a[0], b[0])+1e-7 && p[1] >= math.Min(a[1], b[1])-1e-7 && p[1] <= math.Max(a[1], b[1])+1e-7
}
func plSegmentsMeet(a, b, c, d [2]float64) bool {
	if plOnSegment(a, c, d) || plOnSegment(b, c, d) || plOnSegment(c, a, b) || plOnSegment(d, a, b) {
		return true
	}
	if a[0] == b[0] && c[1] == d[1] {
		return plOnSegment([2]float64{a[0], c[1]}, a, b) && plOnSegment([2]float64{a[0], c[1]}, c, d)
	}
	if a[1] == b[1] && c[0] == d[0] {
		return plOnSegment([2]float64{c[0], a[1]}, a, b) && plOnSegment([2]float64{c[0], a[1]}, c, d)
	}
	return false
}
func plSegmentBox(a, b [2]float64, r layoutBBox) bool {
	if a[0] == b[0] {
		return a[0] > r.MinX && a[0] < r.MaxX && math.Max(a[1], b[1]) > r.MinY && math.Min(a[1], b[1]) < r.MaxY
	}
	return a[1] > r.MinY && a[1] < r.MaxY && math.Max(a[0], b[0]) > r.MinX && math.Min(a[0], b[0]) < r.MaxX
}
