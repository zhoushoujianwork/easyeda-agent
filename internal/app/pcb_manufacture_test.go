package app

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const manufactureGerber = "G04 test*\n%FSLAX45Y45*%\n%MOMM*%\nM02*\n"
const manufactureCSV = "Designator,Footprint,Mid X(mm),Mid Y(mm),Layer,Rotation\r\nR1,0603,10.25,-2.5,TopLayer,90\r\n"

func manufactureZIP(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for name, data := range files {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = io.WriteString(w, data); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestManufactureGerberInventoryAndArchiveGuards(t *testing.T) {
	data := manufactureZIP(t, map[string]string{"board/Top.GTL": manufactureGerber, "board/Outline.GKO": manufactureGerber, "Drill.DRL": "M48\nMETRIC,TZ\nM30\n"})
	entries, err := inspectGerberArchive(data)
	if err != nil || len(entries) != 3 {
		t.Fatal(entries, err)
	}
	for _, e := range entries {
		if len(e.SHA256) != 64 || e.Size <= 0 || e.Kind == "" {
			t.Fatal("missing entry evidence", e)
		}
	}
	for _, files := range []map[string]string{{"../Top.GTL": manufactureGerber}, {"/Top.GTL": manufactureGerber}, {"C:Top.GTL": manufactureGerber}, {"a\\b.GTL": manufactureGerber}, {"Top.GTL": "<html>export failed</html>"}, {"README.txt": "no gerber"}} {
		if _, err := inspectGerberArchive(manufactureZIP(t, files)); err == nil {
			t.Fatal("accepted invalid archive", files)
		}
	}
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	h := &zip.FileHeader{Name: "large.GTL", Method: zip.Store}
	h.UncompressedSize64 = 101 << 20
	if _, err = z.CreateRaw(h); err != nil {
		t.Fatal(err)
	}
	z.Close()
	if _, _, err := readManufactureZip(b.Bytes()); err == nil {
		t.Fatal("accepted oversized ZIP expansion")
	}
	b.Reset()
	z = zip.NewWriter(&b)
	for _, name := range []string{"a/Top.GTL", "a/./Top.GTL"} {
		w, _ := z.Create(name)
		io.WriteString(w, manufactureGerber)
	}
	z.Close()
	if _, _, err := readManufactureZip(b.Bytes()); err == nil {
		t.Fatal("accepted duplicate normalized ZIP path")
	}
}

func TestManufacturePlacementCSVFieldsAndFailures(t *testing.T) {
	rows, err := inspectPlacement([]byte("\ufeff"+manufactureCSV), "csv", "mm")
	if err != nil || len(rows) != 1 || rows[0].Designator != "R1" || rows[0].MidX != 10.25 || rows[0].MidY != -2.5 || rows[0].Rotation != 90 || rows[0].Layer != "TopLayer" {
		t.Fatal(rows, err)
	}
	for _, bad := range []string{"", "Designator,Mid X,Mid Y,Layer,Rotation\n", strings.Replace(manufactureCSV, "10.25", "NaN", 1), strings.Replace(manufactureCSV, "TopLayer", "", 1), manufactureCSV + "R1,0603,0,0,BottomLayer,0\n", strings.Replace(manufactureCSV, "Rotation", "Unknown", 1)} {
		if _, err := inspectPlacement([]byte(bad), "csv", "mm"); err == nil {
			t.Fatal("accepted invalid placement CSV", bad)
		}
	}
	if _, err := inspectPlacement([]byte(manufactureCSV), "csv", "mil"); err == nil {
		t.Fatal("accepted conflicting output unit")
	}
	for _, sep := range []string{";", "\t"} {
		if _, err := inspectPlacement([]byte(strings.ReplaceAll(manufactureCSV, ",", sep)), "csv", "mm"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestManufacturePlacementXLSXInlineAndSharedStrings(t *testing.T) {
	for _, shared := range []bool{false, true} {
		files := map[string]string{"[Content_Types].xml": "<Types/>", "xl/workbook.xml": "<workbook/>"}
		header := "<row>"
		labels := []string{"Designator", "Mid X", "Mid Y", "Layer", "Rotation"}
		for i, label := range labels {
			header += "<c r=\"" + string(rune('A'+i)) + "1\" t=\"inlineStr\"><is><t>" + label + "</t></is></c>"
		}
		header += "</row>"
		ref := "<c r=\"A2\" t=\"inlineStr\"><is><t>R1</t></is></c>"
		if shared {
			files["xl/sharedStrings.xml"] = "<sst><si><r><t>R</t></r><r><t>1</t></r></si></sst>"
			ref = "<c r=\"A2\" t=\"s\"><v>0</v></c>"
		}
		files["xl/worksheets/sheet1.xml"] = "<worksheet><sheetData>" + header + "<row>" + ref + "<c r=\"B2\"><v>1.25</v></c><c r=\"C2\"><v>-2</v></c><c r=\"D2\" t=\"inlineStr\"><is><t>BottomLayer</t></is></c><c r=\"E2\"><v>270</v></c></row></sheetData></worksheet>"
		rows, err := inspectPlacement(manufactureZIP(t, files), "xlsx", "mm")
		if err != nil || len(rows) != 1 || rows[0].MidX != 1.25 || rows[0].Rotation != 270 || rows[0].Designator != "R1" {
			t.Fatal(rows, err)
		}
		files["xl/worksheets/sheet1.xml"] = strings.Replace(files["xl/worksheets/sheet1.xml"], "<v>1.25</v>", "<f>1+1</f><v>1.25</v>", 1)
		if _, err := inspectPlacement(manufactureZIP(t, files), "xlsx", "mm"); err == nil {
			t.Fatal("silently trusted a cached formula")
		}
	}
}

func TestManufactureArtifactIntegrityAndAtomicNoOverwrite(t *testing.T) {
	data := []byte(manufactureCSV)
	p := filepath.Join(t.TempDir(), "artifact.csv")
	os.WriteFile(p, data, 0600)
	res := &actionResult{OK: true, Result: map[string]any{"artifactId": "a", "fileType": "csv", "unit": "mm", "size": float64(len(data)), "fileSha256": sha256Hex(data)}, Artifacts: []artifactRef{{ID: "a", Path: p, Size: int64(len(data)), SHA256: sha256Hex(data)}}}
	if got, err := verifyManufactureArtifact(res, "csv", "mm"); err != nil || !bytes.Equal(got, data) {
		t.Fatal(err)
	}
	res.Artifacts[0].SHA256 = "bad"
	if _, err := verifyManufactureArtifact(res, "csv", "mm"); err == nil {
		t.Fatal("accepted altered artifact")
	}
	res.Artifacts[0].SHA256 = sha256Hex(data)
	res.Result["size"] = float64(1)
	if _, err := verifyManufactureArtifact(res, "csv", "mm"); err == nil {
		t.Fatal("ignored getter size mismatch")
	}
	out := filepath.Join(t.TempDir(), "new.csv")
	if err := writeNewManufactureFile(out, data); err != nil {
		t.Fatal(err)
	}
	if err := writeNewManufactureFile(out, []byte("overwrite")); err == nil {
		t.Fatal("overwrote existing output")
	}
	got, _ := os.ReadFile(out)
	if !bytes.Equal(got, data) {
		t.Fatal("modified published bytes")
	}
}

func TestManufactureCLIRejectsInvalidOptionsBeforeDispatch(t *testing.T) {
	for _, sample := range []struct {
		kind string
		args []string
	}{{"gerber", []string{"--window", "w"}}, {"gerber", []string{"--out", "out.zip", "--unit", "mil"}}, {"gerber", []string{"--out", "out.zip", "--digital-format", "0:7"}}, {"placement", []string{"--out", "out.csv", "--format", "pdf"}}, {"placement", []string{"--out", "out.csv", "--unit", "inch"}}} {
		window := "w"
		cmd := newPcbManufactureExportCmd(&appConfig{}, &window, io.Discard, io.Discard, sample.kind)
		// window is normally inherited from the pcb group.
		cmd.Flags().StringVar(&window, "window", window, "window")
		cmd.SetArgs(sample.args)
		if err := cmd.Execute(); err == nil {
			t.Fatal("invalid CLI accepted", sample)
		}
	}
}
