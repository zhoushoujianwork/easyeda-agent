package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"image"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

func newPcbImageCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	root := &cobra.Command{Use: "image", Short: "Manage embedded reference pictures on PCB document layer 13", Long: "Reference pictures preserve their source artwork on DOCUMENT layer 13. Manufacturing silk uses pcb silk-import-svg instead."}
	var file string
	var x, y, w, h, rotation float64
	var mirror, dry bool
	var layer int
	create := &cobra.Command{Use: "create", Short: "Import PNG/JPEG/SVG as a document-layer reference picture", Args: cobra.NoArgs,
		Long: `Import an embedded reference picture, preserving colors and transparency.
--layer must be 13 (DOCUMENT). It does not create copper or manufacturing silk.
--x/--y is the source top-left anchor in mil, y-UP. Mirroring reflects the
rotated image across the vertical axis through that anchor; at rotation 0,
a mirrored picture extends left to x-width. --width/--height are physical
sizes in mil; at least one is required. One dimension preserves aspect;
both resize independently. --rotation is in degrees; --mirror is horizontal.
--dry-run validates the local source and reports the transformed target bbox
without contacting EDA. Web rendering/persistence must be checked after save/reload.`,
		Example: `  easyeda pcb image create --file "module photo.png" --x 1000 --y -1000 --width 600 --dry-run`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if dry {
				defer setDispatchDryRun(true)()
			}
			if layer != 13 {
				return fmt.Errorf("reference pictures require --layer 13 (DOCUMENT)")
			}
			if file == "" {
				return fmt.Errorf("--file is required")
			}
			if err := validateSchImageExt(file); err != nil {
				return err
			}
			info, err := os.Stat(file)
			if err != nil {
				return err
			}
			if info.Size() <= 0 || info.Size() > 8<<20 {
				return fmt.Errorf("reference image must contain 1 to 8388608 bytes")
			}
			data, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			// Always validate the source, including when both target dimensions are explicit.
			sw, sh, err := schImageIntrinsicSize(data, file)
			if err != nil {
				return err
			}
			if strings.EqualFold(filepath.Ext(file), ".svg") {
				var root struct{ XMLName xml.Name }
				if err := xml.Unmarshal(data, &root); err != nil {
					return fmt.Errorf("decode SVG: %w", err)
				}
				if root.XMLName.Local != "svg" {
					return fmt.Errorf("expected SVG root")
				}
			} else {
				if sw*sh > 16_000_000 {
					return fmt.Errorf("reference image exceeds 16 million pixels")
				}
				_, format, decodeErr := image.Decode(bytes.NewReader(data))
				if decodeErr != nil {
					return fmt.Errorf("decode image: %w", decodeErr)
				}
				ext := strings.ToLower(filepath.Ext(file))
				if (ext == ".png" && format != "png") || (ext != ".png" && format != "jpeg") {
					return fmt.Errorf("image content does not match extension")
				}
			}
			if !cmd.Flags().Changed("width") && !cmd.Flags().Changed("height") {
				return fmt.Errorf("--width or --height is required (mil; no pixel-to-physical-size assumption)")
			}
			if cmd.Flags().Changed("width") && !positiveFinite(w) || cmd.Flags().Changed("height") && !positiveFinite(h) {
				return fmt.Errorf("width/height must be positive finite numbers")
			}
			if !positiveFinite(sw) || !positiveFinite(sh) {
				return fmt.Errorf("invalid source dimensions")
			}
			if !cmd.Flags().Changed("width") {
				w = h * sw / sh
			}
			if !cmd.Flags().Changed("height") {
				h = w * sh / sw
			}
			if !finitePcbImage(x, y, w, h, rotation) || !positiveFinite(w) || !positiveFinite(h) {
				return fmt.Errorf("invalid image geometry")
			}
			payload := map[string]any{"fileName": filepath.Base(file), "x": x, "y": y, "width": w, "height": h, "rotation": rotation, "mirror": mirror, "layer": layer}
			if dry {
				payload["dryRun"] = true
				payload["bytes"] = len(data)
				payload["bbox"] = pcbImageBBox(x, y, w, h, rotation, mirror)
				return json.NewEncoder(stdout).Encode(payload)
			}
			payload["dataBase64"] = base64.StdEncoding.EncodeToString(data)
			return dispatchPcbImage(cfg, *window, "pcb.image.create", payload, stdout, stderr)
		}}
	create.Flags().StringVar(&file, "file", "", "PNG/JPEG/SVG source file (required)")
	create.Flags().Float64Var(&x, "x", 0, "top-left X (mil)")
	create.Flags().Float64Var(&y, "y", 0, "top-left Y (mil)")
	create.Flags().Float64Var(&w, "width", 0, "physical width (mil; width or height required)")
	create.Flags().Float64Var(&h, "height", 0, "physical height (mil)")
	create.Flags().Float64Var(&rotation, "rotation", 0, "rotation (degrees)")
	create.Flags().BoolVar(&mirror, "mirror", false, "horizontal mirror")
	create.Flags().IntVar(&layer, "layer", 13, "reference layer: 13=DOCUMENT only")
	create.Flags().BoolVar(&dry, "dry-run", false, "validate locally without EDA calls")
	root.AddCommand(create)
	var listID string
	list := &cobra.Command{Use: "list", Short: "Read embedded pictures and actual layers, optionally by ID", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		p := map[string]any{}
		if listID != "" {
			p["primitiveId"] = listID
		}
		return dispatch(cfg, "pcb.image.list", *window, p, stdout, stderr)
	}}
	list.Flags().StringVar(&listID, "id", "", "read only this embedded-object ID")
	root.AddCommand(list)
	var id string
	var mx, my, mw, mh, mr float64
	var mm bool
	modify := &cobra.Command{Use: "modify", Short: "Change a document-layer picture's geometry by ID", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if id == "" {
			return fmt.Errorf("--id is required")
		}
		p := map[string]any{"primitiveId": id}
		for key, value := range map[string]float64{"x": mx, "y": my, "width": mw, "height": mh, "rotation": mr} {
			if cmd.Flags().Changed(key) {
				if !finitePcbImage(value) || ((key == "width" || key == "height") && !positiveFinite(value)) {
					return fmt.Errorf("invalid %s", key)
				}
				p[key] = value
			}
		}
		if cmd.Flags().Changed("mirror") {
			p["mirror"] = mm
		}
		if len(p) == 1 {
			return fmt.Errorf("provide geometry fields to modify")
		}
		return dispatchPcbImage(cfg, *window, "pcb.image.modify", p, stdout, stderr)
	}}
	modify.Flags().StringVar(&id, "id", "", "embedded-object ID (required)")
	modify.Flags().Float64Var(&mx, "x", 0, "top-left X (mil)")
	modify.Flags().Float64Var(&my, "y", 0, "top-left Y (mil)")
	modify.Flags().Float64Var(&mw, "width", 0, "width (mil)")
	modify.Flags().Float64Var(&mh, "height", 0, "height (mil)")
	modify.Flags().Float64Var(&mr, "rotation", 0, "rotation (degrees)")
	modify.Flags().BoolVar(&mm, "mirror", false, "horizontal mirror")
	root.AddCommand(modify)
	var deleteID string
	del := &cobra.Command{Use: "delete", Short: "Delete one document-layer picture and verify its absence", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if deleteID == "" {
			return fmt.Errorf("--id is required")
		}
		return dispatchPcbImage(cfg, *window, "pcb.image.delete", map[string]any{"primitiveId": deleteID}, stdout, stderr)
	}}
	del.Flags().StringVar(&deleteID, "id", "", "embedded-object ID (required)")
	root.AddCommand(del)
	return root
}

func positiveFinite(v float64) bool { return v > 0 && finitePcbImage(v) }
func finitePcbImage(v ...float64) bool {
	for _, n := range v {
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return false
		}
	}
	return true
}

// The source rectangle extends downward from its top-left; transform around that anchor.
func pcbImageBBox(x, y, w, h, r float64, mirror bool) map[string]float64 {
	angle := r * math.Pi / 180
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, p := range [][2]float64{{0, 0}, {w, 0}, {w, -h}, {0, -h}} {
		px, py := p[0], p[1]
		// Web 4.1.60 mirrors the rotated rectangle about the anchor's vertical
		// axis, not about the artwork center or in unrotated source space.
		dx := px*math.Cos(angle) - py*math.Sin(angle)
		if mirror {
			dx = -dx
		}
		tx, ty := x+dx, y+px*math.Sin(angle)+py*math.Cos(angle)
		minX = math.Min(minX, tx)
		maxX = math.Max(maxX, tx)
		minY = math.Min(minY, ty)
		maxY = math.Max(maxY, ty)
	}
	return map[string]float64{"minX": minX, "minY": minY, "maxX": maxX, "maxY": maxY}
}

// Preserve mutation output and IDs even when fresh readback fails; never replay automatically.
func dispatchPcbImage(cfg *appConfig, window, action string, p map[string]any, stdout, stderr io.Writer) error {
	var output bytes.Buffer
	err := dispatch(cfg, action, window, p, &output, stderr)
	if _, writeErr := stdout.Write(output.Bytes()); writeErr != nil {
		return writeErr
	}
	if err != nil {
		return err
	}
	var response struct {
		Result struct {
			Verified    bool   `json:"verified"`
			Partial     bool   `json:"partial"`
			PrimitiveID string `json:"primitiveId"`
		}
	}
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		return fmt.Errorf("image mutation readback unavailable: %w", err)
	}
	if !response.Result.Verified || response.Result.Partial {
		return fmt.Errorf("image mutation not fully verified (primitiveId=%s); read pcb image list before retrying", response.Result.PrimitiveID)
	}
	return nil
}
