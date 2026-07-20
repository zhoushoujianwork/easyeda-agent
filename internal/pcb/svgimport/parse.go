package svgimport

import (
	"math"
	"strconv"
)

// parseFloatList parses a whitespace/comma separated list of floats, tolerating
// the SVG shorthands (concatenated numbers like "1.5.5", exponents, leading +/-).
func parseFloatList(s string) []float64 {
	var out []float64
	toks := tokenizeNumbers(s)
	for _, t := range toks {
		if v, err := strconv.ParseFloat(t, 64); err == nil {
			out = append(out, v)
		}
	}
	return out
}

// tokenizeNumbers splits a run of SVG numbers into individual number strings.
// SVG allows numbers to abut without a separator ("1-2" = 1,-2; "1.5.5" =
// 1.5,.5), so a hand-rolled scanner is required.
func tokenizeNumbers(s string) []string {
	var out []string
	i, n := 0, len(s)
	for i < n {
		c := s[i]
		if c == ' ' || c == ',' || c == '\t' || c == '\r' || c == '\n' {
			i++
			continue
		}
		start := i
		seenDot := false
		seenExp := false
		if c == '+' || c == '-' {
			i++
		}
		for i < n {
			c = s[i]
			switch {
			case c >= '0' && c <= '9':
				i++
			case c == '.' && !seenDot && !seenExp:
				seenDot = true
				i++
			case (c == 'e' || c == 'E') && !seenExp && i > start:
				seenExp = true
				i++
				if i < n && (s[i] == '+' || s[i] == '-') {
					i++
				}
			default:
				goto done
			}
		}
	done:
		if i > start {
			out = append(out, s[start:i])
		} else {
			i++ // skip a stray char to avoid an infinite loop
		}
	}
	return out
}

// tokenizePath splits a path "d" attribute into command letters and number
// tokens in order.
func tokenizePath(d string) []string {
	var out []string
	i, n := 0, len(d)
	for i < n {
		c := d[i]
		switch {
		case c == ' ' || c == ',' || c == '\t' || c == '\r' || c == '\n':
			i++
		case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'):
			out = append(out, string(c))
			i++
		default:
			// a number (possibly abutting the next)
			start := i
			seenDot := false
			seenExp := false
			if c == '+' || c == '-' {
				i++
			}
			for i < n {
				c = d[i]
				switch {
				case c >= '0' && c <= '9':
					i++
				case c == '.' && !seenDot && !seenExp:
					seenDot = true
					i++
				case (c == 'e' || c == 'E') && !seenExp && i > start:
					seenExp = true
					i++
					if i < n && (d[i] == '+' || d[i] == '-') {
						i++
					}
				default:
					goto numdone
				}
			}
		numdone:
			if i > start {
				out = append(out, d[start:i])
			} else {
				i++
			}
		}
	}
	return out
}

type pt struct{ x, y float64 }

// parsePathData parses a path "d" string into one or more subpaths of ABSOLUTE
// user-space points, flattening every curve (C/S/Q/T/A) to line segments with
// the given tolerance (in the same user units). A new subpath starts at each
// M/m; Z/z closes the current subpath.
func parsePathData(d string, tol float64) [][]pt {
	toks := tokenizePath(d)
	var subs [][]pt
	var cur []pt
	var curX, curY float64        // current point
	var startX, startY float64    // subpath start (for Z)
	var prevCtrl pt               // reflected control point for S/T
	var prevCmd byte              // last command (for S/T smoothing)
	i := 0

	num := func() (float64, bool) {
		if i < len(toks) {
			if v, err := strconv.ParseFloat(toks[i], 64); err == nil {
				i++
				return v, true
			}
			// a command letter where a number was expected → stop
		}
		return 0, false
	}
	flush := func() {
		if len(cur) >= 2 {
			subs = append(subs, cur)
		}
		cur = nil
	}

	for i < len(toks) {
		t := toks[i]
		if len(t) != 1 || !isAlpha(t[0]) {
			// stray number without a command — skip
			i++
			continue
		}
		cmd := t[0]
		i++
		rel := cmd >= 'a' && cmd <= 'z'
		up := upper(cmd)

		switch up {
		case 'M':
			x, ok := num()
			if !ok {
				break
			}
			y, _ := num()
			if rel {
				x += curX
				y += curY
			}
			flush()
			curX, curY = x, y
			startX, startY = x, y
			cur = []pt{{x, y}}
			// subsequent implicit pairs after M are treated as L
			for {
				if i >= len(toks) || !isNumTok(toks[i]) {
					break
				}
				lx, _ := num()
				ly, _ := num()
				if rel {
					lx += curX
					ly += curY
				}
				curX, curY = lx, ly
				cur = append(cur, pt{lx, ly})
			}
		case 'L':
			for isNumTok(peek(toks, i)) {
				x, _ := num()
				y, _ := num()
				if rel {
					x += curX
					y += curY
				}
				curX, curY = x, y
				cur = append(cur, pt{x, y})
			}
		case 'H':
			for isNumTok(peek(toks, i)) {
				x, _ := num()
				if rel {
					x += curX
				}
				curX = x
				cur = append(cur, pt{curX, curY})
			}
		case 'V':
			for isNumTok(peek(toks, i)) {
				y, _ := num()
				if rel {
					y += curY
				}
				curY = y
				cur = append(cur, pt{curX, curY})
			}
		case 'C':
			for isNumTok(peek(toks, i)) {
				x1, _ := num()
				y1, _ := num()
				x2, _ := num()
				y2, _ := num()
				x, _ := num()
				y, _ := num()
				if rel {
					x1 += curX
					y1 += curY
					x2 += curX
					y2 += curY
					x += curX
					y += curY
				}
				flattenCubic(&cur, curX, curY, x1, y1, x2, y2, x, y, tol)
				prevCtrl = pt{x2, y2}
				curX, curY = x, y
			}
		case 'S':
			for isNumTok(peek(toks, i)) {
				x2, _ := num()
				y2, _ := num()
				x, _ := num()
				y, _ := num()
				if rel {
					x2 += curX
					y2 += curY
					x += curX
					y += curY
				}
				var x1, y1 float64
				if upper(prevCmd) == 'C' || upper(prevCmd) == 'S' {
					x1 = 2*curX - prevCtrl.x
					y1 = 2*curY - prevCtrl.y
				} else {
					x1, y1 = curX, curY
				}
				flattenCubic(&cur, curX, curY, x1, y1, x2, y2, x, y, tol)
				prevCtrl = pt{x2, y2}
				curX, curY = x, y
			}
		case 'Q':
			for isNumTok(peek(toks, i)) {
				x1, _ := num()
				y1, _ := num()
				x, _ := num()
				y, _ := num()
				if rel {
					x1 += curX
					y1 += curY
					x += curX
					y += curY
				}
				flattenQuad(&cur, curX, curY, x1, y1, x, y, tol)
				prevCtrl = pt{x1, y1}
				curX, curY = x, y
			}
		case 'T':
			for isNumTok(peek(toks, i)) {
				x, _ := num()
				y, _ := num()
				if rel {
					x += curX
					y += curY
				}
				var x1, y1 float64
				if upper(prevCmd) == 'Q' || upper(prevCmd) == 'T' {
					x1 = 2*curX - prevCtrl.x
					y1 = 2*curY - prevCtrl.y
				} else {
					x1, y1 = curX, curY
				}
				flattenQuad(&cur, curX, curY, x1, y1, x, y, tol)
				prevCtrl = pt{x1, y1}
				curX, curY = x, y
			}
		case 'A':
			for isNumTok(peek(toks, i)) {
				rx, _ := num()
				ry, _ := num()
				xrot, _ := num()
				large, _ := num()
				sweep, _ := num()
				x, _ := num()
				y, _ := num()
				if rel {
					x += curX
					y += curY
				}
				flattenArc(&cur, curX, curY, rx, ry, xrot, large != 0, sweep != 0, x, y, tol)
				curX, curY = x, y
			}
		case 'Z':
			if len(cur) > 0 {
				cur = append(cur, pt{startX, startY})
			}
			curX, curY = startX, startY
			flush()
		}
		prevCmd = cmd
	}
	flush()
	return subs
}

func isAlpha(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
func upper(c byte) byte {
	if c >= 'a' && c <= 'z' {
		return c - 32
	}
	return c
}
func isNumTok(s string) bool {
	if s == "" {
		return false
	}
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}
func peek(toks []string, i int) string {
	if i < len(toks) {
		return toks[i]
	}
	return ""
}

// ── curve flattening ────────────────────────────────────────────────────────

// flattenCubic appends flattened points (excluding the start, including the end)
// of a cubic Bézier to out.
func flattenCubic(out *[]pt, x0, y0, x1, y1, x2, y2, x3, y3, tol float64) {
	subdivCubic(out, x0, y0, x1, y1, x2, y2, x3, y3, tol, 0)
	*out = append(*out, pt{x3, y3})
}

func subdivCubic(out *[]pt, x0, y0, x1, y1, x2, y2, x3, y3, tol float64, depth int) {
	if depth > 18 || cubicFlatEnough(x0, y0, x1, y1, x2, y2, x3, y3, tol) {
		return
	}
	// de Casteljau split at t=0.5
	x01, y01 := (x0+x1)/2, (y0+y1)/2
	x12, y12 := (x1+x2)/2, (y1+y2)/2
	x23, y23 := (x2+x3)/2, (y2+y3)/2
	x012, y012 := (x01+x12)/2, (y01+y12)/2
	x123, y123 := (x12+x23)/2, (y12+y23)/2
	xm, ym := (x012+x123)/2, (y012+y123)/2
	subdivCubic(out, x0, y0, x01, y01, x012, y012, xm, ym, tol, depth+1)
	*out = append(*out, pt{xm, ym})
	subdivCubic(out, xm, ym, x123, y123, x23, y23, x3, y3, tol, depth+1)
}

func cubicFlatEnough(x0, y0, x1, y1, x2, y2, x3, y3, tol float64) bool {
	// max distance of the two control points from the chord.
	d1 := pointLineDist(x1, y1, x0, y0, x3, y3)
	d2 := pointLineDist(x2, y2, x0, y0, x3, y3)
	return math.Max(d1, d2) <= tol
}

func flattenQuad(out *[]pt, x0, y0, x1, y1, x2, y2, tol float64) {
	// elevate quadratic to cubic
	c1x, c1y := x0+2.0/3.0*(x1-x0), y0+2.0/3.0*(y1-y0)
	c2x, c2y := x2+2.0/3.0*(x1-x2), y2+2.0/3.0*(y1-y2)
	flattenCubic(out, x0, y0, c1x, c1y, c2x, c2y, x2, y2, tol)
}

func pointLineDist(px, py, ax, ay, bx, by float64) float64 {
	dx, dy := bx-ax, by-ay
	l2 := dx*dx + dy*dy
	if l2 == 0 {
		return math.Hypot(px-ax, py-ay)
	}
	return math.Abs((px-ax)*dy-(py-ay)*dx) / math.Sqrt(l2)
}

// flattenArc converts an SVG elliptical arc (endpoint parameterization) to a
// center parameterization and samples it into line segments.
func flattenArc(out *[]pt, x0, y0, rx, ry, xrotDeg float64, large, sweep bool, x, y, tol float64) {
	if rx == 0 || ry == 0 || (x0 == x && y0 == y) {
		*out = append(*out, pt{x, y})
		return
	}
	rx, ry = math.Abs(rx), math.Abs(ry)
	phi := xrotDeg * math.Pi / 180
	cosP, sinP := math.Cos(phi), math.Sin(phi)

	// step 1: compute (x1',y1')
	dx2, dy2 := (x0-x)/2, (y0-y)/2
	x1p := cosP*dx2 + sinP*dy2
	y1p := -sinP*dx2 + cosP*dy2

	// correct out-of-range radii
	lambda := (x1p*x1p)/(rx*rx) + (y1p*y1p)/(ry*ry)
	if lambda > 1 {
		s := math.Sqrt(lambda)
		rx *= s
		ry *= s
	}

	// step 2: compute center (cx',cy')
	num := rx*rx*ry*ry - rx*rx*y1p*y1p - ry*ry*x1p*x1p
	den := rx*rx*y1p*y1p + ry*ry*x1p*x1p
	co := 0.0
	if den != 0 {
		v := num / den
		if v < 0 {
			v = 0
		}
		co = math.Sqrt(v)
	}
	if large == sweep {
		co = -co
	}
	cxp := co * (rx * y1p / ry)
	cyp := co * (-ry * x1p / rx)

	// step 3: compute center (cx,cy)
	cx := cosP*cxp - sinP*cyp + (x0+x)/2
	cy := sinP*cxp + cosP*cyp + (y0+y)/2

	// step 4: compute angles
	ang := func(ux, uy, vx, vy float64) float64 {
		dot := ux*vx + uy*vy
		len := math.Hypot(ux, uy) * math.Hypot(vx, vy)
		a := math.Acos(clamp(dot/len, -1, 1))
		if ux*vy-uy*vx < 0 {
			a = -a
		}
		return a
	}
	theta1 := ang(1, 0, (x1p-cxp)/rx, (y1p-cyp)/ry)
	dtheta := ang((x1p-cxp)/rx, (y1p-cyp)/ry, (-x1p-cxp)/rx, (-y1p-cyp)/ry)
	if !sweep && dtheta > 0 {
		dtheta -= 2 * math.Pi
	} else if sweep && dtheta < 0 {
		dtheta += 2 * math.Pi
	}

	// sample: pick segment count from tolerance vs radius
	rmax := math.Max(rx, ry)
	segs := 2
	if rmax > 0 && tol > 0 && tol < rmax {
		// max angular step so chord error <= tol
		maxStep := 2 * math.Acos(clamp(1-tol/rmax, -1, 1))
		if maxStep > 0 {
			segs = int(math.Ceil(math.Abs(dtheta) / maxStep))
		}
	}
	if segs < 2 {
		segs = 2
	}
	if segs > 512 {
		segs = 512
	}
	for k := 1; k <= segs; k++ {
		th := theta1 + dtheta*float64(k)/float64(segs)
		ex := cx + rx*math.Cos(th)*cosP - ry*math.Sin(th)*sinP
		ey := cy + rx*math.Cos(th)*sinP + ry*math.Sin(th)*cosP
		*out = append(*out, pt{ex, ey})
	}
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
