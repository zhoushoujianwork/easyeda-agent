package app

import (
	"fmt"
	"sort"
)

func checkPCBModuleRouting(c pcbLayoutCandidate, input *pcbLayoutPlanInput, before, after *boardSnapshot, rep pcbModuleCheckReport) pcbModuleCheckReport {
	fail := func(err error) { rep.Status = "fail"; rep.Findings = append(rep.Findings, err.Error()) }
	unknown := func(message string) {
		if rep.Status == "pass" {
			rep.Status = "incomplete"
		}
		rep.Limitations = append(rep.Limitations, message)
	}
	if c.SchemaVersion < 3 && c.Escape == nil && (input == nil || input.Routing == nil) {
		rep.Limitations = append(rep.Limitations, "legacy candidate: simultaneous MCU escape/reflow acceptance is not covered")
		return finishPCBModuleCheck(rep)
	}
	if input == nil || input.Routing == nil {
		unknown("independent --requirements with complete routing demands is required for schema-v3 acceptance")
		return finishPCBModuleCheck(rep)
	}
	if c.SchemaVersion != 3 || c.Escape == nil || c.RoutingRequirementsSHA256 != pcbEscapeIntentHash(*input.Routing) {
		fail(fmt.Errorf("candidate omits or changes independent routing requirements/escape witness"))
		return finishPCBModuleCheck(rep)
	}
	if before == nil || after == nil {
		unknown("routing proof requires fresh before and after snapshots")
		return finishPCBModuleCheck(rep)
	}
	if err := verifyPCBReflowDeclaration(c, *input, before); err != nil {
		fail(err)
	}
	projected, err := projectPCBLayoutCandidate(before, c)
	if err != nil {
		fail(err)
	} else if err := verifyPCBEscape(*input.Routing, *c.Escape, projected); err != nil {
		fail(fmt.Errorf("planned escape provenance/geometry: %w", err))
	}
	if err := verifyPCBEscapeGeometry(*input.Routing, *c.Escape, after); err != nil {
		fail(fmt.Errorf("fresh simultaneous escape geometry: %w", err))
	} else {
		rep.Checks = append(rep.Checks, "all declared owner pads remain covered by simultaneous reservations against fresh copper; this is not a completed-net claim")
	}
	return finishPCBModuleCheck(rep)
}

func verifyPCBReflowDeclaration(c pcbLayoutCandidate, in pcbLayoutPlanInput, before *boardSnapshot) error {
	base := before.byDesignator()
	var module *pcbLayoutModuleSpec
	for i := range in.Modules {
		if in.Modules[i].ID == c.Module {
			module = &in.Modules[i]
			break
		}
	}
	if module == nil {
		return fmt.Errorf("candidate module is absent from independent requirements")
	}
	allowed := map[string]pcbLayoutMemberSpec{}
	for _, m := range module.Members {
		allowed[m.Ref] = m
	}
	groups := map[string]pcbReflowGroupSpec{}
	if in.Reflow != nil {
		for _, g := range in.Reflow.Groups {
			groups[g.ID] = g
		}
	}
	expected := map[string]boardComp{}
	if c.Reflow != nil {
		seen := map[string]bool{}
		for _, m := range c.Reflow.Moves {
			g, ok := groups[m.Group]
			if !ok || seen[m.Group] {
				return fmt.Errorf("undeclared/duplicate reflow group %s", m.Group)
			}
			seen[m.Group] = true
			refs, want := append([]string(nil), m.Refs...), append([]string(nil), g.Refs...)
			sort.Strings(refs)
			sort.Strings(want)
			if fmt.Sprint(refs) != fmt.Sprint(want) {
				return fmt.Errorf("reflow group %s does not preserve full membership", m.Group)
			}
			anchor, ok := base[g.AnchorRef]
			if !ok || !layoutAlmostEqual(anchor.X, m.PivotXMil) || !layoutAlmostEqual(anchor.Y, m.PivotYMil) {
				return fmt.Errorf("reflow group %s uses a different transform anchor", m.Group)
			}
			for _, ref := range g.Refs {
				if _, dup := allowed[ref]; dup {
					return fmt.Errorf("reflow member %s has multiple owners", ref)
				}
				allowed[ref] = pcbLayoutMemberSpec{Ref: ref, FixedAxes: g.FixedAxes, AllowedRotationsDeg: g.AllowedRotationsDeg}
				projected, err := transformBoardComp(base[ref], m.PivotXMil, m.PivotYMil, m.DXMil, m.DYMil, m.RotationDeltaDeg)
				if err != nil {
					return err
				}
				expected[ref] = projected
			}
		}
	}
	seen := map[string]bool{}
	for _, p := range c.Placements {
		ms, ok := allowed[p.Ref]
		if !ok || seen[p.Ref] {
			return fmt.Errorf("undeclared/duplicate placement %s", p.Ref)
		}
		seen[p.Ref] = true
		b, ok := base[p.Ref]
		if !ok {
			return fmt.Errorf("placement %s absent from baseline", p.Ref)
		}
		got := b
		got.X = p.XMil
		got.Y = p.YMil
		got.Rotation = p.RotationDeg
		got.Layer = p.Layer
		if b.Locked && poseChanged(b, got) {
			return fmt.Errorf("locked %s moved", p.Ref)
		}
		axes := append([]string(nil), ms.FixedAxes...)
		if in.Reflow != nil {
			if containsString(in.Reflow.FixedRefs, p.Ref) && poseChanged(b, got) {
				return fmt.Errorf("fixed ref %s moved", p.Ref)
			}
			axes = append(axes, in.Reflow.FixedAxes[p.Ref]...)
		}
		for _, a := range axes {
			if a == "x" && !layoutAlmostEqual(b.X, got.X) || a == "y" && !layoutAlmostEqual(b.Y, got.Y) || a == "rotation" && !sameRotation(b.Rotation, got.Rotation) {
				return fmt.Errorf("%s fixed %s changed", p.Ref, a)
			}
		}
		if len(ms.AllowedRotationsDeg) > 0 && !rotationAllowed(got.Rotation, ms.AllowedRotationsDeg) {
			return fmt.Errorf("%s rotation not allowed", p.Ref)
		}
		if e, ok := expected[p.Ref]; ok && poseChanged(e, got) {
			return fmt.Errorf("reflow %s disagrees with declared rigid transform", p.Ref)
		}
	}
	for ref := range allowed {
		if !seen[ref] {
			return fmt.Errorf("declared affected member %s missing from candidate", ref)
		}
	}
	if c.Bundle == nil {
		return fmt.Errorf("missing candidate bundle")
	}
	if canonicalJSON(c.Bundle.ReplacedObjects) != canonicalJSON(module.ExistingObjects) {
		return fmt.Errorf("owned-object replacements differ from independent requirements")
	}
	if module.CrystalGuard != nil {
		var declared []string
		for _, v := range c.Bundle.Vias {
			if v.ExistingPrimitiveID != "" {
				declared = append(declared, v.ExistingPrimitiveID)
			}
		}
		if !sameStringSet(declared, module.CrystalGuard.ReuseGroundAnchorViaIDs) {
			return fmt.Errorf("reused anchor vias differ from independent requirements")
		}
	}
	if c.Reflow != nil {
		if in.Reflow == nil {
			return fmt.Errorf("missing independent reflow requirements")
		}
		filtered, err := removePCBModuleOwnedObjects(before, module.ExistingObjects)
		if err != nil {
			return err
		}
		target := pcbLayoutVariant{label: c.Variant, comps: map[string]boardComp{}}
		for _, m := range module.Members {
			for _, p := range c.Placements {
				if m.Ref == p.Ref {
					projected, err := transformBoardComp(base[p.Ref], base[p.Ref].X, base[p.Ref].Y, p.XMil-base[p.Ref].X, p.YMil-base[p.Ref].Y, p.RotationDeg-base[p.Ref].Rotation)
					if err != nil {
						return err
					}
					target.comps[p.Ref] = projected
				}
			}
		}
		_, rebuilt, err := rebuildPCBReflowCandidate(*in.Reflow, target, *c.Reflow, filtered, in.MinGapMil)
		if err != nil {
			return err
		}
		declared := pcbReflowCopperPlan{ReplacePrimitiveIDs: c.Bundle.ReflowReplaceIDs, Routes: c.Bundle.ReflowRoutes, Vias: c.Bundle.ReflowVias}
		if canonicalJSON(rebuilt) != canonicalJSON(declared) || canonicalJSON(rebuilt) != canonicalJSON(c.Reflow.Copper) {
			return fmt.Errorf("reflow copper differs from independently rebuilt ownership/connections")
		}
	}
	if c.Reflow == nil && (len(c.Bundle.ReflowRoutes) > 0 || len(c.Bundle.ReflowVias) > 0 || len(c.Bundle.ReflowReplaceIDs) > 0) {
		return fmt.Errorf("reflow copper has no declared movement plan")
	}
	return nil
}
