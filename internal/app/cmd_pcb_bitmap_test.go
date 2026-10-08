package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

func bitmapTestSource(t *testing.T) string {
	t.Helper()
	source, err := os.ReadFile("../pcb/bitmapimport/testdata/donut.png")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "标志 logo.png")
	if err := os.WriteFile(path, source, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBitmapSilkOfflineCommandAndUnsupported(t *testing.T) {
	file := bitmapTestSource(t)
	for _, dry := range []bool{true, false} {
		var out bytes.Buffer
		cmd := newPcbSilkImportBitmapCmd(&out)
		cmd.SilenceUsage, cmd.SilenceErrors = true, true
		args := []string{"--file", file, "--width", "600", "--layer", "3"}
		if dry {
			args = append(args, "--dry-run")
		}
		cmd.SetArgs(args)
		err := cmd.Execute()
		if dry && err != nil || !dry && (err == nil || !strings.Contains(err.Error(), "unsupported:")) {
			t.Fatalf("dry=%v err=%v", dry, err)
		}
		var result struct {
			Validation     string
			HostSupport    string
			WriteAttempted bool
			Payload        protocol.BitmapSilkPayload
		}
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Validation != "offline-verified" || result.HostSupport != "unsupported" || result.WriteAttempted || result.Payload.Width != 600 || result.Payload.Height != 600 || result.Payload.Source.FileName != "标志 logo.png" {
			t.Fatalf("%+v", result)
		}
		if err := result.Payload.Validate(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBitmapSilkParameterFileAndApply(t *testing.T) {
	file := bitmapTestSource(t)
	params := filepath.Join(filepath.Dir(file), "参数.json")
	if err := os.WriteFile(params, []byte(`{"file":"标志 logo.png","width":600,"layer":4,"threshold":0,"simplify":false,"x":10}`), 0600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "bitmap.apply.json")
	var report bytes.Buffer
	cmd := newPcbSilkImportBitmapCmd(&report)
	cmd.SetArgs([]string{"--from", params, "--out", output, "--height", "300", "--keep-aspect", "--mirror=false", "--x", "20"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	pb, _, err := loadPlaybook(output)
	if err != nil {
		t.Fatal(err)
	}
	if errs := preflight(pb, nil); len(errs) != 0 {
		t.Fatal(errs)
	}
	if len(pb.Steps) != 1 || pb.Steps[0].Action != protocol.BitmapSilkAction {
		t.Fatalf("%+v", pb)
	}
	p := pb.Steps[0].Payload
	if p["width"] != float64(300) || p["height"] != float64(300) || p["mirror"] != false || p["x"] != float64(20) || p["layer"] != float64(4) {
		t.Fatalf("payload=%v", p)
	}
	conversion := p["conversion"].(map[string]any)
	if conversion["threshold"] != float64(0) || conversion["simplify"] != false {
		t.Fatal(conversion)
	}
	// Refuse replacing source, parameters, or an earlier plan.
	cmd = newPcbSilkImportBitmapCmd(&bytes.Buffer{})
	cmd.SetArgs([]string{"--from", params, "--out", output})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "must be new") {
		t.Fatalf("overwrite accepted: %v", err)
	}
	// Apply's dry-run recognizes the typed contract without consulting a daemon.
	var applyOut bytes.Buffer
	apply := newApplyCmd(&appConfig{}, &applyOut, &applyOut)
	apply.SetArgs([]string{output, "--dry-run"})
	if err := apply.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(applyOut.String(), "nothing executed") {
		t.Fatal(applyOut.String())
	}
	apply = newApplyCmd(&appConfig{project: "missing", doc: "missing"}, &bytes.Buffer{}, &bytes.Buffer{})
	apply.SetArgs([]string{output})
	if err := apply.Execute(); err == nil || !strings.Contains(err.Error(), "unsupported:") {
		t.Fatalf("Apply must refuse before routing: %v", err)
	}
	// The raw call/request route also answers locally, even without a daemon.
	var refused bytes.Buffer
	if err := dispatch(&appConfig{host: "invalid", doc: "missing"}, protocol.BitmapSilkAction, "", p, &refused, &refused); err == nil || !strings.Contains(refused.String(), "unsupported:") {
		t.Fatalf("call refusal: %v %s", err, refused.String())
	}
	p["layer"] = 13
	if errs := preflight(pb, nil); len(errs) == 0 {
		t.Fatal("Apply dry-run accepted invalid bitmap payload")
	}
}

func TestBitmapSilkRejectsInvalidParameters(t *testing.T) {
	file := bitmapTestSource(t)
	for _, flags := range [][]string{
		{"--width", "600"}, {"--layer", "3"}, {"--layer", "13", "--width", "600"},
		{"--layer", "3", "--width", "0"}, {"--layer", "3", "--width", "NaN"},
		{"--layer", "3", "--width", "600", "--height", "-1"}, {"--layer", "3", "--width", "600", "--x", "Inf"},
		{"--layer", "3", "--width", "600", "--rotation", "NaN"}, {"--layer", "3", "--width", "600", "--threshold", "256"},
		{"--layer", "3", "--width", "600", "--background", "transparent"}, {"--layer", "3", "--width", "600", "--min-line-width", "-1"},
	} {
		cmd := newPcbSilkImportBitmapCmd(&bytes.Buffer{})
		cmd.SetArgs(append([]string{"--file", file, "--dry-run"}, flags...))
		if err := cmd.Execute(); err == nil {
			t.Fatalf("accepted flags %v", flags)
		}
	}
	for _, data := range []string{`{"file":"x.png","width":600,"layer":3,"typo":true}`, `{"file":"x.png","width":600,"layer":3} {}`, `{"file":"x.png","width":0,"layer":3}`, `{"file":"x.png","width":600,"layer":3,"threshold":null}`, `null`} {
		params := filepath.Join(t.TempDir(), "params.json")
		os.WriteFile(params, []byte(data), 0600)
		cmd := newPcbSilkImportBitmapCmd(&bytes.Buffer{})
		cmd.SetArgs([]string{"--from", params, "--dry-run"})
		if err := cmd.Execute(); err == nil {
			t.Fatalf("accepted %s", data)
		}
	}
}

func TestBitmapSilkPlannedBBox(t *testing.T) {
	p := protocol.BitmapSilkPayload{X: 10, Y: 20, Width: 100, Height: 50, Rotation: 90, Mirror: true}
	box := bitmapSilkBBox(p, nil)
	for key, want := range map[string]float64{"minX": 10, "maxX": 60, "minY": 20, "maxY": 120} {
		if box[key] < want-1e-6 || box[key] > want+1e-6 {
			t.Fatalf("%v", box)
		}
	}
	// Left-hand art moves to the right side under the planned source-center mirror.
	p.Rotation = 0
	box = bitmapSilkBBox(p, [][]any{{float64(0), float64(0), "L", float64(10), float64(0), float64(10), float64(10), float64(0), float64(10), float64(0), float64(0)}})
	if box["minX"] != 100 || box["maxX"] != 110 || box["minY"] != 10 || box["maxY"] != 20 {
		t.Fatal(box)
	}
}

func TestBitmapSilkLargeRotationAndOverflow(t *testing.T) {
	file := bitmapTestSource(t)
	for _, tc := range []struct {
		flags []string
		valid bool
	}{
		{[]string{"--width", "600", "--rotation", "1e308"}, true},
		{[]string{"--width", "1e308", "--x", "1e308"}, false},
	} {
		cmd := newPcbSilkImportBitmapCmd(&bytes.Buffer{})
		cmd.SetArgs(append([]string{"--file", file, "--layer", "3", "--dry-run"}, tc.flags...))
		if err := cmd.Execute(); (err == nil) != tc.valid {
			t.Fatalf("%v err=%v", tc.flags, err)
		}
	}
}
