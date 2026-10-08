package protocol

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
)

func bitmapSilkTestPayload() BitmapSilkPayload {
	return BitmapSilkPayload{
		SchemaVersion: 1,
		Source:        BitmapSilkSource{FileName: "logo.png", Format: "png", SHA256: strings.Repeat("a", 64), PixelWidth: 2, PixelHeight: 1},
		Conversion:    BitmapSilkConversion{Threshold: 0, Background: "white", Invert: false, Simplify: false},
		Polygons:      [][]any{{float64(0), float64(0), "L", float64(20), float64(0), float64(20), float64(10), float64(0), float64(10), float64(0), float64(0)}},
		X:             0, Y: 0, Width: 20, Height: 10, Rotation: 0, Mirror: false, Layer: 3, Units: "mil", Anchor: "top-left",
	}
}

func bitmapSilkTestMap(t *testing.T) map[string]any {
	t.Helper()
	data, err := json.Marshal(bitmapSilkTestPayload())
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestDecodeBitmapSilkPayloadValidRepresentations(t *testing.T) {
	p := bitmapSilkTestPayload()
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"typed", p}, {"transport-map", bitmapSilkTestMap(t)}, {"raw-json", json.RawMessage(data)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodeBitmapSilkPayload(tc.value)
			if err != nil || !reflect.DeepEqual(got, p) {
				t.Fatalf("valid typed plan changed: got=%+v err=%v", got, err)
			}
			if got.Mirror || got.Conversion.Invert || got.Conversion.Simplify || got.Conversion.Threshold != 0 {
				t.Fatal("explicit false/zero parameters were defaulted")
			}
		})
	}
}

func TestDecodeBitmapSilkPayloadAllFieldsRequiredNonNull(t *testing.T) {
	for _, level := range []string{"payload", "source", "conversion"} {
		baseline := bitmapSilkTestMap(t)
		fields := baseline
		if level != "payload" {
			fields = baseline[level].(map[string]any)
		}
		for key := range fields {
			for _, absent := range []bool{false, true} {
				mode := "null"
				if absent {
					mode = "missing"
				}
				t.Run(level+"/"+key+"/"+mode, func(t *testing.T) {
					value := bitmapSilkTestMap(t)
					object := value
					if level != "payload" {
						object = value[level].(map[string]any)
					}
					if absent {
						delete(object, key)
					} else {
						object[key] = nil
					}
					if _, err := DecodeBitmapSilkPayload(value); err == nil || !strings.Contains(err.Error(), level+"."+key+" is required") {
						t.Fatalf("want explicit required-field error, got %v", err)
					}
				})
			}
		}
	}
}

func TestDecodeBitmapSilkPayloadUnknownAndCaseAliasedFields(t *testing.T) {
	for _, tc := range []struct {
		level, key string
	}{
		{"payload", "hostSupport"}, {"source", "bytes"}, {"conversion", "force"},
		{"payload", "MIRROR"}, {"source", "PIXELWIDTH"}, {"conversion", "THRESHOLD"},
	} {
		t.Run(tc.level+"/"+tc.key, func(t *testing.T) {
			value := bitmapSilkTestMap(t)
			object := value
			if tc.level != "payload" {
				object = value[tc.level].(map[string]any)
			}
			object[tc.key] = false
			if _, err := DecodeBitmapSilkPayload(value); err == nil || !strings.Contains(err.Error(), "unknown bitmap silk "+tc.level+" field") {
				t.Fatalf("want unknown-field refusal, got %v", err)
			}
		})
	}
}

func TestDecodeBitmapSilkPayloadInvalidObjectsAndJSON(t *testing.T) {
	for _, value := range []any{nil, "json is not an object", []any{}, 5, json.RawMessage(`{"schemaVersion":`), json.RawMessage(`{} {}`)} {
		if _, err := DecodeBitmapSilkPayload(value); err == nil {
			t.Fatalf("want malformed-object refusal for %#v", value)
		}
	}
	for _, key := range []string{"source", "conversion"} {
		for _, malformed := range []any{[]any{}, "not an object", false, 5} {
			value := bitmapSilkTestMap(t)
			value[key] = malformed
			if _, err := DecodeBitmapSilkPayload(value); err == nil || !strings.Contains(err.Error(), key+" must be an object") {
				t.Fatalf("want %s object refusal for %#v, got %v", key, malformed, err)
			}
		}
	}
}

func TestDecodeBitmapSilkPayloadMalformedTypesAndConstraints(t *testing.T) {
	for _, tc := range []struct {
		name, level, key string
		value            any
	}{
		{"wrong-schema", "payload", "schemaVersion", 2},
		{"schema-string", "payload", "schemaVersion", "1"},
		{"wrong-units", "payload", "units", "mm"},
		{"wrong-anchor", "payload", "anchor", "center"},
		{"wrong-layer", "payload", "layer", 13},
		{"fractional-layer", "payload", "layer", 3.5},
		{"mirror-string", "payload", "mirror", "false"},
		{"mirror-number", "payload", "mirror", 0},
		{"negative-width", "payload", "width", -1},
		{"zero-height", "payload", "height", 0},
		{"coordinate-string", "payload", "x", "0"},
		{"invalid-hash", "source", "sha256", strings.Repeat("A", 64)},
		{"filename-path", "source", "fileName", "sub/logo.png"},
		{"filename-backslash", "source", "fileName", `sub\logo.png`},
		{"extension-mismatch", "source", "fileName", "logo.jpg"},
		{"format-mismatch", "source", "format", "jpeg"},
		{"empty-file", "source", "fileName", ""},
		{"zero-pixels", "source", "pixelWidth", 0},
		{"negative-pixels", "source", "pixelHeight", -1},
		{"fractional-pixels", "source", "pixelWidth", 1.5},
		{"pixel-limit", "source", "pixelWidth", 1_000_001},
		{"invalid-threshold", "conversion", "threshold", -1},
		{"threshold-too-high", "conversion", "threshold", 256},
		{"fractional-threshold", "conversion", "threshold", 127.5},
		{"invalid-background", "conversion", "background", "gray"},
		{"invert-string", "conversion", "invert", "false"},
		{"simplify-number", "conversion", "simplify", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := bitmapSilkTestMap(t)
			object := value
			if tc.level != "payload" {
				object = value[tc.level].(map[string]any)
			}
			object[tc.key] = tc.value
			if _, err := DecodeBitmapSilkPayload(value); err == nil {
				t.Fatalf("invalid payload %s passed", tc.name)
			}
		})
	}
}

func TestDecodeBitmapSilkPayloadNonFinite(t *testing.T) {
	for _, n := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		for _, key := range []string{"x", "y", "width", "height", "rotation"} {
			value := bitmapSilkTestMap(t)
			value[key] = n
			if _, err := DecodeBitmapSilkPayload(value); err == nil {
				t.Fatalf("nonfinite %s=%v passed", key, n)
			}
		}
		p := bitmapSilkTestPayload()
		p.Polygons[0][0] = n
		if err := p.Validate(); err == nil {
			t.Fatal("direct typed validation accepted a nonfinite contour")
		}
		if _, err := DecodeBitmapSilkPayload(p); err == nil {
			t.Fatal("strict decode accepted a nonfinite contour")
		}
	}
}

func TestDecodeBitmapSilkPayloadMaliciousContours(t *testing.T) {
	valid := bitmapSilkTestMap(t)["polygons"].([]any)[0].([]any)
	for _, tc := range []struct {
		name     string
		polygons any
	}{
		{"empty", []any{}},
		{"not-array", "polygon"},
		{"nested-object", []any{map[string]any{"points": valid}}},
		{"short", []any{[]any{0, 0, "L", 1, 1}}},
		{"even-command-length", []any{valid[:len(valid)-1]}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := bitmapSilkTestMap(t)
			value["polygons"] = tc.polygons
			if _, err := DecodeBitmapSilkPayload(value); err == nil {
				t.Fatalf("invalid contours %s passed", tc.name)
			}
		})
	}
	for _, tc := range []struct {
		name  string
		index int
		value any
	}{
		{"unknown-command", 2, "C"},
		{"command-in-coordinate", 3, "L"},
		{"object-in-coordinate", 0, map[string]any{"x": 0}},
		{"array-in-coordinate", 0, []any{0}},
		{"null-coordinate", 0, nil},
		{"negative-coordinate", 0, -1},
		{"x-beyond-canvas", 0, 21},
		{"y-beyond-canvas", 1, 11},
		{"unclosed", len(valid) - 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := bitmapSilkTestMap(t)
			contour := value["polygons"].([]any)[0].([]any)
			contour[tc.index] = tc.value
			if _, err := DecodeBitmapSilkPayload(value); err == nil {
				t.Fatalf("invalid contour %s passed", tc.name)
			}
		})
	}
	tooManyPoints := make([]any, 200_003) // 100001 vertices plus the command
	for i := range tooManyPoints {
		tooManyPoints[i] = float64(0)
	}
	tooManyPoints[2] = "L"
	p := bitmapSilkTestPayload()
	p.Polygons = [][]any{tooManyPoints}
	if _, err := DecodeBitmapSilkPayload(p); err == nil || !strings.Contains(err.Error(), "100000 vertices") {
		t.Fatalf("want oversized-contour refusal, got %v", err)
	}
	p.Polygons = make([][]any, 25_001)
	if _, err := DecodeBitmapSilkPayload(p); err == nil || !strings.Contains(err.Error(), "bounded contours") {
		t.Fatalf("want excessive-contour-count refusal, got %v", err)
	}
}
