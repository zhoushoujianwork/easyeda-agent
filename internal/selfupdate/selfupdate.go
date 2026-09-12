// Package selfupdate keeps the locally-installed easyeda-agent skill directories
// (~/.claude, ~/.codex and the shared ~/.agents skill roots) in sync with a
// released version, so a user upgrading the CLI never has to hand-copy the skill.
//
// It deliberately does NOT touch the EasyEDA connector .eext: sideloaded
// extensions have no official in-place auto-update (that is a marketplace-only
// feature), so the daemon can only DETECT a stale connector and log an
// actionable re-import notice (see internal/daemon staleConnectorNotice).
package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
var clientOrder = []string{"claude", "codex", "agents"}

// Endpoint builders, overridable in tests to point at an httptest server.
var (
	tarballURL = func(version string) string {
		return fmt.Sprintf("https://github.com/%s/releases/download/v%s/skills.tar.gz", RepoSlug, version)
	}
	latestAPIURL = func() string {
		return fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", RepoSlug)
	}
	latestWebURL = func() string {
		return fmt.Sprintf("https://github.com/%s/releases/latest", RepoSlug)
	}
)

// SkillTarget is one installed (or installable) skill location.
type SkillTarget struct {
	Client    string `json:"client"`    // "claude" | "codex" | "agents"
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
	var base string
	switch client {
	case "claude":
		base = os.Getenv("CLAUDE_CONFIG_DIR")
		if base == "" {
			base = filepath.Join(home, ".claude")
		}
	case "codex":
		base = os.Getenv("CODEX_HOME")
		if base == "" {
			base = filepath.Join(home, ".codex")
		}
	case "agents":
		base = filepath.Join(home, ".agents")
	default:
		return ""
	}
	if !filepath.IsAbs(base) {
		return ""
	}
	return filepath.Join(base, "skills", SkillName)
}

// ValidateClients rejects unsupported client names and relative client homes
// before any network or filesystem work. An empty selection means both clients.
func ValidateClients(clients []string) error {
	if len(clients) == 0 {
		clients = clientOrder
	}
	for _, client := range clients {
		if client != "codex" && client != "claude" && client != "agents" {
			return fmt.Errorf("unknown skill client %q (want codex, claude, or agents)", client)
		}
		if skillDir(client) == "" {
			return fmt.Errorf("invalid %s client home: CODEX_HOME/CLAUDE_CONFIG_DIR and user home must be absolute paths", client)
		}
	}
	return nil
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

// LatestReleaseVersion resolves the newest GitHub release tag. It authenticates
// the API request when GH_TOKEN/GITHUB_TOKEN is available, then falls back to
// GitHub's public releases/latest redirect when anonymous API quota is exhausted.
func LatestReleaseVersion(ctx context.Context) (string, error) {
	url := latestAPIURL()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token := githubToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			var body struct {
				TagName string `json:"tag_name"`
			}
			if decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); decodeErr == nil {
				if core := SemverCore(body.TagName); core != "" {
					return core, nil
				}
			}
		}
	}
	apiErr := err
	if apiErr == nil {
		apiErr = fmt.Errorf("github releases/latest: %s", resp.Status)
	}

	webReq, err := http.NewRequestWithContext(ctx, http.MethodGet, latestWebURL(), nil)
	if err != nil {
		return "", fmt.Errorf("%v; build latest-release fallback: %w", apiErr, err)
	}
	webResp, err := http.DefaultClient.Do(webReq)
	if err != nil {
		return "", fmt.Errorf("%v; github releases/latest fallback: %w", apiErr, err)
	}
	defer webResp.Body.Close()
	if webResp.StatusCode < 200 || webResp.StatusCode >= 400 {
		return "", fmt.Errorf("%v; github releases/latest fallback: %s", apiErr, webResp.Status)
	}
	tag := strings.TrimPrefix(webResp.Request.URL.Path, "/"+RepoSlug+"/releases/tag/")
	if core := SemverCore(tag); core != "" {
		return core, nil
	}
	return "", fmt.Errorf("%v; github releases/latest fallback had no release tag in %s", apiErr, webResp.Request.URL)
}

func githubToken() string {
	if token := strings.TrimSpace(os.Getenv("GH_TOKEN")); token != "" {
		return token
	}
	return strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
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
	Status string `json:"status"` // updated|up-to-date|created|preserved|skipped|error
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
			clientErr := ValidateClients([]string{c})
			res.Outcomes = append(res.Outcomes, TargetOutcome{Client: c, Status: "error", Err: clientErr.Error()})
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

	if len(jobs) == 0 || syncOutcomeError(res) != nil {
		return res, syncOutcomeError(res)
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
		} else if opts.Preserve {
			status = "preserved"
		}
		if err := materialize(srcRoot, j.dir, opts.Preserve, target); err != nil {
			res.Outcomes = append(res.Outcomes, TargetOutcome{Client: j.client, Dir: j.dir, From: j.from, To: target, Status: "error", Err: err.Error()})
			log("skill-sync: %s %s FAILED: %v", j.client, j.dir, err)
			continue
		}
		res.Outcomes = append(res.Outcomes, TargetOutcome{Client: j.client, Dir: j.dir, From: j.from, To: target, Status: status})
		res.Changed++
		fromLabel := j.from
		if fromLabel == "" {
			fromLabel = "?"
		}
		log("skill-sync: %s %s → %s (%s)", j.client, fromLabel, target, j.dir)
	}
	return res, syncOutcomeError(res)
}

func syncOutcomeError(res SyncResult) error {
	var failures []error
	for _, outcome := range res.Outcomes {
		if outcome.Status == "error" {
			failures = append(failures, fmt.Errorf("%s: %s", outcome.Client, outcome.Err))
		}
	}
	return errors.Join(failures...)
}

func downloadBytesWithFallback(ctx context.Context, primary string, limit int64, expected string, allowMirror bool) ([]byte, error) {
	download := func(url string) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
		}
		return io.ReadAll(io.LimitReader(resp.Body, limit))
	}
	var primaryErr error
	for range 3 {
		body, err := download(primary)
		if err == nil && (expected == "" || checksumHex(body) == strings.ToLower(expected)) {
			return body, nil
		}
		if err == nil {
			err = fmt.Errorf("checksum mismatch from GitHub")
		}
		primaryErr = err
	}
	if !allowMirror {
		return nil, primaryErr
	}
	mirror := githubProxyURL(primary)
	if mirror == "" {
		return nil, primaryErr
	}
	body, err := download(mirror)
	if err != nil {
		return nil, fmt.Errorf("GitHub download failed (%v); mirror failed: %w", primaryErr, err)
	}
	if expected != "" && checksumHex(body) != strings.ToLower(expected) {
		return nil, fmt.Errorf("GitHub download failed (%v); mirror checksum mismatch", primaryErr)
	}
	return body, nil
}

func checksumHex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// fetchSkillTree downloads skills.tar.gz for the version and extracts it to a
// temp dir, returning the path to the extracted `easyeda-agent/` root plus a
// cleanup func.
func fetchSkillTree(ctx context.Context, version string) (root string, cleanup func(), err error) {
	url := tarballURL(version)
	want, checksumErr := fetchChecksum(ctx, version, "skills.tar.gz")
	legacyNoChecksum := errors.Is(checksumErr, errChecksumUnavailable)
	if checksumErr != nil && !legacyNoChecksum {
		return "", func() {}, fmt.Errorf("verify skills.tar.gz: %w", checksumErr)
	}
	archive, err := downloadBytesWithFallback(ctx, url, (64<<20)+1, want, !legacyNoChecksum)
	if err != nil {
		return "", func() {}, fmt.Errorf("download skills.tar.gz: %w", err)
	}
	if len(archive) > 64<<20 {
		return "", func() {}, fmt.Errorf("skills.tar.gz exceeds 64 MiB")
	}

	tmp, err := os.MkdirTemp("", "easyeda-skill-*")
	if err != nil {
		return "", func() {}, err
	}
	cleanup = func() { _ = os.RemoveAll(tmp) }

	// Verify the entire archive before trusting or installing any extracted file.
	sum := sha256.Sum256(archive)
	if !legacyNoChecksum && !strings.EqualFold(want, hex.EncodeToString(sum[:])) {
		cleanup()
		return "", func() {}, fmt.Errorf("checksum mismatch for skills.tar.gz")
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var extractedBytes int64
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
			extractedBytes += hdr.Size
			if hdr.Size > 64<<20 || extractedBytes > 256<<20 {
				cleanup()
				return "", func() {}, fmt.Errorf("skills.tar.gz extracted content exceeds size limit")
			}
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
			if err := f.Close(); err != nil {
				cleanup()
				return "", func() {}, err
			}
		}
	}

	root = filepath.Join(tmp, SkillName)
	skill, readErr := os.ReadFile(filepath.Join(root, "SKILL.md"))
	if !isDir(root) || readErr != nil || len(strings.TrimSpace(string(skill))) == 0 {
		cleanup()
		return "", func() {}, fmt.Errorf("skills.tar.gz did not contain a nonempty %s/SKILL.md", SkillName)
	}
	declared, err := skillMetadataVersion(string(skill))
	if err != nil || (declared == "" && !(legacyNoChecksum && SemverLess(version, "1.4.0"))) || (declared != "" && declared != version) {
		cleanup()
		return "", func() {}, fmt.Errorf("SKILL.md metadata.version %q does not match target %s (parse error: %v)", declared, version, err)
	}
	return root, cleanup, nil
}

// skillMetadataVersion reads the published frontmatter contract: metadata is a
// top-level mapping and its version is an indented scalar. It does not scan
// prose or accept a similarly named field elsewhere in the document.
func skillMetadataVersion(body string) (string, error) {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", nil
	}
	inMetadata, seenMetadata, closed := false, false, false
	version := ""
	for _, line := range lines[1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			closed = true
			break
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			inMetadata = trimmed == "metadata:"
			if inMetadata {
				if seenMetadata {
					return "", fmt.Errorf("duplicate metadata mapping")
				}
				seenMetadata = true
			}
			continue
		}
		if inMetadata && strings.HasPrefix(line, "  version:") {
			if version != "" {
				return "", fmt.Errorf("duplicate metadata.version")
			}
			value := strings.TrimSpace(strings.TrimPrefix(line, "  version:"))
			value = strings.Trim(value, "\"'")
			if !IsCleanRelease(value) && !IsLocalVersion(value) {
				return "", fmt.Errorf("invalid metadata.version %q", value)
			}
			version = strings.TrimPrefix(value, "v")
		}
	}
	if !closed {
		return "", fmt.Errorf("unclosed YAML frontmatter")
	}
	return version, nil
}

// materialize prepares a complete sibling directory, then switches it into
// place. Failed copies never change the installation; a failed switch restores
// its backup. A normal sync removes files absent from the release. Preserve mode
// merges local content and keeps its old marker rather than claiming parity.
func materialize(src, dst string, preserve bool, version string) error {
	if resolved, err := filepath.EvalSymlinks(dst); err == nil {
		dst = resolved
	}
	parent := filepath.Dir(dst)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(parent, ".easyeda-skill-stage-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	existed := isDir(dst)
	if preserve && existed {
		if err := copyTree(dst, stage, false, false); err != nil {
			return err
		}
	}
	if err := copyTree(src, stage, preserve, true); err != nil {
		return err
	}
	if !preserve || !existed {
		if err := os.WriteFile(filepath.Join(stage, versionMarker), []byte(version+"\n"), 0644); err != nil {
			return err
		}
	}
	if err := os.Chmod(stage, 0755); err != nil {
		return err
	}
	if !existed {
		return os.Rename(stage, dst)
	}
	backup, err := os.MkdirTemp(parent, ".easyeda-skill-backup-*")
	if err != nil {
		return err
	}
	if err := os.Remove(backup); err != nil {
		return err
	}
	if err := os.Rename(dst, backup); err != nil {
		return err
	}
	if err := os.Rename(stage, dst); err != nil {
		if restoreErr := os.Rename(backup, dst); restoreErr != nil {
			return fmt.Errorf("switch skill: %w; restore failed: %v (original preserved at %s)", err, restoreErr, backup)
		}
		return err
	}
	return os.RemoveAll(backup)
}

func copyTree(src, dst string, preserve, skipMarker bool) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." || (skipMarker && rel == versionMarker) {
			return nil
		}
		target := filepath.Join(dst, rel)
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("skill tree contains unsupported symlink %s", path)
		}
		if info.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("skill tree contains non-regular file %s", path)
		}
		if preserve {
			if _, err := os.Stat(target); err == nil {
				return nil
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

// StartupSync aligns installed skills with the running released CLI. A pinned
// installation must remain pinned even when a newer release becomes available.
// Development builds skip automatic writes. Checking latest only prints a nudge.
func StartupSync(ctx context.Context, daemonVersion string, logf func(string, ...any)) {
	log := func(format string, a ...any) {
		if logf != nil {
			logf(format, a...)
		}
	}
	if !IsCleanRelease(daemonVersion) || len(Targets(true)) == 0 {
		return
	}
	target := SemverCore(daemonVersion)
	res, err := SyncSkills(ctx, SyncOptions{
		TargetVersion: target,
		Preserve:      PreserveFromEnv(),
	}, log)
	if err != nil {
		log("skill-sync: %v", err)
	}
	if res.Changed > 0 {
		log("skill-sync: refreshed %d skill dir(s) for CLI v%s", res.Changed, target)
	}
	latest, err := LatestReleaseVersion(ctx)
	if err == nil && SemverLess(target, latest) {
		log("update available: CLI v%s < latest v%s — run `easyeda update`, then restart the daemon; "+
			"the connector .eext still needs a manual re-import (`easyeda update --check` prints the URL)", target, latest)
	}
}
