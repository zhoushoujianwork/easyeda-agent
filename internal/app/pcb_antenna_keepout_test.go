package app

import (
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/internal/blocks"
)

// TestAntennaKeepoutRect: the keep-out must land on the module's PAD-FREE end and
// CLEAR the pad bodies (padClear pulls the pad-facing edge back from the outermost
// pad CENTER — pad extents aren't exposed). Modelled on ceshi's U1 WROOM: bbox
// y[-129,900], pad centers up to y=600 → with padClear=40 the strip starts at ~640.
func TestAntennaKeepoutRect(t *testing.T) {
	pads := [][2]float64{{0, -100}, {300, 0}, {-300, 300}, {300, 600}, {-300, 600}}
	x0, y0, x1, y1, ok := antennaKeepoutRect(-374, -129, 374, 900, pads, 0.3, 20, 40)
	if !ok {
		t.Fatal("expected a keep-out rect for the WROOM top strip")
	}
	// Must clear the pads: start padClear ABOVE the outermost pad center (600), not
	// on it (that would cover the pads' antenna-facing half).
	if y0 < 600+40-0.1 {
		t.Errorf("keep-out must clear pad bodies (start ~640, padClear above center 600); got y0=%.1f", y0)
	}
	if y1 < 900 {
		t.Errorf("keep-out must cover the module top (900); got y1=%.1f", y1)
	}
	// Spans the module width (+margin).
	if x0 > -374 || x1 < 374 {
		t.Errorf("keep-out should span the module width; got x[%.1f,%.1f]", x0, x1)
	}

	// endFrac smaller than the strip shrinks the keep-out (block drives depth).
	_, sy0, _, _, ok2 := antennaKeepoutRect(-374, -129, 374, 900, pads, 0.1, 20, 40)
	if !ok2 || sy0 <= y0 {
		t.Errorf("a smaller end_frac must shrink the keep-out (higher y0); got %.1f vs %.1f", sy0, y0)
	}

	// No pads → no derivable antenna strip → ok=false (never guess).
	if _, _, _, _, ok3 := antennaKeepoutRect(0, 0, 100, 300, nil, 0, 0, 40); ok3 {
		t.Error("no pads → antennaKeepoutRect must return ok=false")
	}
}

// TestAntennaKeepoutFrac: depth fraction is resolved from block data by device match.
func TestAntennaKeepoutFrac(t *testing.T) {
	ks := []blocks.AntennaKeepout{{Match: "wroom", EndFrac: 0.3}}
	if f := antennaKeepoutFrac(ks, "ESP32-S3-WROOM-1"); f != 0.3 {
		t.Errorf("WROOM device should resolve end_frac 0.3; got %v", f)
	}
	if f := antennaKeepoutFrac(ks, "CH340C"); f != 0 {
		t.Errorf("non-antenna device should resolve 0; got %v", f)
	}
}

// TestWroomBlockDeclaresKeepout guards that the shipped WROOM block actually
// carries the machine-readable keep-out the generator consumes (approach A).
func TestWroomBlockDeclaresKeepout(t *testing.T) {
	ks, err := blocks.LoadAntennaKeepouts()
	if err != nil {
		t.Fatalf("LoadAntennaKeepouts: %v", err)
	}
	found := false
	for _, k := range ks {
		if k.Match == "wroom" {
			found = true
			if k.EndFrac <= 0 {
				t.Errorf("wroom keepout must have a positive end_frac; got %v", k.EndFrac)
			}
		}
	}
	if !found {
		t.Error("the WROOM block must declare a keepout matching \"wroom\"")
	}
}

// TestChipAntennaKeepout is #123: a two-pad ceramic SMD antenna (whole footprint
// = radiator, no pad-free strip) gets a bbox+margin keep-out; a module-sized
// bbox stays with the strip heuristic.
func TestChipAntennaKeepout(t *testing.T) {
	// Johanson 2450AT18A100E: 3.2×1.6mm ≈ 126×63 mil.
	if !isChipAntennaSize(126, 63) {
		t.Error("126×63mil ceramic antenna must qualify as chip-antenna sized")
	}
	if isChipAntennaSize(700, 710) {
		t.Error("a WROOM-sized bbox must NOT qualify as a chip antenna")
	}
	x0, y0, x1, y1, ok := chipAntennaKeepoutRect(100, 200, 226, 263, 120)
	if !ok {
		t.Fatal("chip keep-out must be produced for a valid bbox")
	}
	if x0 != -20 || y0 != 80 || x1 != 346 || y1 != 383 {
		t.Errorf("chip keep-out = (%g,%g)-(%g,%g), want bbox+120 each side", x0, y0, x1, y1)
	}
	if _, _, _, _, ok := chipAntennaKeepoutRect(100, 200, 100, 263, 120); ok {
		t.Error("degenerate bbox must not produce a keep-out")
	}
}
