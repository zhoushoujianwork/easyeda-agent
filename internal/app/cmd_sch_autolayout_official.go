package app

// cmd_sch_autolayout_official.go — `sch autolayout --engine official`: the
// generic FALLBACK engine that wraps the platform's own schematic auto-layout.
//
// Our template/spec planner (--engine template, the default) produces clean
// functional-group layouts for KNOWN blocks. The official
// eda.sch_Document.autoLayout() (@beta on 3.2.148) is the generic fallback for
// un-templated pages — but a hard-won real-machine session (2026-07-20) proved
// it is DESTRUCTIVE and needs a safety pipeline around it:
//
//   1. It MOVES parts but NOT their wires/netflags → every net goes dangling
//      (16-part minsys: 59 dangling wires, 95 floating pins). It is a
//      PRE-WIRING tool; never run it bare on an already-wired page.
//   2. It places parts OFF the 5-unit grid (405.40, 363.26…) → downstream
//      autoconnect stubs can't land on the pins. Must snap-to-grid after.
//   3. Its scattered radial layout puts related pins so close that re-wiring
//      stubs collide and MERGE into shorts that --replace cannot separate.
//
// So this command: guards a wired page (refuse unless --rewire), runs the
// platform call, ALWAYS snaps to grid, optionally re-wires from the netlist it
// captured BEFORE the run, and self-checks with `sch check` (wiring), not just
// layout-lint (overlap). It goes through the debug.exec_js hatch (no connector
// re-import), same as `sch zone-draw`.

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// officialAutolayoutTimeout is the dispatch budget for the platform call. The
// operation measured ~138s; 300s leaves margin. Proven: a 200s budget returned
// at 138s (returned:true, result:{}).
const officialAutolayoutTimeout = 300 * time.Second

// officialMutateTimeout bounds the per-primitive snap/delete exec_js loops
// (16 parts + up to ~120 wires/flags = a few hundred API calls).
const officialMutateTimeout = 90 * time.Second

// runOfficialAutolayout runs eda.sch_Document.autoLayout() on the ACTIVE
// schematic page inside a safety pipeline. apply gates the real call; rewire
// enables the destroy-and-rebuild wiring path on an already-wired page.
func runOfficialAutolayout(cfg *appConfig, window string, apply, rewire bool, stdout, stderr io.Writer) error {
	// Guard: the platform lays out the ACTIVE document, so it must be a schematic
	// page and foreground. Verify via the live context before a 2-minute call.
	win, err := resolveTargetWindow(cfg, window)
	if err != nil {
		return err
	}
	cur, err := requestAction(cfg, "document.current", win, nil)
	if err != nil {
		return fmt.Errorf("read active document: %w", err)
	}
	if cur.Context == nil || cur.Context.DocumentType != "schematic" {
		dt := "unknown"
		if cur.Context != nil {
			dt = cur.Context.DocumentType
		}
		return fmt.Errorf("active document is %q, not a schematic — `easyeda doc switch <page>` to the target schematic page first (the platform lays out whatever page is foreground)", dt)
	}

	before := fetchSchObstacles(cfg, win) // real part bboxes on the active page
	wireCount, werr := countSchWires(cfg, win)
	if werr != nil {
		return fmt.Errorf("read existing wire count: %w", werr)
	}
	fmt.Fprintf(stderr, "official autolayout: %d part(s), %d wire(s) on the active page\n", len(before), wireCount)

	// Pre-guard: autoLayout destroys existing wiring. Refuse a wired page unless
	// the caller opted into the destroy-and-rebuild path.
	if wireCount > 0 && !rewire {
		return fmt.Errorf("this page already has %d wire(s) — the platform autoLayout MOVES parts without their wires and would leave them ALL dangling. "+
			"Run it BEFORE wiring, or pass --rewire to delete + rebuild the wiring from the current netlist afterward (best-effort: a scattered layout can leave residual shorts)", wireCount)
	}

	if !apply {
		fmt.Fprintf(stdout, "dry-run: would run the platform eda.sch_Document.autoLayout() over %d part(s) on this page\n", len(before))
		if rewire {
			fmt.Fprintln(stdout, "(--rewire: would then snap-to-grid, delete the current wiring, and rebuild it from the live netlist)")
		}
		fmt.Fprintln(stdout, "(the platform API has no preview — pass --apply to actually run it; it is a LONG op, ~2min, and rearranges the WHOLE active page)")
		return nil
	}

	// Capture the netlist BEFORE autoLayout so --rewire can rebuild it. Every
	// live net's pins become an autoconnect connection at the post-layout
	// positions. Done up front because autoLayout obliterates the wiring.
	var conns []acConnSpec
	if rewire {
		liveNets, _, rerr := readLiveNets(cfg, win)
		if rerr != nil {
			return fmt.Errorf("--rewire needs the pre-layout netlist but reading it failed: %w", rerr)
		}
		conns = connsFromLiveNets(liveNets)
		fmt.Fprintf(stderr, "captured %d net(s) → %d pin connection(s) to rebuild after layout\n", countNets(liveNets), len(conns))
	}

	fmt.Fprintln(stderr, "running platform autoLayout — this is a LONG operation (~2min); the editor shows a progress bar…")
	code := "await eda.sch_Document.autoLayout(); return {done:true};"
	if _, err := requestActionTimed(cfg, "debug.exec_js", win,
		map[string]any{"code": code}, officialAutolayoutTimeout); err != nil {
		return fmt.Errorf("platform autoLayout failed (it needs the schematic foreground; a background page can hang): %w", err)
	}
	fmt.Fprintln(stderr, "platform autoLayout finished")

	// ALWAYS snap to grid — the platform places parts off the 5-unit grid, which
	// breaks pin coordinates for any downstream wiring (proven: 16/16 off-grid).
	snapped, serr := snapSchPartsToGrid(cfg, win)
	if serr != nil {
		return fmt.Errorf("snap-to-grid after layout: %w", serr)
	}
	fmt.Fprintf(stderr, "snapped %d off-grid part(s) to the 5-unit grid\n", snapped)

	// Re-wire: the autoLayout left every wire dangling; delete them + the flags
	// and rebuild from the captured netlist.
	if rewire {
		dw, df, derr := deleteSchWiresAndFlags(cfg, win)
		if derr != nil {
			return fmt.Errorf("delete broken wiring before rebuild: %w", derr)
		}
		fmt.Fprintf(stderr, "cleared %d dangling wire(s) + %d flag(s); rebuilding %d connection(s)…\n", dw, df, len(conns))
		if err := runAutoconnect(cfg, win, conns, defaultAutoconnectRules(), false, false, false, false, stderr, stderr); err != nil {
			// autoconnect returns an error when some connections fail (a scattered
			// layout can make stubs collide). Report it but continue to the check —
			// the honest post-state is what matters.
			fmt.Fprintf(stderr, "rewire: %v (some pins may be unconnected — see the check below)\n", err)
		}
	}

	// Self-check: overlap (layout-lint) AND wiring (sch check) — the bare overlap
	// check is exactly what missed the destroyed wiring before.
	res, err := requestAction(cfg, "schematic.components.list", win, map[string]any{"includeBBox": true})
	if err != nil {
		return fmt.Errorf("read back positions: %w", err)
	}
	comps, perr := parseLayoutComps(res.Result)
	if perr != nil {
		return perr
	}
	kept, _ := filterLayoutComps(comps, false)
	overlaps := len(analyzeLayout(kept, 0, -1).Overlaps)

	sum, cerr := schCheckSummary(cfg, win)
	wiringLine := "wiring check unavailable"
	if cerr == nil {
		wiringLine = fmt.Sprintf("%d dangling wire(s), %d floating pin(s)", sum.DanglingWires, sum.FloatingPins)
	}

	clean := overlaps == 0 && (cerr != nil || sum.DanglingWires == 0)
	mark := "⚠"
	if clean {
		mark = "✓"
	}
	fmt.Fprintf(stdout, "%s official autolayout applied — %d part(s), %d overlap(s), %s\n", mark, len(kept), overlaps, wiringLine)
	if !rewire {
		fmt.Fprintln(stdout, "note: wiring was NOT rebuilt (page was unwired, or --rewire not passed) — wire it now with `sch autoconnect`")
	} else if cerr == nil && sum.DanglingWires == 0 && sum.FloatingPins > 40 {
		fmt.Fprintln(stdout, "note: high floating-pin count may include unused IC pins (normal) plus stubs the scattered layout could not route — verify with `sch bridge-check`")
	}
	fmt.Fprintln(stdout, "note: the platform engine is connectivity-clustered (radial) and off-grid; it is messier than `--engine template` and a scattered layout can leave stub-collision shorts. Prefer template for a known block.")
	return nil
}

// countSchWires returns the number of wires on the active page.
func countSchWires(cfg *appConfig, win string) (int, error) {
	res, err := requestActionTimed(cfg, "debug.exec_js", win,
		map[string]any{"code": "return {n:(await eda.sch_PrimitiveWire.getAll()).length}"}, defaultActionTimeout)
	if err != nil {
		return 0, err
	}
	v, _ := res.Result["value"].(map[string]any)
	return int(asFloat(v["n"])), nil
}

// snapSchPartsToGrid moves every off-grid part anchor to the nearest multiple of
// 5 (schAnchorGrid). Returns how many parts moved.
func snapSchPartsToGrid(cfg *appConfig, win string) (int, error) {
	code := `const snap=v=>Math.round(v/5)*5;
const comps=await eda.sch_PrimitiveComponent.getAll();
let n=0;
for(const c of comps){
  if(c.getState_ComponentType&&String(c.getState_ComponentType())==='part'){
    const x=c.getState_X(), y=c.getState_Y();
    if(x%5!==0||y%5!==0){ await eda.sch_PrimitiveComponent.modify(c.getState_PrimitiveId(),{x:snap(x),y:snap(y)}); n++; }
  }
}
return {n};`
	res, err := requestActionTimed(cfg, "debug.exec_js", win, map[string]any{"code": code}, officialMutateTimeout)
	if err != nil {
		return 0, err
	}
	v, _ := res.Result["value"].(map[string]any)
	return int(asFloat(v["n"])), nil
}

// deleteSchWiresAndFlags removes every wire and every netflag/netport component
// on the active page (the clean slate a rewire needs). Returns (wires, flags).
func deleteSchWiresAndFlags(cfg *appConfig, win string) (int, int, error) {
	code := `let w=0,f=0;
const comps=await eda.sch_PrimitiveComponent.getAll();
for(const c of comps){ const t=c.getState_ComponentType?String(c.getState_ComponentType()):'';
  if(/netflag|netport|net_flag|net_port|netlabel/i.test(t)){ await eda.sch_PrimitiveComponent.delete(c.getState_PrimitiveId()); f++; } }
const wires=await eda.sch_PrimitiveWire.getAll();
for(const x of wires){ await eda.sch_PrimitiveWire.delete(x.getState_PrimitiveId()); w++; }
return {w,f};`
	res, err := requestActionTimed(cfg, "debug.exec_js", win, map[string]any{"code": code}, officialMutateTimeout)
	if err != nil {
		return 0, 0, err
	}
	v, _ := res.Result["value"].(map[string]any)
	return int(asFloat(v["w"])), int(asFloat(v["f"])), nil
}

// connsFromLiveNets flattens a captured netlist (net → set of "DESIGNATOR.NUMBER")
// into one autoconnect connection per pin, inferring the flag kind from the net
// name. Single-pin nets are skipped (nothing to tie). Deterministic order.
func connsFromLiveNets(liveNets map[string]map[string]bool) []acConnSpec {
	var nets []string
	for n := range liveNets {
		nets = append(nets, n)
	}
	sort.Strings(nets)
	var out []acConnSpec
	for _, net := range nets {
		members := liveNets[net]
		if len(members) < 2 {
			continue
		}
		kind := bapFlagKind(net)
		var pins []string
		for m := range members {
			pins = append(pins, m)
		}
		sort.Strings(pins)
		for _, m := range pins {
			// netlist member "DESIGNATOR.NUMBER" → autoconnect PinRef "DESIGNATOR:NUMBER".
			ref := m
			if i := strings.IndexByte(m, '.'); i > 0 {
				ref = m[:i] + ":" + m[i+1:]
			}
			out = append(out, acConnSpec{PinRef: ref, Kind: kind, Net: net})
		}
	}
	return out
}

func countNets(liveNets map[string]map[string]bool) int {
	n := 0
	for _, m := range liveNets {
		if len(m) >= 2 {
			n++
		}
	}
	return n
}

// schCheckSummary runs schematic.check and returns its summary (floating pins,
// dangling wires, …).
func schCheckSummary(cfg *appConfig, win string) (checkSummary, error) {
	res, err := requestAction(cfg, "schematic.check", win, map[string]any{})
	if err != nil {
		return checkSummary{}, err
	}
	rep, perr := parseCheckReport(res.Result)
	if perr != nil {
		return checkSummary{}, perr
	}
	return rep.Summary, nil
}
