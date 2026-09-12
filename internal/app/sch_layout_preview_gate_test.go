package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCompletePreviewRejectsPlaceholdersRegardlessOfStatus(t *testing.T) {
	for _, status := range []string{"", "planned", "blocked"} {
		in := renderFixture(t)
		in.Zones[0].Status = status
		in.Zones[0].Layout.Wires = nil
		in.Zones[0].Layout.Flags = nil
		if validateCompleteLayoutPreview(in) == nil {
			t.Fatalf("accepted placeholder status=%q", status)
		}
	}
}

func TestPreviewCLIRequiresExplicitDiagnosticAndPreservesLastGood(t *testing.T) {
	dir := t.TempDir()
	from, out := filepath.Join(dir, "in.json"), filepath.Join(dir, "out.svg")
	in := renderFixture(t)
	good := []byte("last-good")
	in.Zones[0].Status = "planned" // The mode must still be visibly diagnostic.
	in.Zones[0].Layout.Wires = nil
	in.Zones[0].Layout.Flags = nil
	raw, _ := json.Marshal(in)
	os.WriteFile(from, raw, 0600)
	os.WriteFile(out, good, 0600)
	var buf bytes.Buffer
	c := newSchLayoutRenderCmd(&buf)
	c.SetArgs([]string{"--from", from, "--out", out})
	if c.Execute() == nil {
		t.Fatal("implicit incomplete output")
	}
	actual, _ := os.ReadFile(out)
	if !bytes.Equal(actual, good) {
		t.Fatal("overwrote last good result")
	}
	c = newSchLayoutRenderCmd(&buf)
	c.SetArgs([]string{"--from", from, "--out", out, "--diagnostic"})
	if err := c.Execute(); err != nil {
		t.Fatal(err)
	}
	actual, _ = os.ReadFile(out)
	if !bytes.Contains(actual, []byte("诊断模式")) {
		t.Fatal("diagnostic not identified")
	}
}

func TestUnifiedSpacingRawFieldsCannotBeNullOrOverride(t *testing.T) {
	for _, s := range []string{`{"padding":null}`, `{"gap":0}`, `{"padding":10}`, `{"gap":"20"}`} {
		var fields map[string]json.RawMessage
		json.Unmarshal([]byte(s), &fields)
		fields["bounds"] = json.RawMessage(`{"minX":0,"minY":0,"maxX":500,"maxY":400}`)
		fields["border"] = fields["bounds"]
		fields["keepouts"] = json.RawMessage(`[]`)
		raw, _ := json.Marshal(map[string]any{"spacing": 20, "sheet": fields})
		if validateRenderSheetJSON(raw) == nil {
			t.Fatal("accepted conflicting spacing", s)
		}
	}
	if err := validateRenderSheetJSON([]byte(`{"spacing":20,"sheet":{"bounds":{"minX":0,"minY":0,"maxX":500,"maxY":400},"border":{"minX":0,"minY":0,"maxX":500,"maxY":400},"keepouts":[]}}`)); err != nil {
		t.Fatal(err)
	}
}

func TestRenderWireCoordinatesAreNotPaddedOrTruncated(t *testing.T) {
	for _, points := range []string{`[[10],[20,0]]`, `[[10,0,7],[20,0]]`, `[[null,0],[20,0]]`, `[null,[20,0]]`} {
		raw := []byte(`{"zones":[{"layout":{"wires":[{"net":"N","points":` + points + `}]}}]}`)
		if validateRenderMeasurementsJSON(raw) == nil {
			t.Fatal("accepted invented coordinate", points)
		}
	}
}
