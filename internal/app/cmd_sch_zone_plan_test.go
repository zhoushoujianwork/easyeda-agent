package app

import "testing"

// The issue #149 real 6-module A4 page: the planner must carve it into
// non-overlapping, in-sheet partitions that each fully contain their module and
// leave the bottom-right title block a gap — validation all zero.
func TestPlanPartitions_RealA4SixModules(t *testing.T) {
	sheet := layoutBBox{0, 0, 1170, 825}
	keepout := &layoutBBox{912.6, 0, 1170, 115.5}
	mods := []partitionModule{
		{"音频接口", layoutBBox{104.5, 579.5, 285.5, 660.5}},
		{"调试接口", layoutBBox{574.5, 584.5, 600.5, 655.5}},
		{"RGB与编码器", layoutBBox{954.45, 509.5, 995.5, 694.5}},
		{"用户输入", layoutBBox{104.5, 184.5, 255.5, 262}},
		{"显示接口", layoutBBox{499.5, 369.5, 600.5, 460.5}},
		{"马达驱动", layoutBBox{909.5, 149.5, 1035.5, 273}},
	}
	plan := planPartitions(sheet, keepout, mods, defaultPartitionOpts())
	if len(plan.Partitions) == 0 {
		t.Fatal("no partitions produced")
	}
	if !plan.Validation.clean() {
		t.Fatalf("validation not clean: %+v\npartitions: %+v", plan.Validation, plan.Partitions)
	}
	// Every module must be assigned to exactly one partition and fully inside it.
	assigned := map[string]int{}
	for _, p := range plan.Partitions {
		for _, name := range p.Modules {
			assigned[name]++
		}
	}
	for _, m := range mods {
		if assigned[m.Name] != 1 {
			t.Errorf("module %q assigned %d times (want 1)", m.Name, assigned[m.Name])
		}
	}
	// The partition containing 马达驱动 (bottom-right) must clear the title block.
	for _, p := range plan.Partitions {
		if strInSlice(p.Modules, "马达驱动") {
			if p.BBox.MinY <= keepout.MaxY {
				t.Errorf("马达驱动 partition bottom %.1f not lifted above title block %.1f", p.BBox.MinY, keepout.MaxY)
			}
		}
	}
}

func TestPlanPartitions_TwoModules(t *testing.T) {
	sheet := layoutBBox{0, 0, 1170, 825}
	mods := []partitionModule{
		{"主MCU", layoutBBox{456.5, 279.5, 713.5, 663.5}},
		{"复位", layoutBBox{173.5, 146.5, 255.5, 270.5}},
	}
	plan := planPartitions(sheet, nil, mods, defaultPartitionOpts())
	if len(plan.Partitions) < 1 {
		t.Fatal("no partitions")
	}
	if !plan.Validation.clean() {
		t.Fatalf("2-module plan not clean: %+v", plan.Validation)
	}
}

func TestPlanPartitions_EmptyIsNoop(t *testing.T) {
	plan := planPartitions(layoutBBox{0, 0, 1170, 825}, nil, nil, defaultPartitionOpts())
	if len(plan.Partitions) != 0 || !plan.Validation.clean() {
		t.Errorf("empty input → empty clean plan, got %+v", plan)
	}
}

// clusterSplits must split in the empty band between module bboxes, not through a
// straddling module.
func TestClusterSplits_NaturalGap(t *testing.T) {
	// Two clusters: [80,120] and [880,920] → one split in the 120↔880 band (~500).
	two := []axisInterval{{80, 120, 100}, {880, 920, 900}}
	got := clusterSplits(two, 12, 3)
	if len(got) != 1 {
		t.Fatalf("want 1 split for two clusters, got %v", got)
	}
	if got[0] < 400 || got[0] > 600 {
		t.Errorf("split %.0f should sit in the 120↔880 band (~500)", got[0])
	}
	// Intervals that OVERLAP on this axis (a tall module straddling) → no split.
	straddle := []axisInterval{{100, 700, 400}, {150, 260, 205}}
	if s := clusterSplits(straddle, 12, 3); len(s) != 0 {
		t.Errorf("overlapping intervals → no split, got %v", s)
	}
	// A band narrower than the gutter → no split (no room for two partitions).
	tight := []axisInterval{{100, 200, 150}, {205, 300, 252}}
	if s := clusterSplits(tight, 12, 3); len(s) != 0 {
		t.Errorf("5-unit band < 12 gutter → no split, got %v", s)
	}
}
