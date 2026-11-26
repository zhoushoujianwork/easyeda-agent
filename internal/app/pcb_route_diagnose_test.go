package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// In routabilityPocketFixture the J1 signal pads are J1.1..J1.5 on nets
// SIG1..SIG5. A 30 mil VBAT track at y=186 on BOTTOM closes the pocket under
// J1.2..J1.4, so those three pads are trapped (see the routability tests).
func routeDiagnoseTrappedBoard() *boardSnapshot {
	s := routabilityPocketFixture()
	s.Copper.Lines = []any{map[string]any{"primitiveId": "trk-vbat", "net": "VBAT", "layer": float64(1), "startX": 140.0, "startY": 186.0, "endX": 260.0, "endY": 186.0, "lineWidth": 30.0}}
	return s
}

func connectionRow(net string, objs ...string) drcFlatViolation {
	return drcFlatViolation{Rule: pcbDrcConnectionRule, ObjType: "SMD Pad", Net: net, Objs: objs}
}

func defaultDiagnoseOptions(nets ...string) pcbRouteDiagnoseOptions {
	return pcbRouteDiagnoseOptions{StepMil: pcbRoutabilityDefaultStepMil, RadiusMil: pcbRoutabilityDefaultRadiusMil, Nets: nets}
}

func diagnoseNet(t *testing.T, rep pcbRouteDiagnoseReport, net string) pcbRouteDiagnoseNet {
	t.Helper()
	for _, n := range rep.Nets {
		if n.Net == net {
			return n
		}
	}
	t.Fatalf("net %s missing from report: %+v", net, rep.Nets)
	return pcbRouteDiagnoseNet{}
}

func diagnoseBlocker(n pcbRouteDiagnoseNet, kind, label string) bool {
	for _, b := range n.Blockers {
		if b.Kind == kind && (b.Ref == label || b.ID == label) {
			return true
		}
	}
	return false
}

func TestRouteDiagnoseTrappedNetListsPadsAndBlockers(t *testing.T) {
	drc := drcFlatReport{Violations: []drcFlatViolation{connectionRow("SIG3", "pad-a", "pad-b"), connectionRow("SIG2", "pad-c")}}
	rep, err := diagnosePCBRoutes(routeDiagnoseTrappedBoard(), drc, defaultDiagnoseOptions())
	if err == nil || rep.Status != "fail" {
		t.Fatalf("trapped nets must fail: status=%s err=%v", rep.Status, err)
	}
	if len(rep.Nets) != 2 || rep.Nets[0].Net != "SIG2" || rep.Nets[1].Net != "SIG3" {
		t.Fatalf("nets must be sorted by name: %+v", rep.Nets)
	}
	n := diagnoseNet(t, rep, "SIG3")
	if n.Reason != pcbRouteDiagnoseTrapped || len(n.Pads) != 1 || n.Pads[0].Pad != "J1.3" || n.Pads[0].Status != "trapped" {
		t.Fatalf("SIG3 must be trapped at J1.3: %+v", n)
	}
	for _, want := range [][2]string{{"track", "trk-vbat"}, {"pad", "C1.1"}, {"pad", "D1.1"}} {
		if !diagnoseBlocker(n, want[0], want[1]) {
			t.Fatalf("SIG3 blockers miss %s %s: %+v", want[0], want[1], n.Blockers)
		}
	}
	// The union of the trapped pads' blockers, not a copy of one pad's list.
	if len(n.Blockers) < len(n.Pads[0].Blockers) {
		t.Fatalf("blockers lost entries: %d < %d", len(n.Blockers), len(n.Pads[0].Blockers))
	}
	if n.Rows != 1 || strings.Join(n.DrcObjs, ",") != "pad-a,pad-b" {
		t.Fatalf("rows/drcObjs wrong: %+v", n)
	}
	if rep.BoardSemanticSHA256 == "" || rep.Parameters.ViaDrillMil != 12 || rep.Parameters.StepMil != pcbRoutabilityDefaultStepMil {
		t.Fatalf("report lacks hash/parameters: %+v", rep)
	}
	// Same verdict and pads as the routability report for the same board.
	rb, _ := analyzePCBRoutability(routeDiagnoseTrappedBoard(), defaultRoutabilityOptions())
	if got := strings.Join(rb.Trapped, ","); got != "J1.2,J1.3,J1.4" || diagnoseNet(t, rep, "SIG2").Pads[0].Pad != "J1.2" {
		t.Fatalf("diagnose disagrees with routability: %s", got)
	}
}

// Every pad of the net can leave, so the break is between pads: not trapped.
func TestRouteDiagnosePadsEscapeIsNotLocallyTrapped(t *testing.T) {
	drc := drcFlatReport{Violations: []drcFlatViolation{connectionRow("SIG3", "pad-a")}}
	rep, err := diagnosePCBRoutes(routabilityPocketFixture(), drc, defaultDiagnoseOptions())
	if err == nil || rep.Status != "fail" {
		t.Fatalf("an unrouted net still fails: status=%s err=%v", rep.Status, err)
	}
	n := diagnoseNet(t, rep, "SIG3")
	if n.Reason != pcbRouteDiagnoseOpen || len(n.Blockers) != 0 || n.PadsChecked != 1 || len(n.Pads) != 1 || n.Pads[0].Status != "pass" {
		t.Fatalf("SIG3 must be not-locally-trapped with its escaping pad listed: %+v", n)
	}
	if n.Pads[0].ViaSites == 0 {
		t.Fatalf("the pocket pad should escape through a via site: %+v", n.Pads[0])
	}
	if !strings.Contains(strings.Join(rep.Limitations, "\n"), "does not say which channel") {
		t.Fatalf("limitations must say not-locally-trapped names no channel: %v", rep.Limitations)
	}
}

// The reason must follow the geometry: releasing the blocker flips trapped to
// not-locally-trapped. Breaking the classification fails this test.
func TestRouteDiagnoseReasonFollowsBlocker(t *testing.T) {
	drc := drcFlatReport{Violations: []drcFlatViolation{connectionRow("SIG3")}}
	rep, _ := diagnosePCBRoutes(routeDiagnoseTrappedBoard(), drc, defaultDiagnoseOptions())
	if got := diagnoseNet(t, rep, "SIG3").Reason; got != pcbRouteDiagnoseTrapped {
		t.Fatalf("blocked board: reason=%s", got)
	}
	released := routeDiagnoseTrappedBoard()
	released.Copper.Lines = []any{}
	rep, _ = diagnosePCBRoutes(released, drc, defaultDiagnoseOptions())
	if got := diagnoseNet(t, rep, "SIG3").Reason; got != pcbRouteDiagnoseOpen {
		t.Fatalf("released board: reason=%s", got)
	}
}

func TestRouteDiagnoseUnverifiedPadIsUnknownNotPass(t *testing.T) {
	s := routabilityPocketFixture()
	// On a 4-layer board a same-net via in the pocket is an unproven transition.
	s.Copper.Vias = []any{map[string]any{"primitiveId": "via-sig3", "net": "SIG3", "x": 200.0, "y": 186.0, "holeDiameter": 12.0, "diameter": 24.0}}
	drc := drcFlatReport{Violations: []drcFlatViolation{connectionRow("SIG3", "via-sig3")}}
	rep, err := diagnosePCBRoutes(s, drc, defaultDiagnoseOptions("SIG3"))
	n := diagnoseNet(t, rep, "SIG3")
	if err == nil || rep.Status != "incomplete" || n.Reason != pcbRouteDiagnoseUnknown || len(n.Pads) != 1 || n.Pads[0].Status != "unverified" || n.Detail == "" {
		t.Fatalf("unverified pad must be unknown/incomplete: status=%s %+v", rep.Status, n)
	}
	// One unknown net makes the whole report incomplete even next to a trapped one.
	trapped := routeDiagnoseTrappedBoard()
	trapped.Copper.Vias = s.Copper.Vias
	drc.Violations = append(drc.Violations, connectionRow("SIG2"))
	rep, _ = diagnosePCBRoutes(trapped, drc, defaultDiagnoseOptions())
	if rep.Status != "incomplete" || diagnoseNet(t, rep, "SIG2").Reason != pcbRouteDiagnoseTrapped {
		t.Fatalf("mixed unknown+trapped must be incomplete: %s %+v", rep.Status, rep.Nets)
	}
}

func TestRouteDiagnoseIncompleteGeometryIsIncomplete(t *testing.T) {
	tests := map[string]func(*boardSnapshot){
		"fallback rules":    func(s *boardSnapshot) { s.Rules.Source = "fallback" },
		"unknown poured":    func(s *boardSnapshot) { s.Copper.Availability["poured"] = "unknown" },
		"no copper":         func(s *boardSnapshot) { s.Copper = nil },
		"unknown pad shape": func(s *boardSnapshot) { s.Components[0].Pads[0].Shape = nil },
		"pad list missing": func(s *boardSnapshot) {
			s.Components[2].Availability = map[string]string{"pads": "unavailable"}
			s.Components[2].Pads = nil
		},
	}
	drc := drcFlatReport{Violations: []drcFlatViolation{connectionRow("SIG3")}}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			s := routabilityPocketFixture()
			mutate(s)
			rep, err := diagnosePCBRoutes(s, drc, defaultDiagnoseOptions())
			if err == nil || rep.Status != "incomplete" || rep.Reason == "" {
				t.Fatalf("incomplete geometry must not pass: status=%s err=%v", rep.Status, err)
			}
			if n := diagnoseNet(t, rep, "SIG3"); n.Reason != pcbRouteDiagnoseUnknown || n.Detail == "" {
				t.Fatalf("net must be unknown with the reason: %+v", n)
			}
		})
	}
	if rep, err := diagnosePCBRoutes(routabilityPocketFixture(), drc, pcbRouteDiagnoseOptions{StepMil: 0.1, RadiusMil: 100}); err == nil || rep.Status != "incomplete" {
		t.Fatalf("invalid --step must be incomplete: %s", rep.Status)
	}
}

func TestRouteDiagnoseWithoutConnectionErrorsPasses(t *testing.T) {
	drc := drcFlatReport{Violations: []drcFlatViolation{
		{Rule: "Clearance Error", Net: "SIG3", Objs: []string{"x"}},
		{Rule: "Netlist Error", Net: "SIG2"},
	}}
	// Even an incomplete board does not matter when nothing is unrouted.
	broken := routabilityPocketFixture()
	broken.Rules.Source = "fallback"
	for _, s := range []*boardSnapshot{routabilityPocketFixture(), broken} {
		rep, err := diagnosePCBRoutes(s, drc, defaultDiagnoseOptions())
		if err != nil || rep.Status != "pass" || len(rep.Nets) != 0 || rep.ConnectionRows != 0 {
			t.Fatalf("no Connection Error rows must pass: status=%s err=%v", rep.Status, err)
		}
	}
	if rep, err := diagnosePCBRoutes(routabilityPocketFixture(), drcFlatReport{Violations: []drcFlatViolation{}}, defaultDiagnoseOptions()); err != nil || rep.Status != "pass" {
		t.Fatalf("empty DRC must pass: %s %v", rep.Status, err)
	}
}

func TestRouteDiagnoseNetFilterAndRowAggregation(t *testing.T) {
	drc := drcFlatReport{Violations: []drcFlatViolation{
		connectionRow("SIG2", "a", "b"), connectionRow("SIG2", "b", "c"), connectionRow("SIG3", "z"),
		{Rule: "Clearance Error", Net: "SIG2", Objs: []string{"not-a-connection-row"}},
	}}
	rep, _ := diagnosePCBRoutes(routeDiagnoseTrappedBoard(), drc, defaultDiagnoseOptions())
	n := diagnoseNet(t, rep, "SIG2")
	if len(rep.Nets) != 2 || rep.ConnectionRows != 3 || n.Rows != 2 || strings.Join(n.DrcObjs, ",") != "a,b,c" {
		t.Fatalf("rows/drcObjs must aggregate Connection Error rows only: %+v", rep)
	}
	rep, err := diagnosePCBRoutes(routeDiagnoseTrappedBoard(), drc, defaultDiagnoseOptions("SIG3"))
	if err == nil || len(rep.Nets) != 1 || rep.Nets[0].Net != "SIG3" {
		t.Fatalf("--net must narrow the diagnosis: %+v err=%v", rep.Nets, err)
	}
	// A filtered net with no Connection Error row is treated as routed, and the
	// report says so instead of silently reporting pass.
	rep, err = diagnosePCBRoutes(routeDiagnoseTrappedBoard(), drc, defaultDiagnoseOptions("NOPE"))
	if err != nil || rep.Status != "pass" || !strings.Contains(strings.Join(rep.Limitations, "\n"), "NOPE") {
		t.Fatalf("unknown --net must pass with a limitation: %s %v %v", rep.Status, err, rep.Limitations)
	}
}

func TestRouteDiagnoseNetWithoutBoardPadsIsUnknown(t *testing.T) {
	drc := drcFlatReport{Violations: []drcFlatViolation{connectionRow("GHOST_NET")}}
	rep, err := diagnosePCBRoutes(routabilityPocketFixture(), drc, defaultDiagnoseOptions())
	if n := diagnoseNet(t, rep, "GHOST_NET"); err == nil || rep.Status != "incomplete" || n.Reason != pcbRouteDiagnoseUnknown || !strings.Contains(n.Detail, "different states") {
		t.Fatalf("a DRC net absent from the dump must be unknown: %s %+v", rep.Status, n)
	}
}

func TestRouteDiagnoseNetlessConnectionRowsAreIncomplete(t *testing.T) {
	drc := drcFlatReport{Violations: []drcFlatViolation{connectionRow("")}}
	rep, err := diagnosePCBRoutes(routabilityPocketFixture(), drc, defaultDiagnoseOptions())
	if err == nil || rep.Status != "incomplete" || rep.UnattributedRows != 1 {
		t.Fatalf("a Connection Error row without a net cannot be diagnosed: %s %v", rep.Status, err)
	}
	drc.Violations = append(drc.Violations, connectionRow("SIG3"))
	rep, _ = diagnosePCBRoutes(routabilityPocketFixture(), drc, defaultDiagnoseOptions())
	if rep.Status != "incomplete" {
		t.Fatalf("netless rows next to a diagnosable net stay incomplete: %s", rep.Status)
	}
}

func TestLoadPCBDrcFlatReportRejectsWrongInput(t *testing.T) {
	for name, raw := range map[string]string{
		"not json":          `nope`,
		"a board dump":      `{"components":[],"copper":{}}`,
		"null violations":   `{"violations":null}`,
		"total mismatch":    `{"total":3,"violations":[]}`,
		"violations string": `{"violations":"none"}`,
	} {
		if _, err := loadPCBDrcFlatReport([]byte(raw)); err == nil {
			t.Fatalf("%s must be rejected", name)
		}
	}
	got, err := loadPCBDrcFlatReport([]byte(`{"passed":false,"total":1,"violations":[{"rule":"Connection Error","net":"A","objs":["p1"]}]}`))
	if err != nil || len(got.Violations) != 1 || got.Violations[0].Net != "A" {
		t.Fatalf("valid report rejected: %v %+v", err, got)
	}
}

func TestRouteDiagnoseCommandIsReadOnlyAndReportsBlockers(t *testing.T) {
	dir := t.TempDir()
	board := filepath.Join(dir, "board.json")
	drcPath := filepath.Join(dir, "drc.json")
	out := filepath.Join(dir, "diagnose.json")
	boardRaw, _ := json.Marshal(routeDiagnoseTrappedBoard())
	// Written exactly as `pcb drc --json` prints it: flattenDrcResult plus nativeResult.
	flat := flattenDrcResult(map[string]any{"passed": false, "violations": []any{map[string]any{"list": []any{
		map[string]any{"errorType": "Connection Error", "errorObjType": "SMD Pad", "net": "SIG3", "objs": []any{"pad-a"}, "explanation": map[string]any{"str": "x"}},
		map[string]any{"errorType": "Connection Error", "errorObjType": "SMD Pad", "net": "SIG3", "objs": []any{"pad-b"}, "explanation": map[string]any{"str": "x"}},
		map[string]any{"errorType": "Clearance Error", "net": "OTHER", "objs": []any{"c"}, "explanation": map[string]any{"str": "x"}},
	}}}})
	drcRaw, _ := json.Marshal(struct {
		drcFlatReport
		NativeResult map[string]any `json:"nativeResult"`
	}{flat, map[string]any{"passed": false}})
	for path, raw := range map[string][]byte{board: boardRaw, drcPath: drcRaw} {
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var stdout, stderr bytes.Buffer
	cmd := newPcbRouteDiagnoseCmd(&stdout, &stderr)
	cmd.SetArgs([]string{"--board", board, "--drc", drcPath, "--out", out, "--net", "SIG3"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("an unrouted net must exit non-zero")
	}
	if got, _ := os.ReadFile(board); !bytes.Equal(got, boardRaw) {
		t.Fatal("read-only command modified the board")
	}
	if got, _ := os.ReadFile(drcPath); !bytes.Equal(got, drcRaw) {
		t.Fatal("read-only command modified the DRC report")
	}
	var rep pcbRouteDiagnoseReport
	blob, err := os.ReadFile(out)
	if err != nil || json.Unmarshal(blob, &rep) != nil {
		t.Fatalf("report unreadable: %v", err)
	}
	if rep.SchemaVersion != 1 || rep.Status != "fail" || rep.BoardSHA256 != sha256String(boardRaw) || rep.DRCSHA256 != sha256String(drcRaw) || len(rep.Nets) != 1 || rep.Nets[0].Rows != 2 || len(rep.Limitations) == 0 {
		t.Fatalf("unexpected report: %+v", rep)
	}
	if !strings.Contains(stderr.String(), "route-diagnose: fail") || !strings.Contains(stderr.String(), "SIG3: trapped") || !strings.Contains(stderr.String(), "J1.3") || !strings.Contains(stderr.String(), "track:trk-vbat") {
		t.Fatalf("stderr lacks the diagnosis: %s", stderr.String())
	}
	for _, args := range [][]string{{"--board", board, "--drc", drcPath, "--out", board}, {"--board", board, "--drc", drcPath, "--out", drcPath}} {
		cmd = newPcbRouteDiagnoseCmd(&stdout, &stderr)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Fatalf("output aliasing an input must be rejected: %v", args)
		}
	}
	cmd = newPcbRouteDiagnoseCmd(&stdout, &stderr)
	cmd.SetArgs([]string{"--board", board})
	if err := cmd.Execute(); err == nil {
		t.Fatal("--drc is required")
	}
}

func TestRouteDiagnoseCommandPassesWithoutConnectionErrors(t *testing.T) {
	dir := t.TempDir()
	board := filepath.Join(dir, "board.json")
	drcPath := filepath.Join(dir, "drc.json")
	boardRaw, _ := json.Marshal(routabilityPocketFixture())
	if err := os.WriteFile(board, boardRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(drcPath, []byte(`{"passed":true,"total":0,"counts":{},"violations":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	cmd := newPcbRouteDiagnoseCmd(&stdout, &stderr)
	cmd.SetArgs([]string{"--board", board, "--drc", drcPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("pass must exit zero: %v", err)
	}
	var rep pcbRouteDiagnoseReport
	if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil || rep.Status != "pass" || rep.Nets == nil || len(rep.Nets) != 0 {
		t.Fatalf("stdout must carry the pass report when --out is absent: %v %s", err, stdout.String())
	}
	if !strings.Contains(strings.Join(rep.Limitations, "\n"), "same saved and reloaded state") {
		t.Fatalf("limitations must name the same-state requirement: %v", rep.Limitations)
	}
}
