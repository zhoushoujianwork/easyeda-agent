package app

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type cbbTarget struct{ uuid, library, name string }

func (t *cbbTarget) flags(c *cobra.Command) {
	c.Flags().StringVar(&t.uuid, "uuid", "", "exact owned CBB UUID (required)")
	c.Flags().StringVar(&t.library, "library", "", "exact personal library UUID (required)")
	c.Flags().StringVar(&t.name, "expected-name", "", "exact EA_AGENT__ asset name (required)")
}
func (t *cbbTarget) payload() (map[string]any, error) {
	if strings.TrimSpace(t.uuid) == "" || strings.TrimSpace(t.library) == "" || !strings.HasPrefix(t.name, "EA_AGENT__") {
		return nil, fmt.Errorf("--uuid, --library and exact --expected-name EA_AGENT__... are required")
	}
	return map[string]any{"uuid": t.uuid, "libraryUuid": t.library, "expectedName": t.name}, nil
}
func addCbbAuthoringCommands(group *cobra.Command, cfg *appConfig, stdout, stderr io.Writer, window *string) {
	{
		var name string
		c := &cobra.Command{Use: "create", Short: "Create one agent-owned CBB in the personal writable library", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(name) == "" {
				return fmt.Errorf("--name is required")
			}
			return dispatch(cfg, "library.cbb.create", *window, map[string]any{"name": name}, stdout, stderr)
		}}
		c.Flags().StringVar(&name, "name", "", "asset name; EA_AGENT__ prefix added (required)")
		group.AddCommand(c)
	}
	for _, item := range []struct{ use, action, short string }{
		{"delete", "library.cbb.delete", "Delete only an exact owned CBB, after switching away from its project"},
		{"symbol-open", "library.cbb.symbol_open", "Open and verify one owned native CBB symbol without guessing a variant"},
	} {
		target := new(cbbTarget)
		action := item.action
		var symbol string
		var targetProject, importedSchematic string
		c := &cobra.Command{Use: item.use, Short: item.short, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
			p, err := target.payload()
			if err != nil {
				return err
			}
			if action == "library.cbb.symbol_open" {
				if strings.TrimSpace(symbol) == "" {
					return fmt.Errorf("--symbol is required")
				}
				p["symbolUuid"] = symbol
			}
			if action == "library.cbb.symbol_open" && (targetProject != "" || importedSchematic != "") {
				if strings.TrimSpace(targetProject) == "" || strings.TrimSpace(importedSchematic) == "" {
					return fmt.Errorf("both --target-project and --imported-schematic are required")
				}
				p["targetProjectUuid"] = targetProject
				p["importedSchematicUuid"] = importedSchematic
			}
			return dispatch(cfg, action, *window, p, stdout, stderr)
		}}
		target.flags(c)
		if action == "library.cbb.symbol_open" {
			c.Flags().StringVar(&symbol, "symbol", "", "exact CBB symbol UUID from the active module schematic graph (required)")
			c.Flags().StringVar(&targetProject, "target-project", "", "exact current project for an imported owned CBB symbol")
			c.Flags().StringVar(&importedSchematic, "imported-schematic", "", "exact imported CBB schematic UUID; requires --target-project")
		}
		group.AddCommand(c)
	}
	{
		target := new(cbbTarget)
		var from string
		var allow bool
		c := &cobra.Command{Use: "open", Short: "Switch to an owned CBB project; save ALL documents first", Long: "Official beta project switch can discard unsaved changes. Save ALL open documents before explicitly allowing this operation. The current project must match --from-project; no automatic retry.", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
			p, err := target.payload()
			if err != nil {
				return err
			}
			if !allow || strings.TrimSpace(from) == "" {
				return fmt.Errorf("save ALL open documents, then supply --from-project and --allow-project-switch")
			}
			p["fromProjectUuid"] = from
			p["allowProjectSwitch"] = true
			return dispatch(cfg, "library.cbb.open", *window, p, stdout, stderr)
		}}
		target.flags(c)
		c.Flags().StringVar(&from, "from-project", "", "expected current project UUID (required)")
		c.Flags().BoolVar(&allow, "allow-project-switch", false, "explicitly allow project switch after saving ALL open documents")
		group.AddCommand(c)
	}
	{
		target := new(cbbTarget)
		var specPath, symbol string
		var dryRun bool
		c := &cobra.Command{Use: "symbol-build", Short: "Build exact geometry on a proven-empty owned CBB symbol (or inspect with --dry-run)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
			p, err := target.payload()
			if err != nil {
				return err
			}
			if strings.TrimSpace(symbol) == "" || specPath == "" {
				return fmt.Errorf("--symbol and --spec are required")
			}
			data, err := os.ReadFile(specPath)
			if err != nil {
				return err
			}
			if len(data) > 1<<20 {
				return fmt.Errorf("symbol spec exceeds 1 MiB")
			}
			var spec map[string]any
			if err = json.Unmarshal(data, &spec); err != nil {
				return err
			}
			for key := range spec {
				if key != "pins" && key != "outline" {
					return fmt.Errorf("unknown CBB symbol spec field %s", key)
				}
			}
			p["symbolUuid"] = symbol
			p["pins"] = spec["pins"]
			p["outline"] = spec["outline"]
			p["dryRun"] = dryRun
			return dispatch(cfg, "library.cbb.symbol_build", *window, p, stdout, stderr)
		}}
		target.flags(c)
		c.Flags().StringVar(&symbol, "symbol", "", "exact CBB symbol UUID (required)")
		c.Flags().StringVar(&specPath, "spec", "", "JSON with pins and closed outline (required)")
		c.Flags().BoolVar(&dryRun, "dry-run", false, "read existing symbol geometry without writing")
		group.AddCommand(c)
	}

	{
		target := new(cbbTarget)
		var symbol, specPath string
		var dryRun bool
		c := &cobra.Command{Use: "symbol-rectangle-delete", Short: "Remove one exact inventoried rectangle from an owned source CBB symbol", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
			p, err := target.payload()
			if err != nil {
				return err
			}
			if strings.TrimSpace(symbol) == "" || specPath == "" {
				return fmt.Errorf("--symbol and --spec are required")
			}
			data, err := os.ReadFile(specPath)
			if err != nil {
				return err
			}
			if len(data) > 1<<20 {
				return fmt.Errorf("rectangle spec exceeds 1 MiB")
			}
			var rectangle map[string]any
			if err = json.Unmarshal(data, &rectangle); err != nil {
				return err
			}
			if rectangle == nil {
				return fmt.Errorf("rectangle spec must be an object")
			}
			p["symbolUuid"] = symbol
			p["rectangle"] = rectangle
			p["dryRun"] = dryRun
			return dispatch(cfg, "library.cbb.symbol_rectangle_delete", *window, p, stdout, stderr)
		}}
		target.flags(c)
		c.Flags().StringVar(&symbol, "symbol", "", "exact owned source CBB symbol UUID (required)")
		c.Flags().StringVar(&specPath, "spec", "", "JSON rectangle inventory with id/x/y/width/height/rotation (required)")
		c.Flags().BoolVar(&dryRun, "dry-run", false, "verify target and preservation inventory without deletion")
		group.AddCommand(c)
	}
}
func newSchCbbPlaceCmd(cfg *appConfig, stdout, stderr io.Writer, window *string) *cobra.Command {
	target := new(cbbTarget)
	var symbol, project, page string
	var x, y float64
	var rotation int
	var mirror bool
	var allowProbe bool
	c := &cobra.Command{Use: "cbb-place", Short: "Place one exact owned native CBB symbol and verify fresh identity/geometry", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		p, err := target.payload()
		if err != nil {
			return err
		}
		if strings.TrimSpace(symbol) == "" || strings.TrimSpace(project) == "" || strings.TrimSpace(page) == "" || !cmd.Flags().Changed("x") || !cmd.Flags().Changed("y") || math.IsNaN(x) || math.IsNaN(y) || math.IsInf(x, 0) || math.IsInf(y, 0) || (rotation != 0 && rotation != 90 && rotation != 180 && rotation != 270) {
			return fmt.Errorf("explicit --symbol, --project-uuid, --page, finite --x/--y and quarter-turn --rotation are required")
		}
		p["symbolUuid"] = symbol
		p["projectUuid"] = project
		p["documentUuid"] = page
		p["x"] = x
		p["y"] = y
		p["rotation"] = rotation
		p["mirror"] = mirror
		p["allowSourceProjectProbe"] = allowProbe
		return dispatchTimed(cfg, "schematic.cbb.place", *window, p, 60*time.Second, stdout, stderr)
	}}
	target.flags(c)
	c.Flags().StringVar(&symbol, "symbol", "", "exact verified CBB symbol UUID (required)")
	c.Flags().StringVar(&project, "project-uuid", "", "exact target project UUID (required)")
	c.Flags().StringVar(&page, "page", "", "exact target schematic page UUID (required)")
	c.Flags().Float64Var(&x, "x", 0, "raw X coordinate (required)")
	c.Flags().Float64Var(&y, "y", 0, "raw Y coordinate (required)")
	c.Flags().IntVar(&rotation, "rotation", 0, "quarter-turn rotation in degrees")
	c.Flags().BoolVar(&mirror, "mirror", false, "mirror the CBB symbol")
	c.Flags().BoolVar(&allowProbe, "allow-source-project-probe", false, "after saving ALL open documents, allow a read-only source CBB project probe and verified return before single placement")
	return c
}

func newSchCbbProjectPlaceCmd(cfg *appConfig, stdout, stderr io.Writer, window *string) *cobra.Command {
	var project, page, schematic, name, symbol string
	var x, y float64
	var dryRun bool
	var viaSymbol bool
	c := &cobra.Command{Use: "cbb-project-place", Short: "Inspect or place one exact already-imported project CBB without importing again", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(project) == "" || strings.TrimSpace(page) == "" || strings.TrimSpace(schematic) == "" || !strings.HasPrefix(name, "EA_AGENT__") || (!dryRun && strings.TrimSpace(symbol) == "") || !cmd.Flags().Changed("x") || !cmd.Flags().Changed("y") || math.IsNaN(x) || math.IsNaN(y) || math.IsInf(x, 0) || math.IsInf(y, 0) {
			return fmt.Errorf("explicit project/page/schematic, EA_AGENT__ expected name, finite x/y, and symbol (unless dry-run) required")
		}
		p := map[string]any{"projectUuid": project, "documentUuid": page, "schematicUuid": schematic, "expectedName": name, "x": x, "y": y, "dryRun": dryRun}
		p["viaSymbolApi"] = viaSymbol
		if symbol != "" {
			p["symbolUuid"] = symbol
		}
		return dispatchTimed(cfg, "schematic.cbb.project_place", *window, p, 35*time.Second, stdout, stderr)
	}}
	c.Flags().StringVar(&project, "project-uuid", "", "exact active project UUID (required)")
	c.Flags().StringVar(&page, "page", "", "exact target page UUID (required)")
	c.Flags().StringVar(&schematic, "schematic", "", "exact already-imported CBB schematic UUID (required)")
	c.Flags().StringVar(&name, "expected-name", "", "exact owned EA_AGENT__ module name (required)")
	c.Flags().StringVar(&symbol, "symbol", "", "exact project-library symbol UUID returned by dry-run")
	c.Flags().Float64Var(&x, "x", 0, "raw X coordinate (required)")
	c.Flags().Float64Var(&y, "y", 0, "raw Y coordinate (required)")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "inspect exact local variant without placement")
	c.Flags().BoolVar(&viaSymbol, "via-symbol-api", true, "explicitly use official generic symbol placement for the verified native type17 asset; fresh block_symbol readback still required")
	return c
}
