package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/zhoushoujianwork/easyeda-agent/internal/selfupdate"
	"github.com/zhoushoujianwork/easyeda-agent/internal/version"
)

// exitCodeUpdatesAvailable is the exit code `update --check --exit-code` uses when
// the release-compatibility session gate is not ready, so CI/agents can gate on
// it without parsing text (0 = verified and compatible, 1 = check failure).
const exitCodeUpdatesAvailable = 10

// updateReport is the JSON shape of `easyeda update` / `easyeda update --check`.
// The three moving parts of an install are reported side by side: the CLI
// binary, the skill dirs, and the EasyEDA connector — only the first two can be
// updated from here (see connectorNote).
type updateReport struct {
	Mode            string                 `json:"mode"` // check | apply
	CLIVersion      string                 `json:"cliVersion"`
	Latest          string                 `json:"latest,omitempty"`
	LatestErr       string                 `json:"latestError,omitempty"`
	Target          string                 `json:"target,omitempty"`
	CLI             *selfupdate.CLIOutcome `json:"cli,omitempty"`
	Skills          []updateSkillRow       `json:"skills,omitempty"`
	Connector       *connectorReport       `json:"connector,omitempty"`
	Behind          int                    `json:"behind"`          // components behind the target
	Mismatched      int                    `json:"mismatched"`      // components at a different or unknown version
	Unverified      int                    `json:"unverified"`      // live components that could not be checked
	RestartRequired bool                   `json:"restartRequired"` // this process/session loaded a replaced component
	Ready           bool                   `json:"ready"`           // exact CLI/Skill/daemon + compatible connector
	Notes           []string               `json:"notes,omitempty"`
}

// updateSkillRow is one skill dir's before/after. In check mode only the
// "installed" side is filled in.
type updateSkillRow struct {
	Client    string `json:"client"`
	Dir       string `json:"dir"`
	From      string `json:"from,omitempty"`      // version before this run (apply mode)
	Installed string `json:"installed,omitempty"` // version on disk after this run
	Present   bool   `json:"present"`
	Status    string `json:"status"` // behind | ahead | unknown | current | not-installed | updated | created | preserved | skipped | error
	Err       string `json:"err,omitempty"`
}

// connectorReport is the read-only connector verdict. The .eext has no
// programmatic in-place update for sideloads, so `update` can only tell the
// truth about it and print the re-import path.
type connectorReport struct {
	UnknownVersions bool     `json:"unknownVersions,omitempty"`
	DaemonRunning   bool     `json:"daemonRunning"`
	DaemonVersion   string   `json:"daemonVersion,omitempty"`
	DaemonStatus    string   `json:"daemonStatus"` // current | mismatch | unknown | not-running
	DaemonPort      int      `json:"daemonPort,omitempty"`
	Versions        []string `json:"versions,omitempty"` // distinct connector versions across windows
	Windows         int      `json:"windows"`
	Status          string   `json:"status"` // ok | compatible | behind | mismatch | unknown | no-daemon | no-window
}

func newUpdateCmd(cfg *appConfig, stdout, stderr io.Writer) *cobra.Command {
	var (
		checkOnly     bool
		exitCode      bool
		pinVersion    string
		cliOnly       bool
		skillOnly     bool
		clients       []string
		preserve      bool
		force         bool
		createMissing bool
		jsonOut       bool
		localDir      string
		localBinary   string
	)
	c := &cobra.Command{
		Use:     "update",
		Aliases: []string{"upgrade", "self-update"},
		Short:   "Update CLI/Skills and verify the latest compatible runtime set",
		Long: `Bring this installation up to the latest GitHub release.

Covers the two pieces that CAN be updated programmatically:
  • the easyeda CLI binary itself (downloaded for this platform, sha256-verified
    when the release publishes checksums.txt, then atomically swapped in place)
  • the easyeda-agent skill dirs (~/.claude/skills, ~/.codex/skills,
    and the shared ~/.agents/skills root used by Codex Desktop)

The EasyEDA connector .eext is only REPORTED: a sideloaded extension has no
in-place auto-update, so a stale connector has to be re-imported by hand
(marketplace installs update themselves, but lag the CLI).

A dev build (git-describe stamp) is never overwritten without --force.
If the binary lives in a root-owned dir, re-run with sudo.`,
		Args: cobra.NoArgs,
		Example: `  easyeda update                    # CLI + skills → latest
  easyeda update --check            # report only, change nothing
  easyeda update --check --exit-code  # exit 10 unless the release-compatibility gate is ready
  easyeda update --version 0.25.0   # pin a release
  easyeda update --skill-only       # leave the binary alone
  easyeda update --json
  easyeda update --local-dir ./dist --binary /absolute/path/to/easyeda  # install trusted local dev assets
  easyeda update --local-dir ./dist --check --exit-code                 # offline, exact local runtime gate`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if localDir != "" {
				for _, flag := range []string{"version", "cli-only", "skill-only", "client", "preserve", "force", "create-missing"} {
					if cmd.Flags().Changed(flag) {
						return fmt.Errorf("--local-dir cannot be combined with --%s", flag)
					}
				}
				if checkOnly && localBinary != "" {
					return fmt.Errorf("local check verifies the running CLI; --binary is install-only")
				}
				return runLocalUpdate(cfg, localDir, localBinary, checkOnly, exitCode, jsonOut, stdout)
			}
			if localBinary != "" {
				return fmt.Errorf("--binary requires --local-dir")
			}
			if cliOnly && skillOnly {
				return fmt.Errorf("--cli-only and --skill-only are mutually exclusive")
			}
			clients = normalizeClients(clients)
			if !cliOnly || len(clients) > 0 {
				if err := selfupdate.ValidateClients(clients); err != nil {
					return err
				}
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 120*time.Second)
			defer cancel()

			rep := updateReport{Mode: "apply", CLIVersion: version.Version}
			operationFailed := false
			if checkOnly {
				rep.Mode = "check"
			}

			// Resolve the target release first — everything below compares against it.
			target := selfupdate.SemverCore(pinVersion)
			if pinVersion == "" {
				latest, err := selfupdate.LatestReleaseVersion(ctx)
				if err != nil {
					rep.LatestErr = err.Error()
					if jsonOut {
						emitJSON(stdout, rep)
						return errQuiet
					}
					return fmt.Errorf("resolve latest release (pass --version to pin): %w", err)
				}
				rep.Latest = latest
				target = latest
			} else if target == "" {
				return fmt.Errorf("bad --version %q (want x.y.z)", pinVersion)
			}
			rep.Target = target

			// ── connector (read-only, best-effort) ───────────────────────────
			rep.Connector = probeConnector(cfg, target)

			// ── CLI binary ───────────────────────────────────────────────────
			if !skillOnly {
				if checkOnly {
					rep.CLI = checkCLI(target, force)
				} else {
					outcome, err := selfupdate.UpdateCLI(ctx, selfupdate.CLIOptions{
						TargetVersion:  target,
						CurrentVersion: version.Version,
						Force:          force,
					}, func(format string, a ...any) { fmt.Fprintf(stderr, format+"\n", a...) })
					rep.CLI = &outcome
					if err != nil {
						operationFailed = true
						if !jsonOut {
							fmt.Fprintf(stderr, "cli update failed: %v\n", err)
						}
					}
				}
			}

			// ── skill dirs ───────────────────────────────────────────────────
			if !cliOnly && !operationFailed {
				if checkOnly {
					rep.Skills = checkSkills(target, normalizeClients(clients))
				} else {
					if !cmd.Flags().Changed("preserve") && selfupdate.PreserveFromEnv() {
						preserve = true
					}
					res, err := selfupdate.SyncSkills(ctx, selfupdate.SyncOptions{
						TargetVersion: target,
						Clients:       normalizeClients(clients),
						Preserve:      preserve,
						Force:         force,
						CreateMissing: createMissing,
					}, func(format string, a ...any) { fmt.Fprintf(stderr, format+"\n", a...) })
					for _, o := range res.Outcomes {
						row := updateSkillRow{
							Client:  o.Client,
							Dir:     o.Dir,
							From:    o.From,
							Present: o.Status != "skipped",
							Status:  o.Status,
							Err:     o.Err,
						}
						// "Installed" is what is on disk AFTER this run — only a
						// dir we actually wrote (or confirmed current) reached
						// the target; a skip/error stays at its old marker.
						switch o.Status {
						case "updated", "created", "up-to-date":
							row.Installed = o.To
						default:
							row.Installed = o.From
						}
						rep.Skills = append(rep.Skills, row)
					}
					if err != nil {
						operationFailed = true
						if !jsonOut {
							fmt.Fprintf(stderr, "skill sync: %v\n", err)
						}
					}
				}
			}

			rep.Behind = countBehind(rep)
			rep.Mismatched, rep.Unverified = countVersionGateProblems(rep)
			rep.RestartRequired = updateRequiresRestart(rep)
			rep.Ready = rep.Behind == 0 && rep.Mismatched == 0 && rep.Unverified == 0 && !rep.RestartRequired
			rep.Notes = updateNotes(rep)
			if !cliOnly && rep.CLI != nil && rep.CLI.Status == "error" {
				rep.Notes = append(rep.Notes, "Skill update skipped because the CLI update failed.")
			}

			if jsonOut {
				emitJSON(stdout, rep)
			} else {
				printUpdateReport(stdout, rep)
			}
			if operationFailed {
				return errQuiet
			}
			if checkOnly && exitCode && !rep.Ready {
				return exitCodeError{code: exitCodeUpdatesAvailable}
			}
			return nil
		},
	}
	c.Flags().BoolVar(&checkOnly, "check", false, "verify target versions and connector compatibility without changing anything")
	c.Flags().StringVar(&localDir, "local-dir", "", "install/check trusted X.Y.Z-dev.N assets from this local directory; never query GitHub")
	c.Flags().StringVar(&localBinary, "binary", "", "with --local-dir install: absolute destination of the PATH CLI (required)")
	c.Flags().BoolVar(&exitCode, "exit-code", false,
		fmt.Sprintf("with --check: exit %d unless ready (local-dir requires exact dev versions and file contents)", exitCodeUpdatesAvailable))
	c.Flags().StringVar(&pinVersion, "version", "", "pin a release version (default: latest)")
	c.Flags().BoolVar(&cliOnly, "cli-only", false, "update only the CLI binary")
	c.Flags().BoolVar(&skillOnly, "skill-only", false, "update only the skill dirs")
	c.Flags().StringSliceVar(&clients, "client", nil, "limit skill sync to clients: claude,codex,agents (default: all present)")
	c.Flags().BoolVar(&preserve, "preserve", false, "skill sync: keep local edits (never overwrite existing files)")
	c.Flags().BoolVar(&force, "force", false, "overwrite a dev build / re-install even when already at the target")
	c.Flags().BoolVar(&createMissing, "create-missing", false, "install the skill into a client dir that doesn't exist yet")
	c.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	return c
}

// checkCLI is the read-only half of the CLI update: same verdicts as
// selfupdate.UpdateCLI, without downloading anything.
func checkCLI(target string, force bool) *selfupdate.CLIOutcome {
	out := &selfupdate.CLIOutcome{From: version.Version, To: target}
	if p, err := selfupdate.CurrentBinaryPath(); err == nil {
		out.Path = p
	}
	switch {
	case !selfupdate.IsCleanRelease(version.Version) && !force:
		out.Status = "skipped"
		out.Reason = fmt.Sprintf("dev build (%s) — --force to install v%s anyway", version.Version, target)
	case selfupdate.SemverLess(version.Version, target):
		out.Status = "behind"
	case selfupdate.SemverCore(version.Version) == target:
		out.Status = "up-to-date"
	default:
		out.Status = "ahead" // running a newer build than the pinned target
	}
	return out
}

// checkSkills reports each skill dir's version against the target.
func checkSkills(target string, clients []string) []updateSkillRow {
	want := map[string]bool{}
	for _, c := range clients {
		want[c] = true
	}
	var rows []updateSkillRow
	for _, t := range selfupdate.Targets(false) {
		if len(want) > 0 && !want[t.Client] {
			continue
		}
		row := updateSkillRow{Client: t.Client, Dir: t.Dir, Installed: t.Installed, Present: t.Present}
		switch {
		case !t.Present:
			row.Status = "not-installed"
		case selfupdate.SemverCore(t.Installed) == "":
			row.Status = "unknown"
		case selfupdate.SemverLess(t.Installed, target):
			row.Status = "behind"
		case selfupdate.SemverCore(t.Installed) != target:
			row.Status = "ahead"
		default:
			row.Status = "current"
		}
		rows = append(rows, row)
	}
	return rows
}

// probeConnector reads the live daemon's /health to report the connector version
// in each open EasyEDA window. Purely informational: never fails the command,
// and a missing daemon is a normal answer, not an error.
func probeConnector(cfg *appConfig, target string) *connectorReport {
	rep := &connectorReport{Status: "no-daemon", DaemonStatus: "not-running"}
	portStart, portEnd, err := cfg.portRange()
	if err != nil {
		return rep
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	scan := scanHealth(ctx, hostPortOptions{host: cfg.host, portStart: portStart, portEnd: portEnd})
	if scan.Found == nil {
		return rep
	}
	rep.DaemonRunning = true
	rep.DaemonPort = scan.Found.Port

	var parsed struct {
		Version string         `json:"version"`
		Windows []healthWindow `json:"windows"`
	}
	if err := json.Unmarshal(scan.Found.Raw, &parsed); err != nil {
		rep.DaemonStatus = "unknown"
		rep.Status = "unknown"
		return rep
	}
	rep.DaemonVersion = parsed.Version
	switch core := selfupdate.SemverCore(parsed.Version); {
	case core == "":
		rep.DaemonStatus = "unknown"
	case core == target:
		rep.DaemonStatus = "current"
	default:
		rep.DaemonStatus = "mismatch"
	}
	rep.Windows = len(parsed.Windows)
	if len(parsed.Windows) == 0 {
		rep.Status = "no-window"
		return rep
	}

	seen := map[string]bool{}
	behind, mismatch, compatible, unknown := false, false, false, false
	for _, w := range parsed.Windows {
		v := strings.TrimSpace(w.ConnectorVersion)
		if v == "" {
			rep.UnknownVersions = true
			unknown = true
			continue
		}
		if !seen[v] {
			seen[v] = true
			rep.Versions = append(rep.Versions, v)
		}
		core := selfupdate.SemverCore(v)
		switch {
		case core == "":
			unknown = true
		case core == target:
			// Exact connector release.
		case sameMajorMinor(core, target):
			// Patch releases do not change the connector runtime. Marketplace
			// distribution may legitimately lag within this compatibility line.
			compatible = true
		case selfupdate.SemverLess(v, target):
			behind = true
		default:
			mismatch = true
		}
	}
	sort.Strings(rep.Versions)
	switch {
	case behind:
		rep.Status = "behind"
	case mismatch:
		rep.Status = "mismatch"
	case unknown:
		rep.Status = "unknown"
	case compatible:
		rep.Status = "compatible"
	default:
		rep.Status = "ok"
	}
	return rep
}

// countBehind preserves the directional part of the report. The stronger
// --exit-code gate also considers mismatch and unverifiable live state below.
func countBehind(rep updateReport) int {
	n := 0
	if rep.CLI != nil {
		switch rep.CLI.Status {
		case "behind", "error":
			n++
		case "skipped":
			// dev build: intentional, not "behind".
		}
	}
	for _, s := range rep.Skills {
		if s.Status == "behind" || s.Status == "error" || (s.Status == "preserved" && s.Installed != rep.Target) {
			n++
		}
	}
	if rep.Connector != nil && rep.Connector.Status == "behind" {
		n++
	}
	return n
}

// countVersionGateProblems enforces the agent-session invariant: CLI, Skill and
// daemon must equal the selected GitHub release. Connectors need only share its
// major.minor line because patch releases contain no connector runtime changes.
// "Ahead", dev builds and unknown live state are not safe substitutes.
func countVersionGateProblems(rep updateReport) (mismatched, unverified int) {
	if rep.CLI != nil && (rep.CLI.Status == "ahead" || rep.CLI.Status == "skipped") {
		mismatched++
	}
	for _, s := range rep.Skills {
		if s.Present && (s.Status == "ahead" || s.Status == "unknown") {
			mismatched++
		}
	}
	if c := rep.Connector; c != nil {
		switch c.DaemonStatus {
		case "mismatch":
			mismatched++
		case "unknown", "not-running", "":
			unverified++
		}
		switch c.Status {
		case "mismatch":
			mismatched++
		case "unknown", "no-daemon", "no-window":
			unverified++
		}
	}
	return mismatched, unverified
}

func updateRequiresRestart(rep updateReport) bool {
	if rep.CLI != nil && rep.CLI.Status == "updated" {
		return true
	}
	for _, s := range rep.Skills {
		if s.Status == "updated" || s.Status == "created" {
			return true
		}
	}
	return false
}

// updateNotes turns the report into the handful of actionable lines a user needs
// after an update: restart the daemon, re-import the connector, install skills.
func updateNotes(rep updateReport) []string {
	var notes []string
	if rep.Connector != nil && rep.Connector.DaemonStatus == "mismatch" {
		notes = append(notes, "daemon is still running a DIFFERENT binary — restart it with v"+rep.Target+
			" (stop the current `easyeda daemon start`, then start it again)")
	}
	if rep.Connector != nil && (rep.Connector.Status == "behind" || rep.Connector.Status == "mismatch") {
		notes = append(notes, fmt.Sprintf(
			"connector %s is not compatible with the v%s major.minor line and cannot be updated from here — re-import the .eext "+
				"(https://github.com/%s/releases/download/v%s/easyeda-agent-connector.eext), "+
				"then fully quit and relaunch EasyEDA so open windows load it",
			strings.Join(rep.Connector.Versions, ","), rep.Target, selfupdate.RepoSlug, rep.Target))
	}
	skillChanged := false
	for _, s := range rep.Skills {
		if s.Status == "updated" || s.Status == "created" {
			skillChanged = true
		}
		if s.Status == "preserved" {
			notes = append(notes, fmt.Sprintf("skill %s kept local content and its previous version marker; release parity is not claimed", s.Client))
		}
		if s.Status == "not-installed" {
			notes = append(notes, fmt.Sprintf("skill not installed for %s — `easyeda update --create-missing` to add %s", s.Client, s.Dir))
		}
		if s.Status == "skipped" && s.Err != "" {
			notes = append(notes, fmt.Sprintf("skill %s skipped (%s) — `easyeda update --create-missing` to install it", s.Client, s.Err))
		}
	}
	if skillChanged || (rep.CLI != nil && rep.CLI.Status == "updated") {
		notes = append(notes, "this process/session has loaded a component that was just replaced — stop this task and start a new session for the agent; do not continue EDA work in this session")
	}
	return notes
}

func printUpdateReport(w io.Writer, rep updateReport) {
	label := "latest"
	if rep.Latest == "" {
		label = "pinned"
	}
	fmt.Fprintf(w, "easyeda-agent %s  →  %s v%s\n\n", rep.CLIVersion, label, rep.Target)

	// component | status | version(s) | where — one fixed grid so the three
	// rows line up whatever the client names are.
	row := func(name, status, ver, where string) {
		fmt.Fprintf(w, "  %-12s %-13s %-20s %s\n", name, status, ver, where)
	}

	if c := rep.CLI; c != nil {
		ver := "—"
		switch c.Status {
		case "behind", "updated":
			ver = fmt.Sprintf("%s → %s", trimV(c.From), c.To)
		case "up-to-date", "ahead":
			ver = trimV(c.From)
		case "skipped":
			ver = trimV(c.From)
		}
		where := c.Path
		if c.Checksum == "verified" {
			where += "  [sha256 ok]"
		}
		row("cli", c.Status, ver, where)
		if c.Reason != "" {
			fmt.Fprintf(w, "  %-12s %s\n", "", c.Reason)
		}
	}
	for _, s := range rep.Skills {
		var ver string
		switch s.Status {
		case "behind":
			ver = fmt.Sprintf("%s → %s", orDash(s.Installed), rep.Target)
		case "updated", "created":
			ver = fmt.Sprintf("%s → %s", orDash(s.From), orDash(s.Installed))
		case "current", "up-to-date":
			ver = orDash(s.Installed)
		default: // not-installed | skipped | error
			ver = orDash(s.Installed)
		}
		where := s.Dir
		if s.Err != "" {
			where += "  (" + s.Err + ")"
		}
		row("skill:"+s.Client, s.Status, ver, where)
	}
	if c := rep.Connector; c != nil {
		row("daemon", c.DaemonStatus, trimV(c.DaemonVersion), fmt.Sprintf("port :%d", c.DaemonPort))
		ver, where := "—", ""
		switch c.Status {
		case "no-daemon":
			where = "daemon not running — start it to read the connector version"
		case "no-window":
			where = fmt.Sprintf("daemon %s on :%d, no EasyEDA window connected", orDash(c.DaemonVersion), c.DaemonPort)
		case "compatible":
			ver = strings.Join(c.Versions, ",")
			where = fmt.Sprintf("%d window(s), same major.minor; marketplace patch update not required", c.Windows)
		default:
			ver = strings.Join(c.Versions, ",")
			where = fmt.Sprintf("%d window(s), manual .eext re-import only", c.Windows)
		}
		row("connector", c.Status, ver, where)
	}

	fmt.Fprintln(w)
	if rep.Ready {
		fmt.Fprintf(w, "→ READY: CLI/Skills/daemon are exactly v%s; connector major.minor is compatible\n", rep.Target)
	} else {
		fmt.Fprintf(w, "→ BLOCKED: behind=%d mismatched=%d unverified=%d restart-required=%t (required: exact CLI/Skills/daemon v%s + compatible connector major.minor)\n",
			rep.Behind, rep.Mismatched, rep.Unverified, rep.RestartRequired, rep.Target)
	}
	for _, n := range rep.Notes {
		fmt.Fprintf(w, "  ! %s\n", n)
	}
}

// trimV normalizes a version stamp for the table (the daemon and the CLI
// disagree on whether they carry the leading v).
func trimV(v string) string { return orDash(strings.TrimPrefix(v, "v")) }

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

func emitJSON(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
