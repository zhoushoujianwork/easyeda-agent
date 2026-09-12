package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSheetFlowCLISelectionAndPersistence(t *testing.T) {
	for _, tc := range []struct {
		name, input, override, want string
	}{
		{"default", "", "", "z"},
		{"input compact", "compact", "", "compact"},
		{"override compact", "z", "compact", "compact"},
		{"override z", "compact", "z", "z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			from, out := filepath.Join(dir, "input.json"), filepath.Join(dir, "pages.json")
			in := sheetFixture(t)
			in.Sheet.Flow = tc.input
			raw, err := json.Marshal(in)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(from, raw, 0600); err != nil {
				t.Fatal(err)
			}
			args := []string{"--from", from, "--out", out}
			if tc.override != "" {
				args = append(args, "--flow", tc.override)
			}
			c := newSchLayoutSheetPlanCmd(&bytes.Buffer{})
			c.SetArgs(args)
			if err = c.Execute(); err != nil {
				t.Fatal(err)
			}
			result, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			var plan SchematicSheetsPreview
			if err = json.Unmarshal(result, &plan); err != nil {
				t.Fatal(err)
			}
			if len(plan.Pages) != 1 || plan.Pages[0].Sheet.Flow != tc.want {
				t.Fatalf("flow not persisted: %s", result)
			}
			if _, err = RenderSchematicLayoutSVG(plan.Pages[0]); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSheetFlowCLIRejectsInvalidInputWithoutOverwriting(t *testing.T) {
	for _, bad := range []string{`null`, `""`, `"snake"`, `true`, `12`, `{}`} {
		t.Run(bad, func(t *testing.T) {
			dir := t.TempDir()
			from, out := filepath.Join(dir, "input.json"), filepath.Join(dir, "pages.json")
			raw, _ := json.Marshal(sheetFixture(t))
			var input map[string]json.RawMessage
			json.Unmarshal(raw, &input)
			var sheet map[string]json.RawMessage
			json.Unmarshal(input["sheet"], &sheet)
			sheet["flow"] = json.RawMessage(bad)
			input["sheet"], _ = json.Marshal(sheet)
			raw, _ = json.Marshal(input)
			if err := os.WriteFile(from, raw, 0600); err != nil {
				t.Fatal(err)
			}
			good := []byte("last-good")
			if err := os.WriteFile(out, good, 0600); err != nil {
				t.Fatal(err)
			}
			// A valid override cannot silently hide malformed source data.
			c := newSchLayoutSheetPlanCmd(&bytes.Buffer{})
			c.SetArgs([]string{"--from", from, "--out", out, "--flow", "z"})
			if c.Execute() == nil {
				t.Fatal("accepted invalid source flow", bad)
			}
			actual, _ := os.ReadFile(out)
			if !bytes.Equal(actual, good) {
				t.Fatal("overwrote last good output")
			}
		})
	}
	dir := t.TempDir()
	from, out := filepath.Join(dir, "in.json"), filepath.Join(dir, "out.json")
	raw, _ := json.Marshal(sheetFixture(t))
	if err := os.WriteFile(from, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, []byte("last-good"), 0600); err != nil {
		t.Fatal(err)
	}
	c := newSchLayoutSheetPlanCmd(&bytes.Buffer{})
	c.SetArgs([]string{"--from", from, "--out", out, "--flow", "snake"})
	if c.Execute() == nil {
		t.Fatal("accepted invalid --flow")
	}
	actual, _ := os.ReadFile(out)
	if string(actual) != "last-good" {
		t.Fatal("overwrote output on invalid flag")
	}
}
