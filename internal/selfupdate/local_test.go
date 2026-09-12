package selfupdate

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func localFixture(t *testing.T, v string, evil bool) string {
	t.Helper()
	dir := t.TempDir()
	var z bytes.Buffer
	w := zip.NewWriter(&z)
	f, _ := w.Create("extension.json")
	fmt.Fprintf(f, `{"version":%q}`, v)
	f, _ = w.Create("dist/index.js")
	fmt.Fprint(f, "compiled")
	w.Close()
	asset, _ := AssetName(runtime.GOOS, runtime.GOARCH)
	files := map[string][]byte{
		asset:                          []byte("#!/bin/sh\necho 'easyeda-agent v" + v + "'\n"),
		"easyeda-agent-connector.eext": z.Bytes(),
		"skills.tar.gz":                makeVersionedTarball(t, v, map[string]string{"SKILL.md": "guide", "references/test.md": "expected"}, evil),
	}
	var sums strings.Builder
	for name, data := range files {
		if e := os.WriteFile(filepath.Join(dir, name), data, 0644); e != nil {
			t.Fatal(e)
		}
		fmt.Fprintf(&sums, "%x  %s\n", sha256.Sum256(data), name)
	}
	os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(sums.String()), 0644)
	return dir
}

func TestLocalBundleChecksContentsNotMarkers(t *testing.T) {
	dir := localFixture(t, "1.4.9-dev.1", false)
	b, e := ReadLocalBundle(dir)
	if e != nil {
		t.Fatal(e)
	}
	skill := t.TempDir()
	for name, data := range b.Skill {
		p := filepath.Join(skill, name)
		os.MkdirAll(filepath.Dir(p), 0755)
		os.WriteFile(p, data, 0644)
	}
	os.WriteFile(filepath.Join(skill, ".version"), []byte(b.Version), 0644)
	if e := b.CheckSkill(skill); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(skill, "references/test.md"), []byte("stale despite correct marker"), 0644)
	if b.CheckSkill(skill) == nil {
		t.Fatal("accepted stale contents")
	}
	os.WriteFile(filepath.Join(skill, "references/test.md"), b.Skill["references/test.md"], 0644)
	os.WriteFile(filepath.Join(skill, "extra.md"), []byte("retired"), 0644)
	if b.CheckSkill(skill) == nil {
		t.Fatal("accepted extra contents")
	}
	os.WriteFile(filepath.Join(dir, "skills.tar.gz"), []byte("corrupt"), 0644)
	if _, e := ReadLocalBundle(dir); e == nil {
		t.Fatal("accepted corrupt bundle")
	}
}

func TestLocalBundleRejectsReleaseAndTraversal(t *testing.T) {
	for _, tc := range []struct {
		v    string
		evil bool
	}{{"1.4.9", false}, {"1.4.9-dev.0", false}, {"1.4.9-dev.1", true}} {
		if _, e := ReadLocalBundle(localFixture(t, tc.v, tc.evil)); e == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
}

func TestLocalInstallBackupsAndRealBytes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture executable is a POSIX shell script")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	skill := filepath.Join(home, ".agents/skills/easyeda-agent")
	os.MkdirAll(skill, 0755)
	os.WriteFile(filepath.Join(skill, "old.md"), []byte("original"), 0644)
	bin := filepath.Join(home, "easyeda")
	os.WriteFile(bin, []byte("original binary"), 0755)
	b, e := ReadLocalBundle(localFixture(t, "1.4.9-dev.1", false))
	if e != nil {
		t.Fatal(e)
	}
	if e = b.Install(bin, io.Discard); e != nil {
		t.Fatal(e)
	}
	if e = b.CheckBinary(bin); e != nil {
		t.Fatal(e)
	}
	if e = b.CheckSkill(skill); e != nil {
		t.Fatal(e)
	}
	backups, _ := filepath.Glob(filepath.Join(home, ".easyeda-backup-*"))
	if len(backups) != 1 {
		t.Fatal(backups)
	}
	old, _ := os.ReadFile(backups[0])
	if string(old) != "original binary" {
		t.Fatal("lost binary backup")
	}
	backups, _ = filepath.Glob(filepath.Join(filepath.Dir(skill), ".easyeda-local-backup-*"))
	if len(backups) != 1 {
		t.Fatal(backups)
	}
	old, _ = os.ReadFile(filepath.Join(backups[0], "old.md"))
	if string(old) != "original" {
		t.Fatal("lost Skill backup")
	}
	b.Binary = []byte("#!/bin/sh\necho 'easyeda-agent v1.4.8'\n")
	if e = b.Install(bin, io.Discard); e == nil {
		t.Fatal("installed incorrectly versioned executable")
	}
}
