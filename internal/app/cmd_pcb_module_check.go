package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

type pcbModuleCheckReport struct {
	Status       string   `json:"status"`
	Module       string   `json:"module"`
	Candidate    string   `json:"candidate"`
	Checks       []string `json:"checks"`
	Findings     []string `json:"findings,omitempty"`
	Limitations  []string `json:"limitations,omitempty"`
	BeforeSHA256 string   `json:"beforeSemanticSha256"`
	AfterSHA256  string   `json:"afterSemanticSha256"`
	Summary      string   `json:"summary"`
}

type pcbModuleJournalProof struct {
	CapturedByStep map[string]string
	ModulePourIDs  map[string]bool
}

// pcb dump assigns the user-facing project label after fetchBoardSnapshot has
// calculated SemanticSHA256. The label is capture metadata, not board geometry,
// so module-check must use the same semantic projection when it recomputes a
// persisted dump.
func moduleCheckSemanticSHA256(s *boardSnapshot) (string, error) {
	if s == nil {
		return "", fmt.Errorf("nil board snapshot")
	}
	clone := *s
	clone.Project = ""
	return boardSnapshotSemanticSHA256(&clone)
}

func newPcbModuleCheckCmd(stdout, stderr io.Writer) *cobra.Command {
	var candidatePath, beforePath, afterPath, journalPath, outPath, requirementsPath string
	c := &cobra.Command{
		Use:   "module-check",
		Short: "Verify a routed module against fresh before/after copper snapshots and apply journal",
		Long: `Verify one schema-v2 layout-plan candidate without opening the editor.

The candidate is checked against its original semantic board hash, the exact
apply journal, and a fresh 'pcb dump --include-copper'. It verifies component
poses, generated tracks/vias/regions/pours, materialized poured copper, keepout
emptiness, and preservation of every non-owned baseline object. Missing geometry
or an unreadable copper category is incomplete, never an empty/pass result.`,
		Example: `  easyeda pcb module-check --candidate candidates/candidate-01.json \
    --before before.json --after after.json \
    --journal candidates/candidate-01.apply.json.journal.jsonl --out check.json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			for name, value := range map[string]string{"candidate": candidatePath, "before": beforePath, "after": afterPath, "journal": journalPath} {
				if value == "" {
					return fmt.Errorf("--%s is required", name)
				}
			}
			candidate, err := loadPCBLayoutCandidate(candidatePath)
			if err != nil {
				return err
			}
			before, err := loadBoardSnapshotPath(beforePath)
			if err != nil {
				return fmt.Errorf("load --before: %w", err)
			}
			after, err := loadBoardSnapshotPath(afterPath)
			if err != nil {
				return fmt.Errorf("load --after: %w", err)
			}
			var requirements *pcbLayoutPlanInput
			if requirementsPath != "" {
				data, err := os.ReadFile(requirementsPath)
				if err != nil {
					return fmt.Errorf("read independent requirements: %w", err)
				}
				parsed, err := decodePCBLayoutPlanInput(data)
				if err != nil {
					return err
				}
				requirements = &parsed
			}
			rep := checkPCBModule(candidate, before, after, journalPath, requirements)
			raw, err := json.MarshalIndent(rep, "", "  ")
			if err != nil {
				return err
			}
			raw = append(raw, '\n')
			if outPath != "" {
				if err := os.WriteFile(outPath, raw, 0o644); err != nil {
					return err
				}
				fmt.Fprintf(stderr, "module-check: %s → %s\n", rep.Summary, outPath)
			} else if _, err := stdout.Write(raw); err != nil {
				return err
			}
			if rep.Status != "pass" {
				return fmt.Errorf("pcb module-check: %s", rep.Summary)
			}
			return nil
		},
	}
	c.Flags().StringVar(&candidatePath, "candidate", "", "layout-plan candidate JSON (required)")
	c.Flags().StringVar(&beforePath, "before", "", "baseline pcb dump --include-copper JSON (required)")
	c.Flags().StringVar(&afterPath, "after", "", "fresh post-reload pcb dump --include-copper JSON (required)")
	c.Flags().StringVar(&journalPath, "journal", "", "apply journal JSONL for the candidate playbook (required)")
	c.Flags().StringVar(&outPath, "out", "", "write the report to this file (default stdout)")
	c.Flags().StringVar(&requirementsPath, "requirements", "", "independent schema-v3 layout/routing requirements (required for escape/reflow proof)")
	return c
}

func loadPCBLayoutCandidate(path string) (pcbLayoutCandidate, error) {
	var candidate pcbLayoutCandidate
	f, err := os.Open(path)
	if err != nil {
		return candidate, err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&candidate); err != nil {
		return candidate, fmt.Errorf("decode candidate: %w", err)
	}
	if candidate.Bundle == nil || candidate.Bundle.Kind == "" {
		return candidate, fmt.Errorf("candidate has no schema-v2 module bundle")
	}
	return candidate, nil
}

func loadBoardSnapshotPath(path string) (*boardSnapshot, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return loadBoardSnapshotFile(f)
}

func checkPCBModule(candidate pcbLayoutCandidate, before, after *boardSnapshot, journalPath string, requirements ...*pcbLayoutPlanInput) pcbModuleCheckReport {
	rep := pcbModuleCheckReport{Status: "pass", Module: candidate.Module, Candidate: candidate.ID}
	addFail := func(s string) { rep.Findings = append(rep.Findings, s); rep.Status = "fail" }
	addUnknown := func(s string) {
		rep.Limitations = append(rep.Limitations, s)
		if rep.Status == "pass" {
			rep.Status = "incomplete"
		}
	}
	if before == nil || after == nil {
		addUnknown("before/after snapshot is nil")
		return finishPCBModuleCheck(rep)
	}
	beforeHash, err := moduleCheckSemanticSHA256(before)
	if err != nil {
		addUnknown("before semantic hash: " + err.Error())
	} else {
		rep.BeforeSHA256 = beforeHash
		if candidate.BoardSemanticSHA256 == "" || beforeHash != candidate.BoardSemanticSHA256 {
			addFail(fmt.Sprintf("candidate baseline semantic hash differs: candidate=%s before=%s", candidate.BoardSemanticSHA256, beforeHash))
		} else {
			rep.Checks = append(rep.Checks, "baseline semantic hash matches")
		}
	}
	if afterHash, err := moduleCheckSemanticSHA256(after); err != nil {
		addUnknown("after semantic hash: " + err.Error())
	} else {
		rep.AfterSHA256 = afterHash
	}
	if err := verifyModuleSnapshotContext(before, after); err != nil {
		addFail(err.Error())
	} else {
		rep.Checks = append(rep.Checks, "fresh rules and board outline match the planned baseline")
	}
	for label, snap := range map[string]*boardSnapshot{"before": before, "after": after} {
		if snap.Copper == nil {
			addUnknown(label + " snapshot has no copper block")
			continue
		}
		for _, category := range []string{"routing", "vias", "pours", "poured", "regions", "fills"} {
			if snap.Copper.Availability[category] != "available" {
				addUnknown(fmt.Sprintf("%s copper category %s is %q", label, category, snap.Copper.Availability[category]))
			}
		}
	}
	journalProof, err := verifyModuleJournal(candidate, before, after, journalPath)
	if err != nil {
		addFail(err.Error())
	} else {
		rep.Checks = append(rep.Checks, "apply journal matches every playbook step exactly and every captured PID matches one fresh object")
	}
	if err := verifyModuleComponents(candidate, before, after); err != nil {
		addFail(err.Error())
	} else {
		rep.Checks = append(rep.Checks, "member poses match and non-member components are unchanged")
	}
	if err := verifyCrystalFencePitch(candidate); err != nil {
		addFail(err.Error())
	}
	if before.Copper != nil && after.Copper != nil {
		if err := verifyModuleCopper(candidate, before, after, journalProof); err != nil {
			addFail(err.Error())
		} else {
			rep.Checks = append(rep.Checks, "tracks, arcs, vias, regions, pours, materialized pours and non-owned copper match")
		}
		if signalChecks, err := verifyCrystalSignalConnectivity(candidate, after); err != nil {
			addFail(err.Error())
		} else {
			rep.Checks = append(rep.Checks, signalChecks...)
		}
		if err := verifyCrystalGroundConnectivity(candidate, after); err != nil {
			addFail(err.Error())
		} else {
			groundCheck := "owned GND pads, guard, two-layer via path and MCU ground anchor are connected by listed copper"
			if candidate.Bundle != nil && candidate.Bundle.GroundImplementation != "tracks-vias" {
				groundCheck += "; materialized pours touch module GND vias"
			}
			rep.Checks = append(rep.Checks, groundCheck)
		}
		if err := verifyCrystalLiveClearance(candidate, after); err != nil {
			addFail(err.Error())
		} else if candidate.Bundle != nil && candidate.Bundle.Kind == "crystal-guard" {
			rep.Checks = append(rep.Checks, "fresh component and copper geometry satisfies the live clearance and board-edge rules")
		}
		if err := verifyCrystalSensitiveCoverage(candidate, after); err != nil {
			addFail(err.Error())
		} else if candidate.Bundle != nil && candidate.Bundle.Kind == "crystal-guard" {
			rep.Checks = append(rep.Checks, "fresh TOP/BOTTOM no-pours geometry covers every owned member and the complete OSC copper stroke")
		}
		if err := verifyModuleKeepoutEmpty(candidate, after); err != nil {
			addFail(err.Error())
		} else {
			rep.Checks = append(rep.Checks, "no-pours envelopes contain no materialized or static copper")
		}
	}
	var independent *pcbLayoutPlanInput
	if len(requirements) > 0 {
		independent = requirements[0]
	}
	return checkPCBModuleRouting(candidate, independent, before, after, rep)
}

func verifyModuleSnapshotContext(before, after *boardSnapshot) error {
	if before == nil || after == nil || before.Rules == nil || after.Rules == nil {
		return fmt.Errorf("fresh before/after board rules are unavailable")
	}
	br, ar := before.Rules, after.Rules
	if !allFinite(br.ClearanceMil, br.ClearanceTrackTrackMil, br.TrackWidthMil, br.PowerWidthMil, br.TrackWidthMinMil, br.ViaDrillMil, br.ViaDiameterMil, br.CopperToEdgeMil) ||
		!allFinite(ar.ClearanceMil, ar.ClearanceTrackTrackMil, ar.TrackWidthMil, ar.PowerWidthMil, ar.TrackWidthMinMil, ar.ViaDrillMil, ar.ViaDiameterMil, ar.CopperToEdgeMil) {
		return fmt.Errorf("fresh before/after board rules contain non-finite values")
	}
	if br.Source != ar.Source || math.Abs(br.ClearanceMil-ar.ClearanceMil) > netPathGeomEps ||
		math.Abs(br.ClearanceTrackTrackMil-ar.ClearanceTrackTrackMil) > netPathGeomEps ||
		math.Abs(br.TrackWidthMil-ar.TrackWidthMil) > netPathGeomEps ||
		math.Abs(br.PowerWidthMil-ar.PowerWidthMil) > netPathGeomEps ||
		math.Abs(br.TrackWidthMinMil-ar.TrackWidthMinMil) > netPathGeomEps ||
		math.Abs(br.ViaDrillMil-ar.ViaDrillMil) > netPathGeomEps ||
		math.Abs(br.ViaDiameterMil-ar.ViaDiameterMil) > netPathGeomEps ||
		math.Abs(br.CopperToEdgeMil-ar.CopperToEdgeMil) > netPathGeomEps {
		return fmt.Errorf("fresh after board rules differ from the planned baseline")
	}
	if before.Outline == nil || after.Outline == nil {
		return fmt.Errorf("fresh before/after board outline is unavailable")
	}
	bo, ao := before.Outline, after.Outline
	if bo.Source != ao.Source || bo.Format != ao.Format || !sameBBox(bo.BBox, ao.BBox) {
		return fmt.Errorf("fresh after board outline metadata/bounds differ from the planned baseline")
	}
	if (len(bo.Points) < 3) != (len(ao.Points) < 3) {
		return fmt.Errorf("fresh after board outline polygon availability differs from the planned baseline")
	}
	if len(bo.Points) >= 3 {
		beforeShape := canonicalPolygonContours([][][2]float64{bo.Points}, false)
		beforeReverse := canonicalPolygonContours([][][2]float64{bo.Points}, true)
		if beforeReverse < beforeShape {
			beforeShape = beforeReverse
		}
		afterShape := canonicalPolygonContours([][][2]float64{ao.Points}, false)
		afterReverse := canonicalPolygonContours([][][2]float64{ao.Points}, true)
		if afterReverse < afterShape {
			afterShape = afterReverse
		}
		if beforeShape != afterShape {
			return fmt.Errorf("fresh after board outline polygon differs from the planned baseline")
		}
	}
	if before.CopperLayers != after.CopperLayers {
		return fmt.Errorf("fresh after copper-layer count differs from the planned baseline")
	}
	return nil
}

func verifyCrystalLiveClearance(candidate pcbLayoutCandidate, after *boardSnapshot) error {
	if candidate.Bundle == nil || candidate.Bundle.Kind != "crystal-guard" {
		return nil
	}
	if after == nil || after.Rules == nil || after.Outline == nil || after.Copper == nil {
		return fmt.Errorf("fresh rules, outline or copper are unavailable for crystal live-clearance proof")
	}
	clearance := after.Rules.ClearanceMil
	if !allFinite(clearance, after.Rules.ClearanceTrackTrackMil, after.Rules.CopperToEdgeMil) || clearance < 0 || after.Rules.ClearanceTrackTrackMil < 0 || after.Rules.CopperToEdgeMil < 0 {
		return fmt.Errorf("fresh live-clearance rules are invalid")
	}
	owned := map[string]bool{}
	for _, ref := range candidate.Bundle.OwnedRefs {
		owned[ref] = true
	}
	byRef := map[string]boardComp{}
	for _, component := range after.Components {
		if _, duplicate := byRef[component.Designator]; duplicate {
			return fmt.Errorf("fresh component %s appears more than once", component.Designator)
		}
		byRef[component.Designator] = component
	}
	for ref := range owned {
		component, ok := byRef[ref]
		if !ok || component.BBox == nil {
			return fmt.Errorf("owned component %s has no fresh measured bbox", ref)
		}
		if !bboxInsideBoardOutline(after.Outline, *component.BBox) {
			return fmt.Errorf("owned component %s fresh bbox is outside or crosses the board outline", ref)
		}
		for otherRef, other := range byRef {
			if otherRef == ref || other.BBox == nil || !boardComponentsSharePlacementLayer(component, other) {
				continue
			}
			if layoutRectGap(*component.BBox, *other.BBox) < clearance-netPathGeomEps {
				return fmt.Errorf("owned component %s is closer than live %.4fmil clearance to %s", ref, clearance, otherRef)
			}
		}
	}
	tracks, arcs, vias, err := crystalSnapshotRouting(after)
	if err != nil {
		return fmt.Errorf("fresh routing geometry is unavailable: %w", err)
	}
	pads, err := boardSnapshotNetPathPads(after)
	if err != nil {
		return fmt.Errorf("fresh pad geometry is unavailable: %w", err)
	}
	areas, thermalStrokes, err := parseModuleCopperAreasAndStrokes(after.Copper.Fills, after.Copper.Pours, after.Copper.Poured)
	if err != nil {
		return fmt.Errorf("fresh copper-area geometry is unavailable: %w", err)
	}
	tracks = append(tracks, thermalStrokes...)
	plannedVias := append([]pcbViaP(nil), vias...)
	for _, via := range candidate.Bundle.Vias {
		plannedVias = append(plannedVias, pcbViaP{ID: "planned:" + via.ID, Net: via.Net, X: via.X, Y: via.Y, Hole: via.HoleMil, Dia: via.DiameterMil})
	}
	if err := validateCrystalRoutes(candidate.Bundle, after, pads, tracks, arcs, plannedVias, areas); err != nil {
		return fmt.Errorf("fresh crystal copper violates live clearance: %w", err)
	}
	return nil
}

type moduleNoPourRegion struct {
	id     string
	layer  int
	points [][2]float64
}

func resolveModuleNoPourRegions(candidate pcbLayoutCandidate, after *boardSnapshot) ([]moduleNoPourRegion, error) {
	if candidate.Bundle == nil || after == nil || after.Copper == nil {
		return nil, fmt.Errorf("candidate bundle or fresh copper is unavailable")
	}
	var out []moduleNoPourRegion
	for _, expected := range candidate.Bundle.Regions {
		if !containsString(expected.RuleTypes, "no-pours") {
			continue
		}
		var match map[string]any
		for _, raw := range after.Copper.Regions {
			m, ok := raw.(map[string]any)
			if !ok || int(asFloat(m["layer"])) != expected.Layer || m["geometryAvailable"] != true || !sameStringSet(m["ruleTypeNames"], expected.RuleTypes) {
				continue
			}
			equal, err := polygonSourcesEquivalent(m["source"], pointsPolygonSource(expected.Points))
			if err != nil {
				return nil, fmt.Errorf("no-pours region %s geometry is unknown: %w", expected.ID, err)
			}
			if !equal {
				continue
			}
			if match != nil {
				return nil, fmt.Errorf("no-pours region %s has multiple exact fresh matches", expected.ID)
			}
			match = m
		}
		if match == nil {
			return nil, fmt.Errorf("no-pours region %s has no exact fresh match", expected.ID)
		}
		contours, err := polygonSourceContours(match["source"])
		if err != nil || len(contours) != 1 {
			return nil, fmt.Errorf("no-pours region %s must have one readable contour", expected.ID)
		}
		if _, err := buildCompoundPolygonTopology(contours); err != nil {
			return nil, fmt.Errorf("no-pours region %s contour is invalid: %w", expected.ID, err)
		}
		out = append(out, moduleNoPourRegion{id: expected.ID, layer: expected.Layer, points: contours[0]})
	}
	return out, nil
}

func verifyCrystalSensitiveCoverage(candidate pcbLayoutCandidate, after *boardSnapshot) error {
	if candidate.Bundle == nil || candidate.Bundle.Kind != "crystal-guard" {
		return nil
	}
	if after == nil || after.Rules == nil {
		return fmt.Errorf("fresh rules are unavailable for crystal sensitive-envelope proof")
	}
	regions, err := resolveModuleNoPourRegions(candidate, after)
	if err != nil {
		return err
	}
	if len(regions) != 2 {
		return fmt.Errorf("crystal sensitive-envelope proof needs exactly two fresh no-pours regions")
	}
	margin, ok := candidate.Bundle.Metrics["sensitiveKeepoutMarginMil"]
	if !ok || !isFinite(margin) || margin < 0 {
		return fmt.Errorf("crystal sensitive keepout margin is unavailable")
	}
	clearance := math.Max(after.Rules.ClearanceMil, after.Rules.ClearanceTrackTrackMil)
	if !isFinite(clearance) || clearance < 0 {
		return fmt.Errorf("fresh signal clearance is unavailable")
	}
	components := map[string]boardComp{}
	for _, component := range after.Components {
		components[component.Designator] = component
	}
	tracks, arcs, _, err := crystalSnapshotRouting(after)
	if err != nil {
		return err
	}
	signalNets := map[string]bool{}
	for _, route := range candidate.Bundle.SignalRoutes {
		signalNets[route.Net] = true
	}
	for _, region := range regions {
		for _, ref := range candidate.Bundle.OwnedRefs {
			component, exists := components[ref]
			if !exists || component.BBox == nil {
				return fmt.Errorf("owned member %s fresh bbox is unavailable", ref)
			}
			required := expandLayoutBBox(*component.BBox, margin)
			if !simplePolygonContainsBBox(region.points, required) {
				return fmt.Errorf("no-pours region %s on layer %d does not cover owned member %s plus %.4fmil margin", region.id, region.layer, ref, margin)
			}
		}
		seenNet := map[string]bool{}
		for _, track := range tracks {
			if !signalNets[track.Net] {
				continue
			}
			seenNet[track.Net] = true
			radius := track.Width/2 + clearance + margin
			if !simplePolygonContainsSegmentStroke(region.points, [2]float64{track.X1, track.Y1}, [2]float64{track.X2, track.Y2}, radius) {
				return fmt.Errorf("no-pours region %s on layer %d does not cover complete %s track %s stroke", region.id, region.layer, track.Net, track.ID)
			}
		}
		for _, arc := range arcs {
			if !signalNets[arc.Net] {
				continue
			}
			seenNet[arc.Net] = true
			curve, _, err := flattenNetPathArc(arc)
			if err != nil {
				return fmt.Errorf("OSC arc %s geometry is unknown: %w", arc.ID, err)
			}
			for i := 0; i+1 < len(curve); i++ {
				radius := arc.Width/2 + clearance + margin
				if !simplePolygonContainsSegmentStroke(region.points, [2]float64{curve[i].x, curve[i].y}, [2]float64{curve[i+1].x, curve[i+1].y}, radius) {
					return fmt.Errorf("no-pours region %s on layer %d does not cover complete %s arc %s stroke", region.id, region.layer, arc.Net, arc.ID)
				}
			}
		}
		for net := range signalNets {
			if !seenNet[net] {
				return fmt.Errorf("fresh OSC net %s has no readable copper stroke", net)
			}
		}
	}
	return nil
}

func pointInOrOnSimplePolygon(points [][2]float64, p [2]float64) bool {
	for i, a := range points {
		b := points[(i+1)%len(points)]
		if segPtDist(p[0], p[1], a[0], a[1], b[0], b[1]) <= netPathGeomEps {
			return true
		}
	}
	return pointInSimplePolygon(points, p)
}

func simplePolygonContainsBBox(points [][2]float64, box layoutBBox) bool {
	corners := [][2]float64{{box.MinX, box.MinY}, {box.MaxX, box.MinY}, {box.MaxX, box.MaxY}, {box.MinX, box.MaxY}, {(box.MinX + box.MaxX) / 2, (box.MinY + box.MaxY) / 2}}
	for _, corner := range corners {
		if !pointInOrOnSimplePolygon(points, corner) {
			return false
		}
	}
	for _, p := range points {
		if p[0] > box.MinX+netPathGeomEps && p[0] < box.MaxX-netPathGeomEps && p[1] > box.MinY+netPathGeomEps && p[1] < box.MaxY-netPathGeomEps {
			return false
		}
	}
	return true
}

func simplePolygonContainsSegmentStroke(points [][2]float64, a, b [2]float64, radius float64) bool {
	if !isFinite(radius) || radius < 0 || !pointInOrOnSimplePolygon(points, a) || !pointInOrOnSimplePolygon(points, b) {
		return false
	}
	for i, p := range points {
		q := points[(i+1)%len(points)]
		if segSegDist(a[0], a[1], b[0], b[1], p[0], p[1], q[0], q[1]) < radius-netPathGeomEps {
			return false
		}
	}
	return true
}

func verifyCrystalGroundConnectivity(candidate pcbLayoutCandidate, after *boardSnapshot) error {
	if candidate.Bundle == nil || candidate.Bundle.Kind != "crystal-guard" {
		return nil
	}
	if len(candidate.Bundle.GroundAnchors) == 0 {
		return fmt.Errorf("crystal-guard declares no GND anchor")
	}
	groundNet := ""
	for _, route := range candidate.Bundle.GroundRoutes {
		if route.Net != "" {
			groundNet = route.Net
			break
		}
	}
	if groundNet == "" {
		return fmt.Errorf("crystal-guard has no ground-route net")
	}
	if err := verifyCrystalProtectionStructure(candidate, groundNet); err != nil {
		return err
	}
	tracks, arcs, vias, err := crystalSnapshotRouting(after)
	if err != nil {
		return err
	}
	pads, err := boardSnapshotNetPathPads(after)
	if err != nil {
		return err
	}
	owned := map[string]bool{}
	for _, ref := range candidate.Bundle.OwnedRefs {
		owned[ref] = true
	}
	var localGroundPads []string
	for _, p := range pads {
		if owned[p.Designator] && p.Net == groundNet {
			localGroundPads = append(localGroundPads, netPathPadLabel(p))
		}
	}
	sort.Strings(localGroundPads)
	if len(localGroundPads) < 4 {
		return fmt.Errorf("crystal-guard found %d owned GND pad(s), want at least 4", len(localGroundPads))
	}
	anchor := candidate.Bundle.GroundAnchors[0]
	for _, from := range localGroundPads {
		rep, err := analyzePcbNetPath(pads, tracks, arcs, vias, pcbNetPathOptions{From: from, To: anchor, Net: groundNet})
		if err != nil {
			return fmt.Errorf("GND path %s -> %s is unknown: %w", from, anchor, err)
		}
		if !rep.Connected || rep.ViaCount < 2 || !containsInt(rep.Layers, 1) || !containsInt(rep.Layers, 2) {
			return fmt.Errorf("GND path %s -> %s is not proven through TOP, BOTTOM and two vias", from, anchor)
		}
	}
	if err := verifyCrystalGuardSegmentsConnected(candidate.Bundle, pads, tracks, arcs, vias, anchor, groundNet); err != nil {
		return err
	}
	var anchors []pcbModuleVia
	for _, via := range candidate.Bundle.Vias {
		if via.Net == groundNet && via.Role == "ground-anchor" {
			anchors = append(anchors, via)
		}
	}
	if len(anchors) < 2 {
		return fmt.Errorf("crystal-guard declares fewer than two ground-anchor vias")
	}
	if candidate.Bundle.GroundImplementation == "tracks-vias" {
		return nil
	}
	touchedByLayer := map[int]map[string]bool{1: {}, 2: {}}
	layerTouchesModule := map[int]bool{}
	for _, expectedPour := range candidate.Bundle.Pours {
		if expectedPour.Net != groundNet {
			continue
		}
		materialized, err := findExpectedMaterializedPour(expectedPour, after.Copper.Pours, after.Copper.Poured)
		if err != nil {
			return err
		}
		pourTouchesModule := false
		for _, via := range candidate.Bundle.Vias {
			if via.Net != groundNet {
				continue
			}
			touches, err := materializedPourTouchesVia(materialized, via)
			if err != nil {
				return fmt.Errorf("materialized local pour %s contact for declared %s via %s is unknown: %w", expectedPour.ID, via.Role, via.ID, err)
			}
			if touches {
				pourTouchesModule = true
				layerTouchesModule[expectedPour.Layer] = true
				touchedByLayer[expectedPour.Layer][via.ID] = true
			}
		}
		_ = pourTouchesModule // A ring slab may legitimately contain no via; the layer union may not.
	}
	for _, layer := range []int{1, 2} {
		if !layerTouchesModule[layer] {
			return fmt.Errorf("materialized local GND pour union on layer %d touches no declared module GND via", layer)
		}
		for _, via := range candidate.Bundle.Vias {
			if via.Net == groundNet && !touchedByLayer[layer][via.ID] {
				return fmt.Errorf("declared %s via %s touches no local materialized GND pour on layer %d", via.Role, via.ID, layer)
			}
		}
	}
	return nil
}

// verifyCrystalGuardSegmentsConnected proves every declared guard segment from
// its exact fresh track object to the designated ground anchor using only listed
// GND pads/tracks/arcs/vias. A same-net label, a neighboring guard segment, or a
// materialized pour is not substituted for this routed-copper proof.
func verifyCrystalGuardSegmentsConnected(bundle *pcbLayoutModuleBundle, pads []pcbPadP, tracks []pcbTrack, arcs []pcbArc, vias []pcbViaP, anchor, groundNet string) error {
	nodes, byRef := buildNetPathNodes(pads, tracks, arcs, vias, groundNet, nil)
	anchorNode, ok := byRef[strings.ToLower(anchor)]
	if !ok {
		return fmt.Errorf("crystal-guard ground anchor %s is absent from the proven GND pad graph", anchor)
	}
	adj := make([][]int, len(nodes))
	trackNodeByID := map[string]int{}
	for i, node := range nodes {
		if node.kind == "track" {
			if _, duplicate := trackNodeByID[node.id]; duplicate {
				return fmt.Errorf("fresh GND routing repeats track primitiveId %s", node.id)
			}
			trackNodeByID[node.id] = i
		}
		for j := 0; j < i; j++ {
			if netPathNodesTouch(nodes[i], nodes[j], nil) {
				adj[i] = append(adj[i], j)
				adj[j] = append(adj[j], i)
			}
		}
	}
	guardSegments := 0
	for _, route := range bundle.GroundRoutes {
		if route.Role != "guard" {
			continue
		}
		if route.Net != groundNet {
			return fmt.Errorf("guard route %s uses net %s, want %s", route.ID, route.Net, groundNet)
		}
		for segmentIndex := 0; segmentIndex+1 < len(route.Points); segmentIndex++ {
			guardSegments++
			p, q := route.Points[segmentIndex], route.Points[segmentIndex+1]
			matches, covered := matchingTrackCoverage(tracks, route.Net, route.Layer, route.WidthMil, p, q)
			if !covered {
				return fmt.Errorf("guard route %s segment %d is not exactly covered by fresh track geometry (%d collinear part(s))", route.ID, segmentIndex+1, len(matches))
			}
			for _, match := range matches {
				start, exists := trackNodeByID[match.ID]
				if !exists {
					return fmt.Errorf("guard route %s segment %d fresh track %s is absent from the GND graph", route.ID, segmentIndex+1, match.ID)
				}
				if len(bfsNetPath(adj, start, anchorNode)) == 0 {
					return fmt.Errorf("guard route %s segment %d fresh track %s has no listed-copper GND path to anchor %s", route.ID, segmentIndex+1, match.ID, anchor)
				}
			}
		}
	}
	if guardSegments == 0 {
		return fmt.Errorf("crystal-guard declares no role=guard segment")
	}
	return nil
}

func verifyCrystalProtectionStructure(candidate pcbLayoutCandidate, groundNet string) error {
	if candidate.Bundle == nil || candidate.Bundle.Kind != "crystal-guard" {
		return nil
	}
	if candidate.Bundle.GroundImplementation == "tracks-vias" {
		return verifyCrystalTracksViasProtection(candidate, groundNet)
	}
	if len(candidate.Bundle.UnreservedPours) > 0 {
		if candidate.Escape == nil {
			return fmt.Errorf("clipped pours have no escape witness")
		}
		uncut := *candidate.Bundle
		uncut.Pours = uncut.UnreservedPours
		uncut.UnreservedPours = nil
		original := candidate
		original.Bundle = &uncut
		if err := verifyCrystalProtectionStructure(original, groundNet); err != nil {
			return err
		}
		expected, err := clipPCBLocalPours(uncut.Pours, candidate.Escape.Routes, candidate.Escape.Vias, uncut.Metrics["localPourKeepoutClearanceMil"])
		if err != nil {
			return err
		}
		x, _ := json.Marshal(expected)
		y, _ := json.Marshal(candidate.Bundle.Pours)
		if string(x) != string(y) {
			return fmt.Errorf("local pours do not match deterministic reserved-channel clipping")
		}
		layers := map[int]bool{}
		for _, p := range expected {
			layers[p.Layer] = true
		}
		if !layers[1] || !layers[2] {
			return fmt.Errorf("reserved channels removed an entire local ground layer")
		}
		return nil
	}
	pourLayers := map[int]int{}
	for _, pour := range candidate.Bundle.Pours {
		if pour.Net != groundNet {
			return fmt.Errorf("crystal-guard local pour %s uses net %s, want %s", pour.ID, pour.Net, groundNet)
		}
		pourLayers[pour.Layer]++
	}
	if len(candidate.Bundle.Pours) != 8 || pourLayers[1] != 4 || pourLayers[2] != 4 || len(pourLayers) != 2 {
		return fmt.Errorf("crystal-guard local GND ring pours must contain exactly TOP=4 and BOTTOM=4 slabs")
	}
	regionLayers := map[int]int{}
	regions := map[int]pcbModuleRegion{}
	for _, region := range candidate.Bundle.Regions {
		if containsString(region.RuleTypes, "no-pours") {
			regionLayers[region.Layer]++
			regions[region.Layer] = region
		}
	}
	if len(regionLayers) != 2 || regionLayers[1] != 1 || regionLayers[2] != 1 {
		return fmt.Errorf("crystal-guard no-pours regions must cover exactly TOP=1 and BOTTOM=2 once each")
	}
	clearance := candidate.Bundle.Metrics["localPourKeepoutClearanceMil"]
	if !allFinite(clearance) || clearance <= 0 {
		return fmt.Errorf("crystal-guard local ring pour clearance is missing or invalid")
	}
	if !cyclicPolygonPointsNearDirection(removeCollinearPolygonPoints(regions[1].Points), removeCollinearPolygonPoints(regions[2].Points), 1, netPathGeomEps) &&
		!cyclicPolygonPointsNearDirection(removeCollinearPolygonPoints(regions[1].Points), removeCollinearPolygonPoints(regions[2].Points), -1, netPathGeomEps) {
		return fmt.Errorf("crystal-guard TOP/BOTTOM no-pours geometry differs")
	}
	var referenceOuter *layoutBBox
	for _, layer := range []int{1, 2} {
		if _, err := buildCompoundPolygonTopology([][][2]float64{regions[layer].Points}); err != nil {
			return fmt.Errorf("crystal-guard no-pours region on layer %d is invalid: %w", layer, err)
		}
		inner := expandLayoutBBox(pointsBBox(regions[layer].Points), clearance)
		bySide := map[string]layoutBBox{}
		var outer *layoutBBox
		layerName := "top"
		if layer == 2 {
			layerName = "bottom"
		}
		for _, pour := range candidate.Bundle.Pours {
			if pour.Layer != layer {
				continue
			}
			prefix := "crystal-ground-" + layerName + "-"
			if !strings.HasPrefix(pour.ID, prefix) {
				return fmt.Errorf("crystal-guard local pour %s does not use the %s ring identity", pour.ID, layerName)
			}
			side := strings.TrimPrefix(pour.ID, prefix)
			if side != "left" && side != "right" && side != "min-y" && side != "max-y" {
				return fmt.Errorf("crystal-guard local pour %s has unsupported ring side %q", pour.ID, side)
			}
			if _, duplicate := bySide[side]; duplicate {
				return fmt.Errorf("crystal-guard local ring repeats %s on layer %d", side, layer)
			}
			box, err := crystalRectPourBBox(pour.Points)
			if err != nil {
				return fmt.Errorf("crystal-guard local pour %s: %w", pour.ID, err)
			}
			if !simplePolygonsStrictlyDisjoint(pour.Points, regions[layer].Points) {
				return fmt.Errorf("crystal-guard local pour %s boundary intersects or touches no-pours region on layer %d", pour.ID, layer)
			}
			bySide[side] = box
			outer = unionLayoutBBox(outer, &box)
		}
		if len(bySide) != 4 || outer == nil {
			return fmt.Errorf("crystal-guard local ring on layer %d is incomplete", layer)
		}
		expected := map[string]layoutBBox{
			"left":  {MinX: outer.MinX, MinY: outer.MinY, MaxX: inner.MinX, MaxY: outer.MaxY},
			"right": {MinX: inner.MaxX, MinY: outer.MinY, MaxX: outer.MaxX, MaxY: outer.MaxY},
			"min-y": {MinX: inner.MinX, MinY: outer.MinY, MaxX: inner.MaxX, MaxY: inner.MinY},
			"max-y": {MinX: inner.MinX, MinY: inner.MaxY, MaxX: inner.MaxX, MaxY: outer.MaxY},
		}
		if outer.MinX >= inner.MinX-netPathGeomEps || outer.MinY >= inner.MinY-netPathGeomEps || outer.MaxX <= inner.MaxX+netPathGeomEps || outer.MaxY <= inner.MaxY+netPathGeomEps {
			return fmt.Errorf("crystal-guard local ring on layer %d does not strictly enclose the no-pours bbox plus %.3fmil clearance", layer, clearance)
		}
		for side, want := range expected {
			if !layoutBBoxNear(bySide[side], want, netPathGeomEps) {
				return fmt.Errorf("crystal-guard local ring %s on layer %d is %+v, want %+v", side, layer, bySide[side], want)
			}
		}
		if referenceOuter == nil {
			copy := *outer
			referenceOuter = &copy
		} else if !layoutBBoxNear(*referenceOuter, *outer, netPathGeomEps) {
			return fmt.Errorf("crystal-guard TOP/BOTTOM local ring outer geometry differs")
		}
	}
	return nil
}

func crystalRectPourBBox(points [][2]float64) (layoutBBox, error) {
	if len(points) != 4 {
		return layoutBBox{}, fmt.Errorf("must be a four-point rectangle")
	}
	if _, err := buildCompoundPolygonTopology([][][2]float64{points}); err != nil {
		return layoutBBox{}, fmt.Errorf("invalid polygon: %w", err)
	}
	box := pointsBBox(points)
	corners := map[[2]int]bool{}
	for _, p := range points {
		x, y := -1, -1
		if math.Abs(p[0]-box.MinX) <= netPathGeomEps {
			x = 0
		} else if math.Abs(p[0]-box.MaxX) <= netPathGeomEps {
			x = 1
		}
		if math.Abs(p[1]-box.MinY) <= netPathGeomEps {
			y = 0
		} else if math.Abs(p[1]-box.MaxY) <= netPathGeomEps {
			y = 1
		}
		if x < 0 || y < 0 || corners[[2]int{x, y}] {
			return layoutBBox{}, fmt.Errorf("polygon is not an axis-aligned rectangle")
		}
		corners[[2]int{x, y}] = true
	}
	return box, nil
}

func simplePolygonsStrictlyDisjoint(a, b [][2]float64) bool {
	if len(a) < 3 || len(b) < 3 {
		return false
	}
	for i, p := range a {
		q := a[(i+1)%len(a)]
		for j, u := range b {
			v := b[(j+1)%len(b)]
			if segSegDist(p[0], p[1], q[0], q[1], u[0], u[1], v[0], v[1]) <= netPathGeomEps {
				return false
			}
		}
	}
	return !pointInOrOnSimplePolygon(a, b[0]) && !pointInOrOnSimplePolygon(b, a[0])
}

func layoutBBoxNear(a, b layoutBBox, eps float64) bool {
	return math.Abs(a.MinX-b.MinX) <= eps && math.Abs(a.MinY-b.MinY) <= eps && math.Abs(a.MaxX-b.MaxX) <= eps && math.Abs(a.MaxY-b.MaxY) <= eps
}

func verifyCrystalSignalConnectivity(candidate pcbLayoutCandidate, after *boardSnapshot) ([]string, error) {
	if candidate.Bundle == nil || candidate.Bundle.Kind != "crystal-guard" {
		return nil, nil
	}
	tracks, arcs, vias, err := crystalSnapshotRouting(after)
	if err != nil {
		return nil, err
	}
	pads, err := boardSnapshotNetPathPads(after)
	if err != nil {
		return nil, err
	}
	mainByNet, capByNet := map[string]pcbModuleRoute{}, map[string]pcbModuleRoute{}
	for _, route := range candidate.Bundle.SignalRoutes {
		switch route.Role {
		case "signal-main":
			if _, exists := mainByNet[route.Net]; exists {
				return nil, fmt.Errorf("crystal signal net %s has more than one signal-main route", route.Net)
			}
			mainByNet[route.Net] = route
		case "signal-cap":
			if _, exists := capByNet[route.Net]; exists {
				return nil, fmt.Errorf("crystal signal net %s has more than one signal-cap route", route.Net)
			}
			capByNet[route.Net] = route
		default:
			return nil, fmt.Errorf("crystal signal route %s has unsupported role %q", route.ID, route.Role)
		}
	}
	if len(mainByNet) != 2 || len(capByNet) != 2 {
		return nil, fmt.Errorf("crystal-guard needs exactly two signal-main and two signal-cap routes; got %d and %d", len(mainByNet), len(capByNet))
	}
	nets := make([]string, 0, len(mainByNet))
	for net := range mainByNet {
		nets = append(nets, net)
	}
	sort.Strings(nets)
	checks := make([]string, 0, len(nets))
	for _, net := range nets {
		main := mainByNet[net]
		capRoute, ok := capByNet[net]
		if !ok {
			return nil, fmt.Errorf("crystal signal net %s has no capacitor branch", net)
		}
		if main.Layer != 1 || capRoute.Layer != 1 {
			return nil, fmt.Errorf("crystal signal net %s candidate is not entirely TOP", net)
		}
		for _, track := range tracks {
			if track.Net == net && track.Layer != 1 {
				return nil, fmt.Errorf("crystal signal net %s has fresh track %s outside TOP", net, track.ID)
			}
		}
		for _, arc := range arcs {
			if arc.Net == net && arc.Layer != 1 {
				return nil, fmt.Errorf("crystal signal net %s has fresh arc %s outside TOP", net, arc.ID)
			}
		}
		for _, via := range vias {
			if via.Net == net {
				return nil, fmt.Errorf("crystal signal net %s has fresh via %s; want 0 vias", net, via.ID)
			}
		}
		if main.From == "" || main.To == "" || capRoute.From == "" || capRoute.To == "" || !strings.EqualFold(main.To, capRoute.To) {
			return nil, fmt.Errorf("crystal signal net %s does not declare capacitor -> shared crystal pad -> MCU topology", net)
		}
		opts := pcbNetPathOptions{From: capRoute.From, Through: []string{main.To}, To: main.From, Net: net}
		actual, err := analyzePcbNetPath(pads, tracks, arcs, vias, opts)
		if err != nil {
			return nil, fmt.Errorf("crystal signal %s topology proof is unknown: %w", net, err)
		}
		if !actual.Connected {
			return nil, fmt.Errorf("crystal signal %s is not connected in declared order %s -> %s -> %s", net, capRoute.From, main.To, main.From)
		}
		if actual.ViaCount != 0 || len(actual.Layers) != 1 || actual.Layers[0] != 1 {
			return nil, fmt.Errorf("crystal signal %s is not TOP/0-via: layers=%v vias=%d", net, actual.Layers, actual.ViaCount)
		}
		// Derive the expected measurement through the same path analyzer. This
		// catches a readback path that reaches the right pads through extra or
		// differently shaped copper even when all planned segments also exist.
		var expectedTracks []pcbTrack
		for _, route := range candidate.Bundle.SignalRoutes {
			if route.Net != net {
				continue
			}
			for i := 0; i+1 < len(route.Points); i++ {
				p, q := route.Points[i], route.Points[i+1]
				expectedTracks = append(expectedTracks, pcbTrack{ID: fmt.Sprintf("candidate:%s:%d", route.ID, i), Net: net, Layer: route.Layer, X1: p[0], Y1: p[1], X2: q[0], Y2: q[1], Width: route.WidthMil})
			}
		}
		expected, err := analyzePcbNetPath(pads, expectedTracks, nil, nil, opts)
		if err != nil || !expected.Connected {
			return nil, fmt.Errorf("candidate crystal signal %s topology cannot be measured: %v", net, err)
		}
		if math.Abs(actual.LengthMil-expected.LengthMil) > netPathGeomEps || actual.TurnCount != expected.TurnCount {
			return nil, fmt.Errorf("crystal signal %s measurement differs: actual length=%.4fmil turns=%d; candidate length=%.4fmil turns=%d", net, actual.LengthMil, actual.TurnCount, expected.LengthMil, expected.TurnCount)
		}
		checks = append(checks, fmt.Sprintf("signal %s topology %s -> %s -> %s is TOP, 0 via; actual length %.4fmil, turns %d", net, capRoute.From, main.To, main.From, actual.LengthMil, actual.TurnCount))
	}
	return checks, nil
}

func boardSnapshotNetPathPads(s *boardSnapshot) ([]pcbPadP, error) {
	var out []pcbPadP
	for _, c := range s.Components {
		for _, p := range c.Pads {
			pp := pcbPadP{ID: p.ID, Designator: c.Designator, Number: p.Number, Net: p.Net, Layer: p.Layer, X: p.X, Y: p.Y, W: p.W, H: p.H, Rotation: p.Rotation}
			shapeInput := map[string]any{"shape": p.Shape, "specialPad": p.SpecialPad}
			if err := parseNetPathPadShape(shapeInput, &pp, c.Designator+"."+p.Number); err != nil {
				return nil, fmt.Errorf("pad %s.%s geometry: %w", c.Designator, p.Number, err)
			}
			out = append(out, pp)
		}
	}
	return out, nil
}

func containsInt(values []int, want int) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func findExpectedMaterializedPour(expected pcbModulePour, pours, poured []any) (map[string]any, error) {
	var boundary map[string]any
	for _, raw := range pours {
		m, ok := raw.(map[string]any)
		if !ok || asString(m["net"]) != expected.Net || int(asFloat(m["layer"])) != expected.Layer || m["geometryAvailable"] != true {
			continue
		}
		equal, err := polygonSourcesEquivalent(m["source"], pointsPolygonSource(expected.Points))
		if err != nil {
			return nil, fmt.Errorf("local pour %s boundary geometry is unknown: %w", expected.ID, err)
		}
		if !equal {
			continue
		}
		if boundary != nil {
			return nil, fmt.Errorf("local pour %s has multiple exact boundary matches", expected.ID)
		}
		boundary = m
	}
	if boundary == nil {
		return nil, fmt.Errorf("local pour %s has no exact boundary match", expected.ID)
	}
	boundaryID := strings.TrimSpace(asString(boundary["primitiveId"]))
	if boundaryID == "" {
		return nil, fmt.Errorf("local pour %s exact boundary has no primitiveId", expected.ID)
	}
	var result map[string]any
	for _, raw := range poured {
		m, ok := raw.(map[string]any)
		if !ok || asString(m["pourPrimitiveId"]) != boundaryID {
			continue
		}
		if result != nil {
			return nil, fmt.Errorf("local pour %s boundary %s has multiple materialized objects", expected.ID, boundaryID)
		}
		result = m
	}
	if result == nil {
		return nil, fmt.Errorf("local pour %s boundary %s has no materialized object", expected.ID, boundaryID)
	}
	if strings.TrimSpace(asString(result["primitiveId"])) == "" || asString(result["net"]) != expected.Net || int(asFloat(result["layer"])) != expected.Layer {
		return nil, fmt.Errorf("local pour %s materialized identity/net/layer differs from boundary %s", expected.ID, boundaryID)
	}
	fills, ok := result["fills"].([]any)
	if !ok || len(fills) == 0 {
		return nil, fmt.Errorf("local pour %s materialized fills are unavailable or empty", expected.ID)
	}
	normalized, err := normalizeMaterializedPourMap(boundary, result)
	if err != nil {
		return nil, fmt.Errorf("local pour %s materialized geometry is unknown: %w", expected.ID, err)
	}
	return normalized, nil
}

// dev.6 normalizes official strictComplex geometry into mil. Unit metadata,
// never boundary size or a bounding box, is the authority for interpretation.
func normalizeMaterializedPourMap(boundary, materialized map[string]any) (map[string]any, error) {
	if _, err := polygonSourceBBox(boundary["source"]); err != nil {
		return nil, fmt.Errorf("parent boundary: %w", err)
	}
	fills, ok := materialized["fills"].([]any)
	if !ok || len(fills) == 0 {
		return nil, fmt.Errorf("fills unavailable or empty")
	}
	for i, raw := range fills {
		fill, ok := raw.(map[string]any)
		if !ok || fill["sourceUnits"] != "mil" || fill["lineWidthUnits"] != "mil" || fill["arcSweepUnits"] != "degree" {
			return nil, fmt.Errorf("fill %d explicit mil/degree units unavailable; capture using normalized connector", i)
		}
		if _, err := materializedPathPolylines(fill["source"]); err != nil {
			return nil, fmt.Errorf("fill %d geometry: %w", i, err)
		}
	}
	return materialized, nil
}

func materializedPathPolylines(raw any) ([][][2]float64, error) {
	items, ok := raw.([]any)
	if !ok || len(items) == 0 {
		return nil, fmt.Errorf("path source is not an array")
	}
	if _, nested := items[0].([]any); nested {
		var out [][][2]float64
		for _, item := range items {
			paths, err := materializedPathPolylines(item)
			if err != nil {
				return nil, err
			}
			out = append(out, paths...)
		}
		return out, nil
	}
	if len(items) < 5 {
		return nil, fmt.Errorf("path source is too short")
	}
	x, xok := asFloatOK(items[0])
	y, yok := asFloatOK(items[1])
	if !xok || !yok {
		return nil, fmt.Errorf("path start is invalid")
	}
	points := [][2]float64{{x, y}}
	for i := 2; i < len(items); {
		command, ok := items[i].(string)
		if !ok {
			return nil, fmt.Errorf("path command token is invalid at index %d", i)
		}
		switch command {
		case "L":
			i++
			consumed := 0
			for i+1 < len(items) {
				if _, next := items[i].(string); next {
					break
				}
				x, xok = asFloatOK(items[i])
				y, yok = asFloatOK(items[i+1])
				if !xok || !yok {
					return nil, fmt.Errorf("line coordinate is invalid")
				}
				points = append(points, [2]float64{x, y})
				i += 2
				consumed++
			}
			if consumed == 0 {
				return nil, fmt.Errorf("L command has no coordinate pair")
			}
		case "ARC":
			if i+3 >= len(items) {
				return nil, fmt.Errorf("ARC command is incomplete")
			}
			sweep, sok := asFloatOK(items[i+1])
			x, xok = asFloatOK(items[i+2])
			y, yok = asFloatOK(items[i+3])
			if !sok || !xok || !yok {
				return nil, fmt.Errorf("ARC command has invalid numeric fields")
			}
			start := points[len(points)-1]
			curve, _, err := flattenNetPathArc(pcbArc{X1: start[0], Y1: start[1], X2: x, Y2: y, ArcAngle: sweep})
			if err != nil {
				return nil, err
			}
			for _, point := range curve[1:] {
				points = append(points, [2]float64{point.x, point.y})
			}
			i += 4
		default:
			return nil, fmt.Errorf("path command %q is unsupported", command)
		}
	}
	if len(points) < 2 {
		return nil, fmt.Errorf("path has fewer than two vertices")
	}
	return [][][2]float64{points}, nil
}

func parseModuleCopperAreasAndStrokes(staticFills, pours, poured []any) ([]pcbCopperArea, []pcbTrack, error) {
	areas, err := parseCopperAreaObstacles(staticFills, nil)
	if err != nil {
		return nil, nil, err
	}
	var strokes []pcbTrack
	boundaries := primitiveMapsByID(pours)
	for i, raw := range poured {
		materialized, ok := raw.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("materialized poured[%d] is not an object", i)
		}
		boundaryID := asString(materialized["pourPrimitiveId"])
		boundary, ok := boundaries[boundaryID]
		if !ok {
			return nil, nil, fmt.Errorf("materialized poured %s has no exact parent boundary", boundaryID)
		}
		normalized, err := normalizeMaterializedPourMap(boundary, materialized)
		if err != nil {
			return nil, nil, err
		}
		layer := int(asFloat(normalized["layer"]))
		net := asString(normalized["net"])
		fills := normalized["fills"].([]any)
		for j, rawFill := range fills {
			fill := rawFill.(map[string]any)
			fillID := asString(fill["id"])
			if fillID == "" {
				fillID = fmt.Sprintf("%s:%d", boundaryID, j)
			}
			filled, ok := fill["fill"].(bool)
			if !ok {
				return nil, nil, fmt.Errorf("materialized poured %s fill %s solid/stroke state is unavailable", boundaryID, fillID)
			}
			if filled {
				contours, err := polygonSourceContours(fill["source"])
				if err != nil {
					return nil, nil, fmt.Errorf("materialized poured %s fill %s geometry is unknown: %w", boundaryID, fillID, err)
				}
				areas = append(areas, pcbCopperArea{ID: fillID, Kind: "materialized poured copper", Net: net, Layer: layer, Contours: contours})
				continue
			}
			width, ok := asFloatOK(fill["lineWidth"])
			if !ok || width <= 0 {
				return nil, nil, fmt.Errorf("materialized poured %s thermal stroke %s width is unavailable", boundaryID, fillID)
			}
			paths, err := materializedPathPolylines(fill["source"])
			if err != nil {
				return nil, nil, fmt.Errorf("materialized poured %s thermal stroke %s geometry is unknown: %w", boundaryID, fillID, err)
			}
			for pathIndex, path := range paths {
				for segmentIndex := 0; segmentIndex+1 < len(path); segmentIndex++ {
					a, b := path[segmentIndex], path[segmentIndex+1]
					areasID := fmt.Sprintf("%s:%d:%d", fillID, pathIndex, segmentIndex)
					// pcbTrack is also the exact stroke primitive needed by route
					// clearance and connectivity checks.
					strokes = append(strokes, pcbTrack{ID: areasID, Net: net, Layer: layer, Width: width, X1: a[0], Y1: a[1], X2: b[0], Y2: b[1]})
				}
			}
		}
	}
	return areas, strokes, nil
}

func materializedPourTouchesVia(materialized map[string]any, via pcbModuleVia) (bool, error) {
	fills, ok := materialized["fills"].([]any)
	if !ok || len(fills) == 0 {
		return false, fmt.Errorf("fills are unavailable or empty")
	}
	for fillIndex, raw := range fills {
		fill, ok := raw.(map[string]any)
		if !ok {
			return false, fmt.Errorf("fill %d is not an object", fillIndex)
		}
		filled, ok := fill["fill"].(bool)
		if !ok {
			return false, fmt.Errorf("fill %d solid/stroke state is unavailable", fillIndex)
		}
		if filled {
			contours, err := polygonSourceContours(fill["source"])
			if err != nil {
				return false, fmt.Errorf("fill %d polygon is unknown: %w", fillIndex, err)
			}
			topology, err := buildCompoundPolygonTopology(contours)
			if err != nil {
				return false, fmt.Errorf("fill %d topology is unknown: %w", fillIndex, err)
			}
			components, err := topology.annulusComponents(via)
			if err != nil {
				return false, err
			}
			if len(components) > 0 {
				return true, nil
			}
			continue
		}
		width, ok := asFloatOK(fill["lineWidth"])
		if !ok || width <= 0 {
			return false, fmt.Errorf("fill %d thermal-stroke width is unavailable", fillIndex)
		}
		paths, err := materializedPathPolylines(fill["source"])
		if err != nil {
			return false, fmt.Errorf("fill %d thermal-stroke path is unknown: %w", fillIndex, err)
		}
		for _, path := range paths {
			for i := 0; i+1 < len(path); i++ {
				a, b := path[i], path[i+1]
				if trackIntersectsViaAnnulus(pcbTrack{Width: width, X1: a[0], Y1: a[1], X2: b[0], Y2: b[1]}, via) {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

func materializedPourConnectsViaToAnchor(materialized map[string]any, target pcbModuleVia, anchors []pcbModuleVia) (bool, error) {
	fills, ok := materialized["fills"].([]any)
	if !ok || len(fills) == 0 {
		return false, fmt.Errorf("fills are unavailable or empty")
	}
	type solidPart struct {
		topology *compoundPolygonTopology
		nodes    map[int]int
	}
	var solids []solidPart
	var strokes []pcbTrack
	nodeCount := 0
	for fillIndex, raw := range fills {
		fill, ok := raw.(map[string]any)
		if !ok {
			return false, fmt.Errorf("fill %d is not an object", fillIndex)
		}
		filled, ok := fill["fill"].(bool)
		if !ok {
			return false, fmt.Errorf("fill %d solid/stroke state is unavailable", fillIndex)
		}
		if !filled {
			width, ok := asFloatOK(fill["lineWidth"])
			if !ok || width <= 0 {
				return false, fmt.Errorf("fill %d thermal-stroke width is unavailable", fillIndex)
			}
			paths, err := materializedPathPolylines(fill["source"])
			if err != nil {
				return false, fmt.Errorf("fill %d thermal-stroke path is unknown: %w", fillIndex, err)
			}
			for pathIndex, path := range paths {
				for segmentIndex := 0; segmentIndex+1 < len(path); segmentIndex++ {
					a, b := path[segmentIndex], path[segmentIndex+1]
					strokes = append(strokes, pcbTrack{ID: fmt.Sprintf("thermal-%d-%d-%d", fillIndex, pathIndex, segmentIndex), Width: width, X1: a[0], Y1: a[1], X2: b[0], Y2: b[1]})
				}
			}
			continue
		}
		contours, err := polygonSourceContours(fill["source"])
		if err != nil {
			return false, fmt.Errorf("fill %d polygon is unknown: %w", fillIndex, err)
		}
		topology, err := buildCompoundPolygonTopology(contours)
		if err != nil {
			return false, fmt.Errorf("fill %d topology is unknown: %w", fillIndex, err)
		}
		part := solidPart{topology: topology, nodes: map[int]int{}}
		for _, contour := range topology.contours {
			for _, component := range []int{contour.insideComponent, contour.outsideComponent} {
				if component >= 0 {
					if _, exists := part.nodes[component]; !exists {
						part.nodes[component] = nodeCount
						nodeCount++
					}
				}
			}
		}
		solids = append(solids, part)
	}
	strokeOffset := nodeCount
	nodeCount += len(strokes)
	adj := make([][]int, nodeCount)
	connect := func(a, b int) {
		adj[a] = append(adj[a], b)
		adj[b] = append(adj[b], a)
	}
	for i, stroke := range strokes {
		strokeNode := strokeOffset + i
		for _, solid := range solids {
			for component := range solid.topology.strokeComponents(stroke) {
				if node, exists := solid.nodes[component]; exists {
					connect(strokeNode, node)
				}
			}
		}
		for j := 0; j < i; j++ {
			other := strokes[j]
			if segSegDist(stroke.X1, stroke.Y1, stroke.X2, stroke.Y2, other.X1, other.Y1, other.X2, other.Y2) <= (stroke.Width+other.Width)/2+netPathGeomEps {
				connect(strokeNode, strokeOffset+j)
			}
		}
	}
	viaNodes := func(via pcbModuleVia) (map[int]bool, error) {
		nodes := map[int]bool{}
		for _, solid := range solids {
			components, err := solid.topology.annulusComponents(via)
			if err != nil {
				return nil, err
			}
			for component := range components {
				if node, exists := solid.nodes[component]; exists {
					nodes[node] = true
				}
			}
		}
		for i, stroke := range strokes {
			if trackIntersectsViaAnnulus(stroke, via) {
				nodes[strokeOffset+i] = true
			}
		}
		return nodes, nil
	}
	starts, err := viaNodes(target)
	if err != nil {
		return false, err
	}
	goals := map[int]bool{}
	for _, anchor := range anchors {
		nodes, err := viaNodes(anchor)
		if err != nil {
			return false, err
		}
		for node := range nodes {
			goals[node] = true
		}
	}
	seen := make([]bool, nodeCount)
	queue := make([]int, 0, len(starts))
	for node := range starts {
		if goals[node] {
			return true, nil
		}
		seen[node] = true
		queue = append(queue, node)
	}
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		for _, next := range adj[node] {
			if seen[next] {
				continue
			}
			if goals[next] {
				return true, nil
			}
			seen[next] = true
			queue = append(queue, next)
		}
	}
	return false, nil
}

type compoundPolygonContour struct {
	points           [][2]float64
	parent           int
	insideWinding    int
	insideComponent  int
	outsideComponent int
}

type compoundPolygonTopology struct {
	contours []compoundPolygonContour
}

// buildCompoundPolygonTopology turns non-intersecting oriented contours into
// material faces. A hole therefore separates its nested island from the outer
// copper even though both are inside the same outer contour.
func buildCompoundPolygonTopology(contours [][][2]float64) (*compoundPolygonTopology, error) {
	if len(contours) == 0 {
		return nil, fmt.Errorf("polygon has no contours")
	}
	t := &compoundPolygonTopology{contours: make([]compoundPolygonContour, len(contours))}
	areas := make([]float64, len(contours))
	for i, raw := range contours {
		points := removeCollinearPolygonPoints(raw)
		if len(points) < 3 {
			return nil, fmt.Errorf("contour %d has fewer than three vertices", i)
		}
		area := polygonSignedArea(points)
		if math.Abs(area) <= netPathGeomEps*netPathGeomEps {
			return nil, fmt.Errorf("contour %d has zero area", i)
		}
		for edge, a := range points {
			b := points[(edge+1)%len(points)]
			if math.Hypot(b[0]-a[0], b[1]-a[1]) <= netPathGeomEps {
				return nil, fmt.Errorf("contour %d has a zero-length edge", i)
			}
			for other := edge + 1; other < len(points); other++ {
				if other == edge+1 || edge == 0 && other == len(points)-1 {
					continue
				}
				c, d := points[other], points[(other+1)%len(points)]
				if segSegDist(a[0], a[1], b[0], b[1], c[0], c[1], d[0], d[1]) <= netPathGeomEps {
					return nil, fmt.Errorf("contour %d self-intersects or touches at edges %d/%d", i, edge, other)
				}
			}
		}
		areas[i] = area
		t.contours[i] = compoundPolygonContour{points: points, parent: -1, insideComponent: -1, outsideComponent: -1}
	}
	for i := range t.contours {
		for j := i + 1; j < len(t.contours); j++ {
			for ei, a := range t.contours[i].points {
				b := t.contours[i].points[(ei+1)%len(t.contours[i].points)]
				for ej, c := range t.contours[j].points {
					d := t.contours[j].points[(ej+1)%len(t.contours[j].points)]
					if segSegDist(a[0], a[1], b[0], b[1], c[0], c[1], d[0], d[1]) <= netPathGeomEps {
						return nil, fmt.Errorf("contours %d/%d intersect or touch", i, j)
					}
				}
			}
		}
	}
	for i := range t.contours {
		probe := t.contours[i].points[0]
		parent, parentArea := -1, math.Inf(1)
		for j := range t.contours {
			if i == j || !pointInSimplePolygon(t.contours[j].points, probe) {
				continue
			}
			if area := math.Abs(areas[j]); area < parentArea {
				parent, parentArea = j, area
			}
		}
		t.contours[i].parent = parent
	}
	state := make([]int, len(t.contours))
	nextComponent := 0
	var resolve func(int) error
	resolve = func(i int) error {
		if state[i] == 2 {
			return nil
		}
		if state[i] == 1 {
			return fmt.Errorf("contour nesting cycle at %d", i)
		}
		state[i] = 1
		outsideWinding, outsideComponent := 0, -1
		if parent := t.contours[i].parent; parent >= 0 {
			if err := resolve(parent); err != nil {
				return err
			}
			outsideWinding = t.contours[parent].insideWinding
			outsideComponent = t.contours[parent].insideComponent
		}
		sign := 1
		if areas[i] < 0 {
			sign = -1
		}
		insideWinding := outsideWinding + sign
		insideComponent := -1
		if insideWinding != 0 {
			if outsideWinding != 0 {
				insideComponent = outsideComponent
			} else {
				insideComponent = nextComponent
				nextComponent++
			}
		}
		t.contours[i].insideWinding = insideWinding
		t.contours[i].insideComponent = insideComponent
		t.contours[i].outsideComponent = outsideComponent
		state[i] = 2
		return nil
	}
	for i := range t.contours {
		if err := resolve(i); err != nil {
			return nil, err
		}
	}
	return t, nil
}

func (t *compoundPolygonTopology) componentAt(p [2]float64) int {
	component, area := -1, math.Inf(1)
	for _, contour := range t.contours {
		if pointInSimplePolygon(contour.points, p) {
			if a := math.Abs(polygonSignedArea(contour.points)); a < area {
				component, area = contour.insideComponent, a
			}
		}
	}
	return component
}

func (t *compoundPolygonTopology) annulusComponents(via pcbModuleVia) (map[int]bool, error) {
	if !allFinite(via.X, via.Y, via.HoleMil, via.DiameterMil) || via.HoleMil < 0 || via.DiameterMil <= via.HoleMil {
		return nil, fmt.Errorf("via %s annulus geometry is invalid", via.ID)
	}
	inner, outer := via.HoleMil/2, via.DiameterMil/2
	components := map[int]bool{}
	boundaryHit := false
	for _, contour := range t.contours {
		for i, a := range contour.points {
			b := contour.points[(i+1)%len(contour.points)]
			if !segmentIntersectsAnnulus(a, b, [2]float64{via.X, via.Y}, inner, outer) {
				continue
			}
			boundaryHit = true
			if contour.insideComponent >= 0 {
				components[contour.insideComponent] = true
			}
			if contour.outsideComponent >= 0 {
				components[contour.outsideComponent] = true
			}
		}
	}
	if !boundaryHit {
		probeRadius := (inner + outer) / 2
		if component := t.componentAt([2]float64{via.X + probeRadius, via.Y}); component >= 0 {
			components[component] = true
		}
	}
	return components, nil
}

func (t *compoundPolygonTopology) strokeComponents(stroke pcbTrack) map[int]bool {
	components := map[int]bool{}
	radius := stroke.Width / 2
	for _, point := range [][2]float64{{stroke.X1, stroke.Y1}, {stroke.X2, stroke.Y2}, {(stroke.X1 + stroke.X2) / 2, (stroke.Y1 + stroke.Y2) / 2}} {
		if component := t.componentAt(point); component >= 0 {
			components[component] = true
		}
	}
	for _, contour := range t.contours {
		for i, a := range contour.points {
			b := contour.points[(i+1)%len(contour.points)]
			if segSegDist(stroke.X1, stroke.Y1, stroke.X2, stroke.Y2, a[0], a[1], b[0], b[1]) > radius+netPathGeomEps {
				continue
			}
			if contour.insideComponent >= 0 {
				components[contour.insideComponent] = true
			}
			if contour.outsideComponent >= 0 {
				components[contour.outsideComponent] = true
			}
		}
	}
	return components
}

func trackIntersectsViaAnnulus(track pcbTrack, via pcbModuleVia) bool {
	if via.DiameterMil <= via.HoleMil || via.HoleMil < 0 || track.Width <= 0 {
		return false
	}
	center := [2]float64{via.X, via.Y}
	inner := math.Max(0, via.HoleMil/2-track.Width/2)
	outer := via.DiameterMil/2 + track.Width/2
	return segmentIntersectsAnnulus([2]float64{track.X1, track.Y1}, [2]float64{track.X2, track.Y2}, center, inner, outer)
}

func segmentIntersectsAnnulus(a, b, center [2]float64, inner, outer float64) bool {
	minimum := segPtDist(center[0], center[1], a[0], a[1], b[0], b[1])
	maximum := math.Max(math.Hypot(a[0]-center[0], a[1]-center[1]), math.Hypot(b[0]-center[0], b[1]-center[1]))
	return minimum <= outer+netPathGeomEps && maximum >= inner-netPathGeomEps
}

func pointInSimplePolygon(points [][2]float64, p [2]float64) bool {
	winding := 0
	for i, a := range points {
		b := points[(i+1)%len(points)]
		cross := (b[0]-a[0])*(p[1]-a[1]) - (p[0]-a[0])*(b[1]-a[1])
		if a[1] <= p[1] && b[1] > p[1] && cross > 0 {
			winding++
		}
		if a[1] > p[1] && b[1] <= p[1] && cross < 0 {
			winding--
		}
	}
	return winding != 0
}

func finishPCBModuleCheck(rep pcbModuleCheckReport) pcbModuleCheckReport {
	sort.Strings(rep.Findings)
	sort.Strings(rep.Limitations)
	rep.Findings = uniqueStrings(rep.Findings)
	rep.Limitations = uniqueStrings(rep.Limitations)
	rep.Summary = fmt.Sprintf("%s: %d check(s), %d finding(s), %d limitation(s)", rep.Status, len(rep.Checks), len(rep.Findings), len(rep.Limitations))
	return rep
}

func verifyModuleJournal(candidate pcbLayoutCandidate, before, after *boardSnapshot, path string) (pcbModuleJournalProof, error) {
	proof := pcbModuleJournalProof{CapturedByStep: map[string]string{}, ModulePourIDs: map[string]bool{}}
	f, err := os.Open(path)
	if err != nil {
		return proof, fmt.Errorf("apply journal: %w", err)
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	var entries []journalEntry
	headerCount := 0
	line := 0
	for s.Scan() {
		line++
		var raw map[string]any
		if err := json.Unmarshal(s.Bytes(), &raw); err != nil {
			return proof, fmt.Errorf("apply journal line %d: %w", line, err)
		}
		if _, isHeader := raw["playbookSha256"]; isHeader {
			headerCount++
			if headerCount != 1 || line != 1 || asString(raw["playbookSha256"]) != candidate.ApplySHA256 {
				return proof, fmt.Errorf("apply journal must contain exactly one first-line header matching candidate.applySha256")
			}
			continue
		}
		var entry journalEntry
		if err := json.Unmarshal(s.Bytes(), &entry); err != nil {
			return proof, fmt.Errorf("apply journal entry line %d: %w", line, err)
		}
		entries = append(entries, entry)
	}
	if err := s.Err(); err != nil {
		return proof, err
	}
	if headerCount != 1 {
		return proof, fmt.Errorf("apply journal has no matching header")
	}
	if len(entries) != len(candidate.Apply.Steps) {
		return proof, fmt.Errorf("apply journal has %d step entries, want exactly %d", len(entries), len(candidate.Apply.Steps))
	}
	stepIDs, capturedPIDs := map[string]bool{}, map[string]bool{}
	for i, step := range candidate.Apply.Steps {
		id := step.ID
		if id == "" {
			id = fmt.Sprintf("s%d", i+1)
		}
		if stepIDs[id] {
			return proof, fmt.Errorf("candidate playbook repeats step %s", id)
		}
		stepIDs[id] = true
		entry := entries[i]
		if entry.ID != id || entry.Idx != i+1 {
			return proof, fmt.Errorf("apply journal entry %d is %s[%d], want %s[%d]", i+1, entry.ID, entry.Idx, id, i+1)
		}
		if !strings.HasPrefix(entry.Status, "ok") {
			return proof, fmt.Errorf("apply journal step %s is not successful", id)
		}
		objectCreate := step.Action == "pcb.line.create" || step.Action == "pcb.via.create" || step.Action == "pcb.region.create" || step.Action == "pcb.pour.create"
		if objectCreate {
			if len(step.Capture) != 1 {
				return proof, fmt.Errorf("candidate object-create step %s must capture exactly one primitiveId", id)
			}
			for _, selector := range step.Capture {
				if selector != "$.primitiveId" {
					return proof, fmt.Errorf("candidate object-create step %s capture must select $.primitiveId", id)
				}
			}
		}
		if len(entry.Captured) != len(step.Capture) {
			return proof, fmt.Errorf("apply journal step %s captured %d values, want exactly %d", id, len(entry.Captured), len(step.Capture))
		}
		for name := range step.Capture {
			pid := entry.Captured[name]
			if pid == "" {
				return proof, fmt.Errorf("apply journal step %s did not capture %s", id, name)
			}
			if capturedPIDs[pid] {
				return proof, fmt.Errorf("apply journal captured PID %s for more than one step", pid)
			}
			capturedPIDs[pid] = true
			if err := verifyCapturedModuleObject(step, pid, before, after); err != nil {
				return proof, fmt.Errorf("apply journal step %s captured PID %s: %w", id, pid, err)
			}
			proof.CapturedByStep[id] = pid
			if step.Action == "pcb.pour.create" {
				proof.ModulePourIDs[pid] = true
			}
		}
	}
	return proof, nil
}

func verifyCapturedModuleObject(step playbookStep, pid string, before, after *boardSnapshot) error {
	if before == nil || after == nil || before.Copper == nil || after.Copper == nil {
		return fmt.Errorf("before/after copper is unavailable")
	}
	if copperPrimitiveIDCount(before, pid) != 0 {
		return fmt.Errorf("already exists in before snapshot")
	}
	count := copperPrimitiveIDCount(after, pid)
	find := func(raw []any) (map[string]any, bool) {
		m, ok := primitiveMapsByID(raw)[pid]
		return m, ok
	}
	switch step.Action {
	case "pcb.line.create":
		if count == 1 {
			m, ok := find(after.Copper.Lines)
			if !ok || !trackMapMatchesPayload(m, step.Payload) {
				return fmt.Errorf("does not exactly match created track net/layer/width/geometry")
			}
			break
		}
		if count != 0 {
			return fmt.Errorf("appears %d times in after copper, want zero or one", count)
		}
		tracks, _, _, err := crystalSnapshotRouting(after)
		if err != nil {
			return fmt.Errorf("captured track was split and fresh routing is unreadable: %w", err)
		}
		matches, covered := matchingTrackCoverage(tracks, asString(step.Payload["net"]), int(asFloat(step.Payload["layer"])), asFloat(step.Payload["lineWidth"]),
			[2]float64{asFloat(step.Payload["startX"]), asFloat(step.Payload["startY"])}, [2]float64{asFloat(step.Payload["endX"]), asFloat(step.Payload["endY"])})
		if !covered || len(matches) < 2 {
			return fmt.Errorf("captured track PID disappeared without an exact continuous split-track replacement")
		}
	case "pcb.via.create":
		if count != 1 {
			return fmt.Errorf("appears %d times in after copper, want exactly once", count)
		}
		m, ok := find(after.Copper.Vias)
		if !ok || !viaMapMatchesPayload(m, step.Payload) {
			return fmt.Errorf("does not exactly match created via net/position/sizes")
		}
	case "pcb.region.create":
		if count != 1 {
			return fmt.Errorf("appears %d times in after copper, want exactly once", count)
		}
		m, ok := find(after.Copper.Regions)
		if !ok {
			return fmt.Errorf("is not a fresh region")
		}
		if err := regionMapMatchesPayload(m, step.Payload); err != nil {
			return err
		}
	case "pcb.pour.create":
		if count != 1 {
			return fmt.Errorf("appears %d times in after copper, want exactly once", count)
		}
		m, ok := find(after.Copper.Pours)
		if !ok {
			return fmt.Errorf("is not a fresh pour")
		}
		if err := pourMapMatchesPayload(m, step.Payload); err != nil {
			return err
		}
	default:
		return fmt.Errorf("step action %s has a capture but no typed object verifier", step.Action)
	}
	return nil
}

func copperPrimitiveIDCount(s *boardSnapshot, pid string) int {
	if s == nil || s.Copper == nil || pid == "" {
		return 0
	}
	count := 0
	for _, raw := range [][]any{s.Copper.Lines, s.Copper.Arcs, s.Copper.Vias, s.Copper.Regions, s.Copper.Pours, s.Copper.Fills} {
		for _, item := range raw {
			m, _ := item.(map[string]any)
			if asString(m["primitiveId"]) == pid {
				count++
			}
		}
	}
	return count
}

func trackMapMatchesPayload(m, payload map[string]any) bool {
	return asString(m["net"]) == asString(payload["net"]) && int(asFloat(m["layer"])) == int(asFloat(payload["layer"])) &&
		math.Abs(asFloat(m["lineWidth"])-asFloat(payload["lineWidth"])) <= netPathGeomEps &&
		sameSegment(asFloat(m["startX"]), asFloat(m["startY"]), asFloat(m["endX"]), asFloat(m["endY"]), asFloat(payload["startX"]), asFloat(payload["startY"]), asFloat(payload["endX"]), asFloat(payload["endY"]))
}

func viaMapMatchesPayload(m, payload map[string]any) bool {
	const viaSizeReadbackEpsMil = 0.051
	return asString(m["net"]) == asString(payload["net"]) && math.Hypot(asFloat(m["x"])-asFloat(payload["x"]), asFloat(m["y"])-asFloat(payload["y"])) <= netPathGeomEps &&
		math.Abs(asFloat(m["holeDiameter"])-asFloat(payload["holeDiameter"])) <= viaSizeReadbackEpsMil && math.Abs(asFloat(m["diameter"])-asFloat(payload["diameter"])) <= viaSizeReadbackEpsMil
}

func regionMapMatchesPayload(m, payload map[string]any) error {
	if m["geometryAvailable"] != true || int(asFloat(m["layer"])) != int(asFloat(payload["layer"])) || !sameStringSet(m["ruleTypeNames"], payload["ruleType"]) {
		return fmt.Errorf("does not exactly match created region layer/rules")
	}
	return requirePolygonEquivalent(m["source"], payload["points"], "created region")
}

func pourMapMatchesPayload(m, payload map[string]any) error {
	if m["geometryAvailable"] != true || asString(m["net"]) != asString(payload["net"]) || int(asFloat(m["layer"])) != int(asFloat(payload["layer"])) {
		return fmt.Errorf("does not exactly match created pour net/layer")
	}
	return requirePolygonEquivalent(m["source"], payload["points"], "created pour")
}

func verifyModuleComponents(candidate pcbLayoutCandidate, before, after *boardSnapshot) error {
	b, a := before.byDesignator(), after.byDesignator()
	owned := map[string]bool{}
	for _, p := range candidate.Placements {
		owned[p.Ref] = true
		got, ok := a[p.Ref]
		if !ok || got.ID != p.PrimitiveID || !layoutAlmostEqual(got.X, p.XMil) || !layoutAlmostEqual(got.Y, p.YMil) || !sameRotation(got.Rotation, p.RotationDeg) || got.Layer != p.Layer {
			return fmt.Errorf("member %s does not match candidate pose/identity", p.Ref)
		}
		if err := verifyPlacementPads(p, got); err != nil {
			return err
		}
	}
	for ref, original := range b {
		if owned[ref] {
			continue
		}
		got, ok := a[ref]
		if !ok || got.ID != original.ID || poseChanged(original, got) || got.Layer != original.Layer {
			return fmt.Errorf("non-member component %s changed", ref)
		}
	}
	for ref := range a {
		if _, existed := b[ref]; !existed {
			return fmt.Errorf("unexpected component %s appeared", ref)
		}
	}
	return nil
}

func verifyPlacementPads(expected pcbLayoutPlacement, actual boardComp) error {
	if len(expected.Pads) == 0 {
		return fmt.Errorf("member %s candidate has no measured pad geometry", expected.Ref)
	}
	byKey := func(p boardPad) string { return strings.ToLower(p.Number) + "\x00" + p.ID }
	want, got := map[string]boardPad{}, map[string]boardPad{}
	for _, p := range expected.Pads {
		key := byKey(p)
		if _, duplicate := want[key]; duplicate || p.ID == "" || p.Number == "" {
			return fmt.Errorf("member %s candidate pad identity is missing or duplicated", expected.Ref)
		}
		want[key] = p
	}
	for _, p := range actual.Pads {
		key := byKey(p)
		if _, duplicate := got[key]; duplicate || p.ID == "" || p.Number == "" {
			return fmt.Errorf("member %s fresh pad identity is missing or duplicated", expected.Ref)
		}
		got[key] = p
	}
	if len(want) != len(got) {
		return fmt.Errorf("member %s fresh pad set differs from candidate: got %d want %d", expected.Ref, len(got), len(want))
	}
	for key, e := range want {
		a, ok := got[key]
		if err := requireKnownPadGeometry(e, expected.Ref+"."+e.Number+" candidate"); err != nil {
			return err
		}
		if err := requireKnownPadGeometry(a, expected.Ref+"."+e.Number+" fresh"); err != nil {
			return err
		}
		// Component anchors preserve sub-mil precision, while the host's fresh pad
		// projection is quantized to 0.1mil. Keep identity/shape exact and allow
		// only that measured-coordinate round-trip error.
		const padReadbackEpsMil = 0.101
		if !ok || e.Net != a.Net || e.Layer != a.Layer || math.Abs(e.X-a.X) > padReadbackEpsMil || math.Abs(e.Y-a.Y) > padReadbackEpsMil || math.Abs(e.W-a.W) > netPathGeomEps || math.Abs(e.H-a.H) > netPathGeomEps || !sameRotation(e.Rotation, a.Rotation) || e.PadType != a.PadType || canonicalJSON(e.Shape) != canonicalJSON(a.Shape) || canonicalJSON(e.SpecialPad) != canonicalJSON(a.SpecialPad) {
			return fmt.Errorf("member %s pad %s fresh identity/geometry differs from candidate", expected.Ref, e.Number)
		}
	}
	return nil
}

func requireKnownPadGeometry(p boardPad, label string) error {
	parsed := pcbPadP{W: p.W, H: p.H, Rotation: p.Rotation}
	if err := parseNetPathPadShape(map[string]any{"shape": p.Shape, "specialPad": p.SpecialPad}, &parsed, label); err != nil || !parsed.ShapeOK {
		if err == nil {
			err = fmt.Errorf("pad shape is unsupported")
		}
		return fmt.Errorf("member pad %s geometry is unknown: %w", label, err)
	}
	return nil
}

func verifyModuleCopper(candidate pcbLayoutCandidate, before, after *boardSnapshot, journal pcbModuleJournalProof) error {
	if candidate.Bundle == nil {
		return fmt.Errorf("missing module bundle")
	}
	if err := verifyCrystalExistingAnchorVias(candidate.Bundle, before, after); err != nil {
		return err
	}
	filtered, ownedErr := verifyPCBModuleOwnedRemoved(before, after, candidate.Bundle.ReplacedObjects)
	if ownedErr != nil {
		return ownedErr
	}
	before = filtered
	bTracks, bArcs, bVias, err := crystalSnapshotRouting(before)
	if err != nil {
		return err
	}
	aTracks, aArcs, aVias, err := crystalSnapshotRouting(after)
	if err != nil {
		return err
	}
	if candidate.Bundle != nil && candidate.Bundle.Kind == "crystal-guard" {
		signalNets := map[string]bool{}
		for _, route := range candidate.Bundle.SignalRoutes {
			if route.Role == "signal-main" || route.Role == "signal-cap" {
				signalNets[route.Net] = true
			}
		}
		if len(signalNets) != 2 {
			return fmt.Errorf("crystal-guard candidate must declare exactly two oscillator signal nets")
		}
		if _, err := validateCrystalReplacementSet(candidate.Bundle.ReplacePrimitiveIDs, signalNets, bTracks, bArcs); err != nil {
			return fmt.Errorf("fresh baseline replacement proof failed: %w", err)
		}
	}
	replace := map[string]bool{}
	for _, id := range candidate.Bundle.ReplacePrimitiveIDs {
		replace[id] = true
	}
	for _, id := range candidate.Bundle.ReflowReplaceIDs {
		replace[id] = true
	}
	aTrackByID := map[string]pcbTrack{}
	for _, t := range aTracks {
		aTrackByID[t.ID] = t
	}
	for _, t := range bTracks {
		if replace[t.ID] {
			if _, still := aTrackByID[t.ID]; still {
				return fmt.Errorf("replaced track %s still exists", t.ID)
			}
			continue
		}
		got, ok := aTrackByID[t.ID]
		if !ok || !sameTrackGeometry(t, got) {
			return fmt.Errorf("non-owned track %s changed or disappeared", t.ID)
		}
	}
	aArcByID := map[string]pcbArc{}
	for _, arc := range aArcs {
		aArcByID[arc.ID] = arc
	}
	for _, arc := range bArcs {
		if replace[arc.ID] {
			if _, still := aArcByID[arc.ID]; still {
				return fmt.Errorf("replaced arc %s still exists", arc.ID)
			}
			continue
		}
		got, ok := aArcByID[arc.ID]
		if !ok || !sameArcGeometry(arc, got) {
			return fmt.Errorf("non-owned arc %s changed or disappeared", arc.ID)
		}
	}
	baselineArcIDs := map[string]bool{}
	for _, arc := range bArcs {
		if !replace[arc.ID] {
			baselineArcIDs[arc.ID] = true
		}
	}
	for _, arc := range aArcs {
		if !baselineArcIDs[arc.ID] {
			return fmt.Errorf("unexpected non-baseline arc %s appeared", arc.ID)
		}
	}
	for _, route := range pcbBundleAllRoutes(candidate.Bundle) {
		for i := 0; i+1 < len(route.Points); i++ {
			p, q := route.Points[i], route.Points[i+1]
			matches, covered := matchingTrackCoverage(aTracks, route.Net, route.Layer, route.WidthMil, p, q)
			if !covered {
				return fmt.Errorf("route %s segment %d is not exactly covered by fresh track geometry (%d collinear part(s))", route.ID, i+1, len(matches))
			}
		}
	}
	baselineTrackIDs := map[string]bool{}
	for _, t := range bTracks {
		if !replace[t.ID] {
			baselineTrackIDs[t.ID] = true
		}
	}
	for _, t := range aTracks {
		if baselineTrackIDs[t.ID] {
			continue
		}
		matched := false
		for _, route := range pcbBundleAllRoutes(candidate.Bundle) {
			for i := 0; i+1 < len(route.Points); i++ {
				p, q := route.Points[i], route.Points[i+1]
				if trackIsSubsetOfSegment(t, route.Net, route.Layer, route.WidthMil, p, q) {
					matched = true
				}
			}
		}
		if !matched {
			return fmt.Errorf("unexpected non-baseline track %s appeared", t.ID)
		}
	}
	bViaByID, aViaByID := map[string]pcbViaP{}, map[string]pcbViaP{}
	for _, v := range bVias {
		bViaByID[v.ID] = v
	}
	for _, v := range aVias {
		aViaByID[v.ID] = v
	}
	for id, v := range bViaByID {
		got, ok := aViaByID[id]
		if replace[id] {
			if ok {
				return fmt.Errorf("replaced via %s still exists", id)
			}
			continue
		}
		if !ok || !sameViaGeometry(v, got) {
			return fmt.Errorf("baseline via %s changed or disappeared", id)
		}
	}
	for _, expected := range pcbBundleAllVias(candidate.Bundle) {
		matches := 0
		for _, v := range aVias {
			if moduleViaMatchesExpected(v, expected) {
				matches++
			}
		}
		if matches != 1 {
			return fmt.Errorf("via %s has %d exact matches, want 1", expected.ID, matches)
		}
	}
	for _, v := range aVias {
		if _, existed := bViaByID[v.ID]; existed {
			continue
		}
		matched := false
		for _, expected := range pcbBundleAllVias(candidate.Bundle) {
			if moduleViaMatchesExpected(v, expected) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("unexpected non-baseline via %s appeared", v.ID)
		}
	}
	if err := verifyAreaObjects(candidate.Bundle.Regions, after.Copper.Regions, "region"); err != nil {
		return err
	}
	if err := verifyPourObjects(candidate.Bundle.Pours, after.Copper.Pours, after.Copper.Poured); err != nil {
		return err
	}
	for _, group := range []struct {
		name          string
		before, after []any
	}{
		{"region", before.Copper.Regions, after.Copper.Regions}, {"fill", before.Copper.Fills, after.Copper.Fills}, {"pour", before.Copper.Pours, after.Copper.Pours},
	} {
		aByID := primitiveMapsByID(group.after)
		for id, original := range primitiveMapsByID(group.before) {
			got, ok := aByID[id]
			if !ok {
				return fmt.Errorf("baseline %s %s changed or disappeared", group.name, id)
			}
			var sameErr error
			switch group.name {
			case "region":
				sameErr = compareRegionMaps(original, got)
			case "pour":
				sameErr = comparePourMaps(original, got)
			case "fill":
				sameErr = compareAreaMaps(original, got, "fill")
			}
			if sameErr != nil {
				return fmt.Errorf("baseline %s %s changed: %w", group.name, id, sameErr)
			}
		}
		beforeIDs := primitiveMapsByID(group.before)
		for id, got := range aByID {
			if _, existed := beforeIDs[id]; existed {
				continue
			}
			expected := false
			switch group.name {
			case "region":
				var matchErr error
				expected, matchErr = regionMapMatchesAny(got, candidate.Bundle.Regions)
				if matchErr != nil {
					return matchErr
				}
			case "pour":
				var matchErr error
				expected, matchErr = pourMapMatchesAny(got, candidate.Bundle.Pours)
				if matchErr != nil {
					return matchErr
				}
			case "fill":
				expected = false // crystal-guard never creates a static fill
			}
			if !expected {
				return fmt.Errorf("unexpected non-baseline %s %s appeared", group.name, id)
			}
		}
	}
	if err := verifyMaterializedPourPreservation(candidate, before.Copper.Poured, after.Copper.Poured, journal.ModulePourIDs); err != nil {
		return err
	}
	return nil
}

func moduleViaMatchesExpected(actual pcbViaP, expected pcbModuleVia) bool {
	// The via list rounds physical drill/diameter conversion to 0.1mil while
	// keeping the center at full precision (for example 12.01/24.02 -> 12/24).
	const viaSizeReadbackEpsMil = 0.051
	return actual.Net == expected.Net && math.Hypot(actual.X-expected.X, actual.Y-expected.Y) <= netPathGeomEps &&
		math.Abs(actual.Hole-expected.HoleMil) <= viaSizeReadbackEpsMil && math.Abs(actual.Dia-expected.DiameterMil) <= viaSizeReadbackEpsMil
}

func regionMapMatchesAny(m map[string]any, expected []pcbModuleRegion) (bool, error) {
	if m["geometryAvailable"] != true {
		return false, fmt.Errorf("region %s geometry is unknown", asString(m["primitiveId"]))
	}
	for _, e := range expected {
		if int(asFloat(m["layer"])) != e.Layer || !sameStringSet(m["ruleTypeNames"], e.RuleTypes) {
			continue
		}
		equal, err := polygonSourcesEquivalent(m["source"], pointsPolygonSource(e.Points))
		if err != nil {
			return false, fmt.Errorf("region %s geometry is unknown: %w", asString(m["primitiveId"]), err)
		}
		if equal {
			return true, nil
		}
	}
	return false, nil
}

func pourMapMatchesAny(m map[string]any, expected []pcbModulePour) (bool, error) {
	if m["geometryAvailable"] != true {
		return false, fmt.Errorf("pour %s geometry is unknown", asString(m["primitiveId"]))
	}
	for _, e := range expected {
		if asString(m["net"]) != e.Net || int(asFloat(m["layer"])) != e.Layer {
			continue
		}
		equal, err := polygonSourcesEquivalent(m["source"], pointsPolygonSource(e.Points))
		if err != nil {
			return false, fmt.Errorf("pour %s geometry is unknown: %w", asString(m["primitiveId"]), err)
		}
		if equal {
			return true, nil
		}
	}
	return false, nil
}

func verifyAreaObjects(expected []pcbModuleRegion, actual []any, kind string) error {
	for _, e := range expected {
		matches := 0
		for _, raw := range actual {
			m, _ := raw.(map[string]any)
			if int(asFloat(m["layer"])) != e.Layer || m["geometryAvailable"] != true {
				continue
			}
			equal, err := polygonSourcesEquivalent(m["source"], pointsPolygonSource(e.Points))
			if err != nil {
				return fmt.Errorf("%s %s geometry is unknown: %w", kind, asString(m["primitiveId"]), err)
			}
			if equal && sameStringSet(m["ruleTypeNames"], e.RuleTypes) {
				matches++
			}
		}
		if matches != 1 {
			return fmt.Errorf("%s %s has %d exact geometry/rule matches, want 1", kind, e.ID, matches)
		}
	}
	return nil
}

func verifyPourObjects(expected []pcbModulePour, pours, poured []any) error {
	for _, e := range expected {
		if _, err := findExpectedMaterializedPour(e, pours, poured); err != nil {
			return err
		}
	}
	return nil
}

func verifyModuleKeepoutEmpty(candidate pcbLayoutCandidate, after *boardSnapshot) error {
	if candidate.Bundle != nil && candidate.Bundle.Kind == "crystal-guard" {
		groundNet := ""
		for _, route := range candidate.Bundle.GroundRoutes {
			if route.Net != "" {
				groundNet = route.Net
				break
			}
		}
		if groundNet == "" {
			return fmt.Errorf("crystal-guard has no ground-route net")
		}
		if err := verifyCrystalProtectionStructure(candidate, groundNet); err != nil {
			return err
		}
	}
	keepouts, err := resolveModuleNoPourRegions(candidate, after)
	if err != nil {
		return err
	}
	areas, thermalStrokes, err := parseModuleCopperAreasAndStrokes(after.Copper.Fills, after.Copper.Pours, after.Copper.Poured)
	if err != nil {
		return fmt.Errorf("materialized/static copper geometry is unknown: %w", err)
	}
	for _, area := range areas {
		for _, ko := range keepouts {
			if area.Layer != ko.layer {
				continue
			}
			inside, err := copperAreaIntersectsSimplePolygon(area, ko.points)
			if err != nil {
				return fmt.Errorf("%s %s geometry is unknown: %w", area.Kind, area.ID, err)
			}
			if inside {
				if area.Kind == "static fill" {
					return fmt.Errorf("static fill enters no-pours region %s on layer %d (primitive %s)", ko.id, ko.layer, area.ID)
				}
				if area.Kind == "materialized poured copper" {
					return fmt.Errorf("materialized poured copper enters no-pours region %s on layer %d (fill %s)", ko.id, ko.layer, area.ID)
				}
				return fmt.Errorf("%s %s enters no-pours region %s on layer %d", area.Kind, area.ID, ko.id, ko.layer)
			}
		}
	}
	for _, stroke := range thermalStrokes {
		for _, ko := range keepouts {
			if stroke.Layer == ko.layer && strokeIntersectsSimplePolygon(stroke, ko.points) {
				return fmt.Errorf("poured copper thermal stroke %s enters no-pours region %s on layer %d", stroke.ID, ko.id, ko.layer)
			}
		}
	}
	return nil
}

func copperAreaIntersectsSimplePolygon(area pcbCopperArea, polygon [][2]float64) (bool, error) {
	topology, err := buildCompoundPolygonTopology(area.Contours)
	if err != nil {
		return false, err
	}
	// Boundary contact is the expected result of a rebuilt pour against a
	// no-pours region. Only prove intrusion from points strictly inside the
	// opposite material; coincident/tangent boundaries are not copper area.
	for i, point := range polygon {
		if topology.componentAt(point) >= 0 {
			return true, nil
		}
		next := polygon[(i+1)%len(polygon)]
		if topology.componentAt([2]float64{(point[0] + next[0]) / 2, (point[1] + next[1]) / 2}) >= 0 {
			return true, nil
		}
	}
	for _, contour := range area.Contours {
		for i, point := range contour {
			if pointStrictlyInsideSimplePolygon(polygon, point) {
				return true, nil
			}
			next := contour[(i+1)%len(contour)]
			if pointStrictlyInsideSimplePolygon(polygon, [2]float64{(point[0] + next[0]) / 2, (point[1] + next[1]) / 2}) {
				return true, nil
			}
		}
	}
	return false, nil
}

func pointStrictlyInsideSimplePolygon(polygon [][2]float64, point [2]float64) bool {
	for i, a := range polygon {
		b := polygon[(i+1)%len(polygon)]
		if segPtDist(point[0], point[1], a[0], a[1], b[0], b[1]) <= netPathGeomEps {
			return false
		}
	}
	return pointInSimplePolygon(polygon, point)
}

func strokeIntersectsSimplePolygon(stroke pcbTrack, polygon [][2]float64) bool {
	a, b := [2]float64{stroke.X1, stroke.Y1}, [2]float64{stroke.X2, stroke.Y2}
	if pointInOrOnSimplePolygon(polygon, a) || pointInOrOnSimplePolygon(polygon, b) {
		return true
	}
	radius := stroke.Width / 2
	for i, c := range polygon {
		d := polygon[(i+1)%len(polygon)]
		if segSegDist(a[0], a[1], b[0], b[1], c[0], c[1], d[0], d[1]) <= radius+netPathGeomEps {
			return true
		}
	}
	return false
}

func sameTrackGeometry(a, b pcbTrack) bool {
	return a.Net == b.Net && a.Layer == b.Layer && math.Abs(a.Width-b.Width) <= netPathGeomEps && sameSegment(a.X1, a.Y1, a.X2, a.Y2, b.X1, b.Y1, b.X2, b.Y2)
}

func sameArcGeometry(a, b pcbArc) bool {
	return a.Net == b.Net && a.Layer == b.Layer && math.Abs(a.Width-b.Width) <= netPathGeomEps && math.Abs(a.ArcAngle-b.ArcAngle) <= netPathGeomEps &&
		math.Hypot(a.X1-b.X1, a.Y1-b.Y1) <= netPathGeomEps && math.Hypot(a.X2-b.X2, a.Y2-b.Y2) <= netPathGeomEps
}

func sameViaGeometry(a, b pcbViaP) bool {
	return a.Net == b.Net && math.Hypot(a.X-b.X, a.Y-b.Y) <= netPathGeomEps && math.Abs(a.Hole-b.Hole) <= netPathGeomEps && math.Abs(a.Dia-b.Dia) <= netPathGeomEps
}

func sameSegment(ax, ay, bx, by, cx, cy, dx, dy float64) bool {
	direct := math.Hypot(ax-cx, ay-cy) <= netPathGeomEps && math.Hypot(bx-dx, by-dy) <= netPathGeomEps
	reverse := math.Hypot(ax-dx, ay-dy) <= netPathGeomEps && math.Hypot(bx-cx, by-cy) <= netPathGeomEps
	return direct || reverse
}

// matchingTrackCoverage accepts the stable geometry contract of a created
// route even when EasyEDA splits one line at later same-net junctions and gives
// the pieces new primitive IDs. Every accepted piece must stay on the declared
// segment and the union must cover it without a gap; nearby or merely touching
// copper cannot satisfy this proof.
func matchingTrackCoverage(tracks []pcbTrack, net string, layer int, width float64, a, b [2]float64) ([]pcbTrack, bool) {
	length := math.Hypot(b[0]-a[0], b[1]-a[1])
	if length <= netPathGeomEps {
		return nil, false
	}
	type interval struct {
		lo, hi float64
		track  pcbTrack
	}
	var intervals []interval
	ux, uy := (b[0]-a[0])/length, (b[1]-a[1])/length
	for _, track := range tracks {
		if !trackIsSubsetOfSegment(track, net, layer, width, a, b) {
			continue
		}
		t0 := (track.X1-a[0])*ux + (track.Y1-a[1])*uy
		t1 := (track.X2-a[0])*ux + (track.Y2-a[1])*uy
		if t0 > t1 {
			t0, t1 = t1, t0
		}
		intervals = append(intervals, interval{lo: math.Max(0, t0), hi: math.Min(length, t1), track: track})
	}
	if len(intervals) == 0 {
		return nil, false
	}
	sort.SliceStable(intervals, func(i, j int) bool {
		if math.Abs(intervals[i].lo-intervals[j].lo) <= netPathGeomEps {
			return intervals[i].hi < intervals[j].hi
		}
		return intervals[i].lo < intervals[j].lo
	})
	covered := 0.0
	var matches []pcbTrack
	for _, item := range intervals {
		if item.lo > covered+netPathGeomEps {
			return matches, false
		}
		matches = append(matches, item.track)
		if item.hi > covered {
			covered = item.hi
		}
	}
	return matches, covered >= length-netPathGeomEps
}

func trackIsSubsetOfSegment(track pcbTrack, net string, layer int, width float64, a, b [2]float64) bool {
	if track.Net != net || track.Layer != layer || math.Abs(track.Width-width) > netPathGeomEps {
		return false
	}
	length := math.Hypot(b[0]-a[0], b[1]-a[1])
	if length <= netPathGeomEps {
		return false
	}
	ux, uy := (b[0]-a[0])/length, (b[1]-a[1])/length
	for _, point := range [][2]float64{{track.X1, track.Y1}, {track.X2, track.Y2}} {
		projection := (point[0]-a[0])*ux + (point[1]-a[1])*uy
		perpendicular := math.Abs((point[0]-a[0])*uy - (point[1]-a[1])*ux)
		if perpendicular > netPathGeomEps || projection < -netPathGeomEps || projection > length+netPathGeomEps {
			return false
		}
	}
	return true
}

func primitiveMapsByID(raw []any) map[string]map[string]any {
	return primitiveMapsByField(raw, "primitiveId")
}
func primitiveMapsByField(raw []any, field string) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if ok && asString(m[field]) != "" {
			out[asString(m[field])] = m
		}
	}
	return out
}

func canonicalJSON(v any) string { raw, _ := json.Marshal(v); return string(raw) }

func sameStringSet(a, b any) bool {
	stringsFrom := func(raw any) []string {
		converted, err := jsonNormalizedAny(raw)
		if err != nil {
			return nil
		}
		items, _ := converted.([]any)
		out := make([]string, 0, len(items))
		for _, item := range items {
			out = append(out, canonicalJSON(item))
		}
		sort.Strings(out)
		return out
	}
	return canonicalJSON(stringsFrom(a)) == canonicalJSON(stringsFrom(b))
}

func jsonNormalizedAny(v any) (any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func pointsPolygonSource(points [][2]float64) []any {
	if len(points) == 0 {
		return nil
	}
	out := []any{points[0][0], points[0][1], "L"}
	for _, p := range points[1:] {
		out = append(out, p[0], p[1])
	}
	out = append(out, points[0][0], points[0][1])
	return out
}

func polygonPayloadSource(raw any) (any, error) {
	v, err := jsonNormalizedAny(raw)
	if err != nil {
		return nil, err
	}
	items, ok := v.([]any)
	if !ok || len(items) < 3 {
		return nil, fmt.Errorf("points are not a polygon vertex array")
	}
	points := make([][2]float64, 0, len(items))
	for _, item := range items {
		pair, ok := item.([]any)
		if !ok || len(pair) != 2 {
			return nil, fmt.Errorf("polygon vertex is not an x/y pair")
		}
		x, xok := asFloatOK(pair[0])
		y, yok := asFloatOK(pair[1])
		if !xok || !yok {
			return nil, fmt.Errorf("polygon vertex is not numeric")
		}
		points = append(points, [2]float64{x, y})
	}
	return pointsPolygonSource(points), nil
}

func requirePolygonEquivalent(actual, expected any, label string) error {
	expectedSource, err := polygonPayloadSource(expected)
	if err != nil {
		return fmt.Errorf("%s candidate geometry is invalid: %w", label, err)
	}
	equal, err := polygonSourcesEquivalent(actual, expectedSource)
	if err != nil {
		return fmt.Errorf("%s geometry is unknown: %w", label, err)
	}
	if !equal {
		return fmt.Errorf("%s polygon/contours differ", label)
	}
	return nil
}

func polygonSourcesEquivalent(a, b any) (bool, error) {
	ca, err := polygonSourceContours(a)
	if err != nil {
		return false, err
	}
	cb, err := polygonSourceContours(b)
	if err != nil {
		return false, err
	}
	return polygonContourSetsNear(ca, cb, 1, netPathGeomEps) || polygonContourSetsNear(ca, cb, -1, netPathGeomEps), nil
}

func polygonContourSetsNear(a, b [][][2]float64, direction int, eps float64) bool {
	if len(a) != len(b) {
		return false
	}
	used := make([]bool, len(b))
	for _, rawA := range a {
		aPoints := removeCollinearPolygonPoints(rawA)
		matched := false
		for j, rawB := range b {
			if used[j] {
				continue
			}
			if cyclicPolygonPointsNearDirection(aPoints, removeCollinearPolygonPoints(rawB), direction, eps) {
				used[j], matched = true, true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func cyclicPolygonPointsNearDirection(a, b [][2]float64, direction int, eps float64) bool {
	if len(a) != len(b) || len(a) < 3 {
		return false
	}
	near := func(p, q [2]float64) bool { return math.Hypot(p[0]-q[0], p[1]-q[1]) <= eps }
	for start := range b {
		if !near(a[0], b[start]) {
			continue
		}
		ok := true
		for i := range a {
			j := (start + direction*i) % len(b)
			if j < 0 {
				j += len(b)
			}
			if !near(a[i], b[j]) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func canonicalPolygonSource(raw any) (string, error) {
	normalized, err := jsonNormalizedAny(raw)
	if err != nil {
		return "", err
	}
	contours, err := polygonSourceContours(normalized)
	if err != nil {
		return "", err
	}
	forward := canonicalPolygonContours(contours, false)
	reversed := canonicalPolygonContours(contours, true)
	if reversed < forward {
		return reversed, nil
	}
	return forward, nil
}

func canonicalPolygonContours(contours [][][2]float64, reverseAll bool) string {
	parts := make([]string, 0, len(contours))
	for _, contour := range contours {
		points := removeCollinearPolygonPoints(contour)
		if len(points) < 3 {
			continue
		}
		if reverseAll {
			for left, right := 0, len(points)-1; left < right; left, right = left+1, right-1 {
				points[left], points[right] = points[right], points[left]
			}
		}
		vertices := make([]string, len(points))
		for i, p := range points {
			x := int64(math.Round(p[0] / netPathGeomEps))
			y := int64(math.Round(p[1] / netPathGeomEps))
			vertices[i] = fmt.Sprintf("%d,%d", x, y)
		}
		best := ""
		for start := range vertices {
			rotated := make([]string, 0, len(vertices))
			rotated = append(rotated, vertices[start:]...)
			rotated = append(rotated, vertices[:start]...)
			candidate := strings.Join(rotated, ";")
			if best == "" || candidate < best {
				best = candidate
			}
		}
		parts = append(parts, best)
	}
	sort.Strings(parts)
	return strings.Join(parts, "|")
}

func removeCollinearPolygonPoints(points [][2]float64) [][2]float64 {
	if len(points) < 3 {
		return append([][2]float64(nil), points...)
	}
	clean := make([][2]float64, 0, len(points))
	for _, point := range points {
		if len(clean) == 0 || math.Hypot(point[0]-clean[len(clean)-1][0], point[1]-clean[len(clean)-1][1]) > netPathGeomEps {
			clean = append(clean, point)
		}
	}
	if len(clean) > 1 && math.Hypot(clean[0][0]-clean[len(clean)-1][0], clean[0][1]-clean[len(clean)-1][1]) <= netPathGeomEps {
		clean = clean[:len(clean)-1]
	}
	changed := true
	for changed && len(clean) >= 3 {
		changed = false
		out := make([][2]float64, 0, len(clean))
		for i, current := range clean {
			previous := clean[(i+len(clean)-1)%len(clean)]
			next := clean[(i+1)%len(clean)]
			ax, ay := current[0]-previous[0], current[1]-previous[1]
			bx, by := next[0]-current[0], next[1]-current[1]
			cross := math.Abs(ax*by - ay*bx)
			scale := math.Max(1, math.Hypot(ax, ay)+math.Hypot(bx, by))
			if cross <= netPathGeomEps*scale && ax*bx+ay*by >= -netPathGeomEps {
				changed = true
				continue
			}
			out = append(out, current)
		}
		clean = out
	}
	return clean
}

func mapWithout(m map[string]any, keys ...string) map[string]any {
	remove := map[string]bool{}
	for _, key := range keys {
		remove[key] = true
	}
	out := map[string]any{}
	for key, value := range m {
		if !remove[key] {
			out[key] = value
		}
	}
	return out
}

func compareRegionMaps(a, b map[string]any) error {
	if a["geometryAvailable"] != true || b["geometryAvailable"] != true {
		return fmt.Errorf("region geometry is unknown")
	}
	if int(asFloat(a["layer"])) != int(asFloat(b["layer"])) || !sameStringSet(a["ruleType"], b["ruleType"]) || !sameStringSet(a["ruleTypeNames"], b["ruleTypeNames"]) {
		return fmt.Errorf("layer/rules differ")
	}
	if err := compareReadableSemanticFields(a, b, "region", "regionName", "lineWidth", "locked"); err != nil {
		return err
	}
	equal, err := polygonSourcesEquivalent(a["source"], b["source"])
	if err != nil {
		return fmt.Errorf("polygon is unknown: %w", err)
	}
	if !equal {
		return fmt.Errorf("polygon/contours differ")
	}
	return nil
}

func comparePourMaps(a, b map[string]any) error {
	if asString(a["net"]) != asString(b["net"]) || int(asFloat(a["layer"])) != int(asFloat(b["layer"])) {
		return fmt.Errorf("net/layer differ")
	}
	if err := compareReadableSemanticFields(a, b, "pour", "pourName", "fillMethod", "priority", "lineWidth", "locked"); err != nil {
		return err
	}
	return compareAreaMaps(a, b, "pour")
}

func compareAreaMaps(a, b map[string]any, kind string) error {
	if kind == "fill" {
		if asString(a["net"]) != asString(b["net"]) || int(asFloat(a["layer"])) != int(asFloat(b["layer"])) {
			return fmt.Errorf("fill net/layer differ")
		}
		if err := compareReadableSemanticFields(a, b, "fill", "fillMode", "lineWidth", "locked"); err != nil {
			return err
		}
	}
	if a["geometryAvailable"] != true || b["geometryAvailable"] != true {
		return fmt.Errorf("%s geometry is unknown", kind)
	}
	equal, err := polygonSourcesEquivalent(a["source"], b["source"])
	if err != nil {
		return fmt.Errorf("%s polygon is unknown: %w", kind, err)
	}
	if !equal {
		return fmt.Errorf("%s polygon/contours differ", kind)
	}
	return nil
}

func compareReadableSemanticFields(a, b map[string]any, kind string, fields ...string) error {
	for _, field := range fields {
		av, aok := a[field]
		bv, bok := b[field]
		if !aok || !bok {
			return fmt.Errorf("%s semantic field %s is unavailable", kind, field)
		}
		if canonicalJSON(av) != canonicalJSON(bv) {
			return fmt.Errorf("%s semantic field %s differs", kind, field)
		}
	}
	return nil
}

func verifyMaterializedPourPreservation(candidate pcbLayoutCandidate, before, after []any, modulePourIDs map[string]bool) error {
	group := func(raw []any) (map[string]map[string]any, error) {
		out := map[string]map[string]any{}
		for _, item := range raw {
			m, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("materialized poured object is not an object")
			}
			id := asString(m["pourPrimitiveId"])
			if id == "" {
				return nil, fmt.Errorf("materialized poured object has no pourPrimitiveId")
			}
			if _, duplicate := out[id]; duplicate {
				return nil, fmt.Errorf("materialized pour %s appears more than once", id)
			}
			out[id] = m
		}
		return out, nil
	}
	b, err := group(before)
	if err != nil {
		return err
	}
	a, err := group(after)
	if err != nil {
		return err
	}
	affected := map[string]pcbModuleAffectedPour{}
	if candidate.Bundle != nil {
		for _, declaration := range candidate.Bundle.AffectedBaselinePours {
			id := declaration.BoundaryPrimitiveID
			if id == "" || declaration.MaterializedPrimitiveID == "" || declaration.Net == "" || !netPathCopperLayer(declaration.Layer) || !validLayoutBBox(declaration.ImpactEnvelope) {
				return fmt.Errorf("affected baseline pour declaration is incomplete")
			}
			if _, duplicate := affected[id]; duplicate {
				return fmt.Errorf("affected baseline pour %s is declared more than once", id)
			}
			original, ok := b[id]
			if !ok || asString(original["primitiveId"]) != declaration.MaterializedPrimitiveID || asString(original["net"]) != declaration.Net || int(asFloat(original["layer"])) != declaration.Layer {
				return fmt.Errorf("affected baseline pour %s declaration does not match fresh before materialized copper", id)
			}
			affected[id] = declaration
		}
	}
	for pourID, original := range b {
		got, ok := a[pourID]
		if !ok {
			return fmt.Errorf("baseline materialized pour %s disappeared", pourID)
		}
		var compareErr error
		if declaration, declared := affected[pourID]; declared {
			compareErr = compareMaterializedPourOutsideEnvelope(original, got, declaration.ImpactEnvelope)
		} else {
			compareErr = compareMaterializedPourMaps(original, got)
		}
		if compareErr != nil {
			return fmt.Errorf("baseline materialized pour %s changed: %w", pourID, compareErr)
		}
	}
	for pourID := range a {
		if _, existed := b[pourID]; existed {
			continue
		}
		if !modulePourIDs[pourID] {
			return fmt.Errorf("unexpected materialized pour for non-owned pour %s appeared", pourID)
		}
	}
	if candidate.Bundle != nil && len(candidate.Bundle.Pours) > 0 && len(modulePourIDs) != len(candidate.Bundle.Pours) {
		return fmt.Errorf("journal proves %d owned pour PID(s), candidate declares %d", len(modulePourIDs), len(candidate.Bundle.Pours))
	}
	return nil
}

func validLayoutBBox(box layoutBBox) bool {
	return allFinite(box.MinX, box.MinY, box.MaxX, box.MaxY) && box.MinX < box.MaxX && box.MinY < box.MaxY
}

func compareMaterializedPourMaps(a, b map[string]any) error {
	if asString(a["net"]) != asString(b["net"]) || int(asFloat(a["layer"])) != int(asFloat(b["layer"])) {
		return fmt.Errorf("net/layer differ")
	}
	fillSignatures := func(raw any) ([]string, error) {
		fills, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf("fills are unknown")
		}
		out := make([]string, 0, len(fills))
		for _, item := range fills {
			m, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("fill is not an object")
			}
			sig, err := canonicalPolygonSource(m["source"])
			if err != nil {
				return nil, fmt.Errorf("fill polygon is unknown: %w", err)
			}
			out = append(out, sig+"#"+canonicalJSON(mapWithout(m, "source")))
		}
		sort.Strings(out)
		return out, nil
	}
	af, err := fillSignatures(a["fills"])
	if err != nil {
		return err
	}
	bf, err := fillSignatures(b["fills"])
	if err != nil {
		return err
	}
	if canonicalJSON(af) != canonicalJSON(bf) {
		return fmt.Errorf("materialized fill polygons/holes/arcs differ")
	}
	return nil
}

func compareMaterializedPourOutsideEnvelope(a, b map[string]any, envelope layoutBBox) error {
	if asString(a["net"]) != asString(b["net"]) || int(asFloat(a["layer"])) != int(asFloat(b["layer"])) {
		return fmt.Errorf("net/layer differ")
	}
	aSignatures, err := materializedOutsideSignatures(a["fills"], envelope)
	if err != nil {
		return fmt.Errorf("before outside geometry is unknown: %w", err)
	}
	bSignatures, err := materializedOutsideSignatures(b["fills"], envelope)
	if err != nil {
		return fmt.Errorf("after outside geometry is unknown: %w", err)
	}
	if canonicalJSON(aSignatures) != canonicalJSON(bSignatures) {
		return fmt.Errorf("materialized geometry/holes outside declared impact envelope differ")
	}
	return nil
}

func materializedOutsideSignatures(raw any, envelope layoutBBox) ([]string, error) {
	fills, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("fills are unavailable")
	}
	bySemantics := map[string][][][2]float64{}
	for _, item := range fills {
		fill, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("fill is not an object")
		}
		lineWidth, widthOK := fill["lineWidth"]
		fillState, fillOK := fill["fill"]
		if !widthOK || !fillOK {
			return nil, fmt.Errorf("fill lineWidth/fill semantics are unavailable")
		}
		contours, err := polygonSourceContours(fill["source"])
		if err != nil {
			return nil, err
		}
		semantic := canonicalJSON([]any{lineWidth, fillState})
		for _, contour := range contours {
			bySemantics[semantic] = append(bySemantics[semantic], clipContourOutsideEnvelope(contour, envelope)...)
		}
	}
	var result []string
	for semantic, contours := range bySemantics {
		var nonempty [][][2]float64
		for _, contour := range contours {
			contour = removeCollinearPolygonPoints(contour)
			if len(contour) >= 3 && math.Abs(polygonSignedArea(contour)) > netPathGeomEps*netPathGeomEps {
				nonempty = append(nonempty, contour)
			}
		}
		if len(nonempty) > 0 {
			forward, reverse := canonicalPolygonContours(nonempty, false), canonicalPolygonContours(nonempty, true)
			if reverse < forward {
				forward = reverse
			}
			result = append(result, semantic+"#"+forward)
		}
	}
	sort.Strings(result)
	return result, nil
}

func clipContourOutsideEnvelope(contour [][2]float64, envelope layoutBBox) [][][2]float64 {
	type halfPlane struct {
		inside    func([2]float64) bool
		intersect func([2]float64, [2]float64) [2]float64
	}
	vertical := func(x float64, keepLess bool) halfPlane {
		return halfPlane{
			inside: func(p [2]float64) bool {
				if keepLess {
					return p[0] <= x+netPathGeomEps
				}
				return p[0] >= x-netPathGeomEps
			},
			intersect: func(a, b [2]float64) [2]float64 {
				t := (x - a[0]) / (b[0] - a[0])
				return [2]float64{x, a[1] + t*(b[1]-a[1])}
			},
		}
	}
	horizontal := func(y float64, keepLess bool) halfPlane {
		return halfPlane{
			inside: func(p [2]float64) bool {
				if keepLess {
					return p[1] <= y+netPathGeomEps
				}
				return p[1] >= y-netPathGeomEps
			},
			intersect: func(a, b [2]float64) [2]float64 {
				t := (y - a[1]) / (b[1] - a[1])
				return [2]float64{a[0] + t*(b[0]-a[0]), y}
			},
		}
	}
	regions := [][]halfPlane{
		{vertical(envelope.MinX, true)},
		{vertical(envelope.MaxX, false)},
		{vertical(envelope.MinX, false), vertical(envelope.MaxX, true), horizontal(envelope.MinY, true)},
		{vertical(envelope.MinX, false), vertical(envelope.MaxX, true), horizontal(envelope.MaxY, false)},
	}
	var result [][][2]float64
	for _, region := range regions {
		clipped := append([][2]float64(nil), contour...)
		for _, plane := range region {
			clipped = clipPolygonHalfPlane(clipped, plane.inside, plane.intersect)
			if len(clipped) < 3 {
				break
			}
		}
		if len(clipped) >= 3 {
			result = append(result, clipped)
		}
	}
	return result
}

func clipPolygonHalfPlane(points [][2]float64, inside func([2]float64) bool, intersect func([2]float64, [2]float64) [2]float64) [][2]float64 {
	if len(points) == 0 {
		return nil
	}
	var out [][2]float64
	previous := points[len(points)-1]
	previousInside := inside(previous)
	for _, current := range points {
		currentInside := inside(current)
		if currentInside != previousInside {
			out = append(out, intersect(previous, current))
		}
		if currentInside {
			out = append(out, current)
		}
		previous, previousInside = current, currentInside
	}
	return out
}

func polygonSignedArea(points [][2]float64) float64 {
	area := 0.0
	for i, p := range points {
		q := points[(i+1)%len(points)]
		area += p[0]*q[1] - q[0]*p[1]
	}
	return area / 2
}

func pointsBBox(points [][2]float64) layoutBBox {
	b := layoutBBox{MinX: math.Inf(1), MinY: math.Inf(1), MaxX: math.Inf(-1), MaxY: math.Inf(-1)}
	for _, p := range points {
		b.MinX = math.Min(b.MinX, p[0])
		b.MinY = math.Min(b.MinY, p[1])
		b.MaxX = math.Max(b.MaxX, p[0])
		b.MaxY = math.Max(b.MaxY, p[1])
	}
	return b
}

func polygonSourceBBox(raw any) (layoutBBox, error) {
	contours, err := polygonSourceContours(raw)
	if err != nil {
		return layoutBBox{}, err
	}
	var all [][2]float64
	for _, c := range contours {
		all = append(all, c...)
	}
	if len(all) < 3 {
		return layoutBBox{}, fmt.Errorf("polygon has fewer than 3 points")
	}
	return pointsBBox(all), nil
}

func sameBBox(a, b layoutBBox) bool {
	return math.Abs(a.MinX-b.MinX) <= netPathGeomEps && math.Abs(a.MinY-b.MinY) <= netPathGeomEps && math.Abs(a.MaxX-b.MaxX) <= netPathGeomEps && math.Abs(a.MaxY-b.MaxY) <= netPathGeomEps
}

// pcbSourcePath is one parsed source path. closed reports that the source
// returned to its start point (the duplicate closing vertex is removed).
type pcbSourcePath struct {
	points [][2]float64
	closed bool
}

// polygonSourceContours parses a filled polygon source: every path is a
// closed contour of at least three vertices.
func polygonSourceContours(raw any) ([][][2]float64, error) {
	paths, err := polygonSourcePaths(raw)
	if err != nil {
		return nil, err
	}
	out := make([][][2]float64, 0, len(paths))
	for _, path := range paths {
		if len(path.points) < 3 {
			return nil, fmt.Errorf("polygon has fewer than 3 vertices")
		}
		out = append(out, path.points)
	}
	return out, nil
}

// polygonSourcePaths parses the same source grammar without requiring a
// polygon: a stroked path may be open and may have only two points.
func polygonSourcePaths(raw any) ([]pcbSourcePath, error) {
	a, ok := raw.([]any)
	if !ok || len(a) == 0 {
		return nil, fmt.Errorf("polygon source is not an array")
	}
	if _, nested := a[0].([]any); nested {
		var out []pcbSourcePath
		for _, item := range a {
			c, err := polygonSourcePaths(item)
			if err != nil {
				return nil, err
			}
			out = append(out, c...)
		}
		return out, nil
	}
	if len(a) < 5 {
		return nil, fmt.Errorf("polygon source is too short")
	}
	x, xok := asFloatOK(a[0])
	y, yok := asFloatOK(a[1])
	if !xok || !yok {
		return nil, fmt.Errorf("polygon start is invalid")
	}
	points := [][2]float64{{x, y}}
	i := 2
	for i < len(a) {
		command, ok := a[i].(string)
		if !ok {
			return nil, fmt.Errorf("polygon command token is invalid at index %d", i)
		}
		switch command {
		case "L":
			i++
			consumed := 0
			for i+1 < len(a) {
				if _, nextCommand := a[i].(string); nextCommand {
					break
				}
				x, xok = asFloatOK(a[i])
				y, yok = asFloatOK(a[i+1])
				if !xok || !yok {
					return nil, fmt.Errorf("polygon line coordinate is invalid")
				}
				points = append(points, [2]float64{x, y})
				i += 2
				consumed++
			}
			if consumed == 0 {
				return nil, fmt.Errorf("polygon L command has no coordinate pair")
			}
		case "ARC":
			if i+3 >= len(a) {
				return nil, fmt.Errorf("polygon ARC command is incomplete")
			}
			sweep, sok := asFloatOK(a[i+1])
			x, xok = asFloatOK(a[i+2])
			y, yok = asFloatOK(a[i+3])
			if !sok || !xok || !yok {
				return nil, fmt.Errorf("polygon ARC command has invalid numeric fields")
			}
			start := points[len(points)-1]
			curve, _, err := flattenNetPathArc(pcbArc{X1: start[0], Y1: start[1], X2: x, Y2: y, ArcAngle: sweep})
			if err != nil {
				return nil, fmt.Errorf("polygon ARC cannot be bounded: %w", err)
			}
			for _, p := range curve[1:] {
				points = append(points, [2]float64{p.x, p.y})
			}
			i += 4
		default:
			return nil, fmt.Errorf("polygon command %q is unsupported for emptiness proof", command)
		}
	}
	closed := false
	if len(points) > 1 && math.Hypot(points[0][0]-points[len(points)-1][0], points[0][1]-points[len(points)-1][1]) <= netPathGeomEps {
		points = points[:len(points)-1]
		closed = true
	}
	if len(points) < 2 {
		return nil, fmt.Errorf("path has fewer than 2 points")
	}
	return []pcbSourcePath{{points: points, closed: closed}}, nil
}

func complexPolygonOccupiesRect(raw any, rect layoutBBox) (bool, error) {
	contours, err := polygonSourceContours(raw)
	if err != nil {
		return false, err
	}
	eps := math.Max(netPathGeomEps*10, 1e-4)
	inner := layoutBBox{MinX: rect.MinX + eps, MinY: rect.MinY + eps, MaxX: rect.MaxX - eps, MaxY: rect.MaxY - eps}
	tests := [][2]float64{{(inner.MinX + inner.MaxX) / 2, (inner.MinY + inner.MaxY) / 2}, {inner.MinX, inner.MinY}, {inner.MaxX, inner.MinY}, {inner.MaxX, inner.MaxY}, {inner.MinX, inner.MaxY}}
	for _, p := range tests {
		if compoundWinding(contours, p) != 0 {
			return true, nil
		}
	}
	for _, c := range contours {
		for i, p := range c {
			q := c[(i+1)%len(c)]
			if rectSegDist(inner.MinX, inner.MinY, inner.MaxX, inner.MaxY, p[0], p[1], q[0], q[1]) <= netPathGeomEps {
				return true, nil
			}
		}
	}
	return false, nil
}

func compoundWinding(contours [][][2]float64, p [2]float64) int {
	w := 0
	for _, c := range contours {
		for i, a := range c {
			b := c[(i+1)%len(c)]
			if a[1] <= p[1] && b[1] > p[1] && (b[0]-a[0])*(p[1]-a[1])-(p[0]-a[0])*(b[1]-a[1]) > 0 {
				w++
			}
			if a[1] > p[1] && b[1] <= p[1] && (b[0]-a[0])*(p[1]-a[1])-(p[0]-a[0])*(b[1]-a[1]) < 0 {
				w--
			}
		}
	}
	return w
}
