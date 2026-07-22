package app

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
)

// ── sch check: Go-side geometric marker rules (issues #146 / #147 / #148) ─────
//
// The connector's electrical schematic.check reconstructs floating pins, net-name
// mismatches, wire crossings, etc. — but it CANNOT see three classes of purely
// geometric defect that leave the netlist clean yet the drawing wrong/unreadable:
//
//	duplicate-net-marker  #146  two+ markers of the SAME kind+net at the SAME
//	                            anchor — the residue of a partial `sch autoconnect`
//	                            retry. The connector even collapses coincident
//	                            same-name flags to one net, so every electrical
//	                            rule (net-marker-mismatch, dangling-wire, bridge)
//	                            reports clean while the page carries a stacked pair.
//	titleblock-overlap    #147  a part/marker whose bbox intrudes the A4 title-block
//	                            keep-out (图签/明细表). autoconnect can drop a netport
//	                            there and layout-lint (part-only) + the electrical
//	                            check both pass.
//	marker-overlap        #148  a marker body positively overlaps a part or another
//	                            marker — electrically fine, visually unreadable.
//
// All three are pure bbox/anchor geometry over the SAME components.list layout-lint
// already pulls, so they live Go-side (no connector rebuild) and are table-testable
// against the issues' real coordinates.

// schMarkerTypes are the connector componentType values for net markers.
func isSchMarker(t string) bool {
	switch t {
	case "netflag", "netport", "netlabel":
		return true
	}
	return false
}

const (
	// markerAnchorQuant quantizes a marker anchor so two coincident markers with
	// sub-unit float drift (e.g. 1384.9999999 vs 1385) hash to the same bucket,
	// while markers even one grid step apart (≥5) never collide. See issue #146.
	markerAnchorQuant = 0.5
)

// analyzeMarkerGeometry runs the three read-only marker-geometry rules over the
// live component list. Pure (no I/O) so the issues' real H2/H4 bboxes drive table
// tests directly. overlapEps is the minimum positive-area extent (smaller axis)
// the overlap rules report — below it, edge grazing and the ~1-unit float noise of
// parallel same-side ports are ignored.
func analyzeMarkerGeometry(comps []layoutComp, titleBlock *layoutBBox, overlapEps float64) []checkFinding {
	var findings []checkFinding
	findings = append(findings, duplicateNetMarkerFindings(comps)...)
	findings = append(findings, titleblockOverlapFindings(comps, titleBlock, overlapEps)...)
	findings = append(findings, markerOverlapFindings(comps, overlapEps)...)
	return findings
}

// duplicateNetMarkerFindings groups net markers by (kind, net, quantized anchor)
// and reports every group of 2+ as one finding carrying ALL coincident primitive
// IDs plus a keep/delete suggestion. Different kinds, nets, or anchors never merge,
// so a legitimately distinct marker at another point is not a false positive.
func duplicateNetMarkerFindings(comps []layoutComp) []checkFinding {
	type gkey struct {
		kind   string
		net    string
		qx, qy int64
	}
	q := func(v float64) int64 { return int64(math.Round(v / markerAnchorQuant)) }
	groups := map[gkey][]layoutComp{}
	var order []gkey
	for _, c := range comps {
		if !isSchMarker(c.ComponentType) {
			continue
		}
		k := gkey{c.ComponentType, c.Net, q(c.X), q(c.Y)}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], c)
	}
	var out []checkFinding
	for _, k := range order {
		g := groups[k]
		if len(g) < 2 {
			continue
		}
		// Deterministic keep: the lexically-smallest primitiveId.
		sort.Slice(g, func(i, j int) bool { return g[i].ID < g[j].ID })
		ids := make([]string, len(g))
		for i, c := range g {
			ids[i] = c.ID
		}
		keepID := ids[0]
		delIDs := append([]string(nil), ids[1:]...)
		netTxt := g[0].Net
		if netTxt == "" {
			netTxt = "(unnamed)"
		}
		out = append(out, checkFinding{
			Type:             "duplicate-net-marker",
			Level:            "warn",
			ComponentType:    g[0].ComponentType,
			MarkerNet:        g[0].Net,
			PrimitiveId:      keepID,
			PrimitiveIds:     ids,
			SuggestKeepId:    keepID,
			SuggestDeleteIds: delIDs,
			At:               &checkPoint{X: round2(g[0].X), Y: round2(g[0].Y)},
			Message: fmt.Sprintf("重复 %s(%s) @(%.2f,%.2f) ×%d — 建议保留 %s，删除 %s (sch prim-delete)",
				g[0].ComponentType, netTxt, round2(g[0].X), round2(g[0].Y), len(g), keepID, strings.Join(delIDs, "、")),
		})
	}
	return out
}

// titleblockOverlapFindings reports any part or net marker whose bbox positively
// intrudes the title-block keep-out. The sheet itself (spans the page) and
// anything without a bbox are skipped.
func titleblockOverlapFindings(comps []layoutComp, titleBlock *layoutBBox, eps float64) []checkFinding {
	if titleBlock == nil {
		return nil
	}
	var out []checkFinding
	for _, c := range comps {
		if c.BBox == nil {
			continue
		}
		if c.ComponentType != schLayoutPartType && c.ComponentType != "" && !isSchMarker(c.ComponentType) {
			continue // skip the sheet and any non-part/non-marker primitive
		}
		ox, oy, overlap := overlapExtent(*c.BBox, *titleBlock)
		if !overlap || math.Min(ox, oy) <= eps {
			continue
		}
		out = append(out, checkFinding{
			Type:          "titleblock-overlap",
			Level:         "warn",
			PrimitiveId:   c.ID,
			ComponentType: c.ComponentType,
			Designator:    c.Designator,
			MarkerNet:     c.Net,
			BBox:          c.BBox,
			Keepout:       titleBlock,
			OverlapX:      round2(ox),
			OverlapY:      round2(oy),
			Message: fmt.Sprintf("%s(%s) 侵入标题栏 keep-out（重叠 %.2f×%.2f）— 移出图签区或换连线方向",
				markerLabel(c), c.ComponentType, round2(ox), round2(oy)),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PrimitiveId < out[j].PrimitiveId })
	return out
}

// markerOverlapFindings reports pairwise positive-area intersections where at
// least one side is a net marker (marker×part or marker×marker); part×part is
// already `sch layout-lint`'s overlap rule. Only overlaps whose smaller axis
// exceeds eps are reported — edge grazing and parallel-port float noise are below.
func markerOverlapFindings(comps []layoutComp, eps float64) []checkFinding {
	withBox := make([]layoutComp, 0, len(comps))
	for _, c := range comps {
		if c.BBox == nil || c.ComponentType == "sheet" {
			continue
		}
		withBox = append(withBox, c)
	}
	var out []checkFinding
	for i := 0; i < len(withBox); i++ {
		for j := i + 1; j < len(withBox); j++ {
			a, b := withBox[i], withBox[j]
			if !isSchMarker(a.ComponentType) && !isSchMarker(b.ComponentType) {
				continue // part×part is layout-lint's job
			}
			if isCoincidentDuplicate(a, b) {
				continue // already reported (with a keep/delete fix) by duplicate-net-marker
			}
			ox, oy, overlap := overlapExtent(*a.BBox, *b.BBox)
			if !overlap || math.Min(ox, oy) <= eps {
				continue
			}
			// Order the pair by id for stable output.
			pa, pb := a, b
			if pb.ID < pa.ID {
				pa, pb = pb, pa
			}
			out = append(out, checkFinding{
				Type:          "marker-overlap",
				Level:         "warn",
				PrimitiveId:   pa.ID,
				ComponentType: pa.ComponentType,
				Designator:    pa.Designator,
				MarkerNet:     pa.Net,
				BBox:          pa.BBox,
				Other: &checkOverlapSide{
					PrimitiveId:   pb.ID,
					ComponentType: pb.ComponentType,
					Designator:    pb.Designator,
					Net:           pb.Net,
					BBox:          pb.BBox,
				},
				PrimitiveIds: []string{pa.ID, pb.ID},
				OverlapX:     round2(ox),
				OverlapY:     round2(oy),
				Message: fmt.Sprintf("%s(%s) 与 %s(%s) 视觉重叠 %.2f×%.2f — 换方向/offset 或 stagger",
					markerLabel(pa), pa.ComponentType, markerLabel(pb), pb.ComponentType, round2(ox), round2(oy)),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PrimitiveId != out[j].PrimitiveId {
			return out[i].PrimitiveId < out[j].PrimitiveId
		}
		return out[i].Other.PrimitiveId < out[j].Other.PrimitiveId
	})
	return out
}

// isCoincidentDuplicate reports whether two markers are the SAME kind + net at the
// SAME quantized anchor — i.e. a pair the duplicate-net-marker rule already reports
// with a keep/delete suggestion. marker-overlap skips them to avoid double-flagging
// one defect as both "duplicate" and "visual overlap".
func isCoincidentDuplicate(a, b layoutComp) bool {
	if !isSchMarker(a.ComponentType) || a.ComponentType != b.ComponentType || a.Net != b.Net {
		return false
	}
	q := func(v float64) int64 { return int64(math.Round(v / markerAnchorQuant)) }
	return q(a.X) == q(b.X) && q(a.Y) == q(b.Y)
}

// markerLabel picks the most identifying name for a marker/part finding: a real
// designator, else the net name, else the primitive id.
func markerLabel(c layoutComp) string {
	if c.Designator != "" && !strings.HasSuffix(c.Designator, "?") {
		return c.Designator
	}
	if c.Net != "" {
		return c.Net
	}
	if c.ID != "" {
		return c.ID
	}
	return c.ComponentType
}

// mergeMarkerGeomFindings fetches the component bboxes/anchors and folds the three
// geometric rules into an existing electrical check report, updating its summary
// counts and passed/total. Best-effort: a components.list failure is logged to
// stderr and leaves the report untouched.
func mergeMarkerGeomFindings(cfg *appConfig, window string, allPages bool, overlapEps float64, rep *checkReport, stderr io.Writer) {
	payload := map[string]any{"includeBBox": true}
	if allPages {
		payload["allPages"] = true
	}
	res, err := requestAction(cfg, "schematic.components.list", window, payload)
	if err != nil {
		fmt.Fprintf(stderr, "sch check: marker-geometry skipped — components.list failed: %v\n", err)
		return
	}
	comps, perr := parseLayoutComps(res.Result)
	if perr != nil {
		fmt.Fprintf(stderr, "sch check: marker-geometry skipped — %v\n", perr)
		return
	}
	var titleBlock *layoutBBox
	if sheet := sheetBBoxOf(comps); sheet != nil {
		titleBlock, _ = titleBlockKeepout(sheet)
	}
	geo := analyzeMarkerGeometry(comps, titleBlock, overlapEps)
	if len(geo) == 0 {
		return
	}
	rep.Findings = append(rep.Findings, geo...)
	for _, f := range geo {
		switch f.Type {
		case "duplicate-net-marker":
			rep.Summary.DuplicateNetMarkers++
		case "titleblock-overlap":
			rep.Summary.TitleblockOverlaps++
		case "marker-overlap":
			rep.Summary.MarkerOverlaps++
		}
	}
	rep.Summary.Total = len(rep.Findings)
	rep.Passed = len(rep.Findings) == 0
}
