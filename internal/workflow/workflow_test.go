package workflow

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestGlobalDirAndRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvDir, dir)

	st, err := Load("proj-a")
	if err != nil {
		t.Fatalf("fresh load: %v", err)
	}
	st.Confirm(StageOutlineConfirmed, "confirm", "note")
	if err := Save(st); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "proj-a.json")); err != nil {
		t.Fatalf("state must persist under the GLOBAL dir, not cwd: %v", err)
	}
	got, err := Load("proj-a")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !got.Has(StageOutlineConfirmed) {
		t.Fatal("confirmation must survive a reload")
	}
}

func TestLegacyCwdFallback(t *testing.T) {
	t.Setenv(EnvDir, t.TempDir())

	work := t.TempDir()
	cwd, _ := os.Getwd()
	if err := os.Chdir(work); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(cwd)

	// A pre-global state file in the old cwd-relative location.
	legacyDir := filepath.Join(".easyeda", "pcb-stage")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{"project":"old-proj","confirmed":{"outline_confirmed":true}}`
	if err := os.WriteFile(filepath.Join(legacyDir, "old-proj.json"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := Load("old-proj")
	if err != nil {
		t.Fatalf("legacy load: %v", err)
	}
	if !st.Has(StageOutlineConfirmed) {
		t.Fatal("legacy cwd-relative state must be readable as a fallback")
	}
	// A save migrates it to the global path.
	if err := Save(st); err != nil {
		t.Fatalf("save: %v", err)
	}
	if !Exists("old-proj") {
		t.Fatal("state must exist after migration")
	}
	if _, err := os.Stat(Path("old-proj")); err != nil {
		t.Fatalf("save must land at the global path: %v", err)
	}
}

func TestLoadAnyCandidates(t *testing.T) {
	t.Setenv(EnvDir, t.TempDir())

	st, _ := Load("by-name")
	st.Confirm(StagePreRoutePassed, "gate-pass", "")
	if err := Save(st); err != nil {
		t.Fatal(err)
	}

	// The uuid candidate has no file; the name candidate does — LoadAny finds it.
	got, err := LoadAny("uuid-without-file", "by-name")
	if err != nil {
		t.Fatalf("LoadAny: %v", err)
	}
	if !got.Has(StagePreRoutePassed) {
		t.Fatal("LoadAny must return the candidate that has a persisted state")
	}

	// No candidate exists → fresh state keyed by the first non-empty candidate.
	fresh, err := LoadAny("", "nope-1", "nope-2")
	if err != nil {
		t.Fatalf("LoadAny fresh: %v", err)
	}
	if fresh.Project != "nope-1" || len(fresh.Confirmed) != 0 {
		t.Fatalf("fresh LoadAny must key by the first candidate, got %+v", fresh)
	}
}

func TestInvalidateAllClearsEveryAlias(t *testing.T) {
	t.Setenv(EnvDir, t.TempDir())

	for _, key := range []string{"alias-name", "alias-uuid"} {
		st, _ := Load(key)
		st.Confirm(StagePlacementConfirmed, "confirm", "")
		st.Confirm(StagePreRoutePassed, "gate-pass", "")
		if err := Save(st); err != nil {
			t.Fatal(err)
		}
	}
	cleared := InvalidateAll([]string{"alias-name", "alias-uuid", "missing"}, StagePlacementConfirmed, "test")
	if len(cleared) == 0 {
		t.Fatal("InvalidateAll must report cleared stages")
	}
	for _, key := range []string{"alias-name", "alias-uuid"} {
		got, _ := Load(key)
		if got.Has(StagePlacementConfirmed) || got.Has(StagePreRoutePassed) {
			t.Fatalf("%s must be invalidated", key)
		}
	}
}

func TestInvalidateFromDropsFingerprints(t *testing.T) {
	st := &State{Project: "fp", Confirmed: map[Stage]bool{}}
	st.Confirm(StagePlacementConfirmed, "confirm", "")
	st.Confirm(StageOutlineConfirmed, "confirm", "")
	st.LayoutFP = NewFingerprint("aaa", 10)
	st.OutlineFP = NewFingerprint("bbb", 4)

	// Outline-only invalidation keeps the placement fingerprint.
	st.InvalidateFrom(StageOutlineConfirmed, "outline change")
	if st.OutlineFP != nil {
		t.Fatal("outline fingerprint must drop with outline_confirmed")
	}
	if st.LayoutFP == nil {
		t.Fatal("layout fingerprint must survive an outline-only invalidation")
	}

	st.InvalidateFrom(StagePlacementConfirmed, "move")
	if st.LayoutFP != nil {
		t.Fatal("layout fingerprint must drop with placement_confirmed")
	}
}

func TestHashLayoutDeterministicAndOrderIndependent(t *testing.T) {
	a := []ComponentPose{
		{Designator: "U1", X: 100, Y: 200, Rotation: 90, Layer: "top"},
		{Designator: "C1", X: 50.04, Y: 60, Rotation: 0, Layer: "top"},
	}
	b := []ComponentPose{a[1], a[0]} // same poses, different order
	if HashLayout(a) != HashLayout(b) {
		t.Fatal("layout hash must be order-independent")
	}
	moved := []ComponentPose{
		{Designator: "U1", X: 101, Y: 200, Rotation: 90, Layer: "top"},
		a[1],
	}
	if HashLayout(a) == HashLayout(moved) {
		t.Fatal("a moved part must change the hash")
	}
	// Float noise below the 0.1mil rounding grain must NOT change the hash.
	noisy := []ComponentPose{
		{Designator: "U1", X: 100.02, Y: 200.01, Rotation: 90, Layer: "top"},
		{Designator: "C1", X: 50.02, Y: 60.02, Rotation: 0, Layer: "top"},
	}
	if HashLayout(a) != HashLayout(noisy) {
		t.Fatal("float noise below the rounding grain must not read as drift")
	}
}

func TestCheckRouteGateForceDoesNotConfirm(t *testing.T) {
	// #132: on a zero-confirmation board only --force-unsafe passes; use it here
	// so this test keeps pinning its own claim (per-run authorization only).
	st := &State{Project: "f", Confirmed: map[Stage]bool{}}
	g := CheckRouteGate(st, true, true, "why")
	if !g.Allowed || !g.Forced {
		t.Fatalf("force-unsafe must allow (forced), got %+v", g)
	}
	if st.Has(StageRoutingAuthorized) || st.Has(StageOutlineConfirmed) {
		t.Fatal("force must not confirm any stage — per-run authorization only")
	}
	// Un-forced call right after: still blocked.
	if CheckRouteGate(st, false, false, "").Allowed {
		t.Fatal("gate must block again after a forced run")
	}
	// Plain --force on the same zero-confirmation board: refused (#132 plan 1).
	if CheckRouteGate(st, true, false, "why").Allowed {
		t.Fatal("plain force must be refused while the mechanical skeleton is unconfirmed")
	}
}

// Issue #100: representation noise from a doc reload — float tails, -0.0,
// rotation aliasing, layer number-vs-name — must NOT change the hash; only a
// real geometric change may.
func TestHashLayoutNormalization(t *testing.T) {
	base := []ComponentPose{
		{Designator: "U1", X: 1470, Y: 700, Rotation: 0, Layer: "1"},
		{Designator: "C1", X: 100, Y: 200, Rotation: 90, Layer: "2"},
	}
	h0 := HashLayout(base)

	// Float tails (sub-mil), -0.0 rotation, 360≡0, layer name aliases.
	noisy := []ComponentPose{
		{Designator: "U1", X: 1470.0001, Y: 699.9999, Rotation: math.Copysign(0, -1), Layer: "Top Layer"},
		{Designator: "C1", X: 100, Y: 200, Rotation: 450, Layer: "Bottom"},
	}
	if HashLayout(noisy) != h0 {
		t.Fatal("representation noise must not change the layout hash (issue #100)")
	}
	// Negative rotation alias: -90 ≡ 270 ≠ 90 — this IS a change.
	rotated := []ComponentPose{
		{Designator: "U1", X: 1470, Y: 700, Rotation: 0, Layer: "1"},
		{Designator: "C1", X: 100, Y: 200, Rotation: -90, Layer: "2"},
	}
	if HashLayout(rotated) == h0 {
		t.Fatal("a real rotation change (90 → -90/270) must change the hash")
	}
	// A real move (≥1 mil) must change the hash.
	moved := []ComponentPose{
		{Designator: "U1", X: 1472, Y: 700, Rotation: 0, Layer: "1"},
		{Designator: "C1", X: 100, Y: 200, Rotation: 90, Layer: "2"},
	}
	if HashLayout(moved) == h0 {
		t.Fatal("a 2 mil move must change the hash")
	}
	// A layer flip must change the hash (the old asString bug hid it as "").
	flipped := []ComponentPose{
		{Designator: "U1", X: 1470, Y: 700, Rotation: 0, Layer: "2"},
		{Designator: "C1", X: 100, Y: 200, Rotation: 90, Layer: "2"},
	}
	if HashLayout(flipped) == h0 {
		t.Fatal("a TOP→BOTTOM flip must change the hash")
	}
}

// post_route_checked joins the order after routing_authorized and its snapshot
// clears on invalidation from any stage at or before it.
func TestPostRouteCheckedStage(t *testing.T) {
	if Rank(StagePostRouteChecked) != len(Order)-1 {
		t.Fatalf("post_route_checked must be the last stage, rank=%d", Rank(StagePostRouteChecked))
	}
	if Rank(StagePostRouteChecked) <= Rank(StageRoutingAuthorized) {
		t.Fatal("post_route_checked must rank after routing_authorized")
	}
	st := &State{Project: "t"}
	st.Confirm(StagePostRouteChecked, "gate-pass", "test")
	st.Check = &CheckGateSummary{Tracks: 42, At: "now"}
	// Invalidating an EARLIER stage clears the later stage + its snapshot.
	st.InvalidateFrom(StageRoutingAuthorized, "copper changed")
	if st.Has(StagePostRouteChecked) || st.Check != nil {
		t.Fatal("invalidating routing_authorized must clear post_route_checked + Check snapshot")
	}
	// A legacy state file without the new stage loads as unconfirmed.
	if (&State{Project: "old", Confirmed: map[Stage]bool{StageRoutingAuthorized: true}}).Has(StagePostRouteChecked) {
		t.Fatal("legacy state must not have post_route_checked")
	}
}

// TestPowerTracksNetsRoundTrip: power-planes' routeAsTracks verdict must survive
// a save/load cycle — the gate reads it from disk in a LATER process (issue #114).
func TestPowerTracksNetsRoundTrip(t *testing.T) {
	t.Setenv(EnvDir, t.TempDir())

	st, err := Load("proj-pt")
	if err != nil {
		t.Fatalf("fresh load: %v", err)
	}
	st.SetPowerTracksNets([]string{"VDD_SPI", " 5V ", "VDD_SPI", ""})
	if got, want := st.PowerTracksNets, []string{"5V", "VDD_SPI"}; len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("SetPowerTracksNets must trim/dedup/sort: got %v, want %v", got, want)
	}
	if err := Save(st); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load("proj-pt")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !got.IsPowerTracksNet("VDD_SPI") || !got.IsPowerTracksNet("vdd_spi") || !got.IsPowerTracksNet("5V") {
		t.Fatalf("recorded nets must survive the reload: %v", got.PowerTracksNets)
	}
	if got.IsPowerTracksNet("3V3") || got.IsPowerTracksNet("") {
		t.Fatal("unrecorded nets must not be exempt")
	}

	// Empty verdict clears the record (a later run that poured everything).
	got.SetPowerTracksNets(nil)
	if got.PowerTracksNets != nil {
		t.Fatalf("empty verdict must clear the record, got %v", got.PowerTracksNets)
	}
	if err := Save(got); err != nil {
		t.Fatalf("save cleared: %v", err)
	}
	reloaded, err := Load("proj-pt")
	if err != nil {
		t.Fatalf("reload cleared: %v", err)
	}
	if reloaded.IsPowerTracksNet("VDD_SPI") {
		t.Fatal("a cleared record must not resurrect after a reload")
	}
}

// TestPowerTracksNetsInvalidationSemantics pins the #114 lifetime decision:
// the verdict dies with the PLACEMENT, not with the routing.
func TestPowerTracksNetsInvalidationSemantics(t *testing.T) {
	cases := []struct {
		name string
		from Stage
		want bool // still recorded afterwards?
	}{
		{"routing-class invalidation keeps it", StagePostRouteChecked, true},
		{"routing_authorized keeps it", StageRoutingAuthorized, true},
		{"pre_route_passed keeps it", StagePreRoutePassed, true},
		{"outline change keeps it", StageOutlineConfirmed, true},
		{"placement_confirmed drops it", StagePlacementConfirmed, false},
		{"placement_ready drops it", StagePlacementReady, false},
		{"imported drops it", StageImported, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := &State{Project: "p", Confirmed: map[Stage]bool{}}
			for _, s := range Order {
				st.Confirm(s, "confirm", "")
			}
			st.SetPowerTracksNets([]string{"VDD_SPI"})
			st.InvalidateFrom(tc.from, "test")
			if got := st.IsPowerTracksNet("VDD_SPI"); got != tc.want {
				t.Fatalf("InvalidateFrom(%s): recorded=%v, want %v", tc.from, got, tc.want)
			}
		})
	}
}

// TestPlanePouredNetsRoundTrip is #117's state half: power-planes' "poured into
// an inner PLANE" record must survive save/load (the gate reads it in a later
// process) and follow the same placement-lifetime as PowerTracksNets — a
// routing edit keeps it, a placement-class invalidation drops it.
func TestPlanePouredNetsRoundTrip(t *testing.T) {
	t.Setenv(EnvDir, t.TempDir())

	st, err := Load("proj-pp")
	if err != nil {
		t.Fatalf("fresh load: %v", err)
	}
	st.SetPlanePouredNets([]string{" GND ", "GND", ""})
	if got := st.PlanePouredNets; len(got) != 1 || got[0] != "GND" {
		t.Fatalf("SetPlanePouredNets must trim/dedup: got %v", got)
	}
	if err := Save(st); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load("proj-pp")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !got.IsPlanePouredNet("GND") || !got.IsPlanePouredNet("gnd") {
		t.Fatalf("recorded plane net must survive the reload: %v", got.PlanePouredNets)
	}
	if got.IsPlanePouredNet("3V3") {
		t.Fatal("unrecorded net must not be exempt")
	}
	got.SetPlanePouredNets(nil)
	if got.PlanePouredNets != nil {
		t.Fatalf("empty verdict must clear the record, got %v", got.PlanePouredNets)
	}
}

func TestPlanePouredNetsInvalidationSemantics(t *testing.T) {
	cases := []struct {
		name string
		from Stage
		want bool
	}{
		{"routing-class invalidation keeps it", StagePostRouteChecked, true},
		{"placement_confirmed drops it", StagePlacementConfirmed, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := &State{Project: "p", Confirmed: map[Stage]bool{}}
			for _, s := range Order {
				st.Confirm(s, "confirm", "")
			}
			st.SetPlanePouredNets([]string{"GND"})
			st.InvalidateFrom(tc.from, "test")
			if got := st.IsPlanePouredNet("GND"); got != tc.want {
				t.Fatalf("InvalidateFrom(%s): recorded=%v, want %v", tc.from, got, tc.want)
			}
		})
	}
}
