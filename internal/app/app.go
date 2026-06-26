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
	}

	root.PersistentFlags().StringVar(&cfg.host, "host", defaultHost,
		"daemon host")
	root.PersistentFlags().StringVar(&cfg.ports, "ports",
		fmt.Sprintf("%d-%d", defaultPortStart, defaultPortEnd),
		"daemon port range (start-end)")

	root.AddCommand(
		newVersionCmd(stdout),
		newActionsCmd(stdout, stderr),
		newCallCmd(cfg, stdout, stderr),
		newDaemonCmd(cfg, stdout, stderr),
		newAuditCmd(stdout, stderr),
		newProjectCmd(cfg, stdout, stderr),
		newSchCmd(cfg, stdout, stderr),
		newBomCmd(cfg, stdout, stderr),
		newLibCmd(cfg, stdout, stderr),
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
