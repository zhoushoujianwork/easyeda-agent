package app

// enrich_script.go — locating bom-enrich.py, cwd-independently (issue #115).
//
// `bom export` enriches by default, so the script must be findable from
// WHEREVER the agent happens to run the CLI (a project dir, /tmp, $HOME). The
// old resolver only reached the repo checkout by walking up from cwd / the
// binary, so a run outside the repo died with a bare "bom-enrich.py not found".
// The installed SKILL dir — the copy every non-repo user actually has, kept
// current by `easyeda skill sync` — was never probed.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/zhoushoujianwork/easyeda-agent/internal/selfupdate"
)

// enrichScriptName is the script this resolver hunts for.
const enrichScriptName = "bom-enrich.py"

// enrichSkillRels are the script's paths relative to a skills ROOT (a dir that
// contains per-skill dirs). The second entry keeps pre-merge installs working.
var enrichSkillRels = []string{
	"easyeda-agent/scripts/" + enrichScriptName,
	"easyeda-schematic/scripts/" + enrichScriptName, // pre-merge skill name
}

// resolveEnrichScript resolves bom-enrich.py, in priority order:
//
//  1. explicit (--script) — used if it exists, hard error if it doesn't (a typo
//     must not silently fall through to some other copy of the script);
//  2. $EASYEDA_SKILLS_DIR/<skill>/scripts/bom-enrich.py — the deployment override;
//  3. the INSTALLED skill dirs (~/.claude/skills/easyeda-agent/scripts/, ~/.codex/…),
//     resolved via selfupdate.Targets so this never drifts from `easyeda skill
//     status` / `skill sync`;
//  4. skills/ walked up from the running binary (dev: ./bin/easyeda beside the repo);
//  5. skills/ walked up from the working directory (agent run inside the repo);
//  6. bom-enrich.py on $PATH.
//
// The error lists every path probed, so a failure says exactly where to put the
// script (or which --script to pass) instead of just "not found".
func resolveEnrichScript(explicit string) (string, error) {
	var probed []string
	// hit records a candidate and reports whether it is a usable file.
	hit := func(path string) bool {
		probed = append(probed, path)
		st, err := os.Stat(path)
		return err == nil && !st.IsDir()
	}

	if explicit = strings.TrimSpace(explicit); explicit != "" {
		if hit(explicit) {
			return explicit, nil
		}
		return "", fmt.Errorf("--script %s: no such file", explicit)
	}

	// 2. Explicit skills-root override.
	if root := strings.TrimSpace(os.Getenv("EASYEDA_SKILLS_DIR")); root != "" {
		for _, rel := range enrichSkillRels {
			if c := filepath.Join(root, filepath.FromSlash(rel)); hit(c) {
				return c, nil
			}
		}
	}

	// 3. Installed skill dirs (each Target.Dir is …/skills/easyeda-agent).
	for _, t := range selfupdate.Targets(false) {
		if c := filepath.Join(t.Dir, "scripts", enrichScriptName); hit(c) {
			return c, nil
		}
	}

	// 4/5. skills/ walked up from the binary, then from cwd.
	walkUp := func(dir string) (string, bool) {
		for i := 0; i < 8; i++ {
			for _, rel := range enrichSkillRels {
				if c := filepath.Join(dir, "skills", filepath.FromSlash(rel)); hit(c) {
					return c, true
				}
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
		return "", false
	}
	if exe, err := os.Executable(); err == nil {
		// Resolve symlinks: a /usr/local/bin/easyeda symlinked into the repo's
		// bin/ should walk up the REPO, not /usr/local.
		if real, rerr := filepath.EvalSymlinks(exe); rerr == nil {
			exe = real
		}
		if path, ok := walkUp(filepath.Dir(exe)); ok {
			return path, nil
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		if path, ok := walkUp(cwd); ok {
			return path, nil
		}
	}

	// 6. PATH.
	if path, err := exec.LookPath(enrichScriptName); err == nil {
		probed = append(probed, "$PATH/"+enrichScriptName+" → "+path)
		return path, nil
	}
	probed = append(probed, "$PATH/"+enrichScriptName)

	return "", fmt.Errorf("%s not found — probed:\n  %s\npass --script /path/to/%s, "+
		"set EASYEDA_SKILLS_DIR to your skills root, or install the skill (`easyeda skill sync --create-missing`)",
		enrichScriptName, strings.Join(probed, "\n  "), enrichScriptName)
}
