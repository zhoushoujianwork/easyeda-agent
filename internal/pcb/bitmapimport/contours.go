package bitmapimport

import "fmt"

type point struct{ x, y int }

// traceContours traces directed unit boundary edges, keeping foreground on the
// right. A vertex can have two outgoing edges when diagonal pixels touch. The
// right-turn priority keeps their contours separate, without joining pixels
// through a point or introducing a diagonal bridge.
func traceContours(mask []bool, w, h int, simplify bool) ([][]point, int, error) {
	stride := w + 1
	// Four direction bits per grid vertex avoid allocating individual edges
	// (up to millions of objects for a fragmented but permitted input image).
	edges := make([]uint8, stride*(h+1))
	inside := func(x, y int) bool {
		return x >= 0 && y >= 0 && x < w && y < h && mask[y*w+x]
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if !mask[y*w+x] {
				continue
			}
			if !inside(x, y-1) {
				edges[y*stride+x] |= 1 << 0 // east
			}
			if !inside(x+1, y) {
				edges[y*stride+x+1] |= 1 << 1 // south
			}
			if !inside(x, y+1) {
				edges[(y+1)*stride+x+1] |= 1 << 2 // west
			}
			if !inside(x-1, y) {
				edges[(y+1)*stride+x] |= 1 << 3 // north
			}
		}
	}
	offsets := [4]int{1, stride, -1, -stride}
	var contours [][]point
	pointCount := 0
	for seed := 0; seed < len(edges); seed++ {
		for edges[seed] != 0 {
			dir := 0
			for edges[seed]&(1<<dir) == 0 {
				dir++
			}
			current := seed
			contour := []point{{seed % stride, seed / stride}}
			for {
				edges[current] &^= 1 << dir
				next := current + offsets[dir]
				if next < 0 || next >= len(edges) {
					return nil, 0, fmt.Errorf("bitmap boundary escaped its canvas")
				}
				p := point{next % stride, next / stride}
				if simplify && len(contour) >= 2 && collinear(contour[len(contour)-2], contour[len(contour)-1], p) {
					contour[len(contour)-1] = p
				} else {
					contour = append(contour, p)
				}
				// Closing at the seed can remove one more collinear vertex;
				// reserve that one point until the final contour is checked.
				if pointCount+len(contour) > MaxPoints+1 {
					return nil, 0, fmt.Errorf("bitmap contours exceed %d-point limit", MaxPoints)
				}
				if next == seed {
					break
				}
				found := false
				for _, candidate := range [4]int{(dir + 1) % 4, dir, (dir + 3) % 4, (dir + 2) % 4} {
					if edges[next]&(1<<candidate) != 0 {
						dir, found = candidate, true
						break
					}
				}
				if !found {
					return nil, 0, fmt.Errorf("bitmap boundary has an unclosed contour")
				}
				current = next
			}
			if simplify && len(contour) > 4 && collinear(contour[len(contour)-2], contour[0], contour[1]) {
				contour = append(contour[1:len(contour)-1], contour[1])
			}
			pointCount += len(contour)
			if pointCount > MaxPoints {
				return nil, 0, fmt.Errorf("bitmap contours exceed %d-point limit", MaxPoints)
			}
			contours = append(contours, contour)
		}
	}
	return contours, pointCount, nil
}

func collinear(a, b, c point) bool {
	return a.x == b.x && b.x == c.x || a.y == b.y && b.y == c.y
}
