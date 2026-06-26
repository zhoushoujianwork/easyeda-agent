package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
)

// newBomCmd returns the "bom" subcommand group.
func newBomCmd(cfg *appConfig, stdout, stderr io.Writer) *cobra.Command {
	var window string

	bom := &cobra.Command{
		Use:   "bom",
		Short: "BOM export and enrichment",
	}
	bom.PersistentFlags().StringVar(&window, "window", "", "EasyEDA window ID")

	bom.AddCommand(
		newBomExportCmd(cfg, &window, stdout, stderr),
		newBomEnrichCmd(stdout, stderr),
	)
	return bom
}

// ── bom export ────────────────────────────────────────────────────────────
// schematic.export.bom

func newBomExportCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var fileType, template, columnsJSON string

	c := &cobra.Command{
		Use:   "export",
		Short: "Export schematic BOM as csv or xlsx artifact",
		Args:  cobra.NoArgs,
		Example: `  easyeda bom export --type csv
  easyeda bom export --type xlsx --columns '["designator","value","footprint","lcsc"]'`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if fileType == "" {
				return fmt.Errorf("--type is required (csv or xlsx)")
			}
			payload := map[string]any{"fileType": fileType}
			if template != "" {
				payload["template"] = template
			}
			if columnsJSON != "" {
				var cols []any
				if err := json.Unmarshal([]byte(columnsJSON), &cols); err != nil {
					return fmt.Errorf("invalid --columns json (expected array): %w", err)
				}
				payload["columns"] = cols
			}
			return dispatch(cfg, "schematic.export.bom", *window, payload, stdout, stderr)
		},
	}
	c.Flags().StringVar(&fileType, "type", "", "output file type: csv or xlsx (required)")
	c.Flags().StringVar(&template, "template", "", "BOM template name")
	c.Flags().StringVar(&columnsJSON, "columns", "", `JSON array of column names, e.g. '["designator","value"]'`)
	return c
}

// ── bom enrich ────────────────────────────────────────────────────────────
// Shell out to bom-enrich.py

func newBomEnrichCmd(stdout, stderr io.Writer) *cobra.Command {
	var outFile, scriptPath string

	c := &cobra.Command{
		Use:   "enrich <bom.tsv>",
		Short: "Enrich a BOM TSV with LCSC C-numbers via bom-enrich.py",
		Args:  cobra.ExactArgs(1),
		Example: `  easyeda bom enrich bom.tsv
  easyeda bom enrich bom.tsv --out enriched.tsv
  easyeda bom enrich bom.tsv --script /path/to/bom-enrich.py`,
		RunE: func(cmd *cobra.Command, args []string) error {
			script, err := findBomEnrichScript(scriptPath)
			if err != nil {
				return err
			}

			cmdArgs := []string{args[0]}
			if outFile != "" {
				cmdArgs = append(cmdArgs, "--out", outFile)
			}

			py := exec.Command("python3", append([]string{script}, cmdArgs...)...)
			py.Stdin = os.Stdin
			py.Stdout = stdout
			py.Stderr = stderr
			if err := py.Run(); err != nil {
				// exec.ExitError is already descriptive; wrap only generic errors.
				var exitErr *exec.ExitError
				if errors.As(err, &exitErr) {
					return errActionFailed // script printed its own error to stderr
				}
				return fmt.Errorf("bom-enrich: %w", err)
			}
			return nil
		},
	}
	c.Flags().StringVar(&outFile, "out", "", "output file path (default: stdout)")
	c.Flags().StringVar(&scriptPath, "script", "", "path to bom-enrich.py (auto-detected if omitted)")
	return c
}

// findBomEnrichScript resolves the bom-enrich.py script path.
// Priority:
//  1. --script flag value (returned as-is)
//  2. Walk up from the running binary to find skills/.../bom-enrich.py
//  3. PATH lookup for bom-enrich.py
func findBomEnrichScript(override string) (string, error) {
	if override != "" {
		return override, nil
	}

	// Walk up from the executable directory.
	exe, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(exe)
		for i := 0; i < 8; i++ {
			candidate := filepath.Join(dir, "skills", "easyeda-schematic", "scripts", "bom-enrich.py")
			if _, err := os.Stat(candidate); err == nil {
				return candidate, nil
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}

	// Fallback: PATH lookup.
	if path, err := exec.LookPath("bom-enrich.py"); err == nil {
		return path, nil
	}

	return "", errors.New("bom-enrich.py not found; use --script to specify the full path")
}
