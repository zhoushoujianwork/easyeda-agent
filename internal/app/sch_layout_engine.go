package app

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// Geometry aliases expose the shared representation without requiring Lib IR.
type SchematicPlacement = powerLayoutPlacement
type SchematicPin = powerLayoutPin
type SchematicBox = layoutBBox
type SchematicWire = powerLayoutWire
type SchematicMarker = powerLayoutFlag

type SchematicLayoutAttach struct {
	ComponentID string `json:"componentId"`
	PinNumber   string `json:"pinNumber"`
}
type SchematicLayoutPeripheral struct {
	ComponentID string                 `json:"componentId"`
	PinNumber   string                 `json:"pinNumber,omitempty"`
	AttachTo    *SchematicLayoutAttach `json:"attachTo,omitempty"`
}
type SchematicLayoutComponent struct {
	ID               string             `json:"id"`
	Measurement      SchematicPlacement `json:"measurement"`
	PinStates        map[string]string  `json:"pinStates,omitempty"`
	AllowedRotations []float64          `json:"allowedRotations,omitempty"`
}

type SchematicLayoutOptimization struct {
	MaxVariants int `json:"maxVariants,omitempty"`
	MaxAttempts int `json:"maxAttempts,omitempty"`
}

type SchematicLayoutVariant struct {
	ID     string                 `json:"id"`
	Layout *SchematicLayoutResult `json:"layout"`
}

type SchematicOptimizationReport struct {
	AttemptsUsed        int    `json:"attemptsUsed"`
	AcceptedCandidates  int    `json:"acceptedCandidates"`
	RemainingCandidates int    `json:"remainingCandidates"`
	StopReason          string `json:"stopReason"`
}
type SchematicLayoutInput struct {
	SchemaVersion   int                          `json:"schemaVersion"`
	CoreComponentID string                       `json:"coreComponentId"`
	Components      []SchematicLayoutComponent   `json:"components"`
	NetPolicies     map[string]string            `json:"netPolicies"`
	Attachments     []SchematicLayoutPeripheral  `json:"attachments,omitempty"`
	MaxCandidates   int                          `json:"maxCandidates,omitempty"`
	Optimization    *SchematicLayoutOptimization `json:"optimization,omitempty"`
}
type SchematicLayoutResult struct {
	SchemaVersion      int                               `json:"schemaVersion"`
	ComponentIDs       map[string]string                 `json:"componentIds"`
	PinStates          map[string]map[string]string      `json:"pinStates"`
	Placements         []SchematicPlacement              `json:"placements"`
	Wires              []SchematicWire                   `json:"wires"`
	Flags              []SchematicMarker                 `json:"flags"`
	Score              [4]float64                        `json:"score"`
	CandidatesUsed     int                               `json:"candidatesUsed"`
	Search             *SchematicLayoutSearchDiagnostics `json:"search,omitempty"`
	Variants           []SchematicLayoutVariant          `json:"variants,omitempty"`
	AllowedRotations   map[string][]float64              `json:"allowedRotations,omitempty"`
	OptimizationReport *SchematicOptimizationReport      `json:"optimizationReport,omitempty"`
}

// PlanSchematicLayout is side-effect-free. No project, library, sheet, module
// registration or EDA connection is required. Core placement is normalized to 0,0.
func PlanSchematicLayout(input SchematicLayoutInput) (*SchematicLayoutResult, error) {
	budget := input.MaxCandidates
	if budget == 0 {
		budget = 20000
	}
	if budget < 1 || budget > 1000000 {
		return nil, fmt.Errorf("maxCandidates must be 1..1000000")
	}
	return planSchematicLayoutWithBudget(input, &budget)
}

func planSchematicLayoutWithBudget(input SchematicLayoutInput, budget *int) (*SchematicLayoutResult, error) {
	// Deep copy all slices/maps so candidate search cannot mutate caller evidence.
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	var detached SchematicLayoutInput
	if err = json.Unmarshal(raw, &detached); err != nil {
		return nil, err
	}
	input = detached
	if input.SchemaVersion != 1 || len(input.Components) == 0 {
		return nil, fmt.Errorf("schemaVersion:1 and components required")
	}
	measured := map[string]powerLayoutPlacement{}
	refs := map[string]string{}
	members := []string{}
	used := map[string]bool{}
	for _, c := range input.Components {
		m := c.Measurement
		if strings.TrimSpace(c.ID) == "" || measured[c.ID].Designator != "" || strings.TrimSpace(m.Designator) == "" || refs[m.Designator] != "" {
			return nil, fmt.Errorf("unknown/duplicate component ID or designator %s", c.ID)
		}
		if !plBoxValid(m.BBox) || !plGrid(m.X) || !plGrid(m.Y) || !plFinite(m.Rotation) || math.Mod(m.Rotation, 90) != 0 || len(m.Pins) == 0 {
			return nil, fmt.Errorf("%s incomplete measured geometry", c.ID)
		}
		pins := map[string]bool{}
		for _, p := range m.Pins {
			if p.Number == "" || pins[p.Number] || !plGrid(p.X) || !plGrid(p.Y) {
				return nil, fmt.Errorf("%s invalid/duplicate pin %s", c.ID, p.Number)
			}
			pins[p.Number] = true
			state := c.PinStates[p.Number]
			if p.Net == "" {
				if state != "nc" && state != "unconnected" {
					return nil, fmt.Errorf("%s.%s needs explicit nc/unconnected state", c.ID, p.Number)
				}
			} else {
				if state != "" || strings.TrimSpace(p.Net) == "" {
					return nil, fmt.Errorf("%s.%s conflicts with connected net", c.ID, p.Number)
				}
				used[p.Net] = true
			}
			if _, e := libPinSide(p, m.BBox); e != nil {
				return nil, e
			}
		}
		for n := range c.PinStates {
			if !pins[n] {
				return nil, fmt.Errorf("%s unknown pin state %s", c.ID, n)
			}
		}
		for _, b := range m.TextBBoxes {
			if !plBoxValid(b) {
				return nil, fmt.Errorf("%s invalid text bbox", c.ID)
			}
		}
		measured[c.ID] = m
		refs[m.Designator] = c.ID
		members = append(members, c.ID)
	}
	if _, ok := measured[input.CoreComponentID]; !ok {
		return nil, fmt.Errorf("unknown coreComponentId")
	}
	for n := range used {
		switch input.NetPolicies[n] {
		case "direct", "module_port", "local_power", "local_ground":
		default:
			return nil, fmt.Errorf("net %s needs explicit policy", n)
		}
	}
	for n := range input.NetPolicies {
		if !used[n] {
			return nil, fmt.Errorf("unused policy %s", n)
		}
	}
	hints := map[string]SchematicLayoutPeripheral{}
	for _, h := range input.Attachments {
		if measured[h.ComponentID].Designator == "" || h.ComponentID == input.CoreComponentID || hints[h.ComponentID].ComponentID != "" {
			return nil, fmt.Errorf("invalid/duplicate attachment %s", h.ComponentID)
		}
		if h.AttachTo != nil && (h.AttachTo.ComponentID == h.ComponentID || measured[h.AttachTo.ComponentID].Designator == "") {
			return nil, fmt.Errorf("invalid attachment target")
		}
		hints[h.ComponentID] = h
	}
	optimization, allowed, err := schematicOptimizationSettings(input)
	if err != nil {
		return nil, err
	}
	before := *budget
	result, err := solveSchematicLayout(input, measured, members, hints, budget)
	if err != nil {
		return nil, err
	}
	result.SchemaVersion = 1
	result.ComponentIDs = refs
	result.PinStates = map[string]map[string]string{}
	for _, c := range input.Components {
		result.PinStates[c.ID] = c.PinStates
	}
	result.CandidatesUsed = before - *budget
	if optimization != nil {
		result = optimizeSchematicLayout(input, result, measured, members, hints, *optimization, allowed, budget)
		result.CandidatesUsed = before - *budget
	}
	return result, nil
}
