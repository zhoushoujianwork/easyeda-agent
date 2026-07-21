package app

import (
	"strings"
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/internal/blocks"
)

// fixtureDevices is a minimal stand-in for standard-parts.json covering the
// parts led_indicator_gpio uses.
func fixtureDevices() map[string]bapDevice {
	return map[string]bapDevice{
		"led.red_0805": {LibraryUUID: "lib", DeviceUUID: "dev-led", LCSC: "C1"},
		"res.1k_0402":  {LibraryUUID: "lib", DeviceUUID: "dev-res", LCSC: "C2"},
		"res.10k_0402": {LibraryUUID: "lib", DeviceUUID: "dev-res10k"},
	}
}

func ledBlock(t *testing.T) (blocks.Block, [][]string) {
	t.Helper()
	b, ok, err := blocks.Get("led_indicator_gpio")
	if err != nil || !ok {
		t.Fatalf("load led_indicator_gpio: ok=%v err=%v", ok, err)
	}
	topo, err := blockTopology(b)
	if err != nil {
		t.Fatal(err)
	}
	return b, topo
}

// TestPlanBlockApplyLed pins the whole plan for the canonical simple block: the
// role→designator allocation, the fallback-grid coordinates (bapInput.Layout is
// deliberately unset here — the template path has its own tests below), and the
// three resolved nets.
func TestPlanBlockApplyLed(t *testing.T) {
	b, topo := ledBlock(t)
	plan, err := planBlockApply(bapInput{
		Block: b, Topology: topo, Devices: fixtureDevices(),
		Existing: map[string]bool{}, OriginX: 400, OriginY: 300, Spacing: 100, PerRow: 4,
		Bind: map[string]string{"CTRL": "IO2", "GND": "GND"},
	})
	if err != nil {
		t.Fatalf("planBlockApply: %v", err)
	}

	if plan.Instance != "LED1" {
		t.Errorf("instance = %q, want LED1 (first allocated designator)", plan.Instance)
	}
	if len(plan.Placements) != 2 {
		t.Fatalf("placements = %d, want 2", len(plan.Placements))
	}
	// Roles are planned in sorted order: LED then R. Without an in-test template
	// (bapInput.Layout unset), coordinates follow the fallback grid.
	if got := plan.Placements[0]; got.Role != "LED" || got.Designator != "LED1" || got.DeviceUUID != "dev-led" {
		t.Errorf("placement[0] = %+v, want role LED / LED1 / dev-led", got)
	}
	if got := plan.Placements[1]; got.Role != "R" || got.Designator != "R1" || got.X != 500 {
		t.Errorf("placement[1] = %+v, want role R / R1 / x=500", got)
	}

	want := map[string]string{
		"IO2":     "R1:1",        // port CTRL, bound to the host net
		"LED1_N2": "R1:2 LED1:+", // purely internal
		"GND":     "LED1:-",      // port GND
	}
	if len(plan.Nets) != 3 {
		t.Fatalf("nets = %d, want 3", len(plan.Nets))
	}
	for _, n := range plan.Nets {
		w, ok := want[n.Net]
		if !ok {
			t.Errorf("unexpected net %q", n.Net)
			continue
		}
		if got := strings.Join(n.Members, " "); got != w {
			t.Errorf("net %s members = %q, want %q", n.Net, got, w)
		}
	}
	// GND must get a ground flag, not a bare net port.
	for _, n := range plan.Nets {
		if n.Net == "GND" && n.Kind != "gnd" {
			t.Errorf("GND kind = %q, want gnd", n.Kind)
		}
	}
	// The block carries constraint maps this command does not execute; the plan
	// must say so rather than let a green run imply full compliance.
	if len(plan.Unconsumed) == 0 {
		t.Error("unconsumed constraints not reported — a caller would read the apply as complete")
	}
}

// TestPlanBlockApplySecondInstanceDoesNotMerge is the regression that matters:
// same-name nets MERGE in EasyEDA, so if two instances of one block derived their
// internal net names from the BLOCK id, instance 2's internal node would silently
// short to instance 1's. Deriving the instance from the allocated designator keeps
// them apart.
func TestPlanBlockApplySecondInstanceDoesNotMerge(t *testing.T) {
	b, topo := ledBlock(t)
	base := bapInput{
		Block: b, Topology: topo, Devices: fixtureDevices(),
		OriginX: 400, OriginY: 300, Spacing: 100, PerRow: 4,
	}

	first := base
	first.Existing = map[string]bool{}
	p1, err := planBlockApply(first)
	if err != nil {
		t.Fatal(err)
	}

	// Second instance onto a page that already holds the first.
	second := base
	second.Existing = map[string]bool{}
	for _, p := range p1.Placements {
		second.Existing[strings.ToUpper(p.Designator)] = true
	}
	p2, err := planBlockApply(second)
	if err != nil {
		t.Fatal(err)
	}

	if p2.Instance == p1.Instance {
		t.Fatalf("both instances got id %q — internal nets would collide", p1.Instance)
	}
	internal := func(p bapPlan) string {
		for _, n := range p.Nets {
			if n.Port == "" {
				return n.Net
			}
		}
		return ""
	}
	n1, n2 := internal(p1), internal(p2)
	if n1 == "" || n2 == "" {
		t.Fatalf("no internal net found: %q / %q", n1, n2)
	}
	if n1 == n2 {
		t.Errorf("internal nets collide: both %q — the two instances would be shorted together", n1)
	}
	// And the designators must not collide either.
	for _, a := range p1.Placements {
		for _, b := range p2.Placements {
			if a.Designator == b.Designator {
				t.Errorf("designator %s reused across instances", a.Designator)
			}
		}
	}
}

// TestPlanBlockApplyRejectsUnknownBind: a typo'd port must fail before anything
// is placed, not leave a half-built block on the page.
func TestPlanBlockApplyRejectsUnknownBind(t *testing.T) {
	b, topo := ledBlock(t)
	_, err := planBlockApply(bapInput{
		Block: b, Topology: topo, Devices: fixtureDevices(), Existing: map[string]bool{},
		Bind: map[string]string{"CTRLL": "IO2"},
	})
	if err == nil || !strings.Contains(err.Error(), "no port") {
		t.Errorf("err = %v, want a no-such-port error", err)
	}
}

// TestPlanBlockApplyMissingPart: a part absent from standard-parts.json must be a
// hard error — placing a block with a hole in it is worse than not placing it.
func TestPlanBlockApplyMissingPart(t *testing.T) {
	b, topo := ledBlock(t)
	devices := fixtureDevices()
	delete(devices, "led.red_0805")
	_, err := planBlockApply(bapInput{
		Block: b, Topology: topo, Devices: devices, Existing: map[string]bool{},
	})
	if err == nil || !strings.Contains(err.Error(), "not found in standard-parts.json") {
		t.Errorf("err = %v, want a missing-part error", err)
	}
}

func TestBapPrefixFor(t *testing.T) {
	for _, tc := range []struct{ key, want string }{
		{"res.1k_0402", "R"},
		{"cap.100nf_0402", "C"},
		{"led.red_0805", "LED"},
		{"mcu.esp32s3_chip", "U"},
		{"conn.usb_c_16p", "J"},
		{"sw.tact_smd", "SW"},
		{"xtal.40mhz_3225_12pf", "X"},
	} {
		got, err := bapPrefixFor(tc.key)
		if err != nil || got != tc.want {
			t.Errorf("bapPrefixFor(%q) = %q,%v; want %q", tc.key, got, err, tc.want)
		}
	}
	// An unmapped namespace must fail loudly rather than invent a prefix.
	if _, err := bapPrefixFor("widget.thing"); err == nil {
		t.Error("unknown namespace silently accepted — it would misfile the part")
	}
	if _, err := bapPrefixFor("nodot"); err == nil {
		t.Error("namespace-less key silently accepted")
	}
}

func TestBapFlagKind(t *testing.T) {
	for _, tc := range []struct{ net, want string }{
		{"GND", "gnd"},
		{"AGND", "agnd"},
		{"DGND", "gnd"},
		{"CHASSIS_GND", "gnd"},
		{"+3V3", "power"},
		{"3V3", "power"},
		{"5V", "power"},
		{"1V8", "power"},
		{"VCC", "power"},
		{"VBUS", "power"},
		{"VSYS", "power"},
		{"IO2", "netport"},
		{"MCU_TX", "netport"},
		{"LED1_N2", "netport"},
		// A signal that merely starts with a digit is not a rail.
		{"2WIRE_SDA", "netport"},
	} {
		if got := bapFlagKind(tc.net); got != tc.want {
			t.Errorf("bapFlagKind(%q) = %q, want %q", tc.net, got, tc.want)
		}
	}
}

func TestParseKV(t *testing.T) {
	got, err := parseKV([]string{"CTRL=IO2", "GND=GND"}, "--bind")
	if err != nil {
		t.Fatal(err)
	}
	if got["CTRL"] != "IO2" || got["GND"] != "GND" {
		t.Errorf("parseKV = %v", got)
	}
	for _, bad := range []string{"noequals", "=novalue", "nokey="} {
		if _, err := parseKV([]string{bad}, "--bind"); err == nil {
			t.Errorf("parseKV(%q) accepted a malformed pair", bad)
		}
	}
}

// ── schematic_layout template + origin avoidance ────────────────────────────

// TestPlanBlockApplyTemplate: a block with a schematic_layout must place every
// templated role at origin+offset (grid-snapped) with its rotation, and mark
// the source so the manifest can tell template geometry from grid fallback.
func TestPlanBlockApplyTemplate(t *testing.T) {
	b, topo := ledBlock(t)
	layout, err := b.SchematicLayout()
	if err != nil {
		t.Fatal(err)
	}
	if layout == nil {
		t.Fatal("led_indicator_gpio should ship a schematic_layout template")
	}
	plan, err := planBlockApply(bapInput{
		Block: b, Topology: topo, Devices: fixtureDevices(), Existing: map[string]bool{},
		OriginX: 400, OriginY: 300, Spacing: 100, PerRow: 4, Layout: layout,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][3]float64{}
	for _, p := range plan.Placements {
		if p.Source != "template" {
			t.Errorf("%s source = %q, want template", p.Role, p.Source)
		}
		got[p.Role] = [3]float64{p.X, p.Y, p.Rotation}
	}
	// Shipped template: R at +0,+0, LED at +120,+0 (信号流左入右出).
	if got["R"] != [3]float64{400, 300, 0} {
		t.Errorf("R placed at %v, want 400,300 r0", got["R"])
	}
	if got["LED"] != [3]float64{520, 300, 0} {
		t.Errorf("LED placed at %v, want 520,300 r0", got["LED"])
	}
}

// TestPlanBlockApplyPartialTemplateFallsBack: roles the template misses drop to
// the grid BELOW the template extent, never interleaved with it.
func TestPlanBlockApplyPartialTemplateFallsBack(t *testing.T) {
	b, topo := ledBlock(t)
	layout := &blocks.SchematicLayout{Roles: map[string]blocks.SchematicLayoutHint{
		"LED": {DX: 0, DY: 0},
	}}
	plan, err := planBlockApply(bapInput{
		Block: b, Topology: topo, Devices: fixtureDevices(), Existing: map[string]bool{},
		OriginX: 400, OriginY: 300, Spacing: 100, PerRow: 4, Layout: layout,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range plan.Placements {
		switch p.Role {
		case "LED":
			if p.Source != "template" || p.Y != 300 {
				t.Errorf("LED = %+v, want template @ y=300", p)
			}
		case "R":
			// Grid base sits one spacing below the template's max dy (0+100).
			if p.Source != "grid" || p.Y != 400 || p.X != 400 {
				t.Errorf("R = %+v, want grid @ 400,400", p)
			}
		}
	}
}

// TestPlanBlockApplyOriginDodgesObstacles: with existing bboxes squatting on the
// default origin, a non-explicit --at must relocate the whole block to a free
// region (deterministically), and record the move in the plan.
func TestPlanBlockApplyOriginDodgesObstacles(t *testing.T) {
	b, topo := ledBlock(t)
	obstacle := layoutBBox{MinX: 300, MinY: 200, MaxX: 600, MaxY: 400} // covers 400,300
	plan, err := planBlockApply(bapInput{
		Block: b, Topology: topo, Devices: fixtureDevices(), Existing: map[string]bool{},
		OriginX: 400, OriginY: 300, Spacing: 100, PerRow: 4,
		Obstacles: []layoutBBox{obstacle},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Origin == nil || !plan.Origin.Relocated {
		t.Fatalf("origin not relocated: %+v", plan.Origin)
	}
	if plan.Origin.X == 400 && plan.Origin.Y == 300 {
		t.Error("relocated origin equals the requested one")
	}
	// The relocated placements must actually clear the obstacle (+ margin).
	for _, p := range plan.Placements {
		box := layoutBBox{MinX: p.X - bapPartMargin, MinY: p.Y - bapPartMargin, MaxX: p.X + bapPartMargin, MaxY: p.Y + bapPartMargin}
		if boxesOverlap(box, obstacle) {
			t.Errorf("%s at %.0f,%.0f still inside the obstacle", p.Designator, p.X, p.Y)
		}
		if p.X != snapAnchor(p.X) || p.Y != snapAnchor(p.Y) {
			t.Errorf("%s at %.0f,%.0f is off the %d-grid", p.Designator, p.X, p.Y, int(schAnchorGrid))
		}
	}
	// Same input → same relocation (determinism).
	plan2, err := planBlockApply(bapInput{
		Block: b, Topology: topo, Devices: fixtureDevices(), Existing: map[string]bool{},
		OriginX: 400, OriginY: 300, Spacing: 100, PerRow: 4,
		Obstacles: []layoutBBox{obstacle},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan2.Origin.X != plan.Origin.X || plan2.Origin.Y != plan.Origin.Y {
		t.Errorf("relocation not deterministic: %v vs %v", plan.Origin, plan2.Origin)
	}
}

// TestPlanBlockApplyExplicitAtWins: an explicit --at is the caller's decision —
// no relocation, but the collision must surface as a warning, never silently.
func TestPlanBlockApplyExplicitAtWins(t *testing.T) {
	b, topo := ledBlock(t)
	plan, err := planBlockApply(bapInput{
		Block: b, Topology: topo, Devices: fixtureDevices(), Existing: map[string]bool{},
		OriginX: 400, OriginY: 300, Spacing: 100, PerRow: 4,
		Obstacles:  []layoutBBox{{MinX: 300, MinY: 200, MaxX: 600, MaxY: 400}},
		AtExplicit: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Origin.Relocated {
		t.Error("explicit --at was relocated — the origin is the caller's decision")
	}
	if len(plan.Warnings) == 0 {
		t.Error("colliding explicit --at produced no warning")
	}
}

// TestPlanBlockApplyOriginDodgesTitleBlock (issue #141): with NO part obstacles
// but a title-block keep-out squatting on the default origin, a non-explicit --at
// must relocate the block clear of the 图签 — the title block is a first-class
// obstacle, not just other parts.
func TestPlanBlockApplyOriginDodgesTitleBlock(t *testing.T) {
	b, topo := ledBlock(t)
	titleBlock := layoutBBox{MinX: 300, MinY: 200, MaxX: 600, MaxY: 400} // covers 400,300
	plan, err := planBlockApply(bapInput{
		Block: b, Topology: topo, Devices: fixtureDevices(), Existing: map[string]bool{},
		OriginX: 400, OriginY: 300, Spacing: 100, PerRow: 4,
		TitleBlock: &titleBlock, // no part obstacles at all
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Origin == nil || !plan.Origin.Relocated {
		t.Fatalf("origin not relocated off the title block: %+v", plan.Origin)
	}
	// Every placement must clear the title-block rectangle (+ margin).
	for _, p := range plan.Placements {
		box := layoutBBox{MinX: p.X - bapPartMargin, MinY: p.Y - bapPartMargin, MaxX: p.X + bapPartMargin, MaxY: p.Y + bapPartMargin}
		if boxesOverlap(box, titleBlock) {
			t.Errorf("%s at %.0f,%.0f still overlaps the title block", p.Designator, p.X, p.Y)
		}
	}
}

// TestPlanBlockApplyExplicitAtOverTitleBlock: an explicit --at over the title
// block is honoured (no relocation) but must warn — same contract as parts.
func TestPlanBlockApplyExplicitAtOverTitleBlock(t *testing.T) {
	b, topo := ledBlock(t)
	titleBlock := layoutBBox{MinX: 300, MinY: 200, MaxX: 600, MaxY: 400}
	plan, err := planBlockApply(bapInput{
		Block: b, Topology: topo, Devices: fixtureDevices(), Existing: map[string]bool{},
		OriginX: 400, OriginY: 300, Spacing: 100, PerRow: 4,
		TitleBlock: &titleBlock, AtExplicit: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Origin.Relocated {
		t.Error("explicit --at over the title block was relocated")
	}
	if len(plan.Warnings) == 0 {
		t.Error("explicit --at over the title block produced no warning")
	}
}

// ── reconcile (issue #135) ──────────────────────────────────────────────────

func reconTestPlan() bapPlan {
	return bapPlan{
		Nets: []bapNet{
			{Net: "VSYS", Members: []string{"R4:2", "U1:VIN-", "U1:VBUS"}},
			{Net: "INA_ALERT", Members: []string{"R1:1", "U1:Alert"}},
		},
	}
}

func reconTestPins() map[string]map[string][]string {
	return map[string]map[string][]string{
		"R4": {"1": {"1"}, "2": {"2"}},
		"R1": {"1": {"1"}, "2": {"2"}},
		"U1": {"3": {"3"}, "ALERT": {"3"}, "8": {"8"}, "VBUS": {"8"}, "9": {"9"}, "VIN-": {"9"},
			// GND on two physical pins — what a `GND*` member must fan out to.
			"10": {"10"}, "11": {"11"}, "GND": {"10", "11"}},
	}
}

// TestReconcileBlockNets_Match: live nets exactly hold the planned members (plus
// host extras, which must NOT fail the diff) → no diffs.
func TestReconcileBlockNets_Match(t *testing.T) {
	live := map[string]map[string]bool{
		"VSYS":      {"R4.2": true, "U1.9": true, "U1.8": true, "C9.1": true}, // C9.1 = host extra, allowed
		"INA_ALERT": {"R1.1": true, "U1.3": true},
	}
	diffs := reconcileBlockNets(reconTestPlan(), live, reconTestPins())
	if len(diffs) != 0 {
		t.Fatalf("expected no diffs, got %+v", diffs)
	}
}

// TestReconcileBlockNets_MergedShort reproduces the #135 incident shape: U1.3
// (Alert) physically merged into VSYS, INA_ALERT lost it. The diff must name the
// missing pin AND point at the net it landed in.
func TestReconcileBlockNets_MergedShort(t *testing.T) {
	live := map[string]map[string]bool{
		"VSYS":      {"R4.2": true, "U1.9": true, "U1.8": true, "U1.3": true},
		"INA_ALERT": {"R1.1": true},
	}
	diffs := reconcileBlockNets(reconTestPlan(), live, reconTestPins())
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %+v", diffs)
	}
	d := diffs[0]
	if d.Net != "INA_ALERT" {
		t.Fatalf("expected diff on INA_ALERT, got %s", d.Net)
	}
	if len(d.Missing) != 1 || d.Missing[0] != "U1.3" {
		t.Fatalf("expected missing [U1.3], got %v", d.Missing)
	}
	if d.FoundIn["U1.3"] != "VSYS" {
		t.Fatalf("expected U1.3 foundIn VSYS, got %v", d.FoundIn)
	}
}

// TestReconcileBlockNets_UnresolvedPinRef: a member whose pin ref can't resolve
// to a number must surface as missing, never silently pass.
func TestReconcileBlockNets_UnresolvedPinRef(t *testing.T) {
	plan := bapPlan{Nets: []bapNet{{Net: "X", Members: []string{"U9:NOPE"}}}}
	diffs := reconcileBlockNets(plan, map[string]map[string]bool{"X": {}}, reconTestPins())
	if len(diffs) != 1 || len(diffs[0].Missing) != 1 {
		t.Fatalf("expected the unresolved ref to be reported missing, got %+v", diffs)
	}
}

// ── designator renumbering (issue #144) ─────────────────────────────────────

// remapTestPlan is the shape block-apply produces for a two-part instance whose
// internal net is instance-scoped and whose second net is bound to a host rail.
func remapTestPlan() *bapPlan {
	return &bapPlan{
		Instance: "C1",
		Placements: []bapPlacement{
			{Role: "C_V3", Designator: "C1"},
			{Role: "U", Designator: "U1"},
			{Role: "D_ESD", Designator: "D1"},
		},
		Nets: []bapNet{
			{Net: "C1_N3", Members: []string{"U1:V3", "C1:1"}},
			{Net: "5V", Port: "VBUS_5V", Bound: true, Members: []string{"U1:VCC", "D1:3"}},
		},
	}
}

func TestBapRemapDesignators(t *testing.T) {
	plan := remapTestPlan()
	// D1 was NOT renumbered — the platform only dodges what it knows is taken.
	bapRemapDesignators(plan, map[string]string{"C1": "C11", "U1": "U3"})

	if got := []string{plan.Placements[0].Designator, plan.Placements[1].Designator, plan.Placements[2].Designator}; got[0] != "C11" || got[1] != "U3" || got[2] != "D1" {
		t.Fatalf("placements not remapped: %v", got)
	}
	// The instance id follows the first placement, and the instance-scoped net
	// name follows it — otherwise this instance's C1_N3 merges with the C1_N3 of
	// the OTHER page's C1 instance (the netlist is designator-keyed document-wide).
	if plan.Instance != "C11" {
		t.Fatalf("instance = %q, want C11", plan.Instance)
	}
	if plan.Nets[0].Net != "C11_N3" {
		t.Fatalf("internal net = %q, want C11_N3", plan.Nets[0].Net)
	}
	if got := plan.Nets[0].Members; got[0] != "U3:V3" || got[1] != "C11:1" {
		t.Fatalf("members not remapped: %v", got)
	}
	// A port-bound net carries a HOST net name: renaming it would silently move
	// the block onto a different rail.
	if plan.Nets[1].Net != "5V" {
		t.Fatalf("bound net was rewritten to %q", plan.Nets[1].Net)
	}
	if got := plan.Nets[1].Members; got[0] != "U3:VCC" || got[1] != "D1:3" {
		t.Fatalf("bound-net members not remapped: %v", got)
	}
}

func TestBapRemapDesignatorsNoop(t *testing.T) {
	plan := remapTestPlan()
	bapRemapDesignators(plan, nil)
	if plan.Instance != "C1" || plan.Nets[0].Net != "C1_N3" || plan.Nets[0].Members[0] != "U1:V3" {
		t.Fatalf("empty rename set mutated the plan: %+v", plan)
	}
}

// An explicit --instance is not a designator, so no rename may touch it (and the
// nets named after it must stay put).
func TestBapRemapDesignatorsExplicitInstance(t *testing.T) {
	plan := remapTestPlan()
	plan.Instance = "usb1"
	plan.Nets[0].Net = "USB1_N3"
	bapRemapDesignators(plan, map[string]string{"C1": "C11", "U1": "U3"})
	if plan.Instance != "usb1" {
		t.Fatalf("explicit instance was renamed to %q", plan.Instance)
	}
	if plan.Nets[0].Net != "USB1_N3" {
		t.Fatalf("explicit-instance net renamed to %q", plan.Nets[0].Net)
	}
	if plan.Nets[0].Members[1] != "C11:1" {
		t.Fatalf("members should still remap: %v", plan.Nets[0].Members)
	}
}

func TestBapPlacedDesignator(t *testing.T) {
	cases := []struct {
		name string
		res  *actionResult
		want string
	}{
		{"nil", nil, ""},
		{"no component", &actionResult{Result: map[string]any{}}, ""},
		{"present", &actionResult{Result: map[string]any{
			"component": map[string]any{"designator": " C11 "}}}, "C11"},
		// An older connector that omits the field must degrade to "keep the
		// planned name", never to "clear the designator".
		{"absent field", &actionResult{Result: map[string]any{
			"component": map[string]any{}}}, ""},
	}
	for _, tc := range cases {
		if got := bapPlacedDesignator(tc.res); got != tc.want {
			t.Fatalf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A `NAME*` member must reconcile against EVERY pin sharing that name: landing on
// only half the ground pins is precisely the silent half-connection the fan-out
// syntax exists to prevent, so a partial match must still be reported missing.
func TestReconcileBlockNets_FanoutMember(t *testing.T) {
	plan := bapPlan{Nets: []bapNet{{Net: "GND", Members: []string{"U1:GND*"}}}}

	full := map[string]map[string]bool{"GND": {"U1.10": true, "U1.11": true}}
	if d := reconcileBlockNets(plan, full, reconTestPins()); len(d) != 0 {
		t.Fatalf("both ground pins present, want no diff, got %+v", d)
	}

	half := map[string]map[string]bool{"GND": {"U1.10": true}}
	d := reconcileBlockNets(plan, half, reconTestPins())
	if len(d) != 1 || len(d[0].Missing) != 1 || d[0].Missing[0] != "U1.11" {
		t.Fatalf("half-connected fan-out must report U1.11 missing, got %+v", d)
	}
}

func TestBapPinKeysFanout(t *testing.T) {
	pins := reconTestPins()
	got, ok := bapPinKeys("U1:GND*", pins)
	if !ok || len(got) != 2 || got[0] != "U1.10" || got[1] != "U1.11" {
		t.Fatalf("fan-out keys = %v (ok=%v), want [U1.10 U1.11]", got, ok)
	}
	// A plain ref still resolves to exactly one key.
	if got, ok := bapPinKeys("U1:VBUS", pins); !ok || len(got) != 1 || got[0] != "U1.8" {
		t.Fatalf("plain ref = %v (ok=%v), want [U1.8]", got, ok)
	}
	if _, ok := bapPinKeys("U1:NOSUCH", pins); ok {
		t.Fatal("unknown pin must not resolve")
	}
}
