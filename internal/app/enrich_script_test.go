package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mkScript creates a fake bom-enrich.py at path (with parents) and returns path.
func mkScript(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/usr/bin/env python3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// isolate points HOME (→ the installed skill dirs) and cwd at empty temp dirs and
// clears the env override, so a probe only finds what a test plants. PATH is
// emptied too: a real bom-enrich.py on the developer's PATH must not leak in.
func isolate(t *testing.T) (home string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("EASYEDA_SKILLS_DIR", "")
	t.Setenv("PATH", "")
	t.Chdir(t.TempDir())
	return home
}

// TestResolveEnrichScriptPriority walks the probe ladder (issue #115): each rung
// wins while it exists, and removing it falls through to the next.
func TestResolveEnrichScriptPriority(t *testing.T) {
	home := isolate(t)

	skillsRoot := t.TempDir()
	envScript := mkScript(t, filepath.Join(skillsRoot, "easyeda-agent", "scripts", "bom-enrich.py"))
	installed := mkScript(t, filepath.Join(home, ".claude", "skills", "easyeda-agent", "scripts", "bom-enrich.py"))

	// cwd rung: a repo-shaped tree, with cwd a few levels down.
	repo := t.TempDir()
	cwdScript := mkScript(t, filepath.Join(repo, "skills", "easyeda-agent", "scripts", "bom-enrich.py"))
	deep := filepath.Join(repo, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	explicit := mkScript(t, filepath.Join(t.TempDir(), "custom-enrich.py"))

	t.Run("1 explicit --script wins over everything", func(t *testing.T) {
		t.Setenv("EASYEDA_SKILLS_DIR", skillsRoot)
		got, err := resolveEnrichScript(explicit)
		if err != nil || got != explicit {
			t.Fatalf("got %q, %v; want %q", got, err, explicit)
		}
	})

	t.Run("1b explicit --script that does not exist is a hard error", func(t *testing.T) {
		t.Setenv("EASYEDA_SKILLS_DIR", skillsRoot) // a valid fallback exists…
		missing := filepath.Join(t.TempDir(), "typo.py")
		got, err := resolveEnrichScript(missing)
		if err == nil {
			t.Fatalf("a --script typo must not silently fall through (got %q)", got)
		}
		if !strings.Contains(err.Error(), missing) {
			t.Errorf("error must name the bad path: %v", err)
		}
	})

	t.Run("2 EASYEDA_SKILLS_DIR beats the installed skill dir", func(t *testing.T) {
		t.Setenv("EASYEDA_SKILLS_DIR", skillsRoot)
		got, err := resolveEnrichScript("")
		if err != nil || got != envScript {
			t.Fatalf("got %q, %v; want %q", got, err, envScript)
		}
	})

	t.Run("3 installed skill dir is found from an unrelated cwd (#115)", func(t *testing.T) {
		got, err := resolveEnrichScript("")
		if err != nil || got != installed {
			t.Fatalf("got %q, %v; want %q", got, err, installed)
		}
	})

	t.Run("5 cwd walk-up when no skill is installed", func(t *testing.T) {
		if err := os.RemoveAll(filepath.Join(home, ".claude")); err != nil {
			t.Fatal(err)
		}
		t.Chdir(deep)
		got, err := resolveEnrichScript("")
		if err != nil || got != cwdScript {
			t.Fatalf("got %q, %v; want %q", got, err, cwdScript)
		}
	})
}

// TestResolveEnrichScriptPreMergeSkillName: an old easyeda-schematic install
// still resolves (the pre-merge skill name).
func TestResolveEnrichScriptPreMergeSkillName(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	want := mkScript(t, filepath.Join(root, "easyeda-schematic", "scripts", "bom-enrich.py"))
	t.Setenv("EASYEDA_SKILLS_DIR", root)
	got, err := resolveEnrichScript("")
	if err != nil || got != want {
		t.Fatalf("got %q, %v; want %q", got, err, want)
	}
}

// TestResolveEnrichScriptNotFoundListsProbedPaths: the failure must be
// actionable — every probed path plus the --script hint (issue #115).
func TestResolveEnrichScriptNotFoundListsProbedPaths(t *testing.T) {
	home := isolate(t)
	root := t.TempDir()
	t.Setenv("EASYEDA_SKILLS_DIR", root)

	_, err := resolveEnrichScript("")
	if err == nil {
		t.Fatal("want an error when no copy of the script exists")
	}
	msg := err.Error()
	for _, want := range []string{
		filepath.Join(root, "easyeda-agent", "scripts", "bom-enrich.py"), // env rung
		filepath.Join(home, ".claude", "skills", "easyeda-agent", "scripts", "bom-enrich.py"), // installed rung
		filepath.Join(home, ".codex", "skills", "easyeda-agent", "scripts", "bom-enrich.py"),
		"$PATH/bom-enrich.py",
		"--script",
		"EASYEDA_SKILLS_DIR",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error must mention %q; got:\n%s", want, msg)
		}
	}
	// The cwd rung must appear too (the dir the CLI was actually run from).
	cwd, _ := os.Getwd()
	if !strings.Contains(msg, filepath.Join(cwd, "skills")) {
		t.Errorf("error must list the cwd probe %q; got:\n%s", filepath.Join(cwd, "skills"), msg)
	}
}
