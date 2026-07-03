package app

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
)

// newPcbStageSnapshotCmd builds `easyeda pcb stage-snapshot` — the recording /
// demo stage capture that issue #32 asked for.
//
// WHY a dedicated command: for engineering validation we judge state by DATA
// (list/drc/check), because a screenshot can be stale. But a recording/demo run
// ALSO needs a trustworthy native screenshot at each visual stage, plus the
// data that proves what the screenshot shows. This bundles both into one call
// and — crucially — GATES on the frame: if the capture is blank (window not
// rendering; see snapshot_frame.go) or stale (byte-identical to --previous),
// it exits non-zero so a `set -e` recording script stops instead of banking an
// empty canvas as "P7 routing done".
func newPcbStageSnapshotCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var (
		stage       string
		outDir      string
		noFit       bool
		previousSha string
		allowStale  bool
	)
	c := &cobra.Command{
		Use:   "stage-snapshot",
		Short: "Capture a recording/demo STAGE: native snapshot + data bundle, gated on a non-blank/non-stale frame",
		Long: `Capture one recording/demo STAGE in a single call:

  1. a native EasyEDA PCB snapshot  → <out>/<stage>/snapshot.png
  2. a data bundle (the proof)      → components/tracks/vias/pours/nets/drc .json
  3. a stage.json manifest with the frame verdict

Then GATE the frame: a BLANK capture (the EasyEDA window is not rendering this
document — minimized / behind other windows) or a STALE capture (byte-identical
to --previous-sha256) exits NON-ZERO, so a 'set -e' recording script halts
instead of saving an empty/duplicate frame as a finished stage. Pass
--allow-stale to downgrade a stale (but non-blank) frame to a warning.

No API call can force a hidden window to repaint — if this reports BLANK, bring
EasyEDA to the FOREGROUND on the target PCB and re-run.`,
		Args: cobra.NoArgs,
		Example: `  easyeda pcb stage-snapshot --project ceshi --stage "P7 routing"
  PREV=$(easyeda pcb stage-snapshot --stage P6 --out ./rec | jq -r .sha256)
  easyeda pcb stage-snapshot --stage P7 --out ./rec --previous-sha256 "$PREV"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(stage) == "" {
				return fmt.Errorf("--stage is required (e.g. --stage \"P7 routing\")")
			}
			dir := filepath.Join(outDirOrDefault(outDir), sanitizeStage(stage))
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("create stage dir %s: %w", dir, err)
			}

			// 1) native snapshot -------------------------------------------------
			payload := map[string]any{"fit": !noFit}
			if previousSha != "" {
				payload["previousSha256"] = previousSha
			}
			snap, err := requestAction(cfg, "pcb.snapshot", *window, payload)
			if err != nil {
				return err
			}
			// The capture is of the FOREGROUND tab's rendered area — it does NOT
			// switch documents. If that tab is not the PCB, the PNG is the wrong
			// document (e.g. a schematic) even though the command is `pcb ...`, and
			// the data bundle below would target a PCB that isn't in view. Refuse
			// rather than bank a mismatched frame.
			if snap.Context != nil && snap.Context.DocumentType != "" && snap.Context.DocumentType != "pcb" {
				fmt.Fprintf(stderr, "❌ stage %q: the foreground tab is a %s, not a PCB — the capture would be "+
					"the wrong document. Switch EasyEDA to the target PCB tab (`easyeda doc switch <pcb>`), "+
					"bring it to the FOREGROUND, and re-run.\n", stage, snap.Context.DocumentType)
				return errActionFailed
			}
			sha, _ := snap.Result["sha256"].(string)
			pngPath := ""
			if src := snapshotArtifact(snap); src != "" {
				pngPath = filepath.Join(dir, "snapshot.png")
				if err := copyFile(src, pngPath); err != nil {
					fmt.Fprintf(stderr, "⚠️  could not copy snapshot into stage dir: %v\n", err)
					pngPath = src // fall back to the original artifact path
				}
			}

			// analyze the frame for blankness (the "window not rendering" case)
			var frame frameStats
			blank := false
			if pngPath != "" {
				if fs, ferr := analyzeSnapshotFrame(pngPath); ferr == nil {
					frame = fs
					blank = fs.Blank
				}
			}
			stale := snapshotIsStale(snap)

			// 2) data bundle (the proof of what the screenshot shows) ------------
			bundle := []struct {
				file    string
				action  string
				payload map[string]any
			}{
				{"components.json", "pcb.components.list", map[string]any{"includeBBox": true}},
				{"tracks.json", "pcb.line.list", map[string]any{}},
				{"vias.json", "pcb.via.list", map[string]any{}},
				{"pours.json", "pcb.pour.list", map[string]any{}},
				{"nets.json", "pcb.nets.list", map[string]any{}},
				{"drc.json", "pcb.drc.check", map[string]any{}},
			}
			written := []string{}
			var drcResult map[string]any
			for _, b := range bundle {
				r, derr := requestAction(cfg, b.action, *window, b.payload)
				if derr != nil {
					fmt.Fprintf(stderr, "⚠️  %s: %v (skipped)\n", b.action, derr)
					continue
				}
				if b.action == "pcb.drc.check" {
					drcResult = r.Result
				}
				if werr := writeJSONFile(filepath.Join(dir, b.file), r.Result); werr != nil {
					fmt.Fprintf(stderr, "⚠️  write %s: %v\n", b.file, werr)
					continue
				}
				written = append(written, b.file)
			}
			drcPassed, drcKnown := drcPass(drcResult)

			// 3) stage manifest + verdict ---------------------------------------
			manifest := map[string]any{
				"stage":        stage,
				"sha256":       sha,
				"snapshot":     pngPath,
				"blank":        blank,
				"stale":        stale,
				"frameContent": frame.NonBgFraction,
				"frameColors":  frame.DistinctBins,
				"dataFiles":    written,
				"drcPassed":    drcPassed,
				"drcPassKnown": drcKnown,
			}
			_ = writeJSONFile(filepath.Join(dir, "stage.json"), manifest)

			// machine-readable verdict on stdout (jq-friendly)
			_ = json.NewEncoder(stdout).Encode(map[string]any{
				"stage":     stage,
				"dir":       dir,
				"sha256":    sha,
				"blank":     blank,
				"stale":     stale,
				"drcPassed": drcPassed,
			})

			// gate ---------------------------------------------------------------
			if blank {
				fmt.Fprintf(stderr, "❌ stage %q: BLANK frame (%.3f%% content, %d colors) — "+
					"EasyEDA is not rendering this PCB. Bring it to the FOREGROUND on the target "+
					"canvas and re-run. Not banking this stage.\n", stage, frame.NonBgFraction*100, frame.DistinctBins)
				return errActionFailed
			}
			if stale && !allowStale {
				fmt.Fprintf(stderr, "❌ stage %q: STALE frame (byte-identical to --previous-sha256). "+
					"The canvas did not repaint. Re-run (or pass --allow-stale to accept it).\n", stage)
				return errActionFailed
			}
			if stale {
				fmt.Fprintf(stderr, "⚠️  stage %q: stale frame accepted (--allow-stale).\n", stage)
			}
			if drcKnown && !drcPassed {
				fmt.Fprintf(stderr, "⚠️  stage %q: DRC not clean — recorded, but the board has violations.\n", stage)
			}
			fmt.Fprintf(stderr, "✓ stage %q saved → %s (%d data files, frame %.2f%% content)\n",
				stage, dir, len(written), frame.NonBgFraction*100)
			return nil
		},
	}
	c.Flags().StringVar(&stage, "stage", "", "stage label, e.g. \"P7 routing\" (required)")
	c.Flags().StringVar(&outDir, "out", "", "output root (default ./.easyeda/stages)")
	c.Flags().BoolVar(&noFit, "no-fit", false, "do NOT zoom to fit before capturing")
	c.Flags().StringVar(&previousSha, "previous-sha256", "", "prior stage's sha256 → detect+gate a stale (non-repainted) frame")
	c.Flags().BoolVar(&allowStale, "allow-stale", false, "downgrade a stale (but non-blank) frame from error to warning")
	return c
}

// outDirOrDefault resolves the stage output root.
func outDirOrDefault(out string) string {
	if strings.TrimSpace(out) != "" {
		return out
	}
	return filepath.Join(".easyeda", "stages")
}

var stageSanitizeRe = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// sanitizeStage turns a human stage label into a safe directory name.
func sanitizeStage(stage string) string {
	s := stageSanitizeRe.ReplaceAllString(strings.TrimSpace(stage), "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "stage"
	}
	return s
}

// drcPass extracts a pass/fail verdict from a pcb.drc.check result. The second
// return is false when the result shape does not expose a verdict we recognize.
func drcPass(res map[string]any) (passed bool, known bool) {
	if res == nil {
		return false, false
	}
	if v, ok := res["passed"].(bool); ok {
		return v, true
	}
	// Fall back to a zero violation count if "passed" is absent.
	for _, key := range []string{"count", "violationCount", "errorCount"} {
		if n, ok := res[key].(float64); ok {
			return n == 0, true
		}
	}
	return false, false
}

// writeJSONFile marshals v indented to path.
func writeJSONFile(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// copyFile copies src to dst (used to snapshot the artifact into the stage dir).
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
