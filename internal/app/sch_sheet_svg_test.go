package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An anonymized structural fixture from the 4.1.60 official exporter. Dimensions
// are deliberately unlike A4; viewport, resistor and text are irrelevant geometry.
const sheetSVGFixture = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="-999 -999 9999 9999">
<style> *{stroke-linejoin: round;stroke-linecap: round}</style>
<g class="shapeBox"><g c_partid="sheet">
<g><rect x="100" y="-920" width="1500" height="900"/></g>
<g><rect x="120" y="-900" width="1460" height="860"/></g>
<polyline points="300 -920 300 -900"/>
<g c_partid="table"><g id="table-fill-container">
<rect x="1080" y="-200" width="100" height="160"/>
<rect x="1180" y="-200" width="400" height="80"/>
<rect x="1180" y="-120" width="400" height="80"/>
</g><text transform="rotate(0)">Label</text></g>
</g><g c_partid="component"><rect x="700" y="-500" width="20" height="10"/></g></g></svg>`

func TestSheetSVGExactGeometry(t *testing.T) {
	g, err := parseSheetGeometrySVG([]byte(sheetSVGFixture))
	if err != nil {
		t.Fatal(err)
	}
	if *g.Sheet.BBox != (layoutBBox{MinX: 100, MinY: 20, MaxX: 1600, MaxY: 920}) {
		t.Fatalf("outer: %+v", g.Sheet.BBox)
	}
	if *g.Sheet.InnerBBox != (layoutBBox{MinX: 120, MinY: 40, MaxX: 1580, MaxY: 900}) {
		t.Fatalf("inner: %+v", g.Sheet.InnerBBox)
	}
	if *g.TitleBlock.BBox != (layoutBBox{MinX: 1080, MinY: 40, MaxX: 1580, MaxY: 200}) {
		t.Fatalf("table: %+v", g.TitleBlock.BBox)
	}
	if g.SourceSha256 != fmt.Sprintf("%x", sha256.Sum256([]byte(sheetSVGFixture))) || g.TitleBlock.Source != "official-svg-vectors" || len(g.Keepouts) != 1 {
		t.Fatalf("provenance: %+v", g)
	}
}

func TestSheetSVGRejectsUnmeasuredGeometry(t *testing.T) {
	wrapped := func(tag, attrs string) string {
		s := strings.Replace(sheetSVGFixture, `<g class="shapeBox">`, `<`+tag+` `+attrs+`><g class="shapeBox">`, 1)
		return strings.TrimSuffix(s, `</svg>`) + `</` + tag + `></svg>`
	}
	cases := map[string]string{
		"missing sheet":      strings.ReplaceAll(sheetSVGFixture, `c_partid="sheet"`, `c_partid="other"`),
		"missing table":      strings.ReplaceAll(sheetSVGFixture, `c_partid="table"`, `c_partid="other"`),
		"ancestor transform": strings.ReplaceAll(sheetSVGFixture, `class="shapeBox"`, `class="shapeBox" transform="translate(1,2)"`),
		"cell transform":     strings.ReplaceAll(sheetSVGFixture, `id="table-fill-container"`, `id="table-fill-container" transform="scale(2)"`),
		"hidden":             strings.ReplaceAll(sheetSVGFixture, `c_partid="sheet"`, `c_partid="sheet" style="display:none"`),
		"css":                strings.ReplaceAll(sheetSVGFixture, `stroke-linejoin: round;stroke-linecap: round`, `display:none`),
		"css geometry":       strings.ReplaceAll(sheetSVGFixture, `x="120"`, `x="120" style="width:40px"`),
		"nested viewport":    wrapped("svg", `x="40" viewBox="0 0 100 100"`),
		"unrendered defs":    wrapped("defs", ""),
		"unrendered symbol":  wrapped("symbol", ""),
		"duplicate cells":    strings.ReplaceAll(sheetSVGFixture, `</g><text`, `</g><g id="table-fill-container"><rect x="500" y="-200" width="100" height="100"/></g><text`),
		"nested cells":       strings.ReplaceAll(strings.ReplaceAll(sheetSVGFixture, `<g id="table-fill-container">`, `<g><g id="table-fill-container">`), `</g><text`, `</g></g><text`),
		"duplicate sheet":    strings.ReplaceAll(sheetSVGFixture, `c_partid="component"`, `c_partid="sheet"`),
		"extra border":       strings.ReplaceAll(sheetSVGFixture, `<polyline points="300 -920 300 -900"/>`, `<rect x="125" y="-800" width="100" height="100"/>`),
		"missing border":     strings.ReplaceAll(sheetSVGFixture, `<g><rect x="120" y="-900" width="1460" height="860"/></g>`, ``),
		"non-nested":         strings.ReplaceAll(sheetSVGFixture, `x="120"`, `x="80"`),
		"table outside":      strings.ReplaceAll(sheetSVGFixture, `x="1080"`, `x="10"`),
		"nonfinite":          strings.ReplaceAll(sheetSVGFixture, `width="1500"`, `width="NaN"`),
		"unit suffix":        strings.ReplaceAll(sheetSVGFixture, `width="1500"`, `width="1500px"`),
		"rounded":            strings.ReplaceAll(sheetSVGFixture, `width="1500"`, `rx="4" width="1500"`),
		"extra root":         sheetSVGFixture + `<svg/>`,
		"malformed":          "<svg>",
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseSheetGeometrySVG([]byte(data)); err == nil {
				t.Fatal("accepted unsupported geometry")
			}
		})
	}
}

func TestSheetSVGCommandIsOffline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sheet.svg")
	if err := os.WriteFile(path, []byte(sheetSVGFixture), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cmd := newSchCmd(&appConfig{}, &out, &out)
	cmd.SetArgs([]string{"sheet-geometry", "--from-svg", path, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		OK     bool          `json:"ok"`
		Result sheetGeometry `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.OK || envelope.Result.Sheet.InnerBBox == nil {
		t.Fatalf("bad output: %s", out.String())
	}
}

func TestSheetSVGEmptyPathDoesNotFallBackToLive(t *testing.T) {
	var out bytes.Buffer
	cmd := newSchCmd(&appConfig{}, &out, &out)
	cmd.SetArgs([]string{"sheet-geometry", "--from-svg", ""})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "non-empty file path") {
		t.Fatalf("wanted offline path rejection, got %v", err)
	}
}
