package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSemverCore(t *testing.T) {
	cases := map[string]string{
		"v0.9.0":              "0.9.0",
		"0.9.0":               "0.9.0",
		"v0.9.0-1-gabc-dirty": "0.9.0",
		"dev":                 "",
		"0.9":                 "",
		"x.y.z":               "",
	}
	for in, want := range cases {
		if got := SemverCore(in); got != want {
			t.Errorf("SemverCore(%q)=%q want %q", in, got, want)
		}
	}
}

func TestSemverLess(t *testing.T) {
	if !SemverLess("0.8.3", "0.9.0") {
		t.Error("0.8.3 < 0.9.0 should be true")
	}
	if SemverLess("0.9.0", "0.9.0") {
		t.Error("equal is not less")
	}
	if SemverLess("0.10.0", "0.9.0") {
		t.Error("0.10.0 < 0.9.0 should be false (numeric, not lexical)")
	}
}

func TestIsCleanRelease(t *testing.T) {
	if !IsCleanRelease("v0.9.0") {
		t.Error("v0.9.0 is a clean release")
	}
	if IsCleanRelease("v0.9.0-3-gabc-dirty") {
		t.Error("dev-stamped build is not a clean release")
	}
	if IsCleanRelease("dev") {
		t.Error("dev is not a clean release")
	}
}

// makeTarball builds an in-memory skills.tar.gz whose entries live under
// easyeda-agent/, plus an optional malicious entry to test the traversal guard.
func makeTarball(t *testing.T, files map[string]string, withEvil bool) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	write := func(name, body string) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range files {
		write(SkillName+"/"+name, body)
	}
	if withEvil {
		write("../evil.txt", "pwned")
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func serveRelease(t *testing.T, version string, tarball []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/latest":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"tag_name":"v` + version + `"}`))
		case "/download":
			_, _ = w.Write(tarball)
		default:
			http.NotFound(w, r)
		}
	}))
	oldTar, oldLatest := tarballURL, latestAPIURL
	tarballURL = func(v string) string { return srv.URL + "/download" }
	latestAPIURL = func() string { return srv.URL + "/releases/latest" }
	t.Cleanup(func() {
		tarballURL, latestAPIURL = oldTar, oldLatest
		srv.Close()
	})
	return srv
}

func TestSyncSkills_UpdateAndIdempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".claude", "skills", SkillName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	// Pre-existing stale content + old marker.
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("OLD"), 0644); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, versionMarker), []byte("0.8.3\n"), 0644)

	serveRelease(t, "0.9.0", makeTarball(t, map[string]string{
		"SKILL.md":           "NEW",
		"references/flow.md": "flow",
	}, false))

	res, err := SyncSkills(context.Background(), SyncOptions{TargetVersion: "0.9.0", Clients: []string{"claude"}}, nil)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.Changed != 1 {
		t.Fatalf("changed=%d want 1", res.Changed)
	}
	// Overwrote the stale file, added the new one, wrote the marker.
	if b, _ := os.ReadFile(filepath.Join(dir, "SKILL.md")); string(b) != "NEW" {
		t.Errorf("SKILL.md=%q want NEW", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "references", "flow.md")); string(b) != "flow" {
		t.Errorf("references/flow.md=%q want flow", b)
	}
	if got := readMarker(dir); got != "0.9.0" {
		t.Errorf("marker=%q want 0.9.0", got)
	}

	// Re-run: marker matches target → up-to-date, no change.
	res2, err := SyncSkills(context.Background(), SyncOptions{TargetVersion: "0.9.0", Clients: []string{"claude"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Changed != 0 {
		t.Errorf("second run changed=%d want 0", res2.Changed)
	}
	if res2.Outcomes[0].Status != "up-to-date" {
		t.Errorf("status=%q want up-to-date", res2.Outcomes[0].Status)
	}
}

func TestSyncSkills_Preserve(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".claude", "skills", SkillName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("LOCAL-EDIT"), 0644); err != nil {
		t.Fatal(err)
	}

	serveRelease(t, "0.9.0", makeTarball(t, map[string]string{
		"SKILL.md": "UPSTREAM",
		"new.md":   "brand-new",
	}, false))

	if _, err := SyncSkills(context.Background(), SyncOptions{TargetVersion: "0.9.0", Clients: []string{"claude"}, Preserve: true}, nil); err != nil {
		t.Fatal(err)
	}
	// Existing file kept, new file still added.
	if b, _ := os.ReadFile(filepath.Join(dir, "SKILL.md")); string(b) != "LOCAL-EDIT" {
		t.Errorf("preserve kept local? got %q want LOCAL-EDIT", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "new.md")); string(b) != "brand-new" {
		t.Errorf("new file added? got %q", b)
	}
}

func TestSyncSkills_NotInstalledSkipped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	serveRelease(t, "0.9.0", makeTarball(t, map[string]string{"SKILL.md": "x"}, false))

	res, err := SyncSkills(context.Background(), SyncOptions{TargetVersion: "0.9.0", Clients: []string{"claude"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed != 0 || res.Outcomes[0].Status != "skipped" {
		t.Errorf("absent dir should skip, got changed=%d status=%q", res.Changed, res.Outcomes[0].Status)
	}
	// With CreateMissing it should install.
	res2, err := SyncSkills(context.Background(), SyncOptions{TargetVersion: "0.9.0", Clients: []string{"claude"}, CreateMissing: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Changed != 1 || res2.Outcomes[0].Status != "created" {
		t.Errorf("create-missing should install, got changed=%d status=%q", res2.Changed, res2.Outcomes[0].Status)
	}
}

func TestFetchSkillTree_TraversalGuard(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".codex", "skills", SkillName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	serveRelease(t, "0.9.0", makeTarball(t, map[string]string{"SKILL.md": "ok"}, true))

	res, err := SyncSkills(context.Background(), SyncOptions{TargetVersion: "0.9.0", Clients: []string{"codex"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Legit content still lands despite the malicious entry being skipped.
	if res.Changed != 1 {
		t.Fatalf("changed=%d want 1", res.Changed)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "SKILL.md")); string(b) != "ok" {
		t.Errorf("SKILL.md=%q want ok", b)
	}
	// The malicious ../evil.txt must NOT have escaped anywhere reachable.
	for _, p := range []string{filepath.Join(home, "evil.txt"), filepath.Join(dir, "evil.txt")} {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("path traversal escaped: %s was written", p)
		}
	}
}
