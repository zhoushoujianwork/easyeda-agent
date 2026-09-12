package app

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/zhoushoujianwork/easyeda-agent/internal/selfupdate"
	"github.com/zhoushoujianwork/easyeda-agent/internal/version"
)

func runLocalUpdate(cfg *appConfig, dir, binary string, check, exitCode, jsonOut bool, out io.Writer) error {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	b, err := selfupdate.ReadLocalBundle(dir)
	if err != nil {
		return err
	}
	if !check {
		if jsonOut {
			return fmt.Errorf("local installation prints backup paths; --json is check-only")
		}
		if binary == "" {
			return fmt.Errorf("local installation requires --binary /absolute/path/to/easyeda")
		}
		if err := b.Install(binary, out); err != nil {
			return err
		}
		fmt.Fprintf(out, "Installed local v%s. NOT READY: restart daemon, import %s and fully restart EasyEDA. Start a new Agent session before EDA operations. No tag/upload performed.\n", b.Version, b.Connector)
		return nil
	}
	rep := struct {
		Mode     string   `json:"mode"`
		Target   string   `json:"target"`
		Ready    bool     `json:"ready"`
		Problems []string `json:"problems"`
	}{Mode: "local-check", Target: b.Version, Problems: []string{}}
	problem := func(s string) { rep.Problems = append(rep.Problems, s) }
	if normVersion(version.Version) != b.Version {
		problem("CLI embedded version differs from local package")
	}
	p, e := selfupdate.CurrentBinaryPath()
	if e == nil {
		e = b.CheckBinary(p)
	}
	if e != nil {
		problem(e.Error())
	}
	present := 0
	for _, t := range selfupdate.Targets(false) {
		if !t.Present {
			continue
		}
		present++
		if e := b.CheckSkill(t.Dir); e != nil {
			problem(fmt.Sprintf("%s: %v", t.Dir, e))
		}
	}
	if present == 0 {
		problem("no installed Skill")
	}
	c := probeConnector(cfg, b.Version)
	if !c.DaemonRunning || normVersion(c.DaemonVersion) != b.Version {
		problem("daemon absent or not the exact local version")
	}
	if c.Windows == 0 || c.UnknownVersions || c.Status == "unknown" || len(c.Versions) == 0 {
		problem("connector absent or unknown")
	}
	for _, v := range c.Versions {
		if normVersion(v) != b.Version {
			problem("connector is not the exact local version: " + v)
		}
	}
	rep.Ready = len(rep.Problems) == 0
	if jsonOut {
		emitJSON(out, rep)
	} else {
		fmt.Fprintf(out, "Local development target: v%s (offline; no GitHub latest)\n", b.Version)
		for _, s := range rep.Problems {
			fmt.Fprintln(out, "BLOCK: "+s)
		}
		if rep.Ready {
			fmt.Fprintln(out, "READY: local package, CLI, Skills and live runtime match")
		} else {
			fmt.Fprintln(out, "NOT READY")
		}
	}
	if exitCode && !rep.Ready {
		return exitCodeError{code: exitCodeUpdatesAvailable}
	}
	return nil
}
