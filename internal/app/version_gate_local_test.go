package app

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"github.com/zhoushoujianwork/easyeda-agent/internal/selfupdate"
	"github.com/zhoushoujianwork/easyeda-agent/internal/version"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLocalUpdateReadyAndUnknownWindow(t *testing.T) {
	const v = "1.4.9-dev.1"
	old := version.Version
	version.Version = "v" + v
	t.Cleanup(func() { version.Version = old })
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	dir := t.TempDir()
	skill := filepath.Join(home, ".agents/skills/easyeda-agent")
	os.MkdirAll(skill, 0755)
	doc := []byte("---\nmetadata:\n  version: \"" + v + "\"\n---\n")
	os.WriteFile(filepath.Join(skill, "SKILL.md"), doc, 0644)
	os.WriteFile(filepath.Join(skill, ".version"), []byte(v), 0644)
	var packed, zipped bytes.Buffer
	gz := gzip.NewWriter(&packed)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "easyeda-agent/SKILL.md", Mode: 0644, Size: int64(len(doc)), Typeflag: tar.TypeReg})
	tw.Write(doc)
	tw.Close()
	gz.Close()
	zw := zip.NewWriter(&zipped)
	w, _ := zw.Create("extension.json")
	fmt.Fprintf(w, `{"version":%q}`, v)
	w, _ = zw.Create("dist/index.js")
	fmt.Fprint(w, "compiled")
	zw.Close()
	exe, e := selfupdate.CurrentBinaryPath()
	if e != nil {
		t.Fatal(e)
	}
	bin, e := os.ReadFile(exe)
	if e != nil {
		t.Fatal(e)
	}
	asset, e := selfupdate.AssetName(runtime.GOOS, runtime.GOARCH)
	if e != nil {
		t.Fatal(e)
	}
	var sums strings.Builder
	for name, data := range map[string][]byte{asset: bin, "skills.tar.gz": packed.Bytes(), "easyeda-agent-connector.eext": zipped.Bytes()} {
		os.WriteFile(filepath.Join(dir, name), data, 0644)
		fmt.Fprintf(&sums, "%x  %s\n", sha256.Sum256(data), name)
	}
	os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(sums.String()), 0644)
	for _, unknown := range []bool{false, true} {
		windows := `{"connectorVersion":"1.4.9-dev.1"}`
		if unknown {
			windows += `,{"connectorVersion":""}`
		}
		host, port := fakeDaemon(t, `{"service":"easyeda-agent","status":"ok","version":"v1.4.9-dev.1","windows":[`+windows+`]}`)
		cfg := &appConfig{host: host, ports: fmt.Sprintf("%d-%d", port, port)}
		var out bytes.Buffer
		e := runLocalUpdate(cfg, dir, "", true, true, true, &out)
		if (e != nil) != unknown {
			t.Fatalf("unknown=%v err=%v report=%s", unknown, e, out.String())
		}
	}
}

func TestLocalRuntimeExactAndReconnect(t *testing.T) {
	const v = "v1.4.9-dev.1"
	old := version.Version
	version.Version = v
	t.Cleanup(func() { version.Version = old })
	for _, tc := range []struct {
		daemon     string
		connectors []string
		ok         bool
	}{
		{v, []string{"1.4.9-dev.1"}, true},
		{"v1.4.9-dev.2", []string{v}, false},
		{v, []string{"1.4.8"}, false},
		{v, []string{v, ""}, false},
		{v, nil, false},
	} {
		r := evaluateVersionGate(v, tc.daemon, tc.connectors)
		if (r.Verdict == versionSevOK) != tc.ok {
			t.Fatalf("%+v: %+v", tc, r)
		}
	}
	cfg := &appConfig{skipVersionCheck: true}
	var out bytes.Buffer
	if e := checkVersionGate(cfg, []byte(`{"version":"v1.4.9-dev.1","windows":[{"connectorVersion":"1.4.9-dev.1"}]}`), &out); e != nil {
		t.Fatal(e)
	}
	if e := checkVersionGate(cfg, []byte(`{"version":"v1.4.8","windows":[{"connectorVersion":"1.4.8"}]}`), &out); e == nil {
		t.Fatal("cached pass or skip flag bypassed local mismatch")
	}
	if e := checkVersionGate(cfg, []byte(`bad`), &out); e == nil {
		t.Fatal("invalid health accepted")
	}
}

func TestLocalUpdateRejectsMixedModesWithoutNetwork(t *testing.T) {
	for _, args := range [][]string{
		{"update", "--local-dir", "/missing", "--version", "1.4.8"},
		{"update", "--local-dir", "/missing", "--check", "--binary", "/tmp/easyeda"},
		{"update", "--binary", "/tmp/easyeda"},
		{"update", "--local-dir", "/missing", "--client", "agents"},
	} {
		var out bytes.Buffer
		root := newRootCmd(&out, &out)
		root.SetArgs(args)
		if e := root.Execute(); e == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
