package app

import (
	"encoding/json"
	"testing"
)

// Model the connector's eleven-class inventory: only part pins occur in the
// pins class, while marker pins remain in the rich component read. No customer
// identities or geometry are needed to reproduce the rejected Desk snapshot.
func TestComposePartPinInventoryProtectsOwnership(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(map[string]any, map[string]any)
	}{
		{"missing class", func(_ map[string]any, page map[string]any) { delete(page, "pins") }},
		{"unknown class", func(_ map[string]any, page map[string]any) { page["future"] = []any{} }},
		{"unknown replacement class", func(_ map[string]any, page map[string]any) {
			delete(page, "pins")
			page["future"] = []any{}
		}},
		{"missing pin", func(_ map[string]any, page map[string]any) { page["pins"] = page["pins"].([]any)[1:] }},
		{"duplicate pin", func(_ map[string]any, page map[string]any) {
			pins := page["pins"].([]any)
			page["pins"] = append(pins, pins[0])
		}},
		{"unknown owner", func(_ map[string]any, page map[string]any) {
			page["pins"].([]any)[0].(map[string]any)["componentId"] = "foreign"
		}},
		{"missing owner", func(_ map[string]any, page map[string]any) {
			delete(page["pins"].([]any)[0].(map[string]any), "componentId")
		}},
		{"sheet owner", func(_ map[string]any, page map[string]any) {
			page["pins"].([]any)[0].(map[string]any)["componentId"] = "sheet"
		}},
		{"other part owner", func(r map[string]any, page map[string]any) {
			pins := page["pins"].([]any)
			first := pins[0].(map[string]any)
			for _, item := range pins {
				if row := item.(map[string]any); row["componentId"] != first["componentId"] {
					first["componentId"] = row["componentId"]
					return
				}
			}
			t.Fatal("fixture needs two parts")
		}},
		{"pin collides with component", func(_ map[string]any, page map[string]any) {
			page["pins"].([]any)[0].(map[string]any)["primitiveId"] = "sheet"
		}},
		{"rich pin mismatch", func(r map[string]any, _ map[string]any) {
			r["components"].([]any)[1].(map[string]any)["pins"].([]any)[0].(map[string]any)["primitiveId"] = "replacement"
		}},
		{"rich pin unavailable", func(r map[string]any, _ map[string]any) {
			r["components"].([]any)[1].(map[string]any)["pinsAvailable"] = false
		}},
		{"duplicate rich pin", func(r map[string]any, _ map[string]any) {
			part := r["components"].([]any)[1].(map[string]any)
			pins := part["pins"].([]any)
			part["pins"] = append(pins, pins[0])
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, env := preserveDrawingFixture(t)
			r := env["result"].(map[string]any)
			page := r["pagePrimitives"].(map[string]any)
			if _, err := schComposeProtectedPage(r); err != nil {
				t.Fatal("valid eleven-class source rejected:", err)
			}
			tc.edit(r, page)
			if _, err := schComposeProtectedPage(r); err == nil {
				t.Fatal("unsafe pin inventory accepted")
			}
		})
	}
}

func TestPreservedDrawingKeepsNativePartPinLedger(t *testing.T) {
	p, env := preserveDrawingFixture(t)
	pb, err := schCompositionPlaybook(p, preserveDrawingBytes(t, env), true, true)
	if err != nil {
		t.Fatal(err)
	}
	_, after := composeStep(t, pb, "verify-preserved-parts-after-clear")
	page := after.ExpectSchematic.PagePrimitives
	if len(page["pins"].([]any)) == 0 {
		t.Fatal("reset prediction dropped the part-pin ledger")
	}
	r := env["result"].(map[string]any)
	r["pagePrimitives"] = page
	if err := checkSchPageInventory(page, r); err != nil {
		t.Fatal(err)
	}
	raw := preserveDrawingBytes(t, env)
	for _, mutation := range []string{"lost", "owner"} {
		t.Run(mutation, func(t *testing.T) {
			var changed map[string]any
			if err := json.Unmarshal(raw, &changed); err != nil {
				t.Fatal(err)
			}
			r := changed["result"].(map[string]any)
			got := r["pagePrimitives"].(map[string]any)
			pins := got["pins"].([]any)
			if mutation == "lost" {
				got["pins"] = pins[1:]
			} else {
				pins[0].(map[string]any)["componentId"] = "replacement-owner"
			}
			if err := checkSchPageInventory(page, r); err == nil {
				t.Fatal("changed post-reset pin ledger accepted")
			}
		})
	}
}
