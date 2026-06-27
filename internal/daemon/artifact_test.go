package daemon

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

func TestArtifactFileName(t *testing.T) {
	ts := time.Date(2026, 6, 27, 14, 30, 22, 0, time.Local)
	cases := []struct {
		a    protocol.Artifact
		want string
	}{
		{protocol.Artifact{ID: "art_1a2b3c4d5e6f", Kind: "schematic_snapshot", FileName: "snap.png"},
			"20260627-143022-schematic_snapshot-1a2b3c4d.png"},
		{protocol.Artifact{ID: "art_x", FileName: "bom.tsv"}, // short id, no kind
			"20260627-143022-x.tsv"},
		{protocol.Artifact{ID: "", Kind: "", FileName: "f.csv"}, // nothing but timestamp
			"20260627-143022.csv"},
	}
	for _, c := range cases {
		if got := artifactFileName(&c.a, ts); got != c.want {
			t.Errorf("artifactFileName(%+v) = %q, want %q", c.a, got, c.want)
		}
	}
}

func TestArtifactDir(t *testing.T) {
	s := &Server{opts: Options{ArtifactDir: "/cfg/dir"}}
	// CLI cwd wins → hidden subdir
	if got := s.artifactDir("/home/me/proj"); got != filepath.Join("/home/me/proj", ".easyeda", "artifacts") {
		t.Errorf("cwd case: %q", got)
	}
	// no cwd → configured dir
	if got := s.artifactDir(""); got != "/cfg/dir" {
		t.Errorf("configured case: %q", got)
	}
	// no cwd, no config → fallback
	if got := (&Server{}).artifactDir(""); got != "artifacts" {
		t.Errorf("fallback case: %q", got)
	}
}
