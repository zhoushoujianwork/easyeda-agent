package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/zhoushoujianwork/easyeda-agent/internal/pcb/bitmapimport"
	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

type bitmapSilkParameters struct {
	File         string   `json:"file"`
	X            float64  `json:"x"`
	Y            float64  `json:"y"`
	Width        *float64 `json:"width,omitempty"`
	Height       *float64 `json:"height,omitempty"`
	KeepAspect   bool     `json:"keepAspect"`
	Layer        *int     `json:"layer"`
	Rotation     float64  `json:"rotation"`
	Mirror       *bool    `json:"mirror,omitempty"`
	Threshold    int      `json:"threshold"`
	Background   string   `json:"background"`
	Invert       bool     `json:"invert"`
	Simplify     bool     `json:"simplify"`
	MinLineWidth float64  `json:"minLineWidth"`
}

func newPcbSilkImportBitmapCmd(stdout io.Writer) *cobra.Command {
	cli := bitmapSilkParameters{Threshold: 128, Background: "white", Simplify: true, MinLineWidth: 6}
	var from, out string
	var width, height float64
	var layer int
	var mirror, dry bool
	c := &cobra.Command{
		Use: "silk-import-bitmap", Short: "Prepare PNG/JPEG manufacturing silk polygons offline (host import unsupported)", Args: cobra.NoArgs,
		Long: `Convert PNG/JPEG to exact closed pixel-boundary contours, including holes.
--file or --from JSON supplies the source; relative JSON file paths resolve beside
the parameter file. Flags override JSON. --width or --height is required (mil);
one preserves aspect, both resize unless --keep-aspect is set. --layer MUST be
explicit: 3=TOP_SILKSCREEN, 4=BOTTOM_SILKSCREEN (auto-mirror unless overridden).
Local contours use source top-left, x-right/y-down; placement is --x/--y in mil,
y-UP. Planned geometry mirrors about the source canvas vertical center before
rotation (degrees) about the top-left. Host anchor/transform behavior is unverified.
--background white selects grayscale <= --threshold; black selects > threshold.
--invert reverses nontransparent selection; fully transparent pixels always stay
background. Partial alpha composites onto the chosen background first. --simplify
only removes collinear points; there is no smoothing or lossy contour reduction.
Limits: 8 MiB, 1 million pixels, 100000 vertices. Empty foreground is refused.
--dry-run prints geometry/polygons without EDA; --out writes a NEW Apply playbook
without EDA. Pixel pitch is a resolution warning, not a minimum-stroke guarantee;
board-edge, pad-overlap and manufacturing checks remain unsupported.
Without --dry-run/--out this command returns unsupported BEFORE any host call.
The typed pcb.silk.import_bitmap action also refuses writes, including Apply;
offline validation does not certify host creation, rendering or persistence.`,
		Example: `  easyeda pcb silk-import-bitmap --file "标志 logo.png" --width 600 --layer 3 --dry-run
  easyeda pcb silk-import-bitmap --from bitmap-parameters.json --out bitmap.apply.json
  easyeda apply bitmap.apply.json --dry-run`,
		RunE: func(cmd *cobra.Command, args []string) error {
			defer setDispatchDryRun(true)()
			p := bitmapSilkParameters{Threshold: 128, Background: "white", Simplify: true, MinLineWidth: 6}
			if from != "" {
				f, err := os.Open(from)
				if err != nil {
					return err
				}
				info, statErr := f.Stat()
				if statErr != nil || !info.Mode().IsRegular() || info.Size() > 64*1024 {
					f.Close()
					return fmt.Errorf("--from must be a regular parameter file of at most 64 KiB")
				}
				data, readErr := io.ReadAll(io.LimitReader(f, 64*1024+1))
				f.Close()
				if readErr != nil || len(data) > 64*1024 {
					return fmt.Errorf("read --from: invalid or oversized parameter file")
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
					return fmt.Errorf("--from must contain a JSON parameter object")
				}
				for key, value := range fields {
					if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
						return fmt.Errorf("parameter %s cannot be null", key)
					}
				}
				dec := json.NewDecoder(bytes.NewReader(data))
				dec.DisallowUnknownFields()
				err = dec.Decode(&p)
				var extra any
				if err == nil && dec.Decode(&extra) != io.EOF {
					err = fmt.Errorf("unexpected trailing parameter data")
				}
				if err != nil {
					return fmt.Errorf("decode --from: %w", err)
				}
				if p.File != "" && !filepath.IsAbs(p.File) {
					p.File = filepath.Join(filepath.Dir(from), p.File)
				}
			}
			for key, set := range map[string]func(){
				"file": func() { p.File = cli.File }, "x": func() { p.X = cli.X }, "y": func() { p.Y = cli.Y },
				"width": func() { p.Width = &width }, "height": func() { p.Height = &height }, "layer": func() { p.Layer = &layer },
				"rotation": func() { p.Rotation = cli.Rotation }, "mirror": func() { p.Mirror = &mirror },
				"keep-aspect": func() { p.KeepAspect = cli.KeepAspect }, "threshold": func() { p.Threshold = cli.Threshold },
				"background": func() { p.Background = cli.Background }, "invert": func() { p.Invert = cli.Invert },
				"simplify": func() { p.Simplify = cli.Simplify }, "min-line-width": func() { p.MinLineWidth = cli.MinLineWidth },
			} {
				if cmd.Flags().Changed(key) {
					set()
				}
			}
			if p.File == "" {
				return fmt.Errorf("--file or file in --from is required")
			}
			if p.Layer == nil || (*p.Layer != 3 && *p.Layer != 4) {
				return fmt.Errorf("explicit --layer or JSON layer 3|4 is required")
			}
			if !finitePcbImage(p.X, p.Y, p.Rotation, p.MinLineWidth) || p.MinLineWidth < 0 {
				return fmt.Errorf("placement and min-line-width must be finite; min-line-width >= 0")
			}
			if p.Width != nil && !positiveFinite(*p.Width) || p.Height != nil && !positiveFinite(*p.Height) {
				return fmt.Errorf("explicit width/height must be positive finite mil")
			}
			if p.Mirror == nil {
				v := *p.Layer == 4
				p.Mirror = &v
			}
			f, err := os.Open(p.File)
			if err != nil {
				return err
			}
			data, err := io.ReadAll(io.LimitReader(f, bitmapimport.MaxFileBytes+1))
			f.Close()
			if err != nil {
				return err
			}
			opts := bitmapimport.Options{KeepAspect: p.KeepAspect, Threshold: p.Threshold, Background: p.Background, Invert: p.Invert, Simplify: p.Simplify}
			if p.Width != nil {
				opts.TargetWidth = *p.Width
			}
			if p.Height != nil {
				opts.TargetHeight = *p.Height
			}
			r, err := bitmapimport.Parse(data, p.File, opts)
			if err != nil {
				return err
			}
			payload := protocol.BitmapSilkPayload{SchemaVersion: 1, Source: protocol.BitmapSilkSource{FileName: filepath.Base(p.File), Format: r.Format, SHA256: r.SHA256, PixelWidth: r.SourceWidth, PixelHeight: r.SourceHeight}, Conversion: protocol.BitmapSilkConversion{Threshold: p.Threshold, Background: p.Background, Invert: p.Invert, Simplify: p.Simplify}, Polygons: r.Polygons, X: p.X, Y: p.Y, Width: r.Width, Height: r.Height, Rotation: p.Rotation, Mirror: *p.Mirror, Layer: *p.Layer, Units: "mil", Anchor: "top-left"}
			if err := payload.Validate(); err != nil {
				return err
			}
			bbox, inkBBox := bitmapSilkBBox(payload, nil), bitmapSilkBBox(payload, r.Polygons)
			for _, box := range []map[string]float64{bbox, inkBBox} {
				for _, n := range box {
					if !finitePcbImage(n) {
						return fmt.Errorf("planned bitmap bbox overflows finite coordinates")
					}
				}
			}
			report := map[string]any{"validation": "offline-verified", "hostSupport": "unsupported", "writeAttempted": false, "dryRun": dry, "payload": payload, "contours": r.PathCount, "vertices": r.PointCount, "foregroundPixels": r.ForegroundPixels, "pixelPitch": map[string]float64{"x": r.PixelWidth, "y": r.PixelHeight}, "bbox": bbox, "inkBBox": inkBBox, "bboxKind": "planned-geometry", "hostBBoxVerified": false, "dfm": map[string]any{"status": "unsupported", "minimumStroke": "not-measured", "boardEdge": "not-checked", "padOverlap": "not-checked"}}
			if math.Min(r.PixelWidth, r.PixelHeight) < p.MinLineWidth {
				report["resolutionWarning"] = fmt.Sprintf("pixel pitch below %.3g mil; this is not a minimum-stroke measurement", p.MinLineWidth)
			}
			if out != "" {
				pb := playbook{Version: 1, Meta: playbookMeta{Name: "bitmap manufacturing silk (host unsupported)"}, Steps: []playbookStep{{ID: "bitmap-silk", Action: protocol.BitmapSilkAction, Payload: bitmapSilkPayloadMap(payload)}}}
				encoded, err := json.MarshalIndent(pb, "", "  ")
				if err != nil {
					return err
				}
				f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
				if err != nil {
					return fmt.Errorf("create --out (must be new): %w", err)
				}
				_, err = f.Write(append(encoded, '\n'))
				closeErr := f.Close()
				if err != nil {
					return err
				}
				if closeErr != nil {
					return closeErr
				}
				report["applyFile"] = out
			}
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(report); err != nil {
				return err
			}
			if !dry && out == "" {
				return fmt.Errorf("%s", protocol.BitmapSilkUnsupported)
			}
			return nil
		},
	}
	c.Flags().StringVar(&cli.File, "file", "", "PNG/JPEG source file")
	c.Flags().StringVar(&from, "from", "", "JSON parameters; flags override fields")
	c.Flags().StringVar(&out, "out", "", "write NEW Apply playbook locally; action remains unsupported")
	c.Flags().Float64Var(&cli.X, "x", 0, "source top-left X (mil)")
	c.Flags().Float64Var(&cli.Y, "y", 0, "source top-left Y (mil, y-UP)")
	c.Flags().Float64Var(&width, "width", 0, "full canvas physical width (mil; width or height required)")
	c.Flags().Float64Var(&height, "height", 0, "full canvas physical height (mil)")
	c.Flags().BoolVar(&cli.KeepAspect, "keep-aspect", false, "fit uniformly when both dimensions supplied")
	c.Flags().IntVar(&layer, "layer", 0, "explicit silk layer: 3=TOP, 4=BOTTOM")
	c.Flags().Float64Var(&cli.Rotation, "rotation", 0, "planned rotation about top-left (degrees)")
	c.Flags().BoolVar(&mirror, "mirror", false, "planned horizontal mirror (auto-true for layer 4)")
	c.Flags().IntVar(&cli.Threshold, "threshold", 128, "Rec.601 grayscale threshold (0..255)")
	c.Flags().StringVar(&cli.Background, "background", "white", "alpha composite background: white or black")
	c.Flags().BoolVar(&cli.Invert, "invert", false, "invert nontransparent selection")
	c.Flags().BoolVar(&cli.Simplify, "simplify", true, "remove collinear points only (lossless)")
	c.Flags().Float64Var(&cli.MinLineWidth, "min-line-width", 6, "pixel resolution warning threshold (mil), not DFM")
	c.Flags().BoolVar(&dry, "dry-run", false, "convert and print planned geometry locally; no EDA")
	return c
}

func bitmapSilkPayloadMap(p protocol.BitmapSilkPayload) map[string]any {
	data, _ := json.Marshal(p)
	var result map[string]any
	_ = json.Unmarshal(data, &result)
	return result
}

// This is explicit planned geometry, not a claim about PrimitiveImage's native
// mirror/anchor behavior. Mirrors source x about the canvas center before rotation.
func bitmapSilkBBox(p protocol.BitmapSilkPayload, polygons [][]any) map[string]float64 {
	points := [][2]float64{{0, 0}, {p.Width, 0}, {p.Width, p.Height}, {0, p.Height}}
	if polygons != nil {
		points = nil
		for _, c := range polygons {
			for i := 0; i < len(c); {
				if i == 2 {
					i++
					continue
				}
				points = append(points, [2]float64{c[i].(float64), c[i+1].(float64)})
				i += 2
			}
		}
	}
	angle := math.Mod(p.Rotation, 360) * math.Pi / 180
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, v := range points {
		x, y := v[0], -v[1]
		if p.Mirror {
			x = p.Width - x
		}
		tx, ty := p.X+x*math.Cos(angle)-y*math.Sin(angle), p.Y+x*math.Sin(angle)+y*math.Cos(angle)
		minX, minY, maxX, maxY = math.Min(minX, tx), math.Min(minY, ty), math.Max(maxX, tx), math.Max(maxY, ty)
	}
	return map[string]float64{"minX": minX, "minY": minY, "maxX": maxX, "maxY": maxY}
}
