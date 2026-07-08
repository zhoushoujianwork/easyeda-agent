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
	"github.com/zhoushoujianwork/easyeda-agent/internal/selfupdate"
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
	var autosaveDebounce time.Duration
	var autoUpdateSkill bool
	c := &cobra.Command{
		Use:   "start",
		Short: "Start the daemon (blocks until SIGINT/SIGTERM)",
		Long: `Start the daemon (blocks until SIGINT/SIGTERM).

Daemon-level autosave (--autosave-debounce) is a safety net for in-memory edits:
place/wire/modify only change the EasyEDA document in memory, so a window reload,
daemon restart, or crash loses unsaved work. With autosave on, the daemon saves a
window once its edits quiesce for the debounce window (a burst coalesces into one
save). Set to 0 to disable.

Skill auto-update (--auto-update-skill, on by default) keeps your installed
easyeda-agent skill dirs (~/.claude, ~/.codex) in sync with the latest release on
startup, so you never hand-copy the skill after a CLI upgrade. It touches only
dirs that already exist, honors EASYEDA_SKILL_PRESERVE=1, and logs each change.
The EasyEDA connector .eext has no sideload auto-update (marketplace-only), so a
stale connector is only DETECTED and logged with a re-import notice — not swapped.`,
		Args: cobra.NoArgs,
		Example: `  easyeda daemon start
  easyeda daemon start --autosave-debounce 5s
  easyeda daemon start --autosave-debounce 0   # disable autosave
  easyeda daemon start --auto-update-skill=false # don't sync skill on startup`,
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

			// Best-effort background skill sync — never blocks or fails the daemon.
			if autoUpdateSkill {
				go runStartupSkillSync(ctx, stdout)
			}

			srv := daemon.New(daemon.Options{
				Host:             cfg.host,
				PortStart:        portStart,
				PortEnd:          portEnd,
				Version:          version.Version,
				AutosaveDebounce: autosaveDebounce,
			})
			if err := srv.Run(ctx, stdout); err != nil {
				return err
			}
			return nil
		},
	}
	c.Flags().DurationVar(&autosaveDebounce, "autosave-debounce", 3*time.Second,
		"autosave a window this long after its last mutating action (0 = disable)")
	c.Flags().BoolVar(&autoUpdateSkill, "auto-update-skill", true,
		"on startup, sync installed skill dirs to the latest release (best-effort)")
	return c
}

// runStartupSkillSync performs the daemon's best-effort skill refresh in the
// background. It bounds itself with a timeout and logs via the daemon's stdout
// so the user sees exactly what changed (or why it skipped this cycle).
func runStartupSkillSync(parent context.Context, log io.Writer) {
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()
	selfupdate.StartupSync(ctx, version.Version, func(format string, a ...any) {
		fmt.Fprintf(log, "%s daemon: %s\n", daemon.Service, fmt.Sprintf(format, a...))
	})
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
