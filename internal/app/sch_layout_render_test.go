package app

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func renderFixture(t *testing.T) SchematicRenderInput {
	t.Helper()
	p, e := PlanSchematicLayout(standaloneLayoutFixture())
	if e != nil {
		t.Fatal(e)
	}
	return SchematicRenderInput{SchemaVersion: 1, Title: "preview", Zones: []SchematicRenderZone{{ID: "zone", Title: "Supply", Layout: p}}}
}
func TestLayoutRenderDeterministicSafeAndImmutable(t *testing.T) {
	in := renderFixture(t)
	in.Title = `<script>alert("x")</script>`
	in.Zones[0].Layout.Placements[0].Value = `A&B <svg onload="bad">`
	before, _ := json.Marshal(in)
	a, e := RenderSchematicLayoutSVG(in)
	if e != nil {
		t.Fatal(e)
	}
	b, e := RenderSchematicLayoutSVG(in)
	if e != nil || !bytes.Equal(a, b) {
		t.Fatal("nondeterministic")
	}
	after, _ := json.Marshal(in)
	if !bytes.Equal(before, after) {
		t.Fatal("mutated layout")
	}
	if bytes.Contains(a, []byte("<script>")) || bytes.Contains(a, []byte(`<svg onload`)) {
		t.Fatal("unescaped content")
	}
	d := xml.NewDecoder(bytes.NewReader(a))
	for {
		_, e = d.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
	}
	if bytes.Contains(a, []byte("差异")) {
		t.Fatal("unexpected diff panel")
	}
}
func TestLayoutRenderRejectsBadGeometry(t *testing.T) {
	for _, edit := range []func(*SchematicRenderInput){
		func(i *SchematicRenderInput) {
			i.Zones[0].Layout.Wires = []SchematicWire{{Net: "N", Points: [][2]float64{{0, 0}, {0, 0}}}}
		},
		func(i *SchematicRenderInput) {
			i.Zones[0].Layout.Placements[0].BBox.MaxX = i.Zones[0].Layout.Placements[0].BBox.MinX - 1
		},
		func(i *SchematicRenderInput) { i.Zones[0].Status = "passed" },
		func(i *SchematicRenderInput) { i.Zones = append(i.Zones, i.Zones[0]) },
	} {
		in := renderFixture(t)
		edit(&in)
		if _, e := RenderSchematicLayoutSVG(in); e == nil {
			t.Fatal("accepted bad geometry")
		}
	}
}
func TestLayoutRenderCLI(t *testing.T) {
	in := renderFixture(t)
	raw, _ := json.Marshal(in)
	dir := t.TempDir()
	from, out := filepath.Join(dir, "in.json"), filepath.Join(dir, "out.svg")
	if e := os.WriteFile(from, raw, 0600); e != nil {
		t.Fatal(e)
	}
	var buf bytes.Buffer
	c := newSchLayoutRenderCmd(&buf)
	c.SetArgs([]string{"--from", from, "--out", out, "--zone", "zone"})
	if e := c.Execute(); e != nil {
		t.Fatal(e)
	}
	good, _ := os.ReadFile(out)
	if !bytes.Contains(good, []byte("<svg")) {
		t.Fatal("no SVG")
	}
	bad := strings.Replace(string(raw), `"mirror":false,`, "", 1)
	if e := os.WriteFile(from, []byte(bad), 0600); e != nil {
		t.Fatal(e)
	}
	c = newSchLayoutRenderCmd(&buf)
	c.SetArgs([]string{"--from", from, "--out", out})
	if e := c.Execute(); e == nil {
		t.Fatal("invented missing geometry")
	}
	actual, _ := os.ReadFile(out)
	if !bytes.Equal(good, actual) {
		t.Fatal("overwrote last good SVG")
	}
	if e := os.WriteFile(from, raw, 0600); e != nil {
		t.Fatal(e)
	}
	alias := filepath.Join(dir, "alias.svg")
	if e := os.Link(from, alias); e != nil {
		t.Fatal(e)
	}
	c = newSchLayoutRenderCmd(&buf)
	c.SetArgs([]string{"--from", from, "--out", alias})
	if e := c.Execute(); e == nil {
		t.Fatal("overwrote input alias")
	}
}
func TestMidpointTapSymmetry(t *testing.T) {
	in := SchematicLayoutInput{SchemaVersion: 1, CoreComponentID: "a", NetPolicies: map[string]string{"P": "local_power", "G": "local_ground", "MID": "module_port"}, Components: []SchematicLayoutComponent{
		{ID: "a", Measurement: SchematicPlacement{Designator: "R1", BBox: SchematicBox{-5, -10, 5, 10}, Pins: []SchematicPin{{Number: "1", Net: "P", Y: -20}, {Number: "2", Net: "MID", Y: 20}}}},
		{ID: "b", Measurement: SchematicPlacement{Designator: "R2", X: 100, BBox: SchematicBox{95, -10, 105, 10}, Pins: []SchematicPin{{Number: "1", Net: "MID", X: 100, Y: -20}, {Number: "2", Net: "G", X: 100, Y: 20}}}},
	}}
	out, e := PlanSchematicLayout(in)
	if e != nil {
		t.Fatal(e)
	}
	a, _ := libPin(out.Placements[0], "2")
	b, _ := libPin(out.Placements[1], "1")
	if a.X != b.X {
		t.Fatal("vertical branch not aligned")
	}
	found := false
	for _, f := range out.Flags {
		if f.Net == "MID" {
			found = true
			if f.PinX != (a.X+b.X)/2 || f.PinY != (a.Y+b.Y)/2 || f.Direction != "right" && f.Direction != "left" {
				t.Fatalf("asymmetric tap: %+v between %+v/%+v", f, a, b)
			}
		}
	}
	if !found {
		t.Fatal("missing midpoint flag")
	}
	p := powerLayoutPlan{Placements: out.Placements, Wires: out.Wires, Flags: out.Flags}
	if e := validateSchCompositionNets(&p); e != nil {
		t.Fatal("midpoint did not name the actual wire tree", e)
	}
	p.Flags = nil
	p.Wires = nil
	if libPlaceMidpointMarker(&p, libIsland{net: "MID", pins: []powerLayoutPin{a, b}}, "net_port_bi") {
		t.Fatal("invented a midpoint without a real wire")
	}
}

func TestMarkerSearchShortestAcrossDirections(t *testing.T) {
	c := standaloneLayoutFixture().Components[0].Measurement
	p := powerLayoutPlan{Placements: []powerLayoutPlacement{c}}
	q := c.Pins[0]
	dirs := []string{"up", "right", "down", "left"}
	best := 301.0
	for _, d := range dirs {
		trial := p
		if libPlaceMarkerAt(&trial, q, "power", []string{d}, 300) {
			if n := trial.Flags[0].Offset; n < best {
				best = n
			}
		}
	}
	if !libPlaceMarkerAt(&p, q, "power", dirs, 300) || p.Flags[0].Offset != best {
		t.Fatal("direction preference beat shorter safe lead")
	}
}
