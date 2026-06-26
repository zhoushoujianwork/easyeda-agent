package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/zhoushoujianwork/easyeda-agent/internal/daemon"
	"github.com/zhoushoujianwork/easyeda-agent/internal/version"
)

// newDaemonCmd returns the "daemon" subcommand group.
func newDaemonCmd(cfg *appConfig, stdout, stderr io.Writer) *cobra.Command {
	d := &cobra.Command{
		Use:   "daemon",
		Short: "Manage the easyeda-agent background daemon",
	}
	d.AddCommand(
		newDaemonStartCmd(cfg, stdout, stderr),
		newDaemonHealthCmd(cfg, stdout, stderr),
	)
	return d
}

// ── daemon start ──────────────────────────────────────────────────────────

func newDaemonStartCmd(cfg *appConfig, stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the daemon (blocks until SIGINT/SIGTERM)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			portStart, portEnd, err := cfg.portRange()
			if err != nil {
				return err
			}

			killExistingDaemon(stdout)
			cleanup := writeDaemonPID(stdout)
			defer cleanup()

			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			srv := daemon.New(daemon.Options{
				Host:      cfg.host,
				PortStart: portStart,
				PortEnd:   portEnd,
				Version:   version.Version,
			})
			if err := srv.Run(ctx, stdout); err != nil {
				return err
			}
			return nil
		},
	}
}

// ── daemon health ─────────────────────────────────────────────────────────

func newDaemonHealthCmd(cfg *appConfig, stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "health",
		Short: "Check daemon health across the port range",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			portStart, portEnd, err := cfg.portRange()
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			result := scanHealth(ctx, hostPortOptions{
				host:      cfg.host,
				portStart: portStart,
				portEnd:   portEnd,
			})
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(result); err != nil {
				return fmt.Errorf("encode health: %w", err)
			}
			if result.Found == nil {
				return errActionFailed // daemon absent; response already printed
			}
			return nil
		},
	}
}

// ── PID file helpers ──────────────────────────────────────────────────────

// daemonPIDFile returns the path where the daemon records its PID.
func daemonPIDFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".easyeda-agent", "daemon.pid")
}

// killExistingDaemon reads the PID file and terminates any running daemon so
// the new one can bind the preferred port (49620) instead of spilling to the
// next one.
func killExistingDaemon(log io.Writer) {
	pidFile := daemonPIDFile()
	if pidFile == "" {
		return
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		_ = os.Remove(pidFile)
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		_ = os.Remove(pidFile)
		return
	}
	// Signal 0 checks liveness without sending a real signal.
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		_ = os.Remove(pidFile)
		return
	}
	fmt.Fprintf(log, "easyeda-agent: killing existing daemon (pid %d)\n", pid)
	_ = proc.Signal(syscall.SIGTERM)
	for range 20 {
		time.Sleep(100 * time.Millisecond)
		if proc.Signal(syscall.Signal(0)) != nil {
			break
		}
	}
	_ = proc.Signal(syscall.SIGKILL)
	_ = os.Remove(pidFile)
	time.Sleep(200 * time.Millisecond) // let the OS release the port
}

// writeDaemonPID writes our PID to the PID file and returns a cleanup func
// that removes it on exit.
func writeDaemonPID(log io.Writer) func() {
	pidFile := daemonPIDFile()
	if pidFile == "" {
		return func() {}
	}
	if err := os.MkdirAll(filepath.Dir(pidFile), 0755); err != nil {
		fmt.Fprintf(log, "easyeda-agent: create pid dir: %v\n", err)
		return func() {}
	}
	if err := os.WriteFile(pidFile, fmt.Appendf(nil, "%d\n", os.Getpid()), 0644); err != nil {
		fmt.Fprintf(log, "easyeda-agent: write pid file: %v\n", err)
		return func() {}
	}
	return func() { _ = os.Remove(pidFile) }
}
