package app

import "testing"

func TestResolveDoc(t *testing.T) {
	docs := []openableDoc{
		{UUID: "u-p1a", Type: "schematic", Name: "P1"},
		{UUID: "u-p1b", Type: "schematic", Name: "P1"}, // duplicate name across schematics
		{UUID: "u-p2", Type: "schematic", Name: "P2"},
		{UUID: "u-pcb", Type: "pcb", Name: "PCB1"},
	}

	// exact uuid wins even when a name is ambiguous
	if d, err := resolveDoc(docs, "u-p1b"); err != nil || d.UUID != "u-p1b" {
		t.Fatalf("uuid match: got %+v err=%v", d, err)
	}
	// unique name, case-insensitive
	if d, err := resolveDoc(docs, "pcb1"); err != nil || d.UUID != "u-pcb" {
		t.Fatalf("name match: got %+v err=%v", d, err)
	}
	// ambiguous name → error
	if _, err := resolveDoc(docs, "P1"); err == nil {
		t.Fatal("expected ambiguity error for P1")
	}
	// no match → error
	if _, err := resolveDoc(docs, "nope"); err == nil {
		t.Fatal("expected not-found error")
	}
}
