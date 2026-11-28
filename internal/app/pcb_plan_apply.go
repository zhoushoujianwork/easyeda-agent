package app

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
)

// pcb plan-apply: bring the live board's copper to an offline routing plan and
// prove it got there. The plan replaces the copper of a named set of nets; every
// other net must come out exactly as it went in.
//
// Deleting by primitiveId is not enough on its own: when a track is removed the
// editor may merge its collinear neighbours into a NEW primitive whose id was not
// in the delete list, so it survives (#312). The loop therefore never trusts a
// delete's return value — after each change it reads the board back and compares
// what is there with what should be there, per net and layer, by total length
// AND width-weighted length (a net redrawn along the same path at another width
// has the right length and the wrong copper).

type planApplyTrack struct {
	Net            string
	Layer          int // EasyEDA layer id
	X1, Y1, X2, Y2 float64
	Width          float64
}

type planApplyVia struct {
	Net  string
	X, Y float64
	// HoleMil and DiameterMil are optional (0 = the editor default). They are not
	// part of the via's identity key: reconcile matches by net and position, and
	// callers that need the size proven (pcb fanout) compare it after readback.
	HoleMil, DiameterMil float64
}

type planApplyLiveLine struct {
	ID string
	planApplyTrack
	Locked bool
}

type planApplyLiveVia struct {
	ID string
	planApplyVia
	Locked bool
}

type planApplyState struct {
	Lines []planApplyLiveLine
	Vias  []planApplyLiveVia
	Arcs  map[string]int // arcs per net; plan-apply cannot rewrite arcs
}

// planApplyBoard is the live board as plan-apply needs it. The command backs it
// with typed actions; tests back it with an in-memory board.
type planApplyBoard interface {
	Read() (*planApplyState, error)
	DeleteLines(ids []string) error
	DeleteVias(ids []string) error
	AddLine(t planApplyTrack) error
	AddVia(v planApplyVia) error
}

type planApplyTarget struct {
	Replace map[string]bool // nets whose copper the plan owns
	Lines   []planApplyTrack
	Vias    []planApplyVia
	Skipped []string // plan nets marked failed: they end up with no copper
}

type planApplyMismatch struct {
	Net         string  `json:"net"`
	Layer       int     `json:"layer,omitempty"`
	Kind        string  `json:"kind"` // length | width | via-missing | via-extra
	ExpectedMil float64 `json:"expectedMil,omitempty"`
	ActualMil   float64 `json:"actualMil,omitempty"`
	X           float64 `json:"x,omitempty"`
	Y           float64 `json:"y,omitempty"`
}

type planApplyReport struct {
	OK              bool                `json:"ok"`
	DryRun          bool                `json:"dryRun"`
	Reconciled      bool                `json:"reconciled"`
	ReplaceNets     []string            `json:"replaceNets"`
	UnroutedNets    []string            `json:"unroutedNets,omitempty"`
	Passes          int                 `json:"passes"`
	LinesDeleted    int                 `json:"linesDeleted"`
	ViasDeleted     int                 `json:"viasDeleted"`
	LinesCreated    int                 `json:"linesCreated"`
	ViasCreated     int                 `json:"viasCreated"`
	LeftoverSweeps  int                 `json:"leftoverSweeps"` // extra deletes that removed editor-merged tracks
	OtherNetsIntact bool                `json:"otherNetsIntact"`
	Mismatches      []planApplyMismatch `json:"mismatches,omitempty"`
	Next            string              `json:"next,omitempty"`
}

const (
	planApplyLenTol   = 0.5 // mil of total length per net/layer
	planApplyDeleteN  = 40  // ids per delete call
	planApplyMaxSweep = 3
)

type planApplyNL struct {
	Net   string
	Layer int
}

func planApplyRound(v float64) float64 { return math.Round(v*10) / 10 }

func planApplyViaKey(v planApplyVia) string {
	return fmt.Sprintf("%s|%.1f|%.1f", v.Net, planApplyRound(v.X), planApplyRound(v.Y))
}

// planApplySums is total length and width-weighted length per net/layer.
func planApplySums(lines []planApplyTrack) (length, weighted map[planApplyNL]float64) {
	length, weighted = map[planApplyNL]float64{}, map[planApplyNL]float64{}
	for _, l := range lines {
		k := planApplyNL{l.Net, l.Layer}
		d := math.Hypot(l.X2-l.X1, l.Y2-l.Y1)
		length[k] += d
		weighted[k] += d * l.Width
	}
	return
}

// planApplyOtherSig fingerprints the copper plan-apply must not touch.
func planApplyOtherSig(st *planApplyState, replace map[string]bool) string {
	var keys []string
	for _, l := range st.Lines {
		if replace[l.Net] {
			continue
		}
		a := fmt.Sprintf("%.1f,%.1f", planApplyRound(l.X1), planApplyRound(l.Y1))
		b := fmt.Sprintf("%.1f,%.1f", planApplyRound(l.X2), planApplyRound(l.Y2))
		if a > b {
			a, b = b, a
		}
		keys = append(keys, fmt.Sprintf("L|%s|%d|%.1f|%s|%s", l.Net, l.Layer, planApplyRound(l.Width), a, b))
	}
	for _, v := range st.Vias {
		if !replace[v.Net] {
			keys = append(keys, "V|"+planApplyViaKey(v.planApplyVia))
		}
	}
	sort.Strings(keys)
	return strings.Join(keys, "\n")
}

// planApplyDiff compares the replace nets on the board with the target.
func planApplyDiff(st *planApplyState, t *planApplyTarget) (off map[planApplyNL]bool, viaDel []planApplyLiveVia, viaAdd []planApplyVia, mm []planApplyMismatch) {
	off = map[planApplyNL]bool{}
	var live []planApplyTrack
	for _, l := range st.Lines {
		if t.Replace[l.Net] {
			live = append(live, l.planApplyTrack)
		}
	}
	wantLen, wantW := planApplySums(t.Lines)
	gotLen, gotW := planApplySums(live)
	seen := map[planApplyNL]bool{}
	for _, m := range []map[planApplyNL]float64{wantLen, gotLen} {
		for k := range m {
			if seen[k] {
				continue
			}
			seen[k] = true
			switch {
			case math.Abs(wantLen[k]-gotLen[k]) > planApplyLenTol:
				off[k] = true
				mm = append(mm, planApplyMismatch{Net: k.Net, Layer: k.Layer, Kind: "length", ExpectedMil: planApplyRound(wantLen[k]), ActualMil: planApplyRound(gotLen[k])})
			case math.Abs(wantW[k]-gotW[k]) > planApplyLenTol*math.Max(1, wantLen[k]/10):
				off[k] = true
				// report the mean width, which is what a reader can act on
				mm = append(mm, planApplyMismatch{Net: k.Net, Layer: k.Layer, Kind: "width", ExpectedMil: planApplyRound(wantW[k] / math.Max(wantLen[k], 1e-9)), ActualMil: planApplyRound(gotW[k] / math.Max(gotLen[k], 1e-9))})
			}
		}
	}
	want := map[string]planApplyVia{}
	for _, v := range t.Vias {
		want[planApplyViaKey(v)] = v
	}
	have := map[string]bool{}
	for _, v := range st.Vias {
		if !t.Replace[v.Net] {
			continue
		}
		k := planApplyViaKey(v.planApplyVia)
		if _, ok := want[k]; !ok || have[k] { // not in the plan, or a duplicate on the same spot
			viaDel = append(viaDel, v)
			mm = append(mm, planApplyMismatch{Net: v.Net, Kind: "via-extra", X: v.X, Y: v.Y})
			continue
		}
		have[k] = true
	}
	for k, v := range want {
		if !have[k] {
			viaAdd = append(viaAdd, v)
			mm = append(mm, planApplyMismatch{Net: v.Net, Kind: "via-missing", X: v.X, Y: v.Y})
		}
	}
	sort.Slice(viaAdd, func(i, j int) bool { return planApplyViaKey(viaAdd[i]) < planApplyViaKey(viaAdd[j]) })
	sort.Slice(mm, func(i, j int) bool {
		if mm[i].Net != mm[j].Net {
			return mm[i].Net < mm[j].Net
		}
		if mm[i].Layer != mm[j].Layer {
			return mm[i].Layer < mm[j].Layer
		}
		return mm[i].Kind < mm[j].Kind
	})
	return
}

func planApplyChunks(ids []string, fn func([]string) error) error {
	for i := 0; i < len(ids); i += planApplyDeleteN {
		if err := fn(ids[i:min(i+planApplyDeleteN, len(ids))]); err != nil {
			return err
		}
	}
	return nil
}

// runPlanApply drives the board to the target. It returns a report even on
// failure; err is non-nil whenever the board could not be proven to match.
func runPlanApply(b planApplyBoard, t *planApplyTarget, maxPasses int, dryRun bool) (*planApplyReport, error) {
	rep := &planApplyReport{DryRun: dryRun, UnroutedNets: append([]string(nil), t.Skipped...)}
	for n := range t.Replace {
		rep.ReplaceNets = append(rep.ReplaceNets, n)
	}
	sort.Strings(rep.ReplaceNets)
	sort.Strings(rep.UnroutedNets)
	st, err := b.Read()
	if err != nil {
		return rep, err
	}
	for _, n := range rep.ReplaceNets {
		if st.Arcs[n] > 0 {
			return rep, fmt.Errorf("net %s has %d arc track(s); plan-apply only rewrites straight tracks — remove or convert the arcs first", n, st.Arcs[n])
		}
	}
	baseline := planApplyOtherSig(st, t.Replace)
	rep.OtherNetsIntact = true
	for pass := 0; ; pass++ {
		off, viaDel, viaAdd, mm := planApplyDiff(st, t)
		rep.Mismatches = mm
		if len(mm) == 0 {
			rep.Reconciled = true
			break
		}
		if dryRun {
			for _, l := range st.Lines {
				if off[planApplyNL{l.Net, l.Layer}] {
					rep.LinesDeleted++
				}
			}
			for _, l := range t.Lines {
				if off[planApplyNL{l.Net, l.Layer}] {
					rep.LinesCreated++
				}
			}
			rep.ViasDeleted, rep.ViasCreated = len(viaDel), len(viaAdd)
			break
		}
		if pass >= maxPasses {
			break
		}
		rep.Passes = pass + 1
		var locked []string
		for _, l := range st.Lines {
			if l.Locked && off[planApplyNL{l.Net, l.Layer}] {
				locked = append(locked, l.Net)
			}
		}
		for _, v := range viaDel {
			if v.Locked {
				locked = append(locked, v.Net)
			}
		}
		if len(locked) > 0 {
			sort.Strings(locked)
			return rep, fmt.Errorf("copper to replace is locked (first net: %s); unlock it explicitly, e.g. `easyeda pcb track-lock --all --unlock`, then run plan-apply again", locked[0])
		}
		// clear every net/layer that is off; sweep again for tracks the editor merged while we deleted
		for sweep := 0; sweep <= planApplyMaxSweep; sweep++ {
			var ids []string
			for _, l := range st.Lines {
				if off[planApplyNL{l.Net, l.Layer}] {
					ids = append(ids, l.ID)
				}
			}
			if len(ids) == 0 {
				break
			}
			if sweep == planApplyMaxSweep {
				return rep, fmt.Errorf("%d track(s) of the nets being replaced keep reappearing after %d delete sweeps", len(ids), planApplyMaxSweep)
			}
			if sweep > 0 {
				rep.LeftoverSweeps++
			}
			if err := planApplyChunks(ids, b.DeleteLines); err != nil {
				return rep, err
			}
			rep.LinesDeleted += len(ids)
			if st, err = b.Read(); err != nil {
				return rep, err
			}
		}
		var vids []string
		for _, v := range viaDel {
			vids = append(vids, v.ID)
		}
		if err := planApplyChunks(vids, b.DeleteVias); err != nil {
			return rep, err
		}
		rep.ViasDeleted += len(vids)
		for _, l := range t.Lines {
			if off[planApplyNL{l.Net, l.Layer}] {
				if err := b.AddLine(l); err != nil {
					return rep, err
				}
				rep.LinesCreated++
			}
		}
		for _, v := range viaAdd {
			if err := b.AddVia(v); err != nil {
				return rep, err
			}
			rep.ViasCreated++
		}
		if st, err = b.Read(); err != nil {
			return rep, err
		}
	}
	if !dryRun && planApplyOtherSig(st, t.Replace) != baseline {
		rep.OtherNetsIntact = false
		return rep, fmt.Errorf("copper outside the replaced nets changed while applying; inspect the board before saving")
	}
	if dryRun {
		rep.OK = true
		return rep, nil
	}
	if !rep.Reconciled {
		return rep, fmt.Errorf("board does not match the plan after %d pass(es): %d mismatch(es), first: net %s %s", rep.Passes, len(rep.Mismatches), rep.Mismatches[0].Net, rep.Mismatches[0].Kind)
	}
	rep.OK = true
	rep.Next = "not saved: run `easyeda pcb pour-rebuild`, `easyeda pcb save`, `easyeda doc reload`, then `easyeda pcb drc`"
	return rep, nil
}

// ── plan file ────────────────────────────────────────────────────────────────

type planApplyFileNet struct {
	Net    string  `json:"net"`
	Width  float64 `json:"width"`
	Failed bool    `json:"failed"`
	Lines  []struct {
		Layer          int
		X1, Y1, X2, Y2 float64
	} `json:"lines"`
	Vias [][2]float64 `json:"vias"`
}

// parsePlanApplyLayerMap reads "0=1,1=2,2=16": plan layer index → EasyEDA layer id.
func parsePlanApplyLayerMap(s string) (map[int]int, error) {
	m := map[int]int{}
	for _, part := range strings.Split(s, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("--layer-map entry %q is not index=layerId", part)
		}
		k, err1 := strconv.Atoi(strings.TrimSpace(kv[0]))
		v, err2 := strconv.Atoi(strings.TrimSpace(kv[1]))
		if err1 != nil || err2 != nil || v <= 0 {
			return nil, fmt.Errorf("--layer-map entry %q is not index=layerId", part)
		}
		m[k] = v
	}
	return m, nil
}

// loadPlanApplyTarget reads a routing plan (one entry per net: width, straight
// lines on plan layer indices, via centres) and the nets whose copper it replaces.
// With replace empty the plan's own nets are replaced.
func loadPlanApplyTarget(path string, layerMap map[int]int, replace []string) (*planApplyTarget, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var nets []planApplyFileNet
	if err := json.Unmarshal(raw, &nets); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(nets) == 0 {
		return nil, fmt.Errorf("%s: plan has no nets", path)
	}
	t := &planApplyTarget{Replace: map[string]bool{}}
	inPlan := map[string]bool{}
	for _, n := range nets {
		if strings.TrimSpace(n.Net) == "" {
			return nil, fmt.Errorf("%s: a plan entry has no net name", path)
		}
		if inPlan[n.Net] {
			return nil, fmt.Errorf("%s: net %s appears twice", path, n.Net)
		}
		inPlan[n.Net] = true
		if n.Failed {
			t.Skipped = append(t.Skipped, n.Net)
			continue
		}
		if len(n.Lines) > 0 && n.Width <= 0 {
			return nil, fmt.Errorf("%s: net %s has tracks but no width", path, n.Net)
		}
		for _, l := range n.Lines {
			id, ok := layerMap[l.Layer]
			if !ok {
				return nil, fmt.Errorf("%s: net %s uses plan layer %d, which --layer-map does not cover", path, n.Net, l.Layer)
			}
			if l.X1 == l.X2 && l.Y1 == l.Y2 {
				continue
			}
			t.Lines = append(t.Lines, planApplyTrack{Net: n.Net, Layer: id, X1: l.X1, Y1: l.Y1, X2: l.X2, Y2: l.Y2, Width: n.Width})
		}
		seen := map[string]bool{}
		for _, v := range n.Vias {
			pv := planApplyVia{Net: n.Net, X: v[0], Y: v[1]}
			if k := planApplyViaKey(pv); !seen[k] {
				seen[k] = true
				t.Vias = append(t.Vias, pv)
			}
		}
	}
	if len(replace) == 0 {
		for n := range inPlan {
			t.Replace[n] = true
		}
		return t, nil
	}
	for _, n := range replace {
		if strings.TrimSpace(n) == "" {
			return nil, fmt.Errorf("--replace-nets contains an empty net name")
		}
		t.Replace[n] = true
	}
	for n := range inPlan {
		if !t.Replace[n] {
			return nil, fmt.Errorf("plan net %s is not in --replace-nets; its old copper would stay next to the new", n)
		}
	}
	return t, nil
}
