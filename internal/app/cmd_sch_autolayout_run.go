package app

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// ── autolayout orchestration (I/O side; the planner in cmd_sch_autolayout.go is pure) ──

// alSpec is the `--spec` JSON shape (issue #25).
type alSpec struct {
	Page    string         `json:"page"`
	Sheet   string         `json:"sheet"`
	Modules []alSpecModule `json:"modules"`
	Rules   *alSpecRules   `json:"rules"`
}

type alSpecModule struct {
	Name  string   `json:"name"`
	Zone  string   `json:"zone"`
	Core  string   `json:"core"`
	Parts []string `json:"parts"`
}

// alSpecRules mirrors the rules block; pointer fields so an omitted key keeps the
// default instead of zeroing it.
type alSpecRules struct {
	AvoidTitleBlock                   *bool    `json:"avoidTitleBlock"`
	PreservePinFanout                 *bool    `json:"preservePinFanout"`
	ModuleGap                         *float64 `json:"moduleGap"`
	RouteChannelGap                   *float64 `json:"routeChannelGap"`
	PreferVerticalPeripheralPlacement *bool    `json:"preferVerticalPeripheralPlacement"`
	PartGap                           *float64 `json:"partGap"`
}

type alConnectivitySummary struct {
	Scope        string
	Wires        int
	Buses        int
	Netflags     int
	Netports     int
	Netlabels    int
	ShortSymbols int
}

func (s alConnectivitySummary) total() int {
	return s.Wires + s.Buses + s.Netflags + s.Netports + s.Netlabels + s.ShortSymbols
}

func parseAutolayoutConnectivity(result map[string]any) (alConnectivitySummary, error) {
	raw, ok := result["connectivitySummary"].(map[string]any)
	if !ok {
		return alConnectivitySummary{}, fmt.Errorf("connector omitted connectivitySummary (rebuild/re-import the connector; template --apply requires the fail-closed active-page inventory)")
	}
	s := alConnectivitySummary{Scope: asString(raw["scope"])}
	if s.Scope != "activePage" {
		return alConnectivitySummary{}, fmt.Errorf("connectivitySummary.scope=%q, want activePage", s.Scope)
	}
	fields := []struct {
		name string
		dst  *int
	}{
		{"wires", &s.Wires},
		{"buses", &s.Buses},
		{"netflags", &s.Netflags},
		{"netports", &s.Netports},
		{"netlabels", &s.Netlabels},
		{"shortSymbols", &s.ShortSymbols},
	}
	for _, f := range fields {
		n, valid := finiteFloat(raw[f.name])
		if !valid || n < 0 || n != math.Trunc(n) {
			return alConnectivitySummary{}, fmt.Errorf("connectivitySummary.%s=%v is not a non-negative integer", f.name, raw[f.name])
		}
		*f.dst = int(n)
	}
	return s, nil
}

func rejectConnectedTemplatePage(s alConnectivitySummary, phase string) error {
	if s.total() == 0 {
		return nil
	}
	return fmt.Errorf("autolayout: active page connectivity is non-empty %s (wires=%d buses=%d netflags=%d netports=%d netlabels=%d shortSymbols=%d); "+
		"template --apply only moves parts and would detach or misalign existing connectivity. Run it before wiring; no unsafe override is available",
		phase, s.Wires, s.Buses, s.Netflags, s.Netports, s.Netlabels, s.ShortSymbols)
}

// applyTo overlays the spec's rules onto a base ruleset.
func (r *alSpecRules) applyTo(base autolayoutRules) autolayoutRules {
	if r == nil {
		return base
	}
	if r.AvoidTitleBlock != nil {
		base.AvoidTitleBlock = *r.AvoidTitleBlock
	}
	if r.PreservePinFanout != nil {
		base.PreservePinFanout = *r.PreservePinFanout
	}
	if r.ModuleGap != nil {
		base.ModuleGap = *r.ModuleGap
	}
	if r.RouteChannelGap != nil {
		base.RouteChannelGap = *r.RouteChannelGap
	}
	if r.PreferVerticalPeripheralPlacement != nil {
		base.PreferVertical = *r.PreferVerticalPeripheralPlacement
	}
	if r.PartGap != nil {
		base.PartGap = *r.PartGap
	}
	return base
}

// parseAutolayoutParts extracts the placed parts (anchor + bbox + pins) and the
// sheet bbox from a components.list result. Non-part primitives other than the
// sheet are ignored — the planner only moves real parts.
func parseAutolayoutParts(result map[string]any) ([]alPart, *layoutBBox) {
	raw, _ := result["components"].([]any)
	var parts []alPart
	var sheet *layoutBBox
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		ctype := asString(m["componentType"])
		var box *layoutBBox
		if bm, ok := m["bbox"].(map[string]any); ok {
			box = &layoutBBox{
				MinX: asFloat(bm["minX"]), MinY: asFloat(bm["minY"]),
				MaxX: asFloat(bm["maxX"]), MaxY: asFloat(bm["maxY"]),
			}
		}
		switch ctype {
		case "sheet":
			if box != nil {
				sheet = box
			}
		case "part", "":
			p := alPart{
				Designator:  asString(m["designator"]),
				PrimitiveID: asString(m["primitiveId"]),
				AnchorX:     asFloat(m["x"]),
				AnchorY:     asFloat(m["y"]),
				Rotation:    asFloat(m["rotation"]),
			}
			if box != nil {
				p.BBox = *box
				p.HasBBox = true
			}
			if pins, ok := m["pins"].([]any); ok {
				for _, pp := range pins {
					if pm, ok := pp.(map[string]any); ok {
						p.Pins = append(p.Pins, alPinPt{X: asFloat(pm["x"]), Y: asFloat(pm["y"])})
					}
				}
			}
			parts = append(parts, p)
		}
	}
	return parts, sheet
}

// parseAutolayoutPartsChecked is the fail-closed apply parser. The legacy
// parseAutolayoutParts helper remains for read-only alignment/extraction flows,
// while template --apply requires every movable part to have a stable id,
// finite anchor/bbox/rotation, and an explicitly successful pin-geometry read.
func parseAutolayoutPartsChecked(result map[string]any, requirePinProof bool) ([]alPart, *layoutBBox, []layoutComp, error) {
	comps, err := parseLayoutComps(result)
	if err != nil {
		return nil, nil, nil, err
	}
	realParts, _ := filterLayoutComps(comps, false)

	rotationByID := make(map[string]float64)
	rotationKnown := make(map[string]bool)
	if raw, ok := result["components"].([]any); ok {
		for _, item := range raw {
			m, mapOK := item.(map[string]any)
			if !mapOK {
				continue
			}
			id := asString(m["primitiveId"])
			if rotation, valid := finiteFloat(m["rotation"]); valid {
				rotationByID[id] = rotation
				rotationKnown[id] = true
			}
		}
	}

	var issues []string
	seenIDs := make(map[string]bool)
	seenDesignators := make(map[string]string)
	parts := make([]alPart, 0, len(realParts))
	for _, c := range realParts {
		name := label(c)
		if name == "" {
			name = "<unnamed-part>"
		}
		if c.ID == "" {
			issues = append(issues, name+": primitiveId is empty")
		} else if seenIDs[c.ID] {
			issues = append(issues, name+": duplicate primitiveId "+c.ID)
		}
		seenIDs[c.ID] = true
		if prior, exists := seenDesignators[c.Designator]; c.Designator != "" && exists && prior != c.ID {
			issues = append(issues, fmt.Sprintf("%s: duplicate designator resolves to both %s and %s", c.Designator, prior, c.ID))
		} else if c.Designator != "" {
			seenDesignators[c.Designator] = c.ID
		}
		if !c.AnchorAvailable {
			issues = append(issues, name+": anchor x/y is unavailable")
		}
		if c.BBox == nil {
			issues = append(issues, name+": rendered bbox is unavailable")
		}
		if !rotationKnown[c.ID] {
			issues = append(issues, name+": rotation is unavailable or non-finite")
		}
		if requirePinProof && (!c.PinsProofKnown || !c.PinsAvailable) {
			issues = append(issues, name+": connector did not prove a successful pin-array read")
		}
		for _, issue := range c.GeometryErrors {
			issues = append(issues, name+": "+issue)
		}
		if c.ID == "" || !c.AnchorAvailable || c.BBox == nil || !rotationKnown[c.ID] ||
			(requirePinProof && (!c.PinsProofKnown || !c.PinsAvailable)) {
			continue
		}
		p := alPart{
			Designator:  c.Designator,
			PrimitiveID: c.ID,
			AnchorX:     c.X,
			AnchorY:     c.Y,
			Rotation:    rotationByID[c.ID],
			BBox:        *c.BBox,
			HasBBox:     true,
		}
		for _, pin := range c.Pins {
			p.Pins = append(p.Pins, alPinPt{X: pin.X, Y: pin.Y})
		}
		parts = append(parts, p)
	}
	if len(realParts) == 0 {
		issues = append(issues, "active page contains no movable parts")
	}
	if len(issues) > 0 {
		sort.Strings(issues)
		return nil, sheetBBoxOf(comps), realParts, fmt.Errorf("incomplete component geometry: %s", strings.Join(issues, "; "))
	}
	return parts, sheetBBoxOf(comps), realParts, nil
}

func validateAutolayoutSpecForApply(spec alSpec) error {
	claimedBy := make(map[string]string)
	for i, m := range spec.Modules {
		zone := strings.ToLower(strings.TrimSpace(m.Zone))
		if zone != "" && !pcbZoneNames[zone] {
			return fmt.Errorf("autolayout: modules[%d] %q has unknown zone %q (valid: %s)",
				i, m.Name, m.Zone, strings.Join(sortedZoneNames(), ", "))
		}
		for _, d := range append([]string{m.Core}, m.Parts...) {
			d = strings.TrimSpace(d)
			if d == "" {
				continue
			}
			if prior, exists := claimedBy[d]; exists && prior != m.Name {
				return fmt.Errorf("autolayout: part %q is claimed by both module %q and %q", d, prior, m.Name)
			}
			claimedBy[d] = m.Name
		}
	}
	return nil
}

// pinTemplateAutolayoutTarget resolves one concrete connector window and one
// schematic page before any apply preflight. The returned config stores the
// resolved page UUID in doc, so every later mutating action also passes through
// the shared --doc guard.
func pinTemplateAutolayoutTarget(cfg *appConfig, window string, spec alSpec, apply bool) (*appConfig, string, string, error) {
	win, err := resolveTargetWindow(cfg, window)
	if err != nil {
		return nil, "", "", fmt.Errorf("autolayout: resolve target window: %w", err)
	}
	local := *cfg
	if !apply {
		return &local, win, "", nil
	}

	docSelector := strings.TrimSpace(cfg.doc)
	specSelector := strings.TrimSpace(spec.Page)
	if docSelector == "" && specSelector == "" {
		return nil, "", "", fmt.Errorf("autolayout: template --apply requires a target page via --doc <uuid|name> or spec.page")
	}
	docs, activeUUID, resolvedWindow, err := discoverDocs(&local, win)
	if err != nil {
		return nil, "", "", fmt.Errorf("autolayout: discover target page: %w", err)
	}
	resolve := func(selector, source string) (openableDoc, error) {
		doc, derr := resolveDoc(docs, selector)
		if derr != nil {
			return openableDoc{}, fmt.Errorf("%s %q: %w", source, selector, derr)
		}
		if doc.Type != "schematic" {
			return openableDoc{}, fmt.Errorf("%s %q resolves to %s document %s; template autolayout requires a schematic page", source, selector, doc.Type, doc.UUID)
		}
		return doc, nil
	}

	var target openableDoc
	if docSelector != "" {
		target, err = resolve(docSelector, "--doc")
		if err != nil {
			return nil, "", "", err
		}
	}
	if specSelector != "" {
		specTarget, serr := resolve(specSelector, "spec.page")
		if serr != nil {
			return nil, "", "", serr
		}
		if target.UUID != "" && target.UUID != specTarget.UUID {
			return nil, "", "", fmt.Errorf("autolayout: --doc resolves to %s but spec.page resolves to %s; refusing an ambiguous apply target", target.UUID, specTarget.UUID)
		}
		target = specTarget
	}
	local.doc = target.UUID
	if err := ensureActiveDoc(&local, resolvedWindow); err != nil {
		return nil, "", "", fmt.Errorf("autolayout: activate target page %s: %w", target.UUID, err)
	}
	if activeUUID != target.UUID && !waitDocSettle(&local, resolvedWindow) {
		return nil, "", "", fmt.Errorf("autolayout: target page %s did not settle after switching; refusing to treat a possibly shallow zero-primitive snapshot as safe", target.UUID)
	}
	cur, err := requestAction(&local, "document.current", resolvedWindow, nil)
	if err != nil {
		return nil, "", "", fmt.Errorf("autolayout: confirm target page %s: %w", target.UUID, err)
	}
	if err := requireActionDocument(cur, target.UUID, "target confirmation"); err != nil {
		return nil, "", "", err
	}
	return &local, resolvedWindow, target.UUID, nil
}

func requireActionDocument(res *actionResult, targetUUID, phase string) error {
	if targetUUID == "" {
		return nil
	}
	if res == nil || res.Context == nil || res.Context.DocumentUUID == "" {
		return fmt.Errorf("autolayout: %s response omitted document context; refusing to assume it ran on %s", phase, targetUUID)
	}
	if res.Context.DocumentUUID != targetUUID {
		return fmt.Errorf("autolayout: page drift during %s: response came from %s, want %s", phase, res.Context.DocumentUUID, targetUUID)
	}
	return nil
}

func requestAutolayoutAction(cfg *appConfig, action, window string, payload any, targetUUID, phase string) (*actionResult, error) {
	// 队列阻塞的等待在 requestActionTimed 里统一做(见 queue_blocked_retry.go)——
	// 那是唯一的底层出口,--doc guard 的 pages.list 也从那里走,恢复段才穿得过去。
	timeout := defaultActionTimeout
	if action == "schematic.power.connect_pin" {
		// Every layout/recovery caller shares the same budget as sch connect.
		timeout = acConnectPinTimeout
	}
	return requestAutolayoutActionTimed(cfg, action, window, payload, timeout, targetUUID, phase)
}

func requestAutolayoutActionTimed(cfg *appConfig, action, window string, payload any, timeout time.Duration, targetUUID, phase string) (*actionResult, error) {
	res, err := requestActionTimed(cfg, action, window, payload, timeout)
	if err != nil {
		if res != nil {
			if contextErr := requireActionDocument(res, targetUUID, phase); contextErr != nil {
				return res, fmt.Errorf("%v; additionally, %w", err, contextErr)
			}
		}
		return res, err
	}
	if err := requireActionDocument(res, targetUUID, phase); err != nil {
		return res, err
	}
	return res, nil
}

func saveAutolayoutDocument(cfg *appConfig, window, targetUUID, phase string) error {
	res, err := requestAutolayoutAction(cfg, "schematic.save", window, nil, targetUUID, phase)
	if err != nil {
		return err
	}
	saved, ok := res.Result["saved"].(bool)
	if !ok || !saved {
		return fmt.Errorf("schematic.save returned saved=%v; persistence was not proven", res.Result["saved"])
	}
	return nil
}

func autolayoutGeometryPayload(includeConnectivity bool) map[string]any {
	payload := map[string]any{
		"includeBBox": true,
		"includePins": true,
	}
	if includeConnectivity {
		payload["includeConnectivitySummary"] = true
	}
	return payload
}

func compareAutolayoutInputs(before, after []alPart) error {
	if len(before) != len(after) {
		return fmt.Errorf("part count changed from %d to %d", len(before), len(after))
	}
	byID := make(map[string]alPart, len(after))
	for _, p := range after {
		if _, exists := byID[p.PrimitiveID]; exists {
			return fmt.Errorf("fresh snapshot contains duplicate primitiveId %s", p.PrimitiveID)
		}
		byID[p.PrimitiveID] = p
	}
	var drift []string
	eq := func(a, b float64) bool { return math.Abs(a-b) <= acCoordEps }
	for _, old := range before {
		fresh, ok := byID[old.PrimitiveID]
		if !ok {
			drift = append(drift, old.Designator+" was removed or rebound")
			continue
		}
		if old.Designator != fresh.Designator ||
			!eq(old.AnchorX, fresh.AnchorX) || !eq(old.AnchorY, fresh.AnchorY) ||
			!eq(old.Rotation, fresh.Rotation) || old.BBox != fresh.BBox ||
			len(old.Pins) != len(fresh.Pins) {
			drift = append(drift, old.Designator+" geometry changed")
			continue
		}
		for i := range old.Pins {
			if !eq(old.Pins[i].X, fresh.Pins[i].X) || !eq(old.Pins[i].Y, fresh.Pins[i].Y) {
				drift = append(drift, old.Designator+" pin geometry changed")
				break
			}
		}
	}
	if len(drift) > 0 {
		sort.Strings(drift)
		return fmt.Errorf("%s", strings.Join(drift, "; "))
	}
	return nil
}

// runAutolayout pulls real geometry, plans, optionally applies, and renders.
func runAutolayout(cfg *appConfig, window string, spec alSpec, rules autolayoutRules, apply, allPages, asJSON, zoneDraw bool, stdout, stderr io.Writer) error {
	if apply && allPages {
		return fmt.Errorf("autolayout: template --apply cannot be combined with --all-pages: mutation, connectivity inventory, and geometry proof are active-page scoped; use --all-pages only for dry-run")
	}
	if apply {
		if err := validateAutolayoutSpecForApply(spec); err != nil {
			return err
		}
	} else {
		// ADR-0004 Decision 4: 模板 dry-run(默认)必须纯计算 —— 机械保证,
		// 任何 Mutates=true 派发直接在 dispatch 层被拒。
		defer setDispatchDryRun(true)()
	}

	runCfg, win, targetUUID, err := pinTemplateAutolayoutTarget(cfg, window, spec, apply)
	if err != nil {
		return err
	}
	payload := autolayoutGeometryPayload(apply)
	if allPages {
		payload["allPages"] = true
	}
	res, err := requestAutolayoutAction(runCfg, "schematic.components.list", win, payload, targetUUID, "planning snapshot")
	if err != nil {
		return err
	}
	var (
		parts         []alPart
		sheet         *layoutBBox
		baselineParts []layoutComp
	)
	if apply {
		connectivity, cerr := parseAutolayoutConnectivity(res.Result)
		if cerr != nil {
			return fmt.Errorf("autolayout: read connectivity before planning: %w", cerr)
		}
		if err := rejectConnectedTemplatePage(connectivity, "before planning"); err != nil {
			return err
		}
		parts, sheet, baselineParts, err = parseAutolayoutPartsChecked(res.Result, true)
		if err != nil {
			return fmt.Errorf("autolayout: planning snapshot: %w", err)
		}
	} else {
		parts, sheet = parseAutolayoutParts(res.Result)
	}
	if apply && sheet == nil {
		return fmt.Errorf("autolayout: no sheet bbox found; select/create an A4 sheet and verify with 'easyeda sch sheet-geometry' before --apply")
	}

	modules := make([]alModuleSpec, 0, len(spec.Modules))
	for _, m := range spec.Modules {
		modules = append(modules, alModuleSpec{Name: m.Name, Zone: m.Zone, Core: m.Core, Parts: m.Parts})
	}
	rep := planAutolayout(modules, parts, sheet, rules)
	rep.Page = spec.Page
	rep.baselineParts = baselineParts
	rep.rules = rules
	rep.targetDocumentUUID = targetUUID

	if apply {
		// Re-read the COMPLETE scene immediately before the first mutation. This
		// narrows the TOCTOU window and rejects both new connectivity and any part
		// anchor/bbox/pin drift that would make the deterministic plan stale.
		if rep.OK {
			freshRes, ferr := requestAutolayoutAction(runCfg, "schematic.components.list", win, autolayoutGeometryPayload(true), targetUUID, "pre-apply snapshot")
			if ferr != nil {
				return fmt.Errorf("autolayout: refresh immediately before apply: %w", ferr)
			}
			freshConnectivity, cerr := parseAutolayoutConnectivity(freshRes.Result)
			if cerr != nil {
				return fmt.Errorf("autolayout: read connectivity immediately before apply: %w", cerr)
			}
			if err := rejectConnectedTemplatePage(freshConnectivity, "immediately before apply"); err != nil {
				return err
			}
			freshParts, freshSheet, _, perr := parseAutolayoutPartsChecked(freshRes.Result, true)
			if perr != nil {
				return fmt.Errorf("autolayout: pre-apply snapshot: %w", perr)
			}
			if freshSheet == nil || *freshSheet != *sheet {
				return fmt.Errorf("autolayout: page changed after planning; sheet geometry changed from %+v to %+v", *sheet, freshSheet)
			}
			if err := compareAutolayoutInputs(parts, freshParts); err != nil {
				return fmt.Errorf("autolayout: page changed after planning; refusing stale placements: %w", err)
			}
		}
		applyAutolayout(runCfg, win, &rep, stderr)
		// Zone-draw as a first-class step of the placement flow (issue #142): once
		// parts land cleanly, auto-draw the functional partition frames + labels
		// from the spec's modules[].zone — the "先看区、再看线" visualization stops
		// being a manual follow-up. Placement stays applied if decoration fails,
		// but the command returns non-zero instead of claiming the whole flow done.
		if zoneDraw && rep.OK {
			if zerr := drawAutolayoutZones(runCfg, win, targetUUID, buildAutolayoutZoneClaims(spec.Modules, stderr), sheet, stdout); zerr != nil {
				rep.Errors = append(rep.Errors, fmt.Sprintf("zone-draw after verified placement: %v (placement remains applied and saved)", zerr))
				rep.OK = false
			}
		}
	}

	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			return err
		}
	} else {
		renderAutolayoutReport(rep, apply, stdout)
	}
	if !rep.OK {
		return fmt.Errorf("autolayout: incomplete (%d error(s), %d overlap(s))", len(rep.Errors), rep.Validation.PartOverlaps)
	}
	return nil
}

// buildAutolayoutZoneClaims converts the spec's zoned modules into schematic zone
// claims (issue #142). Modules without a zone or parts are skipped; an unknown
// zone name is skipped with a warning — planning already ran, so a bad zone must
// not abort the drawing step.
func buildAutolayoutZoneClaims(modules []alSpecModule, stderr io.Writer) map[string]*schZoneClaim {
	out := map[string]*schZoneClaim{}
	for i, m := range modules {
		zone := strings.ToLower(strings.TrimSpace(m.Zone))
		if zone == "" || len(m.Parts) == 0 {
			continue
		}
		if !pcbZoneNames[zone] {
			fmt.Fprintf(stderr, "autolayout: zone-draw skipping module %q — unknown zone %q\n", m.Name, m.Zone)
			continue
		}
		name := strings.TrimSpace(m.Name)
		if name == "" {
			name = fmt.Sprintf("module%d", i+1)
		}
		out[name] = &schZoneClaim{Zone: zone, Parts: normalizeDesignators(m.Parts), Note: "autolayout"}
	}
	return out
}

func execAutolayoutZoneJS(cfg *appConfig, window, targetUUID, phase, code string) (map[string]any, error) {
	res, err := requestAutolayoutActionTimed(cfg, "debug.exec_js", window, map[string]any{"code": code}, 30*time.Second, targetUUID, phase)
	if err != nil {
		return nil, err
	}
	v, _ := res.Result["value"].(map[string]any)
	if v == nil {
		return nil, fmt.Errorf("exec_js returned no value (result: %v)", res.Result)
	}
	return v, nil
}

// drawAutolayoutZones persists the spec's zone claims and draws them as dashed
// frames + labels on the active sheet after a successful apply (issue #142). It
// reuses the exact zone-draw geometry (buildZoneDrawJS) so the frames match the
// layout-lint zone-violation gate, and records the primitive ids in workflow
// state so `sch zone-draw --clear` removes precisely what it drew. The placement
// is already durably saved, but a requested zone-draw is still part of command
// completion: page-context/draw/state/save failure returns non-zero rather than
// silently claiming the full flow succeeded.
func drawAutolayoutZones(cfg *appConfig, window, targetUUID string, claims map[string]*schZoneClaim, sheet *layoutBBox, stdout io.Writer) error {
	if len(claims) == 0 {
		return nil
	}
	if sheet == nil {
		return fmt.Errorf("no sheet bbox on the target page")
	}
	project, err := resolveStageProject(cfg, window)
	if err != nil {
		return err
	}
	st, err := loadPcbStageState(project)
	if err != nil {
		return err
	}
	// Persist the partition so `sch zones status` and layout-lint see the same claims.
	st.SetSchZonesForPage(targetUUID, claims)
	st.SchZones = nil
	// 分区框一律**数据驱动**(固定九宫格已废弃):框从活体模块 bbox 反推,
	// 所以要在落子之后画 —— 此刻件已经在目标位置,框才套得住电路。
	if err := runPartitionDraw(cfg, window, defaultPartitionOpts(),
		defaultPartitionZoneFontSize, "#AA00AA", false, io.Discard, io.Discard); err != nil {
		return fmt.Errorf("draw partition frames: %w", err)
	}
	return nil
}

func sameUntouchedLayoutGeometry(before, after layoutComp) bool {
	if before.ID != after.ID ||
		before.Designator != after.Designator ||
		before.ComponentType != after.ComponentType ||
		!before.AnchorAvailable || !after.AnchorAvailable ||
		math.Abs(before.X-after.X) > acCoordEps ||
		math.Abs(before.Y-after.Y) > acCoordEps ||
		before.BBox == nil || after.BBox == nil ||
		*before.BBox != *after.BBox ||
		!before.PinsProofKnown || !after.PinsProofKnown ||
		!before.PinsAvailable || !after.PinsAvailable ||
		len(before.Pins) != len(after.Pins) {
		return false
	}
	for i := range before.Pins {
		if before.Pins[i].Number != after.Pins[i].Number ||
			math.Abs(before.Pins[i].X-after.Pins[i].X) > acCoordEps ||
			math.Abs(before.Pins[i].Y-after.Pins[i].Y) > acCoordEps {
			return false
		}
	}
	return true
}

func validateAppliedAutolayout(parts, baseline []layoutComp, sheet *layoutBBox, placements []alPlacement, rules autolayoutRules) []string {
	byID := make(map[string]layoutComp, len(parts))
	for _, c := range parts {
		byID[c.ID] = c
	}
	moved := make(map[string]bool, len(placements))
	for _, pl := range placements {
		moved[pl.PrimitiveID] = true
	}
	var issues []string
	baselineByID := make(map[string]layoutComp, len(baseline))
	for _, c := range baseline {
		baselineByID[c.ID] = c
	}
	if len(parts) != len(baseline) {
		issues = append(issues, fmt.Sprintf("part identity count changed from %d to %d after apply", len(baseline), len(parts)))
	}
	for id, before := range baselineByID {
		after, ok := byID[id]
		if !ok {
			issues = append(issues, fmt.Sprintf("baseline part %s (%s) is missing after apply", label(before), id))
			continue
		}
		if !moved[id] && !sameUntouchedLayoutGeometry(before, after) {
			issues = append(issues, fmt.Sprintf("untouched part %s (%s) geometry drifted during apply", label(before), id))
		}
	}
	for id, after := range byID {
		if _, existed := baselineByID[id]; !existed {
			issues = append(issues, fmt.Sprintf("unexpected part %s (%s) appeared during apply", label(after), id))
		}
	}
	if sheet == nil {
		issues = append(issues, "sheet bounds are unavailable after apply")
	}
	for _, pl := range placements {
		c, ok := byID[pl.PrimitiveID]
		if !ok {
			issues = append(issues, fmt.Sprintf("%s (%s) is missing after apply", pl.Designator, pl.PrimitiveID))
			continue
		}
		if !c.AnchorAvailable || math.Abs(c.X-pl.X) > acCoordEps || math.Abs(c.Y-pl.Y) > acCoordEps {
			issues = append(issues, fmt.Sprintf("%s read back at (%.4f,%.4f), want (%.4f,%.4f)", pl.Designator, c.X, c.Y, pl.X, pl.Y))
		}
		if math.Abs(c.X-math.Round(c.X/schAnchorGrid)*schAnchorGrid) > acCoordEps ||
			math.Abs(c.Y-math.Round(c.Y/schAnchorGrid)*schAnchorGrid) > acCoordEps {
			issues = append(issues, fmt.Sprintf("%s anchor is off the %.0f-unit grid", pl.Designator, float64(schAnchorGrid)))
		}
		if c.BBox == nil {
			issues = append(issues, pl.Designator+" has no rendered bbox after apply")
		} else if sheet != nil && !boxInside(*c.BBox, *sheet) {
			issues = append(issues, pl.Designator+" lies outside the sheet bounds after apply")
		}
		if !c.PinsProofKnown || !c.PinsAvailable {
			issues = append(issues, pl.Designator+" has no proven pin geometry after apply")
		}
	}

	if rules.AvoidTitleBlock {
		tb, provisional := titleBlockKeepout(sheet)
		if provisional || tb == nil {
			issues = append(issues, "title-block keep-out could not be verified after apply")
		} else {
			for _, c := range parts {
				if moved[c.ID] && c.BBox != nil && boxesOverlap(*c.BBox, *tb) {
					issues = append(issues, label(c)+" overlaps the title-block keep-out")
				}
			}
		}
	}

	for i := 0; i < len(parts); i++ {
		for j := i + 1; j < len(parts); j++ {
			a, b := parts[i], parts[j]
			if !moved[a.ID] && !moved[b.ID] {
				continue // historical issue between two untouched parts
			}
			if a.BBox == nil || b.BBox == nil {
				continue
			}
			if boxesOverlap(*a.BBox, *b.BBox) {
				issues = append(issues, fmt.Sprintf("%s overlaps %s after apply", label(a), label(b)))
			} else if gap := rectGap(*a.BBox, *b.BBox); gap < rules.PartGap-acCoordEps {
				issues = append(issues, fmt.Sprintf("%s ↔ %s gap %.4f is below required %.4f", label(a), label(b), gap, rules.PartGap))
			}
			for _, ap := range a.Pins {
				for _, bp := range b.Pins {
					if math.Hypot(ap.X-bp.X, ap.Y-bp.Y) <= acCoordEps {
						issues = append(issues, fmt.Sprintf("%s pin %s coincides with %s pin %s at %.4f,%.4f", label(a), ap.Number, label(b), bp.Number, ap.X, ap.Y))
						break
					}
				}
			}
		}
	}
	sort.Strings(issues)
	return issues
}

// applyAutolayout mutates the pinned page (component.modify per placement), then
// re-pulls the complete rendered scene and proves that every requested anchor
// landed, remains on-grid, has bbox/pin geometry, respects spacing/title-block
// rules, and did not create a pin coincidence. Any failure triggers a
// reverse-order rollback whose own coordinate readback is verified before it is
// counted as restored.
func applyAutolayout(cfg *appConfig, window string, rep *alReport, stderr io.Writer) {
	if !rep.OK {
		rep.Note = strings.TrimSpace(rep.Note + " plan has errors — nothing applied.")
		return
	}
	applied := 0
	attempted := make([]alPlacement, 0, len(rep.Placements))
	for i := range rep.Placements {
		pl := &rep.Placements[i]
		if pl.PrimitiveID == "" {
			rep.Errors = append(rep.Errors, fmt.Sprintf("apply %s: primitive id is empty; refusing to skip a planned placement", pl.Designator))
			rep.OK = false
			rollbackAutolayout(cfg, window, attempted, rep, stderr)
			return
		}
		if !pl.HasOriginal {
			rep.Errors = append(rep.Errors, fmt.Sprintf("apply %s: original anchor is unavailable; refusing an unrollbackable move", pl.Designator))
			rep.OK = false
			rollbackAutolayout(cfg, window, attempted, rep, stderr)
			return
		}
		// Include the current placement before dispatch: an action can fail after
		// mutating remotely, so restoring its original anchor is harmless when the
		// mutation never landed and essential when the response was ambiguous.
		attempted = append(attempted, *pl)
		_, aerr := requestAutolayoutAction(cfg, "schematic.component.modify", window, map[string]any{
			"primitiveId": pl.PrimitiveID,
			"patch":       map[string]any{"x": pl.X, "y": pl.Y},
		}, rep.targetDocumentUUID, "apply "+pl.Designator)
		if aerr != nil {
			rep.Errors = append(rep.Errors, fmt.Sprintf("apply %s: %v", pl.Designator, aerr))
			rep.OK = false
			rollbackAutolayout(cfg, window, attempted, rep, stderr)
			return
		}
		applied++
	}
	rep.Note = strings.TrimSpace(rep.Note + fmt.Sprintf(" applied %d/%d placements.", applied, len(rep.Placements)))

	// Post-apply self-check: pull the complete real scene. An empty/shallow list
	// cannot pass because the checked parser and requested-placement readback both
	// require every target id and its proven geometry.
	lres, lerr := requestAutolayoutAction(cfg, "schematic.components.list", window, autolayoutGeometryPayload(true), rep.targetDocumentUUID, "post-apply snapshot")
	if lerr != nil {
		rep.Errors = append(rep.Errors, fmt.Sprintf("post-apply layout-lint read failed: %v", lerr))
		rep.OK = false
		rollbackAutolayout(cfg, window, attempted, rep, stderr)
		return
	}
	connectivity, cerr := parseAutolayoutConnectivity(lres.Result)
	if cerr != nil {
		rep.Errors = append(rep.Errors, fmt.Sprintf("post-apply connectivity read failed: %v", cerr))
		rep.OK = false
		rollbackAutolayout(cfg, window, attempted, rep, stderr)
		return
	}
	if cerr := rejectConnectedTemplatePage(connectivity, "after apply"); cerr != nil {
		rep.Errors = append(rep.Errors, cerr.Error())
		rep.OK = false
		rollbackAutolayout(cfg, window, attempted, rep, stderr)
		return
	}
	_, sheet, kept, perr := parseAutolayoutPartsChecked(lres.Result, true)
	if perr != nil {
		rep.Errors = append(rep.Errors, fmt.Sprintf("post-apply layout-lint parse failed: %v", perr))
		rep.OK = false
		rollbackAutolayout(cfg, window, attempted, rep, stderr)
		return
	}
	issues := validateAppliedAutolayout(kept, rep.baselineParts, sheet, rep.Placements, rep.rules)
	if len(issues) > 0 {
		rep.Validation.PartOverlaps = 0
		for _, issue := range issues {
			if strings.Contains(issue, " overlaps ") {
				rep.Validation.PartOverlaps++
			}
		}
		rep.Errors = append(rep.Errors, "post-apply verification: "+strings.Join(issues, "; "))
		rep.OK = false
		rollbackAutolayout(cfg, window, attempted, rep, stderr)
		return
	}
	if serr := saveAutolayoutDocument(cfg, window, rep.targetDocumentUUID, "save verified layout"); serr != nil {
		rep.Errors = append(rep.Errors, fmt.Sprintf("save verified layout: %v", serr))
		rep.OK = false
		rollbackAutolayout(cfg, window, attempted, rep, stderr)
		return
	}
	rep.Note = strings.TrimSpace(rep.Note + " post-apply geometry verified and schematic saved.")
}

// rollbackAutolayout restores attempted placements in reverse order from the
// planning snapshot, then reads their anchors back and saves the page. It is not
// a transaction: any unconfirmed coordinate or remote failure is surfaced in
// Errors/stderr instead of being counted as restored.
func rollbackAutolayout(cfg *appConfig, window string, attempted []alPlacement, rep *alReport, stderr io.Writer) {
	if len(attempted) == 0 {
		rep.Note = strings.TrimSpace(rep.Note + " no placements required rollback.")
		return
	}
	for i := len(attempted) - 1; i >= 0; i-- {
		pl := attempted[i]
		if !pl.HasOriginal || pl.PrimitiveID == "" {
			msg := fmt.Sprintf("rollback %s: original anchor or primitive id unavailable", pl.Designator)
			rep.Errors = append(rep.Errors, msg)
			fmt.Fprintf(stderr, "autolayout: %s\n", msg)
			continue
		}
		_, err := requestAutolayoutAction(cfg, "schematic.component.modify", window, map[string]any{
			"primitiveId": pl.PrimitiveID,
			"patch":       map[string]any{"x": pl.OriginalX, "y": pl.OriginalY},
		}, rep.targetDocumentUUID, "rollback "+pl.Designator)
		if err != nil {
			msg := fmt.Sprintf("rollback %s to (%.2f, %.2f): %v", pl.Designator, pl.OriginalX, pl.OriginalY, err)
			rep.Errors = append(rep.Errors, msg)
			fmt.Fprintf(stderr, "autolayout: %s\n", msg)
			continue
		}
	}

	// Count only coordinates proven by a fresh readback. HTTP ok alone is not a
	// restoration guarantee: the editor may acknowledge before its model settles.
	restored := 0
	verifyRes, verr := requestAutolayoutAction(cfg, "schematic.components.list", window, map[string]any{}, rep.targetDocumentUUID, "rollback verification")
	if verr != nil {
		msg := fmt.Sprintf("rollback verification read failed: %v", verr)
		rep.Errors = append(rep.Errors, msg)
		fmt.Fprintf(stderr, "autolayout: %s\n", msg)
	} else {
		comps, perr := parseLayoutComps(verifyRes.Result)
		if perr != nil {
			msg := fmt.Sprintf("rollback verification parse failed: %v", perr)
			rep.Errors = append(rep.Errors, msg)
			fmt.Fprintf(stderr, "autolayout: %s\n", msg)
		} else {
			byID := make(map[string]layoutComp, len(comps))
			for _, c := range comps {
				byID[c.ID] = c
			}
			for _, pl := range attempted {
				c, ok := byID[pl.PrimitiveID]
				if ok && c.AnchorAvailable &&
					math.Abs(c.X-pl.OriginalX) <= acCoordEps &&
					math.Abs(c.Y-pl.OriginalY) <= acCoordEps {
					restored++
					continue
				}
				msg := fmt.Sprintf("rollback %s was not confirmed at original anchor (%.2f, %.2f)", pl.Designator, pl.OriginalX, pl.OriginalY)
				rep.Errors = append(rep.Errors, msg)
				fmt.Fprintf(stderr, "autolayout: %s\n", msg)
			}
		}
	}
	rollbackSaved := true
	if serr := saveAutolayoutDocument(cfg, window, rep.targetDocumentUUID, "save rollback"); serr != nil {
		rollbackSaved = false
		msg := fmt.Sprintf("save rollback: %v", serr)
		rep.Errors = append(rep.Errors, msg)
		fmt.Fprintf(stderr, "autolayout: %s\n", msg)
	}
	saveNote := " and saved the schematic."
	if !rollbackSaved {
		saveNote = "; rollback save FAILED."
	}
	rep.Note = strings.TrimSpace(rep.Note + fmt.Sprintf(" rollback confirmed %d/%d attempted placement(s) at the planning snapshot%s", restored, len(attempted), saveNote))
}

// renderAutolayoutReport prints a compact human summary.
func renderAutolayoutReport(rep alReport, apply bool, w io.Writer) {
	mode := "plan (dry-run)"
	if apply {
		mode = "apply"
	}
	fmt.Fprintf(w, "autolayout: %d placement(s), mode=%s", len(rep.Placements), mode)
	if rep.Page != "" {
		fmt.Fprintf(w, ", page=%s", rep.Page)
	}
	fmt.Fprintln(w)
	if rep.Note != "" {
		fmt.Fprintf(w, "  note: %s\n", rep.Note)
	}
	if rep.TitleBlockProvisional {
		fmt.Fprintf(w, "  note: title-block keep-out is provisional (no sheet bbox) — NOT enforced\n")
	}
	for _, p := range rep.Placements {
		retry := ""
		if p.Retries > 0 {
			retry = fmt.Sprintf(" (retry %d)", p.Retries)
		}
		fmt.Fprintf(w, "  • %-6s [%s] → (%.2f, %.2f) rot=%.0f%s\n", p.Designator, p.Module, p.X, p.Y, p.Rotation, retry)
	}
	for _, wn := range rep.Warnings {
		fmt.Fprintf(w, "  WARN  [%s] %s\n", wn.Module, wn.Message)
	}
	for _, e := range rep.Errors {
		fmt.Fprintf(w, "  ERROR %s\n", e)
	}
	v := rep.Validation
	fmt.Fprintf(w, "  validation: partOverlaps=%d titleBlockHits=%d fanoutKeepoutHits=%d\n", v.PartOverlaps, v.TitleBlockHits, v.FanoutKeepoutHits)
	if rep.OK {
		fmt.Fprintf(w, "✓ layout plan OK\n")
	} else {
		fmt.Fprintf(w, "✗ layout incomplete\n")
	}
}

// newAutolayoutCmd builds the `sch autolayout` subcommand.
func newAutolayoutCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var (
		spec                                            string
		engine                                          string
		dryRun, apply, rewire                           bool
		allPages, asJSON                                bool
		zoneDraw                                        bool
		avoidTitleBlock, preserveFanout, preferVertical bool
		moduleGap, channelGap, partGap                  float64
	)
	c := &cobra.Command{
		Use:   "autolayout",
		Short: "Module-aware planner: place parts by module zone with deterministic, lint-clean coordinates",
		Long: `Module-aware schematic placement planner.

autolayout solves MODULE-LEVEL placement (not routing): it reads a --spec (page,
sheet, modules with zone/core/parts, rules), partitions the usable canvas into
named zones (left-top / left-bottom / center / right / right-top / right-bottom /
…), places each module's core IC near its zone center, fans peripherals around
the core, and retries candidate positions on collision — all while preserving
each core pin's fanout channel and the A4 title-block keep-out.

The planner is PURE and deterministic: the same spec on the same input always
yields identical coordinates that pass 'sch layout-lint'. v1 only MOVES parts
that are already placed (it does not create missing parts).

TWO ENGINES (--engine):
  template  (default) our spec-driven functional-group planner above — clean,
            deterministic, needs --spec. Best for KNOWN blocks/modules. It only
            moves parts, so --apply REFUSES an active page that already has any
            wire, bus, netflag, netport, or netlabel. Run it before wiring; there
            is no unsafe force override.
  official  the platform's own eda.sch_Document.autoLayout() (@beta) — a generic
            connectivity-clustered FALLBACK for un-templated pages. No spec, but
            it is a LONG op (~2min), rearranges the WHOLE active schematic page,
            and is messier than a template. Needs the target page foreground.
            It is DESTRUCTIVE: it moves parts without attached connectivity and
            places off-grid. It atomically guards sheet + part poses + wire/bus/
            marker counts, refuses buses (not rebuildable), requires --rewire
            for other existing connectivity, snaps to grid, self-checks geometry
            + wiring, and proves the save.

  --rewire  (official only; NOT a template override) after layout, delete the now-broken wiring and
            rebuild it from the netlist captured BEFORE the run. Best-effort: a
            scattered layout can leave stub-collision shorts. Required to run
            official on a page that is already wired.

  --dry-run  return proposed coordinates + warnings, mutate nothing (default)
  --apply    pin one target page (--doc or spec.page), prove complete bbox/pin
             geometry + zero connectivity before planning and again before the
             first move, apply, read every target back, verify grid/spacing/
             overlap/pin/title-block constraints, and save. Any failure rolls
             back, reads the original anchors back, and saves the rollback.
  --all-pages template dry-run only; apply is refused because mutation and the
             safety proofs are scoped to one active page
  --json     emit the structured report

Spec shape:
  {
    "page": "P1_MCU_USB_STORAGE", "sheet": "A4",
    "modules": [
      {"name":"MCU","zone":"center","core":"U1","parts":["U1","C18","R6"]}
    ],
    "rules": {"avoidTitleBlock":true,"preservePinFanout":true,
              "moduleGap":80,"routeChannelGap":40,
              "preferVerticalPeripheralPlacement":true}
  }`,
		Args: cobra.NoArgs,
		Example: `  easyeda sch autolayout --spec p1-layout.json --dry-run
  easyeda sch autolayout --spec p1-layout.json --doc P1_MCU_USB_STORAGE --apply
  easyeda sch autolayout --spec p1-layout.json --json
  easyeda sch autolayout --engine official --apply             # unwired page: place only
  easyeda sch autolayout --engine official --apply --rewire    # wired page: layout + rebuild wiring`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if dryRun && apply {
				return fmt.Errorf("--dry-run and --apply are mutually exclusive")
			}
			// v1 group awareness (warn only): both engines place parts individually
			// and do NOT preserve intra-group relative geometry.
			warnSchGroupsPresent(cfg, *window, "autolayout", stderr)
			// The official engine has a totally different interface (no spec): it
			// wraps the platform's own long-running autoLayout over the active page.
			switch engine {
			case "official":
				if spec != "" {
					fmt.Fprintln(stderr, "note: --spec is ignored by --engine official (the platform lays out the whole active page)")
				}
				return runOfficialAutolayout(cfg, *window, apply, rewire, stdout, stderr)
			case "", "template":
				if rewire {
					return fmt.Errorf("--rewire is only supported by --engine official; template has no safe capture+rewire path and does not allow an already-wired page")
				}
				// fall through to the spec-driven planner below
			default:
				return fmt.Errorf("unknown --engine %q (template|official)", engine)
			}
			if spec == "" {
				return fmt.Errorf("--spec is required for --engine template (a layout spec JSON file); or use --engine official for the platform fallback")
			}
			raw, err := os.ReadFile(spec)
			if err != nil {
				return fmt.Errorf("read --spec: %w", err)
			}
			var s alSpec
			if err := json.Unmarshal(raw, &s); err != nil {
				return fmt.Errorf("invalid --spec json: %w", err)
			}
			if len(s.Modules) == 0 {
				return fmt.Errorf("--spec has no modules")
			}
			for _, m := range s.Modules {
				if m.Name == "" {
					return fmt.Errorf("every module needs a name")
				}
				if len(m.Parts) == 0 {
					return fmt.Errorf("module %q has no parts", m.Name)
				}
			}

			rules := defaultAutolayoutRules()
			rules = s.Rules.applyTo(rules)
			// Explicit CLI flags win over the spec's rules block.
			if cmd.Flags().Changed("avoid-titleblock") {
				rules.AvoidTitleBlock = avoidTitleBlock
			}
			if cmd.Flags().Changed("preserve-pin-fanout") {
				rules.PreservePinFanout = preserveFanout
			}
			if cmd.Flags().Changed("prefer-vertical") {
				rules.PreferVertical = preferVertical
			}
			if cmd.Flags().Changed("module-gap") {
				rules.ModuleGap = moduleGap
			}
			if cmd.Flags().Changed("route-channel-gap") {
				rules.RouteChannelGap = channelGap
			}
			if cmd.Flags().Changed("part-gap") {
				rules.PartGap = partGap
			}

			return runAutolayout(cfg, *window, s, rules, apply, allPages, asJSON, zoneDraw, stdout, stderr)
		},
	}
	c.Flags().StringVar(&spec, "spec", "", "layout spec JSON file (required for --engine template)")
	c.Flags().StringVar(&engine, "engine", "template", "placement engine: template (our spec-driven planner) | official (platform eda.sch_Document.autoLayout fallback)")
	c.Flags().BoolVar(&rewire, "rewire", false, "official only (not a template override): after layout, delete broken wiring and rebuild it from the pre-run netlist")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "plan and print proposed coordinates without mutating (default behavior)")
	c.Flags().BoolVar(&apply, "apply", false, "pin one page, require zero connectivity + complete geometry, move/readback/save, and verify any rollback")
	c.Flags().BoolVar(&allPages, "all-pages", false, "build the scene from all schematic pages (template dry-run only; incompatible with --apply)")
	c.Flags().BoolVar(&asJSON, "json", false, "emit the report as JSON")
	c.Flags().BoolVar(&zoneDraw, "zone-draw", true, "template --apply: auto-draw functional zone frames + labels from modules[].zone after placement (issue #142; --zone-draw=false to skip)")
	c.Flags().BoolVar(&avoidTitleBlock, "avoid-titleblock", true, "treat the title block as a hard keep-out")
	c.Flags().BoolVar(&preserveFanout, "preserve-pin-fanout", true, "keep peripherals out of core pin lead-out lanes")
	c.Flags().BoolVar(&preferVertical, "prefer-vertical", true, "try vertical peripheral placement before horizontal")
	c.Flags().Float64Var(&moduleGap, "module-gap", 80, "nominal inter-module breathing room")
	c.Flags().Float64Var(&channelGap, "route-channel-gap", 40, "length of a preserved pin-fanout channel")
	c.Flags().Float64Var(&partGap, "part-gap", 20, "minimum edge-to-edge gap between two parts")
	return c
}
