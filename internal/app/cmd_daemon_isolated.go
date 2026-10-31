package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/zhoushoujianwork/easyeda-agent/internal/daemon"
	"github.com/zhoushoujianwork/easyeda-agent/internal/version"
)

// Explicit isolated acceptance service: never probes/replaces another daemon,
// never uses the shared PID, audit or artifact paths. Port conflicts just fail.
func newDaemonServeIsolatedCmd(stdout io.Writer) *cobra.Command {
	var port int
	var state string
	c := &cobra.Command{Use: "serve-isolated", Short: "Serve on an explicit test port with fresh isolated state; never replace another daemon", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if port < 1024 || port > 65535 || !filepath.IsAbs(state) {
				return fmt.Errorf("explicit --port (1024-65535) and absolute fresh --state-dir are required")
			}
			if err := os.Mkdir(state, 0700); err != nil {
				return fmt.Errorf("isolated state must be a new directory: %w", err)
			}
			pid := filepath.Join(state, "daemon.pid")
			if err := os.WriteFile(pid, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0600); err != nil {
				return err
			}
			defer os.Remove(pid)
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return daemon.New(daemon.Options{Host: "127.0.0.1", PortStart: port, PortEnd: port, Version: version.Version,
				AuditDir: filepath.Join(state, "audit"), ArtifactDir: filepath.Join(state, "artifacts"), AutosaveDebounce: 0}).Run(ctx, stdout)
		}}
	c.Flags().IntVar(&port, "port", 0, "explicit unused test port; an occupied port is refused without stopping its owner")
	c.Flags().StringVar(&state, "state-dir", "", "new absolute directory for this service's PID/audit/artifacts; parent must exist")
	return c
}
