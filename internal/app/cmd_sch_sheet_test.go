package app

import "testing"

func boolPtr(b bool) *bool { return &b }

func hasWarningContaining(warnings []string, substr string) bool {
	for _, w := range warnings {
		if len(w) >= len(substr) && containsStr(w, substr) {
			return true
		}
	}
	return false
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// Known template: an A-series landscape sheet (aspect ≈ √2 ≈ 1.414). The
// title-block keep-out must be the bottom-right corner, tagged known-template.
func TestDeriveSheetGeometry_KnownTemplateA4Landscape(t *testing.T) {
	// 1160 / 820 ≈ 1.4146 → matches a-series-landscape.
	sheet := bb(0, 0, 1160, 820)
	g := deriveSheetGeometry(sheet, boolPtr(true))

	if g.Sheet.Template != "a-series-landscape" {
		t.Fatalf("expected a-series-landscape, got %q", g.Sheet.Template)
	}
	if g.TitleBlock.Source != sheetSourceKnownTemplate {
		t.Fatalf("expected source %q, got %q", sheetSourceKnownTemplate, g.TitleBlock.Source)
	}
	if g.TitleBlock.BBox == nil {
		t.Fatal("expected a title-block bbox")
	}
	tb := g.TitleBlock.BBox
	// 22% width × 14% height, anchored to the high-x/high-y (bottom-right) corner.
	if tb.MaxX != 1160 || tb.MaxY != 820 {
		t.Errorf("keep-out must anchor to the bottom-right corner, got max %.2f,%.2f", tb.MaxX, tb.MaxY)
	}
	if tb.MinX != round2(1160-0.22*1160) || tb.MinY != round2(820-0.14*820) {
		t.Errorf("unexpected keep-out min corner: %.2f,%.2f", tb.MinX, tb.MinY)
	}
	if len(g.Keepouts) != 1 || g.Keepouts[0].Name != "titleBlock" || !g.Keepouts[0].Hard {
		t.Fatalf("expected one hard titleBlock keepout, got %+v", g.Keepouts)
	}
	if hasWarningContaining(g.Warnings, "fallback") {
		t.Errorf("known template must not warn about fallback: %v", g.Warnings)
	}
}

// Unknown template: a square-ish sheet matches no known aspect → fallback ratio,
// keep-out still emitted, but provenance downgraded and a warning surfaced.
func TestDeriveSheetGeometry_UnknownTemplate(t *testing.T) {
	sheet := bb(0, 0, 1000, 1000) // aspect 1.0 → no match
	g := deriveSheetGeometry(sheet, boolPtr(true))

	if g.TitleBlock.Source != sheetSourceFallback {
		t.Fatalf("expected source %q, got %q", sheetSourceFallback, g.TitleBlock.Source)
	}
	if g.TitleBlock.BBox == nil {
		t.Fatal("fallback must still emit a keep-out (generic ratio)")
	}
	if len(g.Keepouts) != 1 {
		t.Fatalf("expected one keepout in fallback, got %d", len(g.Keepouts))
	}
	if !hasWarningContaining(g.Warnings, "did not match a known template") {
		t.Errorf("expected an unrecognized-template warning, got %v", g.Warnings)
	}
}

// A hidden title block must emit NO keep-out and say so.
func TestDeriveSheetGeometry_HiddenTitleBlock(t *testing.T) {
	sheet := bb(0, 0, 1160, 820)
	g := deriveSheetGeometry(sheet, boolPtr(false))

	if g.TitleBlock.BBox != nil {
		t.Errorf("hidden title block must not produce a bbox, got %+v", g.TitleBlock.BBox)
	}
	if len(g.Keepouts) != 0 {
		t.Errorf("hidden title block must emit no keepouts, got %d", len(g.Keepouts))
	}
	if g.TitleBlock.Source != sheetSourceNone {
		t.Errorf("expected source %q, got %q", sheetSourceNone, g.TitleBlock.Source)
	}
	if !hasWarningContaining(g.Warnings, "hidden") {
		t.Errorf("expected a hidden-title-block warning, got %v", g.Warnings)
	}
}

// No sheet primitive → no geometry, a warning, no false precision.
func TestDeriveSheetGeometry_NoSheet(t *testing.T) {
	g := deriveSheetGeometry(nil, nil)

	if g.Sheet.BBox != nil {
		t.Errorf("no sheet must yield a nil sheet bbox, got %+v", g.Sheet.BBox)
	}
	if g.TitleBlock.BBox != nil || len(g.Keepouts) != 0 {
		t.Errorf("no sheet must yield no keep-out, got tb=%+v keepouts=%d", g.TitleBlock.BBox, len(g.Keepouts))
	}
	if !hasWarningContaining(g.Warnings, "no sheet primitive found") {
		t.Errorf("expected a no-sheet warning, got %v", g.Warnings)
	}
}

// Unknown visibility (showTitleBlock not reported) assumes visible and warns.
func TestDeriveSheetGeometry_UnknownVisibility(t *testing.T) {
	sheet := bb(0, 0, 1160, 820)
	g := deriveSheetGeometry(sheet, nil)

	if g.TitleBlock.BBox == nil {
		t.Fatal("unknown visibility must assume visible and emit a keep-out")
	}
	if !hasWarningContaining(g.Warnings, "visibility unknown") {
		t.Errorf("expected a visibility-unknown warning, got %v", g.Warnings)
	}
}

// Degenerate sheet bbox (non-positive dimensions) → no keep-out, warning.
func TestDeriveSheetGeometry_DegenerateBBox(t *testing.T) {
	sheet := bb(100, 100, 100, 100) // zero width/height
	g := deriveSheetGeometry(sheet, boolPtr(true))

	if g.TitleBlock.BBox != nil || len(g.Keepouts) != 0 {
		t.Errorf("degenerate bbox must not derive a keep-out, got %+v", g.TitleBlock.BBox)
	}
	if !hasWarningContaining(g.Warnings, "non-positive dimensions") {
		t.Errorf("expected a degenerate-bbox warning, got %v", g.Warnings)
	}
}

// The autoconnect keep-out must stay consistent with the shared derivation:
// same corner rect for a known sheet, provisional when no sheet.
func TestTitleBlockKeepout_DelegatesToDerive(t *testing.T) {
	sheet := bb(0, 0, 1160, 820)
	box, provisional := titleBlockKeepout(sheet)
	if provisional {
		t.Fatal("a known sheet must not be provisional")
	}
	g := deriveSheetGeometry(sheet, nil)
	if box == nil || g.TitleBlock.BBox == nil || *box != *g.TitleBlock.BBox {
		t.Errorf("titleBlockKeepout must match deriveSheetGeometry; got %+v vs %+v", box, g.TitleBlock.BBox)
	}

	if nilBox, prov := titleBlockKeepout(nil); nilBox != nil || !prov {
		t.Errorf("no sheet → nil keep-out + provisional, got box=%+v prov=%v", nilBox, prov)
	}
}
