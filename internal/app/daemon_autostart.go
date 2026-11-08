package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"

	"github.com/zhoushoujianwork/easyeda-agent/internal/selfupdate"
	"github.com/zhoushoujianwork/easyeda-agent/internal/version"
)

// Background daemon start. Before this, first use meant "open a terminal and
// keep `easyeda daemon start` running in the foreground" — the hardest setup
// step for people and impossible for an agent whose shell blocks on it.
//
// Auto-start is skipped for dev builds (make dev runs the daemon under air;
// a CLI call during an air rebuild must not race it) and with
// EASYEDA_NO_AUTOSTART=1. Two CLI processes starting at once would otherwise
// let the second daemon "replace" the first, so a lock file serializes the
// spawn and the holder re-scans before spawning.

const daemonStartWait = 15 * time.Second

func daemonLogPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".easyeda-agent", "daemon.log")
}

var releaseVersionPattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// autoStartAllowed reports whether a missing daemon may be started implicitly:
// installed builds only — a tagged release or a local -dev.N bundle. Source
// builds (git describe "v1.11.0-2-gffac5fe-dirty", "dev") are what `make dev`
// runs under air; a CLI call during an air rebuild must not race it.
func autoStartAllowed() bool {
	if os.Getenv("EASYEDA_NO_AUTOSTART") != "" {
		return false
	}
	return releaseVersionPattern.MatchString(version.Version) || selfupdate.IsLocalVersion(version.Version)
}

// startDaemonBackground spawns `easyeda daemon start` detached (log appended to
// ~/.easyeda-agent/daemon.log) and waits until /health answers. It returns the
// port the daemon answered on.
func startDaemonBackground(cfg *appConfig, stderr io.Writer, extraArgs ...string) (int, error) {
	return startDaemonBackgroundFrom(cfg, stderr, "", extraArgs...)
}

// startDaemonBackgroundFrom is startDaemonBackground with an explicit binary;
// "" means this process's own executable. `update` passes the freshly swapped
// path: after an atomic replace, os.Executable on Linux names the deleted
// old inode.
func startDaemonBackgroundFrom(cfg *appConfig, stderr io.Writer, exe string, extraArgs ...string) (int, error) {
	portStart, portEnd, err := cfg.portRange()
	if err != nil {
		return 0, err
	}
	scan := func() int {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if r := scanHealth(ctx, hostPortOptions{host: cfg.host, portStart: portStart, portEnd: portEnd}); r.Found != nil {
			return r.Found.Port
		}
		return 0
	}
	waitUp := func() int {
		deadline := time.Now().Add(daemonStartWait)
		for time.Now().Before(deadline) {
			if p := scan(); p != 0 {
				return p
			}
			time.Sleep(300 * time.Millisecond)
		}
		return 0
	}
	logPath := daemonLogPath()
	if logPath == "" {
		return 0, errors.New("cannot locate home directory for the daemon log")
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return 0, err
	}
	lock := logPath + ".start.lock"
	f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		// Another CLI is starting it right now (or a crashed start left the lock).
		if st, serr := os.Stat(lock); serr == nil && time.Since(st.ModTime()) > 2*daemonStartWait {
			_ = os.Remove(lock)
			return startDaemonBackgroundFrom(cfg, stderr, exe, extraArgs...)
		}
		if p := waitUp(); p != 0 {
			return p, nil
		}
		return 0, fmt.Errorf("another easyeda process is starting the daemon but it did not come up; see %s", logPath)
	}
	f.Close()
	defer os.Remove(lock)
	if p := scan(); p != 0 { // started by someone else meanwhile
		return p, nil
	}
	if exe == "" {
		if exe, err = os.Executable(); err != nil {
			return 0, err
		}
	}
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, err
	}
	defer logf.Close()
	// Same host/ports as the caller: a spawned daemon on the default port would
	// "replace" whatever easyeda daemon already holds it.
	args := append([]string{"daemon", "start", "--host", cfg.host, "--ports", cfg.ports}, extraArgs...)
	cmd := exec.Command(exe, args...)
	fmt.Fprintf(logf, "\n=== %s background start: %s %v ===\n", time.Now().Format(time.RFC3339), exe, args)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = logf, logf, nil
	cmd.SysProcAttr = detachAttr()
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("start daemon: %w", err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	if p := waitUp(); p != 0 {
		fmt.Fprintf(stderr, "↻ easyeda daemon 已在后台启动（pid %d，端口 %d，日志 %s）\n", pid, p, logPath)
		return p, nil
	}
	return 0, fmt.Errorf("daemon (pid %d) did not answer /health within %s; see %s", pid, daemonStartWait, logPath)
}

// installedBuild reports whether v is what the installer produces (a tagged
// release or a local -dev.N bundle) — never a source build that make dev runs
// under air, which this package must not stop or replace.
func installedBuild(v string) bool {
	return releaseVersionPattern.MatchString(v) || selfupdate.IsLocalVersion(v)
}

// staleInstalledDaemon reports whether an installed daemon runs an older release
// than this installed CLI — the state left behind when the binary was upgraded
// (by `update`, install.sh or a package manager) while the daemon kept running.
func staleInstalledDaemon(daemonVersion, cliVersion string) bool {
	return installedBuild(daemonVersion) && installedBuild(cliVersion) && selfupdate.SemverLess(daemonVersion, cliVersion)
}

// automaticDaemonReplacementAllowed is deliberately stricter than missing-daemon
// startup. Presence of lifecycle, even null or incomplete, is not legacy proof.
func automaticDaemonReplacementAllowed(raw []byte, cliVersion string) bool {
	if !releaseVersionPattern.MatchString(cliVersion) {
		return false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return false
	}
	if _, present := fields["lifecycle"]; present {
		return false
	}
	var h struct {
		Service string `json:"service"`
		Version string `json:"version"`
		PID     int    `json:"pid"`
	}
	if json.Unmarshal(raw, &h) != nil || h.Service != "easyeda-agent" || h.PID <= 0 {
		return false
	}
	return staleInstalledDaemon(h.Version, cliVersion)
}

// daemonVersionAt reads the version an identified daemon reports on port.
func daemonVersionAt(cfg *appConfig, port int) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	r := scanHealth(ctx, hostPortOptions{host: cfg.host, portStart: port, portEnd: port})
	if r.Found == nil {
		return "", false
	}
	return healthVersion(r.Found.Raw), true
}

func healthVersion(raw []byte) string {
	var h struct {
		Version string `json:"version"`
	}
	_ = json.Unmarshal(raw, &h)
	return h.Version
}

// Lifecycle effects are replaceable in tests so discovery regressions cannot
// signal a process or spawn a real daemon.
var restartDaemonStop = stopLocalDaemonMatching
var restartDaemonStart = startDaemonBackgroundFrom

// restartInstalledDaemon replaces the identified daemon on port with exe ("" =
// this binary) in the background. still re-checks the live version right before
// stopping, so two CLIs that saw the same stale daemon do not stop each other's
// fresh replacement. Startup flags of the old process are not inherited.
func restartInstalledDaemon(cfg *appConfig, port int, exe string, still func(version string) bool, stderr io.Writer, automaticGuard ...func([]byte) bool) error {
	var expected daemonStopExpectation
	if len(automaticGuard) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		scan := scanHealth(ctx, hostPortOptions{host: cfg.host, portStart: port, portEnd: port})
		if scan.Found == nil || !automaticGuard[0](scan.Found.Raw) || !still(healthVersion(scan.Found.Raw)) {
			return nil
		}
		var identity struct {
			PID     int    `json:"pid"`
			Version string `json:"version"`
		}
		if json.Unmarshal(scan.Found.Raw, &identity) != nil || identity.PID <= 0 {
			return nil
		}
		expected = daemonStopExpectation{PID: identity.PID, Version: identity.Version}
	} else if v, ok := daemonVersionAt(cfg, port); ok && !still(v) {
		return nil
	}
	if err := restartDaemonStop(cfg.host, port, stderr, expected); err != nil {
		return err
	}
	_, err := restartDaemonStart(cfg, stderr, exe)
	return err
}
