package app

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// writeTestPNG renders img to a temp PNG and returns its path.
func writeTestPNG(t *testing.T, img image.Image) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "frame.png")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAnalyzeSnapshotFrame_BlankVsContent(t *testing.T) {
	// A flat white frame = the "window not rendering" case → blank.
	white := image.NewRGBA(image.Rect(0, 0, 400, 300))
	for i := range white.Pix {
		white.Pix[i] = 0xff
	}
	if fs, err := analyzeSnapshotFrame(writeTestPNG(t, white)); err != nil {
		t.Fatal(err)
	} else if !fs.Blank {
		t.Errorf("flat white frame: blank=false (content=%.4f%% colors=%d), want blank", fs.NonBgFraction*100, fs.DistinctBins)
	}

	// A frame with varied content → not blank.
	content := image.NewRGBA(image.Rect(0, 0, 400, 300))
	for y := 0; y < 300; y++ {
		for x := 0; x < 400; x++ {
			// spread many colors across the field so both coverage and the
			// distinct-bin signal register real content.
			content.Set(x, y, color.RGBA{uint8(x % 251), uint8(y % 241), uint8((x + y) % 233), 0xff})
		}
	}
	if fs, err := analyzeSnapshotFrame(writeTestPNG(t, content)); err != nil {
		t.Fatal(err)
	} else if fs.Blank {
		t.Errorf("content frame flagged blank (content=%.4f%% colors=%d)", fs.NonBgFraction*100, fs.DistinctBins)
	}
}

func TestSanitizeStage(t *testing.T) {
	cases := map[string]string{
		"P7 routing":     "P7-routing",
		"  P0 布局/板框 ": "P0",  // non-ASCII stripped, edges trimmed
		"///":            "stage", // nothing usable → fallback
		"P8_power-planes": "P8_power-planes",
	}
	for in, want := range cases {
		if got := sanitizeStage(in); got != want {
			t.Errorf("sanitizeStage(%q)=%q want %q", in, got, want)
		}
	}
}

func TestDrcPass(t *testing.T) {
	if p, known := drcPass(nil); known || p {
		t.Errorf("nil result: got (%v,%v) want (false,false)", p, known)
	}
	if p, known := drcPass(map[string]any{"passed": true}); !known || !p {
		t.Errorf("passed=true: got (%v,%v)", p, known)
	}
	if p, known := drcPass(map[string]any{"count": float64(3)}); !known || p {
		t.Errorf("count=3: got (%v,%v) want (false,true)", p, known)
	}
	if p, known := drcPass(map[string]any{"count": float64(0)}); !known || !p {
		t.Errorf("count=0: got (%v,%v) want (true,true)", p, known)
	}
}
