package bitmapimport

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math"
	"math/rand"
	"os"
	"reflect"
	"strings"
	"testing"
)

func pngData(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func maskData(t *testing.T, w, h int, mask []bool) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if !mask[y*w+x] {
				img.SetGray(x, y, color.Gray{Y: 255})
			}
		}
	}
	return pngData(t, img)
}

func parse(t *testing.T, data []byte, fileName string, opts Options) *Result {
	t.Helper()
	r, err := Parse(data, fileName, opts)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return r
}

func contourPoints(t *testing.T, polygon []any) [][2]float64 {
	t.Helper()
	if len(polygon) < 11 || polygon[2] != "L" || len(polygon)%2 != 1 {
		t.Fatalf("invalid polygon command array: %v", polygon)
	}
	points := make([][2]float64, 0, (len(polygon)-1)/2)
	for i := 0; i < len(polygon); {
		if i == 2 {
			i++
		}
		x, xok := polygon[i].(float64)
		y, yok := polygon[i+1].(float64)
		if !xok || !yok || math.IsInf(x, 0) || math.IsInf(y, 0) || math.IsNaN(x) || math.IsNaN(y) {
			t.Fatalf("invalid coordinate in %v", polygon)
		}
		points = append(points, [2]float64{x, y})
		i += 2
	}
	if points[0] != points[len(points)-1] {
		t.Fatalf("contour is not explicitly closed: %v", points)
	}
	return points
}

// filled applies an independent even-odd ray-crossing oracle to polygon arrays.
func filled(t *testing.T, polygons [][]any, x, y float64) bool {
	t.Helper()
	inside := false
	for _, polygon := range polygons {
		points := contourPoints(t, polygon)
		for i := 1; i < len(points); i++ {
			a, b := points[i-1], points[i]
			if (a[1] > y) != (b[1] > y) && x < (b[0]-a[0])*(y-a[1])/(b[1]-a[1])+a[0] {
				inside = !inside
			}
		}
	}
	return inside
}

func checkMask(t *testing.T, r *Result, mask []bool) {
	t.Helper()
	pointCount := 0
	for _, p := range r.Polygons {
		pointCount += len(contourPoints(t, p))
	}
	if r.PathCount != len(r.Polygons) || r.PointCount != pointCount {
		t.Fatalf("counts disagree with geometry: %+v", r)
	}
	for y := 0; y < r.SourceHeight; y++ {
		for x := 0; x < r.SourceWidth; x++ {
			want := mask[y*r.SourceWidth+x]
			if got := filled(t, r.Polygons, (float64(x)+0.5)*r.PixelWidth, (float64(y)+0.5)*r.PixelHeight); got != want {
				t.Fatalf("pixel (%d,%d): even-odd fill=%v, source=%v; polygons=%v", x, y, got, want, r.Polygons)
			}
		}
	}
}

func TestDonutFixtureCanvasAndHole(t *testing.T) {
	data, err := os.ReadFile("testdata/donut.png")
	if err != nil {
		t.Fatal(err)
	}
	r := parse(t, data, "donut.png", Options{TargetWidth: 700, Threshold: 128, Simplify: true})
	if r.SourceWidth != 7 || r.SourceHeight != 7 || r.Width != 700 || r.Height != 700 || r.PixelWidth != 100 || r.PixelHeight != 100 {
		t.Fatalf("full canvas/pitches changed: %+v", r)
	}
	if r.ForegroundPixels != 16 || r.PathCount != 2 || r.PointCount != 10 {
		t.Fatalf("want 16 pixels, outer+hole, 10 closed-contour vertices: %+v", r)
	}
	mask := make([]bool, 49)
	for y := 1; y <= 5; y++ {
		for x := 1; x <= 5; x++ {
			mask[y*7+x] = x == 1 || x == 5 || y == 1 || y == 5
		}
	}
	checkMask(t, r, mask)
	if r.Polygons[0][0] != float64(100) || r.Polygons[0][1] != float64(100) {
		t.Fatalf("transparent padding was cropped: %v", r.Polygons[0])
	}
	sum := sha256.Sum256(data)
	if r.SHA256 != hex.EncodeToString(sum[:]) || r.Format != "png" {
		t.Fatalf("wrong source identity: %+v", r)
	}
	encoded, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"polygons", "width", "height", "sourceWidth", "sourceHeight", "format", "sha256", "foregroundPixels", "pathCount", "pointCount", "pixelWidth", "pixelHeight"} {
		if _, ok := fields[key]; !ok {
			t.Fatalf("missing JSON field %q in %s", key, encoded)
		}
	}
}

func TestDiagonalTouchDoesNotBridge(t *testing.T) {
	mask := []bool{true, false, false, true}
	r := parse(t, maskData(t, 2, 2, mask), "diagonal.PNG", Options{TargetWidth: 20, Threshold: 128, Simplify: true})
	if r.PathCount != 2 || r.PointCount != 10 {
		t.Fatalf("diagonal pixels must remain two closed squares: %+v", r)
	}
	checkMask(t, r, mask)
}

func TestBoundaryAndExactSimplify(t *testing.T) {
	mask := []bool{true, true, true, true, true, true}
	data := maskData(t, 3, 2, mask)
	for _, simplify := range []bool{false, true} {
		r := parse(t, data, "rectangle.png", Options{TargetWidth: 3, Threshold: 128, Simplify: simplify})
		wantPoints := 11
		if simplify {
			wantPoints = 5
		}
		if r.PathCount != 1 || r.PointCount != wantPoints {
			t.Fatalf("simplify=%v: want one contour/%d points: %+v", simplify, wantPoints, r)
		}
		checkMask(t, r, mask)
	}
}

func TestAlphaBackgroundAndInvert(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 6, 1))
	colors := []color.NRGBA{
		{R: 255, G: 255, B: 255, A: 0},
		{A: 255},
		{R: 255, G: 255, B: 255, A: 255},
		{A: 128},
		{R: 255, G: 255, B: 255, A: 128},
		{R: 128, G: 128, B: 128, A: 255},
	}
	for x, c := range colors {
		img.SetNRGBA(x, 0, c)
	}
	data := pngData(t, img)
	for _, tc := range []struct {
		background string
		invert     bool
		mask       []bool
	}{
		{"white", false, []bool{false, true, false, true, false, true}},
		{"white", true, []bool{false, false, true, false, true, false}},
		{"black", false, []bool{false, false, true, false, false, false}},
		{"black", true, []bool{false, true, false, true, true, true}},
	} {
		t.Run(tc.background+map[bool]string{false: "", true: "-invert"}[tc.invert], func(t *testing.T) {
			r := parse(t, data, "alpha.png", Options{TargetWidth: 60, Threshold: 128, Background: tc.background, Invert: tc.invert, Simplify: true})
			checkMask(t, r, tc.mask)
		})
	}
}

func TestRec601AndZeroThreshold(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 1))
	for x, c := range []color.NRGBA{{A: 255}, {R: 1, G: 1, B: 1, A: 255}, {R: 255, A: 255}, {B: 255, A: 255}} {
		img.SetNRGBA(x, 0, c)
	}
	data := pngData(t, img)
	for _, tc := range []struct {
		threshold  int
		background string
		mask       []bool
	}{
		{0, "white", []bool{true, false, false, false}},
		{0, "black", []bool{false, true, true, true}},
		{29, "white", []bool{true, true, false, false}},
		{30, "white", []bool{true, true, false, true}},
		{76, "white", []bool{true, true, false, true}},
		{77, "white", []bool{true, true, true, true}},
		{255, "white", []bool{true, true, true, true}},
	} {
		r := parse(t, data, "colors.png", Options{TargetWidth: 4, Threshold: tc.threshold, Background: tc.background, Simplify: true})
		checkMask(t, r, tc.mask)
	}
}

func TestJPEG(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 2, 2))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"black.jpg", "black.jpeg", "black.JPEG"} {
		r := parse(t, buf.Bytes(), name, Options{TargetWidth: 20, Threshold: 128, Simplify: true})
		if r.Format != "jpeg" || r.ForegroundPixels != 4 {
			t.Fatalf("wrong JPEG conversion: %+v", r)
		}
		checkMask(t, r, []bool{true, true, true, true})
	}
}

func TestDimensions(t *testing.T) {
	data := maskData(t, 4, 2, []bool{true, true, true, true, true, true, true, true})
	for _, tc := range []struct {
		name string
		opts Options
		w, h float64
	}{
		{"width", Options{TargetWidth: 100}, 100, 50},
		{"height", Options{TargetHeight: 100}, 200, 100},
		{"fit", Options{TargetWidth: 200, TargetHeight: 200, KeepAspect: true}, 200, 100},
		{"resize", Options{TargetWidth: 200, TargetHeight: 200}, 200, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := parse(t, data, "dimensions.png", tc.opts)
			if r.Width != tc.w || r.Height != tc.h || r.PixelWidth != tc.w/4 || r.PixelHeight != tc.h/2 {
				t.Fatalf("want %gx%g canvas; got %+v", tc.w, tc.h, r)
			}
		})
	}
}

func TestRandomMasksEvenOddCoverageAndDeterminism(t *testing.T) {
	rng := rand.New(rand.NewSource(272))
	for trial := 0; trial < 300; trial++ {
		w, h := 1+rng.Intn(9), 1+rng.Intn(9)
		mask := make([]bool, w*h)
		for i := range mask {
			mask[i] = rng.Intn(2) == 1
		}
		mask[rng.Intn(len(mask))] = true
		data := maskData(t, w, h, mask)
		for _, simplify := range []bool{false, true} {
			opts := Options{TargetWidth: float64(w) * 0.137, TargetHeight: float64(h) * 0.731, Threshold: 128, Simplify: simplify}
			r := parse(t, data, "random.png", opts)
			checkMask(t, r, mask)
			second := parse(t, data, "random.png", opts)
			if !reflect.DeepEqual(r, second) {
				t.Fatalf("trial %d simplify=%v: output not deterministic", trial, simplify)
			}
		}
	}
}

func TestBlankAndTransparentInvert(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
		opts Options
	}{
		{"white", maskData(t, 2, 2, make([]bool, 4)), Options{TargetWidth: 20, Threshold: 128}},
		{"transparent", pngData(t, image.NewNRGBA(image.Rect(0, 0, 2, 2))), Options{TargetWidth: 20, Threshold: 128, Invert: true}},
		{"black-background-max-threshold", maskData(t, 2, 2, make([]bool, 4)), Options{TargetWidth: 20, Background: "black", Threshold: 255}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse(tc.data, tc.name+".png", tc.opts); err == nil || !strings.Contains(err.Error(), "no foreground") {
				t.Fatalf("want no-foreground error, got %v", err)
			}
		})
	}
}

func TestBadFiles(t *testing.T) {
	valid := maskData(t, 1, 1, []bool{true})
	for _, tc := range []struct {
		name     string
		data     []byte
		fileName string
		want     string
	}{
		{"empty", nil, "empty.png", "empty"},
		{"extension", valid, "test.gif", "extension"},
		{"mismatch", valid, "test.jpg", "does not match"},
		{"malformed", []byte("not a PNG"), "test.png", "decode bitmap config"},
		{"truncated", valid[:len(valid)/2], "test.png", "decode bitmap"},
		{"file-size", make([]byte, MaxFileBytes+1), "large.png", "byte limit"},
		{"pixel-count", pngConfig(MaxPixels+1, 1), "large.png", "pixel limit"},
		{"pixel-product", pngConfig(1001, 1000), "large.png", "pixel limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse(tc.data, tc.fileName, Options{TargetWidth: 10, Threshold: 128}); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

// A valid PNG signature/IHDR is enough for DecodeConfig but has no pixel data.
// Resource-limit tests therefore establish rejection before Decode is called.
func pngConfig(w, h int) []byte {
	buf := bytes.NewBuffer([]byte{137, 80, 78, 71, 13, 10, 26, 10})
	chunk := make([]byte, 17)
	copy(chunk, "IHDR")
	binary.BigEndian.PutUint32(chunk[4:8], uint32(w))
	binary.BigEndian.PutUint32(chunk[8:12], uint32(h))
	chunk[12], chunk[13] = 8, 6 // 8-bit RGBA
	_ = binary.Write(buf, binary.BigEndian, uint32(13))
	_, _ = buf.Write(chunk)
	_ = binary.Write(buf, binary.BigEndian, crc32.ChecksumIEEE(chunk))
	return buf.Bytes()
}

func TestInvalidOptions(t *testing.T) {
	data := maskData(t, 1, 1, []bool{true})
	for _, opts := range []Options{
		{}, {TargetWidth: -1}, {TargetWidth: 1, TargetHeight: -1},
		{TargetWidth: math.NaN()}, {TargetWidth: math.Inf(1)}, {TargetWidth: 1, TargetHeight: math.Inf(-1)},
		{TargetWidth: 1, Threshold: -1}, {TargetWidth: 1, Threshold: 256},
		{TargetWidth: 1, Background: "transparent"},
	} {
		if _, err := Parse(data, "options.png", opts); err == nil {
			t.Fatalf("want error for invalid options %+v", opts)
		}
	}
	// Underflow/overflow after applying the pixel dimensions must also be refused.
	wide := maskData(t, 2, 1, []bool{true, true})
	for _, opts := range []Options{{TargetWidth: math.SmallestNonzeroFloat64}, {TargetHeight: math.MaxFloat64}} {
		if _, err := Parse(wide, "scale.png", opts); err == nil {
			t.Fatalf("want scaling error for %+v", opts)
		}
	}
}

func TestPointLimitAndLargeCollinearSimplification(t *testing.T) {
	// Alternating isolated pixels force >100k contour vertices even simplified.
	w, h := 201, 200
	mask := make([]bool, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			mask[y*w+x] = (x+y)%2 == 0
		}
	}
	data := maskData(t, w, h, mask)
	for _, simplify := range []bool{false, true} {
		if _, err := Parse(data, "fragmented.png", Options{TargetWidth: 201, Threshold: 128, Simplify: simplify}); err == nil || !strings.Contains(err.Error(), "point limit") {
			t.Fatalf("want point-limit error simplify=%v, got %v", simplify, err)
		}
	}
	// A long exact rectangle is safe when collinear edge points are removed.
	longMask := make([]bool, 50_001)
	for i := range longMask {
		longMask[i] = true
	}
	long := maskData(t, len(longMask), 1, longMask)
	if _, err := Parse(long, "long.png", Options{TargetWidth: 50_001}); err == nil || !strings.Contains(err.Error(), "point limit") {
		t.Fatalf("want raw perimeter point-limit error, got %v", err)
	}
	r := parse(t, long, "long.png", Options{TargetWidth: 50_001, Simplify: true})
	if r.PointCount != 5 || r.ForegroundPixels != 50_001 {
		t.Fatalf("exact collinear simplification failed: %+v", r)
	}
}
