package app

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Relations belong to sheet placement, not to electrical ownership or nesting.
// Checking a single page with the same validator also enforces reference
// closure: a samePageAs target omitted from that page cannot be ignored.
func validateSheetZoneRelations(zones []SchematicRenderZone) error {
	if err := validateSchematicRenderPlacements(zones); err != nil {
		return fmt.Errorf("sheet same-page relations: %w", err)
	}
	return nil
}

func sheetHasZoneRelations(zones []SchematicRenderZone) bool {
	for _, z := range zones {
		if z.Placement != nil {
			return true
		}
	}
	return false
}

type sheetRelationGroup struct {
	zones []SchematicRenderZone
}

// Undirected connected components make same-page relations transitive and
// allow cycles. Input order is the stable tie breaker, never map iteration.
func sheetRelationGroups(zones []SchematicRenderZone) []sheetRelationGroup {
	parent := make([]int, len(zones))
	index := map[string]int{}
	for i, z := range zones {
		parent[i], index[z.ID] = i, i
	}
	var root func(int) int
	root = func(i int) int {
		if parent[i] != i {
			parent[i] = root(parent[i])
		}
		return parent[i]
	}
	for i, z := range zones {
		if z.Placement != nil {
			a, b := root(i), root(index[z.Placement.SamePageAs])
			if a > b {
				a, b = b, a
			}
			parent[b] = a
		}
	}
	groups := []sheetRelationGroup{}
	groupIndex := map[int]int{}
	for i, z := range zones {
		r := root(i)
		g, ok := groupIndex[r]
		if !ok {
			g = len(groups)
			groupIndex[r] = g
			groups = append(groups, sheetRelationGroup{})
		}
		groups[g].zones = append(groups[g].zones, z)
	}
	return groups
}

func sheetRelationDimensions(z SchematicRenderZone) (float64, float64) {
	r := z.Frame.Rect
	return r.MaxX - r.MinX, r.MaxY - r.MinY
}

// Score every declared soft edge once. Edge-to-edge distance comes first;
// center distance breaks equal edge gaps without preferring a remote corner.
func sheetRelationDistance(a, b SchematicBox) (float64, float64) {
	dx := math.Max(0, math.Max(a.MinX-b.MaxX, b.MinX-a.MaxX))
	dy := math.Max(0, math.Max(a.MinY-b.MaxY, b.MinY-a.MaxY))
	cx := (a.MinX + a.MaxX - b.MinX - b.MaxX) / 2
	cy := (a.MinY + a.MaxY - b.MinY - b.MaxY) / 2
	return math.Hypot(dx, dy), math.Hypot(cx, cy)
}

func sheetRelationScore(zones []SchematicRenderZone) [2]float64 {
	boxes := map[string]SchematicBox{}
	for _, z := range zones {
		if z.SheetPosition != nil && z.Frame != nil {
			w, h := sheetRelationDimensions(z)
			boxes[z.ID] = SchematicBox{MinX: z.SheetPosition.X, MinY: z.SheetPosition.Y - h, MaxX: z.SheetPosition.X + w, MaxY: z.SheetPosition.Y}
		}
	}
	var score [2]float64
	for _, z := range zones {
		if z.Placement == nil || !z.Placement.PreferAdjacent {
			continue
		}
		a, aOK := boxes[z.ID]
		b, bOK := boxes[z.Placement.SamePageAs]
		if aOK && bOK {
			d, c := sheetRelationDistance(a, b)
			score[0], score[1] = score[0]+d, score[1]+c
		}
	}
	return score
}

// With the same nearest legal edge gap, favor a compact group envelope before
// center distance. Otherwise a narrow satellite may be centered below a wide
// host and consume more page area than a side-by-side arrangement.
func sheetRelationPackingScore(zones []SchematicRenderZone) [3]float64 {
	distance := sheetRelationScore(zones)
	boxes := make([]SchematicBox, 0, len(zones))
	for _, z := range zones {
		if z.Frame == nil || z.SheetPosition == nil {
			continue
		}
		w, h := sheetRelationDimensions(z)
		boxes = append(boxes, SchematicBox{MinX: z.SheetPosition.X, MinY: z.SheetPosition.Y - h, MaxX: z.SheetPosition.X + w, MaxY: z.SheetPosition.Y})
	}
	area := 0.0
	if len(boxes) > 0 {
		b := schBoundsUnion(boxes)
		area = (b.MaxX - b.MinX) * (b.MaxY - b.MinY)
	}
	return [3]float64{distance[0], area, distance[1]}
}

func sheetRelationPackingScoreLess(a, b [3]float64) bool {
	for i := range a {
		if math.Abs(a[i]-b[i]) > 1e-8 {
			return a[i] < b[i]
		}
	}
	return false
}

type sheetRelationCandidate struct {
	position SchematicSheetPosition
	score    [3]float64
}

type sheetRelationSearch struct {
	// Hard bounds apply to each atomic group/page attempt, across all orders.
	// A feasible result may be returned before optimality; a failed bounded
	// search is explicitly not a geometric impossibility proof.
	tests, states, solutions int
	limited                  bool
}

const (
	sheetRelationMaxTests     = 200000
	sheetRelationMaxStates    = 4096
	sheetRelationMaxSolutions = 64
	sheetRelationAlternatives = 8
	sheetRelationPageBeam     = 8
)

func (s *sheetRelationSearch) stopped() bool {
	return s.tests >= sheetRelationMaxTests || s.states >= sheetRelationMaxStates || s.solutions >= sheetRelationMaxSolutions
}

func sheetRelationCandidates(z SchematicRenderZone, placed []SchematicRenderZone, sheet SchematicRenderSheet, search *sheetRelationSearch, groupIDs ...map[string]bool) []sheetRelationCandidate {
	u := sheetPreviewUsable(sheet)
	w, h := sheetRelationDimensions(z)
	xs, ys := map[float64]bool{}, map[float64]bool{}
	addX := func(x float64) { xs[plFloor(x)], xs[plCeil(x)] = true, true }
	addY := func(y float64) { ys[plFloor(y)], ys[plCeil(y)] = true, true }
	addX(u.MinX)
	addX(u.MaxX - w)
	addY(u.MaxY)
	addY(u.MinY + h)
	boxes := make([]SchematicBox, 0, len(placed))
	edges := func(b SchematicBox, gap float64) {
		for _, x := range []float64{b.MinX - w - gap, b.MaxX + gap, b.MinX, b.MaxX - w, (b.MinX + b.MaxX - w) / 2} {
			addX(x)
		}
		for _, y := range []float64{b.MaxY + h + gap, b.MinY - gap, b.MaxY, b.MinY + h, (b.MinY + b.MaxY + h) / 2} {
			addY(y)
		}
	}
	for _, k := range sheet.Keepouts {
		edges(k, sheet.Gap+.5)
	}
	for _, p := range placed {
		pw, ph := sheetRelationDimensions(p)
		b := SchematicBox{MinX: p.SheetPosition.X, MinY: p.SheetPosition.Y - ph, MaxX: p.SheetPosition.X + pw, MaxY: p.SheetPosition.Y}
		boxes = append(boxes, b)
		edges(b, sheet.Gap+1)
	}
	xValues, yValues := []float64{}, []float64{}
	for x := range xs {
		xValues = append(xValues, x)
	}
	for y := range ys {
		yValues = append(yValues, y)
	}
	sort.Float64s(xValues)
	sort.Sort(sort.Reverse(sort.Float64Slice(yValues)))
	candidates := []sheetRelationCandidate{}
	for _, y := range yValues {
		for _, x := range xValues {
			if search.stopped() {
				search.limited = true
				break
			}
			search.tests++
			r := SchematicBox{MinX: x, MinY: y - h, MaxX: x + w, MaxY: y}
			if sheetPreviewPlacementError(z.ID, r, u, sheet, boxes) != nil {
				continue
			}
			candidate := sheetRelationCandidate{position: SchematicSheetPosition{X: x, Y: y}}
			groupBoxes := []SchematicBox{r}
			// Honor both outgoing and incoming preferences. This remains useful
			// even when a cyclic relation makes strict host-first impossible.
			for i, p := range placed {
				linked := (z.Placement != nil && z.Placement.SamePageAs == p.ID) || (p.Placement != nil && p.Placement.SamePageAs == z.ID)
				if (len(groupIDs) == 0 && linked) || (len(groupIDs) > 0 && groupIDs[0][p.ID]) {
					groupBoxes = append(groupBoxes, boxes[i])
				}
				if (z.Placement != nil && z.Placement.PreferAdjacent && z.Placement.SamePageAs == p.ID) || (p.Placement != nil && p.Placement.PreferAdjacent && p.Placement.SamePageAs == z.ID) {
					d, c := sheetRelationDistance(r, boxes[i])
					candidate.score[0] += d
					candidate.score[2] += c
				}
			}
			bounds := schBoundsUnion(groupBoxes)
			candidate.score[1] = (bounds.MaxX - bounds.MinX) * (bounds.MaxY - bounds.MinY)
			candidates = append(candidates, candidate)
		}
		if search.limited {
			break
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if sheetRelationPackingScoreLess(candidates[i].score, candidates[j].score) {
			return true
		}
		if sheetRelationPackingScoreLess(candidates[j].score, candidates[i].score) {
			return false
		}
		if candidates[i].position.Y != candidates[j].position.Y {
			return candidates[i].position.Y > candidates[j].position.Y
		}
		return candidates[i].position.X < candidates[j].position.X
	})
	if len(groupIDs) > 0 && len(groupIDs[0]) > 1 {
		// Fairly expose different relative directions before a bounded DFS can
		// spend all complete-solution slots on nearby variants of one shape.
		buckets := [][]sheetRelationCandidate{}
		classes := map[string]int{}
		for _, candidate := range candidates {
			member := z
			position := candidate.position
			member.SheetPosition = &position
			members := []SchematicRenderZone{member}
			for _, p := range placed {
				if groupIDs[0][p.ID] {
					members = append(members, p)
				}
			}
			class := sheetRelationAlignmentClass(members)
			i, ok := classes[class]
			if !ok {
				i = len(buckets)
				classes[class] = i
				buckets = append(buckets, nil)
			}
			buckets[i] = append(buckets[i], candidate)
		}
		diverse := make([]sheetRelationCandidate, 0, len(candidates))
		for round := 0; len(diverse) < len(candidates); round++ {
			for _, bucket := range buckets {
				if round < len(bucket) {
					diverse = append(diverse, bucket[round])
				}
			}
		}
		return diverse
	}
	return candidates
}

// Search all group members together. A dead end unwinds earlier positions,
// including the host rectangle, before abandoning this page. No partial group
// is ever committed to the page and local electrical geometry is read-only.
func placeSheetRelationGroupCandidates(group sheetRelationGroup, page SchematicRenderInput) ([][]SchematicRenderZone, bool) {
	search := &sheetRelationSearch{}
	var solutions [][]SchematicRenderZone
	seen := map[string]bool{}
	incoming := map[string]int{}
	groupIDs := map[string]bool{}
	for _, z := range group.zones {
		groupIDs[z.ID] = true
		if z.Placement != nil {
			incoming[z.Placement.SamePageAs]++
		}
	}
	for order := 0; order < 4 && !search.stopped(); order++ {
		work := append([]SchematicRenderZone(nil), group.zones...)
		metric := func(z SchematicRenderZone) float64 {
			w, h := sheetRelationDimensions(z)
			switch order {
			case 2:
				return w
			case 3:
				return h
			default:
				return w * h
			}
		}
		sort.SliceStable(work, func(i, j int) bool {
			if order == 0 && incoming[work[i].ID] != incoming[work[j].ID] {
				return incoming[work[i].ID] > incoming[work[j].ID]
			}
			return metric(work[i]) > metric(work[j])
		})
		var visit func(int, []SchematicRenderZone)
		visit = func(depth int, selected []SchematicRenderZone) {
			if search.stopped() {
				search.limited = true
				return
			}
			search.states++
			if depth == len(work) {
				search.solutions++
				key := sheetRelationPlacementKey(selected)
				if !seen[key] {
					seen[key] = true
					solutions = append(solutions, append([]SchematicRenderZone(nil), selected...))
				}
				return
			}
			placed := append(append([]SchematicRenderZone(nil), page.Zones...), selected...)
			for _, candidate := range sheetRelationCandidates(work[depth], placed, *page.Sheet, search, groupIDs) {
				if search.stopped() {
					search.limited = true
					return
				}
				z := work[depth]
				position := candidate.position
				z.SheetPosition = &position
				visit(depth+1, append(append([]SchematicRenderZone(nil), selected...), z))
			}
		}
		visit(0, nil)
	}
	if len(solutions) == 0 {
		return nil, search.limited
	}
	sort.SliceStable(solutions, func(i, j int) bool {
		return sheetRelationPackingScoreLess(sheetRelationPackingScore(solutions[i]), sheetRelationPackingScore(solutions[j]))
	})
	// Keep alignment/orientation diversity, not eight almost-identical centered
	// variants. A top-aligned satellite leaves a whole free rectangle where a
	// centered local optimum would fragment that space for later groups.
	chosen, classes, positions := [][]SchematicRenderZone{}, map[string]bool{}, map[string]bool{}
	add := func(solution []SchematicRenderZone) {
		byID := map[string]SchematicRenderZone{}
		for _, z := range solution {
			byID[z.ID] = z
		}
		result := make([]SchematicRenderZone, 0, len(solution))
		for _, z := range group.zones {
			result = append(result, byID[z.ID])
		}
		chosen = append(chosen, result)
		positions[sheetRelationPlacementKey(solution)] = true
	}
	for _, solution := range solutions {
		class := sheetRelationAlignmentClass(solution)
		if !classes[class] {
			add(solution)
			classes[class] = true
		}
		if len(chosen) >= sheetRelationAlternatives || len(group.zones) == 1 {
			return chosen, search.limited
		}
	}
	for _, solution := range solutions {
		if !positions[sheetRelationPlacementKey(solution)] {
			add(solution)
		}
		if len(chosen) >= sheetRelationAlternatives {
			break
		}
	}
	return chosen, search.limited
}

func sheetRelationPlacementKey(zones []SchematicRenderZone) string {
	work := append([]SchematicRenderZone(nil), zones...)
	sort.Slice(work, func(i, j int) bool { return work[i].ID < work[j].ID })
	var key strings.Builder
	for _, z := range work {
		fmt.Fprintf(&key, "%q@%g,%g;", z.ID, z.SheetPosition.X, z.SheetPosition.Y)
	}
	return key.String()
}

func sheetRelationAlignmentClass(zones []SchematicRenderZone) string {
	boxes := map[string]SchematicBox{}
	for _, z := range zones {
		w, h := sheetRelationDimensions(z)
		boxes[z.ID] = SchematicBox{z.SheetPosition.X, z.SheetPosition.Y - h, z.SheetPosition.X + w, z.SheetPosition.Y}
	}
	work := append([]SchematicRenderZone(nil), zones...)
	sort.Slice(work, func(i, j int) bool { return work[i].ID < work[j].ID })
	var class strings.Builder
	for _, z := range work {
		if z.Placement == nil {
			continue
		}
		a := boxes[z.ID]
		b, ok := boxes[z.Placement.SamePageAs]
		if !ok {
			continue
		}
		direction, alignment := "vertical", "middle"
		if a.MinX >= b.MaxX || a.MaxX <= b.MinX {
			direction = "right"
			if a.MaxX <= b.MinX {
				direction = "left"
			}
			if a.MaxY == b.MaxY {
				alignment = "top"
			} else if a.MinY == b.MinY {
				alignment = "bottom"
			}
		} else {
			direction = "above"
			if a.MaxY <= b.MinY {
				direction = "below"
			}
			if a.MinX == b.MinX {
				alignment = "left"
			} else if a.MaxX == b.MaxX {
				alignment = "right"
			}
		}
		fmt.Fprintf(&class, "%q:%s-%s;", z.ID, direction, alignment)
	}
	return class.String()
}

func planSchematicRelatedSheets(in SchematicRenderInput, zones []SchematicRenderZone) ([]SchematicRenderInput, error) {
	groups := sheetRelationGroups(zones)
	var best []SchematicRenderInput
	var bestScore [3]float64
	var lastErr error
orderLoop:
	for order := 0; order < 4; order++ {
		work := append([]sheetRelationGroup(nil), groups...)
		metric := func(g sheetRelationGroup) float64 {
			value := 0.0
			for _, z := range g.zones {
				w, h := sheetRelationDimensions(z)
				switch order {
				case 1:
					value = math.Max(value, h)
				case 2:
					value = math.Max(value, w)
				default:
					value += w * h
				}
			}
			return value
		}
		if order > 0 {
			sort.SliceStable(work, func(i, j int) bool { return metric(work[i]) > metric(work[j]) })
		}
		beam := [][]SchematicRenderInput{{}}
		for _, group := range work {
			next := [][]SchematicRenderInput{}
			for _, pages := range beam {
				for p := 0; p <= len(pages); p++ {
					fresh := p == len(pages)
					page := SchematicRenderInput{SchemaVersion: 1, Title: in.Title, Spacing: in.Spacing, Sheet: in.Sheet, Diagnostic: in.Diagnostic}
					if !fresh {
						page = pages[p]
					}
					alternatives, limited := placeSheetRelationGroupCandidates(group, page)
					if len(alternatives) > 0 {
						for _, placed := range alternatives {
							trialPage := page
							trialPage.Zones = append(append([]SchematicRenderZone(nil), page.Zones...), placed...)
							if err := validateSchematicSheet(trialPage); err != nil {
								return nil, err
							}
							trial := append([]SchematicRenderInput(nil), pages...)
							if fresh {
								trial = append(trial, trialPage)
							} else {
								trial[p] = trialPage
							}
							next = append(next, trial)
						}
						// First-fit page selection is retained for each branch;
						// only complete alternative group shapes branch the beam.
						break
					}
					if fresh {
						ids := make([]string, 0, len(group.zones))
						for _, z := range group.zones {
							ids = append(ids, z.ID)
						}
						lastErr = fmt.Errorf("same-page group [%s]: bounded rectangle search found no complete placement (budgetLimited=%t; limits=%d rectangle tests/%d states/%d complete candidates per group/page); not a capacity proof, no partial output", strings.Join(ids, ", "), limited, sheetRelationMaxTests, sheetRelationMaxStates, sheetRelationMaxSolutions)
					}
				}
			}
			if len(next) == 0 {
				continue orderLoop
			}
			sort.SliceStable(next, func(i, j int) bool {
				if len(next[i]) != len(next[j]) {
					return len(next[i]) < len(next[j])
				}
				return sheetRelationPackingScoreLess(sheetRelationPagesScore(next[i]), sheetRelationPagesScore(next[j]))
			})
			beam = nil
			seen := map[string]bool{}
			for _, candidate := range next {
				var key strings.Builder
				for _, p := range candidate {
					key.WriteString(sheetRelationPlacementKey(p.Zones))
					key.WriteByte('|')
				}
				if !seen[key.String()] {
					seen[key.String()] = true
					beam = append(beam, candidate)
				}
				if len(beam) >= sheetRelationPageBeam {
					break
				}
			}
		}
		for _, pages := range beam {
			score := sheetRelationPagesScore(pages)
			if best == nil || len(pages) < len(best) || (len(pages) == len(best) && sheetRelationPackingScoreLess(score, bestScore)) {
				best, bestScore = pages, score
			}
		}
	}
	if best == nil {
		return nil, lastErr
	}
	return best, nil
}

func sheetRelationPagesScore(pages []SchematicRenderInput) [3]float64 {
	var score [3]float64
	for _, p := range pages {
		for _, g := range sheetRelationGroups(p.Zones) {
			s := sheetRelationPackingScore(g.zones)
			for i := range score {
				score[i] += s[i]
			}
		}
	}
	return score
}
