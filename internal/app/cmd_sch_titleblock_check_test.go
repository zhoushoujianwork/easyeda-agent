package app

import (
	"strings"
	"testing"
)

// tbItem 是 titleblock.get 返回的一条明细项。
func tbItem(value string) map[string]any {
	return map[string]any{"showTitle": true, "showValue": true, "value": value}
}

func tbTypes(fs []*checkFinding) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Type)
	}
	return out
}

func tbFindingOfType(t *testing.T, fs []*checkFinding, typ string) *checkFinding {
	t.Helper()
	for _, f := range fs {
		if f.Type == typ {
			return f
		}
	}
	t.Fatalf("no %s finding in %v", typ, tbTypes(fs))
	return nil
}

func TestTitleBlockFindingsFor_FilledPageIsClean(t *testing.T) {
	fs := titleBlockFindingsFor(map[string]any{
		"Name":        tbItem("USB-C 5V to 3V3"),
		"Drawed":      tbItem("Roy"),
		"Description": tbItem("LDO test board"),
	})
	if len(fs) != 0 {
		t.Fatalf("a fully filled title block must produce nothing, got %v", tbTypes(fs))
	}
}

func TestTitleBlockFindingsForHiddenRequiredValuesBlockStrict(t *testing.T) {
	name, description := tbItem("LED board"), tbItem("3.3V indicator")
	name["showValue"], description["showValue"] = false, false
	data := map[string]any{"Name": name, "Description": description, "Drawed": tbItem("test"), "Reviewed": map[string]any{"value": "optional", "showValue": false}}
	fs := titleBlockFindingsFor(data)
	f := tbFindingOfType(t, fs, "hidden-titleblock")
	if len(fs) != 1 || f.Count != 2 || !checkLevelBlocks(f.Level, true) || !strings.Contains(f.Message, "attribute-move") {
		t.Fatalf("hidden required text passed: %+v", fs)
	}
	name["showValue"], description["showValue"] = true, true
	if got := titleBlockFindingsFor(data); len(got) != 0 {
		t.Fatalf("restored values or optional hidden field rejected: %+v", got)
	}
}

// 有这个明细项、只是空着 —— 这是 `sch titleblock` 真的能修的那一种,提示照给,
// --strict 照挡。
func TestTitleBlockFindingsFor_BlankItemStaysBlockingUnderStrict(t *testing.T) {
	fs := titleBlockFindingsFor(map[string]any{
		"Name":        tbItem("   "),
		"Drawed":      tbItem("Roy"),
		"Description": tbItem("LDO test board"),
	})
	f := tbFindingOfType(t, fs, "missing-titleblock")
	if len(fs) != 1 || f.Count != 1 || !strings.Contains(f.Message, "Name") {
		t.Fatalf("finding = %+v (all: %v)", f, tbTypes(fs))
	}
	if strings.Contains(f.Message, "Drawed(") {
		t.Errorf("a filled item must not be reported: %s", f.Message)
	}
	if !checkLevelBlocks(f.Level, true) {
		t.Errorf("an actually fixable blank item must keep blocking --strict, level=%q", f.Level)
	}
}

// 页上根本没有这个明细项(国际版图签模板不带 `Drawed`)。
// 此前它与「空着」走同一条路:valueOf 读不到就当空值 → 报 missing-titleblock →
// --strict 恒 FAIL。而 schTitleBlockPatch 对不在 titleBlockData 里的 key 一律拒写,
// 所以提示里那条命令必定失败 —— 门禁要求了一件没有任何命令能做到的事。
func TestTitleBlockFindingsFor_AbsentItemIsNotDemanded(t *testing.T) {
	data := map[string]any{
		"Name":         tbItem("USB-C 5V to 3V3"),
		"Description":  tbItem("LDO test board"),
		"Drawn":        tbItem(""), // 模板里的同义栏位,名字不同
		"Reviewed":     tbItem(""),
		"@Board Name":  tbItem("Board1"), // 系统派生项,写不进去
		"@Project Nam": tbItem("x"),
	}
	fs := titleBlockFindingsFor(data)

	for _, f := range fs {
		if f.Type == "missing-titleblock" && strings.Contains(f.Message, "Drawed") {
			t.Fatalf("an item the page does not have must not be demanded: %s", f.Message)
		}
	}
	f := tbFindingOfType(t, fs, "titleblock-key-absent")
	if f.Count != 1 || !strings.Contains(f.Message, "Drawed") {
		t.Fatalf("absent finding = %+v", f)
	}
	// 关键回归:它必须永不阻塞 —— 否则 `sch gate --strict` 依旧无解。
	if checkLevelBlocks(f.Level, true) {
		t.Fatalf("an unfixable item must never block, even under --strict (level=%q)", f.Level)
	}
	// 要把这一页真正能写的 key 列出来,人才好照着改命令;`@` 派生项不算。
	for _, want := range []string{"Drawn", "Reviewed", "Name", "Description"} {
		if !strings.Contains(f.Message, want) {
			t.Errorf("available keys must list %q: %s", want, f.Message)
		}
	}
	if strings.Contains(f.Message, "@Board Name") {
		t.Errorf("system-derived keys cannot be written and must not be offered: %s", f.Message)
	}
}

// 两种情况同时存在时各走各的:空着的照挡,缺项的只报知。
func TestTitleBlockFindingsFor_BlankAndAbsentAreReportedSeparately(t *testing.T) {
	fs := titleBlockFindingsFor(map[string]any{
		"Name":        tbItem(""),
		"Description": tbItem("LDO test board"),
	})
	blank := tbFindingOfType(t, fs, "missing-titleblock")
	if blank.Count != 1 || !strings.Contains(blank.Message, "Name") || strings.Contains(blank.Message, "Drawed(") {
		t.Fatalf("blank finding = %+v", blank)
	}
	absent := tbFindingOfType(t, fs, "titleblock-key-absent")
	if absent.Count != 1 || !strings.Contains(absent.Message, "Drawed") {
		t.Fatalf("absent finding = %+v", absent)
	}
}

func TestTitleBlockAvailableKeys_SortedWithoutDerived(t *testing.T) {
	got := titleBlockAvailableKeys(map[string]any{
		"Name": tbItem(""), "Drawn": tbItem(""), "@Page No": tbItem(""),
	})
	if len(got) != 2 || got[0] != "Drawn" || got[1] != "Name" {
		t.Fatalf("got %v, want [Drawn Name]", got)
	}
	if got := titleBlockAvailableKeys(map[string]any{"@Board Name": tbItem("")}); len(got) != 1 || !strings.Contains(got[0], "无可写") {
		t.Fatalf("a page with only derived items must say so, got %v", got)
	}
}
