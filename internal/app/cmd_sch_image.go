package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"image"
	_ "image/jpeg" // register JPEG decoder for reference-image intrinsic sizing
	_ "image/png"  // register PNG decoder for reference-image intrinsic sizing
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

const maxSchImageSourceBytes = 8 << 20
const maxSchImagePixels = 16_000_000

// newSchImageCmd wires `easyeda sch image create/list/modify` — the CLI path
// for issue #272's schematic reference images (non-electrical PNG/JPEG/SVG
// artwork placed beside the circuit: a module photo, dimension drawing, or
// pinout diagram). Deletion reuses the existing generic
// `easyeda sch prim-delete` (schematic.primitives.delete already routes any
// primitiveId — including these — through sch_PrimitiveObject.delete), so no
// separate delete verb is added here.
func newSchImageCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	image := &cobra.Command{
		Use:   "image",
		Short: "Non-electrical reference images on the schematic (photo/dimension/pinout diagrams, #272)",
		Long: `Import and manage NON-ELECTRICAL reference images embedded on the schematic
page — a module photo, dimension drawing, or pinout diagram placed beside the
circuit for visual cross-checking. Backed by eda.sch_PrimitiveObject (二进制
内嵌对象); it carries no net or pin and never touches components, wires, or
copper.

COORDINATES ARE SCHEMATIC raw units (0.01 inch, y-UP) — NOT the PCB domain's
mil. This is the same unit convention as ` + "`sch place`" + `'s --x/--y.

To delete an image, use ` + "`easyeda sch prim-delete --id <primitiveId>`" + `
(schematic.primitives.delete already routes any primitive id through the
correct delete path — no separate ` + "`sch image delete`" + ` exists).`,
	}
	image.AddCommand(newSchImageCreateCmd(cfg, window, stdout, stderr))
	image.AddCommand(newSchImageListCmd(cfg, window, stdout, stderr))
	image.AddCommand(newSchImageModifyCmd(cfg, window, stdout, stderr))
	return image
}

func newSchImageCreateCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var file string
	var x, y, width, height, rotation float64
	var mirror, dryRun bool
	c := &cobra.Command{
		Use:   "create",
		Short: "Import a PNG/JPEG/SVG as a non-electrical reference image",
		Args:  cobra.NoArgs,
		Long: `Import a local PNG/JPEG/SVG file as a reference image on the ACTIVE
schematic page. The file is read, base64-encoded, and sent to the connector,
which decodes it into a File and creates ONE sch_PrimitiveObject.

--x/--y place the artwork's TOP-LEFT corner in schematic raw units (0.01 inch,
y-UP) — NOT PCB mil (live-verified 2026-09-29: bbox.maxY equals --y, and the
artwork extends downward to bbox.minY — matching pcb silk-import-svg's
top-left convention on the other domain).

--width/--height (raw units, 1 raw unit = 1 source pixel for PNG/JPEG or 1 SVG
user unit for SVG) resize the artwork. The host does NOT preserve the source's
intrinsic size when both are omitted — live-verified: it substitutes a fixed
~50×40 raw placeholder regardless of the source's real dimensions or aspect
ratio, silently distorting the artwork. So this CLI decodes the source file's
real pixel/viewBox size and sends it explicitly whenever you omit --width or
--height, to make "keep intrinsic size" actually true. Passing only one of
--width/--height still scales the other to preserve the source's aspect ratio.
Supported extensions: .png, .jpg, .jpeg, .svg — anything else is refused
before any file is read.

--dry-run reads and validates the local file (size limit, extension,
complete PNG/JPEG decoding or SVG root and source dimensions, resolved
positive finite width/height) without contacting the connector. Sources are
limited to 8 MiB and raster images to 16 million pixels. Coordinates and
rotation must be finite; an explicitly supplied dimension must be positive.`,
		Example: `  easyeda sch image create --file ./module-photo.png --x 2000 --y -1000
  easyeda sch image create --file ./pinout.svg --x 0 --y 0 --width 800 --dry-run
  easyeda sch image create --file "接口示意图.jpg" --x 500 --y -500 --rotation 90`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if file == "" {
				return fmt.Errorf("--file is required")
			}
			if dryRun {
				defer setDispatchDryRun(true)()
			}
			if !schImageFinite(x, y, rotation) {
				return fmt.Errorf("x/y/rotation must be finite numbers")
			}
			if err := validateSchImageExt(file); err != nil {
				return err
			}
			data, err := readSchImageSource(file)
			if err != nil {
				return fmt.Errorf("read --file: %w", err)
			}
			fileName := filepath.Base(file)

			finalWidth, finalHeight, sizeErr := resolveSchImageDims(
				data, fileName,
				width, cmd.Flags().Changed("width"),
				height, cmd.Flags().Changed("height"),
			)
			if sizeErr != nil {
				return fmt.Errorf("determine image dimensions: %w", sizeErr)
			}

			if dryRun {
				out := map[string]any{
					"dryRun":   true,
					"fileName": fileName,
					"bytes":    len(data),
					"x":        x,
					"y":        y,
					"width":    finalWidth,
					"height":   finalHeight,
					"rotation": rotation,
					"mirror":   mirror,
				}
				enc := json.NewEncoder(stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(out)
			}
			payload := map[string]any{
				"dataBase64": base64.StdEncoding.EncodeToString(data),
				"fileName":   fileName,
				"x":          x,
				"y":          y,
				"width":      finalWidth,
				"height":     finalHeight,
			}
			if cmd.Flags().Changed("rotation") {
				payload["rotation"] = rotation
			}
			if cmd.Flags().Changed("mirror") {
				payload["mirror"] = mirror
			}
			return dispatch(cfg, "schematic.image.create", *window, payload, stdout, stderr)
		},
	}
	c.Flags().StringVar(&file, "file", "", "path to a PNG/JPEG/SVG file (required)")
	c.Flags().Float64Var(&x, "x", 0, "start-point X (schematic raw units, 0.01 inch)")
	c.Flags().Float64Var(&y, "y", 0, "start-point Y (schematic raw units, 0.01 inch)")
	c.Flags().Float64Var(&width, "width", 0, "target width (raw units; omit to keep intrinsic size)")
	c.Flags().Float64Var(&height, "height", 0, "target height (raw units; omit to keep intrinsic size)")
	c.Flags().Float64Var(&rotation, "rotation", 0, "rotation (deg)")
	c.Flags().BoolVar(&mirror, "mirror", false, "mirror the image")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "validate the local file only; do not contact the connector")
	return c
}

func newSchImageListCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var page string
	var stay bool
	c := &cobra.Command{
		Use:   "list",
		Short: "List reference images on the active schematic page",
		Args:  cobra.NoArgs,
		Long: `Read-only enumeration of every reference image (sch_PrimitiveObject) on the
ACTIVE schematic page. Page-lazy-load law applies: only the active page's
images are returned; switch pages with --page or ` + "`doc switch`" + ` to sweep a
multi-page project. The raw file content is never returned — geometry only.`,
		Example: `  easyeda sch image list
  easyeda sch image list --page P2`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if page != "" {
				scope, err := switchToPage(cfg, *window, page)
				if err != nil {
					return err
				}
				if !stay {
					defer func() { _ = scope.restore(cfg) }()
				}
				*window = scope.window
			}
			return dispatch(cfg, "schematic.image.list", *window, nil, stdout, stderr)
		},
	}
	c.Flags().StringVar(&page, "page", "", "switch to this page (name|uuid) first, list, then switch back")
	c.Flags().BoolVar(&stay, "stay", false, "with --page, stay on the target page after listing")
	return c
}

func newSchImageModifyCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var id string
	var x, y, width, height, rotation float64
	var mirror bool
	c := &cobra.Command{
		Use:   "modify",
		Short: "Reposition/resize/rotate/mirror an existing reference image",
		Args:  cobra.NoArgs,
		Long: `Modify an existing reference image's geometry by primitiveId (from
` + "`sch image create`" + ` or ` + "`sch image list`" + `). Only x/y/width/height/rotation/mirror
can change — replacing the artwork itself is a new ` + "`sch image create`" + `, not a
patch. Verified by readback: if only some fields actually applied, the result
reports partial:true + notApplied[] rather than failing outright (the canvas
already changed for whatever DID apply).`,
		Example: `  easyeda sch image modify --id abc123 --x 3000 --y -1500
  easyeda sch image modify --id abc123 --rotation 180 --mirror`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if id == "" {
				return fmt.Errorf("--id is required")
			}
			payload := map[string]any{"primitiveId": id}
			if cmd.Flags().Changed("x") {
				payload["x"] = x
			}
			if cmd.Flags().Changed("y") {
				payload["y"] = y
			}
			if cmd.Flags().Changed("width") {
				payload["width"] = width
			}
			if cmd.Flags().Changed("height") {
				payload["height"] = height
			}
			if cmd.Flags().Changed("rotation") {
				payload["rotation"] = rotation
			}
			if cmd.Flags().Changed("mirror") {
				payload["mirror"] = mirror
			}
			if len(payload) == 1 {
				return fmt.Errorf("nothing to modify — provide at least one of --x/--y/--width/--height/--rotation/--mirror")
			}
			return dispatch(cfg, "schematic.image.modify", *window, payload, stdout, stderr)
		},
	}
	c.Flags().StringVar(&id, "id", "", "primitive ID of the reference image (required)")
	c.Flags().Float64Var(&x, "x", 0, "new start-point X (raw units)")
	c.Flags().Float64Var(&y, "y", 0, "new start-point Y (raw units)")
	c.Flags().Float64Var(&width, "width", 0, "new width (raw units)")
	c.Flags().Float64Var(&height, "height", 0, "new height (raw units)")
	c.Flags().Float64Var(&rotation, "rotation", 0, "new rotation (deg)")
	c.Flags().BoolVar(&mirror, "mirror", false, "new mirror state")
	return c
}

// validateSchImageExt fails fast on an unsupported extension before any
// network round-trip — mirrors the connector's own MIME check so the CLI
// gives the same clear error without a wasted request.
func validateSchImageExt(file string) error {
	switch strings.ToLower(filepath.Ext(file)) {
	case ".png", ".jpg", ".jpeg", ".svg":
		return nil
	default:
		return fmt.Errorf("unsupported reference-image extension %q — supported: .png, .jpg, .jpeg, .svg", filepath.Ext(file))
	}
}

// Bound local reads as well as the dispatched payload; a changing file must
// not bypass the preflight size limit or force an unbounded allocation.
func readSchImageSource(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxSchImageSourceBytes {
		return nil, fmt.Errorf("reference image must be a regular file containing 1 to %d bytes", maxSchImageSourceBytes)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxSchImageSourceBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > maxSchImageSourceBytes {
		return nil, fmt.Errorf("reference image must contain 1 to %d bytes", maxSchImageSourceBytes)
	}
	return data, nil
}

func schImageFinite(values ...float64) bool {
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
	}
	return true
}

// Validate the source even with two explicit target dimensions. DecodeConfig
// alone accepts a valid header followed by truncated/corrupt pixel data.
func validateSchImageSource(data []byte, fileName string) (float64, float64, error) {
	if err := validateSchImageExt(fileName); err != nil {
		return 0, 0, err
	}
	if len(data) == 0 || len(data) > maxSchImageSourceBytes {
		return 0, 0, fmt.Errorf("reference image must contain 1 to %d bytes", maxSchImageSourceBytes)
	}
	ext := strings.ToLower(filepath.Ext(fileName))
	if ext == ".svg" {
		return svgIntrinsicSize(data)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0, fmt.Errorf("decode raster image header: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxSchImagePixels/cfg.Height {
		return 0, 0, fmt.Errorf("reference image exceeds 16 million pixels or has invalid dimensions")
	}
	if (ext == ".png" && format != "png") || (ext != ".png" && format != "jpeg") {
		return 0, 0, fmt.Errorf("image content does not match extension")
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return 0, 0, fmt.Errorf("decode raster image: %w", err)
	}
	return float64(cfg.Width), float64(cfg.Height), nil
}

// resolveSchImageDims fills in whichever of width/height the caller omitted,
// from the source file's REAL intrinsic size (decoded pixel dims for
// PNG/JPEG, width/height/viewBox for SVG) — never from the host default.
//
// WHY: live-verified 2026-09-29 on a real EasyEDA Pro window — omitting BOTH
// --width and --height does NOT keep the source's intrinsic size; the host
// substitutes a fixed ~50×40 raw placeholder regardless of the file's real
// dimensions or aspect ratio (confirmed identical across a 200×120 PNG, a
// 150×100 JPEG, and a 100×60 SVG — all three landed at the same 50×40 bbox).
// So "keep intrinsic size" can only be made true by decoding the file
// ourselves and sending explicit numbers.
//
// 1 raw unit = 1 source pixel for PNG/JPEG, or 1 SVG user unit for SVG (the
// live probe confirmed this: an explicit --width 200 --height 120 against a
// 200×120px PNG produced an exact 200×120 raw bbox). If only one of
// width/height was explicitly given, the other is derived to preserve the
// source's aspect ratio.
func resolveSchImageDims(data []byte, fileName string, width float64, widthSet bool, height float64, heightSet bool) (float64, float64, error) {
	if widthSet && (!schImageFinite(width) || width <= 0) || heightSet && (!schImageFinite(height) || height <= 0) {
		return 0, 0, fmt.Errorf("explicit width/height must be positive finite numbers")
	}
	srcW, srcH, err := validateSchImageSource(data, fileName)
	if err != nil {
		return 0, 0, err
	}
	if !schImageFinite(srcW, srcH) || srcW <= 0 || srcH <= 0 {
		return 0, 0, fmt.Errorf("source image has non-positive intrinsic size (%gx%g)", srcW, srcH)
	}
	switch {
	case widthSet && heightSet:
	case widthSet:
		height = width * (srcH / srcW)
	case heightSet:
		width = height * (srcW / srcH)
	default:
		width, height = srcW, srcH
	}
	if !schImageFinite(width, height) || width <= 0 || height <= 0 {
		return 0, 0, fmt.Errorf("resolved width/height must be positive finite numbers")
	}
	return width, height, nil
}

// schImageIntrinsicSize decodes the real pixel size of a PNG/JPEG, or the
// declared width/height/viewBox of an SVG. It never guesses: an SVG with
// neither width/height nor viewBox is refused rather than defaulting.
func schImageIntrinsicSize(data []byte, fileName string) (float64, float64, error) {
	if strings.EqualFold(filepath.Ext(fileName), ".svg") {
		return svgIntrinsicSize(data)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0, fmt.Errorf("decode raster image: %w", err)
	}
	return float64(cfg.Width), float64(cfg.Height), nil
}

// svgSizeAttrs mirrors just enough of the root <svg> element to recover its
// declared size — width/height (numbers, ignoring a unit suffix like "mm")
// or, failing that, the viewBox's third/fourth numbers.
type svgSizeAttrs struct {
	XMLName xml.Name
	Width   string `xml:"width,attr"`
	Height  string `xml:"height,attr"`
	ViewBox string `xml:"viewBox,attr"`
}

func svgIntrinsicSize(data []byte) (float64, float64, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := dec.Token()
		if err != nil {
			return 0, 0, fmt.Errorf("no root <svg> element found: %w", err)
		}
		if chars, ok := tok.(xml.CharData); ok && strings.TrimSpace(string(chars)) != "" {
			return 0, 0, fmt.Errorf("expected SVG root")
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if se.Name.Local != "svg" || se.Name.Space != "" && se.Name.Space != "http://www.w3.org/2000/svg" {
			return 0, 0, fmt.Errorf("expected SVG root")
		}
		var attrs svgSizeAttrs
		if err := dec.DecodeElement(&attrs, &se); err != nil {
			return 0, 0, fmt.Errorf("decode SVG: %w", err)
		}
		for {
			tail, err := dec.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				return 0, 0, fmt.Errorf("decode SVG: %w", err)
			}
			switch tail := tail.(type) {
			case xml.CharData:
				if strings.TrimSpace(string(tail)) != "" {
					return 0, 0, fmt.Errorf("unexpected content after SVG root")
				}
			case xml.Comment:
			default:
				return 0, 0, fmt.Errorf("unexpected content after SVG root")
			}
		}
		if w, h, ok := parseSvgNumericSize(attrs.Width, attrs.Height); ok {
			return w, h, nil
		}
		if w, h, ok := parseSvgViewBoxSize(attrs.ViewBox); ok {
			return w, h, nil
		}
		return 0, 0, fmt.Errorf("SVG root has no numeric width/height or viewBox — intrinsic size cannot be determined")
	}
}

func parseSvgNumericSize(w, h string) (float64, float64, bool) {
	wv, wErr := strconv.ParseFloat(stripSvgUnit(w), 64)
	hv, hErr := strconv.ParseFloat(stripSvgUnit(h), 64)
	if wErr != nil || hErr != nil || !schImageFinite(wv, hv) || wv <= 0 || hv <= 0 {
		return 0, 0, false
	}
	return wv, hv, true
}

func parseSvgViewBoxSize(vb string) (float64, float64, bool) {
	fields := strings.Fields(strings.ReplaceAll(vb, ",", " "))
	if len(fields) != 4 {
		return 0, 0, false
	}
	values := [4]float64{}
	for i, field := range fields {
		value, err := strconv.ParseFloat(field, 64)
		if err != nil || !schImageFinite(value) {
			return 0, 0, false
		}
		values[i] = value
	}
	return values[2], values[3], values[2] > 0 && values[3] > 0
}

// stripSvgUnit trims a trailing unit suffix (mm/cm/in/px/pt/pc/em/ex/%) from
// an SVG length so ParseFloat sees a bare number; a unitless value (the SVG
// default, "user units") passes through unchanged.
func stripSvgUnit(v string) string {
	v = strings.TrimSpace(v)
	for _, suffix := range []string{"mm", "cm", "in", "px", "pt", "pc", "em", "ex", "%"} {
		if strings.HasSuffix(v, suffix) {
			return strings.TrimSuffix(v, suffix)
		}
	}
	return v
}
