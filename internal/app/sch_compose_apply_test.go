package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/internal/connectivity"
)

// Build an explicit native readback for the synthetic circuit. The complete
// part/pin inventory is also usable as a pre-reset baseline after an edit.
func composeApplyFixture(t *testing.T, matching bool) (*schCompositionPlan, map[string]any) {
	t.Helper()
	src := composeFixture(2)
	src.Modules[0].Placements[0].Rotation = 90
	src.Modules[1].Placements[0].Rotation = 270
	src.Modules[1].Placements[0].Mirror = true
	p, err := planSchComposition(src)
	if err != nil {
		t.Fatal(err)
	}
	parts := []any{map[string]any{"componentType": "sheet", "primitiveId": "sheet", "bbox": p.Sheet}}
	nc := map[string]bool{}
	for _, c := range p.Connectivity.Components {
		for _, pin := range c.Pins {
			nc[c.Ref+"."+pin.Number] = pin.NoConnected
		}
	}
	for _, c := range p.Layout.Placements {
		var device *schematicDeviceExpectation
		for _, canonical := range p.Connectivity.Components {
			if canonical.Ref == c.Designator {
				device = &schematicDeviceExpectation{LibraryUUID: canonical.Device.LibraryUUID, UUID: canonical.Device.UUID}
			}
		}
		pins := []any{}
		for _, pin := range c.Pins {
			pins = append(pins, map[string]any{"primitiveId": "pin-" + c.Designator + "-" + pin.Number, "pinNumber": pin.Number, "pinName": pin.Name, "x": pin.X, "y": pin.Y, "net": pin.Net, "noConnected": nc[c.Designator+"."+pin.Number]})
		}
		parts = append(parts, map[string]any{"componentType": "part", "primitiveId": "live-" + c.Designator, "device": device, "designator": c.Designator, "x": c.X, "y": c.Y, "rotation": c.Rotation, "mirror": c.Mirror, "bbox": c.BBox, "pinsAvailable": true, "pins": pins})
	}
	wires := []any{}
	for _, w := range p.Layout.Wires {
		for i := 1; i < len(w.Points); i++ {
			a, b := w.Points[i-1], w.Points[i]
			wires = append(wires, map[string]any{"x0": a[0], "y0": a[1], "x1": b[0], "y1": b[1], "net": w.Net})
		}
	}
	for i, f := range p.Layout.Flags {
		x, y := f.PinX, f.PinY
		var rotation float64
		// composeFixture uses only the calibrated power-up / ground-down
		// pair, each stored at rotation zero.
		switch {
		case f.Kind == "power" && f.Direction == "up":
			y += f.Offset
		case f.Kind == "ground" && f.Direction == "down":
			y -= f.Offset
		default:
			t.Fatalf("fixture needs native marker geometry for %+v", f)
		}
		parts = append(parts, map[string]any{"componentType": "netflag", "primitiveId": fmt.Sprintf("flag-%d", i), "net": f.Net, "x": x, "y": y, "rotation": rotation})
		wires = append(wires, map[string]any{"x0": f.PinX, "y0": f.PinY, "x1": x, "y1": y, "net": f.Net})
	}
	env := map[string]any{"context": map[string]any{"projectUuid": p.Connectivity.ProjectID, "documentUuid": p.Connectivity.DocumentID, "documentType": "schematic"}, "result": map[string]any{"components": parts, "wires": wires, "count": len(parts), "wiresAvailable": true, "pinNetsAvailable": true, "connectivitySummary": map[string]any{"scope": "activePage", "wires": len(wires), "buses": 0, "netflags": len(p.Layout.Flags), "netports": 0, "netlabels": 0, "shortSymbols": 0}}}
	// Match the numeric/container shapes returned by the JSON HTTP protocol.
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if !matching {
		part := env["result"].(map[string]any)["components"].([]any)[1].(map[string]any)
		part["x"] = part["x"].(float64) + 5
	}
	composeFixturePageInventory(env["result"].(map[string]any))
	return p, env
}

func composeFixturePageInventory(result map[string]any) {
	page := map[string]any{}
	for _, kind := range []string{"buses", "arcs", "circles", "rectangles", "polygons", "texts", "attributes", "objects"} {
		page[kind] = []any{}
	}
	page["components"] = result["components"]
	page["pins"] = []any{}
	for _, item := range result["components"].([]any) {
		component, ok := item.(map[string]any)
		if !ok || component["componentType"] != "part" {
			continue
		}
		pins, _ := component["pins"].([]any)
		for _, item := range pins {
			pin, ok := item.(map[string]any)
			if !ok {
				continue // keep malformed rich records for the negative test
			}
			page["pins"] = append(page["pins"].([]any), map[string]any{"primitiveId": pin["primitiveId"], "componentId": component["primitiveId"]})
		}
	}
	rows := []any{}
	used := map[string]bool{}
	for _, item := range result["wires"].([]any) {
		if id, ok := item.(map[string]any)["primitiveId"].(string); ok {
			used[id] = true
		}
	}
	for i, item := range result["wires"].([]any) {
		wire := item.(map[string]any)
		if wire["primitiveId"] == nil {
			id := fmt.Sprintf("new-wire-%d", i)
			for used[id] {
				id += "-extra"
			}
			wire["primitiveId"] = id
			used[id] = true
		}
		rows = append(rows, map[string]any{"primitiveId": wire["primitiveId"], "Line": []any{wire["x0"], wire["y0"], wire["x1"], wire["y1"]}, "Net": wire["net"], "Color": nil, "LineWidth": 1.0, "LineType": 0.0})
	}
	page["wires"] = rows
	result["pagePrimitives"] = page
}

func composeEmptyPageBefore(p *schCompositionPlan) map[string]any {
	r := map[string]any{"components": []any{map[string]any{"componentType": "sheet", "primitiveId": "sheet", "bbox": p.Sheet}}, "wires": []any{}, "count": 1, "wiresAvailable": true, "pinNetsAvailable": true, "connectivitySummary": map[string]any{"scope": "activePage", "wires": 0, "buses": 0, "netflags": 0, "netports": 0, "netlabels": 0, "shortSymbols": 0}}
	composeFixturePageInventory(r)
	return map[string]any{"context": map[string]any{"projectUuid": p.Connectivity.ProjectID, "documentUuid": p.Connectivity.DocumentID}, "result": r}
}

func composeApplyBytes(t *testing.T, env map[string]any) []byte {
	t.Helper()
	if result, ok := env["result"].(map[string]any); ok {
		if _, hasWires := result["wires"].([]any); hasWires {
			if _, hasComponents := result["components"].([]any); hasComponents {
				composeFixturePageInventory(result)
			}
		}
	}
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func composeStep(t *testing.T, pb *playbook, id string) (int, *playbookStep) {
	t.Helper()
	for i := range pb.Steps {
		if pb.Steps[i].ID == id {
			return i, &pb.Steps[i]
		}
	}
	t.Fatalf("missing composition step %s", id)
	return -1, nil
}

func TestComposeApplyUsesAbsolutePoseAfterZeroRotationCreate(t *testing.T) {
	p, env := composeApplyFixture(t, false)
	p.Connectivity.Components[0].Role = "rf_mcu"
	pb, err := schCompositionPlaybook(p, composeApplyBytes(t, env), true)
	if err != nil {
		t.Fatal(err)
	}
	if !pb.RequireFullExecution || pb.Defaults.Retry == nil || *pb.Defaults.Retry != 0 || pb.Defaults.ContinueOnError == nil || *pb.Defaults.ContinueOnError {
		t.Fatal("destructive composition must run every guard and stop without blind retries")
	}
	resetIndex, reset := composeStep(t, pb, "reset-target-preserving-sheet")
	residualIndex, residual := composeStep(t, pb, "verify-no-residual-primitives")
	emptyIndex, empty := composeStep(t, pb, "verify-cleared-target")
	pinIndex, pinGate := composeStep(t, pb, "verify-physical-pins-before-wiring")
	if reset.Run != "sch clear" || residualIndex <= resetIndex || emptyIndex <= residualIndex || residual.Run != "sch clear" || residual.Flags["dry-run"] != true || residual.Flags["expect-empty"] != true {
		t.Fatal("rebuild must verify residual wires/graphics by a complete dry-run before placement")
	}
	if empty.ExpectSchematic == nil || !empty.ExpectSchematic.ExactParts || len(empty.ExpectSchematic.Parts) != 0 || empty.Assert["$.count"] != "==1" {
		t.Fatal("cleared target must retain only its sheet and no physical parts")
	}
	for i, c := range p.Layout.Placements {
		createIndex, create := composeStep(t, pb, fmt.Sprintf("place-%03d", i))
		orientIndex, orient := composeStep(t, pb, fmt.Sprintf("orient-%03d", i))
		rotation, _ := toFloat(create.Payload["rotation"])
		if createIndex <= emptyIndex || orientIndex != createIndex+1 || orientIndex >= pinIndex || rotation != 0 || create.Payload["mirror"] != false {
			t.Fatalf("%s must be created neutral then absolutely oriented before pin verification", c.Designator)
		}
		capture := fmt.Sprintf("part_%03d", i)
		if create.Action != "schematic.component.place" || create.Capture[capture] != "$.primitiveId" || orient.Action != "schematic.component.modify" || orient.Payload["primitiveId"] != "${"+capture+"}" {
			t.Fatalf("%s must orient the freshly returned primitive, never a source ID", c.Designator)
		}
		patch := orient.Payload["patch"].(map[string]any)
		if patch["rotation"] != c.Rotation || patch["mirror"] != c.Mirror || patch["x"] != c.X || patch["y"] != c.Y {
			t.Fatalf("%s absolute stored pose lost: %+v", c.Designator, patch)
		}
		canonical := p.Connectivity.Components[i]
		properties, ok := patch["otherProperty"].(map[string]any)
		if !ok || properties[connectivity.ComponentIDProperty] != canonical.ID || patch["designator"] != canonical.Ref {
			t.Fatalf("canonical identity must survive fresh placement: %+v", patch)
		}
		if canonical.Role != "" {
			if properties[connectivity.ComponentRoleProperty] != canonical.Role {
				t.Fatal("functional role must be bound independently of the visible reference")
			}
		} else if _, exists := properties[connectivity.ComponentRoleProperty]; exists {
			t.Fatal("placement must not invent a role")
		}
		if create.Payload["otherProperty"] != nil || create.Payload["customAttributes"] != nil {
			t.Fatal("place must not rely on unsupported custom-property inputs")
		}
		assertComponentBindingReadback(t, orient, canonical)
		if pinGate.ExpectSchematic.Parts[c.Designator].BBox == nil || len(pinGate.ExpectSchematic.Parts[c.Designator].Pins) != len(c.Pins) {
			t.Fatal("pre-wiring gate must cover real bbox and every physical pin")
		}
	}
	for i, s := range pb.Steps {
		if (s.Action == "schematic.wire.create" || s.Action == "schematic.power.connect_pin") && i <= pinIndex {
			t.Fatal("wire write precedes the real pin/bbox gate")
		}
	}
	finalIndex, final := composeStep(t, pb, "verify-all-pins-nets-nc")
	saveIndex, _ := composeStep(t, pb, "save-composition")
	if finalIndex >= saveIndex || final.ExpectSchematic.Drawing == nil || !final.ExpectSchematic.ExactParts {
		t.Fatal("save must follow complete electrical and drawing verification")
	}
	// Round-trip the queue through its public file loader, including captured
	// primitive substitution and the newly added bbox/NC/drawing expectations.
	file := filepath.Join(t.TempDir(), "composition.json")
	b, _ := json.Marshal(pb)
	if err := os.WriteFile(file, b, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, _, err := loadPlaybook(file)
	if err != nil {
		t.Fatal(err)
	}
	if errs := preflight(loaded, nil); len(errs) != 0 {
		t.Fatalf("generated queue cannot be replayed: %v", errs)
	}
}

func TestComposeClearGuardsCompleteSourceScene(t *testing.T) {
	p, env := composeApplyFixture(t, false)
	pb, err := schCompositionPlaybook(p, composeApplyBytes(t, env), true)
	if err != nil {
		t.Fatal(err)
	}
	guardIndex, guard := composeStep(t, pb, "verify-source-before-reset")
	clearIndex, clear := composeStep(t, pb, "reset-target-preserving-sheet")
	if guardIndex >= clearIndex || guard.ExpectSchematic.SourceScene == nil || guard.Payload["includePagePrimitives"] != true || clear.Flags["expect-page-primitives-b64"] == nil {
		t.Fatal("complete source scene and atomic clear inventory must precede deletion")
	}
	live := env["result"].(map[string]any)
	if err := guard.ExpectSchematic.check(live, nil); err != nil {
		t.Fatalf("same complete source rejected: %v", err)
	}
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
	}{
		{"wire-geometry", func(r map[string]any) { r["wires"].([]any)[0].(map[string]any)["x1"] = 555.0 }},
		{"wire-native-state", func(r map[string]any) {
			r["pagePrimitives"].(map[string]any)["wires"].([]any)[0].(map[string]any)["LineWidth"] = 9.0
		}},
		{"flag-same-id-net", func(r map[string]any) {
			for _, item := range r["pagePrimitives"].(map[string]any)["components"].([]any) {
				c := item.(map[string]any)
				if c["componentType"] == "netflag" {
					c["net"] = "CHANGED"
					return
				}
			}
		}},
		{"extra-graphic", func(r map[string]any) {
			r["pagePrimitives"].(map[string]any)["texts"] = []any{map[string]any{"primitiveId": "new-text", "Content": "x"}}
		}},
		{"missing-inventory", func(r map[string]any) { delete(r, "pagePrimitives") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var changed map[string]any
			b, _ := json.Marshal(live)
			_ = json.Unmarshal(b, &changed)
			tc.edit(changed)
			if err := guard.ExpectSchematic.check(changed, nil); err == nil {
				t.Fatal("changed or unreadable drawing passed pre-clear guard")
			}
		})
	}
}

func TestComposeClearRejectsLegacyOrPartialSnapshotBeforeQueue(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
	}{
		{"legacy-no-page-inventory", func(r map[string]any) { delete(r, "pagePrimitives") }},
		{"failed-wire-read", func(r map[string]any) { r["wiresAvailable"] = false }},
		{"missing-graphic-class", func(r map[string]any) { delete(r["pagePrimitives"].(map[string]any), "texts") }},
		{"wire-id-unknown", func(r map[string]any) { delete(r["wires"].([]any)[0].(map[string]any), "primitiveId") }},
		{"stale-wire-count", func(r map[string]any) { r["connectivitySummary"].(map[string]any)["wires"] = 0.0 }},
		{"stale-flag-count", func(r map[string]any) { r["connectivitySummary"].(map[string]any)["netflags"] = 0.0 }},
		{"orphan-attribute", func(r map[string]any) {
			r["pagePrimitives"].(map[string]any)["attributes"] = []any{map[string]any{"primitiveId": "orphan", "X": 0, "Y": 0, "Rotation": 0, "Color": nil, "FontName": nil, "FontSize": nil, "Bold": nil, "Italic": nil, "UnderLine": nil, "AlignMode": nil, "FillColor": nil, "Key": "Label", "Value": "old", "KeyVisible": true, "ValueVisible": true, "ParentPrimitiveId": "deleted-parent"}}
		}},
		{"embedded-object", func(r map[string]any) {
			r["pagePrimitives"].(map[string]any)["objects"] = []any{map[string]any{"primitiveId": "logo", "Content": "bytes", "StartX": 0, "StartY": 0, "Width": 10, "Height": 10, "Rotation": 0, "Mirror": false, "FileName": "logo.svg"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, env := composeApplyFixture(t, false)
			r := env["result"].(map[string]any)
			tc.edit(r)
			before, _ := json.Marshal(env)
			if pb, err := schCompositionPlaybook(p, before, true); err == nil || pb != nil {
				t.Fatalf("incomplete source compiled deletion queue: %v", err)
			}
		})
	}
}

func TestComposeApplyRejectsIncompleteDestructiveBaseline(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(map[string]any, map[string]any)
	}{
		{"missing-pin-inventory", func(_ map[string]any, c map[string]any) { delete(c, "pins") }},
		{"unavailable-pins", func(_ map[string]any, c map[string]any) { c["pinsAvailable"] = false }},
		{"missing-pin-position", func(_ map[string]any, c map[string]any) { delete(c["pins"].([]any)[0].(map[string]any), "x") }},
		{"unknown-pin-net", func(_ map[string]any, c map[string]any) { c["pins"].([]any)[0].(map[string]any)["net"] = nil }},
		{"missing-pin-net", func(_ map[string]any, c map[string]any) { delete(c["pins"].([]any)[0].(map[string]any), "net") }},
		{"duplicate-pin", func(_ map[string]any, c map[string]any) { c["pins"] = append(c["pins"].([]any), c["pins"].([]any)[0]) }},
		{"duplicate-part", func(r map[string]any, c map[string]any) { r["components"] = append(r["components"].([]any), c) }},
		{"ambiguous-net", func(_ map[string]any, c map[string]any) { c["netAmbiguous"] = true }},
		{"unknown-NC", func(_ map[string]any, c map[string]any) { c["pins"].([]any)[0].(map[string]any)["noConnected"] = nil }},
		{"unknown-body", func(_ map[string]any, c map[string]any) { delete(c, "bbox") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, env := composeApplyFixture(t, false)
			r := env["result"].(map[string]any)
			c := r["components"].([]any)[1].(map[string]any)
			tc.edit(r, c)
			if pb, err := schCompositionPlaybook(p, composeApplyBytes(t, env), true); err == nil || pb != nil {
				t.Fatalf("incomplete evidence produced a destructive queue: %v", err)
			}
		})
	}
}

func TestComposeApplyMatchingCircuitDoesNotResetOrDuplicate(t *testing.T) {
	p, env := composeApplyFixture(t, true)
	pb, err := schCompositionPlaybook(p, composeApplyBytes(t, env), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range pb.Steps {
		if s.Run == "sch clear" || s.Action == "schematic.component.place" || s.Action == "schematic.component.modify" || s.Action == "schematic.wire.create" || s.Action == "schematic.power.connect_pin" {
			t.Fatalf("matching circuit caused electrical mutation: %+v", s)
		}
	}
	_, verify := composeStep(t, pb, "verify-existing-composition")
	if verify.ExpectSchematic == nil || verify.ExpectSchematic.Drawing == nil {
		t.Fatal("matching path must still verify actual drawn wire/marker geometry")
	}
}

func TestComposeApplySameNetsWithDetourRequireRebuild(t *testing.T) {
	p, env := composeApplyFixture(t, true)
	r := env["result"].(map[string]any)
	wires := r["wires"].([]any)
	w := wires[0].(map[string]any)
	x0, y0, x1, y1 := w["x0"].(float64), w["y0"].(float64), w["x1"].(float64), w["y1"].(float64)
	r["wires"] = append([]any{drawingWire(x0, y0, x0, y0+10), drawingWire(x0, y0+10, x1, y1+10), drawingWire(x1, y1+10, x1, y1)}, wires[1:]...)
	r["connectivitySummary"].(map[string]any)["wires"] = float64(len(r["wires"].([]any)))
	before := composeApplyBytes(t, env)
	if _, err := schCompositionPlaybook(p, before, false); err == nil || !strings.Contains(err.Error(), "target differs") {
		t.Fatalf("equal pin nets hid different drawn routes: %v", err)
	}
	pb, err := schCompositionPlaybook(p, before, true)
	if err != nil {
		t.Fatal(err)
	}
	composeStep(t, pb, "reset-target-preserving-sheet")
}

func TestComposeApplyOtherPagesAllowDistinctRefsButRejectTargetCollisions(t *testing.T) {
	for _, matching := range []bool{false, true} {
		t.Run(fmt.Sprintf("matching-%t", matching), func(t *testing.T) {
			p, env := composeApplyFixture(t, matching)
			pb, err := schCompositionPlaybook(p, composeApplyBytes(t, env), !matching)
			if err != nil {
				t.Fatal(err)
			}
			guard := &pb.Steps[0]
			if guard.Action != "schematic.components.list" || guard.Payload["allPages"] != true || guard.Payload["tagPages"] != true || guard.ExpectSchematic == nil || guard.ExpectSchematic.ExactParts || !guard.ExpectSchematic.DesignatorsOnly {
				t.Fatal("first guard must check project-wide uniqueness while allowing other pages' distinct refs")
			}
			for _, slow := range []string{"includePins", "includeBBox", "includeDeviceIdentity", "includeWires", "includeConnectivitySummary"} {
				if _, exists := guard.Payload[slow]; exists {
					t.Fatalf("project-wide designator guard requested unrelated slow field %s", slow)
				}
			}
			for ref, part := range guard.ExpectSchematic.Parts {
				if part.Device != nil || part.BBox != nil || part.Pins != nil || part.Instance != nil || part.X != nil || part.Y != nil || part.Rotation != nil || part.Mirror != nil {
					t.Fatalf("project-wide designator guard retained non-designator state for %s: %+v", ref, part)
				}
			}
			if err := guard.ExpectSchematic.check(env["result"], nil); err != nil {
				t.Fatalf("original complete target inventory did not pass its guard: %v", err)
			}
			r := env["result"].(map[string]any)
			r["components"] = append(r["components"].([]any), map[string]any{"componentType": "part", "designator": "OLD_PAGE_U9", "primitiveId": "off-page-component"})
			if err := guard.ExpectSchematic.check(r, nil); err != nil {
				t.Fatalf("different designator on another page must be allowed: %v", err)
			}
			localID := "verify-source-before-reset"
			if matching {
				localID = "verify-existing-composition"
			}
			_, local := composeStep(t, pb, localID)
			if local.ExpectSchematic == nil || !local.ExpectSchematic.ExactParts {
				t.Fatal("relaxing project scope must not relax the target page's complete part guard")
			}
			if err := local.ExpectSchematic.check(r, nil); err == nil {
				t.Fatalf("the same additional component on the target page must still be refused: %v", err)
			}
			targetRef := p.Layout.Placements[0].Designator
			r["components"] = append(r["components"].([]any), map[string]any{"componentType": "part", "designator": targetRef, "primitiveId": "off-page-target-collision"})
			if err := guard.ExpectSchematic.check(r, nil); err == nil || !strings.Contains(err.Error(), "duplicate part designator "+targetRef) {
				t.Fatalf("same target designator on another page must block NC/wire attribution: %v", err)
			}
		})
	}
}

func TestComposeApplyEmptyTargetAllowsOtherPageRefsButProtectsNewRefs(t *testing.T) {
	p, env := composeApplyFixture(t, true)
	r := env["result"].(map[string]any)
	r["components"] = []any{r["components"].([]any)[0]} // the measured sheet only
	r["count"], r["wires"] = 1.0, []any{}
	r["connectivitySummary"] = map[string]any{"scope": "activePage", "wires": 0.0, "buses": 0.0, "netflags": 0.0, "netports": 0.0, "netlabels": 0.0, "shortSymbols": 0.0}
	pb, err := schCompositionPlaybook(p, composeApplyBytes(t, env), true)
	if err != nil {
		t.Fatalf("a known empty target page must produce a guarded placement queue: %v", err)
	}
	guard := pb.Steps[0].ExpectSchematic
	if guard == nil || guard.ExactParts || !guard.DesignatorsOnly || len(guard.Parts) != 0 || len(guard.AbsentParts) != len(p.Layout.Placements) {
		t.Fatal("empty target must reserve every soon-to-be-created designator across the project")
	}
	globally := map[string]any{"components": []any{map[string]any{"componentType": "sheet"}, map[string]any{"componentType": "part", "designator": "OTHER_PAGE_R9", "primitiveId": "existing-other-page"}}}
	if err := guard.check(globally, nil); err != nil {
		t.Fatalf("other page's different designators must coexist with an empty target: %v", err)
	}
	for _, planned := range p.Layout.Placements {
		withCollision := map[string]any{"components": append(append([]any{}, globally["components"].([]any)...), map[string]any{"componentType": "part", "designator": planned.Designator, "primitiveId": "already-on-other-page"})}
		if err := guard.check(withCollision, nil); err == nil || !strings.Contains(err.Error(), planned.Designator) {
			t.Fatalf("new designator %s already exists off-page but was not blocked: %v", planned.Designator, err)
		}
	}
}

func TestComposeApplyPartiallyPopulatedTargetChecksExistingAndReservesMissingRefs(t *testing.T) {
	p, env := composeApplyFixture(t, false)
	r := env["result"].(map[string]any)
	var components []any
	for _, entry := range r["components"].([]any) {
		c := entry.(map[string]any)
		if c["componentType"] == "part" && c["designator"] == p.Layout.Placements[1].Designator {
			continue
		}
		components = append(components, entry)
	}
	r["components"], r["count"] = components, len(components)
	pb, err := schCompositionPlaybook(p, composeApplyBytes(t, env), true)
	if err != nil {
		t.Fatal(err)
	}
	guard := pb.Steps[0].ExpectSchematic
	missingRef := p.Layout.Placements[1].Designator
	if guard == nil || len(guard.Parts) != 1 || len(guard.AbsentParts) != 1 || guard.AbsentParts[0] != missingRef {
		t.Fatal("project guard must retain existing part baselines and reserve only missing planned refs")
	}
	if err := guard.check(r, nil); err != nil {
		t.Fatalf("partially populated original target should pass: %v", err)
	}
	r["components"] = append(components, map[string]any{"componentType": "part", "designator": missingRef, "primitiveId": "other-page-collision"})
	if err := guard.check(r, nil); err == nil || !strings.Contains(err.Error(), missingRef) {
		t.Fatalf("planned missing ref occupied on another page passed: %v", err)
	}
}

func composeUnwiredApplyFixture(t *testing.T) (*schCompositionPlan, map[string]any) {
	t.Helper()
	p, env := composeApplyFixture(t, true)
	r := env["result"].(map[string]any)
	var parts []any
	for _, entry := range r["components"].([]any) {
		c := entry.(map[string]any)
		if c["componentType"] != "sheet" && c["componentType"] != "part" {
			continue
		}
		if c["componentType"] == "part" {
			for _, entry := range c["pins"].([]any) {
				pin := entry.(map[string]any)
				pin["net"], pin["noConnected"] = "", false
			}
		}
		parts = append(parts, c)
	}
	r["components"], r["count"], r["wires"] = parts, len(parts), []any{}
	r["connectivitySummary"] = map[string]any{"scope": "activePage", "wires": 0.0, "buses": 0.0, "netflags": 0.0, "netports": 0.0, "netlabels": 0.0, "shortSymbols": 0.0}
	return p, env
}

func TestComposeApplyReusesVerifiedUnwiredPartsAfterNCFailure(t *testing.T) {
	for _, someNCPersisted := range []bool{false, true} {
		t.Run(fmt.Sprintf("some-NC-persisted-%t", someNCPersisted), func(t *testing.T) {
			p, env := composeUnwiredApplyFixture(t)
			r := env["result"].(map[string]any)
			if someNCPersisted {
				r["components"].([]any)[1].(map[string]any)["pins"].([]any)[2].(map[string]any)["noConnected"] = true
			}
			pb, err := schCompositionPlaybook(p, composeApplyBytes(t, env), true)
			if err != nil {
				t.Fatal(err)
			}
			if !pb.RequireFullExecution || pb.Defaults.Retry == nil || *pb.Defaults.Retry != 0 {
				t.Fatal("recovery must replay all guards, never resume past evidence or blindly retry writes")
			}
			baselineIndex, baseline := composeStep(t, pb, "verify-source-before-reset")
			pinIndex, _ := composeStep(t, pb, "verify-physical-pins-before-wiring")
			saveIndex, _ := composeStep(t, pb, "save-placed-parts")
			if baselineIndex >= pinIndex || pinIndex >= saveIndex || baseline.ExpectSchematic == nil || baseline.ExpectSchematic.Drawing == nil {
				t.Fatal("recovery must preserve the complete baseline plus observed empty drawing before the checkpoint save")
			}
			if baseline.Payload["includeConnectivitySummary"] != true || baseline.Assert["$.connectivitySummary.scope"] != "==activePage" || baseline.Assert["$.connectivitySummary.wires"] != "==0" || baseline.Assert["$.connectivitySummary.buses"] != "==0" {
				t.Fatal("unwired recovery must re-read and assert the active page's wire/bus inventory at execution time")
			}
			if err := baseline.ExpectSchematic.check(r, nil); err != nil {
				t.Fatalf("fresh unwired baseline did not pass: %v", err)
			}
			ncCount, wireCount := 0, 0
			for i, s := range pb.Steps {
				if s.Run == "sch clear" || s.Action == "schematic.component.place" || s.Action == "schematic.component.modify" {
					t.Fatalf("verified unwired symbols were unnecessarily recreated: %+v", s)
				}
				if s.Action == "schematic.pin.set_no_connect" {
					ncCount++
					if i <= saveIndex {
						t.Fatal("NC recovery ran before verified placed parts were saved")
					}
				}
				if s.Action == "schematic.wire.create" {
					wireCount++
					if i <= saveIndex {
						t.Fatal("wires ran before recovery geometry/checkpoint gates")
					}
				}
			}
			if ncCount == 0 || wireCount != len(p.Layout.Wires) {
				t.Fatal("recovery omitted pending NC or the complete authored wire drawing")
			}
			// A wire arriving after planning must invalidate the reused baseline,
			// even if all component poses and the stale pin-net read remain equal.
			r["wires"] = []any{drawingWire(0, 0, 10, 0)}
			if err := baseline.ExpectSchematic.check(r, nil); err == nil {
				t.Fatal("concurrent wire creation did not invalidate unwired recovery")
			}
		})
	}
}

func TestComposeApplyCannotReuseUnprovenOrChangedUnwiredState(t *testing.T) {
	for _, tc := range []struct {
		name   string
		edit   func(map[string]any)
		reject bool
	}{
		{"missing-summary", func(r map[string]any) { delete(r, "connectivitySummary") }, true},
		{"null-summary", func(r map[string]any) { r["connectivitySummary"] = nil }, true},
		{"missing-wire-count", func(r map[string]any) { delete(r["connectivitySummary"].(map[string]any), "wires") }, true},
		{"wrong-summary-scope", func(r map[string]any) { r["connectivitySummary"].(map[string]any)["scope"] = "allPages" }, true},
		{"residual-wire", func(r map[string]any) {
			r["wires"] = []any{drawingWire(0, 0, 10, 0)}
			r["connectivitySummary"].(map[string]any)["wires"] = 1.0
		}, false},
		{"residual-bus", func(r map[string]any) { r["connectivitySummary"].(map[string]any)["buses"] = 1.0 }, true},
		{"residual-marker", func(r map[string]any) {
			r["components"] = append(r["components"].([]any), map[string]any{"componentType": "netflag", "primitiveId": "residual-flag", "net": "GND", "x": 0.0, "y": 0.0, "rotation": 0.0})
			r["connectivitySummary"].(map[string]any)["netflags"] = 1.0
		}, false},
		{"wrong-NC-true", func(r map[string]any) {
			r["components"].([]any)[1].(map[string]any)["pins"].([]any)[0].(map[string]any)["noConnected"] = true
		}, false},
		{"moved-part", func(r map[string]any) {
			c := r["components"].([]any)[1].(map[string]any)
			c["x"] = c["x"].(float64) + 5
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, env := composeUnwiredApplyFixture(t)
			tc.edit(env["result"].(map[string]any))
			pb, err := schCompositionPlaybook(p, composeApplyBytes(t, env), true)
			if tc.reject {
				if err == nil || pb != nil {
					t.Fatalf("incomplete source evidence compiled a clear queue: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("explicit --replace should still compile the ordinary guarded rebuild: %v", err)
			}
			composeStep(t, pb, "reset-target-preserving-sheet")
			composeStep(t, pb, "place-000")
		})
	}
}

func TestComposeApplyUnwiredRecoveryStopsOnLiveSummaryChange(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
	}{
		{"bus-added-after-plan", func(r map[string]any) { r["connectivitySummary"].(map[string]any)["buses"] = 1.0 }},
		{"wire-count-disagrees-with-empty-geometry", func(r map[string]any) { r["connectivitySummary"].(map[string]any)["wires"] = 1.0 }},
		{"scope-changed-after-plan", func(r map[string]any) { r["connectivitySummary"].(map[string]any)["scope"] = "allPages" }},
		{"summary-unavailable-at-execution", func(r map[string]any) { delete(r, "connectivitySummary") }},
		{"bus-count-unavailable-at-execution", func(r map[string]any) { delete(r["connectivitySummary"].(map[string]any), "buses") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, env := composeUnwiredApplyFixture(t)
			compiled, err := schCompositionPlaybook(p, composeApplyBytes(t, env), true)
			if err != nil {
				t.Fatal(err)
			}
			_, baseline := composeStep(t, compiled, "verify-source-before-reset")
			live := env["result"].(map[string]any)
			// Symbol geometry and the empty wire/marker drawing remain unchanged;
			// only the fresh enumeration reveals the new bus or missing evidence.
			tc.edit(live)
			b, _ := json.Marshal(live)
			cfg, daemon, cleanup := newBlockApplyTestDaemon(t, func(call blockApplyTestCall) string {
				return `{"ok":true,"result":` + string(b) + `}`
			})
			defer cleanup()
			pb := &playbook{Version: 1, Meta: playbookMeta{Name: "unwired recovery summary gate"}, Defaults: compiled.Defaults, Steps: []playbookStep{*baseline, {Action: "schematic.wire.create", Payload: map[string]any{"points": [][2]float64{{0, 0}, {20, 0}}}}}}
			var out bytes.Buffer
			runner := &applyRunner{cfg: cfg, stdout: &out, stderr: &out, pb: pb, vars: map[string]string{}, window: "w1", yes: true, journalPath: filepath.Join(t.TempDir(), "recovery.jsonl"), toIdx: 1}
			if err := runner.execute(); err == nil {
				t.Fatal("new wire/bus or unavailable live summary did not stop recovery")
			}
			calls := daemon.snapshot()
			if len(calls) != 1 || calls[0].Action != "schematic.components.list" || calls[0].Payload["includeConnectivitySummary"] != true {
				t.Fatalf("summary was not requested or a subsequent wire write escaped the gate: %+v", calls)
			}
		})
	}
}
