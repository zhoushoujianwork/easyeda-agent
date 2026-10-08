// Package bitmapimport converts PNG/JPEG pixels into closed, exact pixel-boundary
// contours in the same complex-polygon form as svgimport. The contours use an
// even-odd fill: inner contours preserve holes, and diagonal foreground pixels
// remain separate contours that may touch at a corner.
//
// Coordinates are in mil, with the image canvas origin at the top-left, +x to
// the right and +y downward. Transparent padding remains part of the canvas.
// This package performs an offline conversion only; it neither calls an EDA
// host nor establishes that a host supports bitmap/silkscreen insertion.
package bitmapimport

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"path/filepath"
	"strings"
)

const (
	MaxFileBytes = 8 * 1024 * 1024
	MaxPixels    = 1_000_000
	MaxPoints    = 100_000
)

// Options controls thresholding and physical sizing. Zero target dimensions
// mean unspecified; at least one dimension must be positive and finite.
type Options struct {
	TargetWidth  float64
	TargetHeight float64
	KeepAspect   bool
	// Threshold is a Rec.601 luminance threshold in [0,255]; zero is valid.
	Threshold int
	// Background is white (the default) or black. Partial transparency is
	// composited onto it before thresholding. White selects dark pixels
	// (luminance <= Threshold), black selects bright pixels (> Threshold).
	Background string
	// Invert flips the selection of nontransparent pixels only.
	Invert bool
	// Simplify removes only collinear vertices, without moving an edge or
	// changing the raster coverage. It is not a curve approximation.
	Simplify bool
}

// Result describes offline conversion, preserving the full source canvas.
type Result struct {
	// Each closed contour is [x0,y0,"L",x1,y1,...,x0,y0]. Numbers are float64.
	Polygons [][]any `json:"polygons"`
	Width    float64 `json:"width"`  // full canvas width in mil
	Height   float64 `json:"height"` // full canvas height in mil
	// SourceWidth/SourceHeight are source raster dimensions in pixels.
	SourceWidth      int    `json:"sourceWidth"`
	SourceHeight     int    `json:"sourceHeight"`
	Format           string `json:"format"` // png or jpeg, detected from contents
	SHA256           string `json:"sha256"`
	ForegroundPixels int    `json:"foregroundPixels"`
	PathCount        int    `json:"pathCount"`
	// PointCount includes the repeated starting vertex of each closed contour.
	PointCount int `json:"pointCount"`
	// PixelWidth/PixelHeight are physical pixel pitches in mil/px. They are
	// resolution information, not a minimum-feature or DFM pass assertion.
	PixelWidth  float64 `json:"pixelWidth"`
	PixelHeight float64 `json:"pixelHeight"`
}

// Parse decodes a PNG/JPEG, thresholds its pixels, and extracts exact contours.
// Input byte and decoded pixel limits are checked before full image decoding;
// contour output is capped separately to bound highly fragmented artwork.
func Parse(data []byte, fileName string, opts Options) (*Result, error) {
	if err := validateOptions(opts); err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("empty bitmap input")
	}
	if len(data) > MaxFileBytes {
		return nil, fmt.Errorf("bitmap exceeds %d-byte limit", MaxFileBytes)
	}
	wantFormat := ""
	switch strings.ToLower(filepath.Ext(fileName)) {
	case ".png":
		wantFormat = "png"
	case ".jpg", ".jpeg":
		wantFormat = "jpeg"
	default:
		return nil, fmt.Errorf("bitmap filename must have .png, .jpg or .jpeg extension")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode bitmap config: %w", err)
	}
	if format != wantFormat {
		return nil, fmt.Errorf("bitmap content format %q does not match filename format %q", format, wantFormat)
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > MaxPixels/config.Height {
		return nil, fmt.Errorf("bitmap dimensions %dx%d exceed %d-pixel limit or are invalid", config.Width, config.Height, MaxPixels)
	}
	sx, sy := scaleFactors(config.Width, config.Height, opts)
	width, height := float64(config.Width)*sx, float64(config.Height)*sy
	if !positiveFinite(sx) || !positiveFinite(sy) || !positiveFinite(width) || !positiveFinite(height) {
		return nil, fmt.Errorf("bitmap scale produces a nonpositive or nonfinite canvas or pixel pitch")
	}
	img, decodedFormat, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode bitmap: %w", err)
	}
	bounds := img.Bounds()
	if decodedFormat != format || bounds.Dx() != config.Width || bounds.Dy() != config.Height {
		return nil, fmt.Errorf("bitmap decoded dimensions or format differ from config")
	}
	background := opts.Background
	if background == "" {
		background = "white"
	}
	mask := make([]bool, config.Width*config.Height)
	foreground := 0
	// RGBA returns alpha-premultiplied 16-bit channels. Add the uncovered
	// white fraction directly, or retain those channels for a black backdrop.
	threshold := uint64(opts.Threshold) * 257 * 1000
	for y := 0; y < config.Height; y++ {
		for x := 0; x < config.Width; x++ {
			r, g, b, a := img.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			if a == 0 {
				continue // fully transparent remains background, also when inverted
			}
			if background == "white" {
				uncovered := uint32(65535) - a
				r, g, b = r+uncovered, g+uncovered, b+uncovered
			}
			luminance := uint64(299)*uint64(r) + uint64(587)*uint64(g) + uint64(114)*uint64(b)
			selected := luminance <= threshold
			if background == "black" {
				selected = luminance > threshold
			}
			if opts.Invert {
				selected = !selected
			}
			if selected {
				mask[y*config.Width+x] = true
				foreground++
			}
		}
	}
	if foreground == 0 {
		return nil, fmt.Errorf("bitmap has no foreground pixels after thresholding")
	}
	contours, points, err := traceContours(mask, config.Width, config.Height, opts.Simplify)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	result := &Result{
		Polygons: make([][]any, 0, len(contours)), Width: width, Height: height,
		SourceWidth: config.Width, SourceHeight: config.Height,
		Format: format, SHA256: hex.EncodeToString(sum[:]),
		ForegroundPixels: foreground, PathCount: len(contours), PointCount: points,
		PixelWidth: sx, PixelHeight: sy,
	}
	for _, contour := range contours {
		polygon := make([]any, 0, 2*len(contour)+1)
		for i, p := range contour {
			polygon = append(polygon, float64(p.x)*sx, float64(p.y)*sy)
			if i == 0 {
				polygon = append(polygon, "L")
			}
		}
		result.Polygons = append(result.Polygons, polygon)
	}
	return result, nil
}

func validateOptions(opts Options) error {
	if opts.TargetWidth < 0 || opts.TargetHeight < 0 || math.IsNaN(opts.TargetWidth) || math.IsNaN(opts.TargetHeight) || math.IsInf(opts.TargetWidth, 0) || math.IsInf(opts.TargetHeight, 0) {
		return fmt.Errorf("target dimensions must be zero or positive finite mil values")
	}
	if opts.TargetWidth == 0 && opts.TargetHeight == 0 {
		return fmt.Errorf("at least one positive target width or height is required")
	}
	if opts.Threshold < 0 || opts.Threshold > 255 {
		return fmt.Errorf("threshold must be in [0,255]")
	}
	if opts.Background != "" && opts.Background != "white" && opts.Background != "black" {
		return fmt.Errorf("background must be white or black")
	}
	return nil
}

func scaleFactors(w, h int, opts Options) (float64, float64) {
	tw, th := opts.TargetWidth, opts.TargetHeight
	switch {
	case tw > 0 && th > 0 && !opts.KeepAspect:
		return tw / float64(w), th / float64(h)
	case tw > 0 && th > 0:
		s := math.Min(tw/float64(w), th/float64(h))
		return s, s
	case tw > 0:
		s := tw / float64(w)
		return s, s
	default:
		s := th / float64(h)
		return s, s
	}
}

func positiveFinite(v float64) bool { return v > 0 && !math.IsInf(v, 0) && !math.IsNaN(v) }
