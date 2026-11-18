package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// planApplyFake is an in-memory board. With mergeOnDelete it reproduces the
// editor behaviour behind #312: deleting a track makes two collinear neighbours
// of the same net merge into ONE new primitive with a new id.
type planApplyFake struct {
	lines         []planApplyLiveLine
	vias          []planApplyLiveVia
	arcs          map[string]int
	next          int
	mergeOnDelete bool
	dropWidth     float64 // when > 0, AddLine stores this width instead (a write that lands wrong)
	writes        int
}

func (f *planApplyFake) id() string { f.next++; return fmt.Sprintf("p%d", f.next) }

func (f *planApplyFake) Read() (*planApplyState, error) {
	return &planApplyState{Lines: append([]planApplyLiveLine(nil), f.lines...), Vias: append([]planApplyLiveVia(nil), f.vias...), Arcs: f.arcs}, nil
}

func (f *planApplyFake) DeleteLines(ids []string) error {
	del := map[string]bool{}
	for _, id := range ids {
		del[id] = true
	}
	var kept []planApplyLiveLine
	removed := false
	for _, l := range f.lines {
		if del[l.ID] {
			removed = true
			continue
		}
		kept = append(kept, l)
	}
	f.lines = kept
	if removed && f.mergeOnDelete {
		f.merge()
	}
	return nil
}

// merge joins the first pair of collinear same-net same-layer tracks that share an end point.
func (f *planApplyFake) merge() {
	for i := range f.lines {
		for j := range f.lines {
			a, b := f.lines[i], f.lines[j]
			if i == j || a.Net != b.Net || a.Layer != b.Layer || a.X2 != b.X1 || a.Y2 != b.Y1 {
				continue
			}
			if (a.X2-a.X1)*(b.Y2-b.Y1) != (a.Y2-a.Y1)*(b.X2-b.X1) {
				continue
			}
			m := planApplyLiveLine{ID: f.id(), planApplyTrack: planApplyTrack{Net: a.Net, Layer: a.Layer, X1: a.X1, Y1: a.Y1, X2: b.X2, Y2: b.Y2, Width: a.Width}}
			var out []planApplyLiveLine
			for k, l := range f.lines {
				if k != i && k != j {
					out = append(out, l)
				}
			}
			f.lines = append(out, m)
			return
		}
	}
}

func (f *planApplyFake) DeleteVias(ids []string) error {
	del := map[string]bool{}
	for _, id := range ids {
		del[id] = true
	}
	var kept []planApplyLiveVia
	for _, v := range f.vias {
		if !del[v.ID] {
			kept = append(kept, v)
		}
	}
	f.vias = kept
	return nil
}

func (f *planApplyFake) AddLine(t planApplyTrack) error {
	f.writes++
	if f.dropWidth > 0 {
		t.Width = f.dropWidth
	}
	f.lines = append(f.lines, planApplyLiveLine{ID: f.id(), planApplyTrack: t})
	return nil
}

func (f *planApplyFake) AddVia(v planApplyVia) error {
	f.writes++
	f.vias = append(f.vias, planApplyLiveVia{ID: f.id(), planApplyVia: v})
	return nil
}

func (f *planApplyFake) line(net string, layer int, x1, y1, x2, y2, w float64) {
	f.lines = append(f.lines, planApplyLiveLine{ID: f.id(), planApplyTrack: planApplyTrack{Net: net, Layer: layer, X1: x1, Y1: y1, X2: x2, Y2: y2, Width: w}})
}

func planApplyTestTarget() *planApplyTarget {
	return &planApplyTarget{
		Replace: map[string]bool{"A": true},
		Lines:   []planApplyTrack{{Net: "A", Layer: 1, X1: 0, Y1: 0, X2: 100, Y2: 0, Width: 5}, {Net: "A", Layer: 2, X1: 100, Y1: 0, X2: 100, Y2: 80, Width: 5}},
		Vias:    []planApplyVia{{Net: "A", X: 100, Y: 0}},
	}
}

func TestPlanApplyReplacesNetAndLeavesOthers(t *testing.T) {
	f := &planApplyFake{}
	f.line("A", 1, 0, 0, 40, 40, 8)     // old route of A
	f.line("KEEP", 1, 0, 50, 90, 50, 8) // must survive untouched
	f.vias = append(f.vias, planApplyLiveVia{ID: "v-old", planApplyVia: planApplyVia{Net: "A", X: 40, Y: 40}}, planApplyLiveVia{ID: "v-keep", planApplyVia: planApplyVia{Net: "KEEP", X: 90, Y: 50}})
	rep, err := runPlanApply(f, planApplyTestTarget(), 3, false)
	if err != nil || !rep.OK || !rep.Reconciled || !rep.OtherNetsIntact {
		t.Fatalf("err=%v report=%+v", err, rep)
	}
	if rep.LinesDeleted != 1 || rep.LinesCreated != 2 || rep.ViasDeleted != 1 || rep.ViasCreated != 1 || rep.Passes != 1 {
		t.Fatalf("counts: %+v", rep)
	}
	keep := 0
	for _, l := range f.lines {
		if l.Net == "KEEP" && l.Width == 8 {
			keep++
		}
	}
	if keep != 1 || len(f.lines) != 3 || len(f.vias) != 2 {
		t.Fatalf("board after apply: %d lines %d vias keep=%d", len(f.lines), len(f.vias), keep)
	}
	// a second run has nothing to do
	writes := f.writes
	rep, err = runPlanApply(f, planApplyTestTarget(), 3, false)
	if err != nil || rep.Passes != 0 || f.writes != writes {
		t.Fatalf("idempotent rerun wrote again: err=%v report=%+v", err, rep)
	}
}

// #312: the stem of a T is stored as two tracks; deleting the branch makes the
// editor merge them into a new primitive that no id list collected before knew.
func TestPlanApplySweepsTracksMergedDuringDelete(t *testing.T) {
	f := &planApplyFake{mergeOnDelete: true}
	f.line("A", 1, 0, 0, 30, 0, 5)   // stem, first half
	f.line("A", 1, 30, 0, 45, 0, 5)  // stem, second half
	f.line("A", 1, 30, 0, 30, 20, 5) // branch
	rep, err := runPlanApply(f, planApplyTestTarget(), 3, false)
	if err != nil || !rep.Reconciled {
		t.Fatalf("err=%v report=%+v", err, rep)
	}
	for _, l := range f.lines {
		if l.Net == "A" && l.Layer == 1 && l.X2 == 45 {
			t.Fatalf("merged leftover survived: %+v", l)
		}
	}
	if len(f.lines) != 2 {
		t.Fatalf("want exactly the plan's 2 tracks, got %d", len(f.lines))
	}
}

// A net already drawn along the plan's path at another width has the right
// length; only the width-weighted comparison catches it.
func TestPlanApplyCatchesSamePathWrongWidth(t *testing.T) {
	f := &planApplyFake{}
	f.line("A", 1, 0, 0, 100, 0, 10)
	f.line("A", 2, 100, 0, 100, 80, 10)
	f.vias = append(f.vias, planApplyLiveVia{ID: "v", planApplyVia: planApplyVia{Net: "A", X: 100, Y: 0}})
	rep, err := runPlanApply(f, planApplyTestTarget(), 3, true)
	if err != nil || rep.Reconciled || len(rep.Mismatches) != 2 || rep.Mismatches[0].Kind != "width" || rep.LinesDeleted != 2 || rep.LinesCreated != 2 {
		t.Fatalf("dry run should report two width mismatches: err=%v report=%+v", err, rep)
	}
	if f.writes != 0 || len(f.lines) != 2 {
		t.Fatal("dry run changed the board")
	}
	rep, err = runPlanApply(f, planApplyTestTarget(), 3, false)
	if err != nil || !rep.Reconciled {
		t.Fatalf("err=%v report=%+v", err, rep)
	}
	for _, l := range f.lines {
		if l.Width != 5 {
			t.Fatalf("track kept the old width: %+v", l)
		}
	}
}

func TestPlanApplyFailsClosed(t *testing.T) {
	// writes that land at the wrong width never reconcile
	f := &planApplyFake{dropWidth: 9}
	rep, err := runPlanApply(f, planApplyTestTarget(), 2, false)
	if err == nil || rep.OK || rep.Reconciled || rep.Passes != 2 || !strings.Contains(err.Error(), "does not match the plan") {
		t.Fatalf("want a reconcile failure after 2 passes: err=%v report=%+v", err, rep)
	}
	// locked copper is refused, not unlocked behind the user's back
	f = &planApplyFake{}
	f.line("A", 1, 0, 0, 40, 40, 8)
	f.lines[0].Locked = true
	if _, err = runPlanApply(f, planApplyTestTarget(), 3, false); err == nil || !strings.Contains(err.Error(), "locked") || len(f.lines) != 1 {
		t.Fatalf("locked copper: err=%v lines=%d", err, len(f.lines))
	}
	// arcs on a net to replace are refused
	f = &planApplyFake{arcs: map[string]int{"A": 1}}
	if _, err = runPlanApply(f, planApplyTestTarget(), 3, false); err == nil || !strings.Contains(err.Error(), "arc") {
		t.Fatalf("arcs: err=%v", err)
	}
}

func TestLoadPlanApplyTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.json")
	write := func(s string) {
		if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	lm, err := parsePlanApplyLayerMap("0=1, 1=2,2=16")
	if err != nil || lm[2] != 16 {
		t.Fatalf("layer map: %v %v", lm, err)
	}
	if _, err := parsePlanApplyLayerMap("0=top"); err == nil {
		t.Fatal("bad layer map accepted")
	}
	write(`[{"net":"A","width":5,"lines":[{"Layer":2,"X1":0,"Y1":0,"X2":10,"Y2":0},{"Layer":0,"X1":3,"Y1":3,"X2":3,"Y2":3}],"vias":[[10,0],[10,0]]},
	        {"net":"B","width":5,"failed":true,"lines":[],"vias":[]}]`)
	tg, err := loadPlanApplyTarget(path, lm, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tg.Lines) != 1 || tg.Lines[0].Layer != 16 || len(tg.Vias) != 1 || !tg.Replace["A"] || !tg.Replace["B"] || len(tg.Skipped) != 1 {
		t.Fatalf("target: %+v", tg)
	}
	if _, err := loadPlanApplyTarget(path, lm, []string{"A"}); err == nil || !strings.Contains(err.Error(), "not in --replace-nets") {
		t.Fatalf("plan net outside --replace-nets accepted: %v", err)
	}
	if tg, err = loadPlanApplyTarget(path, lm, []string{"A", "B", "OLD"}); err != nil || !tg.Replace["OLD"] {
		t.Fatalf("extra replace net: %v", err)
	}
	if _, err := loadPlanApplyTarget(path, map[int]int{0: 1}, nil); err == nil || !strings.Contains(err.Error(), "--layer-map") {
		t.Fatalf("unmapped layer accepted: %v", err)
	}
	write(`[{"net":"A","width":0,"lines":[{"Layer":0,"X1":0,"Y1":0,"X2":10,"Y2":0}]}]`)
	if _, err := loadPlanApplyTarget(path, lm, nil); err == nil || !strings.Contains(err.Error(), "no width") {
		t.Fatalf("zero width accepted: %v", err)
	}
}
