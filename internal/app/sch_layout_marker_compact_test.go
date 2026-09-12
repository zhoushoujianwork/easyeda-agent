package app

import (
	"bytes"
	"math"
	"strings"
	"testing"
)

func TestMarkerLengthBudgetUsesOccupiedSize(t *testing.T) {
	p := powerLayoutPlan{}
	short := libMarkerOffsetCap(&p, "N", "net_port_bi")
	long := libMarkerOffsetCap(&p, "LONG_SIGNAL_NAME", "net_port_bi")
	if short >= long || short != plCeil(10+2*acPortTotalLen("N")) || long > 300 {
		t.Fatal(short, long)
	}
	p.Flags = []powerLayoutFlag{{Net: "LONG_SIGNAL_NAME", Kind: "net_port_bi"}}
	if libMarkerOffsetCap(&p, "N", "net_port_bi") != long {
		t.Fatal("ignored existing longer port")
	}
	if libMarkerOffsetCap(&p, strings.Repeat("N", 100), "net_port_bi") != 300 {
		t.Fatal("unbounded search")
	}
}

func TestDenseSignalPortStaggerCase(t *testing.T) {
	// Synthetic interface: no vendor symbol or training-exam data.
	for _, names := range [][]string{{"DATA_A", "DATA_B", "EN"}, {"A", "B", "C"}, {"LONG_CHANNEL_A", "LONG_CHANNEL_B", "EN"}} {
		c := powerLayoutPlacement{Designator: "J1", BBox: layoutBBox{-10, -25, 10, 25}}
		for i, n := range names {
			c.Pins = append(c.Pins, powerLayoutPin{Number: string(rune('1' + i)), Net: n, X: 30, Y: float64(i*10 - 10)})
		}
		p := powerLayoutPlan{Placements: []powerLayoutPlacement{c}}
		policies := map[string]string{}
		for _, n := range names {
			policies[n] = "module_port"
		}
		if e := libNameIslands(&p, policies); e != nil {
			t.Fatal(e)
		}
		if len(p.Flags) != 3 {
			t.Fatal("lost port")
		}
		for _, f := range p.Flags {
			if f.Offset > libMarkerOffsetCap(&p, f.Net, f.Kind) {
				t.Fatal("excessive lead", f)
			}
		}
		if e := validateLibGeometry(&p); e != nil {
			t.Fatal(e)
		}
		if e := validateSchCompositionNets(&p); e != nil {
			t.Fatal(e)
		}
		// Prove no shorter lead in the selected direction fits the remaining ports.
		for i, f := range p.Flags {
			rest := p
			rest.Flags = append(append([]powerLayoutFlag{}, p.Flags[:i]...), p.Flags[i+1:]...)
			segments, e := schTerminalSegments(&rest)
			if e != nil {
				t.Fatal(e)
			}
			for offset := 10.; offset < f.Offset; offset += 5 {
				candidate := f
				candidate.Offset = offset
				if schTerminalCandidate(&rest, candidate, segments) == nil {
					t.Fatalf("needlessly long port: %+v admits %g", f, offset)
				}
			}
		}
	}
}

func TestMarkerLeadRetraceAndTJunctionCases(t *testing.T) {
	wires := []powerLayoutWire{{Net: "N", Points: [][2]float64{{0, 0}, {40, 0}}}}
	for _, tc := range []struct {
		f       powerLayoutFlag
		retrace bool
	}{
		{powerLayoutFlag{Net: "N", PinX: 20, Direction: "left", Offset: 10}, true},
		{powerLayoutFlag{Net: "N", PinX: 40, Direction: "left", Offset: 10}, true},
		{powerLayoutFlag{Net: "N", PinX: 40, Direction: "right", Offset: 10}, false},
		{powerLayoutFlag{Net: "N", PinX: 20, Direction: "up", Offset: 10}, false},
	} {
		if libMarkerRetraces(tc.f, wires) != tc.retrace {
			t.Fatal(tc)
		}
	}
	layout := SchematicLayoutResult{Wires: wires, Flags: []powerLayoutFlag{{Net: "N", PinX: 20, Direction: "up", Offset: 10}}}
	points := layoutJunctions(&layout)
	if len(points) != 1 || points[0] != [2]float64{20, 0} {
		t.Fatal("missing T dot", points)
	}
	layout.Flags[0].Net = "OTHER"
	if len(layoutJunctions(&layout)) != 0 {
		t.Fatal("foreign crossing got connection dot")
	}
	layout.Flags = nil
	layout.Wires = append(layout.Wires, wires[0])
	if len(layoutJunctions(&layout)) != 0 {
		t.Fatal("duplicate wire invented T dot")
	}
}

func TestDirectionalMarkerEnvelopeAndRender(t *testing.T) {
	for _, d := range []string{"right", "left", "up", "down"} {
		f := powerLayoutFlag{Net: "CHANNEL", Kind: "net_port_bi", Direction: d, Offset: 10}
		p := powerLayoutPlan{Flags: []powerLayoutFlag{f}}
		b := powerLayoutContentBounds(&p)
		x, y := endpointFor(0, 0, 10, d)
		actual := predictedMarkerBBox(x, y, f.Kind, d, f.Net)
		if !boxInside(actual, b) {
			t.Fatal("clipped port")
		}
		// The vacant opposite side retains only the half-width of the real lead.
		switch d {
		case "right":
			if b.MinX != -.5 {
				t.Fatal(b)
			}
		case "left":
			if b.MaxX != .5 {
				t.Fatal(b)
			}
		case "up":
			if b.MinY != -.5 {
				t.Fatal(b)
			}
		case "down":
			if b.MaxY != .5 {
				t.Fatal(b)
			}
		}
		span := math.Max(b.MaxX-b.MinX, b.MaxY-b.MinY)
		if span > acPortTotalLen(f.Net)+21 {
			t.Fatal("symmetric phantom reserve", b)
		}
		var svg bytes.Buffer
		renderLayoutMarker(&svg, f, func(v float64) float64 { return v }, func(v float64) float64 { return -v })
		if !strings.Contains(svg.String(), `class="net-port"`) || !strings.Contains(svg.String(), `class="marker-name"`) {
			t.Fatal("marker geometry hidden")
		}
	}
}

func TestMarkerEnvelopeRefinementPreservesTreeAndBudget(t *testing.T) {
	p := powerLayoutPlan{
		Placements: []powerLayoutPlacement{{Designator: "J1", BBox: layoutBBox{-10, -10, 10, 10}, Pins: []powerLayoutPin{{Number: "1", Net: "N", X: -20, Y: 5}, {Number: "2", Net: "N", X: 20, Y: 5}}}},
		Wires:      []powerLayoutWire{{Net: "N", Points: [][2]float64{{-20, 5}, {-20, 40}}}, {Net: "N", Points: [][2]float64{{-20, 40}, {20, 40}}}, {Net: "N", Points: [][2]float64{{20, 40}, {20, 5}}}},
		Flags:      []powerLayoutFlag{{Net: "N", Kind: "net_port_bi", PinX: -20, PinY: 5, Direction: "left", Offset: 100}},
	}
	if e := validateLibGeometry(&p); e != nil {
		t.Fatal(e)
	}
	before := libCandidateScore(&p)
	budget := 2048
	libCompactMarkerEnvelope(&p, &budget)
	after := libCandidateScore(&p)
	if after[3] > before[3]*.95 || after[0] > before[0]*1.2 {
		t.Fatal("no bounded improvement", before, after)
	}
	if budget < 0 || budget >= 2048 {
		t.Fatal("budget not counted", budget)
	}
	if len(p.Wires) != 3 || len(p.Placements) != 1 || len(p.Flags) != 1 {
		t.Fatal("changed circuit")
	}
	if e := validateSchCompositionNets(&p); e != nil {
		t.Fatal(e)
	}
	if e := validateLibGeometry(&p); e != nil {
		t.Fatal(e)
	}
	f := p.Flags[0]
	budget = 0
	libCompactMarkerEnvelope(&p, &budget)
	if p.Flags[0] != f || budget != 0 {
		t.Fatal("exhaustion changed incumbent")
	}
}
