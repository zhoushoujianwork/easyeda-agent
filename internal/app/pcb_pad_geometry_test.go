package app

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadSimHolderComponents(t *testing.T) []boardComp {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "pcb_dump_geometry", "sim-holder-components.json"))
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	comps := parseBoardComponents(result)
	if len(comps) != 2 {
		t.Fatalf("components = %d", len(comps))
	}
	return comps
}

func simPad(t *testing.T, comps []boardComp, ref, number string) boardPad {
	t.Helper()
	for _, c := range comps {
		for _, p := range c.Pads {
			if c.Designator == ref && p.Number == number {
				return p
			}
		}
	}
	t.Fatalf("pad %s.%s missing", ref, number)
	return boardPad{}
}

// #309: every routing-relevant pad fact carries units and an explicit status.
func TestDumpPadGeometryFromSanitizedSimHolder(t *testing.T) {
	comps := loadSimHolderComponents(t)

	peg := simPad(t, comps, "J2", "PEG1").HoleGeometry
	if peg == nil || peg.Status != "available" || peg.Kind != "SLOT" || peg.Units != "mil" ||
		peg.DiameterMil != 10 || peg.LengthMil != 30 || peg.RotationDeg != 90 || peg.Plated == nil || *peg.Plated {
		t.Fatalf("NPTH slot geometry = %+v", peg)
	}
	if offset := simPad(t, comps, "J2", "PEG2").HoleGeometry; offset == nil || offset.Status != "unknown" || !strings.Contains(offset.Reason, "holeOffsetX is nonzero") {
		t.Fatalf("nonzero offset must stay unknown: %+v", offset)
	}
	if legacy := simPad(t, comps, "J3", "PEG1").HoleGeometry; legacy == nil || legacy.Status != "unknown" || !strings.Contains(legacy.Reason, "not captured") {
		t.Fatalf("missing offset capture must not be read as 0: %+v", legacy)
	}

	poly := simPad(t, comps, "J2", "1")
	if poly.ShapeBBox == nil || *poly.ShapeBBox != (layoutBBox{MinX: 172, MinY: 114, MaxX: 192, MaxY: 124}) {
		t.Fatalf("polygon bbox = %+v", poly.ShapeBBox)
	}
	// Centered on the pad origin (180,120): farthest |dx| = 12, |dy| = 6.
	if poly.W != 24 || poly.H != 12 || poly.ExtentSource != "polygon-centered-envelope" {
		t.Fatalf("polygon extent = %vx%v (%s)", poly.W, poly.H, poly.ExtentSource)
	}
	if arc := simPad(t, comps, "J2", "2"); arc.W != 0 || arc.H != 0 || arc.ShapeBBox != nil || arc.ExtentSource != "" {
		t.Fatalf("ARC polygon must not get a flattened size: %+v", arc)
	}
	// Coverage keeps POLYGON pads honest for pad-contact consumers.
	cov := (&boardSnapshot{Components: comps, ComponentsAvailability: "available"}).geometryCoverage(false)
	if cov.Complete || cov.UnsupportedGeometryPads != 2 {
		t.Fatalf("coverage = %+v", cov)
	}
}

func TestExactPolygonPadSegmentGap(t *testing.T) {
	p := pcbPadP{X: 180, Y: 120}
	if err := parseNetPathPadShape(map[string]any{"shape": []any{"POLYGON", []any{172.0, 114.0, "L", 192.0, 114.0, 192.0, 124.0, 172.0, 124.0, 172.0, 114.0}}}, &p, "J2.1"); err != nil {
		t.Fatal(err)
	}
	if p.ShapeOK || !p.exactClearanceKnown() || p.W != 24 || p.H != 12 {
		t.Fatalf("parsed = %+v", p)
	}
	for _, c := range []struct {
		a, z [2]float64
		want float64
	}{
		{[2]float64{160, 130}, [2]float64{200, 130}, 6}, // above the top edge
		{[2]float64{172, 100}, [2]float64{172, 140}, 0}, // grazes the left edge
		{[2]float64{182, 119}, [2]float64{182, 119}, 0}, // inside
		{[2]float64{195, 114}, [2]float64{198, 114}, 3}, // right of the right edge
	} {
		got, err := pcbExactPadSegmentGap(p, c.a, c.z)
		if err != nil || math.Abs(got-c.want) > 1e-9 {
			t.Fatalf("gap(%v,%v) = %v %v, want %v", c.a, c.z, got, err, c.want)
		}
	}
	arc := pcbPadP{X: 120, Y: 120}
	_ = parseNetPathPadShape(map[string]any{"shape": []any{"POLYGON", []any{115.0, 115.0, "ARC", 90.0, 125.0, 115.0, "L", 125.0, 125.0, 115.0, 125.0, 115.0, 115.0}}}, &arc, "J2.2")
	if arc.exactClearanceKnown() || !strings.Contains(arc.ShapeIssue, "ARC is unsupported") {
		t.Fatalf("arc polygon = %+v", arc)
	}
	if _, err := pcbExactPadSegmentGap(arc, [2]float64{0, 0}, [2]float64{1, 1}); err == nil {
		t.Fatal("arc polygon clearance must be unknown")
	}
}

func simRouteFixture(t *testing.T, pads ...boardPad) (pcbRouteRequest, *boardSnapshot) {
	t.Helper()
	in, s := routeSolverFixture()
	in.MaxDetourMil = 200
	s.Rules.HoleToTrackMil = 6.93
	s.Components = append(s.Components, boardComp{ID: "sim", Designator: "J2", X: 150, Y: 80, Layer: 1, BBox: &layoutBBox{MinX: 100, MinY: 30, MaxX: 200, MaxY: 130}, Pads: pads})
	return in, s
}

// A polygon pad or an NPTH slot on the straight line forces a measured detour;
// before #309 one POLYGON pad anywhere rejected the whole board.
func TestPCBRouteUsesPolygonPadsAndNPTHSlots(t *testing.T) {
	comps := loadSimHolderComponents(t)
	slot := simPad(t, comps, "J2", "PEG1")
	poly := simPad(t, comps, "J2", "1")
	poly.X, poly.Y = 150, 80
	poly.Shape = []any{"POLYGON", []any{140.0, 70.0, "L", 160.0, 70.0, 160.0, 90.0, 140.0, 90.0, 140.0, 70.0}}
	for name, pad := range map[string]boardPad{"npth-slot": slot, "polygon-pad": poly} {
		t.Run(name, func(t *testing.T) {
			in, s := simRouteFixture(t, pad)
			req, clear, _, err := preparePCBRouteRequest(in, s)
			if err != nil {
				t.Fatal(err)
			}
			if clear(req.From, req.To) {
				t.Fatal("straight route crossed the obstacle")
			}
			rep := newPCBRouteReport(nil, nil)
			if err := solvePCBRoute(context.Background(), in, s, &rep); err != nil || rep.Status != "pass" || rep.LengthMil <= 200 {
				t.Fatalf("report = %+v err = %v", rep, err)
			}
			check := newPCBRouteReport(nil, nil)
			if err := checkPCBRoute(context.Background(), in, s, rep, &check); err != nil {
				t.Fatal(err)
			}
		})
	}
	// The slot's clearance comes from the native Hole↔Track rule: a 4 mil
	// track passing 8.5 mil from the slot axis end clears the wall by 3.5 mil,
	// short of 2 + 6.93.
	in, s := simRouteFixture(t, slot)
	_, clear, _, err := preparePCBRouteRequest(in, s)
	if err != nil {
		t.Fatal(err)
	}
	if clear([2]float64{100, 103.5}, [2]float64{200, 103.5}) {
		t.Fatal("Hole↔Track spacing ignored")
	}
	if !clear([2]float64{100, 115}, [2]float64{200, 115}) {
		t.Fatal("segment clear of the slot by 10 mil was rejected")
	}
}

// Unsupported geometry yields one reason per item instead of a whole-board
// refusal on the first pad, and the report states what stays unobservable.
func TestPCBRouteListsEveryUnmeasurablePad(t *testing.T) {
	comps := loadSimHolderComponents(t)
	in, s := simRouteFixture(t, comps[0].Pads...)
	s.Components = append(s.Components, comps[1])
	s.Rules.HoleToTrackMil = 0
	rep := newPCBRouteReport(nil, nil)
	if err := solvePCBRoute(context.Background(), in, s, &rep); err == nil || rep.Status != "incomplete" {
		t.Fatalf("report = %+v err = %v", rep, err)
	}
	joined := strings.Join(rep.Reasons, "\n")
	for _, want := range []string{
		"pad J2.PEG2 drill geometry unknown: holeOffsetX is nonzero",
		"pad J2.2 copper geometry unsupported",
		"pad J3.PEG1 drill geometry unknown: holeOffsetX was not captured",
		"Hole↔Track spacing rule was not measured",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("reasons lack %q:\n%s", want, joined)
		}
	}
	if len(rep.Reasons) != 4 {
		t.Errorf("want one reason per item, got %d:\n%s", len(rep.Reasons), joined)
	}
	if !strings.Contains(strings.Join(rep.Limitations, "\n"), "footprint-internal non-pad primitives are unobservable") {
		t.Errorf("limitations = %v", rep.Limitations)
	}
}

// Track-to-outline checks use the Track/Via↔Board Outline cell, not the zone
// cell (#309: 10 mil reported, 11.8 mil enforced natively).
func TestPCBRouteUsesTrackEdgeRule(t *testing.T) {
	in, s := routeSolverFixture()
	s.Rules.CopperToEdgeMil = 11.8
	s.Rules.ZoneToEdgeMil = 10
	_, clear, _, err := preparePCBRouteRequest(in, s)
	if err != nil {
		t.Fatal(err)
	}
	// 4 mil track centered 13 mil from the y=0 edge: wall at 11 mil < 11.8.
	if clear([2]float64{100, 13}, [2]float64{200, 13}) {
		t.Fatal("track-to-edge rule not applied")
	}
	if !clear([2]float64{100, 14}, [2]float64{200, 14}) {
		t.Fatal("legal edge distance rejected")
	}
}
