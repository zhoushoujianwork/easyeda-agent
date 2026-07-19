package app

import (
	"strings"
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/internal/workflow"
)

// ── issue #125: placement tier ladder ───────────────────────────────────────

func tierPoses() []stageComponentPose {
	return []stageComponentPose{
		{Designator: "H1", X: 0, Y: 0}, {Designator: "H2", X: 10, Y: 0},
		{Designator: "J1", X: 5, Y: 5, Rotation: 270},
		{Designator: "U1", X: 20, Y: 20},
		{Designator: "C1", X: 22, Y: 20}, {Designator: "R1", X: 24, Y: 20},
	}
}

// TestResolveTierParts covers the claim rules: explicit lists for tiers 1-3,
// tier-4 default = the unclaimed rest, conflicts and unknowns rejected.
func TestResolveTierParts(t *testing.T) {
	live := []string{"H1", "H2", "J1", "U1", "C1", "R1"}
	claimed := map[string]int{"H1": 1, "H2": 1, "J1": 2, "U1": 3}

	if _, err := resolveTierParts(3, nil, false, live, claimed); err == nil {
		t.Fatal("tier 3 without --parts must error (explicit list required)")
	}
	got, err := resolveTierParts(4, nil, false, live, claimed)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "C1,R1" {
		t.Fatalf("tier 4 default should claim the rest, got %v", got)
	}
	if _, err := resolveTierParts(2, []string{"NOPE"}, false, live, claimed); err == nil || !strings.Contains(err.Error(), "not on the board") {
		t.Fatalf("unknown designator must be rejected, got %v", err)
	}
	if _, err := resolveTierParts(3, []string{"J1"}, false, live, claimed); err == nil || !strings.Contains(err.Error(), "already claimed") {
		t.Fatalf("cross-tier claim must be rejected, got %v", err)
	}
	if got, err := resolveTierParts(3, nil, true, live, claimed); err != nil || got != nil {
		t.Fatalf("--empty must resolve to no parts, got %v %v", got, err)
	}
}

// TestTierDriftInvalidation: moving one tier's part invalidates that tier and
// everything after it — but NOT the earlier tiers (the point of the ladder).
func TestTierDriftInvalidation(t *testing.T) {
	st := &pcbStageState{Project: "t"}
	poses := tierPoses()
	confirm := func(n int, parts ...string) {
		hash, missing := tierPoseHash(poses, parts)
		if len(missing) > 0 {
			t.Fatalf("setup: missing %v", missing)
		}
		st.ConfirmTier(n, &stageTierConfirm{At: "t0", Designators: parts, Hash: hash})
	}
	confirm(1, "H1", "H2")
	confirm(2, "J1")
	confirm(3, "U1")
	confirm(4, "C1", "R1")
	st.Confirm(stagePlacementConfirmed, "confirm", "")

	if drift := verifyTierFingerprints(st, poses); len(drift) != 0 {
		t.Fatalf("no move → no drift, got %v", drift)
	}

	// Move a SATELLITE: only tier 4 (and the seal) may die.
	moved := tierPoses()
	moved[5].X = 999 // R1
	drift := verifyTierFingerprints(st, moved)
	if len(drift) != 1 || !strings.Contains(drift[0], "tier 4") {
		t.Fatalf("expected tier-4 drift only, got %v", drift)
	}
	if st.Tier(1) == nil || st.Tier(2) == nil || st.Tier(3) == nil {
		t.Fatal("earlier tiers must survive a satellite move")
	}
	if st.Tier(4) != nil || st.Has(stagePlacementConfirmed) {
		t.Fatal("tier 4 and the placement seal must be invalidated")
	}

	// Delete a tier-2 part: tiers 2..4 die, tier 1 survives.
	st2 := &pcbStageState{Project: "t2"}
	stFor := func() { // rebuild
		st2.PlacementTiers = nil
		hash, _ := tierPoseHash(poses, []string{"H1", "H2"})
		st2.ConfirmTier(1, &stageTierConfirm{At: "t0", Designators: []string{"H1", "H2"}, Hash: hash})
		hash, _ = tierPoseHash(poses, []string{"J1"})
		st2.ConfirmTier(2, &stageTierConfirm{At: "t0", Designators: []string{"J1"}, Hash: hash})
		hash, _ = tierPoseHash(poses, []string{"U1"})
		st2.ConfirmTier(3, &stageTierConfirm{At: "t0", Designators: []string{"U1"}, Hash: hash})
	}
	stFor()
	noJ1 := append([]stageComponentPose{}, poses[:2]...)
	noJ1 = append(noJ1, poses[3:]...)
	drift = verifyTierFingerprints(st2, noJ1)
	if len(drift) != 1 || !strings.Contains(drift[0], "tier 2") {
		t.Fatalf("expected tier-2 deletion drift, got %v", drift)
	}
	if st2.Tier(1) == nil {
		t.Fatal("tier 1 must survive")
	}
	if st2.Tier(2) != nil || st2.Tier(3) != nil {
		t.Fatal("tiers 2 and 3 must be invalidated")
	}
}

// TestTierLadderResetSemantics: reset back to placement_ready wipes the ladder;
// a placement_confirmed-level invalidation keeps it.
func TestTierLadderResetSemantics(t *testing.T) {
	st := &pcbStageState{Project: "t"}
	st.ConfirmTier(1, &stageTierConfirm{At: "t0", Empty: true})
	st.Confirm(stagePlacementConfirmed, "confirm", "")

	st.InvalidateFrom(stagePlacementConfirmed, "a move")
	if st.Tier(1) == nil {
		t.Fatal("placement_confirmed invalidation must KEEP tier sign-offs")
	}
	st.InvalidateFrom(stagePlacementReady, "reset --all")
	if st.Tier(1) != nil {
		t.Fatal("reset to placement_ready must wipe the tier ladder")
	}
}

// TestUnclaimedParts: a part added after the ladder closed must surface.
func TestUnclaimedParts(t *testing.T) {
	st := &pcbStageState{Project: "t"}
	st.ConfirmTier(1, &stageTierConfirm{Designators: []string{"H1"}})
	un := unclaimedParts([]string{"H1", "NEW1"}, st.ClaimedTiers())
	if len(un) != 1 || un[0] != "NEW1" {
		t.Fatalf("expected NEW1 unclaimed, got %v", un)
	}
	_ = workflow.PlacementTierCount
}
