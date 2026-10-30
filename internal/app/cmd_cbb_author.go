package app

import (
	"fmt"
	"io"
	"math"
	"strings"

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
		c := &cobra.Command{Use: item.use, Short: item.short, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
			p, err := target.payload()
			if err != nil {
				return err
			}
			return dispatch(cfg, action, *window, p, stdout, stderr)
		}}
		target.flags(c)
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
}
func newSchCbbPlaceCmd(cfg *appConfig, stdout, stderr io.Writer, window *string) *cobra.Command {
	target := new(cbbTarget)
	var symbol, project, page string
	var x, y float64
	var rotation int
	var mirror bool
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
		return dispatch(cfg, "schematic.cbb.place", *window, p, stdout, stderr)
	}}
	target.flags(c)
	c.Flags().StringVar(&symbol, "symbol", "", "exact verified CBB symbol UUID (required)")
	c.Flags().StringVar(&project, "project-uuid", "", "exact target project UUID (required)")
	c.Flags().StringVar(&page, "page", "", "exact target schematic page UUID (required)")
	c.Flags().Float64Var(&x, "x", 0, "raw X coordinate (required)")
	c.Flags().Float64Var(&y, "y", 0, "raw Y coordinate (required)")
	c.Flags().IntVar(&rotation, "rotation", 0, "quarter-turn rotation in degrees")
	c.Flags().BoolVar(&mirror, "mirror", false, "mirror the CBB symbol")
	return c
}
