package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func loadIssue273Fixture(t *testing.T, name string) libLayoutSource {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata/schematic-layout/2026-09-29-power-layout", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	input, err := decodeLibLayout(raw)
	if err != nil {
		t.Fatal(err)
	}
	return input
}

// Issue #273 root cause: the attachment route plus provisional nearby-rail
// joins of D3/R3 walled in U1.COMP (BUCK_COMP, direct) before R6 was placed.
// R6 then had no legal XY in the whole 400/200 raw window (6,480 rejected
// candidates at a 100k budget) and chronological rollback of unrelated R8/R7
// burned the frozen 3,000-candidate allowance. Provisional wires are now held
// to the same local direct-frontier ruler as bodies, so every peripheral of the
// frozen buck input reaches a placement checkpoint without a placement failure.
func TestIssue273BuckPlacementKeepsPendingDirectFrontier(t *testing.T) {
	input := loadIssue273Fixture(t, "input")
	start := time.Now()
	_, err := planLibLayout(input)
	if elapsed := time.Since(start); elapsed > 60*time.Second {
		t.Fatalf("bounded search took %s", elapsed)
	}
	var failure *SchematicLayoutSearchFailure
	if err == nil {
		return // A complete layout is the stronger outcome; validators already ran.
	}
	if !errors.As(err, &failure) {
		t.Fatalf("unexpected failure kind: %v", err)
	}
	if failure.Search.PlacementFailures != 0 || strings.Contains(err.Error(), "cmp-R6 (R6) placement") {
		t.Fatalf("R6 placement is still sealed by provisional wires: %v", err)
	}
	for _, module := range input.LayoutModules {
		for _, peripheral := range module.Peripherals {
			if failure.Search.CheckpointAlternatives[peripheral.ComponentID] == 0 {
				t.Fatalf("%s never reached a placement checkpoint: %v", peripheral.ComponentID, err)
			}
		}
	}
	if failure.RemainingCandidates != 0 && !isSchematicSearchResourceStop(err) {
		t.Fatalf("failure must keep an explicit resource or conflict reason: %v", err)
	}
}

// Route alternatives and reroute rounds re-prove the same island on identical
// geometry. A complete verdict is replayed without a second debit; the probe
// input (bodies, pins, wires, markers, island, policy) is the cache key.
func TestIssue273RouteNamingProbeReplaysCompleteVerdict(t *testing.T) {
	input := loadIssue273Fixture(t, "boost-input")
	var core powerLayoutPlacement
	for _, m := range input.Measurements {
		if m.Designator == "U3" {
			core = plTranslate(m, -m.X, -m.Y)
		}
	}
	p := powerLayoutPlan{Placements: []powerLayoutPlacement{core}}
	pin, ok := libPin(core, "4")
	if !ok {
		t.Fatal("missing FB pin")
	}
	island := libIsland{net: pin.Net, pins: []powerLayoutPin{pin}}
	routing, err := newSchematicRoutingContext(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	budget := 100000
	routing.candidateBudget = &budget
	first, complete := libRouteNamingProbe(&p, island, "direct", routing)
	spent := 100000 - budget
	if !complete || first == nil || spent == 0 {
		t.Fatalf("baseline probe complete=%v witness=%v spent=%d", complete, first != nil, spent)
	}
	again, complete := libRouteNamingProbe(&p, island, "direct", routing)
	if !complete || again != first || 100000-budget != spent {
		t.Fatalf("identical probe was re-paid: spent %d then %d", spent, 100000-budget-spent)
	}
	moved := p
	moved.Wires = []powerLayoutWire{{Net: pin.Net, Points: [][2]float64{{pin.X, pin.Y}, {pin.X + 5, pin.Y}}}}
	if _, complete = libRouteNamingProbe(&moved, island, "direct", routing); !complete || 100000-budget == spent {
		t.Fatal("changed geometry must not reuse the cached verdict")
	}
}

// Issue #273 budget accounting: the relocation target share and the first
// terminal slice were fractions of maxCandidates, so the boost zone succeeded
// with 18,994 candidates at 100,000 but failed at 80,000 (its accepted C21 +10
// pose received only 6,348 of the 8,022 candidates it needs). Slice sizes are
// now fixed and truncated only by the real remainder: every sufficient total
// follows the same trajectory and spends the same candidates. Wider checkpoint
// search now also solves the frozen 3,000 cap; tiny caps keep a resource stop.
func TestIssue273SufficientBudgetIsNotDefeatedBySlicing(t *testing.T) {
	used := -1
	for _, budget := range []int{3000, 20000, 80000, 100000} {
		input := loadIssue273Fixture(t, "boost-input")
		input.MaxCandidates = budget
		start := time.Now()
		result, err := planLibLayoutZones(input, "")
		if elapsed := time.Since(start); elapsed > 60*time.Second {
			t.Fatalf("budget %d took %s", budget, elapsed)
		}
		if err != nil {
			t.Fatalf("budget %d: sufficient total defeated: %v", budget, err)
		}
		if used >= 0 && result.CandidatesUsed != used {
			t.Fatalf("budget %d changed the search trajectory: %d vs %d candidates", budget, result.CandidatesUsed, used)
		}
		used = result.CandidatesUsed
		if used > budget {
			t.Fatalf("spent %d over cap %d", used, budget)
		}
		layout := result.Zones[0].Layout
		if len(layout.Placements) != len(input.Measurements) {
			t.Fatal("lost components")
		}
		if findings := layoutContractRuntimeFindings(t, layout); len(findings) != 0 {
			t.Fatalf("runtime guard: %+v", findings)
		}
	}
	frozen := loadIssue273Fixture(t, "boost-input")
	frozen.MaxCandidates = 1
	_, err := planLibLayout(frozen)
	var failure *SchematicLayoutSearchFailure
	if !errors.As(err, &failure) || failure.RemainingCandidates != 0 || !errors.Is(err, errLibLayoutBudget) {
		t.Fatalf("tiny cap must stop with an explicit budget reason: %v", err)
	}
}

// The unchanged canonical fixtures must now solve in their authored allowance,
// retaining every measured symbol and explicit attachment, and must compose on
// the original paper. No labels may substitute for a mandatory physical path.
func TestIssue273FrozenDefaultCompletesWithoutWeakeningEvidence(t *testing.T) {
	for _, name := range []string{"input", "boost-input"} {
		t.Run(name, func(t *testing.T) {
			input := loadIssue273Fixture(t, name)
			before, _ := json.Marshal(input)
			result, err := planLibLayoutZones(input, "")
			if err != nil {
				t.Fatal(err)
			}
			if result.CandidatesUsed > input.MaxCandidates || result.CandidatesUsed <= 0 || len(result.Zones) != 1 {
				t.Fatalf("invalid accounting: %+v", result)
			}
			layout := result.Zones[0].Layout
			if layout.LayoutMode != "" {
				t.Fatal("substituted an alternate mode")
			}
			if len(layout.Placements) != len(input.Measurements) {
				t.Fatal("lost components")
			}
			p := powerLayoutPlan{Placements: layout.Placements, Wires: layout.Wires, Flags: layout.Flags}
			for _, original := range input.Measurements {
				actual := libPlacementByDesignator(&p, original.Designator)
				if actual == nil || !reflect.DeepEqual(plTranslate(original, actual.X-original.X, actual.Y-original.Y), *actual) {
					t.Fatal("changed measured body/text/pin geometry or pose", original.Designator)
				}
			}
			policies := map[string]string{}
			for _, net := range input.Connectivity.Nets {
				policies[net.Name] = input.LayoutModules[0].NetPolicies[net.ID]
			}
			first := map[string]powerLayoutPin{}
			for _, c := range p.Placements {
				for _, pin := range c.Pins {
					if policies[pin.Net] != "direct" {
						continue
					}
					if prior, ok := first[pin.Net]; ok {
						if !libPinsShareIsland(&p, prior, pin) {
							t.Fatal("direct net split into label islands", pin.Net)
						}
					} else {
						first[pin.Net] = pin
					}
				}
			}
			idToRef := map[string]string{}
			for _, c := range input.Connectivity.Components {
				idToRef[c.ID] = c.Ref
				if layout.ComponentIDs[c.Ref] != c.ID {
					t.Fatal("lost canonical component identity")
				}
			}
			for _, h := range input.LayoutModules[0].Peripherals {
				if h.AttachTo == nil || h.PinNumber == "" {
					continue
				}
				host := libPlacementByDesignator(&p, idToRef[h.AttachTo.ComponentID])
				own := libPlacementByDesignator(&p, idToRef[h.ComponentID])
				a, aOK := libPin(*host, h.AttachTo.PinNumber)
				b, bOK := libPin(*own, h.PinNumber)
				if !aOK || !bOK || !libPinsShareIsland(&p, a, b) {
					t.Fatal("lost explicit attachment", h.ComponentID)
				}
			}
			if findings := layoutContractRuntimeFindings(t, layout); len(findings) != 0 {
				t.Fatalf("runtime guard: %+v", findings)
			}
			if layout.Routing.ExpandedNodes > layout.Routing.MaxExpandedNodes || layout.Routing.Reroutes > layout.Routing.MaxReroutes {
				t.Fatal("reset routing allowances")
			}
			if len(layout.Search.SpacingTrials) != 1 || layout.Search.SpacingTrials[0].Result != "accepted" {
				t.Fatal("missing accepted spacing diagnostic")
			}
			composition, err := planLibLayout(input)
			if err != nil {
				t.Fatal("original sheet cannot contain result", err)
			}
			if composition.Sheet != input.Sheet || !reflect.DeepEqual(composition.SheetBorder, input.SheetBorder) || !reflect.DeepEqual(composition.Connectivity, input.Connectivity) {
				t.Fatal("changed sheet or canonical graph")
			}
			after, _ := json.Marshal(input)
			if !bytes.Equal(before, after) {
				t.Fatal("mutated source")
			}
			t.Logf("%s: candidates=%d/%d, nodes=%d/%d", name, result.CandidatesUsed, input.MaxCandidates, layout.Routing.ExpandedNodes, layout.Routing.MaxExpandedNodes)
		})
	}
}

func TestIssue273OriginalPaperStillRejectsTooSmallComposition(t *testing.T) {
	input := loadIssue273Fixture(t, "boost-input")
	input.Sheet = layoutBBox{0, 0, 100, 100}
	border := layoutBBox{10, 10, 90, 90}
	input.SheetBorder = &border
	before, _ := json.Marshal(input)
	if out, err := planLibLayout(input); err == nil || out != nil {
		t.Fatal("expanded paper or emitted a partial composition")
	}
	after, _ := json.Marshal(input)
	if !bytes.Equal(before, after) {
		t.Fatal("changed original input on failure")
	}
}

func TestCheckpointSpacingRejectsBrokenPeripheralAndKeepsSharedBudget(t *testing.T) {
	input := standaloneLayoutFixture()
	compact, err := PlanSchematicLayout(input)
	if err != nil {
		t.Fatal(err)
	}
	p := powerLayoutPlan{Placements: compact.Placements, Wires: compact.Wires, Flags: compact.Flags}
	before, _ := json.Marshal(p)
	for _, allowance := range []int{1, 20000} {
		measured := map[string]powerLayoutPlacement{}
		for _, c := range input.Components {
			measured[c.ID] = c.Measurement
		}
		budget := allowance
		search := newSchematicRepairSearch(input, measured, []string{"anchor", "peripheral"}, nil, &budget)
		if _, ok := search.tryCheckpointSpacing(p); ok {
			t.Fatal("accepted spacing that splits a required peripheral")
		}
		if budget < 0 || budget >= allowance || len(search.diagnostics.SpacingTrials) != 1 {
			t.Fatalf("invalid bounded accounting: remaining=%d", budget)
		}
		trace := search.diagnostics.SpacingTrials[0]
		if trace.CandidatesUsed != allowance-budget || trace.CandidatesUsed > trace.CandidateQuota+1 || trace.Result == "accepted" {
			t.Fatalf("invalid spacing diagnostic: %+v", trace)
		}
		after, _ := json.Marshal(p)
		if !bytes.Equal(before, after) {
			t.Fatal("failed candidate corrupted its parent checkpoint")
		}
	}
}
