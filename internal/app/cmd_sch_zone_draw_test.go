package app

import (
	"strings"
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/internal/workflow"
)

// TestBuildZoneDrawJS pins the generated script: deterministic module order,
// inset dashed rects, y-down label anchor just inside the top-left corner.
func TestBuildZoneDrawJS(t *testing.T) {
	sheet := layoutBBox{MinX: 0, MinY: 0, MaxX: 900, MaxY: 600}
	zones := map[string]*schZoneClaim{
		"POWER": {Zone: "left-top", Parts: []string{"U3"}},
		"MCU":   {Zone: "center", Parts: []string{"U1"}},
		"BAD":   {Zone: "nope", Parts: []string{"X1"}}, // unknown zone → skipped
	}
	js := buildZoneDrawJS(zones, sheet, "#AA00AA")
	if !strings.Contains(js, `"MCU (center)"`) || !strings.Contains(js, `"POWER (left-top)"`) {
		t.Errorf("labels missing:\n%s", js)
	}
	if strings.Contains(js, "BAD") {
		t.Error("unknown zone was not skipped")
	}
	// MCU (center, full height) rect: x=300+4, y=0+4, w=300-8, h=600-8.
	if !strings.Contains(js, "create(304, 4, 292, 592, 0, 0, \"#AA00AA\", null, 1, 1)") {
		t.Errorf("MCU rect geometry wrong:\n%s", js)
	}
	// Deterministic order: MCU before POWER (sorted).
	if strings.Index(js, "MCU") > strings.Index(js, "POWER") {
		t.Error("modules not emitted in sorted order")
	}
	if !strings.Contains(js, "return {rects, texts};") {
		t.Error("script must return the created ids")
	}
}

func TestBuildZoneClearJS(t *testing.T) {
	js := buildZoneClearJS(&workflow.SchZoneFrames{Rects: []string{"r1"}, Texts: []string{"t1", "t2"}})
	if !strings.Contains(js, `["r1"]`) || !strings.Contains(js, `["t1","t2"]`) {
		t.Errorf("ids not embedded:\n%s", js)
	}
}
