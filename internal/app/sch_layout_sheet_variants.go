package app

import (
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
)

const schematicZVariantBeamWidth = 16

// Candidate tests refer to the variant-selection phase. The independent
// all-default safeguard uses the existing Z planner's own bounded pass.
type SchematicSheetVariantSearch struct {
	Strategy          string `json:"strategy"`
	BeamWidth         int    `json:"beamWidth"`
	CandidateLimit    int    `json:"candidateLimit"`
	CandidatesUsed    int    `json:"candidatesUsed"`
	BudgetLimited     bool   `json:"budgetLimited"`
	DefaultFeasible   bool   `json:"defaultFeasible"`
	DefaultPageCount  int    `json:"defaultPageCount"`
	BaselineFeasible  bool   `json:"baselineFeasible"`
	BaselinePageCount int    `json:"baselinePageCount"`
	SelectedPageCount int    `json:"selectedPageCount"`
}

func schematicSheetHasVariants(zones []SchematicRenderZone) bool {
	for _, z := range zones {
		if len(z.Variants) > 0 {
			return true
		}
	}
	return false
}

// The default is identified by immutable geometry, never by a special ID name.
// Variants are validated before this adapter is called. Output contains only
// the selected payload; render cannot accidentally optimize it a second time.
func schematicSheetVariantOptions(z SchematicRenderZone) ([]SchematicRenderZone, error) {
	if len(z.Variants) == 0 {
		return []SchematicRenderZone{z}, nil
	}
	options := make([]SchematicRenderZone, 0, len(z.Variants))
	baseline := -1
	for _, variant := range z.Variants {
		option := z
		frame, bounds := variant.Frame, variant.ContentBounds
		option.Layout, option.Frame, option.ContentBounds = variant.Layout, &frame, &bounds
		option.SelectedVariantID, option.Variants = variant.ID, nil
		options = append(options, option)
		if (z.SelectedVariantID == "" || z.SelectedVariantID == variant.ID) && schematicVariantLayoutGeometryEqual(z.Layout, variant.Layout) && z.Frame != nil && reflect.DeepEqual(*z.Frame, variant.Frame) && z.ContentBounds != nil && schematicVariantBoxEqual(*z.ContentBounds, variant.ContentBounds) {
			baseline = len(options) - 1
		}
	}
	if baseline < 0 {
		return nil, fmt.Errorf("zone %s variants must contain the unchanged default layout/frame", z.ID)
	}
	// Exact score ties retain the default before alternatives without relying on
	// naming conventions or on the producer's ordering of the candidate array.
	if baseline > 0 {
		original := options[baseline]
		copy(options[1:baseline+1], options[:baseline])
		options[0] = original
	}
	return options, nil
}

type schematicZVariantState struct {
	closed []SchematicRenderInput
	shelf  schematicZShelf
}

func schematicVariantPages(in SchematicRenderInput, state schematicZVariantState) []SchematicRenderInput {
	pages := append([]SchematicRenderInput(nil), state.closed...)
	if len(state.shelf.zones) > 0 {
		page := in
		page.Zones = state.shelf.zones
		pages = append(pages, page)
	}
	return pages
}

// Page count is the leading objective. Current-page occupied height and last
// shelf height preserve room for future zones; area, real wire length and
// adjacency are secondary. Stable traversal resolves exact ties.
func schematicVariantPagesScore(pages []SchematicRenderInput) [7]float64 {
	score := [7]float64{float64(len(pages))}
	for pageIndex, page := range pages {
		bottom, lastTop := math.Inf(1), math.Inf(1)
		for _, z := range page.Zones {
			w, h := sheetRelationDimensions(z)
			score[3] += w * h
			bottom = math.Min(bottom, z.SheetPosition.Y-h)
			lastTop = math.Min(lastTop, z.SheetPosition.Y)
			for _, wire := range z.Layout.Wires {
				for i := 1; i < len(wire.Points); i++ {
					a, b := wire.Points[i-1], wire.Points[i]
					score[4] += math.Abs(a[0]-b[0]) + math.Abs(a[1]-b[1])
				}
			}
			for _, flag := range z.Layout.Flags {
				score[4] += flag.Offset
			}
		}
		if pageIndex == len(pages)-1 && len(page.Zones) > 0 {
			score[1] = sheetPreviewUsable(*page.Sheet).MaxY - bottom
			score[2] = lastTop - bottom
		}
		adjacent := sheetRelationScore(page.Zones)
		score[5], score[6] = score[5]+adjacent[0], score[6]+adjacent[1]
	}
	return score
}

func schematicVariantScoreLess(a, b [7]float64) bool {
	for i := range a {
		if math.Abs(a[i]-b[i]) > 1e-8 {
			return a[i] < b[i]
		}
	}
	return false
}

func schematicVariantStateKey(pages []SchematicRenderInput) string {
	var key strings.Builder
	for _, page := range pages {
		for _, z := range page.Zones {
			fmt.Fprintf(&key, "%q:%q@%g,%g;", z.ID, z.SelectedVariantID, z.SheetPosition.X, z.SheetPosition.Y)
		}
		key.WriteByte('|')
	}
	return key.String()
}

func pruneSchematicVariantStates(in SchematicRenderInput, states []schematicZVariantState) []schematicZVariantState {
	sort.SliceStable(states, func(i, j int) bool {
		return schematicVariantScoreLess(schematicVariantPagesScore(schematicVariantPages(in, states[i])), schematicVariantPagesScore(schematicVariantPages(in, states[j])))
	})
	kept := make([]schematicZVariantState, 0, schematicZVariantBeamWidth)
	seen := map[string]bool{}
	for _, state := range states {
		key := schematicVariantStateKey(schematicVariantPages(in, state))
		if !seen[key] {
			seen[key] = true
			kept = append(kept, state)
		}
		if len(kept) >= schematicZVariantBeamWidth {
			break
		}
	}
	return kept
}

// Expand candidates inside one atomic group on one page. A member that does
// not fit invalidates only that branch; neither a partial group nor a page
// break is allowed to escape before every member has been selected and placed.
func trySchematicZVariantGroup(in SchematicRenderInput, base schematicZVariantState, group sheetRelationGroup, options map[string][]SchematicRenderZone, search *schematicZSearch) ([]schematicZVariantState, error) {
	states := []schematicZVariantState{base}
	for memberIndex, z := range group.zones {
		next := []schematicZVariantState{}
		for _, state := range states {
			for _, option := range options[z.ID] {
				trial := state
				trial.shelf = state.shelf.clone()
				ok, err := trial.shelf.place(option, *in.Sheet, search)
				if err != nil {
					if memberIndex == len(group.zones)-1 {
						return next, err // These earlier alternatives contain the entire group.
					}
					return nil, err
				}
				if ok {
					next = append(next, trial)
				}
			}
		}
		if len(next) == 0 {
			return nil, nil
		}
		states = pruneSchematicVariantStates(in, next)
	}
	return states, nil
}

func planSchematicZVariantSheets(in SchematicRenderInput, zones []SchematicRenderZone) ([]SchematicRenderInput, *SchematicSheetVariantSearch, error) {
	return planSchematicZVariantSheetsWithSearch(in, zones, &schematicZSearch{})
}

func planSchematicZVariantSheetsWithSearch(in SchematicRenderInput, zones []SchematicRenderZone, search *schematicZSearch) ([]SchematicRenderInput, *SchematicSheetVariantSearch, error) {
	report := &SchematicSheetVariantSearch{Strategy: "z-variant-beam-with-two-baselines", BeamWidth: schematicZVariantBeamWidth, CandidateLimit: schematicZMaxCandidates}
	options := map[string][]SchematicRenderZone{}
	defaults := make([]SchematicRenderZone, 0, len(zones))
	baseline := make([]SchematicRenderZone, 0, len(zones))
	for _, z := range zones {
		choices, err := schematicSheetVariantOptions(z)
		if err != nil {
			return nil, report, err
		}
		options[z.ID] = choices
		defaults = append(defaults, choices[0])
		original := choices[0]
		if len(z.Variants) > 0 {
			// Producer contract: variants[0] is the original legal baseline;
			// the main payload may instead match a later optimized candidate.
			for _, choice := range choices {
				if choice.SelectedVariantID == z.Variants[0].ID {
					original = choice
					break
				}
			}
		}
		baseline = append(baseline, original)
	}
	// This complete fallback is independent of beam pruning/search exhaustion.
	// An oversized default may fail here while another immutable variant fits.
	best, defaultErr := planSchematicZSheets(in, defaults)
	if defaultErr == nil {
		report.DefaultFeasible, report.DefaultPageCount = true, len(best)
	}
	original, baselineErr := planSchematicZSheets(in, baseline)
	if baselineErr == nil {
		report.BaselineFeasible, report.BaselinePageCount = true, len(original)
		if best == nil || schematicVariantScoreLess(schematicVariantPagesScore(original), schematicVariantPagesScore(best)) {
			best = original
		}
	}
	groups := sheetRelationGroups(zones)
	states := []schematicZVariantState{{shelf: newSchematicZShelf(*in.Sheet)}}
	var stoppedErr error
groupLoop:
	for groupIndex, group := range groups {
		next := []schematicZVariantState{}
		considerComplete := func(trials []schematicZVariantState) {
			if groupIndex != len(groups)-1 {
				return
			}
			for _, state := range trials {
				pages := schematicVariantPages(in, state)
				if best == nil || schematicVariantScoreLess(schematicVariantPagesScore(pages), schematicVariantPagesScore(best)) {
					best = pages
				}
			}
		}
		for _, state := range states {
			trials, err := trySchematicZVariantGroup(in, state, group, options, search)
			considerComplete(trials)
			if err != nil {
				if search.candidates < schematicZMaxCandidates {
					return nil, report, err
				}
				stoppedErr = err
				break groupLoop
			}
			next = append(next, trials...)
			if len(state.shelf.zones) > 0 {
				fresh := schematicZVariantState{closed: schematicVariantPages(in, state), shelf: newSchematicZShelf(*in.Sheet)}
				trials, err = trySchematicZVariantGroup(in, fresh, group, options, search)
				considerComplete(trials)
				if err != nil {
					if search.candidates < schematicZMaxCandidates {
						return nil, report, err
					}
					stoppedErr = err
					break groupLoop
				}
				next = append(next, trials...)
			}
		}
		if len(next) == 0 {
			stoppedErr = fmt.Errorf("same-page group beginning with zone %s has no complete placement in bounded Z variant beam; no capacity conclusion", group.zones[0].ID)
			break
		}
		states = pruneSchematicVariantStates(in, next)
	}
	report.CandidatesUsed = search.candidates
	report.BudgetLimited = search.candidates >= schematicZMaxCandidates
	if stoppedErr != nil && !report.BudgetLimited {
		// A branch that did not fit is not an error in the baseline solution;
		// validated geometry errors remain fatal, not a reason to skip checks.
		if best == nil {
			return nil, report, stoppedErr
		}
	}
	if best == nil {
		if stoppedErr != nil {
			return nil, report, stoppedErr
		}
		return nil, report, fmt.Errorf("no complete Z variant plan; default: %v; original baseline: %v; no capacity conclusion", defaultErr, baselineErr)
	}
	for _, page := range best {
		if err := validateSchematicSheet(page); err != nil {
			return nil, report, err
		}
	}
	report.SelectedPageCount = len(best)
	return best, report, nil
}
