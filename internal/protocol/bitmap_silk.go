package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"regexp"
	"strings"
)

const BitmapSilkAction = "pcb.silk.import_bitmap"
const BitmapSilkUnsupported = "unsupported: bitmap manufacturing silk is offline-verified only; host creation, geometry readback, DFM and save/reload are not live-verified; no write attempted"

var bitmapSilkSHA256 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// BitmapSilkPayload describes an offline pixel-boundary plan. It carries no
// binary data and is not evidence of host support or manufacturing suitability.
type BitmapSilkPayload struct {
	SchemaVersion int                  `json:"schemaVersion"`
	Source        BitmapSilkSource     `json:"source"`
	Conversion    BitmapSilkConversion `json:"conversion"`
	Polygons      [][]any              `json:"polygons"`
	X             float64              `json:"x"`
	Y             float64              `json:"y"`
	Width         float64              `json:"width"`
	Height        float64              `json:"height"`
	Rotation      float64              `json:"rotation"`
	Mirror        bool                 `json:"mirror"`
	Layer         int                  `json:"layer"`
	Units         string               `json:"units"`
	Anchor        string               `json:"anchor"`
}

type BitmapSilkSource struct {
	FileName    string `json:"fileName"`
	Format      string `json:"format"`
	SHA256      string `json:"sha256"`
	PixelWidth  int    `json:"pixelWidth"`
	PixelHeight int    `json:"pixelHeight"`
}

type BitmapSilkConversion struct {
	Threshold  int    `json:"threshold"`
	Background string `json:"background"`
	Invert     bool   `json:"invert"`
	Simplify   bool   `json:"simplify"`
}

// DecodeBitmapSilkPayload strictly decodes a transport payload, typed value or
// json.RawMessage. Every schema field is required and non-null, including false
// boolean values; exact JSON field names are required at every metadata level.
// Validation describes a bounded offline plan and never enables a host write.
func DecodeBitmapSilkPayload(value any) (BitmapSilkPayload, error) {
	var p BitmapSilkPayload
	data, err := json.Marshal(value)
	if err != nil {
		return p, fmt.Errorf("encode bitmap silk payload: %w", err)
	}
	root, err := bitmapSilkRequiredObject(data, "payload", []string{
		"schemaVersion", "source", "conversion", "polygons", "x", "y", "width", "height", "rotation", "mirror", "layer", "units", "anchor",
	})
	if err != nil {
		return p, err
	}
	if _, err := bitmapSilkRequiredObject(root["source"], "source", []string{"fileName", "format", "sha256", "pixelWidth", "pixelHeight"}); err != nil {
		return p, err
	}
	if _, err := bitmapSilkRequiredObject(root["conversion"], "conversion", []string{"threshold", "background", "invert", "simplify"}); err != nil {
		return p, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return BitmapSilkPayload{}, fmt.Errorf("decode bitmap silk payload: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return BitmapSilkPayload{}, fmt.Errorf("unexpected trailing bitmap silk payload data")
	}
	if err := p.Validate(); err != nil {
		return BitmapSilkPayload{}, err
	}
	return p, nil
}

func bitmapSilkRequiredObject(data []byte, name string, keys []string) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return nil, fmt.Errorf("bitmap silk %s must be an object", name)
	}
	allowed := make(map[string]bool, len(keys))
	for _, key := range keys {
		allowed[key] = true
		if raw, exists := fields[key]; !exists || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil, fmt.Errorf("bitmap silk %s.%s is required and must not be null", name, key)
		}
	}
	// DisallowUnknownFields uses case-insensitive struct matching. Check the
	// exact names too, matching the connector's Object.keys schema validation.
	for key := range fields {
		if !allowed[key] {
			return nil, fmt.Errorf("unknown bitmap silk %s field %q", name, key)
		}
	}
	return fields, nil
}

// Validate checks the typed plan, independently of the CLI converter. A valid
// plan is still unsupported for host writes; validation never grants permission.
func (p BitmapSilkPayload) Validate() error {
	if p.SchemaVersion != 1 || p.Units != "mil" || p.Anchor != "top-left" {
		return fmt.Errorf("bitmap silk requires schemaVersion 1, units mil and anchor top-left")
	}
	for _, n := range []float64{p.X, p.Y, p.Width, p.Height, p.Rotation} {
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return fmt.Errorf("bitmap silk geometry must be finite")
		}
	}
	if p.Width <= 0 || p.Height <= 0 || (p.Layer != 3 && p.Layer != 4) {
		return fmt.Errorf("bitmap silk requires positive dimensions and layer 3 or 4")
	}
	s := p.Source
	ext := strings.ToLower(filepath.Ext(s.FileName))
	if s.FileName == "" || strings.ContainsAny(s.FileName, "/\\") || !bitmapSilkSHA256.MatchString(s.SHA256) ||
		!((s.Format == "png" && ext == ".png") || (s.Format == "jpeg" && (ext == ".jpg" || ext == ".jpeg"))) ||
		s.PixelWidth <= 0 || s.PixelHeight <= 0 || s.PixelWidth > 1_000_000/s.PixelHeight {
		return fmt.Errorf("invalid bitmap source metadata (PNG/JPEG, SHA256, 1..1000000 pixels)")
	}
	if p.Conversion.Threshold < 0 || p.Conversion.Threshold > 255 || (p.Conversion.Background != "white" && p.Conversion.Background != "black") {
		return fmt.Errorf("invalid bitmap conversion: threshold 0..255, background white or black")
	}
	if len(p.Polygons) == 0 || len(p.Polygons) > 25_000 {
		return fmt.Errorf("bitmap silk requires non-empty bounded contours")
	}
	vertices := 0
	for _, c := range p.Polygons {
		if len(c) < 11 || len(c)%2 != 1 || c[2] != "L" {
			return fmt.Errorf("bitmap contour must be closed [x0,y0,L,x1,y1,...,x0,y0]")
		}
		vertices += (len(c) - 1) / 2
		if vertices > 100_000 {
			return fmt.Errorf("bitmap contours exceed 100000 vertices")
		}
		coordIndex := 0
		for i, value := range c {
			if i == 2 {
				continue
			}
			n, ok := value.(float64)
			if !ok || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || (coordIndex%2 == 0 && n > p.Width) || (coordIndex%2 == 1 && n > p.Height) {
				return fmt.Errorf("bitmap contour coordinates must be finite and within the source canvas")
			}
			coordIndex++
		}
		if c[0].(float64) != c[len(c)-2].(float64) || c[1].(float64) != c[len(c)-1].(float64) {
			return fmt.Errorf("bitmap contour is not closed")
		}
	}
	return nil
}
