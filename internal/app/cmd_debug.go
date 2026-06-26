package app

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// newDebugCmd returns the "debug" subcommand group.
func newDebugCmd(cfg *appConfig, stdout, stderr io.Writer) *cobra.Command {
	var window string

	dbg := &cobra.Command{
		Use:   "debug",
		Short: "Debug escape hatches (confirmation-gated)",
	}
	dbg.PersistentFlags().StringVar(&window, "window", "", "EasyEDA window ID")

	// ── debug exec ────────────────────────────────────────────────────────
	// debug.exec_js
	{
		var code string
		c := &cobra.Command{
			Use:   "exec",
			Short: "Run raw eda.* JavaScript in the connector (escape hatch)",
			Args:  cobra.NoArgs,
			Example: `  easyeda debug exec --code "return eda.getProjectInfo()"
  easyeda debug exec --window win-1 --code "return eda.getState_Rotation('id123')"`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if code == "" {
					return fmt.Errorf("--code is required")
				}
				return dispatch(cfg, "debug.exec_js", window,
					map[string]any{"code": code}, stdout, stderr)
			},
		}
		c.Flags().StringVar(&code, "code", "", "JavaScript expression to execute in the connector (required)")
		dbg.AddCommand(c)
	}

	return dbg
}
