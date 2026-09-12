package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLayoutOptimizationJSONRequiresExplicitValidSettings(t *testing.T) {
	for _, value := range []string{`null`, `[]`, `{"maxVariants":null}`, `{"maxVariants":0}`, `{"maxVariants":5}`, `{"maxVariants":1.5}`, `{"maxAttempts":0}`, `{"maxAttempts":65}`, `{"maxAttempts":"24"}`, `{"maxAttemps":24}`} {
		t.Run(value, func(t *testing.T) {
			raw, _ := json.Marshal(standaloneLayoutFixture())
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(raw, &fields)
			fields["optimization"] = json.RawMessage(value)
			raw, _ = json.Marshal(fields)
			if _, err := decodeSchematicLayoutInput(raw); err == nil {
				t.Fatal("invalid optimization accepted")
			}
		})
	}
	for _, value := range []string{`null`, `[]`, `[0,null]`, `[0,0]`, `[45]`, `[-90]`, `[360]`, `["90"]`} {
		t.Run("rotations="+value, func(t *testing.T) {
			raw, _ := json.Marshal(standaloneLayoutFixture())
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(raw, &fields)
			var components []map[string]json.RawMessage
			_ = json.Unmarshal(fields["components"], &components)
			components[1]["allowedRotations"] = json.RawMessage(value)
			fields["components"], _ = json.Marshal(components)
			raw, _ = json.Marshal(fields)
			if _, err := decodeSchematicLayoutInput(raw); err == nil {
				t.Fatal("invalid rotation authority accepted")
			}
		})
	}
}

func TestVariantRawCoordinatesCannotBeInventedForUnselectedAlternative(t *testing.T) {
	for _, target := range []string{"pin", "body", "text", "frame", "bounds", "title"} {
		t.Run(target, func(t *testing.T) {
			raw, _ := json.Marshal(variantSafetyFixture(t))
			var fields map[string]any
			_ = json.Unmarshal(raw, &fields)
			z := fields["zones"].([]any)[0].(map[string]any)
			v := z["variants"].([]any)[1].(map[string]any)
			c := v["layout"].(map[string]any)["placements"].([]any)[1].(map[string]any)
			switch target {
			case "pin":
				delete(c["pins"].([]any)[0].(map[string]any), "x")
			case "body":
				delete(c["bbox"].(map[string]any), "minX")
			case "text":
				delete(c["textBboxes"].([]any)[0].(map[string]any), "minX")
			case "frame":
				delete(v["frame"].(map[string]any)["rect"].(map[string]any), "minX")
			case "bounds":
				delete(v["contentBounds"].(map[string]any), "minX")
			case "title":
				delete(v["frame"].(map[string]any), "titleX")
			}
			raw, _ = json.Marshal(fields)
			if err := validateRenderMeasurementsJSON(raw); err == nil {
				t.Fatal("invented missing candidate coordinate")
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
					t.Fatal("render hid invalid unselected variant")
				}
				got, err := os.ReadFile(out)
				if err != nil || !bytes.Equal(good, got) {
					t.Fatal("failed render damaged prior output", err)
				}
			}
		})
	}
}
