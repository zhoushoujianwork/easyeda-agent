package selfupdate

// Local development packages are an explicit, offline trust boundary. They are
// built by the user, not authenticated releases; checksums detect corruption.
import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

var localVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-dev\.[1-9][0-9]*$`)

func IsLocalVersion(v string) bool {
	return localVersionPattern.MatchString(strings.TrimPrefix(v, "v"))
}

type LocalBundle struct {
	Version   string
	Binary    []byte
	Skill     map[string][]byte
	Connector string
}

// ReadLocalBundle snapshots validated bytes before any installation writes.
func ReadLocalBundle(dir string) (*LocalBundle, error) {
	asset, err := AssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "checksums.txt"))
	if err != nil {
		return nil, err
	}
	sums := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 || filepath.Base(f[1]) != f[1] || strings.ContainsAny(f[1], `/\`) {
			return nil, fmt.Errorf("invalid checksum entry")
		}
		if _, exists := sums[f[1]]; exists {
			return nil, fmt.Errorf("duplicate checksum entry")
		}
		if d, e := hex.DecodeString(f[0]); e != nil || len(d) != 32 {
			return nil, fmt.Errorf("invalid SHA-256")
		}
		sums[f[1]] = f[0]
	}
	read := func(name string) ([]byte, error) {
		b, e := os.ReadFile(filepath.Join(dir, name))
		if e != nil {
			return nil, e
		}
		if len(b) == 0 || fmt.Sprintf("%x", sha256.Sum256(b)) != sums[name] {
			return nil, fmt.Errorf("checksum mismatch: %s", name)
		}
		return b, nil
	}
	b := &LocalBundle{Skill: map[string][]byte{}, Connector: filepath.Join(dir, "easyeda-agent-connector.eext")}
	if b.Binary, err = read(asset); err != nil {
		return nil, err
	}
	connector, err := read("easyeda-agent-connector.eext")
	if err != nil {
		return nil, err
	}
	z, err := zip.NewReader(bytes.NewReader(connector), int64(len(connector)))
	if err != nil {
		return nil, err
	}
	compiled, manifests := false, 0
	for _, f := range z.File {
		if f.Name == "dist/index.js" && f.UncompressedSize64 > 0 {
			compiled = true
		}
		if f.Name != "extension.json" {
			continue
		}
		manifests++
		r, e := f.Open()
		if e != nil {
			return nil, e
		}
		var m struct {
			Version string `json:"version"`
		}
		e = json.NewDecoder(io.LimitReader(r, 1<<20)).Decode(&m)
		r.Close()
		if e != nil {
			return nil, e
		}
		b.Version = m.Version
	}
	if manifests != 1 || !compiled || !IsLocalVersion(b.Version) {
		return nil, fmt.Errorf("not a compiled X.Y.Z-dev.N connector package")
	}
	packed, err := read("skills.tar.gz")
	if err != nil {
		return nil, err
	}
	gz, err := gzip.NewReader(bytes.NewReader(packed))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var total int64
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
		name := strings.TrimPrefix(h.Name, SkillName+"/")
		if name == h.Name || name == "." || name == ".version" || strings.Contains(name, `\`) || filepath.IsAbs(name) || filepath.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") || h.Typeflag != tar.TypeReg {
			return nil, fmt.Errorf("unsafe skill entry: %s", h.Name)
		}
		if _, exists := b.Skill[name]; exists {
			return nil, fmt.Errorf("duplicate skill entry: %s", name)
		}
		total += h.Size
		if h.Size < 0 || h.Size > 64<<20 || total > 256<<20 {
			return nil, fmt.Errorf("skill package too large")
		}
		b.Skill[name], e = io.ReadAll(tr)
		if e != nil {
			return nil, e
		}
	}
	v, err := skillMetadataVersion(string(b.Skill["SKILL.md"]))
	if err != nil || v != b.Version {
		return nil, fmt.Errorf("Skill metadata does not match connector %s", b.Version)
	}
	return b, nil
}

func (b *LocalBundle) CheckBinary(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(data, b.Binary) {
		return fmt.Errorf("CLI bytes differ from local bundle")
	}
	return nil
}

func (b *LocalBundle) CheckSkill(dir string) error {
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	seen := map[string]bool{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		name, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		name = filepath.ToSlash(name)
		if !info.Mode().IsRegular() {
			return fmt.Errorf("nonregular Skill file: %s", name)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if name == ".version" {
			if strings.TrimSpace(string(data)) != b.Version {
				return fmt.Errorf("Skill version marker differs")
			}
		} else if want, ok := b.Skill[name]; !ok || !bytes.Equal(data, want) {
			return fmt.Errorf("Skill content differs: %s", name)
		}
		seen[name] = true
		return nil
	})
	if err != nil {
		return err
	}
	if len(seen) != len(b.Skill)+1 || !seen[".version"] {
		return fmt.Errorf("Skill files/marker missing")
	}
	return nil
}

// Install stages complete files and retains recoverable backups. No process
// restart, network access, or connector import occurs here.
func (b *LocalBundle) Install(binary string, log io.Writer) (retErr error) {
	if !filepath.IsAbs(binary) {
		return fmt.Errorf("--binary must be an absolute installation path")
	}
	if p, err := filepath.EvalSymlinks(binary); err == nil {
		binary = p
	}
	stage, err := os.CreateTemp(filepath.Dir(binary), ".easyeda-local-*")
	if err != nil {
		return err
	}
	stageName := stage.Name()
	defer os.Remove(stageName)
	if _, err = stage.Write(b.Binary); err != nil {
		stage.Close()
		return err
	}
	if err = stage.Close(); err != nil {
		return err
	}
	if err = os.Chmod(stageName, 0755); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, stageName, "--version").Output()
	if err != nil || strings.TrimSpace(string(output)) != "easyeda-agent v"+b.Version {
		return fmt.Errorf("local CLI execution/version check failed: %v", err)
	}
	if _, err = os.Stat(binary); err == nil {
		backup, e := os.CreateTemp(filepath.Dir(binary), ".easyeda-backup-*")
		if e != nil {
			return e
		}
		backup.Close()
		if e = copyFile(binary, backup.Name(), 0755); e != nil {
			return e
		}
		fmt.Fprintf(log, "CLI backup: %s\n", backup.Name())
	}
	if err = replaceBinary(stageName, binary); err != nil {
		return err
	}
	defer func() {
		if retErr != nil {
			retErr = fmt.Errorf("partial local installation: CLI was replaced; use printed backups to recover and restart the Agent session: %w", retErr)
		}
	}()
	if err := b.CheckBinary(binary); err != nil {
		return err
	}
	fmt.Fprintf(log, "Installed CLI: %s\n", binary)
	for _, target := range Targets(false) {
		if !target.Present {
			continue
		}
		dir := target.Dir
		if p, e := filepath.EvalSymlinks(dir); e == nil {
			dir = p
		}
		backup, e := os.MkdirTemp(filepath.Dir(dir), ".easyeda-local-backup-*")
		if e != nil {
			return e
		}
		if e = copyTree(dir, backup, false, false); e != nil {
			return e
		}
		fmt.Fprintf(log, "Skill backup: %s\n", backup)
		src, e := os.MkdirTemp(filepath.Dir(dir), ".easyeda-local-source-*")
		if e != nil {
			return e
		}
		e = func() error {
			defer os.RemoveAll(src)
			for name, data := range b.Skill {
				p := filepath.Join(src, name)
				if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
					return e
				}
				mode := os.FileMode(0644)
				if bytes.HasPrefix(data, []byte("#!")) {
					mode = 0755
				}
				if e := os.WriteFile(p, data, mode); e != nil {
					return e
				}
			}
			return materialize(src, dir, false, b.Version)
		}()
		if e != nil {
			return fmt.Errorf("Skill %s failed: %w", dir, e)
		}
		if e := b.CheckSkill(dir); e != nil {
			return e
		}
		fmt.Fprintf(log, "Installed Skill: %s\n", dir)
	}
	return nil
}
