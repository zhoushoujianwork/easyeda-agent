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
	"encoding/json"
	"fmt"
	"io"
	"math"
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

// officialPartInput is the subset of a part's live state that feeds the
// platform autoLayout call. Capturing it twice gives the long-running command an
// optimistic-concurrency token: if a user/agent moved, replaced, added, or
// removed a part while we were preparing the run, we refuse instead of applying
// a device-type map and pre-layout netlist to a different scene.
type officialPartInput struct {
	PrimitiveID string  `json:"primitiveId"`
	Designator  string  `json:"designator"`
	X           float64 `json:"x"`
	Y           float64 `json:"y"`
	Rotation    float64 `json:"rotation"`
}

type officialInputSnapshot struct {
	DocumentUUID     string
	WireCount        int
	BusCount         int
	NetflagCount     int
	NetportCount     int
	NetlabelCount    int
	ShortSymbolCount int
	SheetCount       int
	Parts            []officialPartInput
}

func (s officialInputSnapshot) connectivity() alConnectivitySummary {
	return alConnectivitySummary{
		Scope: "activePage", Wires: s.WireCount, Buses: s.BusCount,
		Netflags: s.NetflagCount, Netports: s.NetportCount,
		Netlabels: s.NetlabelCount, ShortSymbols: s.ShortSymbolCount,
	}
}

// officialInputSnapshotJS deliberately reads parts and wires in one connector
// action. That makes the second guard as close to atomic as the public editor API
// allows and leaves no independent components.list call between the final wire
// check and autoLayout.
const officialInputSnapshotJS = `// official-autolayout-input-snapshot
const parts=[];
let netflagCount=0,netportCount=0,netlabelCount=0,shortSymbolCount=0,sheetCount=0;
const components=await eda.sch_PrimitiveComponent.getAll();
if(!Array.isArray(components)) throw new Error('official autolayout snapshot: component inventory unavailable');
for(const c of components){
  const t=c.getState_ComponentType?String(c.getState_ComponentType()):'';
  if(t==='part'||t===''){
    parts.push({primitiveId:String(c.getState_PrimitiveId()),designator:String(c.getState_Designator?.()??''),x:c.getState_X(),y:c.getState_Y(),rotation:c.getState_Rotation()});
  }
  if(t==='netflag') netflagCount++;
  else if(t==='netport') netportCount++;
  else if(t==='netlabel') netlabelCount++;
  else if(t==='short_symbol') shortSymbolCount++;
  else if(t==='sheet') sheetCount++;
}
const wires=await eda.sch_PrimitiveWire.getAll();
const buses=await eda.sch_PrimitiveBus.getAll();
if(!Array.isArray(wires)||!Array.isArray(buses)) throw new Error('official autolayout snapshot: wire/bus inventory unavailable');
return {wireCount:wires.length,busCount:buses.length,netflagCount,netportCount,netlabelCount,shortSymbolCount,sheetCount,parts};`

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
	// This command READS the page (part/wire counts, netlist for --rewire) BEFORE
	// it mutates, so the generic mutating-action guard is not enough: the reads
	// must land on the SAME page as the layout. Pin the whole command to --doc up
	// front so a foreground on the wrong page can't make it capture page A's
	// netlist and lay out page B.
	if err := ensureActiveDoc(cfg, win); err != nil {
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
	docUUID := cur.Context.DocumentUUID
	if docUUID == "" {
		return fmt.Errorf("active schematic response has no document UUID — refusing an unpinned platform autoLayout")
	}
	// Pin every later mutation (autoLayout, snap, delete, rewire, save) to the
	// exact UUID observed above, even when the caller selected the foreground
	// page instead of passing --doc. The in-action autoLayout guard remains the
	// last-race defense; this shared guard also protects all follow-up mutations.
	pinnedCfg := *cfg
	pinnedCfg.doc = docUUID
	cfg = &pinnedCfg

	before, berr := readOfficialInputSnapshot(cfg, win, docUUID)
	if berr != nil {
		return fmt.Errorf("capture official autolayout input: %w", berr)
	}
	connectivity := before.connectivity()
	fmt.Fprintf(stderr, "official autolayout: %d part(s), connectivity=%d (wires=%d buses=%d markers=%d) on the pinned page\n",
		len(before.Parts), connectivity.total(), connectivity.Wires, connectivity.Buses,
		connectivity.Netflags+connectivity.Netports+connectivity.Netlabels+connectivity.ShortSymbols)
	if before.SheetCount != 1 {
		return fmt.Errorf("official autolayout requires exactly one readable sheet/title-block primitive on the target page; found %d", before.SheetCount)
	}
	if connectivity.Buses > 0 {
		return fmt.Errorf("this page has %d bus primitive(s); official --rewire cannot capture/rebuild bus topology, so autoLayout is refused even with --rewire", connectivity.Buses)
	}

	// Pre-guard: autoLayout destroys existing wiring. Refuse a wired page unless
	// the caller opted into the destroy-and-rebuild path.
	if connectivity.total() > 0 && !rewire {
		return fmt.Errorf("this page already has connectivity (wires=%d netflags=%d netports=%d netlabels=%d shortSymbols=%d) — the platform autoLayout MOVES parts without attached connectivity. "+
			"Run it BEFORE wiring, or pass --rewire to delete + rebuild the wiring from the current netlist afterward (best-effort: a scattered layout can leave residual shorts)",
			connectivity.Wires, connectivity.Netflags, connectivity.Netports, connectivity.Netlabels, connectivity.ShortSymbols)
	}

	if !apply {
		fmt.Fprintf(stdout, "dry-run: would run the platform eda.sch_Document.autoLayout() over %d part(s) on this page\n", len(before.Parts))
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
	var capturedNets map[string]map[string]bool
	if rewire {
		liveNets, rerr := readOfficialLiveNets(cfg, win, docUUID)
		if rerr != nil {
			return fmt.Errorf("--rewire needs the pre-layout netlist but reading it failed: %w", rerr)
		}
		capturedNets = liveNets
		conns = connsFromLiveNets(liveNets)
		fmt.Fprintf(stderr, "captured %d net(s) → %d pin connection(s) to rebuild after layout\n", countNets(liveNets), len(conns))
	}

	// Feed the platform a designator→device-type map (issue #143): autoLayout is
	// role-aware (resistor/capacitor/inductive/diode/triode/oscillator/chip/
	// otherDevice) and clusters far smarter with it than the bare call. Built from
	// the page's real designators; an empty map degrades to the bare call. Passing
	// an extra props object is harmless on builds that ignore it (JS).
	designators := make([]string, 0, len(before.Parts))
	for _, p := range before.Parts {
		designators = append(designators, p.Designator)
	}
	dtMap := buildDeviceTypeMap(designators)
	if len(dtMap) > 0 {
		fmt.Fprintf(stderr, "feeding designatorDeviceTypeMap for %d part(s) (role-aware layout)\n", len(dtMap))
	}

	// Final pre-mutation guard. For --rewire, compare the canonical netlist too:
	// an equal wire count does NOT prove equal topology (one wire can be replaced
	// by another while preserving N). Read the netlist first, then make the
	// combined parts+wire snapshot the very last read before autoLayout.
	if rewire {
		nowNets, nerr := readOfficialLiveNets(cfg, win, docUUID)
		if nerr != nil {
			return fmt.Errorf("re-read pre-layout netlist immediately before autoLayout: %w", nerr)
		}
		if beforeNet, nowNet := canonicalOfficialNets(capturedNets), canonicalOfficialNets(nowNets); beforeNet != nowNet {
			return fmt.Errorf("official autolayout input drifted before mutation: schematic net topology changed after capture — refusing to delete/rebuild from a stale netlist")
		}
	}
	immediate, ierr := readOfficialInputSnapshot(cfg, win, docUUID)
	if ierr != nil {
		return fmt.Errorf("re-read official autolayout input immediately before autoLayout: %w", ierr)
	}
	if derr := compareOfficialInputSnapshots(before, immediate); derr != nil {
		return fmt.Errorf("official autolayout input drifted before mutation: %w", derr)
	}
	code, codeErr := buildGuardedOfficialAutolayoutJS(before, dtMap)
	if codeErr != nil {
		return fmt.Errorf("build guarded platform autoLayout call: %w", codeErr)
	}

	fmt.Fprintln(stderr, "running platform autoLayout — this is a LONG operation (~2min); the editor shows a progress bar…")
	layoutRes, err := requestActionTimed(cfg, "debug.exec_js", win,
		map[string]any{"code": code}, officialAutolayoutTimeout)
	if err != nil {
		return fmt.Errorf("platform autoLayout failed (it needs the schematic foreground; a background page can hang): %w", err)
	}
	if err := validateOfficialResponseDocument(layoutRes, docUUID); err != nil {
		return fmt.Errorf("platform autoLayout document verification failed after mutation: %w", err)
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
	var rewireErr error
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
			rewireErr = err
		}
	}

	// Self-check: real bbox + pin geometry AND wiring. Every unavailable proof is
	// a hard failure; this command has already mutated the page, so returning zero
	// on a skipped read/check would falsely certify an unknown state.
	res, err := requestAction(cfg, "schematic.components.list", win, map[string]any{
		"includeBBox": true, "includePins": true, "includePinNets": false, "includeConnectivitySummary": true,
	})
	if err != nil {
		return fmt.Errorf("official autolayout post-check could not read back geometry: %w", err)
	}
	if err := validateOfficialResponseDocument(res, docUUID); err != nil {
		return fmt.Errorf("official autolayout post-check read the wrong document: %w", err)
	}
	comps, perr := parseLayoutComps(res.Result)
	if perr != nil {
		return fmt.Errorf("official autolayout post-check could not parse geometry: %w", perr)
	}
	postConnectivity, connErr := parseAutolayoutConnectivity(res.Result)
	if connErr != nil {
		return fmt.Errorf("official autolayout post-check could not prove connectivity inventory: %w", connErr)
	}
	kept, _ := filterLayoutComps(comps, false)
	lrep := analyzeLayout(kept, 0, acCoordEps)
	overlaps := len(lrep.Overlaps)
	pinCoincidences := len(lrep.PinCoincidences)
	offGrid := detectOffGridAnchors(kept, schAnchorGrid, acCoordEps)
	expectedPartIDs := make(map[string]bool, len(before.Parts))
	for _, p := range before.Parts {
		expectedPartIDs[p.PrimitiveID] = true
	}
	postPartIDs := make(map[string]bool, len(kept))
	uncheckedPinSets := 0
	invalidPartGeometry := 0
	for _, c := range kept {
		postPartIDs[c.ID] = true
		if !c.PinsAvailable || !c.PinsProofKnown {
			uncheckedPinSets++
		}
		invalidPartGeometry += len(c.GeometryErrors)
	}
	partSetChanged := len(expectedPartIDs) != len(postPartIDs)
	if !partSetChanged {
		for id := range expectedPartIDs {
			if !postPartIDs[id] {
				partSetChanged = true
				break
			}
		}
	}
	var markerNoBBox int
	for _, c := range comps {
		if isSchMarker(c.ComponentType) && c.BBox == nil {
			markerNoBBox++
		}
	}
	postSheet := sheetBBoxOf(comps)
	titleBlock, tbSource := titleBlockKeepoutWithSource(postSheet)
	markerFindings := analyzeMarkerGeometry(comps, titleBlock, tbSource, 0.5)
	// info-level findings (e.g. titleblock-overlap against an ESTIMATED keep-out on
	// a non-A4 sheet, issue #172) are advisory and must not fail the post-check.
	blockingMarkerFindings := 0
	for _, f := range markerFindings {
		if checkLevelBlocks(f.Level, true) { // strict grading: warn blocks here, info never
			blockingMarkerFindings++
		}
	}

	checkRep, cerr := schCheckReport(cfg, win, docUUID)
	if cerr != nil {
		return fmt.Errorf("official autolayout post-check could not run/parse schematic.check: %w", cerr)
	}
	sum := checkRep.Summary
	wiringLine := fmt.Sprintf("%d dangling wire(s), %d floating pin(s)", sum.DanglingWires, sum.FloatingPins)

	var failures []string
	if rewireErr != nil {
		failures = append(failures, "rewire reported one or more failed connections")
	}
	if len(lrep.NoBBox) > 0 {
		failures = append(failures, fmt.Sprintf("%d part(s) had no readable bbox", len(lrep.NoBBox)))
	}
	if partSetChanged {
		failures = append(failures, "post-layout part identity set differs from the guarded input")
	}
	if uncheckedPinSets > 0 {
		failures = append(failures, fmt.Sprintf("%d part(s) had no readable/proven pin set", uncheckedPinSets))
	}
	if invalidPartGeometry > 0 {
		failures = append(failures, fmt.Sprintf("%d invalid part geometry value(s)", invalidPartGeometry))
	}
	if len(offGrid) > 0 {
		failures = append(failures, fmt.Sprintf("%d part anchor(s) remained off the %d-unit grid", len(offGrid), schAnchorGrid))
	}
	if markerNoBBox > 0 {
		failures = append(failures, fmt.Sprintf("%d net marker(s) had no readable bbox", markerNoBBox))
	}
	if postSheet == nil {
		failures = append(failures, "sheet/title-block bbox was unavailable after layout")
	}
	if postConnectivity.Buses > 0 {
		failures = append(failures, fmt.Sprintf("%d unsupported bus primitive(s) present after layout", postConnectivity.Buses))
	}
	if !rewire && postConnectivity.total() > 0 {
		failures = append(failures, fmt.Sprintf("unwired-mode autoLayout unexpectedly left/created %d connectivity primitive(s)", postConnectivity.total()))
	}
	if overlaps > 0 {
		failures = append(failures, fmt.Sprintf("%d part overlap(s)", overlaps))
	}
	if pinCoincidences > 0 {
		failures = append(failures, fmt.Sprintf("%d cross-part pin coincidence(s)", pinCoincidences))
	}
	if blockingMarkerFindings > 0 {
		failures = append(failures, fmt.Sprintf("%d blocking marker/title-block geometry finding(s)", blockingMarkerFindings))
	}
	if blocking := officialBlockingCheckFindings(checkRep); blocking > 0 {
		failures = append(failures, fmt.Sprintf("%d blocking schematic.check finding(s)", blocking))
	}

	mark := "✗"
	clean := len(failures) == 0
	if clean {
		mark = "✓"
	}
	fmt.Fprintf(stdout, "%s official autolayout applied — %d part(s), %d overlap(s), %d pin-coincidence(s), %s\n",
		mark, len(kept), overlaps, pinCoincidences, wiringLine)
	if !rewire {
		fmt.Fprintln(stdout, "note: wiring was NOT rebuilt (page was unwired, or --rewire not passed) — wire it now with `sch autoconnect`")
	} else if cerr == nil && sum.DanglingWires == 0 && sum.FloatingPins > 40 {
		fmt.Fprintln(stdout, "note: high floating-pin count may include unused IC pins (normal) plus stubs the scattered layout could not route — verify with `sch bridge-check`")
	}
	fmt.Fprintln(stdout, "note: the platform engine is connectivity-clustered (radial) and off-grid; it is messier than `--engine template` and a scattered layout can leave stub-collision shorts. Prefer template for a known block.")
	if !clean {
		return fmt.Errorf("official autolayout post-check failed: %s — the page was mutated; fix/revert it before continuing", strings.Join(failures, "; "))
	}
	if err := saveAutolayoutDocument(cfg, win, docUUID, "save verified official autolayout"); err != nil {
		return fmt.Errorf("official autolayout passed checks but save was not proven: %w", err)
	}
	return nil
}

// buildGuardedOfficialAutolayoutJS closes the last request-to-request race:
// debug.exec_js checks the active document plus the exact guarded part input and
// wire count in the SAME handler invocation that starts autoLayout. The earlier
// Go-side second snapshot produces a useful diff; this in-action guard prevents
// a page switch or edit in the short gap after that snapshot from mutating a
// different scene.
func buildGuardedOfficialAutolayoutJS(expected officialInputSnapshot, dtMap map[string]string) (string, error) {
	docJSON, err := json.Marshal(expected.DocumentUUID)
	if err != nil {
		return "", err
	}
	partsJSON, err := json.Marshal(expected.Parts)
	if err != nil {
		return "", err
	}
	mapJSON, err := json.Marshal(dtMap)
	if err != nil {
		return "", err
	}
	autoCall := "await eda.sch_Document.autoLayout();"
	if len(dtMap) > 0 {
		autoCall = fmt.Sprintf("await eda.sch_Document.autoLayout({designatorDeviceTypeMap:%s});", mapJSON)
	}
	return fmt.Sprintf(`// official-autolayout-guarded-mutate
const expectedDoc=%s;
const activeDoc=await eda.dmt_SelectControl.getCurrentDocumentInfo();
if(!activeDoc||String(activeDoc.uuid)!==expectedDoc){
  throw new Error('official autolayout guard: active document changed before mutation');
}
const expectedParts=%s;
const actualParts=[];
let netflagCount=0,netportCount=0,netlabelCount=0,shortSymbolCount=0,sheetCount=0;
const components=await eda.sch_PrimitiveComponent.getAll();
if(!Array.isArray(components)) throw new Error('official autolayout guard: component inventory unavailable');
for(const c of components){
  const t=c.getState_ComponentType?String(c.getState_ComponentType()):'';
  if(t==='part'||t===''){
    actualParts.push({primitiveId:String(c.getState_PrimitiveId()),designator:String(c.getState_Designator?.()??''),x:c.getState_X(),y:c.getState_Y(),rotation:c.getState_Rotation()});
  }
  if(t==='netflag') netflagCount++;
  else if(t==='netport') netportCount++;
  else if(t==='netlabel') netlabelCount++;
  else if(t==='short_symbol') shortSymbolCount++;
  else if(t==='sheet') sheetCount++;
}
actualParts.sort((a,b)=>a.primitiveId.localeCompare(b.primitiveId));
if(actualParts.length!==expectedParts.length){
  throw new Error('official autolayout guard: part count changed before mutation');
}
for(let i=0;i<expectedParts.length;i++){
  const a=actualParts[i], e=expectedParts[i];
  if(a.primitiveId!==e.primitiveId||a.designator!==e.designator||a.x!==e.x||a.y!==e.y||a.rotation!==e.rotation){
    throw new Error('official autolayout guard: part input changed before mutation');
  }
}
const wires=await eda.sch_PrimitiveWire.getAll();
const buses=await eda.sch_PrimitiveBus.getAll();
if(!Array.isArray(wires)||!Array.isArray(buses)){
  throw new Error('official autolayout guard: wire/bus inventory unavailable');
}
if(wires.length!==%d||buses.length!==%d||netflagCount!==%d||netportCount!==%d||netlabelCount!==%d||shortSymbolCount!==%d||sheetCount!==%d){
  throw new Error('official autolayout guard: connectivity or sheet input changed before mutation');
}
%s
return {done:true,guardedDocumentUuid:expectedDoc};`,
		docJSON, partsJSON,
		expected.WireCount, expected.BusCount,
		expected.NetflagCount, expected.NetportCount, expected.NetlabelCount,
		expected.ShortSymbolCount, expected.SheetCount,
		autoCall), nil
}

func validateOfficialResponseDocument(res *actionResult, expectedUUID string) error {
	if res == nil || res.Context == nil {
		return fmt.Errorf("response has no live document context")
	}
	if res.Context.DocumentUUID != expectedUUID {
		return fmt.Errorf("expected document %s, response came from %s", expectedUUID, res.Context.DocumentUUID)
	}
	if res.Context.DocumentType != "" && res.Context.DocumentType != "schematic" {
		return fmt.Errorf("expected schematic document %s, response type is %q", expectedUUID, res.Context.DocumentType)
	}
	return nil
}

func readOfficialInputSnapshot(cfg *appConfig, win, expectedUUID string) (officialInputSnapshot, error) {
	res, err := requestActionTimed(cfg, "debug.exec_js", win,
		map[string]any{"code": officialInputSnapshotJS}, defaultActionTimeout)
	if err != nil {
		return officialInputSnapshot{}, err
	}
	if err := validateOfficialResponseDocument(res, expectedUUID); err != nil {
		return officialInputSnapshot{}, err
	}
	v, ok := res.Result["value"].(map[string]any)
	if !ok {
		return officialInputSnapshot{}, fmt.Errorf("snapshot response has no value object")
	}
	readCount := func(name string) (int, error) {
		n, ok := finiteFloat(v[name])
		if !ok || n < 0 || n != math.Trunc(n) {
			return 0, fmt.Errorf("snapshot response has invalid %s=%v", name, v[name])
		}
		return int(n), nil
	}
	counts := make(map[string]int)
	for _, name := range []string{
		"wireCount", "busCount", "netflagCount", "netportCount",
		"netlabelCount", "shortSymbolCount", "sheetCount",
	} {
		count, countErr := readCount(name)
		if countErr != nil {
			return officialInputSnapshot{}, countErr
		}
		counts[name] = count
	}
	rawParts, ok := v["parts"].([]any)
	if !ok {
		return officialInputSnapshot{}, fmt.Errorf("snapshot response has no parts array")
	}
	snap := officialInputSnapshot{
		DocumentUUID:     expectedUUID,
		WireCount:        counts["wireCount"],
		BusCount:         counts["busCount"],
		NetflagCount:     counts["netflagCount"],
		NetportCount:     counts["netportCount"],
		NetlabelCount:    counts["netlabelCount"],
		ShortSymbolCount: counts["shortSymbolCount"],
		SheetCount:       counts["sheetCount"],
		Parts:            make([]officialPartInput, 0, len(rawParts)),
	}
	for i, raw := range rawParts {
		m, ok := raw.(map[string]any)
		if !ok {
			return officialInputSnapshot{}, fmt.Errorf("snapshot parts[%d] is not an object", i)
		}
		p := officialPartInput{
			PrimitiveID: asString(m["primitiveId"]),
			Designator:  asString(m["designator"]),
		}
		if p.PrimitiveID == "" {
			return officialInputSnapshot{}, fmt.Errorf("snapshot parts[%d] has no primitiveId", i)
		}
		var numberOK bool
		if p.X, numberOK = asFloatOK(m["x"]); !numberOK || math.IsNaN(p.X) || math.IsInf(p.X, 0) {
			return officialInputSnapshot{}, fmt.Errorf("snapshot part %s has invalid x=%v", p.PrimitiveID, m["x"])
		}
		if p.Y, numberOK = asFloatOK(m["y"]); !numberOK || math.IsNaN(p.Y) || math.IsInf(p.Y, 0) {
			return officialInputSnapshot{}, fmt.Errorf("snapshot part %s has invalid y=%v", p.PrimitiveID, m["y"])
		}
		if p.Rotation, numberOK = asFloatOK(m["rotation"]); !numberOK || math.IsNaN(p.Rotation) || math.IsInf(p.Rotation, 0) {
			return officialInputSnapshot{}, fmt.Errorf("snapshot part %s has invalid rotation=%v", p.PrimitiveID, m["rotation"])
		}
		snap.Parts = append(snap.Parts, p)
	}
	sort.Slice(snap.Parts, func(i, j int) bool {
		return snap.Parts[i].PrimitiveID < snap.Parts[j].PrimitiveID
	})
	return snap, nil
}

func compareOfficialInputSnapshots(before, now officialInputSnapshot) error {
	if before.DocumentUUID != now.DocumentUUID {
		return fmt.Errorf("document changed from %s to %s", before.DocumentUUID, now.DocumentUUID)
	}
	if before.WireCount != now.WireCount {
		return fmt.Errorf("wire count changed from %d to %d", before.WireCount, now.WireCount)
	}
	if before.connectivity() != now.connectivity() {
		return fmt.Errorf("connectivity inventory changed from %+v to %+v", before.connectivity(), now.connectivity())
	}
	if before.SheetCount != now.SheetCount {
		return fmt.Errorf("sheet count changed from %d to %d", before.SheetCount, now.SheetCount)
	}
	if len(before.Parts) != len(now.Parts) {
		return fmt.Errorf("part count changed from %d to %d", len(before.Parts), len(now.Parts))
	}
	for i := range before.Parts {
		if before.Parts[i] != now.Parts[i] {
			return fmt.Errorf("part input changed: before=%+v now=%+v", before.Parts[i], now.Parts[i])
		}
	}
	return nil
}

// readOfficialLiveNets is the fail-closed netlist reader for the destructive
// --rewire path. The shared block-apply reader intentionally tolerates partial
// result shapes; official autoLayout cannot, because an empty/partial capture
// would be followed by deleting the original wiring.
func readOfficialLiveNets(cfg *appConfig, win, expectedUUID string) (map[string]map[string]bool, error) {
	res, err := requestAction(cfg, "schematic.read", win, map[string]any{"includeCheck": false})
	if err != nil {
		return nil, err
	}
	if err := validateOfficialResponseDocument(res, expectedUUID); err != nil {
		return nil, err
	}
	rawNets, ok := res.Result["nets"].([]any)
	if !ok {
		return nil, fmt.Errorf("schematic.read result has no nets array")
	}
	out := map[string]map[string]bool{}
	for i, raw := range rawNets {
		m, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("nets[%d] is not an object", i)
		}
		name := asString(m["net"])
		if name == "" {
			return nil, fmt.Errorf("nets[%d] has no net name", i)
		}
		rawPins, ok := m["pins"].([]any)
		if !ok {
			return nil, fmt.Errorf("net %q has no pins array", name)
		}
		members := out[name]
		if members == nil {
			members = map[string]bool{}
			out[name] = members
		}
		for j, rawPin := range rawPins {
			pin := asString(rawPin)
			if pin == "" {
				return nil, fmt.Errorf("net %q pins[%d] is not a non-empty string", name, j)
			}
			members[pin] = true
		}
	}
	return out, nil
}

func canonicalOfficialNets(nets map[string]map[string]bool) string {
	lines := make([]string, 0, len(nets))
	for net, members := range nets {
		pins := make([]string, 0, len(members))
		for pin := range members {
			pins = append(pins, pin)
		}
		sort.Strings(pins)
		lines = append(lines, net+"="+strings.Join(pins, ","))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func officialBlockingCheckFindings(rep checkReport) int {
	fromSummary := rep.Summary.GeomNetMismatches +
		rep.Summary.NetMarkerMismatches +
		rep.Summary.MultiNetWires +
		rep.Summary.WireCrossings +
		rep.Summary.WireOverPins +
		rep.Summary.ZeroLengthWires +
		rep.Summary.DanglingWires +
		rep.Summary.DuplicateNetMarkers +
		rep.Summary.TitleblockOverlaps +
		rep.Summary.MarkerOverlaps
	fromFindings := 0
	for _, finding := range rep.Findings {
		// Floating pins include intentionally unused IC pins and remain
		// diagnostic here. Every other (including a future unknown) check type is
		// structural and fail-closed.
		if strings.EqualFold(finding.Type, "floating-pin") {
			continue
		}
		count := finding.Count
		if count < 1 {
			count = 1
		}
		fromFindings += count
	}
	if fromFindings > fromSummary {
		return fromFindings
	}
	return fromSummary
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

// deviceTypeForDesignator maps a reference-designator prefix to the device-type
// bucket the platform autoLayout(designatorDeviceTypeMap) understands (issue
// #143). The valid buckets are resistor/capacitor/inductive/diode/triode/
// oscillator/chip/otherDevice; any unrecognised prefix falls back to otherDevice
// so the map never carries an invalid value.
func deviceTypeForDesignator(desig string) string {
	i := 0
	for i < len(desig) {
		c := desig[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
			i++
			continue
		}
		break
	}
	switch strings.ToUpper(desig[:i]) {
	case "R", "RN", "RV", "RT", "RP":
		return "resistor"
	case "C", "CN":
		return "capacitor"
	case "L", "FB":
		return "inductive"
	case "D", "LED", "ZD", "DZ", "BR":
		return "diode"
	case "Q", "T":
		return "triode"
	case "Y", "X", "XTAL", "OSC":
		return "oscillator"
	case "U", "IC":
		return "chip"
	default: // J/SW/K/FL/TP/… and anything unknown
		return "otherDevice"
	}
}

// buildDeviceTypeMap turns a designator list into the designator→device-type map
// the platform autoLayout consumes. Empty designators are skipped.
func buildDeviceTypeMap(desigs []string) map[string]string {
	m := make(map[string]string, len(desigs))
	for _, d := range desigs {
		if d == "" {
			continue
		}
		m[d] = deviceTypeForDesignator(d)
	}
	return m
}

// schCheckReport runs schematic.check and validates the result as a complete
// proof, rather than letting missing fields decode to false-clean zero values.
func schCheckReport(cfg *appConfig, win, expectedUUID string) (checkReport, error) {
	res, err := requestAction(cfg, "schematic.check", win, map[string]any{})
	if err != nil {
		return checkReport{}, err
	}
	if err := validateOfficialResponseDocument(res, expectedUUID); err != nil {
		return checkReport{}, err
	}
	rawSummary, ok := res.Result["summary"].(map[string]any)
	if !ok {
		return checkReport{}, fmt.Errorf("schematic.check result has no summary object")
	}
	requiredCounts := []string{
		"floatingPins",
		"componentsWithFloating",
		"geomNetMismatches",
		"netMarkerMismatches",
		"multiNetWires",
		"wireContacts",
		"wireCrossings",
		"wireOverPins",
		"zeroLengthWires",
		"danglingWires",
		"total",
	}
	for _, field := range requiredCounts {
		n, ok := asFloatOK(rawSummary[field])
		if !ok || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n != math.Trunc(n) {
			return checkReport{}, fmt.Errorf("schematic.check summary has invalid or missing %s=%v", field, rawSummary[field])
		}
	}
	rawFindings, ok := res.Result["findings"].([]any)
	if !ok {
		return checkReport{}, fmt.Errorf("schematic.check result has no findings array")
	}
	passed, ok := res.Result["passed"].(bool)
	if !ok {
		return checkReport{}, fmt.Errorf("schematic.check result has no boolean passed field")
	}
	rep, perr := parseCheckReport(res.Result)
	if perr != nil {
		return checkReport{}, perr
	}
	if rep.Summary.Total != len(rawFindings) {
		return checkReport{}, fmt.Errorf("schematic.check summary total=%d but findings has %d item(s)", rep.Summary.Total, len(rawFindings))
	}
	if passed != (rep.Summary.Total == 0) {
		return checkReport{}, fmt.Errorf("schematic.check passed=%t disagrees with total=%d", passed, rep.Summary.Total)
	}
	return rep, nil
}
