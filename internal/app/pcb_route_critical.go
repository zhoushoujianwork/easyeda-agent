package app

// pcb_route_critical.go — the P7.0 critical-net-first flow as ONE command
// (issue #127). The design-flow ladder says: power gets COPPER AREA first
// (planes on 4-layer, pours on 2-layer), differential pairs get routed
// deterministically and LOCKED, and only the remaining ordinary signals go to
// the auto-route tier. That flow used to be hand-assembled from power-planes /
// manual pcb line / track-lock commands — so it got skipped (#43 R2).
//
//	easyeda pcb route-critical            # power → diff → lock
//	easyeda pcb route-critical --dry-run  # plan + pair identification only
//	easyeda pcb track-lock --net USB_DP,USB_DM        # standalone lock
//
// Diff pairs come from TWO sources, deduplicated:
//   - the circuit-block library's `signals` maps (type=diff_pair — USB_D 90Ω,
//     RS485_AB 120Ω …): declarative, carries impedance + length_match;
//   - a conservative name-pattern scan of the LIVE nets (X_DP/X_DM, X_P/X_N,
//     X+/X−) for boards without block provenance.
//
// v1 scope: pairs are routed with the existing short-route planner (same-layer
// L-hops, 45° corners, obstacle-aware) net-by-net, then MEASURED — total length
// per side, skew vs the pair's budget (block length_match_mm, default 5 mil).
// Out-of-budget skew is REPORTED loudly, not serpentine-tuned (the pairs this
// project routes are connector→chip short runs where "成对、尽量短、≤5mil skew"
// is the spec — issue #127). True coupled/serpentine routing stays on the
// roadmap.

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zhoushoujianwork/easyeda-agent/internal/blocks"
)

// ── diff-pair identification ────────────────────────────────────────────────

type rcDiffPair struct {
	Name         string  `json:"name"`
	NetP         string  `json:"netP"`
	NetN         string  `json:"netN"`
	Source       string  `json:"source"` // "block:<id>" | "name-pattern"
	ImpedanceOhm float64 `json:"impedanceOhm,omitempty"`
	SkewLimitMil float64 `json:"skewLimitMil"`
}

const rcDefaultSkewMil = 5.0
const milPerMM = 39.3701

// rcPairSuffixes are the conservative name-pattern transforms: a net ending in
// the P-form pairs with the same stem ending in the N-form. Longest first so
// "_DP" wins over "P".
var rcPairSuffixes = []struct{ p, n string }{
	{"_DP", "_DM"},
	{"DP", "DM"},
	{"_P", "_N"},
	{"D+", "D-"},
	{"+", "-"},
}

// identifyDiffPairsByName pattern-pairs the live net list. Case-insensitive;
// returns pairs with the ORIGINAL net spellings.
func identifyDiffPairsByName(liveNets []string) []rcDiffPair {
	byUpper := map[string]string{}
	for _, n := range liveNets {
		byUpper[strings.ToUpper(n)] = n
	}
	seen := map[string]bool{}
	var out []rcDiffPair
	var uppers []string
	for u := range byUpper {
		uppers = append(uppers, u)
	}
	sort.Strings(uppers) // deterministic
	for _, u := range uppers {
		for _, sfx := range rcPairSuffixes {
			if !strings.HasSuffix(u, sfx.p) {
				continue
			}
			stem := strings.TrimSuffix(u, sfx.p)
			partner := stem + sfx.n
			if pn, ok := byUpper[partner]; ok {
				key := u + "|" + partner
				if seen[key] {
					break
				}
				seen[key] = true
				name := strings.Trim(stem, "_")
				if name == "" {
					name = byUpper[u] + "/" + pn
				}
				out = append(out, rcDiffPair{
					Name: name, NetP: byUpper[u], NetN: pn,
					Source: "name-pattern", SkewLimitMil: rcDefaultSkewMil,
				})
				break
			}
		}
	}
	return out
}

// identifyDiffPairsFromBlocks resolves the block library's diff_pair signal
// declarations against the live nets. A 2-net group pairs directly (RS485 A/B);
// a larger group (USB hub DN1_DP…DN4_DM) pairs by the name patterns within it.
func identifyDiffPairsFromBlocks(liveNets []string) []rcDiffPair {
	byUpper := map[string]string{}
	for _, n := range liveNets {
		byUpper[strings.ToUpper(n)] = n
	}
	all, err := blocks.Load()
	if err != nil {
		return nil
	}
	var out []rcDiffPair
	for _, b := range all {
		var doc struct {
			Signals map[string]struct {
				Nets          []string `json:"nets"`
				Type          string   `json:"type"`
				ImpedanceOhm  float64  `json:"impedance_ohm"`
				LengthMatchMM float64  `json:"length_match_mm"`
			} `json:"signals"`
		}
		if json.Unmarshal(b.Raw, &doc) != nil {
			continue
		}
		var sigNames []string
		for name := range doc.Signals {
			sigNames = append(sigNames, name)
		}
		sort.Strings(sigNames) // deterministic
		for _, sigName := range sigNames {
			sig := doc.Signals[sigName]
			if sig.Type != "diff_pair" {
				continue
			}
			// Resolve declared names against live nets.
			var live []string
			for _, n := range sig.Nets {
				if ln, ok := byUpper[strings.ToUpper(strings.TrimSpace(n))]; ok {
					live = append(live, ln)
				}
			}
			skew := rcDefaultSkewMil
			if sig.LengthMatchMM > 0 {
				skew = sig.LengthMatchMM * milPerMM
			}
			emit := func(p, n string) {
				out = append(out, rcDiffPair{
					Name: sigName, NetP: p, NetN: n,
					Source: "block:" + b.ID, ImpedanceOhm: sig.ImpedanceOhm, SkewLimitMil: skew,
				})
			}
			if len(live) == 2 {
				emit(live[0], live[1])
				continue
			}
			if len(live) > 2 {
				// Pair within the group by the P/N suffix patterns.
				for _, sub := range identifyDiffPairsByName(live) {
					out = append(out, rcDiffPair{
						Name: sigName + ":" + sub.Name, NetP: sub.NetP, NetN: sub.NetN,
						Source: "block:" + b.ID, ImpedanceOhm: sig.ImpedanceOhm, SkewLimitMil: skew,
					})
				}
			}
		}
	}
	return out
}

// identifyDiffPairs merges the block-informed and name-pattern sources; the
// block source wins on metadata (impedance / skew budget) when both find the
// same net pair.
func identifyDiffPairs(liveNets []string) []rcDiffPair {
	pairKey := func(p rcDiffPair) string {
		a, b := strings.ToUpper(p.NetP), strings.ToUpper(p.NetN)
		if a > b {
			a, b = b, a
		}
		return a + "|" + b
	}
	seen := map[string]bool{}
	var out []rcDiffPair
	for _, p := range identifyDiffPairsFromBlocks(liveNets) {
		if k := pairKey(p); !seen[k] {
			seen[k] = true
			out = append(out, p)
		}
	}
	for _, p := range identifyDiffPairsByName(liveNets) {
		if k := pairKey(p); !seen[k] {
			seen[k] = true
			out = append(out, p)
		}
	}
	return out
}

// ── pair routing + measurement ──────────────────────────────────────────────

type rcPairResult struct {
	rcDiffPair
	Status     string    `json:"status"` // routed | already-routed | partial | unroutable
	Segments   int       `json:"segments"`
	LenPMil    float64   `json:"lenPMil"`
	LenNMil    float64   `json:"lenNMil"`
	SkewMil    float64   `json:"skewMil"`
	WithinSkew bool      `json:"withinSkew"`
	Diags      []string  `json:"diags,omitempty"`
	Segs       []rtSeg   `json:"-"`
	Vias       []rtVia   `json:"-"`
	Notes      []string  `json:"notes,omitempty"`
}

func rtSegLen(s rtSeg) float64 { return math.Hypot(s.X2-s.X1, s.Y2-s.Y1) }

// planPairRoute routes ONE pair with the short-route planner by masking every
// other net as already-routed. Returns the plan + per-net lengths.
func planPairRoute(comps []apComp, pair rcDiffPair, routed map[string]bool, opt rtOptions) rcPairResult {
	res := rcPairResult{rcDiffPair: pair}
	mask := map[string]bool{}
	for k, v := range routed {
		mask[k] = v
	}
	// Collect every live net from pads; mark all but the pair as routed so the
	// planner touches ONLY these two nets.
	want := map[string]bool{strings.ToUpper(pair.NetP): true, strings.ToUpper(pair.NetN): true}
	for _, c := range comps {
		for _, p := range c.pads {
			n := strings.TrimSpace(p.net)
			if n == "" || want[strings.ToUpper(n)] {
				continue
			}
			mask[n] = true
		}
	}
	if routed[pair.NetP] || routed[pair.NetN] {
		res.Status = "already-routed"
		return res
	}
	segs, vias, diags := planShortRoutes(comps, mask, opt)
	res.Segs, res.Vias = segs, vias
	res.Segments = len(segs)
	for _, s := range segs {
		switch strings.ToUpper(s.Net) {
		case strings.ToUpper(pair.NetP):
			res.LenPMil += rtSegLen(s)
		case strings.ToUpper(pair.NetN):
			res.LenNMil += rtSegLen(s)
		}
	}
	for _, d := range diags {
		res.Diags = append(res.Diags, fmt.Sprintf("%s: %s", d.Net, d.Reason))
	}
	res.SkewMil = math.Abs(res.LenPMil - res.LenNMil)
	res.WithinSkew = res.SkewMil <= pair.SkewLimitMil
	switch {
	case res.LenPMil > 0 && res.LenNMil > 0:
		res.Status = "routed"
	case res.LenPMil > 0 || res.LenNMil > 0:
		res.Status = "partial"
	default:
		res.Status = "unroutable"
	}
	if !res.WithinSkew && res.Status == "routed" {
		res.Notes = append(res.Notes, fmt.Sprintf(
			"skew %.1fmil exceeds the %.1fmil budget — shorten the longer side or equalize by hand (v1 does not serpentine-tune)",
			res.SkewMil, res.SkewLimitMil))
	}
	res.LenPMil, res.LenNMil, res.SkewMil = round1(res.LenPMil), round1(res.LenNMil), round1(res.SkewMil)
	return res
}

// ── commands ────────────────────────────────────────────────────────────────

func splitCSVList(items []string) []string {
	var out []string
	for _, s := range items {
		for _, p := range strings.Split(s, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// newPcbRouteCriticalCmd builds `pcb route-critical` — the P7.0 orchestrator.
func newPcbRouteCriticalCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var (
		skipPower, skipDiff, noLock, dryRun bool
		forceReason, forceUnsafeReason     string
	)
	c := &cobra.Command{
		Use:   "route-critical",
		Short: "P7.0 critical nets first: power copper → diff pairs routed+measured → locked (issue #127)",
		Long: `One command for the design-flow P7.0 ladder — the two net classes an
auto-router handles worst are done deterministically FIRST, then locked:

  1. power   4+ copper layers → 'pcb power-planes' (inner GND plane + power layer)
             2 layers         → 'pcb power-pour'   (GND both sides + local rail pours)
  2. diff    pairs identified from the circuit-block signals maps (USB_D 90Ω,
             RS485_AB 120Ω … with their length_match budgets) plus a conservative
             name-pattern scan (X_DP/X_DM, X_P/X_N, X+/X−); each pair routed with
             45° corners on its pads' layer, lengths measured, skew checked
             against the pair's budget (default 5mil) — out-of-budget is REPORTED,
             not silently accepted (v1 does not serpentine-tune);
  3. lock    'pcb.track.lock' on every routed pair net, so the later auto-route /
             rip-up tier cannot destroy the guaranteed copper.

Then hand the REST to the normal tier (route-short / user-clicked native
auto-route per the P7 ladder). Same stage gate as route-short; --dry-run plans
and identifies without mutating.`,
		Example: `  easyeda pcb route-critical --project ceshi --dry-run
  easyeda pcb route-critical --project ceshi
  easyeda pcb route-critical --project ceshi --skip-power   # pairs only`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !dryRun {
				if err := gateRouteCommand(cfg, *window, "route-critical", forceReason, forceUnsafeReason, stderr); err != nil {
					return err
				}
			}
			out := map[string]any{"ok": true, "dryRun": dryRun}

			// ── 1. power ───────────────────────────────────────────────────
			if skipPower {
				out["power"] = "skipped (--skip-power)"
			} else {
				copper := copperLayerCount(cfg, *window)
				if copper >= 4 {
					out["power"] = "power-planes (4-layer)"
					if err := runPowerPlanes(cfg, *window, 15, 16, true, dryRun, stderr, stderr); err != nil {
						return fmt.Errorf("power step (power-planes): %w", err)
					}
				} else {
					out["power"] = "power-pour (2-layer)"
					if err := runPowerPour(cfg, *window, "both", "pour", railMargin, 0, true, true, dryRun, stderr, stderr); err != nil {
						return fmt.Errorf("power step (power-pour): %w", err)
					}
				}
			}

			// ── 2. diff pairs ──────────────────────────────────────────────
			var pairResults []rcPairResult
			var lockNets []string
			if skipDiff {
				out["diff"] = "skipped (--skip-diff)"
			} else {
				res, err := requestAction(cfg, "pcb.components.list", *window, map[string]any{"includePads": true})
				if err != nil {
					return err
				}
				comps := parseApComps(res.Result)
				liveNets := map[string]bool{}
				for _, c := range comps {
					for _, p := range c.pads {
						if n := strings.TrimSpace(p.net); n != "" {
							liveNets[n] = true
						}
					}
				}
				var netList []string
				for n := range liveNets {
					netList = append(netList, n)
				}
				sort.Strings(netList)
				pairs := identifyDiffPairs(netList)

				routed := map[string]bool{}
				if lr, err := requestAction(cfg, "pcb.line.list", *window, nil); err == nil {
					routed = parseRoutedNets(lr.Result)
				}
				rules := fetchPcbRules(cfg, *window)
				opt := defaultRtOptions()
				opt.signalWidth = rules.clampWidth(rules.trackWidthMil)
				opt.netClassWidths = netClassWidthTable(rules)
				opt.corner = "45"       // chamfered corners — diff-pair hygiene
				opt.multilayer = false  // pairs stay on their pads' layer (vias hurt the pair)
				opt.skipPower = true
				opt.avoid = true
				opt.clearance = rules.clearanceMil
				if bt, err := fetchPcbTracks(cfg, *window); err == nil {
					for _, t := range bt {
						opt.existing = append(opt.existing, rtSeg{Net: t.Net, X1: t.X1, Y1: t.Y1, X2: t.X2, Y2: t.Y2, Layer: t.Layer, Width: t.Width})
					}
				}
				if bv, err := fetchPcbVias(cfg, *window); err == nil {
					for _, v := range bv {
						opt.existingVias = append(opt.existingVias, obVia{net: v.Net, x: v.X, y: v.Y, r: v.Dia / 2})
					}
				}
				if sl, err := fetchPcbSlots(cfg, *window); err == nil {
					opt.slots = sl
				}

				drawnTotal := 0
				var failures []map[string]any
				for _, pair := range pairs {
					pr := planPairRoute(comps, pair, routed, opt)
					if !dryRun && pr.Status != "already-routed" {
						for _, s := range pr.Segs {
							payload := map[string]any{"startX": s.X1, "startY": s.Y1, "endX": s.X2, "endY": s.Y2, "net": s.Net, "layer": s.Layer}
							if s.Width > 0 {
								payload["lineWidth"] = s.Width
							}
							if _, err := requestAction(cfg, "pcb.line.create", *window, payload); err != nil {
								failures = append(failures, map[string]any{"net": s.Net, "error": err.Error()})
								continue
							}
							drawnTotal++
						}
					}
					if pr.Status == "routed" || pr.Status == "already-routed" || pr.Status == "partial" {
						lockNets = append(lockNets, pair.NetP, pair.NetN)
					}
					// The plan payload (Segs) stays out of the JSON; the summary carries the verdict.
					pairResults = append(pairResults, pr)
				}
				out["diffPairs"] = pairResults
				out["diffSegmentsDrawn"] = drawnTotal
				if len(failures) > 0 {
					out["diffFailures"] = failures
				}
				if len(pairs) == 0 {
					out["diff"] = "no diff pairs identified (blocks signals + name patterns)"
				}
			}

			// ── 3. lock ────────────────────────────────────────────────────
			if noLock || dryRun || len(lockNets) == 0 {
				if noLock {
					out["lock"] = "skipped (--no-lock)"
				} else if len(lockNets) == 0 {
					out["lock"] = "nothing to lock"
				} else {
					out["lock"] = "skipped (dry-run)"
				}
			} else {
				lres, err := requestAction(cfg, "pcb.track.lock", *window, map[string]any{"net": dedupeStrings(lockNets), "locked": true})
				if err != nil {
					fmt.Fprintf(stderr, "warning: lock step failed: %v — lock by hand: pcb track-lock --net %s\n", err, strings.Join(dedupeStrings(lockNets), ","))
					out["lock"] = "FAILED: " + err.Error()
				} else {
					out["lock"] = lres.Result
					out["lockedNets"] = dedupeStrings(lockNets)
				}
			}

			fmt.Fprintln(stderr, "next: the REMAINING ordinary signals go to the normal tier — sparse: `pcb route-short`; dense: ask the user to click native auto-route (P7 ladder)")
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(out)
		},
	}
	c.Flags().BoolVar(&skipPower, "skip-power", false, "skip the power copper step")
	c.Flags().BoolVar(&skipDiff, "skip-diff", false, "skip the diff-pair step")
	c.Flags().BoolVar(&noLock, "no-lock", false, "do not lock the routed pair nets")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "plan + identify without mutating")
	c.Flags().StringVar(&forceReason, "force", "", "bypass SOFT gate gaps only (audited, per-run) — same tiering as route-short (#132)")
	c.Flags().StringVar(&forceUnsafeReason, "force-unsafe", "", "bypass EVERYTHING incl. an unconfirmed skeleton (audited, per-run)")
	return c
}

func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// copperLayerCount counts copper layers from pcb.layers.list (SIGNAL/PLANE
// types on copper layer ids); falls back to 2 when unreadable.
func copperLayerCount(cfg *appConfig, window string) int {
	res, err := requestAction(cfg, "pcb.layers.list", window, nil)
	if err != nil {
		return 2
	}
	raw, _ := res.Result["layers"].([]any)
	n := 0
	for _, ri := range raw {
		m, ok := ri.(map[string]any)
		if !ok {
			continue
		}
		t := strings.ToUpper(asString(m["type"]))
		if t == "SIGNAL" || t == "PLANE" {
			n++
		}
	}
	if n < 2 {
		return 2
	}
	return n
}
