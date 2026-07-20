package svgimport

import (
	"math"
	"strings"
	"testing"
)

func parse(t *testing.T, svg string, opts Options) *Result {
	t.Helper()
	r, err := Parse(strings.NewReader(svg), opts)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return r
}

// polyBBox returns the bbox of a single contour command array.
func polyBBox(poly []any) (minX, minY, maxX, maxY float64) {
	minX, minY = math.Inf(1), math.Inf(1)
	maxX, maxY = math.Inf(-1), math.Inf(-1)
	for i := 0; i < len(poly); {
		if s, ok := poly[i].(string); ok && s == "L" {
			i++
			continue
		}
		x := poly[i].(float64)
		y := poly[i+1].(float64)
		minX, minY = math.Min(minX, x), math.Min(minY, y)
		maxX, maxY = math.Max(maxX, x), math.Max(maxY, y)
		i += 2
	}
	return
}

func approx(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func TestRectScaledToWidth(t *testing.T) {
	svg := `<svg viewBox="0 0 100 50"><rect x="0" y="0" width="100" height="50"/></svg>`
	r := parse(t, svg, Options{TargetWidth: 200})
	if r.PathCount != 1 {
		t.Fatalf("want 1 contour, got %d", r.PathCount)
	}
	if !approx(r.Width, 200, 0.01) || !approx(r.Height, 100, 0.01) {
		t.Fatalf("want 200x100, got %gx%g", r.Width, r.Height)
	}
	minX, minY, maxX, maxY := polyBBox(r.Polygons[0])
	if !approx(minX, 0, 0.01) || !approx(minY, 0, 0.01) || !approx(maxX, 200, 0.01) || !approx(maxY, 100, 0.01) {
		t.Fatalf("bbox = %g,%g..%g,%g", minX, minY, maxX, maxY)
	}
}

func TestKeepAspect(t *testing.T) {
	svg := `<svg viewBox="0 0 100 50"><rect width="100" height="50"/></svg>`
	r := parse(t, svg, Options{TargetWidth: 200, TargetHeight: 200, KeepAspect: true})
	// uniform scale = min(200/100, 200/50)=2 → 200x100
	if !approx(r.Width, 200, 0.01) || !approx(r.Height, 100, 0.01) {
		t.Fatalf("keep-aspect want 200x100, got %gx%g", r.Width, r.Height)
	}
}

func TestNonUniformScale(t *testing.T) {
	svg := `<svg viewBox="0 0 100 50"><rect width="100" height="50"/></svg>`
	r := parse(t, svg, Options{TargetWidth: 300, TargetHeight: 300})
	if !approx(r.Width, 300, 0.01) || !approx(r.Height, 300, 0.01) {
		t.Fatalf("non-uniform want 300x300, got %gx%g", r.Width, r.Height)
	}
}

func TestContourWithHole(t *testing.T) {
	// outer square + inner square (a hole under even-odd)
	svg := `<svg viewBox="0 0 100 100">
	  <path d="M10 10 H90 V90 H10 Z"/>
	  <path d="M30 30 H70 V70 H30 Z"/>
	</svg>`
	r := parse(t, svg, Options{TargetWidth: 100})
	if r.PathCount != 2 {
		t.Fatalf("want 2 contours (outer+hole), got %d", r.PathCount)
	}
}

func TestPathStartsWithMoveL(t *testing.T) {
	svg := `<svg viewBox="0 0 10 10"><path d="M0 0 L10 0 L10 10 L0 10 Z"/></svg>`
	r := parse(t, svg, Options{TargetWidth: 10})
	poly := r.Polygons[0]
	// first contour must be [x0,y0,"L", ...]
	if len(poly) < 3 {
		t.Fatalf("contour too short: %v", poly)
	}
	if _, ok := poly[0].(float64); !ok {
		t.Fatalf("poly[0] not a number: %T", poly[0])
	}
	if s, ok := poly[2].(string); !ok || s != "L" {
		t.Fatalf("poly[2] must be \"L\", got %v", poly[2])
	}
}

func TestCubicFlattening(t *testing.T) {
	// a cubic that bulges to y≈ -? ; just assert it produced many segments and a
	// sane bbox (endpoints 0,0 → 100,0).
	svg := `<svg viewBox="0 0 100 100"><path d="M0 0 C0 50 100 50 100 0"/></svg>`
	r := parse(t, svg, Options{TargetWidth: 100, FlattenTol: 0.5})
	if r.PointCount < 5 {
		t.Fatalf("cubic under-flattened: %d points", r.PointCount)
	}
	_, minY, _, maxY := polyBBox(r.Polygons[0])
	// control points pull the curve down to ~37.5% → bbox height > 20 in a 100-tall frame
	if maxY-minY < 20 {
		t.Fatalf("cubic bbox height too small: %g", maxY-minY)
	}
}

func TestTransformTranslate(t *testing.T) {
	svg := `<svg viewBox="0 0 200 200"><g transform="translate(100,100)"><rect width="50" height="50"/></g></svg>`
	r := parse(t, svg, Options{}) // no target → user units as mil
	minX, minY, _, _ := polyBBox(r.Polygons[0])
	// content bbox min is normalised to 0,0 regardless of translate, but the
	// single rect means min=0. Assert size preserved.
	if !approx(r.Width, 50, 0.01) || !approx(r.Height, 50, 0.01) {
		t.Fatalf("translate broke size: %gx%g", r.Width, r.Height)
	}
	if minX != 0 || minY != 0 {
		t.Fatalf("not normalised to origin: %g,%g", minX, minY)
	}
}

func TestTransformNested(t *testing.T) {
	// outer translate + inner scale should compose: a 10x10 rect scaled 2x = 20x20
	svg := `<svg viewBox="0 0 200 200"><g transform="translate(10,10)"><g transform="scale(2)"><rect width="10" height="10"/></g></g></svg>`
	r := parse(t, svg, Options{})
	if !approx(r.Width, 20, 0.01) || !approx(r.Height, 20, 0.01) {
		t.Fatalf("nested transform want 20x20, got %gx%g", r.Width, r.Height)
	}
}

func TestCircle(t *testing.T) {
	svg := `<svg viewBox="0 0 100 100"><circle cx="50" cy="50" r="40"/></svg>`
	r := parse(t, svg, Options{TargetWidth: 80, FlattenTol: 0.5})
	if !approx(r.Width, 80, 0.5) || !approx(r.Height, 80, 0.5) {
		t.Fatalf("circle want ~80x80, got %gx%g", r.Width, r.Height)
	}
}

func TestPolygon(t *testing.T) {
	svg := `<svg viewBox="0 0 100 100"><polygon points="0,0 100,0 50,100"/></svg>`
	r := parse(t, svg, Options{TargetWidth: 100})
	if r.PathCount != 1 {
		t.Fatalf("want 1 contour, got %d", r.PathCount)
	}
	if !approx(r.Width, 100, 0.01) || !approx(r.Height, 100, 0.01) {
		t.Fatalf("triangle bbox want 100x100, got %gx%g", r.Width, r.Height)
	}
}

func TestErrorNoGeometry(t *testing.T) {
	svg := `<svg viewBox="0 0 10 10"></svg>`
	if _, err := Parse(strings.NewReader(svg), Options{}); err == nil {
		t.Fatal("want error for empty svg, got nil")
	}
}

func TestArcSemicircle(t *testing.T) {
	// half-circle arc from (0,0) to (100,0), radius 50 → bbox ~100 wide, ~50 tall
	svg := `<svg viewBox="0 0 100 100"><path d="M0 0 A50 50 0 0 1 100 0 Z"/></svg>`
	r := parse(t, svg, Options{TargetWidth: 100, FlattenTol: 0.25})
	_, minY, _, maxY := polyBBox(r.Polygons[0])
	if !approx(maxY-minY, 50, 2) {
		t.Fatalf("semicircle height want ~50, got %g", maxY-minY)
	}
}
