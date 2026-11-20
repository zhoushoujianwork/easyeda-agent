package app

import (
	"strings"
	"testing"
)

// A whole-page rebuild frees its members from stale groups (live: H1 stuck in
// g1 "indicator" after the modules were renamed); block groups still refuse.
func TestGroupsReleaseOverlapping(t *testing.T) {
	groups := []*schGroup{
		{ID: "g1", Name: "indicator", Members: []string{"H1", "R10", "U4"}},
		{ID: "g2", Name: "existing", Members: []string{"R101", "R102", "C9"}},
		{ID: "g3", Name: "mod-led", Members: []string{"H1", "R10", "U4"}},
		{ID: "g4", Name: "other", Members: []string{"Q1"}},
	}
	out, released, err := groupsReleaseOverlapping(groups, "mod-led", []string{"H1", "R10", "U4"}, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 || out[0].ID != "g2" || out[1].ID != "g3" || out[2].ID != "g4" || len(released) != 1 || !strings.Contains(released[0], `g1 "indicator"`) {
		t.Fatalf("emptied stale group must go, exact match and unrelated stay: out=%v released=%v", out, released)
	}
	out, released, err = groupsReleaseOverlapping(groups, "mod-resistors", []string{"R101", "R102"}, "", "", nil)
	if err != nil || len(released) != 1 {
		t.Fatalf("partial release: %v %v", released, err)
	}
	for _, g := range out {
		if g.ID == "g2" && (len(g.Members) != 1 || g.Members[0] != "C9") {
			t.Fatalf("g2 should keep only C9: %v", g.Members)
		}
	}
	if groups[1].Members[0] != "R101" {
		t.Fatal("input groups were mutated")
	}
	if next, _, unchanged, err := groupsCreateWithProvenance(out, "mod-resistors", []string{"R101", "R102"}, "", "", nil, true); err != nil || unchanged || len(next) != len(out)+1 {
		t.Fatalf("create after release failed: %v", err)
	}
	block := []*schGroup{{ID: "g1", Name: "buck", Members: []string{"U3", "C11"}, BlockID: "block.sy8089_buck"}}
	if _, _, err := groupsReleaseOverlapping(block, "mod-pwr", []string{"U3"}, "", "", nil); err == nil || !strings.Contains(err.Error(), "block group") {
		t.Fatalf("block provenance must not be replaced silently: %v", err)
	}
}
