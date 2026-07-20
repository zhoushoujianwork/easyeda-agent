package svgimport

import (
	"math"
	"strings"
)

// matrix is a 2x3 affine transform mapping (x,y) → (a*x+c*y+e, b*x+d*y+f).
// This is the SVG transform convention (column-major [a b c d e f]).
type matrix struct {
	a, b, c, d, e, f float64
}

func identity() matrix { return matrix{1, 0, 0, 1, 0, 0} }

// mul returns m ∘ n (apply n first, then m) — used to compose a parent transform
// with a child's own transform (parent.mul(child)).
func (m matrix) mul(n matrix) matrix {
	return matrix{
		a: m.a*n.a + m.c*n.b,
		b: m.b*n.a + m.d*n.b,
		c: m.a*n.c + m.c*n.d,
		d: m.b*n.c + m.d*n.d,
		e: m.a*n.e + m.c*n.f + m.e,
		f: m.b*n.e + m.d*n.f + m.f,
	}
}

// apply transforms a point.
func (m matrix) apply(x, y float64) (float64, float64) {
	return m.a*x + m.c*y + m.e, m.b*x + m.d*y + m.f
}

func translate(tx, ty float64) matrix { return matrix{1, 0, 0, 1, tx, ty} }
func scale(sx, sy float64) matrix     { return matrix{sx, 0, 0, sy, 0, 0} }

func rotate(deg, cx, cy float64) matrix {
	r := deg * math.Pi / 180
	cs, sn := math.Cos(r), math.Sin(r)
	rot := matrix{cs, sn, -sn, cs, 0, 0}
	if cx == 0 && cy == 0 {
		return rot
	}
	return translate(cx, cy).mul(rot).mul(translate(-cx, -cy))
}

func skewX(deg float64) matrix { return matrix{1, 0, math.Tan(deg * math.Pi / 180), 1, 0, 0} }
func skewY(deg float64) matrix { return matrix{1, math.Tan(deg * math.Pi / 180), 0, 1, 0, 0} }

// parseTransform parses an SVG transform attribute (a chain of
// matrix/translate/scale/rotate/skewX/skewY functions) into a single matrix,
// composed left-to-right as SVG specifies.
func parseTransform(s string) matrix {
	m := identity()
	s = strings.TrimSpace(s)
	for len(s) > 0 {
		open := strings.IndexByte(s, '(')
		if open < 0 {
			break
		}
		name := strings.TrimSpace(s[:open])
		close := strings.IndexByte(s, ')')
		if close < 0 {
			break
		}
		args := parseFloatList(s[open+1 : close])
		s = strings.TrimSpace(s[close+1:])
		// a leading comma between functions is legal
		s = strings.TrimLeft(s, ", \t\r\n")

		switch name {
		case "matrix":
			if len(args) == 6 {
				m = m.mul(matrix{args[0], args[1], args[2], args[3], args[4], args[5]})
			}
		case "translate":
			switch len(args) {
			case 1:
				m = m.mul(translate(args[0], 0))
			case 2:
				m = m.mul(translate(args[0], args[1]))
			}
		case "scale":
			switch len(args) {
			case 1:
				m = m.mul(scale(args[0], args[0]))
			case 2:
				m = m.mul(scale(args[0], args[1]))
			}
		case "rotate":
			switch len(args) {
			case 1:
				m = m.mul(rotate(args[0], 0, 0))
			case 3:
				m = m.mul(rotate(args[0], args[1], args[2]))
			}
		case "skewX":
			if len(args) == 1 {
				m = m.mul(skewX(args[0]))
			}
		case "skewY":
			if len(args) == 1 {
				m = m.mul(skewY(args[0]))
			}
		}
	}
	return m
}
