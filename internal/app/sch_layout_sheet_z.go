package app

import "fmt"

// Z flow is a stable shelf layout, not a Morton/Z-order space-filling curve and
// not a snake: every row reads left to right, then restarts at the left below
// the tallest member of the previous row. A closed row/page is never backfilled.
const schematicZMaxCandidates = 200000

type schematicZShelf struct {
	x, top, bottom float64
	rowOccupied    bool
	zones          []SchematicRenderZone
	boxes          []SchematicBox
}

type schematicZSearch struct{ candidates int }

func newSchematicZShelf(sheet SchematicRenderSheet) schematicZShelf {
	u := sheetPreviewUsable(sheet)
	return schematicZShelf{x: u.MinX, top: u.MaxY, bottom: u.MaxY}
}

func (s schematicZShelf) clone() schematicZShelf {
	s.zones = append([]SchematicRenderZone(nil), s.zones...)
	s.boxes = append([]SchematicBox(nil), s.boxes...)
	return s
}

// Try exactly the current shelf flow. Keepouts can advance the x cursor, or
// skip fully obstructed empty row tops on the fixed grid; they never justify
// returning to an earlier hole. Gap arithmetic includes the existing visible
// frame stroke contract (two half-strokes between zones, one at a keepout).
func (s *schematicZShelf) place(z SchematicRenderZone, sheet SchematicRenderSheet, search *schematicZSearch) (bool, error) {
	u := sheetPreviewUsable(sheet)
	w, h := sheetRelationDimensions(z)
	if !plFinite(w) || !plFinite(h) || w <= 0 || h <= 0 {
		return false, fmt.Errorf("zone %s requires a finite positive frame for Z flow", z.ID)
	}
	if w > u.MaxX-u.MinX || h > u.MaxY-u.MinY {
		return false, nil
	}
	for s.top-h >= u.MinY {
		for x := s.x; x+w <= u.MaxX; {
			if search.candidates >= schematicZMaxCandidates {
				return false, fmt.Errorf("Z-flow shelf candidate budget exhausted (%d); no capacity conclusion", schematicZMaxCandidates)
			}
			search.candidates++
			r := SchematicBox{MinX: x, MinY: s.top - h, MaxX: x + w, MaxY: s.top}
			if sheetPreviewPlacementError(z.ID, r, u, sheet, s.boxes) == nil {
				candidate := z
				candidate.SheetPosition = &SchematicSheetPosition{X: x, Y: s.top}
				s.zones = append(s.zones, candidate)
				s.boxes = append(s.boxes, r)
				if !s.rowOccupied || r.MinY < s.bottom {
					s.bottom = r.MinY
				}
				s.rowOccupied = true
				s.x = plCeil(r.MaxX + sheet.Gap + 1)
				return true, nil
			}
			// At fixed y, moving right past each intersecting obstacle is the
			// first possible way to clear it. Jumping these event edges avoids
			// scanning an entire page-width of grid points for a wide keepout.
			next := x + schAnchorGrid
			for _, k := range sheet.Keepouts {
				if sheetPreviewConflict(r, k, sheet.Gap+.5) {
					if edge := plCeil(k.MaxX + sheet.Gap + .5); edge > next {
						next = edge
					}
				}
			}
			for _, b := range s.boxes {
				if sheetPreviewConflict(r, b, sheet.Gap+1) {
					if edge := plCeil(b.MaxX + sheet.Gap + 1); edge > next {
						next = edge
					}
				}
			}
			x = next
		}
		if s.rowOccupied {
			s.top = plFloor(s.bottom - sheet.Gap - 1)
		} else {
			// There are no zones in this obstructed row. Moving its empty top
			// down cannot break top alignment or skip space behind a zone.
			s.top -= schAnchorGrid
		}
		s.x, s.bottom, s.rowOccupied = u.MinX, s.top, false
	}
	return false, nil
}

func trySchematicZGroup(base schematicZShelf, group sheetRelationGroup, sheet SchematicRenderSheet, search *schematicZSearch) (schematicZShelf, bool, error) {
	trial := base.clone()
	for _, z := range group.zones {
		ok, err := trial.place(z, sheet, search)
		if err != nil || !ok {
			return base, false, err
		}
	}
	return trial, true, nil
}

func planSchematicZSheets(in SchematicRenderInput, zones []SchematicRenderZone) ([]SchematicRenderInput, error) {
	pages := []SchematicRenderInput{}
	state := newSchematicZShelf(*in.Sheet)
	search := &schematicZSearch{}
	appendPage := func(s schematicZShelf) {
		page := in
		page.Zones = s.zones
		pages = append(pages, page)
	}
	// A relation component is atomic. Its earliest input member determines its
	// position among groups; members retain their own input order. This explicitly
	// gathers noncontiguous members (A, B, C with A~C becomes [A,C], [B]) so the
	// same-page contract cannot be broken by pagination. preferAdjacent is soft:
	// consecutive shelf placement helps it but never changes this reading order.
	for _, group := range sheetRelationGroups(zones) {
		trial, ok, err := trySchematicZGroup(state, group, *in.Sheet, search)
		if err != nil {
			return nil, err
		}
		if ok {
			state = trial
			continue
		}
		if len(state.zones) == 0 {
			return nil, fmt.Errorf("same-page group beginning with zone %s does not fit the bounded Z-flow shelf at requested padding; no scaling or splitting, no capacity conclusion", group.zones[0].ID)
		}
		// Commit the previous page only after the whole group succeeds on a new
		// page. A failed attempt leaves neither partial group nor partial output.
		fresh, ok, err := trySchematicZGroup(newSchematicZShelf(*in.Sheet), group, *in.Sheet, search)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("same-page group beginning with zone %s does not fit the bounded Z-flow shelf at requested padding; no scaling or splitting, no capacity conclusion", group.zones[0].ID)
		}
		appendPage(state)
		state = fresh
	}
	if len(state.zones) > 0 {
		appendPage(state)
	}
	return pages, nil
}

func sameSchematicSheetPositions(a, b []SchematicRenderZone) bool {
	if len(a) != len(b) {
		return false
	}
	positions := make(map[string]SchematicSheetPosition, len(a))
	for _, z := range a {
		if z.SheetPosition == nil {
			return false
		}
		positions[z.ID] = *z.SheetPosition
	}
	for _, z := range b {
		p, ok := positions[z.ID]
		if !ok || z.SheetPosition == nil || p != *z.SheetPosition {
			return false
		}
	}
	return true
}

// Rendering a declared Z page fails closed unless its supplied positions are
// exactly those of the deterministic one-page shelf. Geometry-only validation
// would let a former compact layout silently bypass the new reading contract.
func validateSchematicZSheet(in SchematicRenderInput) error {
	if len(in.Zones) > 64 {
		return fmt.Errorf("at most 64 zones per Z-flow preview")
	}
	zones := append([]SchematicRenderZone(nil), in.Zones...)
	for i := range zones {
		f, err := sheetPreviewFrame(zones[i], in.Spacing)
		if err != nil {
			return err
		}
		zones[i].Frame = &f
	}
	pages, err := planSchematicZSheets(in, zones)
	if err != nil {
		return err
	}
	if len(pages) != 1 || !sameSchematicSheetPositions(in.Zones, pages[0].Zones) {
		return fmt.Errorf("sheet.flow z requires canonical left-to-right, top-aligned rows with forward-only row/page progression; rerun layout-sheet-plan")
	}
	return nil
}
