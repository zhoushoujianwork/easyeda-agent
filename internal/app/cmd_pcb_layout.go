package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcblayout"
	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcbmodel"
	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcbsolve"
)

func newPcbLayoutCmd(stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "layout",
		Short: "Offline PCB placement/routing coordination",
		Long: `Solve and independently check bounded two-layer placement/routing candidates.

All subcommands are offline: this command group has no app configuration or
window handle and cannot write the editor. Inputs use mil and footprint-anchor
coordinates. A four-layer count without explicit ordered layer roles is reported
as incomplete; the current solver supports TOP/BOTTOM only.`,
	}
	cmd.AddCommand(newPcbLayoutSolveCmd(stdout, stderr))
	cmd.AddCommand(newPcbLayoutCheckCmd(stdout, stderr))
	cmd.AddCommand(newPcbLayoutRenderCmd(stdout, stderr))
	cmd.AddCommand(newPcbLayoutSimulateCmd(stdout, stderr))
	return cmd
}

func newPcbLayoutSolveCmd(stdout, stderr io.Writer) *cobra.Command {
	var boardPath, fromPath, outPath, applyProject, applyDoc, applyWindow string
	cmd := &cobra.Command{
		Use:   "solve",
		Short: "Solve bounded two-layer placement and all declared routes offline",
		Long: `Jointly trial-route every declared connection, then displace complete groups using observed conflicts.

The --from JSON can declare layout.groups[].translationSearch with step and
maxDistance in mil to generate positions automatically. Existing offsets remain
supported. Automatic movement requires a complete demand graph for external
connections of the movable groups. Reports include attempted conflicts and
planned route/clearance reservations; these are not added to existing copper.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if boardPath == "" || fromPath == "" || outPath == "" {
				return fmt.Errorf("--board, --from and --out are required")
			}
			if (applyProject == "") != (applyDoc == "") {
				return fmt.Errorf("--apply-project and --apply-doc must be provided together")
			}
			if err := rejectLayoutOutputAliases([]string{boardPath, fromPath}, []string{outPath, solveSVGPath(outPath)}); err != nil {
				return err
			}
			requestRaw, err := os.ReadFile(fromPath)
			if err != nil {
				return fmt.Errorf("read solve request: %w", err)
			}
			var request pcbsolve.Request
			if err := decodeStrict(requestRaw, &request); err != nil {
				return fmt.Errorf("decode solve request: %w", err)
			}
			snap, boardRaw, err := readSolveBoard(boardPath)
			if err != nil {
				return err
			}
			board, adapterErr := boardSnapshotToModel(snap)
			if adapterErr != nil {
				report := pcbsolve.Report{SchemaVersion: 1, Status: pcbsolve.Incomplete, Reason: adapterErr.Error(), Budget: request.MaxStates}
				if writeErr := writeJSONAtomic(outPath, report); writeErr != nil {
					return writeErr
				}
				if writeErr := writeDiagnosticSVG(solveSVGPath(outPath), adapterErr.Error()); writeErr != nil {
					return writeErr
				}
				return fmt.Errorf("layout solve incomplete: %w", adapterErr)
			}
			_ = boardRaw // raw bytes are already bound by the verified semantic hash.
			report, solveErr := pcbsolve.Solve(context.Background(), board, request)
			if err := writeJSONAtomic(outPath, report); err != nil {
				return err
			}
			var applyMeta *playbookMeta
			if applyDoc != "" {
				applyMeta = &playbookMeta{Project: applyProject, Doc: applyDoc, Window: applyWindow}
			}
			if err := writeSolveArtifacts(outPath, board, request, report, applyMeta, []string{boardPath, fromPath}); err != nil {
				return err
			}
			fmt.Fprintf(stderr, "pcb layout solve: %s; %d candidate(s); %d/%d states; searchComplete=%t → %s\n", report.Status, len(report.Candidates), report.States, report.Budget, report.SearchComplete, outPath)
			fmt.Fprintln(stdout, outPath)
			if solveErr != nil {
				return solveErr
			}
			if report.Status != pcbsolve.Pass {
				return fmt.Errorf("layout solve %s: %s", report.Status, report.Reason)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&boardPath, "board", "", "fresh pcb dump --include-copper JSON")
	cmd.Flags().StringVar(&fromPath, "from", "", "strict schemaVersion 1 solve request JSON")
	cmd.Flags().StringVar(&outPath, "out", "", "solve report JSON; adjacent SVG/JSON candidate artifacts are generated")
	cmd.Flags().StringVar(&applyProject, "apply-project", "", "optional project bound into guarded layout-only apply playbooks")
	cmd.Flags().StringVar(&applyDoc, "apply-doc", "", "optional PCB document UUID bound into guarded layout-only apply playbooks")
	cmd.Flags().StringVar(&applyWindow, "apply-window", "", "optional EasyEDA window ID bound into guarded layout-only apply playbooks")
	return cmd
}

type pcbLayoutCheckReport struct {
	SchemaVersion     int    `json:"schemaVersion"`
	Status            string `json:"status"`
	Reason            string `json:"reason,omitempty"`
	CandidateID       string `json:"candidateId,omitempty"`
	BoardSemanticHash string `json:"boardSemanticHash,omitempty"`
	RequestHash       string `json:"requestHash,omitempty"`
}

func newPcbLayoutCheckCmd(stdout, stderr io.Writer) *cobra.Command {
	var boardPath, fromPath, candidatePath, candidateID, outPath string
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Independently reproject and verify one solve candidate",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if boardPath == "" || fromPath == "" || candidatePath == "" || outPath == "" {
				return fmt.Errorf("--board, --from, --candidate and --out are required")
			}
			if err := rejectLayoutOutputAliases([]string{boardPath, fromPath, candidatePath}, []string{outPath, solveSVGPath(outPath)}); err != nil {
				return err
			}
			requestRaw, err := os.ReadFile(fromPath)
			if err != nil {
				return err
			}
			var request pcbsolve.Request
			if err := decodeStrict(requestRaw, &request); err != nil {
				return fmt.Errorf("decode solve request: %w", err)
			}
			candidate, err := readSolveCandidate(candidatePath, candidateID)
			if err != nil {
				return err
			}
			snap, _, err := readSolveBoard(boardPath)
			if err != nil {
				return err
			}
			board, adapterErr := boardSnapshotToModel(snap)
			report := pcbLayoutCheckReport{SchemaVersion: 1, CandidateID: candidate.ID, RequestHash: candidate.RequestHash}
			var checkErr error
			if adapterErr != nil {
				report.Status, report.Reason, checkErr = pcbsolve.Incomplete, adapterErr.Error(), adapterErr
			} else {
				report.BoardSemanticHash = board.SemanticHash
				checkErr = pcbsolve.Check(context.Background(), board, request, candidate)
				if checkErr != nil {
					report.Status, report.Reason = pcbsolve.Fail, checkErr.Error()
				} else {
					report.Status = pcbsolve.Pass
				}
			}
			if err := writeJSONAtomic(outPath, report); err != nil {
				return err
			}
			if adapterErr == nil {
				var svg bytes.Buffer
				if err := renderPCBSolveRequestSVG(&svg, board, request, &candidate, report.Status+": "+report.Reason); err != nil {
					return err
				}
				if err := writeAtomic(solveSVGPath(outPath), svg.Bytes()); err != nil {
					return err
				}
			} else if err := writeDiagnosticSVG(solveSVGPath(outPath), report.Reason); err != nil {
				return err
			}
			fmt.Fprintf(stderr, "pcb layout check: %s → %s\n", report.Status, outPath)
			fmt.Fprintln(stdout, outPath)
			if checkErr != nil {
				return fmt.Errorf("layout check %s: %w", report.Status, checkErr)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&boardPath, "board", "", "fresh pcb dump --include-copper JSON")
	cmd.Flags().StringVar(&fromPath, "from", "", "the independent solve request JSON")
	cmd.Flags().StringVar(&candidatePath, "candidate", "", "candidate JSON or solve report JSON")
	cmd.Flags().StringVar(&candidateID, "candidate-id", "", "candidate id when --candidate is a multi-candidate solve report")
	cmd.Flags().StringVar(&outPath, "out", "", "independent check report JSON; adjacent SVG is generated")
	return cmd
}

func newPcbLayoutRenderCmd(stdout, stderr io.Writer) *cobra.Command {
	var boardPath, fromPath, candidatePath, candidateID, outPath string
	var actual bool
	var side string
	cmd := &cobra.Command{
		Use:   "render",
		Short: "Render a solve candidate or an actual PCB readback as SVG",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if actual {
				if cmd.Flags().Changed("from") || cmd.Flags().Changed("candidate") || cmd.Flags().Changed("candidate-id") {
					return fmt.Errorf("--actual cannot be combined with --from, --candidate or --candidate-id")
				}
				if boardPath == "" || outPath == "" {
					return fmt.Errorf("--actual requires --board and --out")
				}
				if err := rejectLayoutOutputAliases([]string{boardPath}, []string{outPath}); err != nil {
					return err
				}
				raw, err := os.ReadFile(boardPath)
				if err != nil {
					return fmt.Errorf("read board snapshot: %w", err)
				}
				var svg bytes.Buffer
				if err := renderPCBActualSVG(&svg, raw, side); err != nil {
					return err
				}
				if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
					return err
				}
				if err := writeAtomic(outPath, svg.Bytes()); err != nil {
					return err
				}
				fmt.Fprintf(stderr, "pcb layout render: actual readback (%s; auxiliary, not official EDA capture) → %s\n", side, outPath)
				fmt.Fprintln(stdout, outPath)
				return nil
			}
			if cmd.Flags().Changed("side") {
				return fmt.Errorf("--side requires --actual; candidate rendering is unchanged")
			}
			if boardPath == "" || candidatePath == "" || outPath == "" {
				return fmt.Errorf("--board, --candidate and --out are required")
			}
			inputs := []string{boardPath, candidatePath}
			if fromPath != "" {
				inputs = append(inputs, fromPath)
			}
			if err := rejectLayoutOutputAliases(inputs, []string{outPath}); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
				return err
			}
			candidate, err := readSolveCandidate(candidatePath, candidateID)
			if err != nil {
				return err
			}
			snap, _, err := readSolveBoard(boardPath)
			if err != nil {
				return err
			}
			board, err := boardSnapshotToModel(snap)
			if err != nil {
				return err
			}
			var svg bytes.Buffer
			note := strings.Join(candidate.MoveReasons, "; ")
			if fromPath != "" {
				raw, err := os.ReadFile(fromPath)
				if err != nil {
					return err
				}
				var request pcbsolve.Request
				if err := decodeStrict(raw, &request); err != nil {
					return fmt.Errorf("decode solve request: %w", err)
				}
				canonical, err := json.Marshal(request)
				if err != nil {
					return err
				}
				if candidate.RequestHash != sha256String(canonical) || candidate.BaseSemanticHash != board.SemanticHash {
					return fmt.Errorf("candidate provenance does not match the render board/request")
				}
				if err := renderPCBSolveRequestSVG(&svg, board, request, &candidate, note); err != nil {
					return err
				}
			} else if err := renderPCBSolveSVG(&svg, board, &candidate, note); err != nil {
				return err
			}
			if err := writeAtomic(outPath, svg.Bytes()); err != nil {
				return err
			}
			fmt.Fprintf(stderr, "pcb layout render: %s → %s\n", candidate.ID, outPath)
			fmt.Fprintln(stdout, outPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&boardPath, "board", "", "fresh pcb dump --include-copper JSON")
	cmd.Flags().StringVar(&fromPath, "from", "", "optional independent solve request JSON; checks provenance and renders mechanical keepouts")
	cmd.Flags().StringVar(&candidatePath, "candidate", "", "candidate JSON or solve report JSON")
	cmd.Flags().StringVar(&candidateID, "candidate-id", "", "candidate id when --candidate is a multi-candidate solve report")
	cmd.Flags().StringVar(&outPath, "out", "", "SVG output path")
	cmd.Flags().BoolVar(&actual, "actual", false, "render actual readback only; no candidate, solver, repositioning or live editor access")
	cmd.Flags().StringVar(&side, "side", "both", "actual rendering: top, bottom (mirrored assembly view), or both")
	return cmd
}

func readSolveBoard(path string) (*boardSnapshot, []byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read board snapshot: %w", err)
	}
	snap, err := loadBoardSnapshotFile(bytes.NewReader(raw))
	if err != nil {
		return nil, raw, err
	}
	return snap, raw, nil
}

func readSolveCandidate(path, id string) (pcbsolve.Candidate, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return pcbsolve.Candidate{}, fmt.Errorf("read candidate: %w", err)
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return pcbsolve.Candidate{}, fmt.Errorf("decode candidate: %w", err)
	}
	if _, ok := probe["candidates"]; !ok {
		var candidate pcbsolve.Candidate
		if err := decodeStrict(raw, &candidate); err != nil {
			return pcbsolve.Candidate{}, fmt.Errorf("decode candidate: %w", err)
		}
		if id != "" && candidate.ID != id {
			return pcbsolve.Candidate{}, fmt.Errorf("candidate id %q is not present", id)
		}
		return candidate, nil
	}
	var report pcbsolve.Report
	if err := decodeStrict(raw, &report); err != nil {
		return pcbsolve.Candidate{}, fmt.Errorf("decode solve report: %w", err)
	}
	if id == "" && len(report.Candidates) != 1 {
		return pcbsolve.Candidate{}, fmt.Errorf("solve report contains %d candidates; select one with --candidate-id", len(report.Candidates))
	}
	for _, candidate := range report.Candidates {
		if id == "" || candidate.ID == id {
			return candidate, nil
		}
	}
	return pcbsolve.Candidate{}, fmt.Errorf("candidate id %q is not present", id)
}

func decodeStrict(raw []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

func writeJSONAtomic(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeAtomic(path, append(raw, '\n'))
}

func solveSVGPath(path string) string { return strings.TrimSuffix(path, filepath.Ext(path)) + ".svg" }

func writeSolveArtifacts(outPath string, board pcbmodel.Board, request pcbsolve.Request, report pcbsolve.Report, applyMeta *playbookMeta, inputPaths []string) error {
	stem := strings.TrimSuffix(outPath, filepath.Ext(outPath))
	outputs := []string{solveSVGPath(outPath), stem + ".local.svg", stem + ".baseline.svg", stem + ".baseline.local.svg"}
	for i := range report.Attempts {
		outputs = append(outputs, fmt.Sprintf("%s.attempt-%02d.svg", stem, i+1), fmt.Sprintf("%s.attempt-%02d.local.svg", stem, i+1))
	}
	for _, candidate := range report.Candidates {
		outputs = append(outputs, fmt.Sprintf("%s.%s.json", stem, candidate.ID), fmt.Sprintf("%s.%s.svg", stem, candidate.ID), fmt.Sprintf("%s.%s.local.svg", stem, candidate.ID))
		if applyMeta != nil {
			applyPath := fmt.Sprintf("%s.%s.layout.apply.json", stem, candidate.ID)
			outputs = append(outputs, applyPath, strings.TrimSuffix(applyPath, ".json")+".unsupported.json")
		}
	}
	if err := rejectLayoutOutputAliases(inputPaths, outputs); err != nil {
		return err
	}
	var baseline bytes.Buffer
	if err := renderPCBSolveRequestSVG(&baseline, board, request, nil, "baseline before placement/routing feedback"); err != nil {
		return err
	}
	if err := writeAtomic(stem+".baseline.svg", baseline.Bytes()); err != nil {
		return err
	}
	var baselineLocal bytes.Buffer
	if err := renderPCBSolveLocalSVG(&baselineLocal, board, request, nil, "baseline before placement/routing feedback"); err != nil {
		return err
	}
	if err := writeAtomic(stem+".baseline.local.svg", baselineLocal.Bytes()); err != nil {
		return err
	}
	layouts := map[string]pcbmodel.Board{}
	for _, c := range report.Layout.Candidates {
		layouts[c.ID] = c.Board
	}
	for i, attempt := range report.Attempts {
		attemptBoard, ok := layouts[attempt.LayoutID]
		if !ok {
			continue
		}
		note := attempt.Status + ": " + attempt.Reason
		if len(attempt.Blockers) > 0 {
			note += "; blockers=" + strings.Join(attempt.Blockers, ",")
		}
		fake := pcbsolve.Candidate{ID: attempt.LayoutID, Board: attemptBoard, Routes: attempt.PartialRoutes, MoveReasons: []string{note}}
		var svg bytes.Buffer
		if err := renderPCBSolveRequestSVG(&svg, board, request, &fake, note, attempt.Conflicts...); err != nil {
			return err
		}
		if err := writeAtomic(fmt.Sprintf("%s.attempt-%02d.svg", stem, i+1), svg.Bytes()); err != nil {
			return err
		}
		var local bytes.Buffer
		if err := renderPCBSolveLocalSVG(&local, board, request, &fake, note, attempt.Conflicts...); err != nil {
			return err
		}
		if err := writeAtomic(fmt.Sprintf("%s.attempt-%02d.local.svg", stem, i+1), local.Bytes()); err != nil {
			return err
		}
	}
	for i := range report.Candidates {
		candidate := report.Candidates[i]
		if err := writeJSONAtomic(fmt.Sprintf("%s.%s.json", stem, candidate.ID), candidate); err != nil {
			return err
		}
		var svg bytes.Buffer
		if err := renderPCBSolveRequestSVG(&svg, board, request, &candidate, strings.Join(candidate.MoveReasons, "; ")); err != nil {
			return err
		}
		if err := writeAtomic(fmt.Sprintf("%s.%s.svg", stem, candidate.ID), svg.Bytes()); err != nil {
			return err
		}
		var local bytes.Buffer
		if err := renderPCBSolveLocalSVG(&local, board, request, &candidate, strings.Join(candidate.MoveReasons, "; ")); err != nil {
			return err
		}
		if err := writeAtomic(fmt.Sprintf("%s.%s.local.svg", stem, candidate.ID), local.Bytes()); err != nil {
			return err
		}
		if applyMeta != nil {
			playbookPath := fmt.Sprintf("%s.%s.layout.apply.json", stem, candidate.ID)
			pb, err := buildPCBSolveLayoutPlaybook(board, request, candidate, *applyMeta)
			if err != nil {
				if err := writeJSONAtomic(strings.TrimSuffix(playbookPath, ".json")+".unsupported.json", map[string]any{"status": "unsupported", "reason": err.Error(), "candidateId": candidate.ID}); err != nil {
					return err
				}
			} else if err := writeJSONAtomic(playbookPath, pb); err != nil {
				return err
			}
		}
	}
	main, mainLocal := baseline.Bytes(), baselineLocal.Bytes()
	if len(report.Candidates) > 0 {
		var svg bytes.Buffer
		if err := renderPCBSolveRequestSVG(&svg, board, request, &report.Candidates[0], strings.Join(report.Candidates[0].MoveReasons, "; ")); err != nil {
			return err
		}
		main = svg.Bytes()
		var local bytes.Buffer
		if err := renderPCBSolveLocalSVG(&local, board, request, &report.Candidates[0], strings.Join(report.Candidates[0].MoveReasons, "; ")); err != nil {
			return err
		}
		mainLocal = local.Bytes()
	}
	if err := writeAtomic(solveSVGPath(outPath), main); err != nil {
		return err
	}
	return writeAtomic(stem+".local.svg", mainLocal)
}

func buildPCBSolveLayoutPlaybook(baseline pcbmodel.Board, request pcbsolve.Request, candidate pcbsolve.Candidate, meta playbookMeta) (playbook, error) {
	groupByID := map[string]pcblayout.Group{}
	for _, group := range request.Layout.Groups {
		groupByID[group.ID] = group
	}
	refs := map[string]bool{}
	for _, move := range candidate.Moves {
		group, ok := groupByID[move.Group]
		if !ok {
			return playbook{}, fmt.Errorf("candidate move names unknown group %s", move.Group)
		}
		if len(group.InternalTrackIDs)+len(group.InternalViaIDs)+len(group.InternalRegionIDs) > 0 {
			return playbook{}, fmt.Errorf("group %s moves explicit internal copper; typed replacement playbook is not implemented for this copper inventory", group.ID)
		}
		for _, ref := range move.Refs {
			refs[ref] = true
		}
	}
	orderedRefs := make([]string, 0, len(refs))
	for ref := range refs {
		orderedRefs = append(orderedRefs, ref)
	}
	sort.Strings(orderedRefs)
	zero, stop := 0, false
	meta.Name = "PCB two-layer candidate layout " + candidate.ID
	meta.Description = fmt.Sprintf("Generated from pcbsolve candidate %s; boardSemanticHash=%s; requestHash=%s; layout only, planned routes are not written before user confirmation", candidate.ID, candidate.BaseSemanticHash, candidate.RequestHash)
	pb := playbook{Version: 1, RequireFullExecution: true, Meta: meta, Defaults: stepPolicy{Retry: &zero, ContinueOnError: &stop}}
	for _, ref := range orderedRefs {
		before, okBefore := baseline.Component(ref)
		after, okAfter := candidate.Board.Component(ref)
		if !okBefore || !okAfter || before.ID == "" || before.ID != after.ID {
			return playbook{}, fmt.Errorf("component %s lacks stable primitive identity", ref)
		}
		payload := map[string]any{"primitiveId": before.ID, "patch": map[string]any{"x": after.Anchor[0], "y": after.Anchor[1], "rotation": after.Rotation}}
		pb.Steps = append(pb.Steps, playbookStep{ID: "place-" + ref, Name: "place " + ref, Action: "pcb.component.modify", Payload: payload})
	}
	pb.Steps = append(pb.Steps, playbookStep{ID: "save-layout", Name: "save PCB layout checkpoint", Action: "pcb.save", Payload: map[string]any{}})
	return pb, nil
}

func writeDiagnosticSVG(path, reason string) error {
	content := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 800 90"><rect width="800" height="90" fill="#fff7ed"/><text x="18" y="38" font-family="ui-monospace,monospace" font-size="14" fill="#9a3412">Incomplete: %s</text></svg>`, htmlEscape(reason))
	return writeAtomic(path, []byte(content))
}

func htmlEscape(value string) string {
	value = strings.ReplaceAll(value, "&", "&amp;")
	value = strings.ReplaceAll(value, "<", "&lt;")
	value = strings.ReplaceAll(value, ">", "&gt;")
	value = strings.ReplaceAll(value, `"`, "&quot;")
	return value
}

func rejectLayoutOutputAliases(inputs, outputs []string) error {
	for _, output := range outputs {
		outAbs, _ := filepath.Abs(output)
		for _, input := range inputs {
			inAbs, _ := filepath.Abs(input)
			if outAbs == inAbs {
				return fmt.Errorf("output must not overwrite input %s", input)
			}
			outInfo, outErr := os.Stat(output)
			inInfo, inErr := os.Stat(input)
			if outErr == nil && inErr == nil && os.SameFile(outInfo, inInfo) {
				return fmt.Errorf("output must not overwrite input %s", input)
			}
		}
	}
	return nil
}
