// Package selfupdate keeps the locally-installed easyeda-agent skill directories
// (~/.claude/skills/easyeda-agent, ~/.codex/skills/easyeda-agent) in sync with a
// released version, so a user upgrading the CLI never has to hand-copy the skill.
//
// It deliberately does NOT touch the EasyEDA connector .eext: sideloaded
// extensions have no official in-place auto-update (that is a marketplace-only
// feature), so the daemon can only DETECT a stale connector and log an
// actionable re-import notice (see internal/daemon staleConnectorNotice).
package selfupdate

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	// RepoSlug is the GitHub owner/repo the release assets live under.
	RepoSlug = "zhoushoujianwork/easyeda-agent"
	// SkillName is the skill slug (dir name under each client's skills/).
	SkillName = "easyeda-agent"
	// versionMarker records the installed skill version inside a skill dir.
	versionMarker = ".version"
	// PreserveEnv, when "1", makes a sync keep existing files (local edits win).
	PreserveEnv = "EASYEDA_SKILL_PRESERVE"
)

// clientOrder is the deterministic client iteration order.
var clientOrder = []string{"claude", "codex"}

// Endpoint builders, overridable in tests to point at an httptest server.
var (
	tarballURL = func(version string) string {
		return fmt.Sprintf("https://github.com/%s/releases/download/v%s/skills.tar.gz", RepoSlug, version)
	}
	latestAPIURL = func() string {
		return fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", RepoSlug)
	}
)

// SkillTarget is one installed (or installable) skill location.
type SkillTarget struct {
	Client    string `json:"client"`    // "claude" | "codex"
	Dir       string `json:"dir"`       // absolute skill dir
	Present   bool   `json:"present"`   // dir exists on disk
	Installed string `json:"installed"` // version marker, "" if unknown/missing
}

// skillDir returns the skill dir for a client under $HOME, or "" if unknown.
func skillDir(client string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	switch client {
	case "claude":
		return filepath.Join(home, ".claude", "skills", SkillName)
	case "codex":
		return filepath.Join(home, ".codex", "skills", SkillName)
	}
	return ""
}

// Targets returns skill targets in deterministic order. When onlyPresent is true,
// only dirs that already exist on disk are returned (the daemon's default — never
// create a skill dir for a client the user doesn't use).
func Targets(onlyPresent bool) []SkillTarget {
	var out []SkillTarget
	for _, c := range clientOrder {
		dir := skillDir(c)
		if dir == "" {
			continue
		}
		present := isDir(dir)
		if onlyPresent && !present {
			continue
		}
		out = append(out, SkillTarget{
			Client:    c,
			Dir:       dir,
			Present:   present,
			Installed: readMarker(dir),
		})
	}
	return out
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func readMarker(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, versionMarker))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// LatestReleaseVersion queries the GitHub API for the newest release tag and
// returns its bare semver core (e.g. "0.9.0"). Best-effort: honors ctx deadline,
// returns an error on any network/parse failure so callers can skip silently.
func LatestReleaseVersion(ctx context.Context) (string, error) {
	url := latestAPIURL()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github releases/latest: %s", resp.Status)
	}
	var body struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", err
	}
	core := SemverCore(body.TagName)
	if core == "" {
		return "", fmt.Errorf("unparseable tag %q", body.TagName)
	}
	return core, nil
}

// SyncOptions configures SyncSkills.
type SyncOptions struct {
	// TargetVersion is the version to bring skill dirs up to (bare "x.y.z" or
	// "vx.y.z"). Required.
	TargetVersion string
	// Clients filters which clients to touch (nil = all present on disk).
	Clients []string
	// Preserve keeps existing files instead of overwriting (local edits win).
	Preserve bool
	// Force syncs even when a dir's marker already equals the target.
	Force bool
	// CreateMissing installs into a client dir even if it doesn't exist yet
	// (the manual `skill sync` default; the daemon leaves this false).
	CreateMissing bool
}

// TargetOutcome is the per-dir result of a sync.
type TargetOutcome struct {
	Client string `json:"client"`
	Dir    string `json:"dir"`
	From   string `json:"from"`   // installed version before
	To     string `json:"to"`     // target version
	Status string `json:"status"` // updated|up-to-date|created|skipped|error
	Err    string `json:"err,omitempty"`
}

// SyncResult is the full sync report.
type SyncResult struct {
	Target   string          `json:"target"`
	Outcomes []TargetOutcome `json:"outcomes"`
	Changed  int             `json:"changed"`
}

// SyncSkills brings the selected skill dirs up to TargetVersion by downloading
// that release's skills.tar.gz once and materializing it into each dir. logf may
// be nil. The download only happens if at least one dir actually needs it.
func SyncSkills(ctx context.Context, opts SyncOptions, logf func(string, ...any)) (SyncResult, error) {
	log := func(format string, a ...any) {
		if logf != nil {
			logf(format, a...)
		}
	}
	target := SemverCore(opts.TargetVersion)
	if target == "" {
		return SyncResult{}, fmt.Errorf("sync: bad target version %q", opts.TargetVersion)
	}
	res := SyncResult{Target: target}

	// Which clients?
	want := opts.Clients
	if len(want) == 0 {
		for _, c := range clientOrder {
			want = append(want, c)
		}
	}

	// Decide per-dir what needs doing before paying for a download.
	type job struct {
		client, dir, from string
		create            bool
	}
	var jobs []job
	for _, c := range want {
		dir := skillDir(c)
		if dir == "" {
			res.Outcomes = append(res.Outcomes, TargetOutcome{Client: c, Status: "error", Err: "unknown client"})
			continue
		}
		present := isDir(dir)
		from := readMarker(dir)
		if !present && !(opts.CreateMissing) {
			res.Outcomes = append(res.Outcomes, TargetOutcome{Client: c, Dir: dir, From: from, To: target, Status: "skipped", Err: "not installed"})
			continue
		}
		if present && !opts.Force && from == target {
			res.Outcomes = append(res.Outcomes, TargetOutcome{Client: c, Dir: dir, From: from, To: target, Status: "up-to-date"})
			continue
		}
		jobs = append(jobs, job{client: c, dir: dir, from: from, create: !present})
	}

	if len(jobs) == 0 {
		return res, nil
	}

	// Download + extract the release skill tree once into a temp dir.
	log("skill-sync: fetching skills.tar.gz for v%s", target)
	srcRoot, cleanup, err := fetchSkillTree(ctx, target)
	if err != nil {
		// Every pending job fails, but that's best-effort — report and return.
		for _, j := range jobs {
			res.Outcomes = append(res.Outcomes, TargetOutcome{Client: j.client, Dir: j.dir, From: j.from, To: target, Status: "error", Err: err.Error()})
		}
		return res, fmt.Errorf("fetch skills v%s: %w", target, err)
	}
	defer cleanup()

	for _, j := range jobs {
		status := "updated"
		if j.create {
			status = "created"
		}
		if err := materialize(srcRoot, j.dir, opts.Preserve); err != nil {
			res.Outcomes = append(res.Outcomes, TargetOutcome{Client: j.client, Dir: j.dir, From: j.from, To: target, Status: "error", Err: err.Error()})
			log("skill-sync: %s %s FAILED: %v", j.client, j.dir, err)
			continue
		}
		_ = os.WriteFile(filepath.Join(j.dir, versionMarker), []byte(target+"\n"), 0644)
		res.Outcomes = append(res.Outcomes, TargetOutcome{Client: j.client, Dir: j.dir, From: j.from, To: target, Status: status})
		res.Changed++
		fromLabel := j.from
		if fromLabel == "" {
			fromLabel = "?"
		}
		log("skill-sync: %s %s → %s (%s)", j.client, fromLabel, target, j.dir)
	}
	return res, nil
}

// fetchSkillTree downloads skills.tar.gz for the version and extracts it to a
// temp dir, returning the path to the extracted `easyeda-agent/` root plus a
// cleanup func.
func fetchSkillTree(ctx context.Context, version string) (root string, cleanup func(), err error) {
	url := tarballURL(version)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", func() {}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", func() {}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", func() {}, fmt.Errorf("download skills.tar.gz: %s", resp.Status)
	}

	tmp, err := os.MkdirTemp("", "easyeda-skill-*")
	if err != nil {
		return "", func() {}, err
	}
	cleanup = func() { _ = os.RemoveAll(tmp) }

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			cleanup()
			return "", func() {}, err
		}
		// Guard against path traversal; only accept entries under the skill dir.
		clean := filepath.Clean(hdr.Name)
		if clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) || filepath.IsAbs(clean) {
			continue
		}
		dst := filepath.Join(tmp, clean)
		if !strings.HasPrefix(dst, filepath.Clean(tmp)+string(os.PathSeparator)) {
			continue
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dst, 0755); err != nil {
				cleanup()
				return "", func() {}, err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
				cleanup()
				return "", func() {}, err
			}
			f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode)&0777|0600)
			if err != nil {
				cleanup()
				return "", func() {}, err
			}
			if _, err := io.Copy(f, io.LimitReader(tr, 64<<20)); err != nil {
				f.Close()
				cleanup()
				return "", func() {}, err
			}
			f.Close()
		}
	}

	root = filepath.Join(tmp, SkillName)
	if !isDir(root) {
		cleanup()
		return "", func() {}, fmt.Errorf("skills.tar.gz did not contain %s/", SkillName)
	}
	return root, cleanup, nil
}

// materialize copies the extracted skill tree onto dst. When preserve is true,
// existing files are kept (not overwritten); otherwise files are overwritten.
// Removed-in-new files are left in place (a safety-net sync never deletes).
func materialize(src, dst string, preserve bool) error {
	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		if preserve {
			if _, err := os.Stat(target); err == nil {
				return nil // keep existing
			}
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode&0777|0600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// PreserveFromEnv reports whether EASYEDA_SKILL_PRESERVE requests preserve mode.
func PreserveFromEnv() bool {
	return os.Getenv(PreserveEnv) == "1"
}

// ── semver helpers (self-contained; mirror internal/daemon) ─────────────────

// SemverCore extracts the "x.y.z" core from a version string, dropping a leading
// 'v' and any "-suffix". Returns "" if not x.y.z.
func SemverCore(v string) string {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexByte(v, '-'); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return ""
	}
	for _, p := range parts {
		if p == "" {
			return ""
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return ""
			}
		}
	}
	return v
}

// SemverLess reports whether core a < b. Empty is lower than any real version.
func SemverLess(a, b string) bool {
	a, b = SemverCore(a), SemverCore(b)
	if a == b {
		return false
	}
	if a == "" {
		return true
	}
	if b == "" {
		return false
	}
	ap, bp := strings.Split(a, "."), strings.Split(b, ".")
	for i := range 3 {
		x, _ := strconv.Atoi(ap[i])
		y, _ := strconv.Atoi(bp[i])
		if x != y {
			return x < y
		}
	}
	return false
}

// IsCleanRelease reports whether v is a bare release tag (vX.Y.Z, no suffix).
func IsCleanRelease(v string) bool {
	core := SemverCore(v)
	return core != "" && core == strings.TrimPrefix(strings.TrimSpace(v), "v")
}

// StartupSync is the daemon's best-effort skill refresh, run in the background on
// `daemon start`. It brings every ALREADY-PRESENT skill dir up to the latest
// release (never creates a new one), logging each step via logf, then — if the
// running CLI/daemon is itself behind the latest — logs an actionable nudge to
// re-run install.sh (which also re-imports the connector). It never blocks the
// daemon and never fails hard: any network hiccup just skips this cycle.
func StartupSync(ctx context.Context, daemonVersion string, logf func(string, ...any)) {
	log := func(format string, a ...any) {
		if logf != nil {
			logf(format, a...)
		}
	}
	present := Targets(true)
	if len(present) == 0 {
		return // no installed skill dirs — nothing to keep in sync
	}
	latest, err := LatestReleaseVersion(ctx)
	if err != nil {
		log("skill-sync: skipped this cycle (cannot reach GitHub: %v)", err)
		return
	}

	// Sync present dirs to latest. SyncSkills only downloads if something is behind.
	res, err := SyncSkills(ctx, SyncOptions{
		TargetVersion: latest,
		Preserve:      PreserveFromEnv(),
	}, log)
	if err == nil && res.Changed == 0 {
		// Quiet steady-state: everything already current.
	} else if err != nil {
		log("skill-sync: %v", err)
	}
	if res.Changed > 0 {
		log("skill-sync: updated %d skill dir(s) to v%s", res.Changed, latest)
	}

	// Nudge the full-suite upgrade when the CLI/connector lag the latest release.
	if IsCleanRelease(daemonVersion) && SemverLess(daemonVersion, latest) {
		log("update available: CLI v%s < latest v%s — run install.sh to upgrade the "+
			"CLI + connector (skill already synced): "+
			"curl -fsSL https://raw.githubusercontent.com/%s/main/install.sh | sh",
			SemverCore(daemonVersion), latest, RepoSlug)
	}
}
