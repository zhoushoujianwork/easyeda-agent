package app

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// encodeTestPNG builds a real, decodable PNG of the given pixel size — the
// dimension resolver now decodes the source file for real, so a fake byte
// sequence (a PNG magic number followed by garbage) no longer parses.
func encodeTestPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode test PNG: %v", err)
	}
	return buf.Bytes()
}

// sch image create requires --file; missing it must fail before any daemon
// round-trip.
func TestSchImageCreate_RequiresFile(t *testing.T) {
	cfg, _, cleanup := newCapturingDaemon(t)
	defer cleanup()

	var stdout, stderr bytes.Buffer
	window := "w1"
	cmd := newSchImageCmd(cfg, &window, &stdout, &stderr)
	cmd.SetArgs([]string{"create", "--x", "0", "--y", "0"})
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error when --file is missing")
	}
}

// An unsupported extension is refused before the file is even read (mirrors
// the connector's own MIME check, so a bogus format never reaches the wire).
func TestSchImageCreate_RejectsUnsupportedExtension(t *testing.T) {
	cfg, cap, cleanup := newCapturingDaemon(t)
	defer cleanup()

	dir := t.TempDir()
	path := filepath.Join(dir, "artwork.bmp")
	if err := os.WriteFile(path, []byte("not-a-real-bmp"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	var stdout, stderr bytes.Buffer
	window := "w1"
	cmd := newSchImageCmd(cfg, &window, &stdout, &stderr)
	cmd.SetArgs([]string{"create", "--file", path, "--x", "0", "--y", "0"})
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error for unsupported extension .bmp")
	}
	cap.mu.Lock()
	defer cap.mu.Unlock()
	if cap.action != "" {
		t.Errorf("expected no daemon call for a rejected extension, got action %q", cap.action)
	}
}

// A missing local file fails with a clear read error before any daemon call.
func TestSchImageCreate_MissingFile(t *testing.T) {
	cfg, cap, cleanup := newCapturingDaemon(t)
	defer cleanup()

	var stdout, stderr bytes.Buffer
	window := "w1"
	cmd := newSchImageCmd(cfg, &window, &stdout, &stderr)
	cmd.SetArgs([]string{"create", "--file", filepath.Join(t.TempDir(), "missing.png"), "--x", "0", "--y", "0"})
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error for a missing file")
	}
	cap.mu.Lock()
	defer cap.mu.Unlock()
	if cap.action != "" {
		t.Errorf("expected no daemon call for a missing file, got action %q", cap.action)
	}
}

// --dry-run parses/validates the local file and prints a preview WITHOUT
// contacting the connector — the ADR-0004 dry-run purity guarantee shared with
// pcb silk-import-svg's --dry-run.
func TestSchImageCreate_DryRunSkipsDaemon(t *testing.T) {
	cfg, cap, cleanup := newCapturingDaemon(t)
	defer cleanup()

	dir := t.TempDir()
	path := filepath.Join(dir, "module.png")
	// 200x120 source, matching the live-verified aspect-ratio-preserving probe.
	content := encodeTestPNG(t, 200, 120)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	var stdout, stderr bytes.Buffer
	window := "w1"
	cmd := newSchImageCmd(cfg, &window, &stdout, &stderr)
	cmd.SetArgs([]string{"create", "--file", path, "--x", "100", "--y", "-50", "--width", "400", "--dry-run"})
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v (stderr=%s)", err, stderr.String())
	}

	cap.mu.Lock()
	action := cap.action
	cap.mu.Unlock()
	if action != "" {
		t.Errorf("dry-run must not contact the daemon, got action %q", action)
	}

	var out map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("parse dry-run output: %v (stdout=%s)", err, stdout.String())
	}
	if out["dryRun"] != true {
		t.Errorf("expected dryRun:true, got %v", out["dryRun"])
	}
	if out["fileName"] != "module.png" {
		t.Errorf("expected fileName module.png, got %v", out["fileName"])
	}
	if got, want := out["bytes"].(float64), float64(len(content)); got != want {
		t.Errorf("expected bytes=%v, got %v", want, got)
	}
	if got, want := out["width"].(float64), float64(400); got != want {
		t.Errorf("expected width=%v, got %v", want, got)
	}
	// --width 400 on a 200x120 source must derive height=240 to preserve
	// aspect ratio (live-verified 2026-09-30: the host does NOT do this
	// itself, so the CLI must).
	if got, want := out["height"].(float64), float64(240); got != want {
		t.Errorf("expected derived height=%v (aspect-ratio preserved from 200x120), got %v", want, got)
	}
}

// A real (non-dry-run) create sends schematic.image.create with the file
// base64-encoded, the exact fileName, and only the flags the caller changed.
func TestSchImageCreate_WiresActionAndPayload(t *testing.T) {
	cfg, cap, cleanup := newCapturingDaemon(t)
	defer cleanup()

	dir := t.TempDir()
	path := filepath.Join(dir, "pinout.svg")
	// 100x60 viewBox, matching the live-verified probe file.
	content := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="100" height="60" viewBox="0 0 100 60"></svg>`)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	var stdout, stderr bytes.Buffer
	window := "w1"
	cmd := newSchImageCmd(cfg, &window, &stdout, &stderr)
	cmd.SetArgs([]string{"create", "--file", path, "--x", "2000", "--y", "-1000", "--rotation", "90"})
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v (stderr=%s)", err, stderr.String())
	}

	cap.mu.Lock()
	defer cap.mu.Unlock()
	if cap.action != "schematic.image.create" {
		t.Fatalf("expected action schematic.image.create, got %q", cap.action)
	}
	if cap.payload["fileName"] != "pinout.svg" {
		t.Errorf("fileName: got %v", cap.payload["fileName"])
	}
	wantB64 := base64.StdEncoding.EncodeToString(content)
	if cap.payload["dataBase64"] != wantB64 {
		t.Errorf("dataBase64 mismatch: got %v, want %v", cap.payload["dataBase64"], wantB64)
	}
	if cap.payload["x"] != float64(2000) || cap.payload["y"] != float64(-1000) {
		t.Errorf("x/y: got x=%v y=%v", cap.payload["x"], cap.payload["y"])
	}
	if cap.payload["rotation"] != float64(90) {
		t.Errorf("rotation: got %v", cap.payload["rotation"])
	}
	// Neither --width nor --height was passed, so the CLI must decode and send
	// the source's real intrinsic size (100x60) explicitly — the host does
	// NOT keep intrinsic size on its own (live-verified 2026-09-30).
	if cap.payload["width"] != float64(100) {
		t.Errorf("expected decoded intrinsic width=100, got %v", cap.payload["width"])
	}
	if cap.payload["height"] != float64(60) {
		t.Errorf("expected decoded intrinsic height=60, got %v", cap.payload["height"])
	}
	if _, ok := cap.payload["mirror"]; ok {
		t.Errorf("mirror should be omitted when --mirror was not passed, got %v", cap.payload["mirror"])
	}
}

// sch image list forwards to schematic.image.list with a nil payload (no
// --page given).
func TestSchImageList_WiresAction(t *testing.T) {
	cfg, cap, cleanup := newCapturingDaemon(t)
	defer cleanup()

	var stdout, stderr bytes.Buffer
	window := "w1"
	cmd := newSchImageCmd(cfg, &window, &stdout, &stderr)
	cmd.SetArgs([]string{"list"})
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v (stderr=%s)", err, stderr.String())
	}

	cap.mu.Lock()
	defer cap.mu.Unlock()
	if cap.action != "schematic.image.list" {
		t.Fatalf("expected action schematic.image.list, got %q", cap.action)
	}
}

// sch image modify requires --id.
func TestSchImageModify_RequiresID(t *testing.T) {
	cfg, _, cleanup := newCapturingDaemon(t)
	defer cleanup()

	var stdout, stderr bytes.Buffer
	window := "w1"
	cmd := newSchImageCmd(cfg, &window, &stdout, &stderr)
	cmd.SetArgs([]string{"modify", "--x", "10"})
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error when --id is missing")
	}
}

// sch image modify refuses a no-op call (--id with no geometry flags) before
// any daemon round-trip.
func TestSchImageModify_RequiresAtLeastOneField(t *testing.T) {
	cfg, cap, cleanup := newCapturingDaemon(t)
	defer cleanup()

	var stdout, stderr bytes.Buffer
	window := "w1"
	cmd := newSchImageCmd(cfg, &window, &stdout, &stderr)
	cmd.SetArgs([]string{"modify", "--id", "abc123"})
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error when no geometry flag is given")
	}
	cap.mu.Lock()
	defer cap.mu.Unlock()
	if cap.action != "" {
		t.Errorf("expected no daemon call for a no-op modify, got action %q", cap.action)
	}
}

// resolveSchImageDims is the fix for the live-verified defect (2026-09-30):
// omitting both --width/--height must NOT fall back to the host's fixed
// ~50x40 placeholder — it must resolve to the source's REAL intrinsic size.
func TestResolveSchImageDims_OmittedBoth_PNG(t *testing.T) {
	data := encodeTestPNG(t, 200, 120)
	w, h, err := resolveSchImageDims(data, "x.png", 0, false, 0, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w != 200 || h != 120 {
		t.Errorf("expected intrinsic 200x120, got %vx%v", w, h)
	}
}

func TestResolveSchImageDims_OmittedBoth_SVGViewBox(t *testing.T) {
	data := []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 60"></svg>`)
	w, h, err := resolveSchImageDims(data, "x.svg", 0, false, 0, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w != 100 || h != 60 {
		t.Errorf("expected viewBox-derived 100x60, got %vx%v", w, h)
	}
}

func TestResolveSchImageDims_OmittedBoth_SVGWidthHeightAttrs(t *testing.T) {
	data := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="50mm" height="30mm"></svg>`)
	w, h, err := resolveSchImageDims(data, "x.svg", 0, false, 0, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w != 50 || h != 30 {
		t.Errorf("expected width/height-derived 50x30 (unit stripped), got %vx%v", w, h)
	}
}

func TestResolveSchImageDims_SVGWithNoSize_Errors(t *testing.T) {
	data := []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`)
	if _, _, err := resolveSchImageDims(data, "x.svg", 0, false, 0, false); err == nil {
		t.Fatal("expected an error for an SVG with no width/height/viewBox")
	}
}

// Explicit width-only must derive height to preserve the source's aspect
// ratio, not leave it at zero or copy the width.
func TestResolveSchImageDims_WidthOnly_PreservesAspectRatio(t *testing.T) {
	data := encodeTestPNG(t, 200, 120) // 5:3
	w, h, err := resolveSchImageDims(data, "x.png", 400, true, 0, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w != 400 || h != 240 {
		t.Errorf("expected 400x240 (5:3 preserved), got %vx%v", w, h)
	}
}

// Explicit height-only must derive width symmetrically.
func TestResolveSchImageDims_HeightOnly_PreservesAspectRatio(t *testing.T) {
	data := encodeTestPNG(t, 200, 120) // 5:3
	w, h, err := resolveSchImageDims(data, "x.png", 0, false, 60, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w != 100 || h != 60 {
		t.Errorf("expected 100x60 (5:3 preserved), got %vx%v", w, h)
	}
}

// Explicit target dimensions still require a valid source; resizing must not
// bypass format/content validation.
func TestResolveSchImageDims_BothExplicit_ValidatesSource(t *testing.T) {
	if _, _, err := resolveSchImageDims([]byte("not a real image"), "x.png", 400, true, 240, true); err == nil {
		t.Fatal("corrupt source accepted with two explicit dimensions")
	}
	w, h, err := resolveSchImageDims(encodeTestPNG(t, 200, 120), "x.png", 400, true, 240, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w != 400 || h != 240 {
		t.Errorf("expected passthrough 400x240, got %vx%v", w, h)
	}
}

func TestResolveSchImageDims_CorruptRasterFile_Errors(t *testing.T) {
	if _, _, err := resolveSchImageDims([]byte("not a real png"), "x.png", 0, false, 0, false); err == nil {
		t.Fatal("expected a decode error for a corrupt PNG")
	}
}

// sch image modify wires only the flags the caller changed.
func TestSchImageModify_WiresActionAndPayload(t *testing.T) {
	cfg, cap, cleanup := newCapturingDaemon(t)
	defer cleanup()

	var stdout, stderr bytes.Buffer
	window := "w1"
	cmd := newSchImageCmd(cfg, &window, &stdout, &stderr)
	cmd.SetArgs([]string{"modify", "--id", "abc123", "--rotation", "180", "--mirror"})
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v (stderr=%s)", err, stderr.String())
	}

	cap.mu.Lock()
	defer cap.mu.Unlock()
	if cap.action != "schematic.image.modify" {
		t.Fatalf("expected action schematic.image.modify, got %q", cap.action)
	}
	if cap.payload["primitiveId"] != "abc123" {
		t.Errorf("primitiveId: got %v", cap.payload["primitiveId"])
	}
	if cap.payload["rotation"] != float64(180) {
		t.Errorf("rotation: got %v", cap.payload["rotation"])
	}
	if cap.payload["mirror"] != true {
		t.Errorf("mirror: got %v", cap.payload["mirror"])
	}
	if _, ok := cap.payload["x"]; ok {
		t.Errorf("x should be omitted when --x was not passed, got %v", cap.payload["x"])
	}
}

func encodeSchReferenceJPEG(t *testing.T) []byte {
	t.Helper()
	var data bytes.Buffer
	if err := jpeg.Encode(&data, image.NewRGBA(image.Rect(0, 0, 20, 12)), nil); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestSchImageCreate_RefusesInvalidSourcesWithoutDaemon(t *testing.T) {
	cfg, cap, cleanup := newCapturingDaemon(t)
	defer cleanup()
	pngData := encodeTestPNG(t, 20, 12)
	jpegData := encodeSchReferenceJPEG(t)
	// Keep the dimensions readable, but remove pixel data. Header-only decoding
	// must not count as source validation.
	truncatedPNG := pngData[:41]
	truncatedJPEG := jpegData[:len(jpegData)-16]
	for name, data := range map[string][]byte{"PNG": truncatedPNG, "JPEG": truncatedJPEG} {
		if _, _, err := image.DecodeConfig(bytes.NewReader(data)); err != nil {
			t.Fatalf("%s truncated fixture must retain a readable header: %v", name, err)
		}
	}
	oversizedPixels := append([]byte(nil), pngData...)
	binary.BigEndian.PutUint32(oversizedPixels[16:20], 4001)
	binary.BigEndian.PutUint32(oversizedPixels[20:24], 4000)
	binary.BigEndian.PutUint32(oversizedPixels[29:33], crc32.ChecksumIEEE(oversizedPixels[12:29]))
	for _, tc := range []struct {
		name, fileName string
		data           []byte
		want           string
	}{
		{"corrupt PNG", "source.png", []byte("broken"), "decode raster"},
		{"truncated PNG", "source.png", truncatedPNG, "decode raster image:"},
		{"truncated JPEG", "source.jpeg", truncatedJPEG, "decode raster image:"},
		{"PNG named JPEG", "source.jpg", pngData, "does not match extension"},
		{"JPEG named PNG", "source.png", jpegData, "does not match extension"},
		{"empty", "source.png", nil, "1 to 8388608 bytes"},
		{"too many bytes", "source.png", make([]byte, maxSchImageSourceBytes+1), "1 to 8388608 bytes"},
		{"too many pixels", "source.png", oversizedPixels, "16 million pixels"},
		{"nested SVG root", "source.svg", []byte(`<html><svg width="10" height="6"/></html>`), "expected SVG root"},
		{"bad SVG namespace", "source.svg", []byte(`<svg xmlns="urn:other" width="10" height="6"/>`), "expected SVG root"},
		{"unclosed SVG", "source.svg", []byte(`<svg width="10" height="6">`), "decode SVG"},
		{"extra SVG root", "source.svg", []byte(`<svg width="10" height="6"/><svg/>`), "after SVG root"},
		{"trailing text", "source.svg", []byte(`<svg width="10" height="6"/>garbage`), "after SVG root"},
		{"no SVG size", "source.svg", []byte(`<svg/>`), "intrinsic size"},
		{"nonfinite SVG size", "source.svg", []byte(`<svg width="NaN" height="6"/>`), "intrinsic size"},
		{"nonfinite SVG viewBox origin", "source.svg", []byte(`<svg viewBox="Inf 0 10 6"/>`), "intrinsic size"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), tc.fileName)
			if err := os.WriteFile(path, tc.data, 0600); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			window := "w1"
			cmd := newSchImageCmd(cfg, &window, &stdout, &stderr)
			cmd.SetArgs([]string{"create", "--file", path, "--width", "400", "--height", "240", "--dry-run"})
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want refusal containing %q, got %v", tc.want, err)
			}
			cap.mu.Lock()
			defer cap.mu.Unlock()
			if cap.action != "" {
				t.Fatalf("invalid source contacted daemon: %s", cap.action)
			}
		})
	}
}

func TestSchImageCreate_RefusesInvalidGeometryWithoutDaemon(t *testing.T) {
	cfg, cap, cleanup := newCapturingDaemon(t)
	defer cleanup()
	path := filepath.Join(t.TempDir(), "module.PnG")
	if err := os.WriteFile(path, encodeTestPNG(t, 20, 12), 0600); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"x", "y", "rotation", "width", "height"} {
		values := []string{"NaN", "+Inf", "-Inf"}
		if key == "width" || key == "height" {
			values = append(values, "0", "-1")
		}
		for _, value := range values {
			t.Run(key+"="+value, func(t *testing.T) {
				var stdout, stderr bytes.Buffer
				window := "w1"
				cmd := newSchImageCmd(cfg, &window, &stdout, &stderr)
				cmd.SetArgs([]string{"create", "--file", path, "--" + key, value, "--dry-run"})
				cmd.SetOut(&stdout)
				cmd.SetErr(&stderr)
				if err := cmd.Execute(); err == nil {
					t.Fatal("invalid geometry accepted")
				}
				cap.mu.Lock()
				defer cap.mu.Unlock()
				if cap.action != "" {
					t.Fatalf("invalid geometry contacted daemon: %s", cap.action)
				}
			})
		}
	}
}

func TestResolveSchImageDims_RejectsDerivedOverflow(t *testing.T) {
	data := []byte(`<svg width="1" height="100"/>`)
	if _, _, err := resolveSchImageDims(data, "x.svg", math.MaxFloat64, true, 0, false); err == nil {
		t.Fatal("derived infinite height accepted")
	}
}

func TestValidateSchImageSource_AcceptsRealJPEGAndCommaViewBox(t *testing.T) {
	w, h, err := validateSchImageSource(encodeSchReferenceJPEG(t), "接口照片.JpEg")
	if err != nil || w != 20 || h != 12 {
		t.Fatalf("JPEG dimensions = %gx%g, err=%v", w, h, err)
	}
	w, h, err = validateSchImageSource([]byte(`<svg viewBox="0,0,100,60"/>`), "pinout.svg")
	if err != nil || w != 100 || h != 60 {
		t.Fatalf("SVG dimensions = %gx%g, err=%v", w, h, err)
	}
}
