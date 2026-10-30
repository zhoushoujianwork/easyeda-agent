package app

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"github.com/zhoushoujianwork/easyeda-agent/internal/version"
)

// These are chapter routes, not a second copy of the engineering rules.
var skillStageChapters = map[string][]string{
	"requirements": {"references/design-flow.md", "references/design-decisions.md", "references/part-selection.md"},
	"schematic":    {"references/schematic-data.md", "references/auto-layout-sop.md", "references/schematic-placement.md", "references/schematic-wiring.md"},
	"pcb-init":     {"references/pcb-config.md", "references/pcb-layout.md", "references/pcb-design-rules.md"},
	"pcb-layout":   {"references/pcb-layout.md", "references/pcb-design-rules.md"},
	"pcb-routing":  {"references/pcb-routing.md", "references/pcb-design-rules.md"},
	"check":        {"references/pcb.md", "references/pcb-design-rules.md", "references/design-drawings.md"},
	"enclosure":    {"references/pcb.md", "references/pcb-layout.md"},
	"delivery":     {"references/design-drawings.md", "references/pcb-design-rules.md"},
}

type skillChapterRead struct {
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Content string `json:"content"`
}

type skillStageRead struct {
	SchemaVersion int                `json:"schemaVersion"`
	Stage         string             `json:"stage"`
	CLIVersion    string             `json:"cliVersion"`
	SkillVersion  string             `json:"skillVersion"`
	SourceRoot    string             `json:"sourceRoot"`
	SHA256        string             `json:"sha256"`
	Chapters      []skillChapterRead `json:"chapters"`
	Limitation    string             `json:"limitation"`
}

func newSkillReadCmd(stdout io.Writer) *cobra.Command {
	var stage, root string
	c := &cobra.Command{Use: "read", Short: "Read actual stage Skill chapters offline with source hashes (no EDA calls)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, ok := skillStageChapters[stage]; !ok {
				return fmt.Errorf("unknown Skill stage %q; use requirements|schematic|pcb-init|pcb-layout|pcb-routing|check|enclosure|delivery", stage)
			}
			if root == "" {
				entry, err := (skillAsset{name: "SKILL.md", rels: []string{"easyeda-agent/SKILL.md"}, flagHint: "--skill-root"}).resolve("")
				if err != nil {
					return err
				}
				root = filepath.Dir(entry)
			}
			result, err := readSkillStage(root, stage)
			if err != nil {
				return err
			}
			return writeJSON(stdout, result)
		}}
	c.Flags().StringVar(&stage, "stage", "", "required design stage; PCB uses pcb-init, pcb-layout and pcb-routing separately")
	c.Flags().StringVar(&root, "skill-root", "", "explicit local Skill directory; otherwise use the installed Skill resolver")
	return c
}

func readSkillStage(root, stage string) (*skillStageRead, error) {
	paths, ok := skillStageChapters[stage]
	if !ok {
		return nil, fmt.Errorf("unknown Skill stage %q", stage)
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("Skill root: %w", err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	report := &skillStageRead{SchemaVersion: 1, Stage: stage, CLIVersion: version.Version, SourceRoot: root,
		Limitation: "Chapters supplied to the caller; reading is not proof of rule execution or engineering acceptance. Follow linked references when relevant; unknown evidence stays unverified."}
	manifest := sha256.New()
	fmt.Fprintf(manifest, "%s\x00", stage)
	for _, path := range append([]string{"SKILL.md"}, paths...) {
		real, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			return nil, fmt.Errorf("required Skill chapter %s: %w", path, err)
		}
		rel, err := filepath.Rel(root, real)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return nil, fmt.Errorf("Skill chapter %s escapes the selected root", path)
		}
		f, err := os.Open(real)
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(f, 1024*1024+1))
		f.Close()
		if err != nil {
			return nil, err
		}
		if len(data) > 1024*1024 || len(strings.TrimSpace(string(data))) == 0 || !utf8.Valid(data) {
			return nil, fmt.Errorf("Skill chapter %s is empty, oversized or invalid UTF-8", path)
		}
		hash := sha256.Sum256(data)
		sha := hex.EncodeToString(hash[:])
		report.Chapters = append(report.Chapters, skillChapterRead{Path: path, SHA256: sha, Content: string(data)})
		fmt.Fprintf(manifest, "%s\x00%s\x00", path, sha)
	}
	match := regexp.MustCompile(`(?m)^  version: "([^"]+)"\s*$`).FindStringSubmatch(report.Chapters[0].Content)
	if len(match) != 2 {
		return nil, fmt.Errorf("Skill entry lacks metadata.version")
	}
	report.SkillVersion = match[1]
	report.SHA256 = hex.EncodeToString(manifest.Sum(nil))
	return report, nil
}
