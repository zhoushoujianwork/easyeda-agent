package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Keep the otherwise-valid main title/bounds on literal zero coordinates. A
// missing/null raw field would deserialize to exactly the accepted geometry,
// so post-decode comparison alone cannot establish measured-field presence.
func sheetVariantZeroCoordinateFixture(t *testing.T) SchematicRenderInput {
	t.Helper()
	in := variantSafetyFixture(t)
	z := &in.Zones[0]
	dx, dy := -z.Frame.TitleX, -z.ContentBounds.MinY
	for i := range z.Variants {
		v := &z.Variants[i]
		for j, p := range v.Layout.Placements {
			v.Layout.Placements[j] = plTranslate(p, dx, dy)
		}
		refreshTestVariantFrame(t, z.ID, z.Title, v)
	}
	z.Layout, z.Frame, z.ContentBounds = z.Variants[0].Layout, &z.Variants[0].Frame, &z.Variants[0].ContentBounds
	if z.Frame.TitleX != 0 || z.ContentBounds.MinY != 0 {
		t.Fatalf("fixture lost intended zero fields: titleX=%g minY=%g", z.Frame.TitleX, z.ContentBounds.MinY)
	}
	if err := validateSchematicZoneVariants(in); err != nil {
		t.Fatal(err)
	}
	return in
}

func TestSheetVariantRawMainZeroCoordinatesCannotBeInvented(t *testing.T) {
	for _, target := range []string{"titleX", "boundsMinY"} {
		for _, mode := range []string{"missing", "null"} {
			t.Run(target+"/"+mode, func(t *testing.T) {
				in := sheetVariantZeroCoordinateFixture(t)
				raw, _ := json.Marshal(in)
				var fields map[string]any
				_ = json.Unmarshal(raw, &fields)
				zone := fields["zones"].([]any)[0].(map[string]any)
				box, key := zone["frame"].(map[string]any), "titleX"
				if target == "boundsMinY" {
					box, key = zone["contentBounds"].(map[string]any), "minY"
				}
				if mode == "missing" {
					delete(box, key)
				} else {
					box[key] = nil
				}
				raw, _ = json.Marshal(fields)
				var decoded SchematicRenderInput
				if err := json.Unmarshal(raw, &decoded); err != nil {
					t.Fatal(err)
				}
				if err := validateSchematicZoneVariants(decoded); err != nil {
					t.Fatal("fixture no longer demonstrates the raw-zero ambiguity", err)
				}
				assertSheetVariantRawRejected(t, raw)
			})
		}
	}
}

func TestSheetVariantRawRotationAuthorityRejectsNullAngles(t *testing.T) {
	for _, target := range []string{"main", "unselected"} {
		for _, mode := range []string{"null-angle", "null-array"} {
			t.Run(target+"/"+mode, func(t *testing.T) {
				raw, _ := json.Marshal(variantSafetyFixture(t))
				var fields map[string]any
				_ = json.Unmarshal(raw, &fields)
				zone := fields["zones"].([]any)[0].(map[string]any)
				layout := zone["layout"].(map[string]any)
				if target == "unselected" {
					layout = zone["variants"].([]any)[1].(map[string]any)["layout"].(map[string]any)
				}
				rotations := layout["allowedRotations"].(map[string]any)
				if mode == "null-angle" {
					rotations["core"] = []any{nil}
				} else {
					rotations["core"] = nil
				}
				raw, _ = json.Marshal(fields)
				if mode == "null-angle" {
					var decoded SchematicRenderInput
					_ = json.Unmarshal(raw, &decoded)
					if err := validateSchematicZoneVariants(decoded); err != nil {
						t.Fatal("fixture no longer demonstrates null becoming angle zero", err)
					}
				}
				assertSheetVariantRawRejected(t, raw)
			})
		}
	}
}

func assertSheetVariantRawRejected(t *testing.T, raw []byte) {
	t.Helper()
	if err := validateRenderMeasurementsJSON(raw); err == nil {
		t.Fatal("accepted invented raw variant data")
	}
	dir := t.TempDir()
	from, out := filepath.Join(dir, "input.json"), filepath.Join(dir, "last-good.svg")
	if err := os.WriteFile(from, raw, 0600); err != nil {
		t.Fatal(err)
	}
	good := []byte("last good artifact")
	if err := os.WriteFile(out, good, 0600); err != nil {
		t.Fatal(err)
	}
	for _, extra := range [][]string{nil, {"--zone", "zone"}, {"--zone", "zone", "--diagnostic"}} {
		var stdout bytes.Buffer
		cmd := newSchLayoutRenderCmd(&stdout)
		cmd.SetArgs(append([]string{"--from", from, "--out", out}, extra...))
		if err := cmd.Execute(); err == nil {
			t.Fatal("CLI hid incomplete variant data", extra)
		}
		got, err := os.ReadFile(out)
		if err != nil || !bytes.Equal(good, got) {
			t.Fatal("rejected render damaged last good output", err)
		}
	}
}
