package app

import (
	"encoding/json"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestBoardPadProjectionPreservesNestedContoursAndArc(t *testing.T) {
	outer := []any{12.0, 20.0, "ARC", 90.0, 10.0, 22.0, "L", 8.0, 20.0, 12.0, 20.0}
	hole := []any{10.0, 20.0, "L", 10.0, 21.0, 11.0, 20.0, 10.0, 20.0}
	c := lpComp("u", "J1", 10, 20, 0, lpBBox(8, 18, 12, 22), boardPad{Number: "1", X: 10, Y: 20, Shape: []any{"POLYGON", []any{outer, hole}}})
	before := canonicalJSON(c)
	got, err := transformBoardComp(c, 10, 20, 100, 200, 90)
	if err != nil {
		t.Fatal(err)
	}
	want := []any{"POLYGON", []any{
		[]any{110.0, 222.0, "ARC", 90.0, 108.0, 220.0, "L", 110.0, 218.0, 110.0, 222.0},
		[]any{110.0, 220.0, "L", 109.0, 220.0, 110.0, 221.0, 110.0, 220.0},
	}}
	if !reflect.DeepEqual(got.Pads[0].Shape, want) {
		t.Fatalf("shape=%v; want %v", got.Pads[0].Shape, want)
	}
	if got.Pads[0].X != 110 || got.Pads[0].Y != 220 || got.Pads[0].Rotation != 90 {
		t.Fatalf("pad=%+v", got.Pads[0])
	}
	back, err := transformBoardComp(got, 110, 220, -100, -200, -90)
	if err != nil {
		t.Fatal(err)
	}
	if canonicalJSON(back) != before {
		t.Fatal("inverse transform changed source geometry")
	}
	got.Pads[0].Shape.([]any)[1].([]any)[0].([]any)[0] = 999.0
	if canonicalJSON(c) != before {
		t.Fatal("candidate aliases source polygon")
	}
}

func TestBoardPadProjectionRealUSBShapes(t *testing.T) {
	data, err := os.ReadFile("testdata/pcb_polygon_pad/usb-c-component.json")
	if err != nil {
		t.Fatal(err)
	}
	var c boardComp
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	before := canonicalJSON(c)
	polygons, relative := 0, 0
	for _, delta := range []float64{0, 90, 180, 270} {
		got, err := transformBoardComp(c, c.X, c.Y, 4193.6685, 6784.209, delta)
		if err != nil {
			t.Fatal(err)
		}
		for i, p := range c.Pads {
			shape := p.Shape.([]any)
			if shape[0] != "POLYGON" {
				if !reflect.DeepEqual(got.Pads[i].Shape, shape) {
					t.Fatalf("relative shape moved: %s", p.Number)
				}
				if delta == 0 {
					relative++
				}
				continue
			}
			if delta == 0 {
				polygons++
			}
			source, err := polygonSourceContours(shape[1])
			if err != nil {
				t.Fatal(err)
			}
			projected, err := polygonSourceContours(got.Pads[i].Shape.([]any)[1])
			if err != nil {
				t.Fatal(err)
			}
			for n, contour := range source {
				for j, point := range contour {
					x, y := point[0]-c.X, point[1]-c.Y
					switch delta {
					case 90:
						x, y = -y, x
					case 180:
						x, y = -x, -y
					case 270:
						x, y = y, -x
					}
					x += c.X + 4193.6685
					y += c.Y + 6784.209
					if math.Hypot(projected[n][j][0]-x, projected[n][j][1]-y) > 0.00008 {
						t.Fatalf("pad %s vertex %d rotation %.0f stayed at source", p.Number, j, delta)
					}
				}
			}
		}
	}
	if polygons != 4 || relative != 12 {
		t.Fatalf("controls=%d/%d", polygons, relative)
	}
	if canonicalJSON(c) != before {
		t.Fatal("repeated candidates changed source")
	}
}

func TestBoardPadProjectionRejectsUnknownOrMalformedGeometry(t *testing.T) {
	triangle := []any{0.0, 0.0, "L", 10.0, 0.0, 0.0, 10.0, 0.0, 0.0}
	for name, shape := range map[string]any{
		"missing":        []any{"POLYGON", nil},
		"command":        []any{"POLYGON", []any{0.0, 0.0, "BEZIER", 1.0, 2.0, 3.0, 4.0}},
		"odd-line":       []any{"POLYGON", []any{0.0, 0.0, "L", 1.0, 2.0, 3.0}},
		"empty-line":     []any{"POLYGON", []any{0.0, 0.0, "L", "ARC", 90.0, 1.0, 2.0}},
		"incomplete-arc": []any{"POLYGON", []any{0.0, 0.0, "ARC", 90.0, 1.0}},
		"nan":            []any{"POLYGON", []any{math.NaN(), 0.0, "L", 1.0, 2.0, 3.0, 4.0}},
		"inf":            []any{"POLYGON", []any{0.0, 0.0, "L", math.Inf(1), 2.0, 3.0, 4.0}},
		"arc-inf":        []any{"POLYGON", []any{0.0, 0.0, "ARC", math.Inf(1), 1.0, 2.0, "L", 3.0, 4.0}},
		"too-few":        []any{"POLYGON", []any{0.0, 0.0, "L", 1.0, 2.0}},
		"mixed-contours": []any{"POLYGON", []any{triangle, "unknown"}},
		"extra-tuple":    []any{"POLYGON", triangle, 1.0},
		"unknown":        []any{"CUSTOM", triangle},
		"bad-relative":   []any{"RECT", -1.0, 2.0, 0.0},
		"extra-relative": []any{"RECT", 10.0, 20.0, 0.0, map[string]any{"unknownAbsoluteGeometry": []any{1.0, 2.0, 3.0, 4.0}}},
		"extra-oval":     []any{"OVAL", 10.0, 20.0, 1.0},
		"extra-ellipse":  []any{"ELLIPSE", 10.0, 20.0, 1.0},
		"extra-ngon":     []any{"NGON", 10.0, 6.0, 1.0},
	} {
		t.Run(name, func(t *testing.T) {
			c := lpComp("j", "J1", 0, 0, 0, lpBBox(-10, -10, 10, 10), boardPad{ID: "pad1", Number: "1", Shape: shape})
			got, err := transformBoardComp(c, 0, 0, 100, 200, 90)
			if err == nil || !strings.Contains(err.Error(), "J1 pad 1 (pad1)") {
				t.Fatalf("got=%+v err=%v", got, err)
			}
			if got.ID != "" || len(got.Pads) != 0 {
				t.Fatal("returned partial candidate")
			}
			_, err = generateRigidVariants(pcbLayoutModuleSpec{AnchorRef: "J1"}, map[string]boardComp{"J1": c})
			if err == nil {
				t.Fatal("rigid generator accepted bad geometry")
			}
		})
	}
	c := lpComp("j", "J1", 0, 0, 0, lpBBox(-10, -10, 10, 10), boardPad{Number: "1", Shape: []any{"POLYGON", triangle}, SpecialPad: []any{triangle}})
	if _, err := translateBoardComp(c, 10, 20); err == nil {
		t.Fatal("unsupported special pad accepted")
	}
	if _, err := transformBoardComp(c, 0, 0, math.Inf(1), 0, 0); err == nil {
		t.Fatal("non-finite transform accepted")
	}
}

func TestBoardPadProjectionKeepsRelativeDimensionsAndRoutingBoundary(t *testing.T) {
	for _, shape := range [][]any{{"RECT", 10.0, 20.0, 2.0}, {"OVAL", 10.0, 20.0}, {"ELLIPSE", 10.0, 20.0}, {"NGON", 20.0, 6.0}} {
		c := lpComp("r", "R1", 0, 0, 0, lpBBox(-10, -10, 10, 10), boardPad{X: 2, Y: 3, W: 10, H: 20, Shape: shape})
		got, err := transformBoardComp(c, 0, 0, 100, 200, 90)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got.Pads[0].Shape, shape) || got.Pads[0].W != 20 || got.Pads[0].H != 10 {
			t.Fatalf("relative=%+v", got.Pads[0])
		}
		got.Pads[0].Shape.([]any)[1] = 999.0
		if shape[1] == 999.0 {
			t.Fatal("relative tuple aliases source")
		}
	}
	var parsed pcbPadP
	err := parseNetPathPadShape(map[string]any{"shape": []any{"POLYGON", []any{0.0, 0.0, "L", 1.0, 0.0, 0.0, 1.0}}}, &parsed, "pad")
	if err != nil || parsed.ShapeOK || !strings.Contains(parsed.ShapeIssue, "not supported") {
		t.Fatalf("routing boundary changed: %+v err=%v", parsed, err)
	}
}
