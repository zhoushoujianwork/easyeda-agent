package app

import (
	"strings"
	"testing"
	"time"
)

func ts(sec int) time.Time { return time.Date(2026, 7, 3, 12, 0, sec, 0, time.UTC) }

func TestExportPlaybookWiresCaptures(t *testing.T) {
	rows := []auditRow{
		// read → filtered out
		{Ts: ts(1), Action: "schematic.components.list", OK: true},
		// place → produces primitiveId
		{Ts: ts(2), Action: "schematic.component.place", OK: true,
			Payload: map[string]any{"libraryUuid": "lib", "uuid": "dev", "x": 10.0},
			Result:  map[string]any{"primitiveId": "abcdef0123456789"}},
		// modify references that id → must become ${ID..} + capture on producer
		{Ts: ts(3), Action: "schematic.component.modify", OK: true,
			Payload: map[string]any{"primitiveId": "abcdef0123456789",
				"patch": map[string]any{"designator": "U1"}}},
		// failed action → filtered out
		{Ts: ts(4), Action: "schematic.wire.create", OK: false,
			Payload: map[string]any{"net": "X"}},
		// modify referencing an id born OUTSIDE the window → raw flag
		{Ts: ts(5), Action: "pcb.component.modify", OK: true,
			Payload: map[string]any{"primitiveId": "1234567890abcdef",
				"patch": map[string]any{"x": 1.0}}},
		// save storm → squashed to one
		{Ts: ts(6), Action: "schematic.save", OK: true},
		{Ts: ts(7), Action: "schematic.save", OK: true},
		{Ts: ts(8), Action: "schematic.save", OK: true},
	}
	pb, stats := exportPlaybook(rows, exportOptions{to: time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), squashSaves: true})

	if len(pb.Steps) != 4 { // place, modify, pcb.modify, ONE save
		t.Fatalf("want 4 steps, got %d: %+v", len(pb.Steps), pb.Steps)
	}
	// capture wired on the producer
	place := pb.Steps[0]
	if len(place.Capture) != 1 {
		t.Fatalf("producer capture missing: %+v", place)
	}
	var varName string
	for v, path := range place.Capture {
		varName = v
		if path != "$.primitiveId" {
			t.Fatalf("capture path wrong: %s", path)
		}
	}
	mod := pb.Steps[1]
	if mod.Payload["primitiveId"] != "${"+varName+"}" {
		t.Fatalf("id not rewritten: %v", mod.Payload["primitiveId"])
	}
	if stats.captures != 1 {
		t.Fatalf("stats.captures=%d", stats.captures)
	}
	// outside-window raw id flagged
	raw := pb.Steps[2]
	if !strings.Contains(raw.Name, "raw-id") || stats.rawIDs != 1 {
		t.Fatalf("raw id not flagged: %+v stats=%+v", raw, stats)
	}
	// save squashed + checkpoint
	save := pb.Steps[3]
	if save.Action != "schematic.save" || !save.Checkpoint {
		t.Fatalf("save step wrong: %+v", save)
	}
	// exported playbook must survive preflight
	pb.Meta.Name = "t"
	pb.Version = 1
	if errs := preflight(pb, map[string]string{}); len(errs) != 0 {
		t.Fatalf("exported playbook fails preflight: %v", errs)
	}
}

func TestExportFiltersWindowAndTime(t *testing.T) {
	rows := []auditRow{
		{Ts: ts(1), WindowID: "w1", Action: "pcb.save", OK: true},
		{Ts: ts(5), WindowID: "w2", Action: "pcb.save", OK: true},
		{Ts: ts(9), WindowID: "w1", Action: "pcb.save", OK: true},
	}
	pb, _ := exportPlaybook(rows, exportOptions{
		window: "w1", from: ts(4), to: ts(20), squashSaves: false,
	})
	if len(pb.Steps) != 1 {
		t.Fatalf("window/time filter wrong: %d steps", len(pb.Steps))
	}
}

func TestLooksLikePrimitiveID(t *testing.T) {
	cases := map[string]bool{
		"be7fdb3c8c24fe36": true,  // 16-hex with letters
		"gge4":             true,  // unique id
		"1234567890123456": false, // digits only — could be a number
		"GND":              false,
		"schematic.save":   false,
		"0819f05c4eef4c71ace90d822a990e87": false, // 32-hex library uuid ≠ primitive id
	}
	for s, want := range cases {
		if got := looksLikePrimitiveID(s); got != want {
			t.Errorf("%q: got %v want %v", s, got, want)
		}
	}
}
