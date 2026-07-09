package blocks

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// skillBlocksDir is the community source-of-truth; the embedded data/ dir is a
// build-time copy (Makefile `sync-blocks`). Relative to this test file.
const skillBlocksDir = "../../skills/easyeda-agent/references/blocks"

func TestLoad(t *testing.T) {
	all, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("no blocks embedded — did `make sync-blocks` run?")
	}
	for _, b := range all {
		if b.ID == "" || b.Desc == "" {
			t.Errorf("block missing id/desc: %+v", b)
		}
		if b.Ready() != (b.Validated != nil && *b.Validated != "") {
			t.Errorf("%s: Ready() disagrees with Validated", b.ID)
		}
	}
}

func TestGetPrefixOptional(t *testing.T) {
	all, err := Load()
	if err != nil || len(all) == 0 {
		t.Skip("no blocks")
	}
	want := all[0].ID // e.g. block.xxx
	bare := want[len("block."):]
	for _, id := range []string{want, bare} {
		b, ok, err := Get(id)
		if err != nil || !ok {
			t.Fatalf("Get(%q): ok=%v err=%v", id, ok, err)
		}
		if b.ID != want {
			t.Errorf("Get(%q) → %s, want %s", id, b.ID, want)
		}
	}
}

// TestEmbedInSyncWithSkill fails if the go:embed copy drifted from the skill
// source — a forgotten `make sync-blocks` after editing a block. Keeps the two
// copies honest so a remote `go install` binary ships the real library.
func TestEmbedInSyncWithSkill(t *testing.T) {
	skillFiles, err := filepath.Glob(filepath.Join(skillBlocksDir, "*.json"))
	if err != nil {
		t.Fatalf("glob skill blocks: %v", err)
	}
	if len(skillFiles) == 0 {
		t.Skip("skill blocks dir not found (running outside repo tree)")
	}
	var skillNames []string
	for _, f := range skillFiles {
		name := filepath.Base(f)
		if strings.HasPrefix(name, "_") { // _schema.json etc. are not blocks, not embedded
			continue
		}
		skillNames = append(skillNames, name)
		embedded, err := data.ReadFile("data/" + name)
		if err != nil {
			t.Errorf("%s in skill but not embedded — run `make sync-blocks`", name)
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if string(src) != string(embedded) {
			t.Errorf("%s: embedded copy differs from skill source — run `make sync-blocks`", name)
		}
	}
	// Reverse: no stale embedded file the skill no longer has.
	embeddedEntries, _ := data.ReadDir("data")
	sort.Strings(skillNames)
	for _, e := range embeddedEntries {
		name := e.Name()
		if idx := sort.SearchStrings(skillNames, name); idx >= len(skillNames) || skillNames[idx] != name {
			t.Errorf("%s embedded but not in skill source — run `make sync-blocks`", name)
		}
	}
}
