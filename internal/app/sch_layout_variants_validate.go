package app

import (
	"fmt"
	"math"
	"reflect"
	"strings"
)

// Alternatives are untrusted geometry, not solver receipts. Validate every
// candidate, including those which will not be selected, before page planning.
// A diagnostic render must not turn a blocked alternative into an accepted one.
func validateSchematicZoneVariants(in SchematicRenderInput) error {
	for _, z := range in.Zones {
		if len(z.Variants) == 0 {
			continue // Preserve the established non-variant validation contract.
		}
		if len(z.Variants) > 4 {
			return fmt.Errorf("zone %s requires 1..4 variants", z.ID)
		}
		if z.Layout == nil || z.Frame == nil || z.ContentBounds == nil || strings.TrimSpace(z.CoreComponentID) == "" {
			return fmt.Errorf("zone %s variants require main layout, frame, contentBounds and coreComponentId", z.ID)
		}
		if len(z.Layout.Variants) != 0 {
			return fmt.Errorf("zone %s forbids nested layout variants", z.ID)
		}
		if err := validateSchematicVariantGeometry(in, z, z.Layout, *z.Frame, *z.ContentBounds); err != nil {
			return fmt.Errorf("zone %s main variant: %w", z.ID, err)
		}
		ids := map[string]bool{}
		matched := false
		for _, v := range z.Variants {
			if strings.TrimSpace(v.ID) == "" || ids[v.ID] {
				return fmt.Errorf("zone %s variant IDs must be nonempty and unique", z.ID)
			}
			ids[v.ID] = true
			if v.Layout == nil || len(v.Layout.Variants) != 0 {
				return fmt.Errorf("zone %s variant %s requires a layout without nested variants", z.ID, v.ID)
			}
			if err := validateSchematicVariantGeometry(in, z, v.Layout, v.Frame, v.ContentBounds); err != nil {
				return fmt.Errorf("zone %s variant %s: %w", z.ID, v.ID, err)
			}
			if err := validateSchematicVariantPreservation(z, v.Layout); err != nil {
				return fmt.Errorf("zone %s variant %s: %w", z.ID, v.ID, err)
			}
			if (z.SelectedVariantID == "" || z.SelectedVariantID == v.ID) &&
				schematicVariantLayoutGeometryEqual(z.Layout, v.Layout) &&
				reflect.DeepEqual(*z.Frame, v.Frame) && schematicVariantBoxEqual(*z.ContentBounds, v.ContentBounds) {
				matched = true
			}
		}
		if !matched {
			return fmt.Errorf("zone %s main geometry must match its selected variant, or one listed variant when selectedVariantId is omitted", z.ID)
		}
		// variants[0] retains the original physical drawing. A same-name marker
		// can name an island, but must not excuse severing its former short wire.
		baseline := z.Variants[0].Layout
		if err := validateSchematicVariantConnectivityPreserved(baseline, z.Layout); err != nil {
			return fmt.Errorf("zone %s main variant: %w", z.ID, err)
		}
		for _, v := range z.Variants {
			if err := validateSchematicVariantConnectivityPreserved(baseline, v.Layout); err != nil {
				return fmt.Errorf("zone %s variant %s: %w", z.ID, v.ID, err)
			}
		}
	}
	return nil
}

type schematicVariantPinIdentity struct{ component, number string }
type schematicVariantPinIsland struct {
	net  string
	root int
}

type schematicVariantPhysicalPin struct{ Designator, Number string }
type schematicVariantPhysicalIsland struct {
	Net  string
	Pins []schematicVariantPhysicalPin
}

// Preserve source component/pin traversal order for deterministic local reroutes.
func schematicVariantPhysicalIslands(layout *SchematicLayoutResult) ([]schematicVariantPhysicalIsland, error) {
	pins, err := schematicVariantPhysicalPinIslands(layout)
	if err != nil {
		return nil, err
	}
	var islands []schematicVariantPhysicalIsland
	indices := map[int]int{}
	for _, c := range layout.Placements {
		for _, p := range c.Pins {
			if p.Net == "" {
				continue
			}
			island := pins[schematicVariantPinIdentity{layout.ComponentIDs[c.Designator], p.Number}]
			index, exists := indices[island.root]
			if !exists {
				index = len(islands)
				indices[island.root] = index
				islands = append(islands, schematicVariantPhysicalIsland{Net: p.Net})
			}
			islands[index].Pins = append(islands[index].Pins, schematicVariantPhysicalPin{Designator: c.Designator, Number: p.Number})
		}
	}
	return islands, nil
}

// Same net names are not physical connectivity. Preserve all original pin-pair
// connections through real wire/marker-lead islands while allowing same-net
// islands to merge. This is shared with local candidate generation so bad
// alternatives are discarded before they reach the zone wrapper.
func validateSchematicVariantConnectivityPreserved(baseline, candidate *SchematicLayoutResult) error {
	before, err := schematicVariantPhysicalPinIslands(baseline)
	if err != nil {
		return fmt.Errorf("baseline physical connectivity: %w", err)
	}
	after, err := schematicVariantPhysicalPinIslands(candidate)
	if err != nil {
		return fmt.Errorf("candidate physical connectivity: %w", err)
	}
	targets := map[int]schematicVariantPinIsland{}
	for _, component := range baseline.Placements {
		for _, pin := range component.Pins {
			if pin.Net == "" {
				continue
			}
			id := schematicVariantPinIdentity{baseline.ComponentIDs[component.Designator], pin.Number}
			original := before[id]
			current, exists := after[id]
			if !exists || current.net != original.net {
				return fmt.Errorf("pin %s.%s changed or disappeared from physical connectivity", component.Designator, pin.Number)
			}
			if first, seen := targets[original.root]; seen && first != current {
				return fmt.Errorf("net %s original direct wire island was split at %s.%s; same-name labels cannot replace that physical connection", pin.Net, component.Designator, pin.Number)
			}
			targets[original.root] = current
		}
	}
	return nil
}

func schematicVariantPhysicalPinIslands(layout *SchematicLayoutResult) (map[schematicVariantPinIdentity]schematicVariantPinIsland, error) {
	if layout == nil {
		return nil, fmt.Errorf("layout required")
	}
	p := powerLayoutPlan{Placements: layout.Placements, Wires: layout.Wires, Flags: layout.Flags}
	segments, err := schTerminalSegments(&p)
	if err != nil {
		return nil, err
	}
	parent := make([]int, len(segments))
	for i := range parent {
		parent[i] = i
	}
	var root func(int) int
	root = func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	for i, segment := range segments {
		for j, other := range segments[:i] {
			if plSegmentsMeet(segment.Points[0], segment.Points[1], other.Points[0], other.Points[1]) {
				if segment.Net != other.Net {
					return nil, fmt.Errorf("foreign nets physically intersect: %s/%s", segment.Net, other.Net)
				}
				parent[root(i)] = root(j)
			}
		}
	}
	pins := map[schematicVariantPinIdentity]schematicVariantPinIsland{}
	nextIsolated := len(segments)
	for _, c := range layout.Placements {
		stableID := layout.ComponentIDs[c.Designator]
		if stableID == "" {
			return nil, fmt.Errorf("component %s lacks stable identity", c.Designator)
		}
		for _, pin := range c.Pins {
			if pin.Net == "" {
				continue
			}
			id := schematicVariantPinIdentity{stableID, pin.Number}
			if _, exists := pins[id]; pin.Number == "" || exists || !plGrid(pin.X) || !plGrid(pin.Y) {
				return nil, fmt.Errorf("invalid/duplicate physical pin %s.%s", c.Designator, pin.Number)
			}
			island := nextIsolated
			nextIsolated++ // Merely overlapping two pins without a wire is not a connection.
			for i, segment := range segments {
				if plOnSegment([2]float64{pin.X, pin.Y}, segment.Points[0], segment.Points[1]) {
					if segment.Net != pin.Net {
						return nil, fmt.Errorf("wire touches foreign pin %s.%s", c.Designator, pin.Number)
					}
					island = root(i)
				}
			}
			pins[id] = schematicVariantPinIsland{net: pin.Net, root: island}
		}
	}
	return pins, nil
}

func validateSchematicVariantGeometry(in SchematicRenderInput, owner SchematicRenderZone, layout *SchematicLayoutResult, frame schFrameSpec, bounds SchematicBox) error {
	if layout == nil || layout.SchemaVersion != 1 || len(layout.Placements) == 0 {
		return fmt.Errorf("complete schemaVersion:1 layout required")
	}
	if frame.ID != owner.ID || frame.Title != owner.Title {
		return fmt.Errorf("frame must preserve zone ID and title")
	}
	if err := (schFrameDocument{SchemaVersion: 1, DocumentID: "offline-variant", Frames: []schFrameSpec{frame}}).validate(); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, c := range layout.Placements {
		id := layout.ComponentIDs[c.Designator]
		if strings.TrimSpace(id) == "" || seen[id] || !plGrid(c.X) || !plGrid(c.Y) || !schematicVariantRotationValid(c.Rotation) {
			return fmt.Errorf("component %s requires unique stable identity and grid-aligned quarter-turn geometry", c.Designator)
		}
		seen[id] = true
		pinNumbers := map[string]bool{}
		for _, p := range c.Pins {
			pinNumbers[p.Number] = true
		}
		for n := range layout.PinStates[id] {
			if !pinNumbers[n] {
				return fmt.Errorf("component %s has state for unknown pin %s", c.Designator, n)
			}
		}
	}
	if !seen[owner.CoreComponentID] || len(layout.ComponentIDs) != len(seen) {
		return fmt.Errorf("component identity coverage or coreComponentId is invalid")
	}
	for id := range layout.PinStates {
		if !seen[id] {
			return fmt.Errorf("pinStates contains unknown component %s", id)
		}
	}
	for id, rotations := range layout.AllowedRotations {
		if !seen[id] || len(rotations) == 0 || len(rotations) > 4 {
			return fmt.Errorf("allowedRotations requires a known component and 1..4 quarter-turn angles: %s", id)
		}
		angles := map[float64]bool{}
		for _, angle := range rotations {
			if !schematicVariantRotationValid(angle) || angle < 0 || angle >= 360 || angles[schematicVariantRotation(angle)] {
				return fmt.Errorf("allowedRotations contains invalid/duplicate quarter-turn angle for %s", id)
			}
			angles[schematicVariantRotation(angle)] = true
		}
	}
	// Detach only the wrapper. The validators below are read-only; clearing the
	// alternatives prevents recursion when called by Render or the complete gate.
	single := owner
	single.Variants, single.SelectedVariantID = nil, ""
	single.Placement, single.SheetPosition = nil, nil
	single.Layout, single.Frame, single.ContentBounds = layout, &frame, &bounds
	preview := SchematicRenderInput{SchemaVersion: 1, Zones: []SchematicRenderZone{single}, Spacing: in.Spacing}
	if _, err := RenderSchematicLayoutSVG(preview); err != nil {
		return err
	}
	if err := validateCompleteLayoutPreview(preview); err != nil {
		return err
	}
	p := powerLayoutPlan{Placements: layout.Placements, Wires: layout.Wires, Flags: layout.Flags}
	obstacles := powerLayoutContentObstacles(&p)
	actual := powerLayoutContentBounds(&p)
	if !schematicVariantBoxEqual(bounds, actual) {
		return fmt.Errorf("contentBounds disagrees with recomputed geometry")
	}
	// Never rely on saved obstacle/width caches to prove that a title is clear.
	title := layoutBBox{MinX: frame.TitleX, MinY: frame.TitleY - frame.FontSize, MaxX: frame.TitleX + schModuleTitleWidth(frame.Title, frame.FontSize), MaxY: frame.TitleY}
	if frame.TitleLayout != nil {
		if frame.TitleLayout.Width < title.MaxX-title.MinX || frame.TitleLayout.Clearance < schModuleTitleClearance {
			return fmt.Errorf("frame title cache understates text width or clearance")
		}
		title = frame.titleBounds()
	}
	if !boxInside(title, frame.Rect) {
		return fmt.Errorf("frame clips its recomputed title envelope")
	}
	for _, box := range obstacles {
		if sheetPreviewConflict(title, box, schModuleTitleClearance) {
			return fmt.Errorf("frame title collides with current variant content")
		}
	}
	return nil
}

func validateSchematicVariantPreservation(z SchematicRenderZone, alternative *SchematicLayoutResult) error {
	base := z.Layout
	if !reflect.DeepEqual(base.ComponentIDs, alternative.ComponentIDs) || !reflect.DeepEqual(base.PinStates, alternative.PinStates) {
		return fmt.Errorf("variant changed component identities or pin net/NC states")
	}
	if !reflect.DeepEqual(base.AllowedRotations, alternative.AllowedRotations) {
		return fmt.Errorf("variant changed allowedRotations authority")
	}
	byRef := map[string]SchematicPlacement{}
	for _, c := range base.Placements {
		byRef[c.Designator] = c
	}
	if len(base.Placements) != len(alternative.Placements) {
		return fmt.Errorf("variant added or removed components")
	}
	for _, c := range alternative.Placements {
		original, exists := byRef[c.Designator]
		if !exists || c.PrimitiveID != original.PrimitiveID || c.Value != original.Value || c.Mirror != original.Mirror || len(c.Pins) != len(original.Pins) {
			return fmt.Errorf("component %s changed identity, value, mirror or pin coverage", c.Designator)
		}
		id := base.ComponentIDs[c.Designator]
		if id == z.CoreComponentID && (c.X != original.X || c.Y != original.Y || schematicVariantRotation(c.Rotation) != schematicVariantRotation(original.Rotation)) {
			return fmt.Errorf("core component %s changed its fixed anchor or pose", c.Designator)
		}
		rotation := schematicVariantRotation(c.Rotation)
		allowed := rotation == schematicVariantRotation(original.Rotation)
		if rotations, explicit := base.AllowedRotations[id]; explicit {
			allowed = false
			for _, angle := range rotations {
				allowed = allowed || rotation == schematicVariantRotation(angle)
			}
		}
		if !allowed {
			return fmt.Errorf("component %s rotation %g is not authorized by allowedRotations", c.Designator, c.Rotation)
		}
		quarters := int(schematicVariantRotation(c.Rotation-original.Rotation) / 90)
		expected := plTranslate(plRotate(original, quarters), c.X-original.X, c.Y-original.Y)
		if !schematicVariantBoxEqual(expected.BBox, c.BBox) || len(expected.TextBBoxes) != len(c.TextBBoxes) {
			return fmt.Errorf("component %s bbox is not a rigid quarter-turn transform", c.Designator)
		}
		for i, box := range expected.TextBBoxes {
			if !schematicVariantBoxEqual(box, c.TextBBoxes[i]) {
				return fmt.Errorf("component %s text bbox is not the same rigid transform", c.Designator)
			}
		}
		pins := map[string]SchematicPin{}
		for _, pin := range expected.Pins {
			pins[pin.Number] = pin
		}
		for _, pin := range c.Pins {
			p, exists := pins[pin.Number]
			if !exists || p.Name != pin.Name || p.Net != pin.Net || !schematicVariantNumberEqual(p.X, pin.X) || !schematicVariantNumberEqual(p.Y, pin.Y) {
				return fmt.Errorf("component %s pin %s changed name/net/coverage or rigid geometry", c.Designator, pin.Number)
			}
		}
	}
	return nil
}

func schematicVariantRotationValid(angle float64) bool {
	return plFinite(angle) && math.Mod(angle, 90) == 0
}

func schematicVariantRotation(angle float64) float64 {
	return math.Mod(math.Mod(angle, 360)+360, 360)
}

func schematicVariantNumberEqual(a, b float64) bool {
	return plFinite(a) && plFinite(b) && math.Abs(a-b) < 1e-7
}

func schematicVariantBoxEqual(a, b SchematicBox) bool {
	return plBoxValid(a) && plBoxValid(b) && schematicVariantNumberEqual(a.MinX, b.MinX) && schematicVariantNumberEqual(a.MinY, b.MinY) && schematicVariantNumberEqual(a.MaxX, b.MaxX) && schematicVariantNumberEqual(a.MaxY, b.MaxY)
}

// Solver score/search counters are diagnostics, never geometric certification.
func schematicVariantLayoutGeometryEqual(a, b *SchematicLayoutResult) bool {
	return a != nil && b != nil && a.SchemaVersion == b.SchemaVersion &&
		reflect.DeepEqual(a.ComponentIDs, b.ComponentIDs) && reflect.DeepEqual(a.PinStates, b.PinStates) &&
		reflect.DeepEqual(a.Placements, b.Placements) && reflect.DeepEqual(a.Wires, b.Wires) &&
		reflect.DeepEqual(a.Flags, b.Flags) && reflect.DeepEqual(a.AllowedRotations, b.AllowedRotations)
}
