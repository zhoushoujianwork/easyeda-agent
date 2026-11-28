package app

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSkillEntry(t *testing.T, dir, version string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nmetadata:\n  version: \""+version+"\"\n---\nEntry\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// isolateSkillDirs points the personal client dirs at an empty temporary home.
func isolateSkillDirs(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("EASYEDA_SKILLS_DIR", "")
	return home
}

func TestActiveSkillPrefersEasyedaSkillsDirOverPersonalCopies(t *testing.T) {
	home := isolateSkillDirs(t)
	writeSkillEntry(t, filepath.Join(home, ".claude", "skills", "easyeda-agent"), "1.17.0")
	root := t.TempDir()
	writeSkillEntry(t, filepath.Join(root, "easyeda-agent"), "1.22.0-dev.3")
	t.Setenv("EASYEDA_SKILLS_DIR", root)

	got := resolveActiveSkill()
	if got.Source != "EASYEDA_SKILLS_DIR" || got.Version != "1.22.0-dev.3" || got.Error != "" {
		t.Fatalf("%+v", got)
	}
	if line := formatActiveSkill(got); line != "Active: 1.22.0-dev.3  (EASYEDA_SKILLS_DIR)  "+got.Dir {
		t.Fatal(line)
	}
}

func TestActiveSkillNamesThePersonalClientCopyWithoutOverride(t *testing.T) {
	home := isolateSkillDirs(t)
	writeSkillEntry(t, filepath.Join(home, ".claude", "skills", "easyeda-agent"), "1.17.0")

	got := resolveActiveSkill()
	if got.Source != "claude" || got.Version != "1.17.0" {
		t.Fatalf("%+v", got)
	}
}

func TestFormatActiveSkillReportsMissingAndUnreadableCopies(t *testing.T) {
	if line := formatActiveSkill(activeSkill{Error: "SKILL.md not found"}); line != "Active: (none: SKILL.md not found)" {
		t.Fatal(line)
	}
	if line := formatActiveSkill(activeSkill{Dir: "/s", Source: "codex", Error: "bad frontmatter"}); line != "Active: unknown (bad frontmatter)  (codex)  /s" {
		t.Fatal(line)
	}
}
