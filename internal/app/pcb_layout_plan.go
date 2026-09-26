package app

// pcb_layout_plan.go implements the pure, module-at-a-time PCB placement planner.
// It deliberately separates intent from geometry:
//
//   * layout JSON says which parts belong together, which pin owns each satellite,
//     and which transformations may be considered;
//   * a `pcb dump` snapshot supplies measured footprint anchors, bboxes and pads;
//   * the planner emits several factual candidates and an `easyeda apply` playbook,
//     but never writes to the editor and never collapses the facts into a score.

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"math"
	"sort"
	"strings"
)

type pcbLayoutPlanInput struct {
	SchemaVersion      int                   `json:"schemaVersion"`
	Units              string                `json:"units"`
	CoordinateSemantic string                `json:"coordinateSemantic"`
	MinGapMil          float64               `json:"minGapMil,omitempty"`
	Apply              pcbLayoutApplyMeta    `json:"apply,omitempty"`
	Keepouts           []pcbLayoutKeepout    `json:"keepouts,omitempty"`
	Modules            []pcbLayoutModuleSpec `json:"modules"`
	Routing            *pcbRoutingIntent     `json:"routing,omitempty"`
	Reflow             *pcbReflowSpec        `json:"reflow,omitempty"`
}

type pcbLayoutApplyMeta struct {
	Project string `json:"project,omitempty"`
	Window  string `json:"window,omitempty"`
	Doc     string `json:"doc,omitempty"`
}

type pcbLayoutKeepout struct {
	ID         string   `json:"id"`
	MinX       float64  `json:"minXMil"`
	MinY       float64  `json:"minYMil"`
	MaxX       float64  `json:"maxXMil"`
	MaxY       float64  `json:"maxYMil"`
	Source     string   `json:"source,omitempty"`
	Layers     []string `json:"layers,omitempty"` // top | bottom | all; omitted = all
	ExceptRefs []string `json:"exceptRefs,omitempty"`
}

type pcbLayoutModuleSpec struct {
	ExistingObjects []pcbModuleOwnedObject   `json:"existingObjects,omitempty"`
	ID              string                   `json:"id"`
	Strategy        string                   `json:"strategy"`               // rigid | edge | pin-satellites
	CopperPolicy    string                   `json:"copperPolicy,omitempty"` // ignore | require-board-empty
	AnchorRef       string                   `json:"anchorRef"`
	Members         []pcbLayoutMemberSpec    `json:"members"`
	Search          pcbLayoutSearchSpec      `json:"search"`
	PinAssignments  []pcbLayoutPinAssignment `json:"pinAssignments,omitempty"`
	Relations       []pcbLayoutRelation      `json:"relations,omitempty"`
	CrystalGuard    *pcbCrystalGuardSpec     `json:"crystalGuard,omitempty"`
}

type pcbCrystalProtectionSearch struct {
	GuardOffsetsMil []float64 `json:"guardOffsetsMil,omitempty"`
	StepMil         float64   `json:"stepMil"`
	MaxDetourMil    float64   `json:"maxDetourMil"`
}

type pcbCrystalGuardSpec struct {
	ReuseGroundAnchorViaIDs []string                    `json:"reuseGroundAnchorViaIds,omitempty"`
	GroundImplementation    string                      `json:"groundImplementation,omitempty"`
	ProtectionSearch        *pcbCrystalProtectionSearch `json:"protectionSearch,omitempty"`
	CrystalRef              string                      `json:"crystalRef"`
	OwnerRef                string                      `json:"ownerRef"`
	OwnerSide               string                      `json:"ownerSide"` // bottom (v2)
	Ports                   []pcbCrystalGuardPort       `json:"ports"`
	GroundNet               string                      `json:"groundNet"`
	CrystalGroundPads       []string                    `json:"crystalGroundPads"`
	GroundAnchors           []string                    `json:"groundAnchors"`
	SignalLayer             int                         `json:"signalLayer"`
	SignalWidthMil          float64                     `json:"signalWidthMil"`
	ComponentGapMil         float64                     `json:"componentGapMil"`
	GuardLayer              int                         `json:"guardLayer"`
	GuardWidthMil           float64                     `json:"guardWidthMil"`
	GuardGapMil             float64                     `json:"guardGapMil"`
	KeepoutMarginMil        float64                     `json:"keepoutMarginMil"`
	FencePitchMil           float64                     `json:"fencePitchMil"`
	FenceMarginMil          float64                     `json:"fenceMarginMil"`
	ViaHoleMil              float64                     `json:"viaHoleMil,omitempty"`
	ViaDiameterMil          float64                     `json:"viaDiameterMil,omitempty"`
	LocalPourMarginMil      float64                     `json:"localPourMarginMil"`
	ReplacePrimitiveIDs     []string                    `json:"replacePrimitiveIds"`
}

type pcbCrystalGuardPort struct {
	Net                  string  `json:"net"`
	OwnerPad             string  `json:"ownerPad"`
	CrystalPad           string  `json:"crystalPad"`
	CapacitorRef         string  `json:"capacitorRef"`
	CapacitorSignalPad   string  `json:"capacitorSignalPad"`
	CapacitorGroundPad   string  `json:"capacitorGroundPad"`
	CapacitorSide        string  `json:"capacitorSide"` // left | right
	CapacitorRotationDeg float64 `json:"capacitorRotationDeg"`
}

type pcbLayoutMemberSpec struct {
	Ref                 string    `json:"ref"`
	FixedAxes           []string  `json:"fixedAxes,omitempty"` // x | y | rotation
	AllowedRotationsDeg []float64 `json:"allowedRotationsDeg,omitempty"`
	Side                string    `json:"side,omitempty"`        // current | top | bottom
	LocalFacing         string    `json:"localFacing,omitempty"` // footprint at 0deg: left|right|bottom|top
}

type pcbLayoutSearchSpec struct {
	// rigid: offsets × rotation deltas form the finite search set.
	OffsetsMil        []pcbLayoutOffset `json:"offsetsMil,omitempty"`
	RotationDeltasDeg []float64         `json:"rotationDeltasDeg,omitempty"`
	// edge: rotate the whole module, align EdgeMember to an edge, and choose its
	// along-edge seed from AlongRefs (+ each AlongOffsetMil).
	Edges            []string  `json:"edges,omitempty"`
	EdgeMember       string    `json:"edgeMember,omitempty"`
	EdgeClearanceMil float64   `json:"edgeClearanceMil,omitempty"`
	AlongRefs        []string  `json:"alongRefs,omitempty"`
	AlongOffsetsMil  []float64 `json:"alongOffsetsMil,omitempty"`
	// pin-satellites: one complete candidate per explicit gap. Each satellite
	// still uses its own exact owner/self pad pair from PinAssignments.
	GapsMil []float64 `json:"gapsMil,omitempty"`
	// crystal-guard: bounded offsets relative to owner-pad alignment; away is -Y.
	CrystalOffsets *pcbCrystalOffsetSearch `json:"crystalOffsets,omitempty"`
}

type pcbCrystalOffsetSearch struct {
	MaxXMil    float64 `json:"maxXMil"`
	MaxAwayMil float64 `json:"maxAwayMil"`
	StepMil    float64 `json:"stepMil"`
}

type pcbLayoutOffset struct {
	XMil  float64 `json:"xMil"`
	YMil  float64 `json:"yMil"`
	Label string  `json:"label,omitempty"`
}

type pcbLayoutPinAssignment struct {
	MemberRef            string   `json:"memberRef"`
	MemberPad            string   `json:"memberPad"`
	OwnerRef             string   `json:"ownerRef"`
	OwnerPad             string   `json:"ownerPad"`
	MemberPadPrimitiveID string   `json:"memberPadPrimitiveId,omitempty"`
	OwnerPadPrimitiveID  string   `json:"ownerPadPrimitiveId,omitempty"`
	OwnerPads            []string `json:"ownerPads,omitempty"` // equivalent pad group; measurement/rigid facts only
	OwnerPadPrimitiveIDs []string `json:"ownerPadPrimitiveIds,omitempty"`
	Side                 string   `json:"side,omitempty"` // left|right|bottom|top; omitted = owner pad's nearest bbox edge
	AlongOffsetMil       float64  `json:"alongOffsetMil,omitempty"`
	Source               string   `json:"source,omitempty"`
}

type pcbLayoutRelation struct {
	Kind   string `json:"kind"` // near | far
	From   string `json:"from"`
	To     string `json:"to"`
	Source string `json:"source,omitempty"`
}

type pcbLayoutPlanReport struct {
	SchemaVersion       int                  `json:"schemaVersion"`
	Units               string               `json:"units"`
	CoordinateSemantic  string               `json:"coordinateSemantic"`
	Module              string               `json:"module"`
	Strategy            string               `json:"strategy"`
	BoardOutlineSource  string               `json:"boardOutlineSource"`
	BoardOutlineFormat  string               `json:"boardOutlineFormat,omitempty"`
	EdgeDistanceMethod  string               `json:"edgeDistanceMethod"`
	InputHashSemantic   string               `json:"inputHashSemantic,omitempty"`
	LayoutSHA256        string               `json:"layoutSha256,omitempty"`
	BoardSHA256         string               `json:"boardSha256,omitempty"`
	BoardSemanticSHA256 string               `json:"boardSemanticSha256,omitempty"`
	Candidates          []pcbLayoutCandidate `json:"candidates"`
	Rejected            []pcbLayoutRejected  `json:"rejected,omitempty"`
	MissingGeometry     []string             `json:"missingGeometry,omitempty"`
	Limitations         []string             `json:"limitations,omitempty"`
	Summary             string               `json:"summary"`
}

type pcbLayoutRejected struct {
	Variant      string                `json:"variant"`
	Reasons      []string              `json:"reasons"`
	Placements   []pcbLayoutPlacement  `json:"placements,omitempty"`
	Measurements pcbLayoutMeasurements `json:"measurements,omitempty"`
}

type pcbLayoutCandidate struct {
	SchemaVersion             int                    `json:"schemaVersion,omitempty"`
	RoutingRequirementsSHA256 string                 `json:"routingRequirementsSha256,omitempty"`
	Escape                    *pcbEscapeReport       `json:"escape,omitempty"`
	Reflow                    *pcbReflowCandidate    `json:"reflow,omitempty"`
	ID                        string                 `json:"id"`
	Variant                   string                 `json:"variant"`
	Module                    string                 `json:"module"`
	Strategy                  string                 `json:"strategy"`
	Placements                []pcbLayoutPlacement   `json:"placements"`
	Measurements              pcbLayoutMeasurements  `json:"measurements"`
	Actions                   []pcbLayoutTypedAction `json:"actions"`
	Apply                     playbook               `json:"apply"`
	ApplySHA256               string                 `json:"applySha256,omitempty"`
	BoardSemanticSHA256       string                 `json:"boardSemanticSha256,omitempty"`
	Bundle                    *pcbLayoutModuleBundle `json:"bundle,omitempty"`
	Files                     map[string]string      `json:"files,omitempty"`
}

type pcbLayoutModuleBundle struct {
	GroundImplementation  string                  `json:"groundImplementation,omitempty"`
	ReplacedObjects       []pcbModuleOwnedObject  `json:"replacedObjects,omitempty"`
	UnreservedPours       []pcbModulePour         `json:"unreservedPours,omitempty"`
	ReflowRoutes          []pcbModuleRoute        `json:"reflowRoutes,omitempty"`
	ReflowVias            []pcbModuleVia          `json:"reflowVias,omitempty"`
	ReflowReplaceIDs      []string                `json:"reflowReplacePrimitiveIds,omitempty"`
	GuardContour          [][2]float64            `json:"guardContour,omitempty"`
	FenceContour          [][2]float64            `json:"fenceContour,omitempty"`
	FencePitchMil         float64                 `json:"fencePitchMil,omitempty"`
	Kind                  string                  `json:"kind"`
	OwnedRefs             []string                `json:"ownedRefs"`
	ReplacePrimitiveIDs   []string                `json:"replacePrimitiveIds,omitempty"`
	SignalRoutes          []pcbModuleRoute        `json:"signalRoutes,omitempty"`
	GroundRoutes          []pcbModuleRoute        `json:"groundRoutes,omitempty"`
	Regions               []pcbModuleRegion       `json:"regions,omitempty"`
	Vias                  []pcbModuleVia          `json:"vias,omitempty"`
	Pours                 []pcbModulePour         `json:"pours,omitempty"`
	AffectedBaselinePours []pcbModuleAffectedPour `json:"affectedBaselinePours,omitempty"`
	GroundAnchors         []string                `json:"groundAnchors,omitempty"`
	Envelope              *layoutBBox             `json:"envelope,omitempty"`
	Metrics               map[string]float64      `json:"metrics,omitempty"`
}

// pcbModuleAffectedPour is an explicit, snapshot-bound exception for a
// pre-existing pour whose materialized copper can be recomputed by this module.
// The editable boundary remains immutable. Only materialized geometry inside
// ImpactEnvelope may differ after rebuild; outside geometry and holes must match.
type pcbModuleAffectedPour struct {
	BoundaryPrimitiveID     string     `json:"boundaryPrimitiveId"`
	MaterializedPrimitiveID string     `json:"materializedPrimitiveId"`
	Net                     string     `json:"net"`
	Layer                   int        `json:"layer"`
	ImpactEnvelope          layoutBBox `json:"impactEnvelope"`
}

type pcbModuleRoute struct {
	ID       string       `json:"id"`
	Net      string       `json:"net"`
	Layer    int          `json:"layer"`
	WidthMil float64      `json:"widthMil"`
	Points   [][2]float64 `json:"points"`
	Role     string       `json:"role"`
	From     string       `json:"from,omitempty"`
	Through  []string     `json:"through,omitempty"`
	To       string       `json:"to,omitempty"`
}

type pcbModuleRegion struct {
	ID        string       `json:"id"`
	Layer     int          `json:"layer"`
	RuleTypes []string     `json:"ruleTypes"`
	Points    [][2]float64 `json:"points"`
}

type pcbModuleVia struct {
	ExistingPrimitiveID string  `json:"existingPrimitiveId,omitempty"`
	ID                  string  `json:"id"`
	Net                 string  `json:"net"`
	X                   float64 `json:"x"`
	Y                   float64 `json:"y"`
	HoleMil             float64 `json:"holeMil"`
	DiameterMil         float64 `json:"diameterMil"`
	Role                string  `json:"role"`
}

type pcbModulePour struct {
	ID     string       `json:"id"`
	Net    string       `json:"net"`
	Layer  int          `json:"layer"`
	Points [][2]float64 `json:"points"`
}

type pcbLayoutPlacement struct {
	Ref         string      `json:"ref"`
	PrimitiveID string      `json:"primitiveId"`
	XMil        float64     `json:"xMil"`
	YMil        float64     `json:"yMil"`
	RotationDeg float64     `json:"rotationDeg"`
	Layer       int         `json:"layer"`
	BBox        *layoutBBox `json:"bbox,omitempty"`
	Pads        []boardPad  `json:"pads,omitempty"`
}

type pcbLayoutTypedAction struct {
	ID      string         `json:"id"`
	Action  string         `json:"action"`
	Payload map[string]any `json:"payload"`
}

type pcbLayoutMeasurements struct {
	ModuleBounds                *layoutBBox                    `json:"moduleBounds,omitempty"`
	ModuleWidthMil              float64                        `json:"moduleWidthMil,omitempty"`
	ModuleHeightMil             float64                        `json:"moduleHeightMil,omitempty"`
	MinInternalGapMil           *float64                       `json:"minInternalGapMil,omitempty"`
	ClosestInternalGap          *pcbLayoutGapMeasurement       `json:"closestInternalGap,omitempty"`
	MinExternalComponentGapMil  *float64                       `json:"minExternalComponentGapMil,omitempty"`
	ClosestExternalComponentGap *pcbLayoutGapMeasurement       `json:"closestExternalComponentGap,omitempty"`
	MinKeepoutGapMil            *float64                       `json:"minKeepoutGapMil,omitempty"`
	ClosestKeepoutGap           *pcbLayoutGapMeasurement       `json:"closestKeepoutGap,omitempty"`
	PinDistances                []pcbLayoutPinDistance         `json:"pinDistances,omitempty"`
	EdgeDistances               []pcbLayoutEdgeDistance        `json:"edgeDistances,omitempty"`
	Relations                   []pcbLayoutRelationMeasurement `json:"relations,omitempty"`
	Facings                     []pcbLayoutFacingMeasurement   `json:"facings,omitempty"`
	MissingGeometry             []string                       `json:"missingGeometry,omitempty"`
	Limitations                 []string                       `json:"limitations,omitempty"`
}

type pcbLayoutGapMeasurement struct {
	From        string  `json:"from"`
	To          string  `json:"to"`
	DistanceMil float64 `json:"distanceMil"`
}

type pcbLayoutFacingMeasurement struct {
	Ref            string `json:"ref"`
	LocalFacing    string `json:"localFacing"`
	ActualFacing   string `json:"actualFacing"`
	NearestEdge    string `json:"nearestEdge"`
	OutwardAligned bool   `json:"outwardAligned"`
}

type pcbLayoutPinDistance struct {
	Member      string  `json:"member"`
	MemberPad   string  `json:"memberPad"`
	Owner       string  `json:"owner"`
	OwnerPad    string  `json:"ownerPad"`
	Net         string  `json:"net,omitempty"`
	DistanceMil float64 `json:"distanceMil"`
	Source      string  `json:"source,omitempty"`
}

type pcbLayoutEdgeDistance struct {
	Ref         string  `json:"ref"`
	NearestEdge string  `json:"nearestEdge"`
	DistanceMil float64 `json:"distanceMil"`
	Source      string  `json:"source"`
}

type pcbLayoutRelationMeasurement struct {
	Kind        string  `json:"kind"`
	From        string  `json:"from"`
	To          string  `json:"to"`
	DistanceMil float64 `json:"distanceMil"`
	Source      string  `json:"source,omitempty"`
}

type pcbLayoutVariant struct {
	label string
	comps map[string]boardComp
}

func decodePCBLayoutPlanInput(raw []byte) (pcbLayoutPlanInput, error) {
	var in pcbLayoutPlanInput
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return in, fmt.Errorf("parse layout input: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return in, fmt.Errorf("parse layout input: multiple JSON values are not allowed")
		}
		return in, fmt.Errorf("parse layout input trailing data: %w", err)
	}
	if in.SchemaVersion != 1 && in.SchemaVersion != 2 && in.SchemaVersion != 3 {
		return in, fmt.Errorf("unsupported schemaVersion %d (want 1, 2 or 3)", in.SchemaVersion)
	}
	if in.SchemaVersion < 3 && (in.Routing != nil || in.Reflow != nil) {
		return in, fmt.Errorf("routing/reflow require schemaVersion 3")
	}
	if in.SchemaVersion == 3 && in.Routing == nil {
		return in, fmt.Errorf("schemaVersion 3 requires independent routing demands")
	}
	if in.Units != "mil" {
		return in, fmt.Errorf("units must be %q", "mil")
	}
	if in.CoordinateSemantic != "footprint-anchor" {
		return in, fmt.Errorf("coordinateSemantic must be %q", "footprint-anchor")
	}
	if len(in.Modules) == 0 {
		return in, fmt.Errorf("modules must contain at least one module")
	}
	if !isFinite(in.MinGapMil) || in.MinGapMil < 0 {
		return in, fmt.Errorf("minGapMil must be finite and non-negative")
	}
	for i := range in.Keepouts {
		k := &in.Keepouts[i]
		if strings.TrimSpace(k.ID) == "" || !allFinite(k.MinX, k.MinY, k.MaxX, k.MaxY) || k.MaxX <= k.MinX || k.MaxY <= k.MinY {
			return in, fmt.Errorf("keepouts[%d] requires id and increasing min/max coordinates", i)
		}
		for _, layer := range k.Layers {
			switch strings.ToLower(strings.TrimSpace(layer)) {
			case "top", "bottom", "all":
			default:
				return in, fmt.Errorf("keepouts[%d] has invalid layer %q (want top|bottom|all)", i, layer)
			}
		}
	}
	seenModules := map[string]bool{}
	for i, mod := range in.Modules {
		if strings.TrimSpace(mod.ID) == "" {
			return in, fmt.Errorf("modules[%d] requires id", i)
		}
		if seenModules[mod.ID] {
			return in, fmt.Errorf("duplicate module id %q", mod.ID)
		}
		seenModules[mod.ID] = true
		if in.SchemaVersion == 3 && mod.CrystalGuard != nil && mod.CrystalGuard.GroundImplementation != "tracks-vias" {
			return in, fmt.Errorf("schemaVersion 3 crystal-guard requires groundImplementation=tracks-vias")
		}
		if err := validatePCBLayoutInputNumbers(mod); err != nil {
			return in, err
		}
	}
	return in, nil
}

func validatePCBLayoutInputNumbers(mod pcbLayoutModuleSpec) error {
	if s := mod.Search.CrystalOffsets; s != nil {
		if mod.Strategy != "crystal-guard" || !allFinite(s.MaxXMil, s.MaxAwayMil, s.StepMil) || s.MaxXMil < 0 || s.MaxAwayMil < 0 || s.StepMil <= 0 {
			return fmt.Errorf("module %q crystalOffsets requires crystal-guard, finite non-negative maxXMil/maxAwayMil and positive stepMil", mod.ID)
		}
		if (2*math.Ceil(s.MaxXMil/s.StepMil)+3)*(math.Ceil(s.MaxAwayMil/s.StepMil)+2) > 4096 {
			return fmt.Errorf("module %q crystalOffsets grid exceeds 4096 offset limit", mod.ID)
		}
	}
	for i, off := range mod.Search.OffsetsMil {
		if !allFinite(off.XMil, off.YMil) {
			return fmt.Errorf("module %q search.offsetsMil[%d] must be finite", mod.ID, i)
		}
		if mod.Strategy == "crystal-guard" && off.YMil > 0 {
			return fmt.Errorf("module %q crystal-guard offsetsMil yMil must be <= 0 (away from bottom owner)", mod.ID)
		}
	}
	for i, d := range mod.Search.RotationDeltasDeg {
		if !isQuarterTurn(d) {
			return fmt.Errorf("module %q search.rotationDeltasDeg[%d] must be one of 0/90/180/270", mod.ID, i)
		}
	}
	if !isFinite(mod.Search.EdgeClearanceMil) || mod.Search.EdgeClearanceMil < 0 {
		return fmt.Errorf("module %q edgeClearanceMil must be finite and non-negative", mod.ID)
	}
	for i, n := range mod.Search.AlongOffsetsMil {
		if !isFinite(n) {
			return fmt.Errorf("module %q search.alongOffsetsMil[%d] must be finite", mod.ID, i)
		}
	}
	for i, n := range mod.Search.GapsMil {
		if !isFinite(n) || n < 0 {
			return fmt.Errorf("module %q search.gapsMil[%d] must be finite and non-negative", mod.ID, i)
		}
	}
	for i, m := range mod.Members {
		for j, r := range m.AllowedRotationsDeg {
			if !isQuarterTurn(r) {
				return fmt.Errorf("module %q members[%d].allowedRotationsDeg[%d] must be one of 0/90/180/270", mod.ID, i, j)
			}
		}
	}
	for i, a := range mod.PinAssignments {
		if !isFinite(a.AlongOffsetMil) {
			return fmt.Errorf("module %q pinAssignments[%d].alongOffsetMil must be finite", mod.ID, i)
		}
	}
	for i, r := range mod.Relations {
		if r.Kind != "near" && r.Kind != "far" {
			return fmt.Errorf("module %q relations[%d] has invalid kind %q (want near|far)", mod.ID, i, r.Kind)
		}
		if strings.TrimSpace(r.From) == "" || strings.TrimSpace(r.To) == "" {
			return fmt.Errorf("module %q relations[%d] requires from and to", mod.ID, i)
		}
	}
	if mod.CrystalGuard != nil {
		g := mod.CrystalGuard
		if g.GroundImplementation != "" && g.GroundImplementation != "tracks-vias" && g.GroundImplementation != "legacy-local-pours" {
			return fmt.Errorf("module %q crystalGuard.groundImplementation must be tracks-vias", mod.ID)
		}
		if !allFinite(g.SignalWidthMil, g.ComponentGapMil, g.GuardWidthMil, g.GuardGapMil, g.KeepoutMarginMil, g.FencePitchMil, g.FenceMarginMil, g.ViaHoleMil, g.ViaDiameterMil, g.LocalPourMarginMil) {
			return fmt.Errorf("module %q crystalGuard numeric fields must be finite", mod.ID)
		}
		for i, p := range g.Ports {
			if !isQuarterTurn(p.CapacitorRotationDeg) {
				return fmt.Errorf("module %q crystalGuard ports[%d].capacitorRotationDeg must be a quarter turn", mod.ID, i)
			}
		}
	}
	return nil
}

func planPCBLayoutModule(in pcbLayoutPlanInput, snap *boardSnapshot, moduleID string, limit int) (pcbLayoutPlanReport, error) {
	rep := pcbLayoutPlanReport{SchemaVersion: in.SchemaVersion, Units: "mil", CoordinateSemantic: "footprint-anchor", Module: moduleID}
	if snap == nil || snap.Outline == nil {
		return rep, fmt.Errorf("board snapshot requires an outline")
	}
	rep.BoardOutlineSource = snap.Outline.Source
	rep.BoardOutlineFormat = snap.Outline.Format
	rep.EdgeDistanceMethod = outlineDistanceMethod(snap.Outline)
	rep.InputHashSemantic = "raw SHA256 for provenance; semantic SHA256 excludes capture timestamp"
	rep.BoardSemanticSHA256 = snap.SemanticSHA256
	rep.Limitations = append(rep.Limitations, snap.Partial...)
	if limit < 1 {
		return rep, fmt.Errorf("candidate limit must be at least 1")
	}
	var mod *pcbLayoutModuleSpec
	for i := range in.Modules {
		if in.Modules[i].ID == moduleID {
			mod = &in.Modules[i]
			break
		}
	}
	if mod == nil {
		return rep, fmt.Errorf("module %q not found", moduleID)
	}
	originalSnapshot := snap
	if len(mod.ExistingObjects) > 0 {
		var filterErr error
		snap, filterErr = removePCBModuleOwnedObjects(snap, mod.ExistingObjects)
		if filterErr != nil {
			return rep, filterErr
		}
	}
	rep.Strategy = mod.Strategy
	if mod.CopperPolicy == "ignore" {
		switch {
		case snap.RoutedLines == nil:
			rep.Limitations = append(rep.Limitations, "copperPolicy=ignore: routed copper count is unavailable; module-to-copper clearance is not proven")
		case *snap.RoutedLines > 0:
			rep.Limitations = append(rep.Limitations, fmt.Sprintf("copperPolicy=ignore: board has %d routed line(s); this snapshot has no copper geometry, so module-to-copper clearance is not proven", *snap.RoutedLines))
		}
	}
	base := map[string]boardComp{}
	for _, c := range snap.Components {
		if _, duplicate := base[c.Designator]; duplicate {
			return rep, fmt.Errorf("board snapshot contains duplicate designator %q", c.Designator)
		}
		base[c.Designator] = c
	}
	members, err := validatePCBLayoutModule(*mod, base)
	if err != nil {
		return rep, err
	}
	if mod.Strategy == "edge" {
		edgeRef := mod.Search.EdgeMember
		if edgeRef == "" {
			edgeRef = mod.AnchorRef
		}
		for _, ms := range mod.Members {
			if ms.Ref == edgeRef && ms.LocalFacing == "" {
				rep.Limitations = append(rep.Limitations, fmt.Sprintf("%s localFacing is not declared; board-edge placement does not prove its mechanical/optical opening faces outward", edgeRef))
			}
		}
	}
	var variants []pcbLayoutVariant
	switch mod.Strategy {
	case "rigid":
		variants, err = generateRigidVariants(*mod, members)
	case "edge":
		variants, err = generateEdgeVariants(*mod, members, base, snap.Outline)
	case "pin-satellites":
		variants, err = generatePinSatelliteVariants(*mod, members)
	case "crystal-guard":
		variants, err = generateCrystalGuardVariants(*mod, members, base, snap)
	default:
		err = fmt.Errorf("module %q has unsupported strategy %q", mod.ID, mod.Strategy)
	}
	if err != nil {
		return rep, err
	}

	memberSet := map[string]bool{}
	memberSpec := map[string]pcbLayoutMemberSpec{}
	for _, m := range mod.Members {
		memberSet[m.Ref] = true
		memberSpec[m.Ref] = m
	}
	minGap := in.MinGapMil
	if minGap <= 0 && snap.Rules != nil {
		minGap = snap.Rules.ClearanceMil
	}
	seen := map[string]bool{}
	reflowByVariant := map[string]*pcbReflowCandidate{}
	if in.Reflow != nil {
		var expanded []pcbLayoutVariant
		remainingStates := in.Reflow.MaxStates
		for _, seed := range variants {
			if remainingStates <= 0 {
				rep.Limitations = append(rep.Limitations, "reflow total search budget exhausted; remaining seeds not examined")
				break
			}
			spec := *in.Reflow
			spec.MaxStates = remainingStates
			vv, rr, err := resolvePCBReflow(spec, seed, snap, minGap)
			remainingStates -= rr.States
			if rr.Exhausted {
				rep.Limitations = append(rep.Limitations, "reflow search incomplete: bounded search exhausted")
			}
			if len(vv) == 0 {
				rep.Rejected = append(rep.Rejected, pcbLayoutRejected{Variant: seed.label, Reasons: rr.Rejected})
			}
			if err != nil {
				rep.Rejected = append(rep.Rejected, pcbLayoutRejected{Variant: seed.label, Reasons: append([]string{err.Error()}, rr.Rejected...)})
				if rr.Exhausted {
					rep.Limitations = append(rep.Limitations, "reflow search budget exhausted; no global impossibility claim")
				}
				continue
			}
			for i := range rr.Candidates {
				value := rr.Candidates[i]
				reflowByVariant[value.Variant] = &value
			}
			expanded = append(expanded, vv...)
		}
		variants = expanded
	}
	for _, v := range variants {
		currentSet := map[string]bool{}
		currentSpecs := map[string]pcbLayoutMemberSpec{}
		for ref := range v.comps {
			currentSet[ref] = true
			currentSpecs[ref] = memberSpec[ref]
		}
		if in.Reflow != nil {
			for _, g := range in.Reflow.Groups {
				for _, ref := range g.Refs {
					if currentSet[ref] && !memberSet[ref] {
						currentSpecs[ref] = pcbLayoutMemberSpec{Ref: ref, FixedAxes: g.FixedAxes, AllowedRotationsDeg: g.AllowedRotationsDeg}
					}
				}
			}
		}
		reasons := validatePCBLayoutVariant(v, base, currentSpecs, currentSet, base, snap, in.Keepouts, minGap, mod.CopperPolicy)
		if len(reasons) > 0 {
			observed := buildPCBLayoutCandidate(in, *mod, v, base, snap, memberSet)
			rep.Rejected = append(rep.Rejected, pcbLayoutRejected{Variant: v.label, Reasons: reasons, Placements: observed.Placements, Measurements: observed.Measurements})
			continue
		}
		sig := pcbLayoutVariantSignature(v)
		if seen[sig] {
			continue
		}
		seen[sig] = true
		candidate := buildPCBLayoutCandidate(in, *mod, v, base, snap, currentSet)
		candidate.BoardSemanticSHA256 = originalSnapshot.SemanticSHA256
		candidate.Reflow = reflowByVariant[v.label]
		if in.Routing != nil {
			if err := attachPCBLayoutRouting(&candidate, in, *mod, v, base, originalSnapshot); err != nil {
				rep.Rejected = append(rep.Rejected, pcbLayoutRejected{Variant: v.label, Reasons: []string{err.Error()}, Placements: candidate.Placements, Measurements: candidate.Measurements})
				continue
			}
		} else if mod.Strategy == "crystal-guard" {
			if err := attachCrystalGuardBundle(&candidate, *mod, v, base, snap); err != nil {
				rep.Rejected = append(rep.Rejected, pcbLayoutRejected{Variant: v.label, Reasons: []string{err.Error()}, Placements: candidate.Placements, Measurements: candidate.Measurements})
				continue
			}
		}
		candidate.ID = fmt.Sprintf("candidate-%02d", len(rep.Candidates)+1)
		candidate.Apply.Meta.Name = mod.ID + " " + candidate.ID
		rep.Candidates = append(rep.Candidates, candidate)
		if len(rep.Candidates) >= limit {
			break
		}
	}
	missing := map[string]bool{}
	for _, c := range members {
		if c.BBox == nil {
			missing[c.Designator+":bbox"] = true
		}
		if len(c.Pads) == 0 {
			missing[c.Designator+":pads"] = true
		}
	}
	for ref, c := range base {
		if !memberSet[ref] && c.BBox == nil {
			missing[ref+":bbox"] = true
		}
	}
	for s := range missing {
		rep.MissingGeometry = append(rep.MissingGeometry, s)
	}
	sort.Strings(rep.MissingGeometry)
	sort.Strings(rep.Limitations)
	rep.Limitations = uniqueStrings(rep.Limitations)
	rep.Summary = fmt.Sprintf("%d candidate(s), %d rejected variant(s); no editor writes performed", len(rep.Candidates), len(rep.Rejected))
	if len(rep.Candidates) == 0 {
		return rep, fmt.Errorf("module %q produced no legal candidate", mod.ID)
	}
	return rep, nil
}

func validatePCBLayoutModule(mod pcbLayoutModuleSpec, all map[string]boardComp) (map[string]boardComp, error) {
	if strings.TrimSpace(mod.ID) == "" || strings.TrimSpace(mod.AnchorRef) == "" {
		return nil, fmt.Errorf("module requires id and anchorRef")
	}
	if mod.CopperPolicy != "ignore" && mod.CopperPolicy != "require-board-empty" && mod.CopperPolicy != "module-owned" {
		return nil, fmt.Errorf("module %q requires explicit copperPolicy ignore|require-board-empty|module-owned", mod.ID)
	}
	if len(mod.Members) == 0 {
		return nil, fmt.Errorf("module %q has no members", mod.ID)
	}
	out := map[string]boardComp{}
	for i, ms := range mod.Members {
		if ms.Ref == "" {
			return nil, fmt.Errorf("module %q members[%d] has no ref", mod.ID, i)
		}
		if _, dup := out[ms.Ref]; dup {
			return nil, fmt.Errorf("module %q repeats member %q", mod.ID, ms.Ref)
		}
		c, ok := all[ms.Ref]
		if !ok {
			return nil, fmt.Errorf("module %q member %q is absent from board snapshot", mod.ID, ms.Ref)
		}
		if strings.TrimSpace(c.ID) == "" {
			return nil, fmt.Errorf("module %q member %q has no primitiveId; executable apply cannot be generated", mod.ID, ms.Ref)
		}
		if c.BBox == nil {
			return nil, fmt.Errorf("module %q member %q has no bbox; legal placement cannot be proven", mod.ID, ms.Ref)
		}
		for _, axis := range ms.FixedAxes {
			if axis != "x" && axis != "y" && axis != "rotation" {
				return nil, fmt.Errorf("module %q member %q has invalid fixed axis %q", mod.ID, ms.Ref, axis)
			}
		}
		if ms.Side != "" && ms.Side != "current" && ms.Side != "top" && ms.Side != "bottom" {
			return nil, fmt.Errorf("module %q member %q has invalid side %q", mod.ID, ms.Ref, ms.Side)
		}
		if ms.LocalFacing != "" {
			if _, ok := parseLayoutEdge(ms.LocalFacing); !ok {
				return nil, fmt.Errorf("module %q member %q has invalid localFacing %q", mod.ID, ms.Ref, ms.LocalFacing)
			}
			if !isNormalizedQuarterTurn(c.Rotation) {
				return nil, fmt.Errorf("module %q member %q localFacing requires a current 0/90/180/270 rotation", mod.ID, ms.Ref)
			}
		}
		out[ms.Ref] = c
	}
	if _, ok := out[mod.AnchorRef]; !ok {
		return nil, fmt.Errorf("module %q anchorRef %q is not a member", mod.ID, mod.AnchorRef)
	}
	if mod.Strategy == "edge" && len(mod.PinAssignments) > 0 {
		edgeMember := mod.Search.EdgeMember
		if edgeMember == "" {
			edgeMember = mod.AnchorRef
		}
		if mod.AnchorRef != edgeMember {
			return nil, fmt.Errorf("module %q hybrid edge strategy requires anchorRef == edgeMember", mod.ID)
		}
		assigned := assignedMemberSet(mod.PinAssignments)
		if assigned[edgeMember] {
			return nil, fmt.Errorf("module %q edgeMember %q cannot also be a pin-assignment follower", mod.ID, edgeMember)
		}
		for _, ms := range mod.Members {
			if ms.Ref == edgeMember || assigned[ms.Ref] || memberExplicitlyRooted(ms, all[ms.Ref]) {
				continue
			}
			return nil, fmt.Errorf("module %q hybrid edge member %q must be a pin-assignment follower or explicitly fixed/locked root", mod.ID, ms.Ref)
		}
	}
	if err := validatePCBLayoutAssignments(mod, out); err != nil {
		return nil, err
	}
	for i, r := range mod.Relations {
		if _, ok := all[r.From]; !ok {
			return nil, fmt.Errorf("module %q relations[%d] from %q is absent from board snapshot", mod.ID, i, r.From)
		}
		if _, ok := all[r.To]; !ok {
			return nil, fmt.Errorf("module %q relations[%d] to %q is absent from board snapshot", mod.ID, i, r.To)
		}
	}
	return out, nil
}

func memberExplicitlyRooted(ms pcbLayoutMemberSpec, c boardComp) bool {
	if c.Locked {
		return true
	}
	fixed := map[string]bool{}
	for _, axis := range ms.FixedAxes {
		fixed[axis] = true
	}
	return fixed["x"] && fixed["y"] && fixed["rotation"]
}

func validatePCBLayoutAssignments(mod pcbLayoutModuleSpec, members map[string]boardComp) error {
	seenFollowers := map[string]bool{}
	for i, a := range mod.PinAssignments {
		if seenFollowers[a.MemberRef] {
			return fmt.Errorf("module %q pinAssignments[%d] repeats follower %q", mod.ID, i, a.MemberRef)
		}
		seenFollowers[a.MemberRef] = true
		member, mok := members[a.MemberRef]
		owner, ook := members[a.OwnerRef]
		if !mok || !ook {
			return fmt.Errorf("module %q pinAssignments[%d] member and owner must both be module members", mod.ID, i)
		}
		if a.MemberRef == a.OwnerRef {
			return fmt.Errorf("module %q pinAssignments[%d] member and owner must differ", mod.ID, i)
		}
		mp, err := findBoardPadExact(member, a.MemberPad, a.MemberPadPrimitiveID)
		if err != nil {
			return fmt.Errorf("module %q pinAssignments[%d] member pad %s.%s: %w", mod.ID, i, a.MemberRef, a.MemberPad, err)
		}
		ops, err := resolveOwnerPads(owner, a)
		if err != nil {
			return fmt.Errorf("module %q pinAssignments[%d] owner pad %s: %w", mod.ID, i, a.OwnerRef, err)
		}
		for _, op := range ops {
			if mp.Net == "" || op.Net == "" || mp.Net != op.Net {
				return fmt.Errorf("module %q pinAssignments[%d] %s.%s → %s.%s does not share a non-empty net", mod.ID, i, a.MemberRef, a.MemberPad, a.OwnerRef, op.Number)
			}
		}
		if a.Side != "" {
			if _, ok := parseLayoutEdge(a.Side); !ok {
				return fmt.Errorf("module %q pinAssignments[%d] has invalid side %q", mod.ID, i, a.Side)
			}
		}
	}
	return nil
}

func generateRigidVariants(mod pcbLayoutModuleSpec, members map[string]boardComp) ([]pcbLayoutVariant, error) {
	offsets := mod.Search.OffsetsMil
	if len(offsets) == 0 {
		offsets = []pcbLayoutOffset{{}}
	}
	rots := mod.Search.RotationDeltasDeg
	if len(rots) == 0 {
		rots = []float64{0}
	}
	anchor := members[mod.AnchorRef]
	var out []pcbLayoutVariant
	for _, off := range offsets {
		for _, d := range rots {
			label := off.Label
			if label == "" {
				label = fmt.Sprintf("offset(%.2f,%.2f)-rotate(%.2f)", off.XMil, off.YMil, d)
			}
			placed := map[string]boardComp{}
			for ref, c := range members {
				projected, err := transformBoardComp(c, anchor.X, anchor.Y, off.XMil, off.YMil, d)
				if err != nil {
					return nil, err
				}
				placed[ref] = projected
			}
			out = append(out, pcbLayoutVariant{label: label, comps: placed})
		}
	}
	return out, nil
}

func generateEdgeVariants(mod pcbLayoutModuleSpec, members, all map[string]boardComp, outline *boardOutline) ([]pcbLayoutVariant, error) {
	if outline == nil {
		return nil, fmt.Errorf("edge strategy requires board outline")
	}
	edgeMember := mod.Search.EdgeMember
	if edgeMember == "" {
		edgeMember = mod.AnchorRef
	}
	if _, ok := members[edgeMember]; !ok {
		return nil, fmt.Errorf("edgeMember %q is not a member of module %q", edgeMember, mod.ID)
	}
	edges := mod.Search.Edges
	if len(edges) == 0 {
		return nil, fmt.Errorf("edge strategy requires search.edges")
	}
	rots := mod.Search.RotationDeltasDeg
	if len(rots) == 0 {
		rots = []float64{0}
	}
	alongRefs := mod.Search.AlongRefs
	if len(alongRefs) == 0 {
		alongRefs = []string{edgeMember}
	}
	alongOffsets := mod.Search.AlongOffsetsMil
	if len(alongOffsets) == 0 {
		alongOffsets = []float64{0}
	}
	anchor := members[mod.AnchorRef]
	outlineBounds := outlineCenterlineBBox(outline)
	gaps := mod.Search.GapsMil
	if len(gaps) == 0 {
		gaps = []float64{20}
	}
	var out []pcbLayoutVariant
	for _, edgeName := range edges {
		edge, ok := parseLayoutEdge(edgeName)
		if !ok {
			return nil, fmt.Errorf("invalid edge %q (want left|right|bottom|top)", edgeName)
		}
		for _, d := range rots {
			rotated := map[string]boardComp{}
			if len(mod.PinAssignments) == 0 {
				for ref, c := range members {
					projected, err := transformBoardComp(c, anchor.X, anchor.Y, 0, 0, d)
					if err != nil {
						return nil, err
					}
					rotated[ref] = projected
				}
			} else {
				// Hybrid edge module: first pin the declared edge member, then place
				// every follower through exact pad ownership. This lets an LED sit on
				// the board edge while its driver/passives reflow on the inboard side,
				// instead of translating an unnecessarily wide historical cluster.
				em0 := members[edgeMember]
				projected, err := transformBoardComp(em0, em0.X, em0.Y, 0, 0, d)
				if err != nil {
					return nil, err
				}
				rotated[edgeMember] = projected
			}
			em := rotated[edgeMember]
			if em.BBox == nil {
				return nil, fmt.Errorf("edge member %q requires bbox geometry", edgeMember)
			}
			for _, ar := range alongRefs {
				target, ok := all[ar]
				if !ok {
					return nil, fmt.Errorf("alongRef %q is absent from board snapshot", ar)
				}
				tx, ty := target.center()
				for _, along := range alongOffsets {
					cx, cy := em.center()
					dx, dy := 0.0, 0.0
					switch edge {
					case edgeLeft:
						dx = outlineBounds.MinX + mod.Search.EdgeClearanceMil - em.BBox.MinX
						dy = ty + along - cy
					case edgeRight:
						dx = outlineBounds.MaxX - mod.Search.EdgeClearanceMil - em.BBox.MaxX
						dy = ty + along - cy
					case edgeBottom:
						dx = tx + along - cx
						dy = outlineBounds.MinY + mod.Search.EdgeClearanceMil - em.BBox.MinY
					case edgeTop:
						dx = tx + along - cx
						dy = outlineBounds.MaxY - mod.Search.EdgeClearanceMil - em.BBox.MaxY
					}
					seed := map[string]boardComp{}
					for ref, c := range rotated {
						projected, err := translateBoardComp(c, dx, dy)
						if err != nil {
							return nil, err
						}
						seed[ref] = projected
					}
					if len(mod.PinAssignments) == 0 {
						label := fmt.Sprintf("edge(%s)-align(%s%+.2f)-rotate(%.2f)", edgeName, ar, along, d)
						out = append(out, pcbLayoutVariant{label: label, comps: seed})
						continue
					}
					assigned := assignedMemberSet(mod.PinAssignments)
					for ref, c := range members {
						if !assigned[ref] && ref != edgeMember {
							seed[ref] = c
						}
					}
					for _, gap := range gaps {
						placed, perr := placePinFollowers(mod, members, seed, gap)
						if perr != nil {
							return nil, perr
						}
						label := fmt.Sprintf("edge(%s)-align(%s%+.2f)-rotate(%.2f)-pin-gap(%.2f)", edgeName, ar, along, d, gap)
						out = append(out, pcbLayoutVariant{label: label, comps: placed})
					}
				}
			}
		}
	}
	return out, nil
}

func generatePinSatelliteVariants(mod pcbLayoutModuleSpec, members map[string]boardComp) ([]pcbLayoutVariant, error) {
	if len(mod.PinAssignments) == 0 {
		return nil, fmt.Errorf("pin-satellites strategy requires pinAssignments")
	}
	gaps := mod.Search.GapsMil
	if len(gaps) == 0 {
		return nil, fmt.Errorf("pin-satellites strategy requires search.gapsMil")
	}
	assigned := assignedMemberSet(mod.PinAssignments)
	var out []pcbLayoutVariant
	for _, gap := range gaps {
		if gap < 0 {
			return nil, fmt.Errorf("pin-satellite gap must be non-negative")
		}
		seed := make(map[string]boardComp, len(members))
		for ref, c := range members {
			if !assigned[ref] {
				seed[ref] = c
			}
		}
		placed, err := placePinFollowers(mod, members, seed, gap)
		if err != nil {
			return nil, err
		}
		out = append(out, pcbLayoutVariant{label: fmt.Sprintf("pin-gap(%.2f)", gap), comps: placed})
	}
	return out, nil
}

func assignedMemberSet(assignments []pcbLayoutPinAssignment) map[string]bool {
	out := map[string]bool{}
	for _, a := range assignments {
		out[a.MemberRef] = true
	}
	return out
}

func placePinFollowers(mod pcbLayoutModuleSpec, members, seed map[string]boardComp, gap float64) (map[string]boardComp, error) {
	memberSpecs := map[string]pcbLayoutMemberSpec{}
	for _, ms := range mod.Members {
		memberSpecs[ms.Ref] = ms
	}
	assigned := map[string]bool{}
	for _, a := range mod.PinAssignments {
		if assigned[a.MemberRef] {
			return nil, fmt.Errorf("member %q has more than one pin assignment", a.MemberRef)
		}
		assigned[a.MemberRef] = true
		if _, ok := members[a.MemberRef]; !ok {
			return nil, fmt.Errorf("pin assignment member %q is outside module", a.MemberRef)
		}
		if _, ok := members[a.OwnerRef]; !ok {
			return nil, fmt.Errorf("pin assignment owner %q is outside module", a.OwnerRef)
		}
		if a.Side != "" {
			if _, ok := parseLayoutEdge(a.Side); !ok {
				return nil, fmt.Errorf("pin assignment %s has invalid side %q", a.MemberRef, a.Side)
			}
		}
	}
	placed := make(map[string]boardComp, len(members))
	for ref, c := range seed {
		placed[ref] = c
	}
	pending := append([]pcbLayoutPinAssignment(nil), mod.PinAssignments...)
	for len(pending) > 0 {
		progress := false
		next := pending[:0]
		for _, a := range pending {
			owner, ready := placed[a.OwnerRef]
			if !ready {
				next = append(next, a)
				continue
			}
			member := members[a.MemberRef]
			ownerPads, err := resolveOwnerPads(owner, a)
			if err != nil {
				return nil, fmt.Errorf("owner pad %s: %w", a.OwnerRef, err)
			}
			if len(ownerPads) != 1 {
				return nil, fmt.Errorf("pin placement for %s requires exactly one owner pad; equivalent ownerPads groups are measurement-only", a.MemberRef)
			}
			op := ownerPads[0]
			mp, err := findBoardPadExact(member, a.MemberPad, a.MemberPadPrimitiveID)
			if err != nil {
				return nil, fmt.Errorf("member pad %s.%s: %w", a.MemberRef, a.MemberPad, err)
			}
			if op.Net == "" || mp.Net == "" || op.Net != mp.Net {
				return nil, fmt.Errorf("pin assignment %s.%s → %s.%s does not share a non-empty net", a.MemberRef, a.MemberPad, a.OwnerRef, a.OwnerPad)
			}
			if owner.BBox == nil || member.BBox == nil {
				return nil, fmt.Errorf("pin assignment %s → %s requires both bboxes", a.MemberRef, a.OwnerRef)
			}
			edge, _ := edgeFor(boardCompToAP(owner), boardPadToAP(op))
			if a.Side != "" {
				edge, _ = parseLayoutEdge(a.Side)
			}
			rot, err := choosePinSatelliteRotation(member, mp.Number, mp.ID, edge, memberSpecs[a.MemberRef].AllowedRotationsDeg)
			if err != nil {
				return nil, err
			}
			rotated, err := transformBoardComp(member, member.X, member.Y, 0, 0, rotationDelta(member.Rotation, rot))
			if err != nil {
				return nil, err
			}
			rmp, err := findBoardPadExact(rotated, a.MemberPad, a.MemberPadPrimitiveID)
			if err != nil {
				return nil, err
			}
			ax, ay := edgeAlongVector(edge)
			// Normal placement is bbox-to-bbox, not pad-center-to-pad-center. A core
			// pad commonly sits inside the package outline; using its center as the
			// gap origin can push the satellite body into the core even when the two
			// copper pads are separated. Along the edge, align the explicitly named
			// pads and apply the declared tangent offset.
			dx := ax * (op.X + ax*a.AlongOffsetMil - rmp.X)
			dy := ay * (op.Y + ay*a.AlongOffsetMil - rmp.Y)
			switch edge {
			case edgeLeft:
				dx = owner.BBox.MinX - gap - rotated.BBox.MaxX
			case edgeRight:
				dx = owner.BBox.MaxX + gap - rotated.BBox.MinX
			case edgeBottom:
				dy = owner.BBox.MinY - gap - rotated.BBox.MaxY
			case edgeTop:
				dy = owner.BBox.MaxY + gap - rotated.BBox.MinY
			}
			projected, err := translateBoardComp(rotated, dx, dy)
			if err != nil {
				return nil, err
			}
			placed[a.MemberRef] = projected
			progress = true
		}
		if !progress {
			return nil, fmt.Errorf("pin assignment dependency cycle or missing owner among: %v", next)
		}
		pending = append([]pcbLayoutPinAssignment(nil), next...)
	}
	return placed, nil
}

func transformBoardComp(c boardComp, pivotX, pivotY, dx, dy, delta float64) (boardComp, error) {
	for _, n := range []float64{pivotX, pivotY, dx, dy, delta} {
		if _, ok := netPathOptionalFinite(n); !ok {
			return boardComp{}, fmt.Errorf("component %s transform must be finite", c.Designator)
		}
	}
	out := c
	transform := func(x, y float64) (float64, float64) {
		rx, ry := rotateVec(x-pivotX, y-pivotY, delta)
		return round4(pivotX + dx + rx), round4(pivotY + dy + ry)
	}
	out.X, out.Y = transform(c.X, c.Y)
	out.Rotation = normalizeDeg(c.Rotation + delta)
	if c.BBox != nil {
		var bb layoutBBox
		bb.MinX, bb.MinY = math.Inf(1), math.Inf(1)
		bb.MaxX, bb.MaxY = math.Inf(-1), math.Inf(-1)
		for _, p := range [][2]float64{{c.BBox.MinX, c.BBox.MinY}, {c.BBox.MaxX, c.BBox.MinY}, {c.BBox.MaxX, c.BBox.MaxY}, {c.BBox.MinX, c.BBox.MaxY}} {
			x, y := transform(p[0], p[1])
			bb.MinX, bb.MinY = math.Min(bb.MinX, x), math.Min(bb.MinY, y)
			bb.MaxX, bb.MaxY = math.Max(bb.MaxX, x), math.Max(bb.MaxY, y)
		}
		out.BBox = &bb
	}
	out.Pads = make([]boardPad, len(c.Pads))
	for i, p := range c.Pads {
		shape, err := projectBoardPadShape(p, transform)
		if err != nil {
			return boardComp{}, fmt.Errorf("component %s pad %s (%s): %w", c.Designator, p.Number, p.ID, err)
		}
		p.Shape = shape
		p.X, p.Y = transform(p.X, p.Y)
		p.Rotation = normalizeDeg(p.Rotation + delta)
		if math.Abs(math.Mod(math.Abs(delta), 180)-90) < 1e-6 {
			p.W, p.H = p.H, p.W
		}
		out.Pads[i] = p
	}
	return out, nil
}

func translateBoardComp(c boardComp, dx, dy float64) (boardComp, error) {
	return transformBoardComp(c, 0, 0, dx, dy, 0)
}

func validatePCBLayoutVariant(v pcbLayoutVariant, original map[string]boardComp, specs map[string]pcbLayoutMemberSpec, memberSet map[string]bool, all map[string]boardComp, snap *boardSnapshot, keepouts []pcbLayoutKeepout, minGap float64, copperPolicy string) []string {
	var reasons []string
	refs := sortedBoardCompKeys(v.comps)
	moved := false
	for _, ref := range refs {
		c, before := v.comps[ref], original[ref]
		moved = moved || poseChanged(before, c)
		ms := specs[ref]
		if before.Locked && poseChanged(before, c) {
			reasons = append(reasons, ref+" is locked")
		}
		fixed := map[string]bool{}
		for _, a := range ms.FixedAxes {
			fixed[a] = true
		}
		if fixed["x"] && !layoutAlmostEqual(c.X, before.X) {
			reasons = append(reasons, ref+" fixes x")
		}
		if fixed["y"] && !layoutAlmostEqual(c.Y, before.Y) {
			reasons = append(reasons, ref+" fixes y")
		}
		if fixed["rotation"] && !sameRotation(c.Rotation, before.Rotation) {
			reasons = append(reasons, ref+" fixes rotation")
		}
		if len(ms.AllowedRotationsDeg) > 0 && !rotationAllowed(c.Rotation, ms.AllowedRotationsDeg) {
			reasons = append(reasons, fmt.Sprintf("%s rotation %.2f is not allowed", ref, c.Rotation))
		}
		if ms.Side == "top" && c.Layer != 1 {
			reasons = append(reasons, ref+" must stay on top")
		}
		if ms.Side == "bottom" && c.Layer != 2 {
			reasons = append(reasons, ref+" must stay on bottom")
		}
		if c.BBox != nil {
			if !bboxInsideBoardOutline(snap.Outline, *c.BBox) {
				reasons = append(reasons, ref+" bbox is outside or crosses the board outline")
			}
			for _, k := range keepouts {
				if keepoutApplies(k, ref, c) && layoutRectGap(*c.BBox, layoutBBox{MinX: k.MinX, MinY: k.MinY, MaxX: k.MaxX, MaxY: k.MaxY}) < minGap {
					reasons = append(reasons, fmt.Sprintf("%s intersects keepout %s", ref, k.ID))
				}
			}
			if strings.TrimSpace(c.ID) == "" {
				reasons = append(reasons, ref+" has no primitiveId")
			}
			if c.BBox == nil {
				reasons = append(reasons, ref+" has no bbox")
			}
		}
	}
	if moved && copperPolicy == "require-board-empty" {
		if snap.RoutedLines == nil {
			reasons = append(reasons, "copperPolicy require-board-empty cannot be proven: routedLines is unknown")
		} else if *snap.RoutedLines > 0 {
			reasons = append(reasons, fmt.Sprintf("copperPolicy require-board-empty rejected a move on a board with %d routed line(s)", *snap.RoutedLines))
		}
	}
	for i := 0; i < len(refs); i++ {
		for j := i + 1; j < len(refs); j++ {
			a, b := v.comps[refs[i]], v.comps[refs[j]]
			if !boardComponentsSharePlacementLayer(a, b) {
				continue
			}
			if a.BBox != nil && b.BBox != nil && layoutRectGap(*a.BBox, *b.BBox) < minGap {
				reasons = append(reasons, fmt.Sprintf("%s and %s are closer than %.2fmil", refs[i], refs[j], minGap))
			}
		}
	}
	for _, ref := range refs {
		c := v.comps[ref]
		if c.BBox == nil {
			continue
		}
		for otherRef, other := range all {
			if memberSet[otherRef] {
				continue
			}
			if !boardComponentsSharePlacementLayer(c, other) {
				continue
			}
			if other.BBox == nil {
				reasons = append(reasons, fmt.Sprintf("obstacle %s shares a placement layer with %s but has no bbox", otherRef, ref))
				continue
			}
			if layoutRectGap(*c.BBox, *other.BBox) < minGap {
				reasons = append(reasons, fmt.Sprintf("%s is closer than %.2fmil to obstacle %s", ref, minGap, otherRef))
			}
		}
	}
	sort.Strings(reasons)
	return uniqueStrings(reasons)
}

func boardComponentsSharePlacementLayer(a, b boardComp) bool {
	if a.Layer == 0 || b.Layer == 0 || a.Layer == b.Layer {
		return true
	}
	return boardCompHasThroughHolePad(a) || boardCompHasThroughHolePad(b)
}

func boardCompHasThroughHolePad(c boardComp) bool {
	for _, p := range c.Pads {
		if p.isThroughHole() {
			return true
		}
	}
	return false
}

func buildPCBLayoutCandidate(in pcbLayoutPlanInput, mod pcbLayoutModuleSpec, v pcbLayoutVariant, base map[string]boardComp, snap *boardSnapshot, memberSet map[string]bool) pcbLayoutCandidate {
	c := pcbLayoutCandidate{Variant: v.label, Module: mod.ID, Strategy: mod.Strategy}
	if in.SchemaVersion >= 3 {
		c.SchemaVersion = in.SchemaVersion
	}
	for _, ref := range sortedBoardCompKeys(v.comps) {
		p := v.comps[ref]
		c.Placements = append(c.Placements, pcbLayoutPlacement{Ref: ref, PrimitiveID: p.ID, XMil: p.X, YMil: p.Y, RotationDeg: p.Rotation, Layer: p.Layer, BBox: p.BBox, Pads: p.Pads})
		if poseChanged(base[ref], p) {
			payload := map[string]any{"primitiveId": p.ID, "patch": map[string]any{"x": p.X, "y": p.Y, "rotation": p.Rotation}}
			c.Actions = append(c.Actions, pcbLayoutTypedAction{ID: "place-" + ref, Action: "pcb.component.modify", Payload: payload})
			c.Apply.Steps = append(c.Apply.Steps, playbookStep{ID: "place-" + ref, Name: "place " + ref, Action: "pcb.component.modify", Payload: payload})
		}
	}
	c.Apply.Version = 1
	c.Apply.Meta = playbookMeta{Project: in.Apply.Project, Window: in.Apply.Window, Doc: in.Apply.Doc, Description: "Generated by easyeda pcb layout-plan; footprint-anchor coordinates; apply only to the recorded board baseline"}
	c.Actions = append(c.Actions, pcbLayoutTypedAction{ID: "save", Action: "pcb.save", Payload: map[string]any{}})
	c.Apply.Steps = append(c.Apply.Steps, playbookStep{ID: "save", Name: "save PCB checkpoint", Action: "pcb.save", Payload: map[string]any{}})
	c.Measurements = measurePCBLayoutCandidate(mod, v, base, snap, memberSet, in.Keepouts)
	return c
}

func measurePCBLayoutCandidate(mod pcbLayoutModuleSpec, v pcbLayoutVariant, base map[string]boardComp, snap *boardSnapshot, memberSet map[string]bool, keepouts []pcbLayoutKeepout) pcbLayoutMeasurements {
	var m pcbLayoutMeasurements
	m.Limitations = append(m.Limitations, snap.Partial...)
	if mod.CopperPolicy == "ignore" {
		switch {
		case snap.RoutedLines == nil:
			m.Limitations = append(m.Limitations, "routed copper is unknown and was not checked")
		case *snap.RoutedLines > 0:
			m.Limitations = append(m.Limitations, fmt.Sprintf("%d routed line(s) exist; copper geometry was not checked", *snap.RoutedLines))
		}
	}
	for _, ref := range sortedBoardCompKeys(v.comps) {
		c := v.comps[ref]
		if c.BBox == nil {
			m.MissingGeometry = append(m.MissingGeometry, ref+":bbox")
			continue
		}
		if m.ModuleBounds == nil {
			bb := *c.BBox
			m.ModuleBounds = &bb
		} else {
			m.ModuleBounds.MinX = math.Min(m.ModuleBounds.MinX, c.BBox.MinX)
			m.ModuleBounds.MinY = math.Min(m.ModuleBounds.MinY, c.BBox.MinY)
			m.ModuleBounds.MaxX = math.Max(m.ModuleBounds.MaxX, c.BBox.MaxX)
			m.ModuleBounds.MaxY = math.Max(m.ModuleBounds.MaxY, c.BBox.MaxY)
		}
		edge, d := edgeDistanceForBBox(snap.Outline, *c.BBox)
		m.EdgeDistances = append(m.EdgeDistances, pcbLayoutEdgeDistance{Ref: ref, NearestEdge: edge, DistanceMil: round4(d), Source: snap.Outline.Source})
		for _, ms := range mod.Members {
			if ms.Ref == ref && ms.LocalFacing != "" {
				actual := facingAfterRotation(ms.LocalFacing, c.Rotation)
				m.Facings = append(m.Facings, pcbLayoutFacingMeasurement{Ref: ref, LocalFacing: ms.LocalFacing, ActualFacing: actual, NearestEdge: edge, OutwardAligned: actual == edge})
				break
			}
		}
	}
	if m.ModuleBounds != nil {
		m.ModuleWidthMil = round4(m.ModuleBounds.MaxX - m.ModuleBounds.MinX)
		m.ModuleHeightMil = round4(m.ModuleBounds.MaxY - m.ModuleBounds.MinY)
	}
	for _, a := range mod.PinAssignments {
		member, mok := v.comps[a.MemberRef]
		owner, ook := v.comps[a.OwnerRef]
		if !ook {
			owner, ook = base[a.OwnerRef]
		}
		mp, mperr := findBoardPadExact(member, a.MemberPad, a.MemberPadPrimitiveID)
		ops, operr := resolveOwnerPads(owner, a)
		if mok && ook && mperr == nil && operr == nil {
			best := math.Inf(1)
			ownerLabel := a.OwnerPad
			if len(a.OwnerPads) > 0 {
				ownerLabel = strings.Join(a.OwnerPads, "|")
			}
			for _, op := range ops {
				best = math.Min(best, math.Hypot(mp.X-op.X, mp.Y-op.Y))
			}
			m.PinDistances = append(m.PinDistances, pcbLayoutPinDistance{Member: a.MemberRef, MemberPad: a.MemberPad, Owner: a.OwnerRef, OwnerPad: ownerLabel, Net: mp.Net, DistanceMil: round4(best), Source: a.Source})
		} else {
			m.MissingGeometry = append(m.MissingGeometry, fmt.Sprintf("pin-assignment:%s.%s->%s.%s", a.MemberRef, a.MemberPad, a.OwnerRef, a.OwnerPad))
		}
	}
	for _, r := range mod.Relations {
		a, ok := v.comps[r.From]
		if !ok {
			a, ok = base[r.From]
		}
		b, bok := v.comps[r.To]
		if !bok {
			b, bok = base[r.To]
		}
		if ok && bok {
			ax, ay := a.center()
			bx, by := b.center()
			m.Relations = append(m.Relations, pcbLayoutRelationMeasurement{Kind: r.Kind, From: r.From, To: r.To, DistanceMil: round4(math.Hypot(ax-bx, ay-by)), Source: r.Source})
		} else {
			m.MissingGeometry = append(m.MissingGeometry, "relation:"+r.From+"->"+r.To)
		}
	}
	bestInternal := math.Inf(1)
	bestComponent := math.Inf(1)
	bestKeepout := math.Inf(1)
	var closestInternal, closestComponent, closestKeepout *pcbLayoutGapMeasurement
	refs := sortedBoardCompKeys(v.comps)
	for i := 0; i < len(refs); i++ {
		for j := i + 1; j < len(refs); j++ {
			a, b := v.comps[refs[i]], v.comps[refs[j]]
			if a.BBox != nil && b.BBox != nil && boardComponentsSharePlacementLayer(a, b) {
				gap := layoutRectGap(*a.BBox, *b.BBox)
				if gap < bestInternal {
					bestInternal = gap
					closestInternal = &pcbLayoutGapMeasurement{From: refs[i], To: refs[j], DistanceMil: round4(gap)}
				}
			}
		}
	}
	for _, memberRef := range refs {
		c := v.comps[memberRef]
		if c.BBox == nil {
			continue
		}
		for _, ref := range sortedBoardCompKeys(base) {
			other := base[ref]
			if memberSet[ref] || !boardComponentsSharePlacementLayer(c, other) {
				continue
			}
			if other.BBox == nil {
				m.MissingGeometry = append(m.MissingGeometry, ref+":bbox")
				continue
			}
			gap := layoutRectGap(*c.BBox, *other.BBox)
			if gap < bestComponent {
				bestComponent = gap
				closestComponent = &pcbLayoutGapMeasurement{From: memberRef, To: ref, DistanceMil: round4(gap)}
			}
		}
		for _, k := range keepouts {
			if keepoutApplies(k, c.Designator, c) {
				gap := layoutRectGap(*c.BBox, layoutBBox{MinX: k.MinX, MinY: k.MinY, MaxX: k.MaxX, MaxY: k.MaxY})
				if gap < bestKeepout {
					bestKeepout = gap
					closestKeepout = &pcbLayoutGapMeasurement{From: memberRef, To: k.ID, DistanceMil: round4(gap)}
				}
			}
		}
	}
	if !math.IsInf(bestInternal, 1) {
		v := round4(bestInternal)
		m.MinInternalGapMil = &v
		m.ClosestInternalGap = closestInternal
	}
	if !math.IsInf(bestComponent, 1) {
		v := round4(bestComponent)
		m.MinExternalComponentGapMil = &v
		m.ClosestExternalComponentGap = closestComponent
	}
	if !math.IsInf(bestKeepout, 1) {
		v := round4(bestKeepout)
		m.MinKeepoutGapMil = &v
		m.ClosestKeepoutGap = closestKeepout
	}
	sort.Strings(m.MissingGeometry)
	m.MissingGeometry = uniqueStrings(m.MissingGeometry)
	sort.Strings(m.Limitations)
	m.Limitations = uniqueStrings(m.Limitations)
	return m
}

func renderPCBLayoutCandidateSVG(w io.Writer, snap *boardSnapshot, cand pcbLayoutCandidate, keepouts []pcbLayoutKeepout) error {
	if snap == nil || snap.Outline == nil {
		return fmt.Errorf("preview requires board outline")
	}
	bb := snap.Outline.BBox
	width, height := 900.0, 520.0
	pad := 35.0
	sx := (width - 2*pad) / (bb.MaxX - bb.MinX)
	sy := (height - 2*pad) / (bb.MaxY - bb.MinY)
	s := math.Min(sx, sy)
	xp := func(x float64) float64 { return pad + (x-bb.MinX)*s }
	yp := func(y float64) float64 { return height - pad - (y-bb.MinY)*s }
	fmt.Fprintf(w, "<svg xmlns=\"http://www.w3.org/2000/svg\" width=\"%.0f\" height=\"%.0f\" viewBox=\"0 0 %.0f %.0f\">\n", width, height, width, height)
	fmt.Fprintln(w, "<defs><pattern id=\"keepout-hatch\" width=\"8\" height=\"8\" patternUnits=\"userSpaceOnUse\" patternTransform=\"rotate(45)\"><line x1=\"0\" y1=\"0\" x2=\"0\" y2=\"8\" stroke=\"#dc2626\" stroke-width=\"2\"/></pattern></defs>")
	fmt.Fprintln(w, "<rect width=\"100%\" height=\"100%\" fill=\"#fbfbfc\"/>")
	if len(snap.Outline.Points) >= 3 {
		fmt.Fprint(w, "<polygon points=\"")
		for _, p := range snap.Outline.Points {
			fmt.Fprintf(w, "%.2f,%.2f ", xp(p[0]), yp(p[1]))
		}
		fmt.Fprintln(w, "\" fill=\"white\" stroke=\"#111827\" stroke-width=\"2\"/>")
	} else {
		fmt.Fprintf(w, "<rect x=\"%.2f\" y=\"%.2f\" width=\"%.2f\" height=\"%.2f\" fill=\"white\" stroke=\"#111827\" stroke-width=\"2\"/>\n", xp(bb.MinX), yp(bb.MaxY), (bb.MaxX-bb.MinX)*s, (bb.MaxY-bb.MinY)*s)
	}
	orderedKeepouts := append([]pcbLayoutKeepout(nil), keepouts...)
	sort.Slice(orderedKeepouts, func(i, j int) bool { return orderedKeepouts[i].ID < orderedKeepouts[j].ID })
	for _, k := range orderedKeepouts {
		fmt.Fprintf(w, "<rect x=\"%.2f\" y=\"%.2f\" width=\"%.2f\" height=\"%.2f\" fill=\"#fecaca\" fill-opacity=\".55\" stroke=\"#dc2626\"/>\n", xp(k.MinX), yp(k.MaxY), (k.MaxX-k.MinX)*s, (k.MaxY-k.MinY)*s)
	}
	selected := map[string]pcbLayoutPlacement{}
	for _, p := range cand.Placements {
		selected[p.Ref] = p
	}
	orderedComponents := append([]boardComp(nil), snap.Components...)
	sort.Slice(orderedComponents, func(i, j int) bool { return orderedComponents[i].Designator < orderedComponents[j].Designator })
	for _, c := range orderedComponents {
		if _, ok := selected[c.Designator]; ok || c.BBox == nil {
			continue
		}
		fmt.Fprintf(w, "<rect x=\"%.2f\" y=\"%.2f\" width=\"%.2f\" height=\"%.2f\" fill=\"#e5e7eb\" stroke=\"#9ca3af\" stroke-width=\".6\"/>\n", xp(c.BBox.MinX), yp(c.BBox.MaxY), (c.BBox.MaxX-c.BBox.MinX)*s, (c.BBox.MaxY-c.BBox.MinY)*s)
	}
	for _, p := range cand.Placements {
		if p.BBox == nil {
			continue
		}
		fmt.Fprintf(w, "<rect x=\"%.2f\" y=\"%.2f\" width=\"%.2f\" height=\"%.2f\" rx=\"2\" fill=\"#bfdbfe\" stroke=\"#2563eb\" stroke-width=\"1.4\"/>\n", xp(p.BBox.MinX), yp(p.BBox.MaxY), (p.BBox.MaxX-p.BBox.MinX)*s, (p.BBox.MaxY-p.BBox.MinY)*s)
		cx, cy := (p.BBox.MinX+p.BBox.MaxX)/2, (p.BBox.MinY+p.BBox.MaxY)/2
		fmt.Fprintf(w, "<text x=\"%.2f\" y=\"%.2f\" font-family=\"Arial,sans-serif\" font-size=\"11\" text-anchor=\"middle\" dominant-baseline=\"middle\" fill=\"#1e3a8a\">%s</text>\n", xp(cx), yp(cy), html.EscapeString(p.Ref))
	}
	if cand.Bundle != nil {
		renderPCBLayoutBundleSVG(w, *cand.Bundle, xp, yp, s)
	}
	renderPCBLayoutReservationsSVG(w, cand, xp, yp, s)
	fmt.Fprintf(w, "<text x=\"35\" y=\"22\" font-family=\"Arial,sans-serif\" font-size=\"14\" fill=\"#111827\">%s · %s</text>\n", html.EscapeString(cand.Module), html.EscapeString(cand.Variant))
	_, err := fmt.Fprintln(w, "</svg>")
	return err
}

// renderPCBLayoutModuleSVG uses the same candidate.Bundle geometry that feeds
// the apply playbook.  local focuses on the assembled module; compare overlays
// the original member poses and the planned poses over the complete board.
func renderPCBLayoutModuleSVG(w io.Writer, snap *boardSnapshot, cand pcbLayoutCandidate, mode string) error {
	if snap == nil || snap.Outline == nil || cand.Bundle == nil {
		return fmt.Errorf("module preview requires board outline and a schema-v2 bundle")
	}
	view := snap.Outline.BBox
	if mode == "local" {
		if cand.Bundle.Envelope == nil {
			return fmt.Errorf("local module preview requires a measured envelope")
		}
		view = expandLayoutBBox(*cand.Bundle.Envelope, 35)
		routes := pcbBundleAllRoutes(cand.Bundle)
		if cand.Escape != nil {
			routes = append(routes, cand.Escape.Routes...)
		}
		for _, p := range cand.Placements {
			if p.BBox != nil {
				view = *unionLayoutBBox(&view, p.BBox)
			}
		}
		for _, route := range routes {
			for _, p := range route.Points {
				view.MinX, view.MinY = math.Min(view.MinX, p[0]-20), math.Min(view.MinY, p[1]-20)
				view.MaxX, view.MaxY = math.Max(view.MaxX, p[0]+20), math.Max(view.MaxY, p[1]+20)
			}
		}
	}
	if view.MaxX <= view.MinX || view.MaxY <= view.MinY {
		return fmt.Errorf("module preview has an invalid view envelope")
	}
	width, height, pad := 960.0, 640.0, 42.0
	s := math.Min((width-2*pad)/(view.MaxX-view.MinX), (height-2*pad)/(view.MaxY-view.MinY))
	xp := func(x float64) float64 { return pad + (x-view.MinX)*s }
	yp := func(y float64) float64 { return height - pad - (y-view.MinY)*s }
	fmt.Fprintf(w, "<svg xmlns=\"http://www.w3.org/2000/svg\" width=\"%.0f\" height=\"%.0f\" viewBox=\"0 0 %.0f %.0f\">\n", width, height, width, height)
	fmt.Fprintln(w, "<defs><pattern id=\"keepout-hatch\" width=\"8\" height=\"8\" patternUnits=\"userSpaceOnUse\" patternTransform=\"rotate(45)\"><line x1=\"0\" y1=\"0\" x2=\"0\" y2=\"8\" stroke=\"#dc2626\" stroke-width=\"2\"/></pattern></defs>")
	fmt.Fprintln(w, "<rect width=\"100%\" height=\"100%\" fill=\"#f8fafc\"/>")
	if mode == "compare" {
		if len(snap.Outline.Points) >= 3 {
			renderPCBPolygonSVG(w, snap.Outline.Points, xp, yp, "white", "#111827", 2, 1)
		} else {
			fmt.Fprintf(w, "<rect x=\"%.2f\" y=\"%.2f\" width=\"%.2f\" height=\"%.2f\" fill=\"white\" stroke=\"#111827\" stroke-width=\"2\"/>\n", xp(view.MinX), yp(view.MaxY), (view.MaxX-view.MinX)*s, (view.MaxY-view.MinY)*s)
		}
	}
	owned := map[string]bool{}
	for _, p := range cand.Placements {
		owned[p.Ref] = true
	}
	// Existing nearby components show the real available channel.  Original
	// module poses are red/dashed in compare mode and omitted from local mode.
	ordered := append([]boardComp(nil), snap.Components...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Designator < ordered[j].Designator })
	for _, c := range ordered {
		if c.BBox == nil || (!layoutBBoxesIntersect(*c.BBox, view) && mode == "local") {
			continue
		}
		if owned[c.Designator] {
			if mode == "compare" {
				fmt.Fprintf(w, "<rect x=\"%.2f\" y=\"%.2f\" width=\"%.2f\" height=\"%.2f\" fill=\"none\" stroke=\"#dc2626\" stroke-width=\"1.5\" stroke-dasharray=\"6 4\"/>\n", xp(c.BBox.MinX), yp(c.BBox.MaxY), (c.BBox.MaxX-c.BBox.MinX)*s, (c.BBox.MaxY-c.BBox.MinY)*s)
			}
			continue
		}
		fill, stroke := "#e5e7eb", "#9ca3af"
		if mode == "local" {
			fill, stroke = "#f1f5f9", "#94a3b8"
		}
		fmt.Fprintf(w, "<rect x=\"%.2f\" y=\"%.2f\" width=\"%.2f\" height=\"%.2f\" fill=\"%s\" stroke=\"%s\" stroke-width=\".8\"/>\n", xp(c.BBox.MinX), yp(c.BBox.MaxY), (c.BBox.MaxX-c.BBox.MinX)*s, (c.BBox.MaxY-c.BBox.MinY)*s, fill, stroke)
	}
	for _, p := range cand.Placements {
		if p.BBox == nil {
			continue
		}
		fmt.Fprintf(w, "<rect x=\"%.2f\" y=\"%.2f\" width=\"%.2f\" height=\"%.2f\" rx=\"3\" fill=\"#bfdbfe\" fill-opacity=\".85\" stroke=\"#2563eb\" stroke-width=\"2\"/>\n", xp(p.BBox.MinX), yp(p.BBox.MaxY), (p.BBox.MaxX-p.BBox.MinX)*s, (p.BBox.MaxY-p.BBox.MinY)*s)
		fmt.Fprintf(w, "<text x=\"%.2f\" y=\"%.2f\" font-family=\"Arial,sans-serif\" font-size=\"12\" text-anchor=\"middle\" dominant-baseline=\"middle\" fill=\"#1e3a8a\">%s</text>\n", xp((p.BBox.MinX+p.BBox.MaxX)/2), yp((p.BBox.MinY+p.BBox.MaxY)/2), html.EscapeString(p.Ref))
	}
	renderPCBLayoutBundleSVG(w, *cand.Bundle, xp, yp, s)
	renderPCBLayoutReservationsSVG(w, cand, xp, yp, s)
	title := cand.Module + " · " + cand.Variant + " · " + mode
	fmt.Fprintf(w, "<rect x=\"20\" y=\"12\" width=\"%.0f\" height=\"28\" rx=\"5\" fill=\"white\" fill-opacity=\".9\"/>\n", math.Min(width-40, float64(len(title))*8+32))
	fmt.Fprintf(w, "<text x=\"30\" y=\"31\" font-family=\"Arial,sans-serif\" font-size=\"14\" fill=\"#111827\">%s</text>\n", html.EscapeString(title))
	_, err := fmt.Fprintln(w, "</svg>")
	return err
}

func renderPCBLayoutBundleSVG(w io.Writer, bundle pcbLayoutModuleBundle, xp, yp func(float64) float64, scale float64) {
	for _, route := range bundle.ReflowRoutes {
		renderPCBPolylineSVG(w, route.Points, xp, yp, "#db2777", math.Max(1.4, route.WidthMil*scale), .85)
	}
	for _, pour := range bundle.Pours {
		fill := "#bbf7d0"
		if pour.Layer == 2 {
			fill = "#bae6fd"
		}
		renderPCBPolygonSVG(w, pour.Points, xp, yp, fill, "#0284c7", 1, .16)
	}
	for _, region := range bundle.Regions {
		renderPCBPolygonSVG(w, region.Points, xp, yp, "url(#keepout-hatch)", "#dc2626", 1.2, .28)
	}
	for _, route := range bundle.GroundRoutes {
		renderPCBPolylineSVG(w, route.Points, xp, yp, "#16a34a", math.Max(1.4, route.WidthMil*scale), .82)
	}
	for _, route := range bundle.SignalRoutes {
		color := "#f97316"
		if strings.Contains(strings.ToUpper(route.Net), "OUT") {
			color = "#7c3aed"
		}
		renderPCBPolylineSVG(w, route.Points, xp, yp, color, math.Max(1.6, route.WidthMil*scale), .95)
	}
	for _, via := range bundle.Vias {
		r := math.Max(2.3, via.DiameterMil*scale/2)
		fmt.Fprintf(w, "<circle cx=\"%.2f\" cy=\"%.2f\" r=\"%.2f\" fill=\"#22c55e\" stroke=\"#166534\" stroke-width=\"1.2\"/><circle cx=\"%.2f\" cy=\"%.2f\" r=\"%.2f\" fill=\"#f8fafc\"/>\n", xp(via.X), yp(via.Y), r, xp(via.X), yp(via.Y), math.Max(.9, via.HoleMil*scale/2))
	}
}

func renderPCBLayoutReservationsSVG(w io.Writer, c pcbLayoutCandidate, xp, yp func(float64) float64, scale float64) {
	if c.Escape == nil {
		return
	}
	fmt.Fprintln(w, "<g stroke-dasharray=\"5 3\"><title>Reserved escape paths: planning geometry, not editor copper</title>")
	for _, r := range c.Escape.Routes {
		fmt.Fprintf(w, "<g><title>%s %s → %s (layer %d)</title>", html.EscapeString(r.Net), html.EscapeString(r.From), html.EscapeString(r.To), r.Layer)
		renderPCBPolylineSVG(w, r.Points, xp, yp, "#0891b2", math.Max(1.3, r.WidthMil*scale), .85)
		fmt.Fprintln(w, "</g>")
	}
	fmt.Fprintln(w, "</g>")
}

func renderPCBPolygonSVG(w io.Writer, points [][2]float64, xp, yp func(float64) float64, fill, stroke string, strokeWidth, opacity float64) {
	if len(points) < 3 {
		return
	}
	fmt.Fprint(w, "<polygon points=\"")
	for _, p := range points {
		fmt.Fprintf(w, "%.2f,%.2f ", xp(p[0]), yp(p[1]))
	}
	fmt.Fprintf(w, "\" fill=\"%s\" fill-opacity=\"%.2f\" stroke=\"%s\" stroke-width=\"%.2f\"/>\n", fill, opacity, stroke, strokeWidth)
}

func renderPCBPolylineSVG(w io.Writer, points [][2]float64, xp, yp func(float64) float64, stroke string, width, opacity float64) {
	if len(points) < 2 {
		return
	}
	fmt.Fprint(w, "<polyline points=\"")
	for _, p := range points {
		fmt.Fprintf(w, "%.2f,%.2f ", xp(p[0]), yp(p[1]))
	}
	fmt.Fprintf(w, "\" fill=\"none\" stroke=\"%s\" stroke-width=\"%.2f\" stroke-linecap=\"round\" stroke-linejoin=\"round\" opacity=\"%.2f\"/>\n", stroke, width, opacity)
}

func layoutBBoxesIntersect(a, b layoutBBox) bool {
	return a.MaxX >= b.MinX && b.MaxX >= a.MinX && a.MaxY >= b.MinY && b.MaxY >= a.MinY
}

func boardCompToAP(c boardComp) apComp {
	a := apComp{id: c.ID, designator: c.Designator, x: c.X, y: c.Y, rotation: c.Rotation, locked: c.Locked, pads: make([]apPad, 0, len(c.Pads))}
	if c.BBox != nil {
		a.hasBBox = true
		a.minX = c.BBox.MinX
		a.minY = c.BBox.MinY
		a.maxX = c.BBox.MaxX
		a.maxY = c.BBox.MaxY
	}
	for _, p := range c.Pads {
		a.pads = append(a.pads, boardPadToAP(p))
	}
	return a
}

func boardPadToAP(p boardPad) apPad {
	return apPad{num: p.Number, net: p.Net, x: p.X, y: p.Y, layer: p.Layer, w: p.W, h: p.H}
}

func findBoardPadExact(c boardComp, number, primitiveID string) (boardPad, error) {
	var matches []boardPad
	for _, p := range c.Pads {
		if p.Number != number {
			continue
		}
		if primitiveID != "" && p.ID != primitiveID {
			continue
		}
		matches = append(matches, p)
	}
	if len(matches) == 0 {
		return boardPad{}, fmt.Errorf("not found")
	}
	if len(matches) > 1 {
		return boardPad{}, fmt.Errorf("ambiguous (%d pads share this number); provide pad primitiveId", len(matches))
	}
	return matches[0], nil
}

func resolveOwnerPads(c boardComp, a pcbLayoutPinAssignment) ([]boardPad, error) {
	if a.OwnerPad != "" && len(a.OwnerPads) > 0 {
		return nil, fmt.Errorf("use ownerPad or ownerPads, not both")
	}
	if a.OwnerPad != "" {
		p, err := findBoardPadExact(c, a.OwnerPad, a.OwnerPadPrimitiveID)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", a.OwnerPad, err)
		}
		return []boardPad{p}, nil
	}
	if len(a.OwnerPads) == 0 {
		return nil, fmt.Errorf("no owner pad declared")
	}
	if len(a.OwnerPadPrimitiveIDs) > 0 && len(a.OwnerPadPrimitiveIDs) != len(a.OwnerPads) {
		return nil, fmt.Errorf("ownerPadPrimitiveIds length must match ownerPads")
	}
	out := make([]boardPad, 0, len(a.OwnerPads))
	for i, n := range a.OwnerPads {
		id := ""
		if len(a.OwnerPadPrimitiveIDs) > 0 {
			id = a.OwnerPadPrimitiveIDs[i]
		}
		p, err := findBoardPadExact(c, n, id)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", n, err)
		}
		out = append(out, p)
	}
	return out, nil
}

func choosePinSatelliteRotation(c boardComp, pad, padID string, edge apEdge, allowed []float64) (float64, error) {
	if len(allowed) == 0 {
		return c.Rotation, nil
	}
	ux, uy := edgeOutwardVector(edge)
	best, bestDot := allowed[0], math.Inf(-1)
	for _, rot := range allowed {
		t, err := transformBoardComp(c, c.X, c.Y, 0, 0, rotationDelta(c.Rotation, rot))
		if err != nil {
			return 0, err
		}
		p, err := findBoardPadExact(t, pad, padID)
		if err != nil {
			continue
		}
		cx, cy := t.center()
		if d := (cx-p.X)*ux + (cy-p.Y)*uy; d > bestDot {
			bestDot, best = d, rot
		}
	}
	return normalizeDeg(best), nil
}

func edgeOutwardVector(e apEdge) (float64, float64) {
	switch e {
	case edgeLeft:
		return -1, 0
	case edgeRight:
		return 1, 0
	case edgeBottom:
		return 0, -1
	default:
		return 0, 1
	}
}
func edgeAlongVector(e apEdge) (float64, float64) {
	if e.vertical() {
		return 0, 1
	}
	return 1, 0
}

func facingAfterRotation(local string, rotation float64) string {
	order := []string{"right", "top", "left", "bottom"}
	idx := 0
	for i, name := range order {
		if name == strings.ToLower(local) {
			idx = i
			break
		}
	}
	steps := int(math.Round(normalizeDeg(rotation)/90)) % 4
	return order[(idx+steps)%4]
}

func keepoutApplies(k pcbLayoutKeepout, ref string, c boardComp) bool {
	for _, except := range k.ExceptRefs {
		if except == ref {
			return false
		}
	}
	if len(k.Layers) == 0 {
		return true
	}
	for _, side := range k.Layers {
		switch strings.ToLower(side) {
		case "all":
			return true
		case "top":
			if c.Layer == 0 || c.Layer == 1 || boardCompHasThroughHolePad(c) {
				return true
			}
		case "bottom":
			if c.Layer == 0 || c.Layer == 2 || boardCompHasThroughHolePad(c) {
				return true
			}
		}
	}
	return false
}

func parseLayoutEdge(s string) (apEdge, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "left":
		return edgeLeft, true
	case "right":
		return edgeRight, true
	case "bottom":
		return edgeBottom, true
	case "top":
		return edgeTop, true
	}
	return edgeLeft, false
}

func outlineCenterlineBBox(o *boardOutline) layoutBBox {
	if o == nil || len(o.Points) < 3 {
		if o == nil {
			return layoutBBox{}
		}
		return o.BBox
	}
	bb := layoutBBox{MinX: math.Inf(1), MinY: math.Inf(1), MaxX: math.Inf(-1), MaxY: math.Inf(-1)}
	for _, p := range o.Points {
		bb.MinX, bb.MinY = math.Min(bb.MinX, p[0]), math.Min(bb.MinY, p[1])
		bb.MaxX, bb.MaxY = math.Max(bb.MaxX, p[0]), math.Max(bb.MaxY, p[1])
	}
	return bb
}

func outlineDistanceMethod(o *boardOutline) string {
	if o != nil && len(o.Points) >= 3 {
		return "polygon-centerline"
	}
	return "outline-aabb"
}

func bboxInsideBoardOutline(o *boardOutline, b layoutBBox) bool {
	if o == nil {
		return false
	}
	rect := [][2]float64{{b.MinX, b.MinY}, {b.MaxX, b.MinY}, {b.MaxX, b.MaxY}, {b.MinX, b.MaxY}}
	for _, p := range rect {
		if !o.containsPoint(p[0], p[1]) {
			return false
		}
	}
	if len(o.Points) < 3 {
		return true
	}
	// Corners alone are insufficient on a concave outline: a bbox edge can leave
	// the board through a notch and re-enter. Any interior intersection with the
	// outline therefore rejects the candidate. A tiny epsilon keeps near misses
	// deterministic while edge-clearance=0 remains conservatively unsupported.
	for i := range rect {
		a, z := rect[i], rect[(i+1)%len(rect)]
		for j := range o.Points {
			p, q := o.Points[j], o.Points[(j+1)%len(o.Points)]
			if segSegDist(a[0], a[1], z[0], z[1], p[0], p[1], q[0], q[1]) <= 1e-7 {
				return false
			}
		}
	}
	return true
}

func edgeDistanceForBBox(o *boardOutline, b layoutBBox) (string, float64) {
	ob := outlineCenterlineBBox(o)
	vals := []struct {
		n string
		d float64
	}{{"left", b.MinX - ob.MinX}, {"right", ob.MaxX - b.MaxX}, {"bottom", b.MinY - ob.MinY}, {"top", ob.MaxY - b.MaxY}}
	best := vals[0]
	for _, v := range vals[1:] {
		if v.d < best.d {
			best = v
		}
	}
	if o == nil || len(o.Points) < 3 {
		return best.n, best.d
	}
	rect := [][2]float64{{b.MinX, b.MinY}, {b.MaxX, b.MinY}, {b.MaxX, b.MaxY}, {b.MinX, b.MaxY}}
	d := math.Inf(1)
	for i := range rect {
		a, z := rect[i], rect[(i+1)%len(rect)]
		for j := range o.Points {
			p, q := o.Points[j], o.Points[(j+1)%len(o.Points)]
			d = math.Min(d, segSegDist(a[0], a[1], z[0], z[1], p[0], p[1], q[0], q[1]))
		}
	}
	for _, p := range rect {
		if !o.containsPoint(p[0], p[1]) {
			return best.n, -d
		}
	}
	return best.n, d
}

func layoutRectGap(a, b layoutBBox) float64 {
	dx := math.Max(math.Max(b.MinX-a.MaxX, a.MinX-b.MaxX), 0)
	dy := math.Max(math.Max(b.MinY-a.MaxY, a.MinY-b.MaxY), 0)
	if dx > 0 || dy > 0 {
		return math.Hypot(dx, dy)
	}
	px := math.Min(a.MaxX, b.MaxX) - math.Max(a.MinX, b.MinX)
	py := math.Min(a.MaxY, b.MaxY) - math.Max(a.MinY, b.MinY)
	return -math.Min(px, py)
}

func pcbLayoutVariantSignature(v pcbLayoutVariant) string {
	var b strings.Builder
	for _, ref := range sortedBoardCompKeys(v.comps) {
		c := v.comps[ref]
		fmt.Fprintf(&b, "%s:%.4f,%.4f,%.4f;", ref, c.X, c.Y, normalizeDeg(c.Rotation))
	}
	return b.String()
}

func sortedBoardCompKeys(m map[string]boardComp) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func poseChanged(a, b boardComp) bool {
	return !layoutAlmostEqual(a.X, b.X) || !layoutAlmostEqual(a.Y, b.Y) || !sameRotation(a.Rotation, b.Rotation)
}
func layoutAlmostEqual(a, b float64) bool { return math.Abs(a-b) < 1e-4 }
func sameRotation(a, b float64) bool      { return math.Abs(rotationDelta(a, b)) < 1e-4 }
func rotationDelta(from, to float64) float64 {
	d := normalizeDeg(to) - normalizeDeg(from)
	if d > 180 {
		d -= 360
	}
	if d <= -180 {
		d += 360
	}
	return d
}
func normalizeDeg(d float64) float64 {
	d = math.Mod(d, 360)
	if d < 0 {
		d += 360
	}
	if math.Abs(d-360) < 1e-7 {
		return 0
	}
	return round4(d)
}
func rotationAllowed(v float64, allowed []float64) bool {
	for _, a := range allowed {
		if sameRotation(v, a) {
			return true
		}
	}
	return false
}
func round4(v float64) float64 { return math.Round(v*10000) / 10000 }
func isFinite(v float64) bool  { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func allFinite(values ...float64) bool {
	for _, v := range values {
		if !isFinite(v) {
			return false
		}
	}
	return true
}
func isQuarterTurn(v float64) bool {
	if !isFinite(v) {
		return false
	}
	return layoutAlmostEqual(v, 0) || layoutAlmostEqual(v, 90) || layoutAlmostEqual(v, 180) || layoutAlmostEqual(v, 270)
}
func isNormalizedQuarterTurn(v float64) bool {
	if !isFinite(v) {
		return false
	}
	n := normalizeDeg(v)
	return layoutAlmostEqual(n, 0) || layoutAlmostEqual(n, 90) || layoutAlmostEqual(n, 180) || layoutAlmostEqual(n, 270)
}
func uniqueStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := in[:0]
	var prev string
	for i, s := range in {
		if i == 0 || s != prev {
			out = append(out, s)
			prev = s
		}
	}
	return out
}
