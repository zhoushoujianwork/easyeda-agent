package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"
	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
	"github.com/zhoushoujianwork/easyeda-agent/internal/version"
)

// Run is the main entry point called by main.go.
// It returns 0 on success, 1 on any error.
func Run(args []string, stdout, stderr io.Writer) int {
	root := newRootCmd(stdout, stderr)
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	if err := root.ExecuteContext(context.Background()); err != nil {
		// errActionFailed means the response was already printed to stdout;
		// no further message needed. All other errors get printed here.
		if !errors.Is(err, errActionFailed) {
			fmt.Fprintln(stderr, err)
		}
		return 1
	}
	return 0
}

func newRootCmd(stdout, stderr io.Writer) *cobra.Command {
	cfg := &appConfig{
		host:  defaultHost,
		ports: fmt.Sprintf("%d-%d", defaultPortStart, defaultPortEnd),
	}

	root := &cobra.Command{
		Use:   "easyeda",
		Short: version.Name + " — AI-native EasyEDA Pro automation layer",
		// SilenceUsage: don't dump usage on every error.
		// SilenceErrors: we handle printing ourselves so we can suppress
		// errActionFailed without also suppressing "unknown command" etc.
		SilenceUsage:  true,
		SilenceErrors: true,
		// Setting Version enables `--version`; pre-registering the flag below
		// adds the `-v` shorthand. Same output as the `version` subcommand.
		Version: version.Version,
	}
	root.SetVersionTemplate(version.Name + " {{.Version}}\n")
	root.Flags().BoolP("version", "v", false, "print version and exit")

	root.PersistentFlags().StringVar(&cfg.host, "host", defaultHost,
		"daemon host")
	root.PersistentFlags().StringVar(&cfg.ports, "ports",
		fmt.Sprintf("%d-%d", defaultPortStart, defaultPortEnd),
		"daemon port range (start-end)")
	root.PersistentFlags().StringVar(&cfg.project, "project", "",
		"route by project name/uuid instead of --window (survives windowId churn)")

	root.AddCommand(
		newVersionCmd(stdout),
		newActionsCmd(stdout, stderr),
		newNotifyCmd(cfg, stdout, stderr),
		newCallCmd(cfg, stdout, stderr),
		newApplyCmd(cfg, stdout, stderr),
		newDaemonCmd(cfg, stdout, stderr),
		newAuditCmd(stdout, stderr),
		newProjectCmd(cfg, stdout, stderr),
		newDocCmd(cfg, stdout, stderr),
		newSchCmd(cfg, stdout, stderr),
		newPcbCmd(cfg, stdout, stderr),
		newBoardCmd(cfg, stdout, stderr),
		newViewCmd(cfg, stdout, stderr),
		newBomCmd(cfg, stdout, stderr),
		newLibCmd(cfg, stdout, stderr),
		newApiCmd(stdout, stderr),
		newDebugCmd(cfg, stdout, stderr),
	)

	return root
}

// ── version ───────────────────────────────────────────────────────────────

func newVersionCmd(stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(stdout, "%s %s\n", version.Name, version.Version)
			return nil
		},
	}
}

// ── notify ────────────────────────────────────────────────────────────────

func newNotifyCmd(cfg *appConfig, stdout, stderr io.Writer) *cobra.Command {
	var window, message, typ string
	var duration float64
	c := &cobra.Command{
		Use:   "notify",
		Short: "Show a toast inside the EasyEDA window (design-flow step notification)",
		Long: `Surface a non-blocking toast INSIDE the EasyEDA window. The design flow calls this
as each stage passes so the user can watch progress live — "完成 X,下一步 Y".
type ∈ info | success | warn | error | question.`,
		Args: cobra.NoArgs,
		Example: `  easyeda notify --message "完成 布局,下一步 布线" --type success
  easyeda notify --message "DRC 未通过,需修复" --type error --duration 5`,
		RunE: func(cmd *cobra.Command, args []string) error {
			payload := map[string]any{"message": message}
			if typ != "" {
				payload["type"] = typ
			}
			if cmd.Flags().Changed("duration") {
				payload["duration"] = duration
			}
			return dispatch(cfg, "system.notify", window, payload, stdout, stderr)
		},
	}
	c.Flags().StringVar(&message, "message", "", "toast text (required)")
	c.Flags().StringVar(&typ, "type", "info", "info | success | warn | error | question")
	c.Flags().Float64Var(&duration, "duration", 3, "seconds to show")
	c.Flags().StringVar(&window, "window", "", "EasyEDA window ID (else use --project)")
	_ = c.MarkFlagRequired("message")
	return c
}

// ── actions ───────────────────────────────────────────────────────────────

func newActionsCmd(stdout, _ io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "actions",
		Short: "Print the typed action catalog as JSON",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(protocol.AllActions())
		},
	}
}

// ── call (generic escape hatch) ───────────────────────────────────────────

func newCallCmd(cfg *appConfig, stdout, stderr io.Writer) *cobra.Command {
	var window, payload string

	cmd := &cobra.Command{
		Use:   "call <action>",
		Short: "Generic escape hatch: call any typed action directly",
		Args:  cobra.ExactArgs(1),
		Example: `  easyeda call system.health
  easyeda call schematic.components.list --window win-1
  easyeda call schematic.component.place --payload '{"libraryUuid":"...","uuid":"...","x":100,"y":200}'`,
		RunE: func(cmd *cobra.Command, args []string) error {
			action := args[0]

			var payloadMap map[string]any
			if payload != "" {
				if err := json.Unmarshal([]byte(payload), &payloadMap); err != nil {
					return fmt.Errorf("invalid --payload json: %w", err)
				}
			}

			return dispatch(cfg, action, window, payloadMap, stdout, stderr)
		},
	}
	cmd.Flags().StringVar(&window, "window", "", "EasyEDA window ID")
	cmd.Flags().StringVar(&payload, "payload", "", "action payload as a JSON object")
	return cmd
}
