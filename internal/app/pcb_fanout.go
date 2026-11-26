package app

// pcb fanout turns a passing escape-plan report into real copper. The escape
// plan only reserves paths; fanout writes those reservations through the
// plan-apply path (typed create, readback, per net/layer reconcile) and then
// locks exactly the primitives it created, so the official router continues
// from the fanout end points instead of ripping them up.
//
// plan-apply replaces the copper of the nets in its plan. Fanout nets are
// therefore required to carry no copper at all before the write: the fanout
// must precede signal routing, and nothing real can be deleted by accident.
import (
	"fmt"
	"math"
	"sort"
	"strings"
)

const (
	pcbFanoutLockChunk = 100   // ids per pcb.track.lock call
	pcbFanoutViaEpsMil = 0.051 // readback tolerance of via sizes, as in module-check
)

type pcbFanoutPlan struct {
	Target *planApplyTarget
	Nets   []string // sorted fanout nets (= the nets plan-apply replaces)
}

type pcbFanoutReport struct {
	planApplyReport
	FanoutNets []string `json:"fanoutNets"`
	Segments   int      `json:"segments"` // tracks in the plan
	Vias       int      `json:"vias"`     // vias in the plan
	Locked     int      `json:"locked"`   // primitives the editor reports locked
	// LockError is set when the copper was written and proven but locking failed.
	LockError string `json:"lockError,omitempty"`
}

type pcbFanoutBoard interface {
	planApplyBoard
	// Lock locks exactly these primitive ids and returns how many are locked.
	Lock(ids []string) (int, error)
}

// buildPCBFanoutPlan checks that the escape report is a pass for this very
// board, converts it into a plan-apply target and proves the dump carries no
// copper on the fanout nets.
func buildPCBFanoutPlan(esc *pcbEscapeReport, snap *boardSnapshot) (*pcbFanoutPlan, error) {
	if esc.SchemaVersion != 1 {
		return nil, fmt.Errorf("escape report schemaVersion %d is not supported (want 1): is --escapes the output of `pcb escape-plan`?", esc.SchemaVersion)
	}
	if esc.Status != "pass" {
		reason := strings.Join(esc.Reasons, "; ")
		if reason != "" {
			reason = ": " + reason
		}
		return nil, fmt.Errorf("escape report status is %q, not pass%s; fix the escape plan first, fanout writes only proven reservations", esc.Status, reason)
	}
	have, err := moduleCheckSemanticSHA256(snap)
	if err != nil {
		return nil, err
	}
	if esc.BoardSemanticSHA256 == "" || esc.BoardSemanticSHA256 != have {
		return nil, fmt.Errorf("--board is not the dump the escape plan was computed on (semantic hash %s, report records %q); re-run `pcb escape-plan` on a fresh dump", have, esc.BoardSemanticSHA256)
	}
	target, nets, err := convertPCBEscapeToPlan(esc, snap.Rules)
	if err != nil {
		return nil, err
	}
	if err := pcbFanoutDumpCopper(snap, target.Replace); err != nil {
		return nil, err
	}
	return &pcbFanoutPlan{Target: target, Nets: nets}, nil
}

// convertPCBEscapeToPlan maps routes and vias to a plan-apply target with an
// identity layer map (escape routes already carry EasyEDA layer ids).
func convertPCBEscapeToPlan(esc *pcbEscapeReport, rules *boardRules) (*planApplyTarget, []string, error) {
	t := &planApplyTarget{Replace: map[string]bool{}}
	for _, r := range esc.Routes {
		if strings.TrimSpace(r.Net) == "" {
			return nil, nil, fmt.Errorf("route %s has no net", r.ID)
		}
		if !netPathCopperLayer(r.Layer) {
			return nil, nil, fmt.Errorf("route %s is on layer %d, not a copper layer", r.ID, r.Layer)
		}
		if !allFinite(r.WidthMil) || r.WidthMil <= 0 {
			return nil, nil, fmt.Errorf("route %s has no positive width", r.ID)
		}
		if len(r.Points) < 2 {
			return nil, nil, fmt.Errorf("route %s has fewer than two points", r.ID)
		}
		for _, p := range r.Points {
			if !allFinite(p[0], p[1]) {
				return nil, nil, fmt.Errorf("route %s has a non-finite point", r.ID)
			}
		}
		t.Replace[r.Net] = true
		for i := 0; i+1 < len(r.Points); i++ {
			a, b := r.Points[i], r.Points[i+1]
			if math.Hypot(b[0]-a[0], b[1]-a[1]) < 1e-6 {
				continue
			}
			t.Lines = append(t.Lines, planApplyTrack{Net: r.Net, Layer: r.Layer, X1: a[0], Y1: a[1], X2: b[0], Y2: b[1], Width: r.WidthMil})
		}
	}
	seen := map[string]planApplyVia{}
	for _, v := range esc.Vias {
		if v.ExistingPrimitiveID != "" || v.Role == "existing-ground-ep-entry" {
			return nil, nil, fmt.Errorf("escape report reuses existing via %s; fanout writes only new copper — drop the reuse from the escape intent or rip the via up first", v.ID)
		}
		if strings.TrimSpace(v.Net) == "" {
			return nil, nil, fmt.Errorf("via %s has no net", v.ID)
		}
		if !allFinite(v.X, v.Y) {
			return nil, nil, fmt.Errorf("via %s has a non-finite position", v.ID)
		}
		hole, dia := v.HoleMil, v.DiameterMil
		if !(allFinite(hole, dia) && hole > 0 && dia > hole) {
			// Not in the escape report: use the rules measured from the board.
			if rules == nil || !containsString(rules.Measured, pcbRuleFieldViaDrill) || !containsString(rules.Measured, pcbRuleFieldViaDiameter) ||
				!allFinite(rules.ViaDrillMil, rules.ViaDiameterMil) || rules.ViaDrillMil <= 0 || rules.ViaDiameterMil <= rules.ViaDrillMil {
				return nil, nil, fmt.Errorf("via %s has no valid hole/diameter and the board has no measured via rules to fall back on", v.ID)
			}
			hole, dia = rules.ViaDrillMil, rules.ViaDiameterMil
		}
		pv := planApplyVia{Net: v.Net, X: v.X, Y: v.Y, HoleMil: hole, DiameterMil: dia}
		key := planApplyViaKey(pv)
		if prev, dup := seen[key]; dup {
			if math.Abs(prev.HoleMil-hole) > pcbFanoutViaEpsMil || math.Abs(prev.DiameterMil-dia) > pcbFanoutViaEpsMil {
				return nil, nil, fmt.Errorf("vias at the same spot on net %s disagree on size (%.2f/%.2f vs %.2f/%.2f mil)", v.Net, prev.HoleMil, prev.DiameterMil, hole, dia)
			}
			continue
		}
		seen[key] = pv
		t.Replace[v.Net] = true
		t.Vias = append(t.Vias, pv)
	}
	if len(t.Lines) == 0 && len(t.Vias) == 0 {
		return nil, nil, fmt.Errorf("escape report has no routes or vias to write")
	}
	nets := make([]string, 0, len(t.Replace))
	for n := range t.Replace {
		nets = append(nets, n)
	}
	sort.Strings(nets)
	return t, nets, nil
}

// pcbFanoutExistingCopper describes the copper already on any of the nets, or "".
func pcbFanoutExistingCopper(nets map[string]bool, tracks, vias, arcs map[string]int) string {
	var parts []string
	var names []string
	for n := range nets {
		if tracks[n]+vias[n]+arcs[n] > 0 {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}
	var ripArgs []string
	for _, n := range names {
		parts = append(parts, fmt.Sprintf("%s (%d track(s), %d arc(s), %d via(s))", n, tracks[n], arcs[n], vias[n]))
		ripArgs = append(ripArgs, "--net "+n)
	}
	return fmt.Sprintf("fanout nets already carry copper: %s; fanout must precede signal routing and plan-apply would replace it. Remove it first with `easyeda pcb rip-up %s`, then save, reload and re-run `pcb dump --include-copper` and `pcb escape-plan`",
		strings.Join(parts, ", "), strings.Join(ripArgs, " "))
}

// pcbFanoutDumpCopper proves from the dump that the fanout nets carry no copper.
func pcbFanoutDumpCopper(snap *boardSnapshot, nets map[string]bool) error {
	tracks, arcs, vias, err := crystalSnapshotRouting(snap)
	if err != nil {
		return fmt.Errorf("cannot prove the fanout nets carry no copper: %w (re-dump with `pcb dump --include-copper`)", err)
	}
	t, a, v := map[string]int{}, map[string]int{}, map[string]int{}
	for _, x := range tracks {
		t[x.Net]++
	}
	for _, x := range arcs {
		a[x.Net]++
	}
	for _, x := range vias {
		v[x.Net]++
	}
	if msg := pcbFanoutExistingCopper(nets, t, v, a); msg != "" {
		return fmt.Errorf("%s", msg)
	}
	return nil
}

// pcbFanoutGuard re-proves the precondition on the live board with the very
// first read plan-apply makes, before anything is written. The dump can be old.
type pcbFanoutGuard struct {
	planApplyBoard
	nets    map[string]bool
	checked bool
}

func (g *pcbFanoutGuard) Read() (*planApplyState, error) {
	st, err := g.planApplyBoard.Read()
	if err != nil || g.checked {
		return st, err
	}
	g.checked = true
	t, v := map[string]int{}, map[string]int{}
	for _, l := range st.Lines {
		t[l.Net]++
	}
	for _, x := range st.Vias {
		v[x.Net]++
	}
	if msg := pcbFanoutExistingCopper(g.nets, t, v, st.Arcs); msg != "" {
		return nil, fmt.Errorf("live board: %s", msg)
	}
	return st, nil
}

// runPCBFanout writes the plan, proves it by readback and locks what it made.
// The report is returned even on failure.
func runPCBFanout(b pcbFanoutBoard, plan *pcbFanoutPlan, passes int, dryRun, lock bool) (*pcbFanoutReport, error) {
	base, err := runPlanApply(&pcbFanoutGuard{planApplyBoard: b, nets: plan.Target.Replace}, plan.Target, passes, dryRun)
	rep := &pcbFanoutReport{planApplyReport: *base, FanoutNets: plan.Nets, Segments: len(plan.Target.Lines), Vias: len(plan.Target.Vias)}
	if err != nil || dryRun {
		return rep, err
	}
	// The nets were empty before and reconcile proved their copper equals the
	// plan, so everything on them now is what fanout created.
	st, err := b.Read()
	if err != nil {
		rep.OK = false
		return rep, fmt.Errorf("read back after write: %w", err)
	}
	var ids []string
	for _, l := range st.Lines {
		if plan.Target.Replace[l.Net] {
			ids = append(ids, l.ID)
		}
	}
	live := map[string]planApplyLiveVia{}
	for _, v := range st.Vias {
		if plan.Target.Replace[v.Net] {
			ids = append(ids, v.ID)
			live[planApplyViaKey(v.planApplyVia)] = v
		}
	}
	sort.Strings(ids)
	// Reconcile matches vias by position; the size must be proven separately.
	for _, want := range plan.Target.Vias {
		got, ok := live[planApplyViaKey(want)]
		switch {
		case !ok:
			rep.Mismatches = append(rep.Mismatches, planApplyMismatch{Net: want.Net, Kind: "via-missing", X: want.X, Y: want.Y})
		case !(got.HoleMil > 0 && got.DiameterMil > 0):
			rep.Mismatches = append(rep.Mismatches, planApplyMismatch{Net: want.Net, Kind: "via-size-unreadable", X: want.X, Y: want.Y})
		case math.Abs(got.HoleMil-want.HoleMil) > pcbFanoutViaEpsMil:
			rep.Mismatches = append(rep.Mismatches, planApplyMismatch{Net: want.Net, Kind: "via-hole", ExpectedMil: want.HoleMil, ActualMil: got.HoleMil, X: want.X, Y: want.Y})
		case math.Abs(got.DiameterMil-want.DiameterMil) > pcbFanoutViaEpsMil:
			rep.Mismatches = append(rep.Mismatches, planApplyMismatch{Net: want.Net, Kind: "via-diameter", ExpectedMil: want.DiameterMil, ActualMil: got.DiameterMil, X: want.X, Y: want.Y})
		}
	}
	if len(rep.Mismatches) > 0 {
		rep.OK = false
		return rep, fmt.Errorf("%d via(s) on the board do not match the plan size/position after write (first: net %s %s); not locking, inspect before saving", len(rep.Mismatches), rep.Mismatches[0].Net, rep.Mismatches[0].Kind)
	}
	rep.Next = "not saved: run `easyeda pcb save`, `easyeda doc reload`, then read the copper back with a fresh `easyeda pcb dump --include-copper` before `easyeda pcb autoroute`"
	if !lock {
		return rep, nil
	}
	if len(ids) == 0 {
		return rep, fmt.Errorf("readback found no copper on the fanout nets, nothing to lock")
	}
	var lockErr error
	for i := 0; i < len(ids); i += pcbFanoutLockChunk {
		chunk := ids[i:min(i+pcbFanoutLockChunk, len(ids))]
		n, err := b.Lock(chunk)
		rep.Locked += n
		if err == nil && n != len(chunk) {
			err = fmt.Errorf("locked %d of %d primitive(s)", n, len(chunk))
		}
		if err != nil && lockErr == nil {
			lockErr = err
		}
	}
	if lockErr != nil {
		rep.LockError = lockErr.Error()
		return rep, fmt.Errorf("fanout copper is written and proven, but locking failed (%d of %d locked): %w", rep.Locked, len(ids), lockErr)
	}
	return rep, nil
}
