package app

import (
	"io"

	"github.com/spf13/cobra"
)

// newViewCmd returns the "view" subcommand group: editor canvas view shortcuts
// (适应全部 / 适应选中 / zoom-to / region) that map to eda.dmt_EditorControl.*.
// These are document-agnostic — they act on the focused canvas of whichever
// document (schematic or PCB) is active, so --window/--project picks the window.
func newViewCmd(cfg *appConfig, stdout, stderr io.Writer) *cobra.Command {
	var window string

	view := &cobra.Command{
		Use:   "view",
		Short: "Editor canvas view shortcuts (zoom / fit) for schematic & PCB",
	}
	view.PersistentFlags().StringVar(&window, "window", "", "EasyEDA window ID")

	// ── fit ───────────────────────────────────────────────────────────────
	// view.fit — 适应全部 (the `K` shortcut)
	view.AddCommand(&cobra.Command{
		Use:     "fit",
		Short:   "Zoom to fit all primitives (适应全部, the `K` shortcut)",
		Args:    cobra.NoArgs,
		Example: `  easyeda view fit`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return dispatch(cfg, "view.fit", window, nil, stdout, stderr)
		},
	})

	// ── fit-selection ──────────────────────────────────────────────────────
	// view.fit_selection — 适应选中
	view.AddCommand(&cobra.Command{
		Use:     "fit-selection",
		Short:   "Zoom to fit the currently selected primitives (适应选中)",
		Args:    cobra.NoArgs,
		Example: `  easyeda sch select --ids '["id1"]' && easyeda view fit-selection`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return dispatch(cfg, "view.fit_selection", window, nil, stdout, stderr)
		},
	})

	// ── zoom ───────────────────────────────────────────────────────────────
	// view.zoom — pan/zoom to a center coordinate and/or scale ratio (percent)
	{
		var x, y, scale float64
		c := &cobra.Command{
			Use:   "zoom",
			Short: "Pan/zoom to a center coordinate and/or scale ratio (percent)",
			Args:  cobra.NoArgs,
			Example: `  easyeda view zoom --scale 200
  easyeda view zoom --x 100 --y 200 --scale 150`,
			RunE: func(cmd *cobra.Command, args []string) error {
				payload := map[string]any{}
				if cmd.Flags().Changed("x") {
					payload["x"] = x
				}
				if cmd.Flags().Changed("y") {
					payload["y"] = y
				}
				if cmd.Flags().Changed("scale") {
					payload["scale"] = scale
				}
				return dispatch(cfg, "view.zoom", window, payload, stdout, stderr)
			},
		}
		c.Flags().Float64Var(&x, "x", 0, "center X coordinate (keeps current if unset)")
		c.Flags().Float64Var(&y, "y", 0, "center Y coordinate (keeps current if unset)")
		c.Flags().Float64Var(&scale, "scale", 0, "zoom percent, e.g. 200 = 200% (keeps current if unset)")
		view.AddCommand(c)
	}

	// ── region ─────────────────────────────────────────────────────────────
	// view.region — zoom to a rectangular region (canvas units)
	{
		var left, right, top, bottom float64
		c := &cobra.Command{
			Use:   "region",
			Short: "Zoom to a rectangular region (canvas units: sch 0.01inch, PCB mil)",
			Long: `Zoom to a rectangular region of the canvas.

--left/--right are the two X bounds, --top/--bottom the two Y bounds. Order does
NOT matter: the connector sorts each axis to a min/max box, so a reversed pair
still frames the same rectangle. A zero-area box (a bound repeated, e.g.
--left == --right) is rejected.

Units are canvas units: schematic 0.01inch, PCB mil. NOTE the schematic canvas
is y-DOWN — a LARGER stored y renders LOWER on screen (verified on Pro 3.2.121,
issue #19). The flag names are just "two Y bounds"; you do not need to guess
which is visually higher.

For a partial / zoomed-in screenshot, frame the area here first, then capture
with "easyeda sch snapshot --no-fit" so the snapshot keeps this viewport. The
snapshot waits for the canvas to repaint before grabbing the frame (issue #20).`,
			Args:    cobra.NoArgs,
			Example: `  easyeda view region --left 0 --right 1000 --top 1000 --bottom 0`,
			RunE: func(cmd *cobra.Command, args []string) error {
				payload := map[string]any{
					"left":   left,
					"right":  right,
					"top":    top,
					"bottom": bottom,
				}
				return dispatch(cfg, "view.region", window, payload, stdout, stderr)
			},
		}
		c.Flags().Float64Var(&left, "left", 0, "first X bound")
		c.Flags().Float64Var(&right, "right", 0, "second X bound")
		c.Flags().Float64Var(&top, "top", 0, "first Y bound (canvas is y-DOWN: larger y renders lower)")
		c.Flags().Float64Var(&bottom, "bottom", 0, "second Y bound")
		view.AddCommand(c)
	}

	return view
}
