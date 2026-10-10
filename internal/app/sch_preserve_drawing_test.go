package app

import (
	"encoding/json"
	"maps"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func preserveDrawingFixture(t *testing.T) (*schCompositionPlan, map[string]any) {
	t.Helper()
	p, env := preserveComposeFixture(t)
	r := env["result"].(map[string]any)
	page := r["pagePrimitives"].(map[string]any)
	// Native page component inventory does not contain derived pin/net records.
	native := []any{}
	for _, item := range page["components"].([]any) {
		row := maps.Clone(item.(map[string]any))
		delete(row, "pins")
		native = append(native, row)
	}
	page["components"] = native
	part := preserveFirstPart(env)
	pin := part["pins"].([]any)[0].(map[string]any)
	for _, entry := range []struct{ id, parent string }{{"part-attr", stringVal(part["primitiveId"])}, {"pin-attr", stringVal(pin["primitiveId"])}, {"sheet-attr", "sheet"}, {"flag-attr", "flag-0"}, {"flag-pin-attr", "pin-flag-0"}} {
		page["attributes"] = append(page["attributes"].([]any), preserveAttribute(entry.id, entry.parent))
	}
	page["rectangles"] = []any{map[string]any{"primitiveId": "frame", "TopLeftX": 0.0, "TopLeftY": 50.0, "Width": 100.0, "Height": 50.0, "CornerRadius": 0.0, "Rotation": 0.0, "Color": nil, "FillColor": nil, "LineWidth": 1.0, "LineType": 0.0, "FillStyle": nil}}
	page["texts"] = []any{map[string]any{"primitiveId": "frame-title", "X": 5.0, "Y": 45.0, "Content": "Module", "Rotation": 0.0, "TextColor": nil, "FontName": "Arial", "FontSize": 8.0, "Bold": false, "Italic": false, "UnderLine": false, "AlignMode": nil}}
	return p, env
}

func preserveDrawingBytes(t *testing.T, env map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func preserveAttribute(id, parent string) map[string]any {
	return map[string]any{"primitiveId": id, "ParentPrimitiveId": parent, "X": 0.0, "Y": 0.0, "Rotation": 0.0, "Color": nil, "FontName": "Arial", "FontSize": 8.0, "Bold": false, "Italic": false, "UnderLine": false, "AlignMode": nil, "FillColor": nil, "Key": "${literal}", "Value": "={Value}", "KeyVisible": false, "ValueVisible": true}
}

func TestPreservedDrawingDeletionIsExplicitAndNeverTargetsOwnedPrimitives(t *testing.T) {
	p, env := preserveDrawingFixture(t)
	pb, err := schCompositionPlaybook(p, preserveDrawingBytes(t, env), true, true)
	if err != nil {
		t.Fatal(err)
	}
	_, step := composeStep(t, pb, "reset-drawing-preserving-instances")
	ids := step.Payload["primitiveIds"].([]string)
	want := []string{"frame", "frame-title"}
	r := env["result"].(map[string]any)
	for _, item := range r["pagePrimitives"].(map[string]any)["wires"].([]any) {
		want = append(want, stringVal(item.(map[string]any)["primitiveId"]))
	}
	for _, item := range r["components"].([]any) {
		row := item.(map[string]any)
		if row["componentType"] == "netflag" {
			want = append(want, stringVal(row["primitiveId"]))
		}
	}
	sort.Strings(want)
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("delete set: got %v, want %v", ids, want)
	}
	for _, s := range pb.Steps {
		if s.Action == "schematic.page.clear" || s.Run == "sch clear" {
			t.Fatal("broad clear in preserve queue")
		}
	}
	_, after := composeStep(t, pb, "verify-preserved-parts-after-clear")
	attrs := after.ExpectSchematic.PagePrimitives["attributes"].([]any)
	if len(attrs) != 3 {
		t.Fatal("retained owned attributes or cascade prediction wrong", attrs)
	}
}

func TestPreservedDrawingResetGuardStopsPinAttributeLossAndResidualObjects(t *testing.T) {
	p, env := preserveDrawingFixture(t)
	pb, err := schCompositionPlaybook(p, preserveDrawingBytes(t, env), true, true)
	if err != nil {
		t.Fatal(err)
	}
	_, after := composeStep(t, pb, "verify-preserved-parts-after-clear")
	// Model the official deletion of exactly the requested ids and marker attrs.
	_, del := composeStep(t, pb, "reset-drawing-preserving-instances")
	deleted := map[string]bool{}
	for _, id := range del.Payload["primitiveIds"].([]string) {
		deleted[id] = true
	}
	removedOwners := maps.Clone(deleted)
	for _, item := range env["result"].(map[string]any)["components"].([]any) {
		row := item.(map[string]any)
		if deleted[stringVal(row["primitiveId"])] {
			for _, item := range row["pins"].([]any) {
				removedOwners[stringVal(item.(map[string]any)["primitiveId"])] = true
			}
		}
	}
	r := env["result"].(map[string]any)
	page := r["pagePrimitives"].(map[string]any)
	for kind, value := range page {
		kept := []any{}
		for _, item := range value.([]any) {
			row := item.(map[string]any)
			if !deleted[stringVal(row["primitiveId"])] && !(kind == "attributes" && removedOwners[stringVal(row["ParentPrimitiveId"])]) {
				kept = append(kept, row)
			}
		}
		page[kind] = kept
	}
	parts := []any{}
	for _, item := range r["components"].([]any) {
		row := item.(map[string]any)
		if deleted[stringVal(row["primitiveId"])] {
			continue
		}
		if row["componentType"] == "part" {
			for _, item := range row["pins"].([]any) {
				item.(map[string]any)["net"] = ""
			}
		}
		parts = append(parts, row)
	}
	r["components"], r["wires"] = parts, []any{}
	for key := range r["connectivitySummary"].(map[string]any) {
		if key != "scope" {
			r["connectivitySummary"].(map[string]any)[key] = 0.0
		}
	}
	if err := after.ExpectSchematic.check(r, map[string]string{"literal": "do-not-substitute"}); err != nil {
		t.Fatal("unchanged residual rejected", err)
	}
	raw, _ := json.Marshal(r)
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
	}{
		{"lost pin data", func(r map[string]any) {
			r["components"].([]any)[1].(map[string]any)["pins"].([]any)[0].(map[string]any)["otherProperty"] = map[string]any{}
		}},
		{"changed pin id", func(r map[string]any) {
			r["components"].([]any)[1].(map[string]any)["pins"].([]any)[0].(map[string]any)["primitiveId"] = "replacement"
		}},
		{"lost NC", func(r map[string]any) {
			q := r["components"].([]any)[1].(map[string]any)["pins"].([]any)[0].(map[string]any)
			q["noConnected"] = !q["noConnected"].(bool)
		}},
		{"lost owned attr", func(r map[string]any) { r["pagePrimitives"].(map[string]any)["attributes"] = []any{} }},
		{"residual frame", func(r map[string]any) {
			r["pagePrimitives"].(map[string]any)["rectangles"] = []any{map[string]any{"primitiveId": "survivor"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var changed map[string]any
			if err := json.Unmarshal(raw, &changed); err != nil {
				t.Fatal(err)
			}
			tc.edit(changed)
			if err := after.ExpectSchematic.check(changed, nil); err == nil {
				t.Fatal("unsafe post-delete state accepted")
			}
		})
	}
}

func TestPreservedDrawingRejectsUnknownClassesAndOrphanAttributes(t *testing.T) {
	for _, kind := range []string{"buses", "arcs", "circles", "polygons", "objects"} {
		t.Run(kind, func(t *testing.T) {
			p, env := preserveDrawingFixture(t)
			preserved, err := prepareSchPreservedParts(p, preserveDrawingBytes(t, env), false)
			if err != nil {
				t.Fatal(err)
			}
			page := env["result"].(map[string]any)["pagePrimitives"].(map[string]any)
			page[kind] = []any{map[string]any{"primitiveId": "unsupported"}}
			if _, _, err := schPreservedDrawingReset(page, preserved); err == nil {
				t.Fatal("unsupported drawing accepted")
			}
		})
	}
	p, env := preserveDrawingFixture(t)
	page := env["result"].(map[string]any)["pagePrimitives"].(map[string]any)
	page["attributes"] = append(page["attributes"].([]any), preserveAttribute("orphan", "unknown-parent"))
	if _, err := schCompositionPlaybook(p, preserveDrawingBytes(t, env), true, true); err == nil || !strings.Contains(err.Error(), "attribute") {
		t.Fatal("orphan attribute accepted", err)
	}
	delete(preserveFirstPart(env)["pins"].([]any)[0].(map[string]any), "otherProperty")
	if _, err := schCompositionPlaybook(p, preserveDrawingBytes(t, env), true, true); err == nil || !strings.Contains(err.Error(), "otherProperty") {
		t.Fatal("incomplete pin data accepted", err)
	}
}

func TestPreservedOwnedAttributesSurviveJSONAndPermitOnlyPoseChanges(t *testing.T) {
	p, env := preserveDrawingFixture(t)
	pb, err := schCompositionPlaybook(p, preserveDrawingBytes(t, env), true, true)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(pb)
	var loaded playbook
	if err := json.Unmarshal(raw, &loaded); err != nil {
		t.Fatal(err)
	}
	_, before := composeStep(t, &loaded, "verify-source-before-reset")
	if err := before.ExpectSchematic.check(env["result"], map[string]string{"literal": "changed"}); err != nil {
		t.Fatal(err)
	}
	guard := before.ExpectSchematic.OwnedAttributes
	attrs := env["result"].(map[string]any)["pagePrimitives"].(map[string]any)["attributes"].([]any)
	attr := attrs[0].(map[string]any)
	attr["X"], attr["Y"], attr["Rotation"] = 100.0, 200.0, 90.0
	if err := guard.check(env["result"].(map[string]any)); err != nil {
		t.Fatal("owner pose rejected", err)
	}
	attr["ValueVisible"] = false
	if err := guard.check(env["result"].(map[string]any)); err == nil {
		t.Fatal("visibility loss accepted")
	}
}
