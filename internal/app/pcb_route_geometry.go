package app

import (
	"fmt"
	"math"
)

// Host geometry adapter shared by escape reservations and offline path solving.
// It performs no IO and refuses missing inventories or unsupported pad shapes.
func preparePCBRouteGeometry(snap *boardSnapshot) (*pcbEscapeContext, string, error) {
	return preparePCBRouteGeometryAreas(snap, true)
}

// preparePCBRouteGeometryAreas with materializedAreas=false leaves static fills
// and poured copper out of ctx.areas: the caller must model them itself (as
// routability does with pcbBoardCopperExtras, which keeps stroke widths and
// unfilled paths that the polygon-only area parser cannot represent).
func preparePCBRouteGeometryAreas(snap *boardSnapshot, materializedAreas bool) (*pcbEscapeContext, string, error) {
	unknown := func(msg string) (*pcbEscapeContext, string, error) { return nil, "incomplete", fmt.Errorf("%s", msg) }
	if snap == nil || snap.Rules == nil || snap.Rules.Source != "live" || snap.Outline == nil || snap.Outline.Source != "polygon" || len(snap.Outline.Points) < 3 {
		return unknown("escape proof requires live rules and exact polygon board outline")
	}
	r := snap.Rules
	if !allFinite(r.ClearanceMil, r.ClearanceTrackTrackMil, r.TrackWidthMinMil, r.CopperToEdgeMil) || r.ClearanceMil <= 0 || r.ClearanceTrackTrackMil < 0 || r.TrackWidthMinMil <= 0 || r.CopperToEdgeMil < 0 {
		return unknown("escape proof has missing/invalid rule measurements")
	}
	if snap.Copper == nil {
		return unknown("escape proof requires pcb dump --include-copper")
	}
	for _, k := range []string{"routing", "vias", "pours", "poured", "regions", "fills"} {
		if snap.Copper.Availability[k] != "available" {
			return unknown("copper category " + k + " is unknown")
		}
	}
	if snap.Copper.ArcsAvailable == nil || !*snap.Copper.ArcsAvailable {
		return unknown("arc inventory is unknown")
	}
	for name, items := range map[string][]any{"lines": snap.Copper.Lines, "arcs": snap.Copper.Arcs, "vias": snap.Copper.Vias, "pours": snap.Copper.Pours, "poured": snap.Copper.Poured, "regions": snap.Copper.Regions, "fills": snap.Copper.Fills} {
		if items == nil {
			return unknown("copper inventory " + name + " is null; known-empty must be []")
		}
	}
	ctx := &pcbEscapeContext{snap: snap, pads: map[string]pcbPadP{}, ambiguous: map[string]bool{}}
	var err error
	ctx.allPads, err = crystalCandidatePads(snap, nil)
	if err != nil {
		return unknown(err.Error())
	}
	for _, p := range ctx.allPads {
		key := p.Designator + "." + p.Number
		if _, found := ctx.pads[key]; found {
			ctx.ambiguous[key] = true
		}
		if !allFinite(p.X, p.Y, p.W, p.H, p.Rotation) || p.W <= 0 || p.H <= 0 || !netPathCopperLayer(p.Layer) && p.Layer != pcbLayerMulti {
			return unknown("pad geometry/layer invalid: " + key)
		}
		if _, err := pcbExactPadSegmentGap(p, [2]float64{p.X, p.Y}, [2]float64{p.X, p.Y}); err != nil {
			return unknown(err.Error())
		}
		ctx.pads[key] = p
	}
	ctx.tracks, ctx.arcs, ctx.vias, err = crystalSnapshotRouting(snap)
	if err != nil {
		return unknown(err.Error())
	}
	if materializedAreas {
		ctx.areas, err = parseCopperAreaObstacles(snap.Copper.Fills, snap.Copper.Poured)
		if err != nil {
			return unknown(err.Error())
		}
	}
	for _, raw := range snap.Copper.Regions {
		m, ok := raw.(map[string]any)
		if !ok {
			return unknown("region is not an object")
		}
		rules, ok := m["ruleType"].([]any)
		if !ok {
			return unknown("region ruleType is unknown")
		}
		ban := false
		for _, rawRule := range rules {
			v, ok := asFloatOK(rawRule)
			if !ok || v != math.Trunc(v) {
				return unknown("region ruleType is invalid")
			}
			if v != 2 && v != 5 && v != 6 && v != 7 && v != 8 {
				return unknown("region has unsupported routing rule type")
			}
			if v == 5 {
				ban = true
			}
		}
		if !ban {
			continue
		} // no-pours alone does not forbid explicit tracks.
		layer, ok := asFloatOK(m["layer"])
		if !ok || layer != math.Trunc(layer) {
			return unknown("no-wires region layer is unknown")
		}
		contours, e := polygonSourceContours(m["source"])
		if e != nil {
			return unknown("no-wires region exact polygon unknown: " + e.Error())
		}
		ctx.bans = append(ctx.bans, pcbCopperArea{ID: asString(m["primitiveId"]), Layer: int(layer), Contours: contours})
	}
	return ctx, "", nil
}
