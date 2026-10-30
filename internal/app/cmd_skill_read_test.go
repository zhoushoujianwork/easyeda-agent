package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func skillReadFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("---\nmetadata:\n  version: \"1.2.3\"\n---\nActual entry\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "references"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, paths := range skillStageChapters {
		for _, path := range paths {
			if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(path)), []byte("# Real chapter\n"+path+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	return root
}

func TestSkillReadUsesActualRootContentAndHashes(t *testing.T) {
	root := skillReadFixture(t)
	var out bytes.Buffer
	cmd := newSkillCmd(&out, &out)
	cmd.SetArgs([]string{"read", "--stage", "pcb-routing", "--skill-root", root})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var before skillStageRead
	if err := json.Unmarshal(out.Bytes(), &before); err != nil {
		t.Fatal(err)
	}
	if before.SkillVersion != "1.2.3" || len(before.Chapters) != 3 || len(before.SHA256) != 64 {
		t.Fatal(before)
	}
	path := filepath.Join(root, "references", "pcb-routing.md")
	if err := os.WriteFile(path, []byte("# Updated routing\nUse measured geometry\n"), 0600); err != nil {
		t.Fatal(err)
	}
	after, err := readSkillStage(root, "pcb-routing")
	if err != nil {
		t.Fatal(err)
	}
	if before.SHA256 == after.SHA256 || before.Chapters[1].SHA256 == after.Chapters[1].SHA256 || after.Chapters[1].Content != "# Updated routing\nUse measured geometry\n" {
		t.Fatal(after)
	}
	if before.Chapters[0] != after.Chapters[0] {
		t.Fatal("unmodified entry changed")
	}
}

func TestSkillReadRefusesMissingEscapedAndInvalidChapters(t *testing.T) {
	for _, mode := range []string{"missing", "escape", "empty", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			root := skillReadFixture(t)
			path := filepath.Join(root, "references", "pcb-routing.md")
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "escape":
				outside := filepath.Join(t.TempDir(), "foreign.md")
				if err := os.WriteFile(outside, []byte("foreign"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, path); err != nil {
					t.Skip(err)
				}
			case "empty":
				if err := os.WriteFile(path, []byte(" \n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "invalid":
				if err := os.WriteFile(path, []byte{0xff}, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if result, err := readSkillStage(root, "pcb-routing"); err == nil || result != nil {
				t.Fatalf("accepted %s", mode)
			}
		})
	}
	if _, err := readSkillStage(skillReadFixture(t), "../../foreign"); err == nil {
		t.Fatal("unknown stage accepted")
	}
}

func TestSkillReadRoutesAllStagesToExistingPublicChapters(t *testing.T) {
	root := filepath.Join("..", "..", ".agents", "skills", "easyeda-agent")
	for stage := range skillStageChapters {
		if _, err := readSkillStage(root, stage); err != nil {
			t.Fatalf("%s: %v", stage, err)
		}
	}
}
