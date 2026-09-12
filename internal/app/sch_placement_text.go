package app

import "fmt"

// Explicit text boxes belong to the measured pose, not to an inferred full-height
// right-side band. Keep the latter as a fallback estimate, never a wire obstacle.
func validatePlacementText(p *powerLayoutPlan, sheet layoutBBox) error {
	hasText := false
	for _, c := range p.Placements {
		hasText = hasText || len(c.TextBBoxes) > 0
	}
	if !hasText {
		return nil
	}
	segments, err := schTerminalSegments(p)
	if err != nil {
		return err
	}
	for i, c := range p.Placements {
		for _, box := range c.TextBBoxes {
			if !plBoxValid(box) || !boxInside(box, sheet) {
				return fmt.Errorf("%s invalid/out-of-sheet text bbox", c.Designator)
			}
			for j, other := range p.Placements {
				if j != i && boxesGapOverlap(box, other.BBox, 5) {
					return fmt.Errorf("text collision: %s label / %s body", c.Designator, other.Designator)
				}
				if j < i {
					for _, b := range other.TextBBoxes {
						if boxesGapOverlap(box, b, 5) {
							return fmt.Errorf("text collision: %s / %s labels", c.Designator, other.Designator)
						}
					}
				}
			}
			for _, w := range segments {
				if plSegmentBox(w.Points[0], w.Points[1], box) {
					return fmt.Errorf("text collision: %s wire / %s label", w.Net, c.Designator)
				}
			}
			for _, f := range p.Flags {
				for _, b := range schTerminalMarkerBoxes(f) {
					if boxesGapOverlap(box, b, 5) {
						return fmt.Errorf("text collision: %s marker / %s label", f.Net, c.Designator)
					}
				}
			}
		}
	}
	return nil
}
