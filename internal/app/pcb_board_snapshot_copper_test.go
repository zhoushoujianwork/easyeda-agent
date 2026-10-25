package app

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestBoardSnapshotSemanticHashIgnoresCaptureMetadataOnly(t *testing.T) {
	s := lpSnapshot(lpComp("u1", "U1", 100, 100, 0, lpBBox(90, 90, 110, 110)))
	s.CapturedAt = "2026-09-22T01:00:00Z"
	s.SemanticSHA256 = "old-self-hash"
	h1, err := boardSnapshotSemanticSHA256(s)
	if err != nil {
		t.Fatal(err)
	}
	s.CapturedAt = "2026-09-22T02:00:00Z"
	s.Project = "renamed-capture-label"
	s.SemanticSHA256 = "another-self-hash"
	h2, err := boardSnapshotSemanticSHA256(s)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatalf("capture metadata changed semantic hash: %s != %s", h1, h2)
	}
	s.Components[0].X++
	h3, err := boardSnapshotSemanticSHA256(s)
	if err != nil {
		t.Fatal(err)
	}
	if h3 == h2 {
		t.Fatal("component geometry/identity change did not change semantic hash")
	}
}

func TestBoardSnapshotSemanticHashSurvivesDumpRoundTripWithProjectLabel(t *testing.T) {
	s := lpSnapshot(lpComp("u1", "U1", 100, 100, 0, lpBBox(90, 90, 110, 110)))
	h, err := boardSnapshotSemanticSHA256(s)
	if err != nil {
		t.Fatal(err)
	}
	// cmd_pcb_dump attaches this provenance label after the geometry fetch.
	s.Project = "ceshi"
	s.SemanticSHA256 = h
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := loadBoardSnapshotFile(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	got, err := boardSnapshotSemanticSHA256(loaded)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SemanticSHA256 != got {
		t.Fatalf("dump invalidated its own semantic hash: stored=%s recomputed=%s", loaded.SemanticSHA256, got)
	}
}

func TestCopperSnapshotAvailabilityDistinguishesKnownEmptyFromUnknown(t *testing.T) {
	knownEmpty := &boardSnapshot{Copper: &boardCopperSnapshot{
		Availability: map[string]string{"routing": "available", "vias": "available", "pours": "available", "poured": "available", "regions": "available", "fills": "available"},
		Lines:        []any{}, Arcs: []any{}, Vias: []any{}, Pours: []any{}, Poured: []any{}, Regions: []any{}, Fills: []any{},
	}}
	available := true
	knownEmpty.Copper.ArcsAvailable = &available
	if got := knownEmpty.Copper.Availability["poured"]; got != "available" || knownEmpty.Copper.Poured == nil {
		t.Fatalf("known-empty materialized copper lost: availability=%q list=%#v", got, knownEmpty.Copper.Poured)
	}
	unknown := &boardSnapshot{Copper: &boardCopperSnapshot{Availability: map[string]string{"poured": "unknown"}}}
	if unknown.Copper.Availability["poured"] != "unknown" {
		t.Fatalf("unknown availability=%q", unknown.Copper.Availability["poured"])
	}
}

func TestBoardSnapshotPreservesExactPadShapeAndSpecialPad(t *testing.T) {
	shape := []any{"RECT", 20.0, 12.0, 2.0}
	special := []any{"L", 1.0, 2.0}
	components := parseBoardComponents(componentsResult(map[string]any{
		"primitiveId": "u1", "designator": "U1", "layer": 1.0,
		"pads": []any{map[string]any{
			"primitiveId": "p1", "padNumber": "1", "net": "GND", "layer": 1.0,
			"x": 10.0, "y": 20.0, "width": 20.0, "height": 12.0,
			"shape": shape, "specialPad": special,
		}},
	}))
	if len(components) != 1 || len(components[0].Pads) != 1 {
		t.Fatalf("components=%+v", components)
	}
	pad := components[0].Pads[0]
	if canonicalJSON(pad.Shape) != canonicalJSON(shape) || canonicalJSON(pad.SpecialPad) != canonicalJSON(special) {
		t.Fatalf("pad geometry was dropped: shape=%#v special=%#v", pad.Shape, pad.SpecialPad)
	}
}

func TestBoardSnapshotSemanticHashIgnoresFloatJitter(t *testing.T) {
	// Live 2026-10-13: save → reload returned the same board with getter noise
	// (-200.30700000000001 vs -200.307) and the semantic hash changed anyway.
	a := lpSnapshot(lpComp("c1", "C1", -200.30700000000001, -761.4249999999998, 0, lpBBox(-200.3070000000001, -843.5749999999998, -39.69300000000004, -761.4)))
	b := lpSnapshot(lpComp("c1", "C1", -200.307, -761.425, 0, lpBBox(-200.307, -843.575, -39.692999999999984, -761.4)))
	ha, err := boardSnapshotSemanticSHA256(a)
	if err != nil {
		t.Fatal(err)
	}
	hb, err := boardSnapshotSemanticSHA256(b)
	if err != nil {
		t.Fatal(err)
	}
	if ha != hb {
		t.Fatalf("float jitter changed the semantic hash: %s != %s", ha, hb)
	}
	b.Components[0].X += 0.01
	if hc, _ := boardSnapshotSemanticSHA256(b); hc == hb {
		t.Fatal("a real 0.01 mil move must change the semantic hash")
	}
}

func TestBoardSnapshotSemanticHashIgnoresRecomputedPourFills(t *testing.T) {
	// Live 2026-10-24: an editor reload re-poured the same GND planes with new
	// fill ids and shifted polygons; the semantic hash changed with no edit.
	poured := func(fillID string, x float64) []any {
		return []any{map[string]any{"primitiveId": "p1", "pourPrimitiveId": "p1", "net": "GND", "layer": 1.0,
			"fills": []any{map[string]any{"id": fillID, "source": []any{"M", x, 0.0, "L", 10.0, 0.0}, "fill": true}}}}
	}
	snap := func(fills []any) *boardSnapshot {
		s := lpSnapshot(lpComp("c1", "C1", 0, 0, 0, lpBBox(0, 0, 10, 10)))
		s.Copper = &boardCopperSnapshot{Availability: map[string]string{"pours": "available", "poured": "available"},
			Pours:  []any{map[string]any{"primitiveId": "p1", "net": "GND", "layer": 1.0}},
			Poured: fills}
		return s
	}
	a, b := snap(poured("f1", 0)), snap(poured("f9", 3.25))
	ha, _ := boardSnapshotSemanticSHA256(a)
	hb, _ := boardSnapshotSemanticSHA256(b)
	if ha == "" || ha != hb {
		t.Fatalf("recomputed pour fills changed the semantic hash: %s != %s", ha, hb)
	}
	if len(b.Copper.Poured[0].(map[string]any)["fills"].([]any)) != 1 {
		t.Fatal("hashing must not strip fills from the snapshot itself")
	}
	if hc, _ := boardSnapshotSemanticSHA256(snap([]any{})); hc == ha {
		t.Fatal("an unpoured board must hash differently from a poured one")
	}
	b.Copper.Pours[0].(map[string]any)["net"] = "VCC"
	if hd, _ := boardSnapshotSemanticSHA256(b); hd == ha {
		t.Fatal("a pour definition change must change the semantic hash")
	}
}
