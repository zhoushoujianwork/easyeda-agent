package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"

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
	var fileType, template, columnsJSON, partsPath, scriptPath string
	var enrich bool

	c := &cobra.Command{
		Use:   "export",
		Short: "Export schematic BOM as csv or xlsx artifact",
		Args:  cobra.NoArgs,
		Long: `Export the schematic BOM as a csv or xlsx artifact.

By default a csv export is enriched in place with LCSC C-numbers (joined from
standard-parts.json by Manufacturer Part) so the "Supplier Part" column is
directly orderable — EasyEDA's own export writes <MPN>.1 there, which is not.
Pass --enrich=false to keep the raw export. Enrichment is csv-only (xlsx is
binary) and best-effort: if it fails the exported file is left un-enriched and
a warning is printed, but the export itself still succeeds.`,
		Example: `  easyeda bom export --type csv
  easyeda bom export --type csv --enrich=false
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

			res, err := dispatchCapture(cfg, "schematic.export.bom", *window, payload, stdout)
			if err != nil {
				return err
			}
			if !enrich {
				return nil
			}

			// Enrichment joins LCSC C-numbers into a TEXT BOM; xlsx is binary.
			if fileType != "csv" {
				fmt.Fprintf(stderr, "note: --enrich skipped — C-number enrichment is csv-only (got %s); use --type csv for an orderable BOM\n", fileType)
				return nil
			}
			bomPath := ""
			for _, a := range res.Artifacts {
				if a.Path != "" {
					bomPath = a.Path
					break
				}
			}
			if bomPath == "" {
				fmt.Fprintln(stderr, "warning: --enrich skipped — export returned no file path")
				return nil
			}
			// Best-effort: a missing python3 / script must NOT fail an
			// already-exported BOM. Warn and keep the raw file.
			if err := enrichBomFile(scriptPath, bomPath, partsPath, stderr); err != nil {
				fmt.Fprintf(stderr, "warning: BOM exported but enrichment failed (file left un-enriched): %v\n", err)
			}
			return nil
		},
	}
	c.Flags().StringVar(&fileType, "type", "", "output file type: csv or xlsx (required)")
	c.Flags().StringVar(&template, "template", "", "BOM template name")
	c.Flags().StringVar(&columnsJSON, "columns", "", `JSON array of column names, e.g. '["designator","value"]'`)
	c.Flags().BoolVar(&enrich, "enrich", true, "enrich csv export in place with LCSC C-numbers (best-effort)")
	c.Flags().StringVar(&partsPath, "parts", "", "path to standard-parts.json (auto-detected if omitted)")
	c.Flags().StringVar(&scriptPath, "script", "", "path to bom-enrich.py (auto-detected if omitted)")
	return c
}

// enrichBomFile rewrites a text BOM in place, joining LCSC C-numbers from
// standard-parts.json via bom-enrich.py. Best-effort: callers treat a returned
// error as non-fatal (the raw export already succeeded). The script's
// human-readable match report goes to stderr so stdout stays the action JSON.
func enrichBomFile(scriptOverride, bomPath, partsPath string, stderr io.Writer) error {
	script, err := findBomEnrichScript(scriptOverride)
	if err != nil {
		return err
	}
	cmdArgs := []string{script, bomPath, "--out", bomPath}
	if partsPath != "" {
		cmdArgs = append(cmdArgs, "--parts", partsPath)
	}
	py := exec.Command("python3", cmdArgs...)
	py.Stdout = stderr // match report -> stderr, never the BOM file or action JSON
	py.Stderr = stderr
	return py.Run()
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

// findBomEnrichScript resolves the bom-enrich.py script path (see
// resolveEnrichScript for the probe order).
func findBomEnrichScript(override string) (string, error) {
	return resolveEnrichScript(override)
}
