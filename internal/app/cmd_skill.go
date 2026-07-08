package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/zhoushoujianwork/easyeda-agent/internal/selfupdate"
	"github.com/zhoushoujianwork/easyeda-agent/internal/version"
)

// newSkillCmd returns the "skill" subcommand group — inspect and update the
// locally-installed easyeda-agent skill dirs (~/.claude/skills/easyeda-agent,
// ~/.codex/skills/easyeda-agent). The daemon runs `skill sync` automatically on
// startup (daemon start --auto-update-skill, on by default); these commands are
// the manual, self-describing surface for the same machinery.
//
// Scope note: this only manages the SKILL. The EasyEDA connector .eext has no
// official in-place auto-update (marketplace-only), so a stale connector is
// detected + logged by the daemon, and re-imported by hand — see `docs/quick-start.md`.
func newSkillCmd(stdout, stderr io.Writer) *cobra.Command {
	s := &cobra.Command{
		Use:   "skill",
		Short: "Inspect and update the locally-installed easyeda-agent skill dirs",
		Long: "Inspect and update the easyeda-agent skill installed for your AI clients.\n\n" +
			"  easyeda skill status                 show installed skill dirs + versions vs latest release\n" +
			"  easyeda skill sync                   update present skill dirs to the latest release\n" +
			"  easyeda skill sync --version 0.9.0   pin a specific version\n\n" +
			"The daemon also syncs skill dirs on startup (daemon start --auto-update-skill).\n" +
			"The connector .eext is NOT covered here (no sideload auto-update) — the daemon\n" +
			"logs a re-import notice when it detects a stale connector.",
	}
	s.AddCommand(
		newSkillStatusCmd(stdout, stderr),
		newSkillSyncCmd(stdout, stderr),
	)
	return s
}

// skillStatusReport is the JSON shape of `skill status`.
type skillStatusReport struct {
	CLIVersion string                   `json:"cliVersion"`
	Latest     string                   `json:"latest,omitempty"`
	LatestErr  string                   `json:"latestError,omitempty"`
	Targets    []selfupdate.SkillTarget `json:"targets"`
}

func newSkillStatusCmd(stdout, stderr io.Writer) *cobra.Command {
	var jsonOut bool
	c := &cobra.Command{
		Use:   "status",
		Short: "Show installed skill dirs, their versions, and the latest release",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()

			rep := skillStatusReport{
				CLIVersion: version.Version,
				Targets:    selfupdate.Targets(false),
			}
			if latest, err := selfupdate.LatestReleaseVersion(ctx); err != nil {
				rep.LatestErr = err.Error()
			} else {
				rep.Latest = latest
			}

			if jsonOut {
				enc := json.NewEncoder(stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(rep)
			}

			fmt.Fprintf(stdout, "CLI:    %s\n", version.Version)
			if rep.Latest != "" {
				fmt.Fprintf(stdout, "Latest: v%s", rep.Latest)
				if selfupdate.IsCleanRelease(version.Version) && selfupdate.SemverLess(version.Version, rep.Latest) {
					fmt.Fprint(stdout, "  (CLI behind — run install.sh to upgrade CLI + connector)")
				}
				fmt.Fprintln(stdout)
			} else {
				fmt.Fprintf(stdout, "Latest: (unavailable: %s)\n", rep.LatestErr)
			}
			fmt.Fprintln(stdout, "Skill dirs:")
			for _, t := range rep.Targets {
				state := t.Installed
				switch {
				case !t.Present:
					state = "not installed"
				case state == "":
					state = "unknown"
				}
				marker := "  "
				if t.Present && rep.Latest != "" && selfupdate.SemverLess(t.Installed, rep.Latest) {
					marker = "↑ " // behind latest
				}
				fmt.Fprintf(stdout, "  %s%-7s %-14s %s\n", marker, t.Client, state, t.Dir)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	return c
}

func newSkillSyncCmd(stdout, stderr io.Writer) *cobra.Command {
	var (
		pinVersion    string
		clients       []string
		preserve      bool
		force         bool
		createMissing bool
		jsonOut       bool
	)
	c := &cobra.Command{
		Use:   "sync",
		Short: "Update installed skill dirs to the latest release (or a pinned --version)",
		Long: "Download the release skill bundle and materialize it into your client skill dirs.\n\n" +
			"By default only dirs that already exist are updated, to the LATEST release.\n" +
			"Use --create-missing to install into a client dir that doesn't exist yet,\n" +
			"--preserve to keep local edits (never overwrite an existing file), and\n" +
			"--version to pin a specific release instead of latest.",
		Args: cobra.NoArgs,
		Example: `  easyeda skill sync
  easyeda skill sync --version 0.9.0
  easyeda skill sync --client claude --preserve
  easyeda skill sync --create-missing`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			target := selfupdate.SemverCore(pinVersion)
			if pinVersion == "" {
				latest, err := selfupdate.LatestReleaseVersion(ctx)
				if err != nil {
					return fmt.Errorf("resolve latest release (pass --version to pin): %w", err)
				}
				target = latest
			} else if target == "" {
				return fmt.Errorf("bad --version %q (want x.y.z)", pinVersion)
			}

			if !cmd.Flags().Changed("preserve") && selfupdate.PreserveFromEnv() {
				preserve = true
			}

			logf := func(format string, a ...any) {
				fmt.Fprintf(stderr, format+"\n", a...)
			}
			res, err := selfupdate.SyncSkills(ctx, selfupdate.SyncOptions{
				TargetVersion: target,
				Clients:       normalizeClients(clients),
				Preserve:      preserve,
				Force:         force,
				CreateMissing: createMissing,
			}, logf)

			if jsonOut {
				enc := json.NewEncoder(stdout)
				enc.SetIndent("", "  ")
				_ = enc.Encode(res)
			} else {
				for _, o := range res.Outcomes {
					line := fmt.Sprintf("%-7s %-11s %s", o.Client, o.Status, o.Dir)
					if o.Err != "" {
						line += "  (" + o.Err + ")"
					}
					fmt.Fprintln(stdout, line)
				}
				fmt.Fprintf(stdout, "→ %d dir(s) updated to v%s\n", res.Changed, res.Target)
			}
			return err
		},
	}
	c.Flags().StringVar(&pinVersion, "version", "", "pin a release version (default: latest)")
	c.Flags().StringSliceVar(&clients, "client", nil, "limit to clients: claude,codex (default: all present)")
	c.Flags().BoolVar(&preserve, "preserve", false, "keep local edits (never overwrite existing files)")
	c.Flags().BoolVar(&force, "force", false, "sync even if the dir is already at the target version")
	c.Flags().BoolVar(&createMissing, "create-missing", false, "install into a client dir that doesn't exist yet")
	c.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	return c
}

// normalizeClients is a defensive trim for user-passed --client values.
func normalizeClients(in []string) []string {
	var out []string
	for _, c := range in {
		c = strings.ToLower(strings.TrimSpace(c))
		if c != "" {
			out = append(out, c)
		}
	}
	return out
}
