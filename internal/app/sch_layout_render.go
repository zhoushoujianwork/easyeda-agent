package app

import (
	"bytes"
	"fmt"
	"html"
	"math"
	"sort"
	"strings"
)

type SchematicRenderZone struct {
	ID              string                  `json:"id"`
	Title           string                  `json:"title"`
	CoreComponentID string                  `json:"coreComponentId,omitempty"`
	ContentBounds   *SchematicBox           `json:"contentBounds,omitempty"`
	Frame           *schFrameSpec           `json:"frame,omitempty"`
	Layout          *SchematicLayoutResult  `json:"layout"`
	Status          string                  `json:"status,omitempty"`
	Error           string                  `json:"error,omitempty"`
	SheetPosition   *SchematicSheetPosition `json:"sheetPosition,omitempty"`
	Placement       *SchematicZonePlacement `json:"placement,omitempty"`
}
type SchematicRenderInput struct {
	SchemaVersion  int                   `json:"schemaVersion"`
	Title          string                `json:"title,omitempty"`
	Zones          []SchematicRenderZone `json:"zones"`
	CandidatesUsed int                   `json:"candidatesUsed,omitempty"`
	Sheet          *SchematicRenderSheet `json:"sheet,omitempty"`
	Spacing        *float64              `json:"spacing,omitempty"`
	Diagnostic     bool                  `json:"diagnostic,omitempty"`
}

// RenderSchematicLayoutSVG translates existing geometry only. Zone translations
// are display packing, never new component positions for Apply. No AI/EDA calls.
func RenderSchematicLayoutSVG(in SchematicRenderInput) ([]byte, error) {
	var spacingErr error
	in, spacingErr = resolveSchematicRenderSpacing(in)
	if spacingErr != nil {
		return nil, spacingErr
	}
	if in.SchemaVersion != 1 || len(in.Zones) == 0 {
		return nil, fmt.Errorf("schemaVersion:1 and nonempty zones required")
	}
	if err := validateSchematicRenderPlacements(in.Zones); err != nil {
		return nil, err
	}
	if in.Sheet != nil {
		if err := validateSchematicSheet(in); err != nil {
			return nil, err
		}
	}
	type panel struct {
		zone       SchematicRenderZone
		frame      schFrameSpec
		x, y, w, h float64
	}
	panels := []panel{}
	ids, refs := map[string]bool{}, map[string]bool{}
	const wrap = 1200.0
	x, y, row, maxX := 20.0, 85.0, 0.0, 0.0
	for _, z := range in.Zones {
		if strings.TrimSpace(z.ID) == "" || strings.TrimSpace(z.Title) == "" || ids[z.ID] || z.Layout == nil || len(z.Layout.Placements) == 0 {
			return nil, fmt.Errorf("zone requires unique ID/title and layout placements")
		}
		ids[z.ID] = true
		if z.Status != "" && z.Status != "planned" && z.Status != "blocked" {
			return nil, fmt.Errorf("zone %s invalid status", z.ID)
		}
		p := powerLayoutPlan{Placements: z.Layout.Placements, Wires: z.Layout.Wires, Flags: z.Layout.Flags}
		for _, c := range p.Placements {
			if c.Designator == "" || refs[c.Designator] || !plBoxValid(c.BBox) || !plFinite(c.X) || !plFinite(c.Y) || !plFinite(c.Rotation) || len(c.Pins) == 0 {
				return nil, fmt.Errorf("invalid/duplicate placement %s", c.Designator)
			}
			refs[c.Designator] = true
			pins := map[string]bool{}
			for _, q := range c.Pins {
				if q.Number == "" || pins[q.Number] || !plFinite(q.X) || !plFinite(q.Y) {
					return nil, fmt.Errorf("invalid pin %s.%s", c.Designator, q.Number)
				}
				pins[q.Number] = true
			}
			for _, b := range c.TextBBoxes {
				if !plBoxValid(b) {
					return nil, fmt.Errorf("invalid text bounds")
				}
			}
		}
		for _, w := range p.Wires {
			if w.Net == "" || len(w.Points) < 2 {
				return nil, fmt.Errorf("invalid wire")
			}
			for j, q := range w.Points {
				if !plFinite(q[0]) || !plFinite(q[1]) {
					return nil, fmt.Errorf("nonfinite wire")
				}
				if j > 0 {
					a := w.Points[j-1]
					if a == q || (a[0] != q[0] && a[1] != q[1]) {
						return nil, fmt.Errorf("zero/diagonal wire")
					}
				}
			}
		}
		for _, f := range p.Flags {
			if !plFinite(f.PinX) || !plFinite(f.PinY) || !plFinite(f.Offset) || f.Offset <= 0 || f.Net == "" {
				return nil, fmt.Errorf("invalid marker")
			}
			switch f.Direction {
			case "left", "right", "up", "down":
			default:
				return nil, fmt.Errorf("invalid marker direction")
			}
			switch f.Kind {
			case "power", "ground", "net_port_bi", "net_port_in", "net_port_out":
			default:
				return nil, fmt.Errorf("invalid marker kind")
			}
		}
		boxes := powerLayoutContentObstacles(&p)
		frame, err := sheetPreviewFrame(z, in.Spacing)
		if err != nil {
			return nil, err
		}
		if z.Frame != nil {
			frame = *z.Frame
			if !plBoxValid(frame.Rect) || !plFinite(frame.TitleX) || !plFinite(frame.TitleY) || !plFinite(frame.FontSize) || frame.FontSize <= 0 || frame.Title != z.Title {
				return nil, fmt.Errorf("invalid supplied frame %s", z.ID)
			}
			for _, b := range boxes {
				if !boxInside(b, frame.Rect) {
					return nil, fmt.Errorf("frame %s clips content", z.ID)
				}
			}
		}
		w, h := frame.Rect.MaxX-frame.Rect.MinX, frame.Rect.MaxY-frame.Rect.MinY+25
		if w > 100000 || h > 100000 {
			return nil, fmt.Errorf("render extent too large")
		}
		if x+w > wrap && x > 20 {
			x = 20
			y += row + 20
			row = 0
		}
		panels = append(panels, panel{z, frame, x, y, w, h})
		if in.Sheet != nil {
			p := &panels[len(panels)-1]
			p.x = 20 + z.SheetPosition.X - in.Sheet.Bounds.MinX
			p.y = 85 + in.Sheet.Bounds.MaxY - z.SheetPosition.Y - 25
		}
		row = math.Max(row, h)
		maxX = math.Max(maxX, x+w+20)
		x += w + 20
	}
	height := y + row + 20
	if len(panels) == 1 {
		maxX = math.Max(maxX, 360)
	} else {
		maxX = math.Max(maxX, 600)
	}
	// Square viewport also avoids platform thumbnailers cropping tall/wide SVGs.
	extent := math.Max(maxX, height)
	canvasW, canvasH := extent, extent
	if in.Sheet != nil {
		canvasW = in.Sheet.Bounds.MaxX - in.Sheet.Bounds.MinX + 40
		canvasH = in.Sheet.Bounds.MaxY - in.Sheet.Bounds.MinY + 105
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%g" height="%g" viewBox="0 0 %g %g"><rect width="100%%" height="100%%" fill="#f8fafc"/><g font-family="Arial,PingFang SC,sans-serif">`, canvasW*2, canvasH*2, canvasW, canvasH)
	text := func(x, y, size float64, color, value, anchor string) {
		fmt.Fprintf(&b, `<text x="%g" y="%g" font-size="%g" fill="%s" text-anchor="%s">%s</text>`, x, y, size, color, anchor, html.EscapeString(value))
	}
	title := in.Title
	if title == "" {
		title = "原理图布局预览"
	}
	if len(panels) == 1 {
		title = panels[0].zone.Title + " · 布局预览"
	}
	if in.Diagnostic {
		title = "诊断模式 · " + title
	}
	text(20, 28, 20, "#22344d", title, "start")
	text(20, 49, 9, "#546579", "离线转译 / 简化符号 / 非仿真、非官方导图", "start")
	if in.Diagnostic {
		text(20, 65, 9, "#c53b45", "诊断输出：未通过完整性门禁，不得作为完成效果或 Apply 依据。", "start")
	} else {
		text(20, 65, 9, "#546579", "只转译输入几何；离线预览不等于现场 Apply 验收。", "start")
	}
	if in.Sheet != nil {
		s := in.Sheet
		rect := func(r SchematicBox, fill, stroke, dash string) {
			fmt.Fprintf(&b, `<rect x="%g" y="%g" width="%g" height="%g" fill="%s" stroke="%s" stroke-dasharray="%s"/>`, 20+r.MinX-s.Bounds.MinX, 85+s.Bounds.MaxY-r.MaxY, r.MaxX-r.MinX, r.MaxY-r.MinY, fill, stroke, dash)
		}
		rect(s.Bounds, "white", "#94a3b8", "")
		rect(s.Border, "none", "#c74747", "")
		usable := sheetPreviewUsable(*s)
		rect(usable, "none", "#4a998e", "3 4")
		for _, k := range s.Keepouts {
			rect(k, "#f1f5f9", "#9ca8b8", "3 3")
			text(24+k.MinX-s.Bounds.MinX, 99+s.Bounds.MaxY-k.MaxY, 9, "#64748b", "图签禁放区", "start")
		}
		text(20, canvasH-7, 9, "#546579", fmt.Sprintf("原比例 / 页边净距 ≥ %g raw (%.2f mm) / 区间净距 ≥ %g raw / 绿色虚线：排版边界", s.Padding, s.Padding*.254, s.Gap), "start")
		if s.BorderSource != "" {
			text(canvasW-20, canvasH-7, 8, "#546579", s.BorderSource, "end")
		}
	}
	for _, v := range panels {
		z, f := v.zone, v.frame
		color, status := "#aa00aa", "预案"
		if in.Diagnostic {
			color, status = "#c53b45", "诊断"
		}
		if z.Status == "blocked" {
			color, status = "#c53b45", "未完成"
		}
		if in.Sheet == nil {
			text(v.x, v.y+10, 9, color, fmt.Sprintf("%s · %d 个器件", status, len(z.Layout.Placements)), "start")
		}
		X := func(x float64) float64 { return v.x + x - f.Rect.MinX }
		Y := func(y float64) float64 { return v.y + 25 + f.Rect.MaxY - y }
		fmt.Fprintf(&b, `<rect x="%g" y="%g" width="%g" height="%g" fill="white" stroke="%s" stroke-dasharray="5 3"/>`, v.x, v.y+25, v.w, v.h-25, color)
		text(X(f.TitleX), Y(f.TitleY)+f.FontSize*.8, f.FontSize, color, f.Title, "start")
		line := func(a, c [2]float64, color string) {
			fmt.Fprintf(&b, `<path d="M%g %g L%g %g" fill="none" stroke="%s" stroke-width="0.8"/>`, X(a[0]), Y(a[1]), X(c[0]), Y(c[1]), color)
		}
		for _, w := range z.Layout.Wires {
			for i := 1; i < len(w.Points); i++ {
				line(w.Points[i-1], w.Points[i], "#07814d")
			}
		}
		for _, c := range z.Layout.Placements {
			r := c.BBox
			fmt.Fprintf(&b, `<rect x="%g" y="%g" width="%g" height="%g" fill="white" stroke="#34445b" stroke-width="0.8"/>`, X(r.MinX), Y(r.MaxY), r.MaxX-r.MinX, r.MaxY-r.MinY)
			text(X(r.MinX), Y(r.MaxY)-4, 7, "#24344a", c.Designator, "start")
			text(X(r.MinX), Y(r.MinY)+9, 5, "#546579", c.Value, "start")
			for _, q := range c.Pins {
				ex, ey := math.Max(r.MinX, math.Min(r.MaxX, q.X)), math.Max(r.MinY, math.Min(r.MaxY, q.Y))
				line([2]float64{ex, ey}, [2]float64{q.X, q.Y}, "#34445b")
				label, anchor, dx := q.Name, "middle", 0.0
				if label == "" {
					label = q.Number
				}
				if q.X < r.MinX {
					anchor = "start"
					dx = 2
				}
				if q.X > r.MaxX {
					anchor = "end"
					dx = -2
				}
				text(X(ex)+dx, Y(ey)+1.5, 4.5, "#34445b", label, anchor)
				fmt.Fprintf(&b, `<circle cx="%g" cy="%g" r="1.1" fill="#34445b"><title>%s</title></circle>`, X(q.X), Y(q.Y), html.EscapeString(c.Designator+"."+q.Number+" "+q.Net))
				id := z.Layout.ComponentIDs[c.Designator]
				if q.Net == "" && z.Layout.PinStates[id][q.Number] == "nc" {
					line([2]float64{q.X - 2, q.Y - 2}, [2]float64{q.X + 2, q.Y + 2}, "#c53b45")
					line([2]float64{q.X - 2, q.Y + 2}, [2]float64{q.X + 2, q.Y - 2}, "#c53b45")
				}
			}
		}
		for _, m := range z.Layout.Flags {
			x, y := endpointFor(m.PinX, m.PinY, m.Offset, m.Direction)
			line([2]float64{m.PinX, m.PinY}, [2]float64{x, y}, "#07814d")
			renderLayoutMarker(&b, m, X, Y)
		}
		for _, q := range layoutJunctions(z.Layout) {
			fmt.Fprintf(&b, `<circle class="junction" cx="%g" cy="%g" r="1.6" fill="#07814d"/>`, X(q[0]), Y(q[1]))
		}
	}
	b.WriteString("</g></svg>\n")
	return b.Bytes(), nil
}

// Draw the same directional body and text envelope that the solver reserves.
// Glyphs remain schematic approximations, not vendor symbol artwork.
func renderLayoutMarker(b *bytes.Buffer, m powerLayoutFlag, X, Y func(float64) float64) {
	x, y := endpointFor(m.PinX, m.PinY, m.Offset, m.Direction)
	body := predictedMarkerBody(x, y, m.Kind, m.Direction, m.Net)
	u, v := 1.0, 0.0
	switch m.Direction {
	case "left":
		u = -1
	case "up":
		u, v = 0, 1
	case "down":
		u, v = 0, -1
	}
	profile := markerBBoxProfile(m.Kind, m.Net)
	point := func(along, across float64) [2]float64 {
		return [2]float64{X(x + u*along - v*across), Y(y + v*along + u*across)}
	}
	line := func(a, c [2]float64) {
		fmt.Fprintf(b, `<path d="M%g %g L%g %g" fill="none" stroke="#176bc0" stroke-width="0.8"/>`, a[0], a[1], c[0], c[1])
	}
	line(point(0, 0), point(profile.Near, 0))
	if isNetPortKind(m.Kind) {
		fmt.Fprint(b, `<polygon class="net-port" points="`)
		for _, q := range [][2]float64{{profile.Near, 0}, {profile.Near + 5, profile.Cross}, {profile.Far - 5, profile.Cross}, {profile.Far, 0}, {profile.Far - 5, -profile.Cross}, {profile.Near + 5, -profile.Cross}} {
			p := point(q[0], q[1])
			fmt.Fprintf(b, "%g,%g ", p[0], p[1])
		}
		fmt.Fprint(b, `" fill="none" stroke="#176bc0" stroke-width="0.8"/>`)
	} else if m.Kind == "ground" {
		for i := 0; i < 3; i++ {
			a := profile.Near + float64(i)*(profile.Far-profile.Near)/2
			h := profile.Cross * float64(3-i) / 3
			line(point(a, -h), point(a, h))
		}
	} else {
		line(point(profile.Near, 0), point(profile.Far, 0))
		line(point(profile.Far, -profile.Cross), point(profile.Far, profile.Cross))
	}
	if band := predictedFlagTextBand(x, y, body, m.Kind, m.Direction, m.Net); band != nil {
		cx, cy := X((band.MinX+band.MaxX)/2), Y((band.MinY+band.MaxY)/2)
		width, angle := band.MaxX-band.MinX, 0
		if isNetPortKind(m.Kind) && (m.Direction == "up" || m.Direction == "down") {
			width = band.MaxY - band.MinY
			angle = -90
			if m.Direction == "down" {
				angle = 90
			}
		}
		fmt.Fprintf(b, `<text class="marker-name" transform="translate(%g %g) rotate(%d)" y="3.5" text-anchor="middle" font-size="10" textLength="%g" lengthAdjust="spacingAndGlyphs" fill="#176bc0">%s</text>`, cx, cy, angle, math.Max(1, width-2), html.EscapeString(m.Net))
	}
}

// Degree counts distinct rays, not segment records: duplicate collinear wires
// cannot invent a junction, and crossing foreign nets must never get a dot.
func layoutJunctions(layout *SchematicLayoutResult) [][2]float64 {
	var segments []powerLayoutWire
	for _, w := range layout.Wires {
		for i := 1; i < len(w.Points); i++ {
			segments = append(segments, powerLayoutWire{Net: w.Net, Points: [][2]float64{w.Points[i-1], w.Points[i]}})
		}
	}
	for _, f := range layout.Flags {
		x, y := endpointFor(f.PinX, f.PinY, f.Offset, f.Direction)
		segments = append(segments, powerLayoutWire{Net: f.Net, Points: [][2]float64{{f.PinX, f.PinY}, {x, y}}})
	}
	for _, c := range layout.Placements {
		for _, p := range c.Pins {
			if p.Net != "" {
				segments = append(segments, powerLayoutWire{Net: p.Net, Points: [][2]float64{{p.X, p.Y}, {math.Max(c.BBox.MinX, math.Min(c.BBox.MaxX, p.X)), math.Max(c.BBox.MinY, math.Min(c.BBox.MaxY, p.Y))}}})
			}
		}
	}
	candidates := map[[2]float64]bool{}
	for _, w := range segments {
		for _, p := range w.Points {
			candidates[p] = true
		}
	}
	// Include interior-interior orthogonal intersections, too.
	for i, a := range segments {
		for _, b := range segments[:i] {
			if a.Points[0][1] == a.Points[1][1] && b.Points[0][0] == b.Points[1][0] {
				candidates[[2]float64{b.Points[0][0], a.Points[0][1]}] = true
			}
			if b.Points[0][1] == b.Points[1][1] && a.Points[0][0] == a.Points[1][0] {
				candidates[[2]float64{a.Points[0][0], b.Points[0][1]}] = true
			}
		}
	}
	var out [][2]float64
	for p := range candidates {
		nets := map[string]bool{}
		rays := map[[2]int]bool{}
		for _, w := range segments {
			if !plOnSegment(p, w.Points[0], w.Points[1]) {
				continue
			}
			nets[w.Net] = true
			for _, q := range w.Points {
				if q == p {
					continue
				}
				dx, dy := 0, 0
				if q[0] > p[0] {
					dx = 1
				}
				if q[0] < p[0] {
					dx = -1
				}
				if q[1] > p[1] {
					dy = 1
				}
				if q[1] < p[1] {
					dy = -1
				}
				rays[[2]int{dx, dy}] = true
			}
		}
		if len(nets) == 1 && len(rays) >= 3 {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i][0] != out[j][0] {
			return out[i][0] < out[j][0]
		}
		return out[i][1] < out[j][1]
	})
	return out
}
