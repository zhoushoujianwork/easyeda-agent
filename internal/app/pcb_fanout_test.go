package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

// fanoutFake is plan-apply's in-memory board plus the lock call and via sizes.
type fanoutFake struct {
	*planApplyFake
	lockCalls [][]string
	lockErr   error
	lockShort int // report this many fewer locked than asked
	viaScale  float64
}

func (f *fanoutFake) AddVia(v planApplyVia) error {
	f.writes++
	if f.viaScale != 0 {
		v.DiameterMil *= f.viaScale
	}
	f.vias = append(f.vias, planApplyLiveVia{ID: f.id(), planApplyVia: v})
	return nil
}

func (f *fanoutFake) Lock(ids []string) (int, error) {
	f.lockCalls = append(f.lockCalls, append([]string(nil), ids...))
	if f.lockErr != nil {
		return 0, f.lockErr
	}
	return len(ids) - f.lockShort, nil
}

func newFanoutFake() *fanoutFake { return &fanoutFake{planApplyFake: &planApplyFake{}} }

func (f *fanoutFake) lockedIDs() []string {
	var all []string
	for _, c := range f.lockCalls {
		all = append(all, c...)
	}
	sort.Strings(all)
	return all
}

// fanoutEscape is a hand-written passing report on the escape fixture board.
func fanoutEscape(t *testing.T, snap *boardSnapshot) *pcbEscapeReport {
	t.Helper()
	hash, err := moduleCheckSemanticSHA256(snap)
	if err != nil {
		t.Fatal(err)
	}
	return &pcbEscapeReport{SchemaVersion: 1, Status: "pass", BoardSemanticSHA256: hash, Routes: []pcbModuleRoute{
		{ID: "a/top-entry", Net: "A", Layer: 1, WidthMil: 5, Points: [][2]float64{{50, 80}, {50, 80}, {80, 80}, {100, 110}}},
		{ID: "a/bottom", Net: "A", Layer: 2, WidthMil: 6, Points: [][2]float64{{100, 110}, {100, 150}}},
		{ID: "b", Net: "B", Layer: 1, WidthMil: 4, Points: [][2]float64{{50, 120}, {90, 120}}},
	}, Vias: []pcbModuleVia{
		{ID: "a/via-1", Net: "A", X: 100, Y: 110, HoleMil: 10, DiameterMil: 20, Role: "escape-layer-transition"},
		{ID: "b/via-1", Net: "B", X: 90, Y: 120, Role: "escape-layer-transition"}, // no size: board rules
	}}
}

func fanoutBoardWithLine(net string) *boardSnapshot {
	_, s := escapeFixture()
	s.Rules.Measured = []string{pcbRuleFieldViaDrill, pcbRuleFieldViaDiameter}
	s.Copper.Lines = []any{map[string]any{"primitiveId": "old-" + net, "net": net, "layer": float64(1), "startX": 0.0, "startY": 0.0, "endX": 30.0, "endY": 0.0, "lineWidth": 5.0}}
	return s
}

func TestFanoutConvertsEveryRouteSegmentAndVia(t *testing.T) {
	_, snap := escapeFixture()
	snap.Rules.Measured = []string{pcbRuleFieldViaDrill, pcbRuleFieldViaDiameter}
	plan, err := buildPCBFanoutPlan(fanoutEscape(t, snap), snap)
	if err != nil {
		t.Fatal(err)
	}
	wantLines := []planApplyTrack{
		{Net: "A", Layer: 1, X1: 50, Y1: 80, X2: 80, Y2: 80, Width: 5}, // the duplicated first point is skipped
		{Net: "A", Layer: 1, X1: 80, Y1: 80, X2: 100, Y2: 110, Width: 5},
		{Net: "A", Layer: 2, X1: 100, Y1: 110, X2: 100, Y2: 150, Width: 6},
		{Net: "B", Layer: 1, X1: 50, Y1: 120, X2: 90, Y2: 120, Width: 4},
	}
	if !reflect.DeepEqual(plan.Target.Lines, wantLines) {
		t.Fatalf("segments differ:\n got %+v\nwant %+v", plan.Target.Lines, wantLines)
	}
	wantVias := []planApplyVia{
		{Net: "A", X: 100, Y: 110, HoleMil: 10, DiameterMil: 20},
		{Net: "B", X: 90, Y: 120, HoleMil: snap.Rules.ViaDrillMil, DiameterMil: snap.Rules.ViaDiameterMil}, // fallback to measured rules
	}
	if !reflect.DeepEqual(plan.Target.Vias, wantVias) {
		t.Fatalf("vias differ:\n got %+v\nwant %+v", plan.Target.Vias, wantVias)
	}
	if !plan.Target.Replace["A"] || !plan.Target.Replace["B"] || len(plan.Target.Replace) != 2 || strings.Join(plan.Nets, ",") != "A,B" {
		t.Fatalf("replace set must be exactly the fanout nets: %+v %v", plan.Target.Replace, plan.Nets)
	}
	// Without measured via rules and without sizes in the report there is nothing to write.
	snap.Rules.Measured = nil
	if _, err := buildPCBFanoutPlan(fanoutEscape(t, snap), snap); err == nil || !strings.Contains(err.Error(), "measured via rules") {
		t.Fatalf("unmeasured via fallback must be refused: %v", err)
	}
}

func TestFanoutRefusesUnprovenOrStaleEscapeReport(t *testing.T) {
	_, snap := escapeFixture()
	snap.Rules.Measured = []string{pcbRuleFieldViaDrill, pcbRuleFieldViaDiameter}
	tests := map[string]struct {
		mutate func(*pcbEscapeReport)
		want   string
	}{
		"incomplete": {func(r *pcbEscapeReport) { r.Status = "incomplete"; r.Reasons = []string{"budget exhausted"} }, "not pass: budget exhausted"},
		"fail":       {func(r *pcbEscapeReport) { r.Status = "fail" }, `"fail", not pass`},
		"no status":  {func(r *pcbEscapeReport) { r.Status = "" }, "not pass"},
		"hash":       {func(r *pcbEscapeReport) { r.BoardSemanticSHA256 = strings.Repeat("0", 64) }, "not the dump"},
		"no hash":    {func(r *pcbEscapeReport) { r.BoardSemanticSHA256 = "" }, "not the dump"},
		"schema":     {func(r *pcbEscapeReport) { r.SchemaVersion = 2 }, "schemaVersion"},
		"reused via": {func(r *pcbEscapeReport) { r.Vias[0].ExistingPrimitiveID = "v9" }, "reuses existing via"},
		"no copper":  {func(r *pcbEscapeReport) { r.Routes, r.Vias = nil, nil }, "no routes or vias"},
		"bad layer":  {func(r *pcbEscapeReport) { r.Routes[0].Layer = 12 }, "not a copper layer"},
		"no width":   {func(r *pcbEscapeReport) { r.Routes[0].WidthMil = 0 }, "positive width"},
		"one point":  {func(r *pcbEscapeReport) { r.Routes[0].Points = r.Routes[0].Points[:1] }, "fewer than two"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			esc := fanoutEscape(t, snap)
			tc.mutate(esc)
			if _, err := buildPCBFanoutPlan(esc, snap); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
	// A semantically different board (a pad moved) must not accept the report.
	moved := cloneEscapeSnapshot(snap)
	moved.Components[0].Pads[0].X += 5
	if _, err := buildPCBFanoutPlan(fanoutEscape(t, snap), moved); err == nil || !strings.Contains(err.Error(), "not the dump") {
		t.Fatalf("a changed board must fail the hash check: %v", err)
	}
	// Capture time is provenance, not board semantics.
	later := cloneEscapeSnapshot(snap)
	later.CapturedAt = "later"
	if _, err := buildPCBFanoutPlan(fanoutEscape(t, snap), later); err != nil {
		t.Fatalf("capture time must not break the hash: %v", err)
	}
}

func TestFanoutRefusesExistingCopperOnFanoutNets(t *testing.T) {
	for name, mutate := range map[string]func(*boardSnapshot){
		"track": func(s *boardSnapshot) {},
		"via": func(s *boardSnapshot) {
			s.Copper.Lines = []any{}
			s.Copper.Vias = []any{map[string]any{"primitiveId": "old-via", "net": "B", "x": 5.0, "y": 5.0, "holeDiameter": 8.0, "diameter": 16.0}}
		},
		"arc": func(s *boardSnapshot) {
			s.Copper.Lines = []any{}
			s.Copper.Arcs = []any{map[string]any{"primitiveId": "old-arc", "net": "A", "layer": float64(1), "startX": 0.0, "startY": 0.0, "endX": 10.0, "endY": 10.0, "arcAngle": 90.0, "lineWidth": 5.0}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			snap := fanoutBoardWithLine("A")
			mutate(snap)
			_, err := buildPCBFanoutPlan(fanoutEscape(t, snap), snap)
			if err == nil || !strings.Contains(err.Error(), "already carry copper") || !strings.Contains(err.Error(), "pcb rip-up --net ") {
				t.Fatalf("existing copper must be refused with the rip-up hint: %v", err)
			}
			wantNet := map[string]string{"track": "A", "via": "B", "arc": "A"}[name]
			if !strings.Contains(err.Error(), wantNet+" (") {
				t.Fatalf("error must name net %s: %v", wantNet, err)
			}
		})
	}
	// Copper on a net the fanout does not touch is none of its business.
	snap := fanoutBoardWithLine("OTHER")
	if _, err := buildPCBFanoutPlan(fanoutEscape(t, snap), snap); err != nil {
		t.Fatalf("copper on unrelated nets must be accepted: %v", err)
	}
	// An unreadable copper inventory cannot prove the precondition.
	snap = fanoutBoardWithLine("OTHER")
	snap.Copper.ArcsAvailable = nil
	if _, err := buildPCBFanoutPlan(fanoutEscape(t, snap), snap); err == nil || !strings.Contains(err.Error(), "cannot prove") {
		t.Fatalf("unknown arc inventory must be refused: %v", err)
	}
}

func fanoutPlanFor(t *testing.T) *pcbFanoutPlan {
	t.Helper()
	_, snap := escapeFixture()
	snap.Rules.Measured = []string{pcbRuleFieldViaDrill, pcbRuleFieldViaDiameter}
	plan, err := buildPCBFanoutPlan(fanoutEscape(t, snap), snap)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestFanoutWritesProvesAndLocksExactlyTheNewCopper(t *testing.T) {
	f := newFanoutFake()
	f.line("KEEP", 1, 0, 50, 90, 50, 8)
	f.vias = append(f.vias, planApplyLiveVia{ID: "v-keep", planApplyVia: planApplyVia{Net: "KEEP", X: 90, Y: 50, HoleMil: 8, DiameterMil: 16}})
	rep, err := runPCBFanout(f, fanoutPlanFor(t), 3, false, true)
	if err != nil || !rep.OK || !rep.Reconciled || !rep.OtherNetsIntact {
		t.Fatalf("err=%v report=%+v", err, rep)
	}
	if rep.Segments != 4 || rep.Vias != 2 || rep.LinesCreated != 4 || rep.ViasCreated != 2 || strings.Join(rep.FanoutNets, ",") != "A,B" {
		t.Fatalf("counts: %+v", rep)
	}
	// The ids that exist on the fanout nets after the write, from the readback.
	var want []string
	for _, l := range f.lines {
		if l.Net == "A" || l.Net == "B" {
			want = append(want, l.ID)
		}
	}
	for _, v := range f.vias {
		if v.Net == "A" || v.Net == "B" {
			want = append(want, v.ID)
		}
	}
	sort.Strings(want)
	if len(want) != 6 || !reflect.DeepEqual(f.lockedIDs(), want) || rep.Locked != 6 {
		t.Fatalf("lock must cover exactly the 6 new primitives: locked=%v want=%v report=%d", f.lockedIDs(), want, rep.Locked)
	}
	for _, id := range f.lockedIDs() {
		if id == "v-keep" || id == "p1" { // KEEP's pre-existing track and via
			t.Fatalf("pre-existing copper was locked: %v", f.lockedIDs())
		}
	}
	if !strings.Contains(rep.Next, "pcb save") || !strings.Contains(rep.Next, "pcb dump") {
		t.Fatalf("report must say save → reload → readback follows: %q", rep.Next)
	}
	// Idempotent: a second run is refused by the precondition, not rewritten.
	writes := f.writes
	if _, err := runPCBFanout(f, fanoutPlanFor(t), 3, false, true); err == nil || !strings.Contains(err.Error(), "already carry copper") || f.writes != writes {
		t.Fatalf("a rerun must refuse before writing: err=%v writes %d→%d", err, writes, f.writes)
	}
}

func TestFanoutDryRunWritesAndLocksNothing(t *testing.T) {
	f := newFanoutFake()
	f.line("KEEP", 1, 0, 50, 90, 50, 8)
	rep, err := runPCBFanout(f, fanoutPlanFor(t), 3, true, true)
	if err != nil || !rep.OK || !rep.DryRun || rep.Reconciled {
		t.Fatalf("err=%v report=%+v", err, rep)
	}
	if rep.LinesCreated != 4 || rep.ViasCreated != 2 || rep.Locked != 0 || rep.Segments != 4 || rep.Vias != 2 {
		t.Fatalf("dry run must report the plan: %+v", rep)
	}
	if f.writes != 0 || len(f.lines) != 1 || len(f.vias) != 0 || len(f.lockCalls) != 0 {
		t.Fatalf("dry run changed the board: writes=%d lines=%d vias=%d locks=%d", f.writes, len(f.lines), len(f.vias), len(f.lockCalls))
	}
}

func TestFanoutNoLockLeavesCopperUnlocked(t *testing.T) {
	f := newFanoutFake()
	rep, err := runPCBFanout(f, fanoutPlanFor(t), 3, false, false)
	if err != nil || !rep.OK || rep.Locked != 0 || len(f.lockCalls) != 0 {
		t.Fatalf("--lock=false must not lock: err=%v report=%+v locks=%d", err, rep, len(f.lockCalls))
	}
}

func TestFanoutLockFailureKeepsReportAndFails(t *testing.T) {
	for name, f := range map[string]*fanoutFake{
		"error": {planApplyFake: &planApplyFake{}, lockErr: fmt.Errorf("host refused")},
		"short": {planApplyFake: &planApplyFake{}, lockShort: 2},
	} {
		t.Run(name, func(t *testing.T) {
			rep, err := runPCBFanout(f, fanoutPlanFor(t), 3, false, true)
			if err == nil || !strings.Contains(err.Error(), "locking failed") {
				t.Fatalf("a lock failure must be an error: %v", err)
			}
			if rep == nil || !rep.OK || !rep.Reconciled || rep.LinesCreated != 4 || rep.LockError == "" || len(f.lines) != 4 {
				t.Fatalf("the proven copper report must survive a lock failure: %+v", rep)
			}
			if name == "short" && rep.Locked != 4 {
				t.Fatalf("locked count must be what the editor reported: %d", rep.Locked)
			}
		})
	}
}

// Reconcile matches vias by position only; a via written at the wrong size
// must stop the run before it is locked.
func TestFanoutViaSizeMismatchIsNotLocked(t *testing.T) {
	f := newFanoutFake()
	f.viaScale = 2
	rep, err := runPCBFanout(f, fanoutPlanFor(t), 3, false, true)
	if err == nil || rep.OK || len(f.lockCalls) != 0 || len(rep.Mismatches) == 0 || rep.Mismatches[0].Kind != "via-diameter" {
		t.Fatalf("size mismatch must fail without locking: err=%v report=%+v", err, rep)
	}
	// A readback that carries no via size cannot prove the size either.
	g := &sizeBlindFake{fanoutFake: newFanoutFake()}
	rep, err = runPCBFanout(g, fanoutPlanFor(t), 3, false, true)
	if err == nil || len(g.lockCalls) != 0 || rep.Mismatches[0].Kind != "via-size-unreadable" {
		t.Fatalf("an unreadable via size must fail: err=%v report=%+v", err, rep)
	}
}

// sizeBlindFake is a connector that does not report via sizes.
type sizeBlindFake struct{ *fanoutFake }

func (f *sizeBlindFake) Read() (*planApplyState, error) {
	st, err := f.fanoutFake.Read()
	for i := range st.Vias {
		st.Vias[i].HoleMil, st.Vias[i].DiameterMil = 0, 0
	}
	return st, err
}

// The dump can be stale: the live board is checked again before the first write.
func TestFanoutLiveBoardWithExistingCopperIsRefusedBeforeWriting(t *testing.T) {
	f := newFanoutFake()
	f.line("A", 1, 0, 0, 30, 0, 5)
	f.lines[0].Locked = true
	rep, err := runPCBFanout(f, fanoutPlanFor(t), 3, false, true)
	if err == nil || !strings.Contains(err.Error(), "live board") || !strings.Contains(err.Error(), "pcb rip-up --net A") {
		t.Fatalf("live copper must be refused: %v", err)
	}
	if f.writes != 0 || len(f.lines) != 1 || len(f.lockCalls) != 0 || rep.OK {
		t.Fatalf("nothing may be written or locked: writes=%d lines=%d", f.writes, len(f.lines))
	}
	f = newFanoutFake()
	f.vias = append(f.vias, planApplyLiveVia{ID: "v", planApplyVia: planApplyVia{Net: "B", X: 1, Y: 1}})
	if _, err := runPCBFanout(f, fanoutPlanFor(t), 3, true, true); err == nil || !strings.Contains(err.Error(), "live board") {
		t.Fatalf("--dry-run must also refuse: %v", err)
	}
}

// A real escape-plan report goes through the converter unchanged.
func TestFanoutAcceptsARealEscapePlanReport(t *testing.T) {
	in, snap := escapeFixture()
	snap.Rules.Measured = []string{pcbRuleFieldViaDrill, pcbRuleFieldViaDiameter}
	rep, err := planPCBEscape(in, snap)
	if err != nil || rep.Status != "pass" {
		t.Fatalf("escape-plan: %v %+v", err, rep)
	}
	raw, _ := json.Marshal(rep)
	var round pcbEscapeReport
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatal(err)
	}
	plan, err := buildPCBFanoutPlan(&round, snap)
	if err != nil || len(plan.Target.Lines) == 0 {
		t.Fatalf("real report rejected: %v", err)
	}
	var want float64
	for _, r := range rep.Routes {
		for i := 0; i+1 < len(r.Points); i++ {
			want += (&planApplyTrack{X1: r.Points[i][0], Y1: r.Points[i][1], X2: r.Points[i+1][0], Y2: r.Points[i+1][1]}).length()
		}
	}
	var got float64
	for _, l := range plan.Target.Lines {
		got += l.length()
	}
	if d := want - got; d > 1e-6 || d < -1e-6 {
		t.Fatalf("total length changed in conversion: %.3f vs %.3f", got, want)
	}
}

func (t *planApplyTrack) length() float64 {
	dx, dy := t.X2-t.X1, t.Y2-t.Y1
	return math.Hypot(dx, dy)
}

// ── command: typed actions through a stub daemon ────────────────────────────

type fanoutHost struct {
	mu      sync.Mutex
	next    int
	lines   []map[string]any
	vias    []map[string]any
	creates int
	locks   []map[string]any
}

func (h *fanoutHost) respond(_ int, call autolayoutTestCall) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	ctx := `,"context":{"projectUuid":"proj-1","projectName":"ceshi","documentType":"pcb"}`
	ok := func(result any) string {
		raw, _ := json.Marshal(result)
		return `{"ok":true,"result":` + string(raw) + ctx + `}`
	}
	id := func() string { h.next++; return fmt.Sprintf("id%d", h.next) }
	p := call.Payload
	switch call.Action {
	case "pcb.line.list":
		return ok(map[string]any{"lines": h.lines, "arcs": []any{}, "arcsAvailable": true})
	case "pcb.via.list":
		return ok(map[string]any{"vias": h.vias})
	case "pcb.line.create":
		h.creates++
		h.lines = append(h.lines, map[string]any{"primitiveId": id(), "net": p["net"], "layer": p["layer"], "startX": p["startX"], "startY": p["startY"], "endX": p["endX"], "endY": p["endY"], "lineWidth": p["lineWidth"], "locked": false})
		return ok(map[string]any{})
	case "pcb.via.create":
		h.creates++
		h.vias = append(h.vias, map[string]any{"primitiveId": id(), "net": p["net"], "x": p["x"], "y": p["y"], "holeDiameter": p["holeDiameter"], "diameter": p["diameter"], "locked": false})
		return ok(map[string]any{})
	case "pcb.track.lock":
		h.locks = append(h.locks, p)
		ids, _ := p["primitiveIds"].([]any)
		return ok(map[string]any{"locked": true, "total": len(ids), "failures": []any{}})
	}
	return `{"ok":false,"error":{"code":"UNEXPECTED","message":"` + call.Action + `"}}`
}

func writeFanoutInputs(t *testing.T, snap *boardSnapshot, esc *pcbEscapeReport) (escapes, board string) {
	t.Helper()
	dir := t.TempDir()
	escapes, board = filepath.Join(dir, "escapes.json"), filepath.Join(dir, "board.json")
	for path, v := range map[string]any{escapes: esc, board: snap} {
		raw, _ := json.Marshal(v)
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return
}

func runFanoutCmd(t *testing.T, h *fanoutHost, args ...string) (string, error) {
	t.Helper()
	t.Setenv(envCheckpoint, "off")
	cfg, _, cleanup := newAutolayoutTestDaemon(t, h.respond)
	defer cleanup()
	var stdout, stderr bytes.Buffer
	window := "w1"
	cmd := newPcbFanoutCmd(cfg, &window, &stdout, &stderr)
	cmd.SetArgs(args)
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	err := cmd.Execute()
	return stdout.String(), err
}

func TestFanoutCommandLocksExactlyTheCreatedPrimitives(t *testing.T) {
	_, snap := escapeFixture()
	snap.Rules.Measured = []string{pcbRuleFieldViaDrill, pcbRuleFieldViaDiameter}
	escapes, board := writeFanoutInputs(t, snap, fanoutEscape(t, snap))
	h := &fanoutHost{lines: []map[string]any{{"primitiveId": "keep-1", "net": "KEEP", "layer": 1, "startX": 0.0, "startY": 50.0, "endX": 90.0, "endY": 50.0, "lineWidth": 8.0, "locked": false}}}
	out, err := runFanoutCmd(t, h, "--escapes", escapes, "--board", board, "--project-uuid", "proj-1", "--dry-run")
	var rep pcbFanoutReport
	if err != nil || json.Unmarshal([]byte(out), &rep) != nil || !rep.DryRun || !rep.OK || h.creates != 0 || len(h.locks) != 0 {
		t.Fatalf("dry-run must not write: err=%v creates=%d locks=%d out=%s", err, h.creates, len(h.locks), out)
	}
	out, err = runFanoutCmd(t, h, "--escapes", escapes, "--board", board, "--project-uuid", "proj-1")
	if err != nil || json.Unmarshal([]byte(out), &rep) != nil || !rep.OK || rep.Locked != 6 || rep.Segments != 4 || rep.Vias != 2 || strings.Join(rep.FanoutNets, ",") != "A,B" {
		t.Fatalf("fanout: err=%v out=%s", err, out)
	}
	if h.creates != 6 || len(h.locks) != 1 {
		t.Fatalf("creates=%d lockCalls=%d", h.creates, len(h.locks))
	}
	var created []string
	for _, l := range h.lines {
		if l["net"] != "KEEP" {
			created = append(created, l["primitiveId"].(string))
		}
	}
	for _, v := range h.vias {
		created = append(created, v["primitiveId"].(string))
		if v["holeDiameter"] == nil || v["diameter"] == nil {
			t.Fatalf("via created without its size: %+v", v)
		}
	}
	got, _ := h.locks[0]["primitiveIds"].([]any)
	var ids []string
	for _, x := range got {
		ids = append(ids, x.(string))
	}
	sort.Strings(ids)
	sort.Strings(created)
	if !reflect.DeepEqual(ids, created) || h.locks[0]["locked"] != true {
		t.Fatalf("lock payload must be exactly the created ids: lock=%v created=%v payload=%v", ids, created, h.locks[0])
	}
	for _, id := range ids {
		if id == "keep-1" {
			t.Fatal("the pre-existing track was locked")
		}
	}
}

func TestFanoutCommandRefusalsAndFlags(t *testing.T) {
	_, snap := escapeFixture()
	snap.Rules.Measured = []string{pcbRuleFieldViaDrill, pcbRuleFieldViaDiameter}
	esc := fanoutEscape(t, snap)
	esc.Status = "incomplete"
	escapes, board := writeFanoutInputs(t, snap, esc)
	h := &fanoutHost{}
	if _, err := runFanoutCmd(t, h, "--escapes", escapes, "--board", board, "--project-uuid", "proj-1"); err == nil || !strings.Contains(err.Error(), "not pass") || h.creates != 0 {
		t.Fatalf("a non-pass report must be refused before any write: %v", err)
	}
	for _, args := range [][]string{
		{"--board", board, "--project-uuid", "p"},
		{"--escapes", escapes, "--project-uuid", "p"},
		{"--escapes", escapes, "--board", board},
		{"--escapes", escapes, "--board", board, "--project-uuid", "p", "--passes", "9"},
	} {
		if _, err := runFanoutCmd(t, h, args...); err == nil {
			t.Fatalf("invalid flags accepted: %v", args)
		}
	}
	// The default is to lock.
	cmd := newPcbFanoutCmd(&appConfig{}, new(string), &bytes.Buffer{}, &bytes.Buffer{})
	if f := cmd.Flags().Lookup("lock"); f == nil || f.DefValue != "true" {
		t.Fatalf("--lock must default to true: %+v", f)
	}
}
