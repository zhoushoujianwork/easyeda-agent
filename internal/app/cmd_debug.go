package app

import (
	"fmt"
	"io"
	"time"

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
		var timeoutSec int
		c := &cobra.Command{
			Use:   "exec",
			Short: "Run raw eda.* JavaScript in the connector (escape hatch)",
			Args:  cobra.NoArgs,
			Example: `  easyeda debug exec --code "return eda.getProjectInfo()"
  easyeda debug exec --timeout 60 --code "const f = await eda.sch_ManufactureData.getNetlistFile('netlist.json'); return f && await f.text()"`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if code == "" {
					return fmt.Errorf("--code is required")
				}
				timeout := defaultActionTimeout
				if timeoutSec > 0 {
					timeout = time.Duration(timeoutSec) * time.Second
				}
				return dispatchTimed(cfg, "debug.exec_js", window,
					map[string]any{"code": code}, timeout, stdout, stderr)
			},
		}
		c.Flags().StringVar(&code, "code", "", "JavaScript expression to execute in the connector (required)")
		c.Flags().IntVar(&timeoutSec, "timeout", 0, "round-trip timeout in seconds (default 20; raise only for unavoidable slow calls)")
		dbg.AddCommand(c)
	}

	return dbg
}
