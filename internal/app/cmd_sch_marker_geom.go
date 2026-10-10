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
	case "netflag", "netport", "netlabel", "short_symbol":
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
// titleBlockSource is the keep-out's provenance (sheetSourceKnownTemplate /
// sheetSourceFallback / sheetSourceNone) — it grades titleblock-overlap severity.
func analyzeMarkerGeometry(comps []layoutComp, titleBlock *layoutBBox, titleBlockSource string, overlapEps float64) []checkFinding {
	var findings []checkFinding
	findings = append(findings, duplicateNetMarkerFindings(comps)...)
	findings = append(findings, titleblockOverlapFindings(comps, titleBlock, titleBlockSource, overlapEps)...)
	findings = append(findings, markerOverlapFindings(comps, overlapEps)...)
	findings = append(findings, foldedNetLabelFindings(comps)...)
	return findings
}

// foldedNetLabelFindings flags netports standing VERTICAL (rotation 90/270): the
// long body (31×11 horizontal) rotates to 11×31 and its net name renders sideways
// — the "标签折起来" readability fail on dense pin columns (live 2026-08-11: the
// autoconnect scorer picked vertical to dodge fanout-channel penalties; the
// costFoldedPort scoring change fixes the planner, this rule is the delivery-gate
// backstop so a folded label can never reach a finished sheet unseen, whatever
// planner produced it). Pure bbox geometry: height > width on a netport ⇔
// rotation ∈ {90,270}; ground/power markers are near-square and exempt.
func foldedNetLabelFindings(comps []layoutComp) []checkFinding {
	// 2026-08-12 用户拍板「netport 顺着方向摆布即可」:竖放 netport 合法
	// (rotation 走 orientation 真值表 port up=90/down=270),不再单独报 folded。
	// 当年立此判据的真实痛点是**密集 pin 列上侧向标签互相堆叠**——那是拥挤,
	// 由 marker-overlap 管(netport 的平台 bbox 天然含文字,竖放 11×31 参与
	// overlap 判定)。保留函数骨架与 summary 字段(报表格式兼容),恒零。
	_ = comps
	return nil
}

// reversedNetFlagFindings 抓**反挂的旗**:netflag 的 stored rotation 与其桩线
// 方向按真值表(flagBodyRotation ← orientation.json)不符 = 倒挂/侧翻。此前
// 朝向判据只活在 layout-score / lint.sh(非门);而 2026-08-12 真值表修正前,
// 生成侧与校验侧共享同一份错表(双盲)——旗倒挂两个月 gate 全绿,用户肉眼
// 才抓出。此规则把朝向下沉进 `sch check`(gate 的 check 关会拦)。
// 桩方向 = 锚点所在 wire 上「锚 − 相邻顶点」的主轴向(旗 body 沿桩朝外);
// 锚不在任何 wire 端点上的旗跳过(orphan-flag 已管)。
func reversedNetFlagFindings(comps []layoutComp, wires []schGroupWire) []checkFinding {
	var out []checkFinding
	for _, c := range comps {
		if c.ComponentType != "netflag" || c.Rotation == nil || !c.AnchorAvailable {
			continue
		}
		family := ""
		switch tidyNetClass(c.Net) {
		case "power":
			family = "power"
		case "ground":
			family = "ground"
		default:
			continue
		}
		// 找锚点 = wire 某端顶点的线,取相邻顶点定桩方向。
		dir := ""
		for _, w := range wires {
			pts := w.Points
			if len(pts) < 4 {
				continue
			}
			for i := 0; i+1 < len(pts); i += 2 {
				if math.Abs(pts[i]-c.X) > 0.5 || math.Abs(pts[i+1]-c.Y) > 0.5 {
					continue
				}
				// 相邻顶点:段数组任意序,取同一段的另一端(段 = [i..i+3] 对)。
				var nx, ny float64
				if i%4 == 0 && i+3 < len(pts) {
					nx, ny = pts[i+2], pts[i+3]
				} else {
					nx, ny = pts[i-2], pts[i-1]
				}
				dx, dy := c.X-nx, c.Y-ny
				if dx == 0 && dy == 0 {
					continue
				}
				if math.Abs(dx) >= math.Abs(dy) {
					if dx > 0 {
						dir = "right"
					} else {
						dir = "left"
					}
				} else {
					if dy > 0 {
						dir = "up"
					} else {
						dir = "down"
					}
				}
				break
			}
			if dir != "" {
				break
			}
		}
		if dir == "" {
			continue
		}
		want := flagBodyRotation[family][dir]
		got := math.Mod(math.Mod(*c.Rotation, 360)+360, 360)
		if got == want {
			continue
		}
		out = append(out, checkFinding{
			Type:          "reversed-net-flag",
			Level:         "warn",
			PrimitiveId:   c.ID,
			ComponentType: c.ComponentType,
			MarkerNet:     c.Net,
			BBox:          c.BBox,
			At:            &checkPoint{X: c.X, Y: c.Y},
			Message: fmt.Sprintf("%s 旗反挂:桩朝 %s 而旗 rot=%g(应为 %g)— `sch disconnect` 后 `sch connect --direction %s` 重连(或该 pin `sch autoconnect --replace`)",
				c.Net, dir, got, want, dir),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PrimitiveId < out[j].PrimitiveId })
	return out
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
//
// Severity is graded by the keep-out's provenance (issue #172): only a CONFIRMED
// keep-out (source=known-template-ratio, i.e. the A4-calibrated geometry) reports
// warn; an ESTIMATED keep-out (fallback-ratio — non-A4 sheet or unmatched aspect)
// reports info, because the rectangle is a best-effort fixed-size guess and a hit
// needs human confirmation before it may block anything.
func titleblockOverlapFindings(comps []layoutComp, titleBlock *layoutBBox, source string, eps float64) []checkFinding {
	if titleBlock == nil {
		return nil
	}
	confirmed := source == sheetSourceKnownTemplate
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
		level := "warn"
		msg := fmt.Sprintf("%s(%s) 侵入标题栏 keep-out（重叠 %.2f×%.2f）— 移出图签区或换连线方向",
			markerLabel(c), c.ComponentType, round2(ox), round2(oy))
		if !confirmed {
			level = "info"
			msg = fmt.Sprintf("%s(%s) 命中基于估计的图签区（source=%s，重叠 %.2f×%.2f）— keep-out 非 A4 标定，仅供参考,建议人工确认真实图签位置",
				markerLabel(c), c.ComponentType, source, round2(ox), round2(oy))
		}
		out = append(out, checkFinding{
			Type:          "titleblock-overlap",
			Level:         level,
			PrimitiveId:   c.ID,
			ComponentType: c.ComponentType,
			Designator:    c.Designator,
			MarkerNet:     c.Net,
			BBox:          c.BBox,
			Keepout:       titleBlock,
			OverlapX:      round2(ox),
			OverlapY:      round2(oy),
			Message:       msg,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PrimitiveId < out[j].PrimitiveId })
	return out
}

// flagBodyRotation:netflag 正确朝向的 stored rotation 真值表(power/ground 全
// 四向)及 netport。SSOT 是 .agents/skills/easyeda-agent/references/orientation.json(2026-08-12
// 五点实测重校准);TestFlagBodyRotationMatchesOrientationJSON 断言两份不漂移。
var flagBodyRotation = map[string]map[string]float64{
	"power":  {"up": 0, "left": 90, "down": 180, "right": 270},
	"ground": {"up": 180, "left": 270, "down": 0, "right": 90},
	"port":   {"up": 90, "left": 180, "down": 270, "right": 0},
}

// flagDirectionOf 由 stored rotation 反查旗的实际朝向(family 相关)。
func flagDirectionOf(family string, rot float64) (string, bool) {
	table, ok := flagBodyRotation[family]
	if !ok {
		return "", false
	}
	r := math.Mod(math.Mod(rot, 360)+360, 360)
	for dir, v := range table {
		if v == r {
			return dir, true
		}
	}
	return "", false
}

// flagTextBand 估算 netflag 的**文字带**:平台 getPrimitivesBBox 只包旗符号本体
// (实测 2026-08-12:GND 旗 bbox 10×21,"GND" 文字不在内)——两支旗符号相切、
// 文字互叠时 overlap=0,check 静默(用户肉眼抓出 U2 双 GND pin 挤成一坨)。
// 文字位置按实际渲染:水平旗文字在锚点内侧(pin 方向)桩线上方,竖直旗文字在
// 符号外端水平居中。宽按 6/字符估,高 12。
func flagTextBand(c layoutComp) *layoutBBox {
	if c.BBox == nil || !c.AnchorAvailable || c.Net == "" {
		return nil
	}
	// **netport 的网名画在本体之外**(2026-08-15 实测推翻旧注释):平台给的 bbox 恒为
	// 31×11,`C7_N3` 与 `USB_DTR` 一模一样 —— 名字根本不在里面。于是"标签压标签"对
	// check / layout-lint / 落点评分器全都是隐形的:用户一眼看见两支标签叠着,工具却
	// 一路报 0 marker-overlap。实测 MCU_TX 的本体 x=[544,576],而 MCU_RX 的本体
	// x=[500,530] 正落在 MCU_TX 名字要渲染的那一段上。
	// 名字沿本体的**背离锚点**方向往外接一条带(与 relayoutPortWidth 同一个宽度口径)。
	if c.ComponentType == "netport" {
		// 只补**超出六边形的那一段**:acPortTotalLen 是「六边形 + 名字」的总长,
		// 实测 bbox 的长边就是那个六边形。差值才是名字真正多出来的占地。
		long := math.Max(c.BBox.MaxX-c.BBox.MinX, c.BBox.MaxY-c.BBox.MinY)
		l := acPortTotalLen(c.Net) - long
		if l <= 0 {
			return nil // 名字短到画得进六边形,没有额外占地
		}
		const h = 11.0
		b := *c.BBox
		switch {
		case b.MaxX <= c.X: // 本体在锚点左侧 → 名字继续往左
			return &layoutBBox{MinX: b.MinX - l, MinY: b.MinY, MaxX: b.MinX, MaxY: b.MinY + h}
		case b.MinX >= c.X: // 本体在锚点右侧 → 名字继续往右
			return &layoutBBox{MinX: b.MaxX, MinY: b.MinY, MaxX: b.MaxX + l, MaxY: b.MinY + h}
		case b.MaxY <= c.Y: // 竖放:名字接在下端
			return &layoutBBox{MinX: b.MinX, MinY: b.MinY - l, MaxX: b.MinX + h, MaxY: b.MinY}
		default:
			return &layoutBBox{MinX: b.MinX, MinY: b.MaxY, MaxX: b.MinX + h, MaxY: b.MaxY + l}
		}
	}
	if c.ComponentType != "netflag" || c.Rotation == nil {
		return nil
	}
	family := ""
	switch tidyNetClass(c.Net) {
	case "power":
		family = "power"
	case "ground":
		family = "ground"
	default:
		return nil
	}
	dir, ok := flagDirectionOf(family, *c.Rotation)
	if !ok {
		return nil
	}
	l := 6 * float64(len(c.Net))
	const h = 12.0
	switch dir {
	case "right": // 符号在锚右,文字在锚左侧(pin 方向)线上方
		return &layoutBBox{MinX: c.X - l, MinY: c.Y, MaxX: c.X, MaxY: c.Y + h}
	case "left":
		return &layoutBBox{MinX: c.X, MinY: c.Y, MaxX: c.X + l, MaxY: c.Y + h}
	case "up": // 符号在锚上方,文字在符号顶上居中
		return &layoutBBox{MinX: c.X - l/2, MinY: c.BBox.MaxY, MaxX: c.X + l/2, MaxY: c.BBox.MaxY + h}
	case "down":
		return &layoutBBox{MinX: c.X - l/2, MinY: c.BBox.MinY - h, MaxX: c.X + l/2, MaxY: c.BBox.MinY}
	}
	return nil
}

// markerOverlapFindings reports pairwise positive-area intersections where at
// least one side is a net marker (marker×part or marker×marker); part×part is
// already `sch layout-lint`'s overlap rule. Only overlaps whose smaller axis
// exceeds eps are reported — edge grazing and parallel-port float noise are below.
// Symbols and calibrated text bands are judged separately: their rectangular
// envelope may contain empty corners, but real text collisions must still report.
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
			ox, oy, overlap := 0.0, 0.0, false
			var boxA, boxB layoutBBox
			for _, occupiedA := range markerCollisionBoxes(a) {
				for _, occupiedB := range markerCollisionBoxes(b) {
					x, y, hit := overlapExtent(occupiedA, occupiedB)
					// Preserve the numeric-only tolerance for platform float drift.
					// Real text/symbol intersections above eps still report.
					if hit && math.Min(x, y) > eps+1e-6 && (!overlap || x*y > ox*oy) {
						ox, oy, overlap = x, y, true
						boxA, boxB = occupiedA, occupiedB
					}
				}
			}
			if !overlap {
				continue
			}
			a.BBox, b.BBox = &boxA, &boxB
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
				Message: fmt.Sprintf("%s(%s) 与 %s(%s) 视觉重叠 %.2f×%.2f — `sch destagger` 可自动搬(默认只算不动),或换方向/offset",
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
	mergeMarkerGeomFindingsWith(cfg, window, allPages, overlapEps, rep, stderr, nil)
}

// mergeMarkerGeomFindingsWith 是带**预读快照**的版本(geom=nil 时行为不变)。
func mergeMarkerGeomFindingsWith(cfg *appConfig, window string, allPages bool, overlapEps float64, rep *checkReport, stderr io.Writer, geom *schGeomSnapshot) {
	payload := map[string]any{"includeBBox": true, "includePins": true}
	if allPages {
		payload["allPages"] = true
	}
	geometryResult, perr := geom.resultOr(cfg, window, payload)
	var comps []layoutComp
	if perr == nil {
		comps, perr = parseLayoutComps(geometryResult.Result)
	}
	if perr != nil {
		fmt.Fprintf(stderr, "sch check: marker-geometry skipped — %v\n", perr)
		rep.Findings = append(rep.Findings, checkFinding{Type: "marker-geometry-unavailable", Level: "ERROR", Message: perr.Error()})
		rep.Summary.Total = len(rep.Findings)
		rep.Passed = false
		return
	}
	titleBlock, tbSource := titleBlockKeepoutWithSource(sheetBBoxOf(comps))
	geo := analyzeMarkerGeometry(comps, titleBlock, tbSource, overlapEps)

	// redundant-net-marker needs the wire trees (exec_js read, stable). Best-effort:
	// a wire-read failure skips this rule only. Single-page only (wires are read
	// from the active page).
	var visualWires []schGroupWire
	if !allPages {
		if wires, werr := fetchSchWirePolylinesStable(cfg, window, ""); werr != nil {
			fmt.Fprintf(stderr, "sch check: redundant-marker/reversed-flag skipped — wire read failed: %v\n", werr)
			geo = append(geo, checkFinding{Type: "wire-geometry-unavailable", Level: "ERROR", Message: "wire-dependent visual checks could not run: " + werr.Error()})
		} else {
			visualWires = wires
			geo = append(geo, redundantNetMarkerFindings(comps, wires)...)
			geo = append(geo, reversedNetFlagFindings(comps, wires)...)
			geo = append(geo, schWireGeometryFindings(geometryResult.Result, wires)...)
		}
	}

	// Layout-organization rule (铁律 #15): a multi-module page with no functional
	// zone frames. Mechanical backstop — the rule was skipped twice
	// in one session when it lived only in docs. Scope to the single page under
	// check (allPages inflates the part count while text.list is active-page only).
	if !allPages {
		geo = append(geo, liveSchFrameCollisionFindings(cfg, window, comps, visualWires)...)
		for _, pf := range partitionFinding(cfg, window, comps, stderr) {
			geo = append(geo, *pf)
		}
		// 图签是交付件:标题/设计者/板名空着,打印出来就是一张没人认领的图。
		for _, tf := range titleBlockFinding(cfg, window, stderr) {
			geo = append(geo, *tf)
		}

	} else {
		geo = append(geo, checkFinding{Type: "partition-geometry-unavailable", Level: "ERROR", Message: "multi-page shallow reads cannot verify frame/Designator geometry; run sch check separately with --doc for each page"})
	}

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
		case "missing-partition", "missing-titleblock":
			// 交付件(区框/图签)共用一个聚合计数槽 —— 汇总行必须写成
			// missing-deliverable 而不是 missing-partition:2026-08-17 真机上一条
			// missing-titleblock 被汇总行标成「1 missing-partition」,引着人去查
			// 明明画好的框。逐条明细(WARN 行)始终带真实类型,判读以明细为准。
			rep.Summary.MissingPartitions++
		case "redundant-net-marker":
			rep.Summary.RedundantNetMarkers++
		case "reversed-net-flag":
			rep.Summary.ReversedNetFlags++
		case "folded-net-label":
			rep.Summary.FoldedNetLabels++
		}
	}
	rep.Summary.Total = len(rep.Findings)
	rep.Passed = len(rep.Findings) == 0
}

// ── redundant-net-marker(同树冗余标志)────────────────────────────────────
//
// duplicate-net-marker only catches flags whose anchors COINCIDE (quantized).
// Live 2026-08-12: two 3V3 flags 10 units apart on the SAME stub tree (a repair
// stacked a second flag on an already-flagged pin) slipped every rule — anchors
// differ so no duplicate, bbox graze was under the overlap eps, electrically the
// tree is fine so bridge-check is silent. Visually it renders as stacked text.
// Rule: within ONE wire tree, ≥2 markers of the same (net, componentType) are
// redundant — one names the net, the rest are debris. WARN + suggestDeleteIds
// (keep the first by stable order).
func redundantNetMarkerFindings(comps []layoutComp, wires []schGroupWire) []checkFinding {
	if len(wires) == 0 {
		return nil
	}
	// Union-find over wires: two wires share a tree when any vertex of one lies on
	// the other (same touch semantics as the group expansion / disconnect family).
	parent := make([]int, len(wires))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	union := func(a, b int) { parent[find(a)] = find(b) }
	const eps = 0.5
	for i := 0; i < len(wires); i++ {
		for j := i + 1; j < len(wires); j++ {
			touched := false
			for k := 0; k+1 < len(wires[i].Points) && !touched; k += 2 {
				if pointOnPolyline(wires[i].Points[k], wires[i].Points[k+1], wires[j].Points, eps) {
					touched = true
				}
			}
			for k := 0; k+1 < len(wires[j].Points) && !touched; k += 2 {
				if pointOnPolyline(wires[j].Points[k], wires[j].Points[k+1], wires[i].Points, eps) {
					touched = true
				}
			}
			if touched {
				union(i, j)
			}
		}
	}
	// Assign each marker (netflag/netport/netlabel with a usable anchor) to the
	// tree its anchor sits on; group per tree by (net, componentType).
	type key struct {
		tree int
		net  string
		typ  string
	}
	groups := map[key][]layoutComp{}
	for _, c := range comps {
		if !isSchMarker(c.ComponentType) || !c.AnchorAvailable || c.Net == "" {
			continue
		}
		for wi, w := range wires {
			if pointOnPolyline(c.X, c.Y, w.Points, eps) {
				k := key{find(wi), c.Net, c.ComponentType}
				groups[k] = append(groups[k], c)
				break
			}
		}
	}
	var findings []checkFinding
	for k, ms := range groups {
		if len(ms) < 2 {
			continue
		}
		sort.Slice(ms, func(i, j int) bool { return ms[i].ID < ms[j].ID })
		ids := make([]string, len(ms))
		var del []string
		for i, m := range ms {
			ids[i] = m.ID
			if i > 0 {
				del = append(del, m.ID)
			}
		}
		findings = append(findings, checkFinding{
			Type:             "redundant-net-marker",
			Level:            "warn",
			MarkerNet:        k.net,
			ComponentType:    k.typ,
			PrimitiveIds:     ids,
			SuggestKeepId:    ms[0].ID,
			SuggestDeleteIds: del,
			At:               &checkPoint{X: ms[0].X, Y: ms[0].Y},
			Count:            len(ms),
			Message: fmt.Sprintf("同一根线树上有 %d 个 %s(%s)标志 — 一个命名即可,其余是修补残留(视觉堆叠);建议保留 %s,删除 %s",
				len(ms), k.net, k.typ, ms[0].ID, strings.Join(del, ",")),
		})
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].SuggestKeepId < findings[j].SuggestKeepId })
	return findings
}

// schPartitionMinParts is the part-count above which a page is expected to be
// organized into functional zones (铁律 #15). Below it, a page is a single trivial
// module and framing would be noise; our fixed ESP32-blink regression board has 12.
const schPartitionMinParts = 6

// partitionFinding checks functional frames on a multi-part page. Canvas title
// evidence and recorded frame IDs are reconciled by schPartitionPageEvidence.
// A failed read remains a diagnostic warning and never masks electrical findings.
func partitionFinding(cfg *appConfig, window string, comps []layoutComp, stderr io.Writer) []*checkFinding {
	parts := 0
	for _, c := range comps {
		if c.ComponentType == schLayoutPartType {
			parts++
		}
	}
	if parts < schPartitionMinParts {
		return nil
	}
	res, err := requestAction(cfg, "schematic.text.list", window, map[string]any{})
	if err != nil {
		fmt.Fprintf(stderr, "sch check: partition-check skipped — text.list failed: %v\n", err)
		return nil
	}
	ev := schPartitionPageEvidence(cfg, window, parseZoneMoveTexts(res.Result))
	return partitionFindingForZones(parts, ev.Frames(), ev.Labels(), schTextCount(res.Result), ev.Zones)
}

// titleBlockFinding 判图签有没有填。**这是交付件不是装饰**:图纸标题、设计者、板名
// 空着或还停在平台默认值(`Board1`),打印出来就是一张没人认领的图。
// 平台把图签字段放在 sheet 上而不是自由文本里,所以 partitionFinding 那条判据看不见它。
func titleBlockFinding(cfg *appConfig, window string, stderr io.Writer) []*checkFinding {
	res, err := requestAction(cfg, "schematic.titleblock.get", window, map[string]any{})
	if err != nil {
		fmt.Fprintf(stderr, "sch check: titleblock-check skipped — titleblock.get failed: %v\n", err)
		return nil
	}
	data, _ := res.Result["titleBlockData"].(map[string]any)
	if data == nil {
		return nil
	}
	// **不判 showTitleBlock**:平台两个读接口对它各说各话 —— `getCurrentSchematicPageInfo()`
	// 报 true 而 `titleblock.get` 报 false(2026-08-15 实测,同一页同一时刻)。
	// 建立在互相矛盾的读数上的判据只会误报,留给人眼。
	return titleBlockFindingsFor(data)
}

// titleBlockRequired 是必填项与它们的中文说明。
// 只列**可写**项。`@` 开头的是系统派生项(@Board Name / @Project Name / @Page No…),
// 来自工程与板子对象,平台按「无法识别的明细项将被忽略」静默丢弃 —— 要求它等于
// 要求一件做不到的事(实测写 `@Board Name` 返回 true 但纹丝不动)。
var titleBlockRequired = []struct{ Key, Label string }{
	{"Name", "Name(图纸标题)"},
	{"Drawed", "Drawed(设计者)"},
	{"Description", "Description(图纸说明)"},
}

// titleBlockFindingsFor 是纯判据,输入就是 `titleblock.get` 的 titleBlockData。
//
// 必填项分两种,**因为修法完全不同**:
//
//   - 页上**有这个明细项、只是空着** → `missing-titleblock`(warn,--strict 阻塞)。
//     `sch titleblock --data …` 填得进去,提示照给。
//   - 页上**根本没有这个明细项**(图签模板不带它) → `titleblock-key-absent`(info)。
//     写入侧会明确拒绝它:schTitleBlockPatch 对不在 titleBlockData 里的 key 返回
//     「这些明细项当前页没有:… —— 先跑 `easyeda sch titleblock-get` 看可用 key」。
//     此前两处读的是同一份 titleBlockData,却只有写入侧看 key 在不在:缺项被
//     valueOf 当成空值,于是 check 要求填一个 CLI 自己必定拒写的项,
//     `sch gate --strict` 因此恒为 FAIL,且没有任何命令能解除。
//
// 缺项报 info 而不是直接丢掉:门禁不该要求做不到的事,但「这张图没有设计者栏位」
// 仍然是交付前该知道的事,并且要把这一页**真正有的 key** 列出来,人才好选。
func titleBlockFindingsFor(data map[string]any) []*checkFinding {
	valueOf := func(k string) string {
		m, _ := data[k].(map[string]any)
		if m == nil {
			return ""
		}
		return strings.TrimSpace(asString(m["value"]))
	}
	var blank, absent []string
	for _, req := range titleBlockRequired {
		if _, present := data[req.Key]; !present {
			absent = append(absent, req.Key)
			continue
		}
		if valueOf(req.Key) == "" {
			blank = append(blank, req.Label)
		}
	}
	var out []*checkFinding
	if len(blank) > 0 {
		out = append(out, &checkFinding{
			Type:    "missing-titleblock",
			Level:   "warn",
			Count:   len(blank),
			Message: fmt.Sprintf("图签未填:%s — 交付图必须能认领(`sch titleblock --data '{\"Name\":\"…\",\"Drawed\":\"…\"}'`)", strings.Join(blank, "、")),
		})
	}
	if len(absent) > 0 {
		out = append(out, &checkFinding{
			Type:  "titleblock-key-absent",
			Level: "info",
			Count: len(absent),
			Message: fmt.Sprintf("这一页的图签模板没有这些明细项:%s —— 写入会被拒(`sch titleblock` 只接受当前页已有的 key)。本页可用 key:%s",
				strings.Join(absent, "、"), strings.Join(titleBlockAvailableKeys(data), ", ")),
		})
	}
	return out
}

// titleBlockAvailableKeys 列出这一页真正能写的 key(排除 `@` 派生项),升序,
// 供人照着改提示里的命令。
func titleBlockAvailableKeys(data map[string]any) []string {
	keys := make([]string, 0, len(data))
	for k := range data {
		if strings.HasPrefix(k, "@") {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return []string{"(无可写明细项)"}
	}
	return keys
}

// partitionFindingFor checks module frames. Standalone circuit notes are not required.
func partitionFindingFor(parts, frameRects, labelTexts, textCount int) []*checkFinding {
	return partitionFindingForZones(parts, frameRects, labelTexts, textCount, 0)
}

// partitionFindingForZones 是真正的纯核。zones = 本页登记的功能模块数(虚拟组/认领),
// 它**只影响报文**:已归组说明「区分好了、就差画框」,该给的下一步与「一片散件」
// 完全不同(而「给不出能执行的下一步」正是本仓的复发病)。归组本身不免检 ——
// 铁律#15 要的是框,SKILL 明写 block-apply 不自动画框、必须补。
func partitionFindingForZones(parts, frameRects, labelTexts, textCount, zones int) []*checkFinding {
	if parts < schPartitionMinParts {
		return nil
	}
	var out []*checkFinding
	// **判据是「够不够」不是「有没有」**(2026-08-26 实测):MCU_CORE 6 个模块只画上
	// 2 个框(其余 4 个 text create 失败),check 却报 0 findings —— 因为 frameRects≠0。
	// 「缺 4 个框」与「全都有」在旧口径里是同一个答案,用户当场问「都不缺了吗」
	// 而工具答不上来。zones>0 时按模块数判缺口;zones==0(没有模块记账)证不出
	// 「该有几个」,退回老口径只判「一个都没有」。
	if zones > 0 && frameRects > 0 && frameRects < zones {
		out = append(out, &checkFinding{
			Type:  "missing-partition",
			Level: "warn",
			Count: zones - frameRects,
			Message: fmt.Sprintf("%d 个功能模块只画上 %d 个分区框,还缺 %d 个 —— 重跑 `sch zone-draw --mode partition`(它只补缺的那几个);"+
				"若被拒,先 `sch zone-plan` 看逐条 ✗ 点名是哪两个区顶住、差多少,照它给的 `sch zone move` 挪完再画。"+
				"⚠ 画框会**部分失败**(平台 text create 偶发返回 undefined),所以画完必须回读复核,别只看退出码",
				zones, frameRects, zones-frameRects),
		})
	}
	if frameRects == 0 {
		msg := fmt.Sprintf("%d 个器件的页没有功能分区框 — 铁律#15:`sch zones set` → `sch zone-draw`(整纸版式 `--mode partition`);交付前必须有", parts)
		if zones > 0 {
			// **出路必须是轻的那一条**(2026-08-24):此前这里只写「先 `zone-arrange
			// --apply` 拉开各区」—— 整页重排是重操作、有风险、本身还可能 blocked,于是
			// 「画不出框 → check 报没框 → gate FAIL」在真机上把交付卡死了两页。
			// `zone-plan` 现在逐对报出「谁顶谁 / 还差多少 / 一条 `sch zone move` 命令」,
			// 先走那条;整页重排降级成最后一档。
			msg = fmt.Sprintf("%d 个器件的页已分成 %d 个功能模块(虚拟组/认领)但一个分区框都没画 — 铁律#15:`sch zone-draw --mode partition`;"+
				"被拒(partitionOverlap / titleBlockHits)就先跑 `sch zone-plan` 看逐条 ✗ —— 它点名是哪两个区顶住、还差多少,"+
				"并给出一条 `sch zone move --zone <区> --dx/--dy <量>` 的最小挪动;挪完再画,"+
				"整页重排 `sch zone-arrange --apply` 或拆页是最后一档", parts, zones)
		}
		out = append(out, &checkFinding{
			Type:    "missing-partition",
			Level:   "warn",
			Count:   parts,
			Message: msg,
		})
	}
	return out
}

// schTextCount extracts the number of free text primitives from a
// schematic.text.list result, tolerating either {texts:[…]} or a bare […].
func schTextCount(result map[string]any) int {
	if result == nil {
		return 0
	}
	if raw, ok := result["texts"]; ok {
		if arr, ok := raw.([]any); ok {
			return len(arr)
		}
	}
	return 0
}
