package app

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// newDebugCmd returns the "debug" subcommand group.
func newDebugCmd(cfg *appConfig, stdout, stderr io.Writer) *cobra.Command {
	var window string

	dbg := &cobra.Command{
		Use:   "debug",
		Short: "Read connector diagnostics or run confirmation-gated debug scripts",
	}
	dbg.PersistentFlags().StringVar(&window, "window", "", "EasyEDA window ID")
	{
		var limit int
		c := &cobra.Command{
			Use: "connector-logs", Short: "Read bounded connector transport diagnostics without scripts or log clearing",
			Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				if limit < 1 || limit > 100 {
					return fmt.Errorf("--limit must be from 1 to 100")
				}
				res, err := requestAction(cfg, "project.current", window, map[string]any{"includeConnectorLogs": limit})
				if err != nil {
					return err
				}
				if err := validateConnectorLogs(res.Result, limit); err != nil {
					return err
				}
				return writeJSON(stdout, res)
			},
		}
		c.Flags().IntVar(&limit, "limit", 30, "Most recent connector log entries (1-100); other extension/project logs excluded")
		dbg.AddCommand(c)
	}

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

func validateConnectorLogs(result map[string]any, limit int) error {
	rows, ok := result["connectorLogs"].([]any)
	if !ok || len(rows) > limit {
		return fmt.Errorf("connector diagnostics unavailable or exceed requested limit; update the connector before diagnosing")
	}
	for _, value := range rows {
		row, ok := value.(map[string]any)
		if !ok || !schAttributeFinite(row["timestamp"]) || strField(row, "type") == "" || !strings.HasPrefix(strField(row, "message"), "[easyeda-agent] ") {
			return fmt.Errorf("connector diagnostic entry is incomplete or unscoped")
		}
	}
	return nil
}
