package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
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
stale connector is only DETECTED and logged with a re-import notice — not swapped.

The daemon binds a SINGLE fixed port (60832, the start of --ports) and never
spills to the next one — so at most one daemon ever runs and the connector always
finds it there, instead of several daemons quietly binding 49621/49622… and the
connector churning between them. If 60832 is already held by another easyeda
daemon it is replaced automatically; if held by a FOREIGN process the daemon asks
(interactive terminal) or refuses with a clear message (headless) rather than
starting a second daemon elsewhere.`,
		Args: cobra.NoArgs,
		Example: `  easyeda daemon start
  easyeda daemon start --autosave-debounce 5s
  easyeda daemon start --autosave-debounce 0   # disable autosave
  easyeda daemon start --auto-update-skill=false # don't sync skill on startup`,
		RunE: func(cmd *cobra.Command, args []string) error {
			portStart, _, err := cfg.portRange()
			if err != nil {
				return err
			}
			// The daemon uses a SINGLE fixed port (the start of the range, 60832) —
			// it never spills to the next port. That guarantees at most one daemon
			// runs and the connector always finds it on 60832, instead of multiple
			// daemons quietly binding 49621/49622… and the connector churning between
			// them. If 60832 is already held, ensurePortAvailable replaces our own
			// stale daemon automatically, or (for a foreign process) asks / refuses.
			port := portStart
			if err := ensurePortAvailable(cfg.host, port, stdout); err != nil {
				return err
			}
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
				PortStart:        port,
				PortEnd:          port, // single fixed port — no spill
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

// newHealthAliasCmd exposes `easyeda health` at the root as an alias of
// `easyeda daemon health` — the skill docs mandate it as the preflight check
// before any window operation, and the bare form is what they spell (#130).
func newHealthAliasCmd(cfg *appConfig, stdout, stderr io.Writer) *cobra.Command {
	h := newDaemonHealthCmd(cfg, stdout, stderr)
	h.Short += " (alias of `daemon health`)"
	return h
}

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

// ensurePortAvailable makes the daemon's FIXED port bindable before startup.
// Behavior (chosen with the user): the daemon uses ONE port and never spills.
//   - port free                         → proceed.
//   - held by ANOTHER easyeda daemon    → replace it automatically (safe: it's our
//     own stale/duplicate service; this is the reliable, port-based successor to
//     the old PID-file kill).
//   - held by a FOREIGN process         → never kill it silently. On an interactive
//     terminal, ask; headless (air / nohup — no TTY), refuse with a clear message
//     so a second daemon can't quietly spawn on another port.
func ensurePortAvailable(host string, port int, log io.Writer) error {
	if portFree(host, port) {
		return nil
	}
	pid := listenerPID(port)
	if daemonOnPort(host, port) {
		fmt.Fprintf(log, "%s daemon: port %d already held by an easyeda daemon (pid %d) — replacing it\n", daemon.Service, port, pid)
		termPID(pid)
		if waitPortFree(host, port, 3*time.Second) {
			return nil
		}
		return fmt.Errorf("port %d still busy after replacing daemon pid %d", port, pid)
	}
	cmdName := pidCommand(pid)
	if isInteractive() {
		fmt.Fprintf(log, "⚠ port %d is held by pid %d (%s) — NOT an easyeda daemon.\n  Kill it and take over the port? [y/N]: ", port, pid, cmdName)
		if !readYes() {
			return fmt.Errorf("port %d busy (pid %d %s) — declined; free it (kill %d) or run with --ports", port, pid, cmdName, pid)
		}
		termPID(pid)
		if waitPortFree(host, port, 3*time.Second) {
			return nil
		}
		return fmt.Errorf("port %d still busy after killing pid %d", port, pid)
	}
	return fmt.Errorf("port %d is held by pid %d (%s), not an easyeda daemon — free it (kill %d) or run with --ports; refusing to spawn a second daemon", port, pid, cmdName, pid)
}

// portFree reports whether the daemon can bind host:port right now.
func portFree(host string, port int) bool {
	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// daemonOnPort reports whether an easyeda daemon answers /health on host:port.
func daemonOnPort(host string, port int) bool {
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get(fmt.Sprintf("http://%s/health", net.JoinHostPort(host, strconv.Itoa(port))))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var body struct {
		Service string `json:"service"`
	}
	if json.NewDecoder(resp.Body).Decode(&body) != nil {
		return false
	}
	return body.Service == daemon.Service
}

// listenerPID returns the pid LISTENing on the TCP port (0 if unknown). lsof is
// present on macOS/Linux; a missing lsof just yields 0 (message still useful).
func listenerPID(port int) int {
	out, err := exec.Command("lsof", "-nP", fmt.Sprintf("-iTCP:%d", port), "-sTCP:LISTEN", "-t").Output()
	if err != nil {
		return 0
	}
	for _, f := range strings.Fields(string(out)) {
		if pid, e := strconv.Atoi(f); e == nil {
			return pid
		}
	}
	return 0
}

// pidCommand returns a short command name for a pid (best-effort).
func pidCommand(pid int) string {
	if pid <= 0 {
		return "unknown"
	}
	out, err := exec.Command("ps", "-o", "comm=", "-p", strconv.Itoa(pid)).Output()
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

// termPID sends SIGTERM then (after a grace period) SIGKILL.
func termPID(pid int) {
	if pid <= 0 {
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	_ = proc.Signal(syscall.SIGTERM)
	for range 20 {
		time.Sleep(100 * time.Millisecond)
		if proc.Signal(syscall.Signal(0)) != nil {
			return // gone
		}
	}
	_ = proc.Signal(syscall.SIGKILL)
}

// waitPortFree polls until the port is bindable or the timeout elapses.
func waitPortFree(host string, port int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if portFree(host, port) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return portFree(host, port)
}

// isInteractive reports whether stdin is a terminal (so we can prompt). Under
// air / nohup / a pipe it is not, so we fall back to fail-with-message.
func isInteractive() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && (fi.Mode()&os.ModeCharDevice) != 0
}

// readYes reads one line from stdin and reports whether it is an affirmative.
func readYes() bool {
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
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
