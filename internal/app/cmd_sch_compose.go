package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zhoushoujianwork/easyeda-agent/internal/connectivity"
)

// Composition is a presentation layer over the canonical electrical IR. It
// accepts authored/measured module geometry; it never guesses missing circuits.
type schCompositionModule struct {
	ID           string                   `json:"id"`
	Title        string                   `json:"title"`
	TitleMetrics *schTitleMetrics         `json:"titleMetrics,omitempty"`
	Placements   []powerLayoutPlacement   `json:"placements"`
	Wires        []powerLayoutWire        `json:"wires"`
	Flags        []powerLayoutFlag        `json:"flags"`
	Terminals    []schCompositionTerminal `json:"terminals,omitempty"`
}
type schCompositionSource struct {
	SchemaVersion int                    `json:"schemaVersion"`
	Connectivity  connectivity.Document  `json:"connectivity"`
	Sheet         layoutBBox             `json:"sheet"`
	SheetBorder   *layoutBBox            `json:"sheetBorder,omitempty"`
	Keepouts      []layoutBBox           `json:"keepouts"`
	Modules       []schCompositionModule `json:"modules"`
}
type schCompositionPlan struct {
	SchemaVersion           int                   `json:"schemaVersion"`
	Connectivity            connectivity.Document `json:"connectivity"`
	Sheet                   layoutBBox            `json:"sheet"`
	SheetBorder             *layoutBBox           `json:"sheetBorder,omitempty"`
	PlacementBoundarySource string                `json:"placementBoundarySource"`
	UsableBounds            layoutBBox            `json:"usableBounds"`
	Keepouts                []layoutBBox          `json:"keepouts"`
	Layout                  powerLayoutPlan       `json:"layout"`
	Rows                    int                   `json:"rows"`
	RowHeight               float64               `json:"rowHeight"`
	RowHeights              []float64             `json:"rowHeights"`
	PageMargin              float64               `json:"pageMargin"`
	ModuleGap               float64               `json:"moduleGap"`
}

func newSchComposeCmd(stdout, stderr io.Writer) *cobra.Command {
	var from, out, before, playbookOut, layoutPage string
	var replace bool
	c := &cobra.Command{Use: "compose", Short: "Compose authored Lib circuits onto one sheet and compile a guarded SCH Apply", Long: `Read schemaVersion:1 composition data containing connectivity (complete 1.4 IR),
sheet, keepouts and ordered modules (id/title/placements/wires/flags/terminals).
Optional sheetBorder is the explicit inner drawing-border bbox, separate from
the full sheet bbox retained for Apply verification. Frames leave at least
10 raw clearance inside that border, including their half-unit stroke; bounds
are rounded inward to the 5-raw grid. Missing sheetBorder uses the legacy sheet
bbox inset and reports sheet-bbox-fallback, not a measured inner border.
Optional terminals reference measured pins and generate shortest clear straight
leads, staggering marker lengths without changing nets or designators.
Plan content-sized compact frames/titles, then top-aligned Z rows with fixed
0.1-inch gaps. Each new row advances by the tallest frame in the preceding row;
shorter module frames keep their own height.
Preserve every pin-to-net, NC and explicit connectionState:"unconnected". Known
open pins retain electrical warnings; missing evidence is still refused.
No editor calls are made by this command.
--playbook requires --before (fresh target components.list snapshot with hydrated
device-library identity, pins, bbox and wire inventory). Rebuilding
a differing target requires --replace; an already matching target produces only
verification/frame steps. Apply always verifies pins before drawing wires.
Other pages may contain different parts; duplicate designators are refused before mutation.
Optional --layout-page consumes one selected layout-sheet-plan page. It verifies
the source modules, canonical membership/pin intent and exact paper evidence, then
preserves the supplied frames, titles, spacing and Z positions by rigid translation.
It never reruns placement or chooses variants. A complete selected page without
variants and explicit source sheetBorder/keepouts are required.
No automatic pagination, symbol scaling or source-page deletion is performed.`, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if from == "" {
			return fmt.Errorf("--from is required")
		}
		if err := validateSchCompositionOutputPaths([]string{from, before, layoutPage}, []string{out, playbookOut}); err != nil {
			return err
		}
		raw, err := os.ReadFile(from)
		if err != nil {
			return err
		}
		var source schCompositionSource
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err = dec.Decode(&source); err != nil {
			return err
		}
		if err = dec.Decode(new(any)); err != io.EOF {
			return fmt.Errorf("composition requires exactly one JSON document")
		}
		if err = validateSchCompositionBorderJSON(raw); err != nil {
			return err
		}
		var page *SchematicRenderInput
		if layoutPage != "" {
			if err = validateSchCompositionPreplacedSourceJSON(raw); err != nil {
				return err
			}
			pageRaw, e := os.ReadFile(layoutPage)
			if e != nil {
				return e
			}
			page, err = decodeSchCompositionLayoutPage(pageRaw)
			if err != nil {
				return err
			}
		}
		plan, err := planSchCompositionWithPage(source, page)
		if err != nil {
			return err
		}
		if playbookOut != "" {
			if before == "" {
				return fmt.Errorf("--playbook requires a fresh --before target snapshot")
			}
			b, err := os.ReadFile(before)
			if err != nil {
				return err
			}
			pb, err := schCompositionPlaybook(plan, b, replace)
			if err != nil {
				return err
			}
			b, err = json.MarshalIndent(pb, "", "  ")
			if err != nil {
				return err
			}
			if err = os.WriteFile(playbookOut, append(b, '\n'), 0644); err != nil {
				return err
			}
		}
		b, err := json.MarshalIndent(plan, "", "  ")
		if err != nil {
			return err
		}
		if out != "" {
			if err = os.WriteFile(out, append(b, '\n'), 0644); err != nil {
				return err
			}
		} else {
			if _, err = stdout.Write(append(b, '\n')); err != nil {
				return err
			}
		}
		fmt.Fprintf(stderr, "compose: %d modules, %d parts, %d rows, maximum row height %g; page margin/gap %g/%g raw; boundary %s\n", len(plan.Layout.Frames), len(plan.Layout.Placements), plan.Rows, plan.RowHeight, plan.PageMargin, plan.ModuleGap, plan.PlacementBoundarySource)
		if plan.SheetBorder == nil {
			fmt.Fprintln(stderr, "compose: inner drawing border not supplied; clearance is relative to the sheet bbox only")
		}
		return nil
	}}
	c.Flags().StringVar(&from, "from", "", "authored module composition JSON")
	c.Flags().StringVar(&out, "out", "", "write computed composition JSON")
	c.Flags().StringVar(&before, "before", "", "fresh target sch list snapshot with --include-device-identity --include-bbox --include-pins --include-wires")
	c.Flags().StringVar(&playbookOut, "playbook", "", "also write ordered SCH Apply queue")
	c.Flags().BoolVar(&replace, "replace", false, "compile a guarded reset of a differing target, preserving its sheet")
	c.Flags().StringVar(&layoutPage, "layout-page", "", "selected complete layout-sheet-plan page: preserve its geometry, frames and spacing instead of repacking; requires matching source modules and explicit sheetBorder")
	return c
}

func planSchComposition(src schCompositionSource) (*schCompositionPlan, error) {
	return planSchCompositionWithPage(src, nil)
}

func planSchCompositionWithPage(src schCompositionSource, page *SchematicRenderInput) (*schCompositionPlan, error) {
	// Never mutate the caller's canonical input through slices/pointers.
	raw, _ := json.Marshal(src)
	var cloned schCompositionSource
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return nil, err
	}
	src = cloned
	if page != nil {
		if err := validateSchCompositionPreplaced(src, *page); err != nil {
			return nil, err
		}
	}
	d := src.Connectivity
	if src.SchemaVersion != 1 || len(src.Modules) == 0 || !plBoxValid(src.Sheet) || d.ProjectID == "" || d.DocumentID == "" {
		return nil, fmt.Errorf("composition requires schemaVersion:1, modules, sheet and target projectId/documentId")
	}
	if err := d.Validate(); err != nil {
		return nil, err
	}
	usable, boundarySource, err := schCompositionUsableBounds(src.Sheet, src.SheetBorder)
	if err != nil {
		return nil, err
	}
	for _, k := range src.Keepouts {
		if !plBoxValid(k) || !boxInside(k, src.Sheet) {
			return nil, fmt.Errorf("invalid keepout")
		}
	}
	byRef := map[string]connectivity.Component{}
	byID := map[string]connectivity.Component{}
	nets := map[string]string{}
	pinNet := map[string]string{}
	for _, n := range d.Nets {
		if strings.TrimSpace(n.Name) == "" {
			return nil, fmt.Errorf("net %s has no canonical name", n.ID)
		}
		for id, name := range nets {
			if id != n.ID && name == n.Name {
				return nil, fmt.Errorf("different net IDs share name %s; explicit reconciliation required before composition", n.Name)
			}
		}
		nets[n.ID] = n.Name
	}
	for _, c := range d.Components {
		if c.Device.LibraryUUID == "" || !isDeviceLibraryUUID(c.Device.UUID) {
			return nil, fmt.Errorf("%s requires a real library/device UUID; resolve its LCSC identity first", c.Ref)
		}
		byRef[c.Ref], byID[c.ID] = c, c
	}
	for _, e := range d.Connections {
		pinNet[byID[e.ComponentID].Ref+"."+e.PinNumber] = nets[e.NetID]
	}
	members := map[string]map[string]bool{}
	for _, m := range d.Modules {
		if _, ok := members[m.ID]; ok {
			return nil, fmt.Errorf("duplicate canonical module %s", m.ID)
		}
		members[m.ID] = map[string]bool{}
		for _, id := range append(append([]string(nil), m.CoreComponents...), m.PeripheralComponents...) {
			if _, ok := byID[id]; !ok || members[m.ID][id] {
				return nil, fmt.Errorf("module %s has unknown/duplicate member %s", m.ID, id)
			}
			members[m.ID][id] = true
		}
	}
	result := &schCompositionPlan{SchemaVersion: 1, Connectivity: d, Sheet: src.Sheet, SheetBorder: src.SheetBorder, PlacementBoundarySource: boundarySource, UsableBounds: usable, Keepouts: src.Keepouts, PageMargin: schModulePageMargin, ModuleGap: schModuleGap, Layout: powerLayoutPlan{SchemaVersion: 1, DocumentID: d.DocumentID, ExpectedPinNets: pinNet}}
	if page != nil {
		resolved, _ := resolveSchematicRenderSpacing(*page)
		usable = sheetPreviewUsable(*resolved.Sheet)
		result.UsableBounds = usable
		result.PageMargin, result.ModuleGap = resolved.Sheet.Padding, resolved.Sheet.Gap
	}
	seen := map[string]bool{}
	seenModules := map[string]bool{}
	layouts := []powerLayoutPlan{}
	for moduleIndex, m := range src.Modules {
		if seenModules[m.ID] || len(members[m.ID]) == 0 {
			return nil, fmt.Errorf("missing/duplicate canonical module membership: %s", m.ID)
		}
		seenModules[m.ID] = true
		if len(m.Placements) != len(members[m.ID]) {
			return nil, fmt.Errorf("module %s member set differs from IR", m.ID)
		}
		p := powerLayoutPlan{SchemaVersion: 1, DocumentID: d.DocumentID, Placements: m.Placements, Wires: m.Wires, Flags: m.Flags}
		for i, c := range p.Placements {
			canonical, ok := byRef[c.Designator]
			if !ok || seen[c.Designator] || !members[m.ID][canonical.ID] {
				return nil, fmt.Errorf("unknown/repeated/unowned component %s", c.Designator)
			}
			seen[c.Designator] = true
			if !plBoxValid(c.BBox) || !plGrid(c.X) || !plGrid(c.Y) || !plFinite(c.Rotation) || math.Mod(c.Rotation, 90) != 0 {
				return nil, fmt.Errorf("%s invalid measured body or grid/orientation", c.Designator)
			}
			pins := map[string]connectivity.Pin{}
			for _, q := range canonical.Pins {
				pins[q.Number] = q
			}
			if len(pins) == 0 || len(c.Pins) != len(pins) {
				return nil, fmt.Errorf("%s pin set differs from IR", c.Designator)
			}
			seenPins := map[string]bool{}
			for _, q := range c.Pins {
				cp, exists := pins[q.Number]
				net := pinNet[c.Designator+"."+q.Number]
				if !exists || seenPins[q.Number] || !plGrid(q.X) || !plGrid(q.Y) || q.Net != net {
					return nil, fmt.Errorf("%s.%s pin geometry/net differs from IR", c.Designator, q.Number)
				}
				seenPins[q.Number] = true
				states := 0
				if net != "" {
					states++
				}
				if cp.NoConnected {
					states++
				}
				if cp.ConnectionState == "unconnected" {
					states++
				}
				if states != 1 {
					return nil, fmt.Errorf("%s.%s must have exactly one net or explicit NC or unconnected intent", c.Designator, q.Number)
				}
			}
			// The target will create fresh primitives; source instance IDs are not
			// placement identities and must never be replayed or compared after create.
			p.Placements[i].PrimitiveID = ""
		}
		var segments []powerLayoutWire
		for _, w := range p.Wires {
			if len(w.Points) < 2 {
				return nil, fmt.Errorf("module %s wire needs at least two points", m.ID)
			}
			for i := 1; i < len(w.Points); i++ {
				segments = append(segments, powerLayoutWire{Net: w.Net, Points: [][2]float64{w.Points[i-1], w.Points[i]}})
			}
		}
		p.Wires = segments
		if err := planSchCompositionTerminals(&p, m.Terminals); err != nil {
			return nil, fmt.Errorf("module %s: %w", m.ID, err)
		}
		if err := validateSchCompositionNets(&p); err != nil {
			return nil, fmt.Errorf("module %s: %w", m.ID, err)
		}
		obstacles, err := compositionMarkerGeometry(&p)
		if err != nil {
			return nil, fmt.Errorf("module %s: %w", m.ID, err)
		}
		var frame schFrameSpec
		if page != nil {
			frame = *page.Zones[moduleIndex].Frame
		} else {
			frame, err = measureSchModuleFrameObstacles(m.ID, m.Title, obstacles, m.TitleMetrics, nil)
			if err != nil {
				return nil, err
			}
		}
		p.Frames = []schFrameSpec{frame}
		// Local coordinates can be outside the eventual sheet until row placement.
		if err = validatePowerLayout(&p, frame.Rect); err != nil {
			return nil, fmt.Errorf("module %s: %w", m.ID, err)
		}
		layouts = append(layouts, p)
		result.Layout.Frames = append(result.Layout.Frames, frame)
	}
	if len(seen) != len(d.Components) || len(seenModules) != len(d.Modules) {
		return nil, fmt.Errorf("composition must cover every IR component and module exactly once")
	}
	var rows []schModuleRowPlacement
	if page != nil {
		rows = schCompositionPreplacedRows(*page)
	} else {
		rows, err = planSchModuleRows(result.Layout.Frames, usable, 0, schModuleGap)
	}
	if err != nil {
		return nil, err
	}
	result.Layout.Frames = nil
	for i, r := range rows {
		if !boxInside(r.Frame.Rect, usable) {
			return nil, fmt.Errorf("module %s exceeds the usable drawing border", r.Frame.ID)
		}
		for _, k := range src.Keepouts {
			if boxesGapOverlap(r.Frame.Rect, k, schModulePageMargin) {
				return nil, fmt.Errorf("module %s overlaps the title-block keepout; revise module geometry/order", r.Frame.ID)
			}
		}
		p := layouts[i]
		translatePowerLayout(&p, r.DX, r.DY)
		result.Layout.Placements = append(result.Layout.Placements, p.Placements...)
		result.Layout.Wires = append(result.Layout.Wires, p.Wires...)
		result.Layout.Flags = append(result.Layout.Flags, p.Flags...)
		result.Layout.Frames = append(result.Layout.Frames, r.Frame)
		result.Rows = r.Row + 1
		height := r.Frame.Rect.MaxY - r.Frame.Rect.MinY
		for len(result.RowHeights) <= r.Row {
			result.RowHeights = append(result.RowHeights, 0)
		}
		result.RowHeights[r.Row] = math.Max(result.RowHeights[r.Row], height)
		result.RowHeight = math.Max(result.RowHeight, height)
	}
	// Translated official coordinates can retain arithmetic tails. Normalize
	// only the newly compiled drawing, never the caller's measurement/baseline.
	normalizeSchCompositionGeometry(&result.Layout)
	if err = validatePowerLayout(&result.Layout, src.Sheet); err != nil {
		return nil, err
	}
	for i, c := range result.Connectivity.Components {
		for _, placed := range result.Layout.Placements {
			if placed.Designator == c.Ref {
				result.Connectivity.Components[i].PageID = d.DocumentID
				result.Connectivity.Components[i].PageName = ""
				result.Connectivity.Components[i].Placement = &connectivity.Placement{X: placed.X, Y: placed.Y, Rotation: placed.Rotation, Mirror: placed.Mirror, BBox: &connectivity.BBox{MinX: placed.BBox.MinX, MinY: placed.BBox.MinY, MaxX: placed.BBox.MaxX, MaxY: placed.BBox.MaxY}}
				for j, pin := range c.Pins {
					for _, q := range placed.Pins {
						if pin.Number == q.Number {
							result.Connectivity.Components[i].Pins[j].X = q.X
							result.Connectivity.Components[i].Pins[j].Y = q.Y
						}
					}
				}
			}
		}
	}
	return result, nil
}

// Verify that every electrical pin reaches a named marker through real wires.
// Names written only in the JSON wire record do not name a native wire tree.
func validateSchCompositionNets(p *powerLayoutPlan) error {
	segs := append([]powerLayoutWire(nil), p.Wires...)
	flagIndexes := map[int]string{}
	for _, f := range p.Flags {
		if strings.TrimSpace(f.Net) == "" || !plGrid(f.Offset) || f.Offset <= 0 {
			return fmt.Errorf("invalid marker net/offset")
		}
		switch f.Kind {
		case "power", "ground", "net_port_in", "net_port_out", "net_port_bi":
		default:
			return fmt.Errorf("unsupported marker kind %s", f.Kind)
		}
		e := [2]float64{f.PinX, f.PinY}
		switch f.Direction {
		case "left":
			e[0] -= f.Offset
		case "right":
			e[0] += f.Offset
		case "up":
			e[1] += f.Offset
		case "down":
			e[1] -= f.Offset
		default:
			return fmt.Errorf("invalid marker direction")
		}
		flagIndexes[len(segs)] = f.Net
		segs = append(segs, powerLayoutWire{Net: f.Net, Points: [][2]float64{{f.PinX, f.PinY}, e}})
	}
	parent := make([]int, len(segs))
	for i := range parent {
		parent[i] = i
	}
	var root func(int) int
	root = func(i int) int {
		for i != parent[i] {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	for i, a := range segs {
		if len(a.Points) != 2 || a.Net == "" {
			return fmt.Errorf("invalid named wire segment")
		}
		for j, b := range segs[:i] {
			if plSegmentsMeet(a.Points[0], a.Points[1], b.Points[0], b.Points[1]) {
				if a.Net != b.Net {
					return fmt.Errorf("wire bridge %s/%s", a.Net, b.Net)
				}
				parent[root(i)] = root(j)
			}
		}
	}
	named := map[int]string{}
	touched := map[int]bool{}
	for i, n := range flagIndexes {
		named[root(i)] = n
	}
	for _, c := range p.Placements {
		for _, q := range c.Pins {
			found := false
			for i, s := range segs {
				if plOnSegment([2]float64{q.X, q.Y}, s.Points[0], s.Points[1]) {
					if q.Net == "" || s.Net != q.Net {
						return fmt.Errorf("wire touches NC/foreign pin %s.%s", c.Designator, q.Number)
					}
					touched[root(i)] = true
					if named[root(i)] == q.Net {
						found = true
					}
				}
			}
			if q.Net != "" && !found {
				return fmt.Errorf("%s.%s does not reach a named wire tree", c.Designator, q.Number)
			}
		}
	}
	for i := range segs {
		if !touched[root(i)] {
			return fmt.Errorf("orphan wire/marker tree for %s", segs[i].Net)
		}
	}
	return nil
}

func schCompositionExpectation(p *schCompositionPlan, final bool) *schematicStateExpectation {
	e := &schematicStateExpectation{ExactParts: true, Parts: map[string]schematicPartExpectation{}}
	nc := map[string]bool{}
	devices := map[string]*schematicDeviceExpectation{}
	for _, c := range p.Connectivity.Components {
		devices[c.Ref] = &schematicDeviceExpectation{LibraryUUID: c.Device.LibraryUUID, UUID: c.Device.UUID}
		for _, q := range c.Pins {
			nc[c.Ref+"."+q.Number] = q.NoConnected
		}
	}
	for _, c := range p.Layout.Placements {
		c := c
		part := schematicPartExpectation{Device: devices[c.Designator], X: &c.X, Y: &c.Y, Rotation: &c.Rotation, Mirror: &c.Mirror, BBox: &c.BBox, Pins: map[string]schematicPinExpectation{}}
		for _, q := range c.Pins {
			q := q
			v := schematicPinExpectation{X: &q.X, Y: &q.Y}
			if final {
				noConnect := nc[c.Designator+"."+q.Number]
				v.Net = &q.Net
				v.NC = &noConnect
			}
			part.Pins[q.Number] = v
		}
		e.Parts[c.Designator] = part
	}
	if final {
		e.Drawing = &schematicDrawingExpectation{Wires: p.Layout.Wires, Flags: p.Layout.Flags}
	}
	return e
}

func schCompositionPlaybook(p *schCompositionPlan, before []byte, replace bool) (*playbook, error) {
	if err := connectivity.ValidatePlacementDesignators(p.Connectivity); err != nil {
		return nil, fmt.Errorf("composition placement designators: %w", err)
	}
	var env struct {
		Result  map[string]any `json:"result"`
		Context struct {
			Project string `json:"projectUuid"`
			Doc     string `json:"documentUuid"`
		} `json:"context"`
	}
	if err := json.Unmarshal(before, &env); err != nil {
		return nil, err
	}
	if env.Context.Project != p.Connectivity.ProjectID || env.Context.Doc != p.Connectivity.DocumentID {
		return nil, fmt.Errorf("before snapshot does not match target project/document")
	}
	if env.Result == nil {
		return nil, fmt.Errorf("missing before snapshot result")
	}
	raw, _ := json.Marshal(env.Result)
	var src powerLayoutSnapshot
	if err := json.Unmarshal(raw, &src); err != nil {
		return nil, err
	}
	// Require the same measured sheet as the planner, not a guessed A4 extent.
	sheets := 0
	for _, c := range src.Components {
		if c.ComponentType == "sheet" {
			if c.BBox == nil || *c.BBox != p.Sheet {
				return nil, fmt.Errorf("target sheet geometry differs from composition")
			}
			sheets++
		}
	}
	if sheets != 1 {
		return nil, fmt.Errorf("exactly one measured target sheet required")
	}
	// A baseline may contain different devices (explicit --replace handles
	// them), but every existing device must be identified before reuse/reset.
	beforeDevices := map[string]*schematicDeviceExpectation{}
	for _, entry := range env.Result["components"].([]any) {
		record, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("malformed before component record")
		}
		if record["componentType"] != "part" {
			continue
		}
		ref, _ := record["designator"].(string)
		device, err := measuredSchematicDevice(ref, record)
		if err != nil {
			return nil, fmt.Errorf("before snapshot: %w", err)
		}
		beforeDevices[ref] = device
	}
	final := schCompositionExpectation(p, true)
	matchError := final.check(env.Result, nil)
	matches := matchError == nil
	if !matches && !replace {
		return nil, fmt.Errorf("target differs from the planned circuit; use --replace with its fresh snapshot to compile a guarded rebuild: %w", matchError)
	}
	reuseUnwired := schCompositionExpectation(p, false).check(env.Result, nil) == nil && (&schematicDrawingExpectation{}).check(env.Result) == nil
	if summary, ok := env.Result["connectivitySummary"].(map[string]any); !ok || summary["scope"] != "activePage" || summary["wires"] != float64(0) || summary["buses"] != float64(0) {
		reuseUnwired = false
	}
	desiredNC := map[string]bool{}
	for _, c := range p.Connectivity.Components {
		for _, q := range c.Pins {
			desiredNC[c.Ref+"."+q.Number] = q.NoConnected
		}
	}
	for _, c := range src.Components {
		for _, q := range c.Pins {
			if q.NC && !desiredNC[c.Designator+"."+q.Number] {
				reuseUnwired = false
			}
		}
	}
	zero, timeout, stop := 0, 90, false
	pb := &playbook{Version: 1, RequireFullExecution: true, Meta: playbookMeta{Name: "Single-sheet Lib composition", Project: p.Connectivity.ProjectID, Doc: p.Connectivity.DocumentID}, Defaults: stepPolicy{Retry: &zero, TimeoutSec: &timeout, ContinueOnError: &stop}}
	read := map[string]any{"includePins": true, "includeBBox": true, "includeDeviceIdentity": true, "includeWires": true, "includeConnectivitySummary": true}
	if matches {
		all := *final
		all.Drawing = nil
		all.ExactParts = false
		pb.Steps = append(pb.Steps, playbookStep{ID: "verify-project-unique-designators", Action: "schematic.components.list", Payload: map[string]any{"includePins": true, "includeBBox": true, "includeDeviceIdentity": true, "allPages": true, "tagPages": true}, ExpectSchematic: &all})
		pb.Steps = append(pb.Steps, playbookStep{ID: "verify-existing-composition", Action: "schematic.components.list", Payload: read, ExpectSchematic: final})
	} else {
		baseline := &schematicStateExpectation{ExactParts: true, Parts: map[string]schematicPartExpectation{}}
		for _, c := range src.Components {
			if c.ComponentType == "part" {
				if c.PrimitiveID == "" || c.X == nil || c.Y == nil || c.Rotation == nil || c.Mirror == nil || c.BBox == nil || len(c.Pins) == 0 || c.PinsAvailable == nil || !*c.PinsAvailable || c.NetAmbiguous {
					return nil, fmt.Errorf("before %s lacks complete identity/geometry/pins", c.Designator)
				}
				if _, exists := baseline.Parts[c.Designator]; exists {
					return nil, fmt.Errorf("duplicate before part %s", c.Designator)
				}
				part := schematicPartExpectation{Device: beforeDevices[c.Designator], BBox: c.BBox, PrimitiveID: c.PrimitiveID, X: c.X, Y: c.Y, Rotation: c.Rotation, Mirror: c.Mirror, Pins: map[string]schematicPinExpectation{}}
				for _, q := range c.Pins {
					q := q
					if q.X == nil || q.Y == nil || q.Net == nil || q.Number == "" {
						return nil, fmt.Errorf("before %s.%s lacks complete pin geometry/net", c.Designator, q.Number)
					}
					if _, exists := part.Pins[q.Number]; exists {
						return nil, fmt.Errorf("duplicate before pin %s.%s", c.Designator, q.Number)
					}
					part.Pins[q.Number] = schematicPinExpectation{X: q.X, Y: q.Y, NC: &q.NC, Net: q.Net}
				}
				baseline.Parts[c.Designator] = part
			}
		}
		if err := baseline.validate(); err != nil {
			return nil, err
		}
		if err := baseline.check(env.Result, nil); err != nil {
			return nil, fmt.Errorf("incomplete before snapshot: %w", err)
		}
		if reuseUnwired {
			baseline.Drawing = &schematicDrawingExpectation{}
		}
		all := *baseline
		all.Drawing = nil
		all.ExactParts = false
		for _, c := range p.Connectivity.Components {
			if _, exists := baseline.Parts[c.Ref]; !exists {
				all.AbsentParts = append(all.AbsentParts, c.Ref)
			}
		}
		pb.Steps = append(pb.Steps, playbookStep{ID: "verify-project-unique-designators", Action: "schematic.components.list", Payload: map[string]any{"includePins": true, "includeBBox": true, "includeDeviceIdentity": true, "allPages": true, "tagPages": true}, ExpectSchematic: &all})
		var baselineAssertions map[string]string
		if reuseUnwired {
			baselineAssertions = map[string]string{"$.connectivitySummary.scope": "==activePage", "$.connectivitySummary.wires": "==0", "$.connectivitySummary.buses": "==0"}
		}
		pb.Steps = append(pb.Steps, playbookStep{ID: "verify-source-before-reset", Action: "schematic.components.list", Payload: read, ExpectSchematic: baseline, Assert: baselineAssertions})

		if !reuseUnwired {
			pb.Steps = append(pb.Steps, playbookStep{ID: "reset-target-preserving-sheet", Run: "sch clear"})
			pb.Steps = append(pb.Steps, playbookStep{ID: "verify-no-residual-primitives", Run: "sch clear", Flags: map[string]any{"dry-run": true, "expect-empty": true}})
			pb.Steps = append(pb.Steps, playbookStep{ID: "verify-cleared-target", Action: "schematic.components.list", Payload: read, ExpectSchematic: &schematicStateExpectation{ExactParts: true, Parts: map[string]schematicPartExpectation{}}, Assert: map[string]string{"$.count": "==1"}})
			byRef := map[string]connectivity.Component{}
			for _, c := range p.Connectivity.Components {
				byRef[c.Ref] = c
			}
			for i, c := range p.Layout.Placements {
				canonical := byRef[c.Designator]
				d := canonical.Device
				payload := map[string]any{"libraryUuid": d.LibraryUUID, "uuid": d.UUID, "x": c.X, "y": c.Y, "rotation": 0, "mirror": false, "designator": c.Designator}
				pb.Steps = append(pb.Steps, playbookStep{ID: fmt.Sprintf("place-%03d", i), Action: "schematic.component.place", Payload: payload, Capture: map[string]string{fmt.Sprintf("part_%03d", i): "$.primitiveId"}})
				// Create uses the opposite rotation sign on current EasyEDA builds.
				// Absolute modify has the stored-rotation contract used by measured IR.
				patch, assertions := schComponentBinding(canonical)
				patch["rotation"], patch["mirror"], patch["x"], patch["y"] = c.Rotation, c.Mirror, c.X, c.Y
				pb.Steps = append(pb.Steps, playbookStep{ID: fmt.Sprintf("orient-%03d", i), Action: "schematic.component.modify", Payload: map[string]any{"primitiveId": fmt.Sprintf("${part_%03d}", i), "patch": patch}, Assert: assertions})
			}
		}
		pb.Steps = append(pb.Steps, playbookStep{ID: "verify-physical-pins-before-wiring", Action: "schematic.components.list", Payload: read, ExpectSchematic: schCompositionExpectation(p, false)})
		pb.Steps = append(pb.Steps, playbookStep{ID: "save-placed-parts", Action: "schematic.save", Assert: map[string]string{"$.saved": "true"}})
		for i, c := range p.Connectivity.Components {
			pins := []string{}
			for _, q := range c.Pins {
				if q.NoConnected {
					pins = append(pins, q.Number)
				}
			}
			sort.Strings(pins)
			if len(pins) > 0 {
				pb.Steps = append(pb.Steps, playbookStep{ID: fmt.Sprintf("nc-%03d", i), Action: "schematic.pin.set_no_connect", Payload: map[string]any{"designator": c.Ref, "pins": pins, "noConnected": true}, Assert: map[string]string{"$.notApplied": "len==0"}})
			}
		}
		for i, w := range p.Layout.Wires {
			pb.Steps = append(pb.Steps, playbookStep{ID: fmt.Sprintf("wire-%03d", i), Action: "schematic.wire.create", Payload: map[string]any{"points": w.Points}})
		}
		for i, f := range p.Layout.Flags {
			pb.Steps = append(pb.Steps, playbookStep{ID: fmt.Sprintf("marker-%03d", i), Action: "schematic.power.connect_pin", Payload: map[string]any{"pinX": f.PinX, "pinY": f.PinY, "net": f.Net, "kind": f.Kind, "offset": f.Offset, "direction": f.Direction}})
		}
	}
	refs := map[string]string{}
	for _, c := range p.Connectivity.Components {
		refs[c.ID] = c.Ref
	}
	for i, m := range p.Connectivity.Modules {
		var members []string
		for _, id := range append(append([]string(nil), m.CoreComponents...), m.PeripheralComponents...) {
			members = append(members, refs[id])
		}
		pb.Steps = append(pb.Steps, playbookStep{ID: fmt.Sprintf("register-lib-%03d", i), Run: "sch group create", Flags: map[string]any{"name": m.ID, "members": strings.Join(members, ","), "if-absent": true}})
	}
	frames, err := powerLayoutFrameSteps(&p.Layout)
	if err != nil {
		return nil, err
	}
	pb.Steps = append(pb.Steps, frames...)
	pb.Steps = append(pb.Steps, playbookStep{ID: "verify-all-pins-nets-nc", Action: "schematic.components.list", Payload: read, ExpectSchematic: final}, playbookStep{ID: "electrical-check", Action: "schematic.check", Assert: map[string]string{"$.passed": "true"}}, playbookStep{ID: "wire-tree-check", Action: "schematic.bridgeCheck", Assert: map[string]string{"$.passed": "true"}}, playbookStep{ID: "strict-schematic-gate", Run: "sch gate", Flags: map[string]any{"strict": true, "json": true}}, playbookStep{ID: "save-composition", Action: "schematic.save", Assert: map[string]string{"$.saved": "true"}})
	if errs := preflight(pb, nil); len(errs) > 0 {
		return nil, fmt.Errorf("composition Apply preflight: %v", errs)
	}
	return pb, nil
}
