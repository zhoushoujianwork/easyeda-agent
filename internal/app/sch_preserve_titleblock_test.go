package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPreservedTitleBlockChangesOnlyAuthoredSheetFieldsAfterWrite(t *testing.T) {
	p, env := preserveDrawingFixture(t)
	r := env["result"].(map[string]any)
	attrs := r["pagePrimitives"].(map[string]any)["attributes"].([]any)
	var sheet map[string]any
	for _, item := range attrs {
		row := item.(map[string]any)
		if row["primitiveId"] == "sheet-attr" {
			sheet = row
			row["Key"], row["Value"], row["KeyVisible"], row["ValueVisible"] = "Name", "old", nil, nil
		}
	}
	hide, show := false, true
	p.TitleBlock = schCompositionTitleBlock{"Name": {Value: "LED board", ShowTitle: &hide, ShowValue: &show}}
	pb, err := schCompositionPlaybook(p, preserveDrawingBytes(t, env), true, true)
	if err != nil {
		t.Fatal(err)
	}
	_, before := composeStep(t, pb, "verify-source-before-reset")
	_, after := composeStep(t, pb, "verify-all-pins-nets-nc")
	_, saved := composeStep(t, pb, "verify-saved-instance-preservation")
	if err := before.ExpectSchematic.OwnedAttributes.check(r); err != nil {
		t.Fatal("original guard was modified", err)
	}
	sheet["Value"], sheet["KeyVisible"], sheet["ValueVisible"] = "LED board", false, true
	if err := before.ExpectSchematic.OwnedAttributes.check(r); err == nil {
		t.Fatal("pre-write guard accepted a changed source")
	}
	for _, guard := range []*schOwnedAttributesExpectation{after.ExpectSchematic.OwnedAttributes, saved.ExpectSchematic.OwnedAttributes} {
		if err := guard.check(r); err != nil {
			t.Fatal("authored metadata update rejected", err)
		}
	}
	raw, _ := json.Marshal(r)
	for _, field := range []string{"Value", "ValueVisible", "FontName", "ParentPrimitiveId"} {
		t.Run(field, func(t *testing.T) {
			var changed map[string]any
			json.Unmarshal(raw, &changed)
			for _, item := range changed["pagePrimitives"].(map[string]any)["attributes"].([]any) {
				row := item.(map[string]any)
				if row["primitiveId"] == "sheet-attr" {
					if field == "ValueVisible" {
						row[field] = false
					} else {
						row[field] = "unexpected"
					}
				}
			}
			if err := after.ExpectSchematic.OwnedAttributes.check(changed); err == nil {
				t.Fatal("unplanned attribute change accepted")
			}
		})
	}
}

func TestPreservedTitleBlockRejectsUnboundOrUnknownNativeFields(t *testing.T) {
	for _, mode := range []string{"missing", "ambiguous", "unknown-visibility"} {
		t.Run(mode, func(t *testing.T) {
			p, env := preserveDrawingFixture(t)
			page := env["result"].(map[string]any)["pagePrimitives"].(map[string]any)
			row := preserveAttribute("sheet-name", "sheet")
			row["Key"], row["KeyVisible"] = "Name", nil
			if mode != "missing" {
				page["attributes"] = append(page["attributes"].([]any), row)
			}
			if mode == "ambiguous" {
				other := preserveAttribute("sheet-name-2", "sheet")
				other["Key"] = "Name"
				page["attributes"] = append(page["attributes"].([]any), other)
			}
			hide, show := false, true
			field := schCompositionTitleBlockField{Value: "new", ShowTitle: &hide, ShowValue: &show}
			if mode == "unknown-visibility" {
				field.ShowTitle = nil
			}
			p.TitleBlock = schCompositionTitleBlock{"Name": field}
			_, err := schCompositionPlaybook(p, preserveDrawingBytes(t, env), true, true)
			if err == nil || !strings.Contains(err.Error(), "titleBlock") {
				t.Fatal("unsafe field accepted", err)
			}
		})
	}
}

func TestPreservedTitleBlockMatchingPageKeepsPrewriteExpectation(t *testing.T) {
	p, env := preserveDrawingFixture(t)
	part := preserveFirstPart(env)
	part["x"] = part["x"].(float64) - 5
	r := env["result"].(map[string]any)
	for _, item := range r["pagePrimitives"].(map[string]any)["attributes"].([]any) {
		row := item.(map[string]any)
		if row["primitiveId"] == "sheet-attr" {
			row["Key"], row["Value"] = "Name", "old"
		}
	}
	hide, show := false, true
	p.TitleBlock = schCompositionTitleBlock{"Name": {Value: "new", ShowTitle: &hide, ShowValue: &show}}
	pb, err := schCompositionPlaybook(p, preserveDrawingBytes(t, env), true, true)
	if err != nil {
		t.Fatal(err)
	}
	_, before := composeStep(t, pb, "verify-existing-composition")
	_, after := composeStep(t, pb, "verify-all-pins-nets-nc")
	if err := before.ExpectSchematic.OwnedAttributes.check(r); err != nil {
		t.Fatal("matching-page guard used the future metadata", err)
	}
	if err := after.ExpectSchematic.OwnedAttributes.check(r); err == nil {
		t.Fatal("post-write guard accepted old metadata")
	}
}
