package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const maxManufactureBytes = 20 << 20

func newPcbManufactureExportCmd(cfg *appConfig, window *string, stdout, stderr io.Writer, kind string) *cobra.Command {
	var out, name, unit, format, digital string
	var color bool
	c := &cobra.Command{
		Use: "export-" + kind, Short: "Export the official PCB " + kind + " file with integrity and content readback",
		Long:    "Read-only official beta manufacture export. Requires --window and a new --out file.\nChecks complete PCB readbacks before/after, artifact size/SHA-256 and output content before writing.\nGerber is preserved as ZIP with an enumerated file list; placement CSV/XLSX exposes designator, center, layer and rotation.\nNo implicit save/reload or GUI fallback. Unsupported APIs and missing/empty/oversized files fail.\nMaximum file size 20 MiB; ZIP expansion limit 100 MiB. Export success does not certify manufacturing readiness.",
		Args:    cobra.NoArgs,
		Example: "  easyeda pcb export-" + kind + " --window <id> --project <uuid> --doc <pcb-uuid> --out ./board." + map[string]string{"gerber": "zip", "placement": "csv"}[kind],
		RunE: func(cmd *cobra.Command, _ []string) error {
			if *window == "" || out == "" {
				return fmt.Errorf("--window and a new --out file are required")
			}
			if unit != "mm" && (kind == "gerber" && unit != "inch" || kind == "placement" && unit != "mil") {
				return fmt.Errorf("unsupported --unit for %s", kind)
			}
			if kind == "placement" && format != "csv" && format != "xlsx" {
				return fmt.Errorf("--format must be csv or xlsx")
			}
			if _, err := os.Lstat(out); err == nil {
				return fmt.Errorf("--out must be new")
			} else if !os.IsNotExist(err) {
				return err
			}
			if name == "" {
				name = filepath.Base(out)
			}
			if strings.TrimSpace(name) == "" || name == "." || name == ".." || len(name) > 128 || strings.ContainsAny(name, "/\\") || strings.IndexFunc(name, func(r rune) bool { return r < 32 }) >= 0 {
				return fmt.Errorf("--name must be a basename of at most 128 bytes")
			}
			payload := map[string]any{"fileName": name, "unit": unit}
			fileType := format
			if kind == "gerber" {
				fileType = "zip"
				if digital == "" {
					digital = "4:5"
					if unit == "inch" {
						digital = "3:6"
					}
				}
				parts := strings.Split(digital, ":")
				if len(parts) != 2 {
					return fmt.Errorf("--digital-format must be integer:decimal, each 1..6")
				}
				a, e1 := strconv.Atoi(parts[0])
				b, e2 := strconv.Atoi(parts[1])
				if e1 != nil || e2 != nil || a < 1 || a > 6 || b < 1 || b > 6 {
					return fmt.Errorf("--digital-format must be integer:decimal, each 1..6")
				}
				payload["digitalFormat"], payload["colorSilkscreen"] = map[string]int{"integerNumber": a, "decimalNumber": b}, color
			} else {
				payload["fileType"] = format
			}
			if !strings.EqualFold(filepath.Ext(out), "."+fileType) {
				return fmt.Errorf("--out extension must be .%s", fileType)
			}
			opts := boardSnapshotOpts{withSilk: true, withRules: true, withLayers: true, withCopper: true}
			before, err := fetchBoardSnapshot(cfg, *window, opts)
			if err != nil {
				return err
			}
			if err = valid3DBoard(before, *window); err != nil {
				return fmt.Errorf("manufacture export needs a complete PCB readback: %w", err)
			}
			if cfg.doc != "" && cfg.doc != before.Capture.DocumentUUID {
				return fmt.Errorf("--doc must be the active PCB UUID; switch to it before export")
			}
			res, err := requestActionTimed(cfg, "pcb.export."+kind, *window, payload, 180*time.Second)
			if err != nil {
				return err
			}
			after, err := fetchBoardSnapshot(cfg, *window, opts)
			if err != nil {
				return err
			}
			if err = valid3DBoard(after, *window); err != nil {
				return err
			}
			if before.SemanticSHA256 != after.SemanticSHA256 || before.Capture.ProjectUUID != after.Capture.ProjectUUID || before.Capture.DocumentUUID != after.Capture.DocumentUUID || before.Capture.TabID != after.Capture.TabID {
				return fmt.Errorf("PCB identity/version changed during manufacture export; no output accepted")
			}
			if res.Context == nil || res.WindowID != *window || res.Context.ProjectUUID != before.Capture.ProjectUUID || res.Context.DocumentUUID != before.Capture.DocumentUUID || res.Context.DocumentType != "pcb" || res.Context.TabID != before.Capture.TabID {
				return fmt.Errorf("manufacture response target differs from PCB readbacks")
			}
			data, err := verifyManufactureArtifact(res, fileType, unit)
			if err != nil {
				return err
			}
			var content any
			if kind == "gerber" {
				content, err = inspectGerberArchive(data)
			} else {
				content, err = inspectPlacement(data, fileType, unit)
			}
			if err != nil {
				return err
			}
			if err = writeNewManufactureFile(out, data); err != nil {
				return err
			}
			res.Result["out"], res.Result["content"] = out, content
			res.Result["artifactSha256"], res.Result["artifactSize"] = res.Artifacts[0].SHA256, res.Artifacts[0].Size
			res.Result["semanticSha256"], res.Result["manufacturingAccepted"] = before.SemanticSHA256, false
			return encodeResultEnvelope(res, res.Result, stdout)
		},
	}
	c.Flags().Bool("json", true, "emit structured JSON (the default output; accepted for scripts)")
	c.Flags().StringVar(&out, "out", "", "new output file (.zip for Gerber; .csv or .xlsx for placement), required")
	c.Flags().StringVar(&name, "name", "", "official export basename (default output basename)")
	c.Flags().StringVar(&unit, "unit", "mm", map[string]string{"gerber": "mm | inch", "placement": "mm | mil"}[kind])
	if kind == "gerber" {
		c.Flags().BoolVar(&color, "color-silkscreen", false, "request JLCEDA color silkscreen manufacture data")
		c.Flags().StringVar(&digital, "digital-format", "", "integer:decimal digits, each 1..6 (default mm=4:5, inch=3:6)")
	} else {
		c.Flags().StringVar(&format, "format", "csv", "csv | xlsx; preserves official bytes and reads placement fields")
	}
	return c
}

func verifyManufactureArtifact(res *actionResult, format, unit string) ([]byte, error) {
	if res == nil || !res.OK || len(res.Artifacts) != 1 || res.Result["fileType"] != format || res.Result["unit"] != unit {
		return nil, fmt.Errorf("manufacture export returned no single artifact matching format/unit")
	}
	a := res.Artifacts[0]
	if a.ID == "" || a.Path == "" || a.Size <= 0 || a.Size > maxManufactureBytes || res.Result["artifactId"] != a.ID {
		return nil, fmt.Errorf("manufacture artifact identity/size/path invalid")
	}
	f, err := os.Open(a.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxManufactureBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != a.Size || res.Result["size"] != float64(len(data)) || sha256Hex(data) != a.SHA256 {
		return nil, fmt.Errorf("manufacture artifact getter/daemon size or SHA-256 mismatch")
	}
	return data, nil
}

// Publish only fully validated bytes; never overwrite an existing output or
// leave a partially written final file after an I/O error.
func writeNewManufactureFile(out string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(out), ".manufacture-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Link(tmp.Name(), out)
}
