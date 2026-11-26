package app

// route-diagnose explains why nets stay unrouted after the official router ran.
// The unrouted set comes only from the native DRC "Connection Error" rows; the
// command does not rebuild whole-net connectivity. For every unrouted net it
// floods each netted SMD pad with the same geometry and rule kernel as
// routability and reports whether a pad is locally trapped (and by what) or
// every pad can leave, which puts the break in the channels between pads.
import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const pcbDrcConnectionRule = "Connection Error"

// Net reasons.
const (
	pcbRouteDiagnoseTrapped = "trapped"
	pcbRouteDiagnoseOpen    = "not-locally-trapped"
	pcbRouteDiagnoseUnknown = "unknown"
)

type pcbRouteDiagnoseOptions struct {
	StepMil   float64
	RadiusMil float64
	Nets      []string // optional net filter; empty = every unrouted net
}

type pcbRouteDiagnoseNet struct {
	Net    string `json:"net"`
	Rows   int    `json:"rows"`   // Connection Error rows of this net
	Reason string `json:"reason"` // trapped | not-locally-trapped | unknown
	// Detail explains an unknown reason.
	Detail string `json:"detail,omitempty"`
	// PadsChecked counts flooded SMD pads; ThroughHolePads are counted only.
	PadsChecked     int `json:"padsChecked"`
	ThroughHolePads int `json:"throughHolePads"`
	// Pads are the pads that explain the reason: trapped pads for trapped,
	// unverified pads for unknown, every flooded pad for not-locally-trapped.
	Pads []pcbRoutabilityPad `json:"pads"`
	// Blockers is the union of the trapped pads' blockers.
	Blockers []pcbRoutabilityBlocker `json:"blockers"`
	// DrcObjs is the union of primitive ids the net's Connection Error rows name.
	DrcObjs []string `json:"drcObjs"`
}

type pcbRouteDiagnoseReport struct {
	SchemaVersion       int                      `json:"schemaVersion"`
	Status              string                   `json:"status"` // pass | fail | incomplete
	Reason              string                   `json:"reason,omitempty"`
	BoardSemanticSHA256 string                   `json:"boardSemanticSha256,omitempty"`
	BoardSHA256         string                   `json:"boardSha256,omitempty"`
	DRCSHA256           string                   `json:"drcSha256,omitempty"`
	Parameters          pcbRoutabilityParameters `json:"parameters"`
	ConnectionRows      int                      `json:"connectionErrorRows"`
	// UnattributedRows are Connection Error rows with no net name; they cannot be
	// diagnosed, and without a --net filter they make the result incomplete.
	UnattributedRows int                   `json:"unattributedRows,omitempty"`
	Nets             []pcbRouteDiagnoseNet `json:"nets"`
	Limitations      []string              `json:"limitations"`
}

// loadPCBDrcFlatReport reads `pcb drc --json` output. A file without the
// violations array (a dump, a raw envelope) is refused: reading it as "no
// violations" would turn a wrong input into a pass.
func loadPCBDrcFlatReport(raw []byte) (drcFlatReport, error) {
	var in struct {
		Violations *[]drcFlatViolation `json:"violations"`
		Total      *int                `json:"total"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return drcFlatReport{}, fmt.Errorf("parse DRC report: %w", err)
	}
	if in.Violations == nil {
		return drcFlatReport{}, fmt.Errorf("DRC report has no violations array: pass the output of `easyeda pcb drc --json`")
	}
	if in.Total != nil && *in.Total != len(*in.Violations) {
		return drcFlatReport{}, fmt.Errorf("DRC report is inconsistent: total=%d but %d violation rows", *in.Total, len(*in.Violations))
	}
	return drcFlatReport{Violations: *in.Violations}, nil
}

type pcbRouteDiagnoseRows struct {
	rows int
	objs map[string]bool
}

func pcbRouteDiagnoseSortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func diagnosePCBRoutes(snap *boardSnapshot, drc drcFlatReport, opt pcbRouteDiagnoseOptions) (pcbRouteDiagnoseReport, error) {
	rep := pcbRouteDiagnoseReport{SchemaVersion: 1, Status: "incomplete", Nets: []pcbRouteDiagnoseNet{}, Parameters: pcbRoutabilityParameters{StepMil: opt.StepMil, RadiusMil: opt.RadiusMil}, Limitations: []string{
		"DRC and the board dump must come from the same saved and reloaded state (save, doc reload, then pcb drc --json and a fresh pcb dump --include-copper). The command cannot verify this: a stale pair gives wrong or missing blockers.",
		"Unrouted nets are the nets of the native Connection Error rows; this command does not rebuild whole-net connectivity.",
		"not-locally-trapped means every netted SMD pad of the net has a track escape or a legal via site at this grid, so the break is in the channels between pads. It does not say which channel is blocked; drcObjs lists the objects the DRC rows name.",
		"Pad analysis is the routability local necessary condition (fixed grid, minimum track width, measured rules); a pad that is not trapped may still lose its only exit to a neighbour's copper.",
		"Through-hole pads are counted, not flooded.",
	}}
	rep.BoardSemanticSHA256, _ = moduleCheckSemanticSHA256(snap)
	incomplete := func(msg string) (pcbRouteDiagnoseReport, error) {
		rep.Status, rep.Reason = "incomplete", msg
		return rep, fmt.Errorf("%s", msg)
	}
	if err := validatePCBRoutabilityScan(opt.StepMil, opt.RadiusMil); err != nil {
		return incomplete(err.Error())
	}
	filter := map[string]bool{}
	for _, n := range opt.Nets {
		if strings.TrimSpace(n) == "" {
			return incomplete("--net contains an empty net name")
		}
		filter[n] = true
	}
	byNet := map[string]*pcbRouteDiagnoseRows{}
	for _, v := range drc.Violations {
		if v.Rule != pcbDrcConnectionRule {
			continue
		}
		rep.ConnectionRows++
		if strings.TrimSpace(v.Net) == "" {
			rep.UnattributedRows++
			continue
		}
		if len(filter) > 0 && !filter[v.Net] {
			continue
		}
		r := byNet[v.Net]
		if r == nil {
			r = &pcbRouteDiagnoseRows{objs: map[string]bool{}}
			byNet[v.Net] = r
		}
		r.rows++
		for _, id := range v.Objs {
			r.objs[id] = true
		}
	}
	var missing []string
	for n := range filter {
		if byNet[n] == nil {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		rep.Limitations = append(rep.Limitations, "No Connection Error rows for requested net(s), so they are treated as routed: "+strings.Join(missing, ", ")+". Check the spelling against the DRC report.")
	}
	if rep.UnattributedRows > 0 && len(filter) > 0 {
		rep.Limitations = append(rep.Limitations, fmt.Sprintf("%d Connection Error row(s) carry no net name and were not diagnosed (outside the --net filter scope).", rep.UnattributedRows))
	}
	names := make([]string, 0, len(byNet))
	for n := range byNet {
		names = append(names, n)
	}
	sort.Strings(names)
	if len(names) == 0 {
		if rep.UnattributedRows > 0 && len(filter) == 0 {
			return incomplete(fmt.Sprintf("%d Connection Error row(s) carry no net name and cannot be diagnosed", rep.UnattributedRows))
		}
		rep.Status = "pass"
		return rep, nil
	}
	nets := make([]pcbRouteDiagnoseNet, len(names))
	for i, n := range names {
		nets[i] = pcbRouteDiagnoseNet{Net: n, Rows: byNet[n].rows, DrcObjs: pcbRouteDiagnoseSortedKeys(byNet[n].objs), Pads: []pcbRoutabilityPad{}, Blockers: []pcbRoutabilityBlocker{}}
	}
	finish := func() (pcbRouteDiagnoseReport, error) {
		rep.Nets = nets
		var trapped, open, unknown int
		for _, n := range nets {
			switch n.Reason {
			case pcbRouteDiagnoseTrapped:
				trapped++
			case pcbRouteDiagnoseOpen:
				open++
			default:
				unknown++
			}
		}
		rep.Reason = fmt.Sprintf("%d unrouted net(s): %d trapped, %d not-locally-trapped, %d unknown", len(nets), trapped, open, unknown)
		if unknown > 0 || rep.UnattributedRows > 0 && len(filter) == 0 {
			rep.Status = "incomplete"
			if rep.UnattributedRows > 0 && len(filter) == 0 {
				rep.Reason += fmt.Sprintf("; %d Connection Error row(s) carry no net name", rep.UnattributedRows)
			}
		} else {
			rep.Status = "fail"
		}
		return rep, fmt.Errorf("%s", rep.Reason)
	}
	geo, err := preparePCBRoutabilityGeometry(snap, opt.StepMil, opt.RadiusMil)
	if err != nil {
		for i := range nets {
			nets[i].Reason, nets[i].Detail = pcbRouteDiagnoseUnknown, "geometry incomplete: "+err.Error()
		}
		rep.Nets = nets
		return incomplete(err.Error())
	}
	rep.Parameters = geo.params
	// Flood every netted SMD pad of the unrouted nets in one parallel batch.
	index := map[string]int{}
	for i, n := range nets {
		index[n.Net] = i
	}
	var pads []pcbPadP
	padNet := []int{}
	for _, pad := range geo.ctx.allPads {
		i, ok := index[pad.Net]
		if !ok {
			continue
		}
		switch {
		case pad.Layer == pcbLayerMulti:
			nets[i].ThroughHolePads++
		case pad.Layer == 1 || pad.Layer == 2:
			pads = append(pads, pad)
			padNet = append(padNet, i)
		default:
			if nets[i].Detail == "" {
				nets[i].Detail = fmt.Sprintf("pad %s.%s is on unsupported layer %d", pad.Designator, pad.Number, pad.Layer)
			}
		}
	}
	results, errs := geo.floodPads(pads)
	type tally struct {
		trapped, unverified, all []pcbRoutabilityPad
		err                      error
	}
	tallies := make([]tally, len(nets))
	for k, result := range results {
		t := &tallies[padNet[k]]
		if errs[k] != nil {
			if t.err == nil {
				t.err = errs[k]
			}
			continue
		}
		nets[padNet[k]].PadsChecked++
		t.all = append(t.all, result)
		switch result.Status {
		case "trapped":
			t.trapped = append(t.trapped, result)
		case "unverified":
			t.unverified = append(t.unverified, result)
		}
	}
	byPad := func(p []pcbRoutabilityPad) {
		sort.SliceStable(p, func(i, j int) bool { return p[i].Pad < p[j].Pad })
	}
	for i := range nets {
		n, t := &nets[i], &tallies[i]
		byPad(t.trapped)
		byPad(t.unverified)
		byPad(t.all)
		switch {
		case len(t.trapped) > 0:
			// A trapped pad is a witness on its own; another pad's geometry
			// error is kept as detail instead of hiding it.
			n.Reason, n.Pads = pcbRouteDiagnoseTrapped, t.trapped
			if t.err != nil {
				n.Detail = "another pad's geometry is incomplete: " + t.err.Error()
			}
			seen := map[string]bool{}
			for _, p := range t.trapped {
				for _, b := range p.Blockers {
					if k := b.Kind + "|" + b.ID + "|" + b.Ref; !seen[k] {
						seen[k] = true
						n.Blockers = append(n.Blockers, b)
					}
				}
			}
			sort.Slice(n.Blockers, func(a, b int) bool {
				x, y := n.Blockers[a], n.Blockers[b]
				if x.Kind != y.Kind {
					return x.Kind < y.Kind
				}
				return x.Ref+x.ID < y.Ref+y.ID
			})
		case t.err != nil:
			n.Reason, n.Detail = pcbRouteDiagnoseUnknown, "pad geometry incomplete: "+t.err.Error()
		case len(t.unverified) > 0:
			n.Reason, n.Pads = pcbRouteDiagnoseUnknown, t.unverified
			n.Detail = fmt.Sprintf("%d pad(s) unverified (an unproven same-net transition is the only exit, or no grid point lands on the pad copper)", len(t.unverified))
		case n.Detail != "":
			n.Reason = pcbRouteDiagnoseUnknown
		case n.PadsChecked+n.ThroughHolePads == 0:
			n.Reason, n.Detail = pcbRouteDiagnoseUnknown, "the board dump has no pads on this net: DRC and dump are probably from different states, or the net name differs"
		default:
			n.Reason, n.Pads = pcbRouteDiagnoseOpen, t.all
		}
	}
	return finish()
}
