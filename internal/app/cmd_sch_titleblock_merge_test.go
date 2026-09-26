package app

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestTitleBlockCobraFailureDoesNotMaskUnappliedHide(t *testing.T) {
	cfg, calls, closeFn := newAutolayoutTestDaemon(t, func(_ int, call autolayoutTestCall) string {
		switch call.Action {
		case "schematic.titleblock.get":
			return autolayoutOK("page-1", `{"showTitleBlock":true,"titleBlockData":{"Name":{"value":"same","showTitle":false,"showValue":true}}}`)
		case "schematic.titleblock.modify":
			if call.Payload["showTitleBlock"] != false {
				t.Errorf("hide not sent: %v", call.Payload)
			}
			return `{"ok":false,"error":{"code":"HOST_ERROR","message":"hide refused"}}`
		case "schematic.components.list":
			return autolayoutOK("page-1", `{"components":[{"componentType":"sheet","primitiveId":"sheet-1","bbox":{"minX":0,"minY":0,"maxX":1000,"maxY":800}}]}`)
		default:
			t.Errorf("unexpected action %s", call.Action)
			return `{"ok":false}`
		}
	})
	defer closeFn()
	var out, stderr bytes.Buffer
	cmd := newSchCmd(cfg, &out, &stderr)
	cmd.SetArgs([]string{"titleblock", "--hide", "--data", `{"Name":"same"}`})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	if err := cmd.Execute(); err == nil {
		t.Fatalf("failed hide was masked by matching text; stdout=%s stderr=%s", out.String(), stderr.String())
	}
	mutations := 0
	for _, call := range calls.snapshot() {
		if call.Action == "schematic.titleblock.modify" {
			mutations++
		}
	}
	if mutations != 1 {
		t.Fatalf("failed modify retried: %d", mutations)
	}
}

// 写图签只传**用户点名的项**(2026-08-26 起不再整包回传)。
//
// 曾经的 TestTbKeepStructural 测的是「整包回传时按住 Title Block/Border 两个结构
// 开关」——那套保护随整包回传一起删了:整包回传本身才是图框损毁的成因(#186)。
// 现在的不变式是**结构键压根不进 payload**,由下面这条钉住。
func TestSchTitleBlockMerge_OnlySendsRequestedKeys(t *testing.T) {
	full := map[string]any{
		"Title Block": map[string]any{"value": "1"},
		"Border":      map[string]any{"value": "1"},
		"Device":      map[string]any{"value": "Drawing-Symbol_A4"},
		"Name":        map[string]any{"value": "old", "showTitle": nil, "showValue": nil},
		"Drawed":      map[string]any{"value": "", "showTitle": false, "showValue": true},
	}
	cfg, _, closeFn := newAutolayoutTestDaemon(t, func(_ int, call autolayoutTestCall) string {
		if call.Action != "schematic.titleblock.get" {
			t.Errorf("unexpected action %s", call.Action)
		}
		return autolayoutOK("page-1", `{"showTitleBlock":true,"titleBlockData":{"Name":{"value":"old","showTitle":null,"showValue":null},"Drawed":{"value":"","showTitle":false,"showValue":true},"Device":{"value":"Drawing-Symbol_A4"},"Border":{"value":"1"}}}`)
	})
	defer closeFn()
	out, show, err := schTitleBlockMerge(cfg, "w1", map[string]any{"Name": map[string]any{"value": "new", "showTitle": false, "showValue": false}, "Drawed": "Design team"})
	if err != nil || show {
		t.Fatalf("merge: %v, show=%v", err, show)
	}
	if len(out) != 2 {
		t.Fatalf("only requested text keys: %v", out)
	}
	if !reflect.DeepEqual(out["Name"], map[string]any{"value": "new", "showTitle": false, "showValue": false}) || !reflect.DeepEqual(out["Drawed"], map[string]any{"value": "Design team", "showTitle": false, "showValue": true}) {
		t.Fatalf("text update changed visibility: %#v", out)
	}
	for _, forbidden := range []string{"Title Block", "Border", "Device"} {
		if _, present := out[forbidden]; present {
			t.Errorf("结构键 %s 绝不能出现在下发数据里(#186 图框损毁成因)", forbidden)
		}
	}
	if full["Name"].(map[string]any)["value"] != "old" {
		t.Fatal("source changed")
	}
}

func TestTitleBlockPatchExplicitVisibility(t *testing.T) {
	full := map[string]any{"Name": map[string]any{"value": "keep", "showTitle": true, "showValue": true}}
	out, _, err := buildTitleBlockTextPatch(full, map[string]any{"Name": map[string]any{"showTitle": false, "showValue": false}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out["Name"], map[string]any{"showTitle": false, "showValue": false}) {
		t.Fatalf("visibility-only update lost content: %#v", out)
	}
	for _, patch := range []map[string]any{
		nil, {},
		{"Name": map[string]any{"showTitle": nil}}, {"Name": map[string]any{"showValue": "false"}}, {"Name": map[string]any{"showTitel": false}}, {"Name": map[string]any{}}, {"Missing": "X"},
	} {
		if out, _, err := buildTitleBlockTextPatch(full, patch, true); err == nil || out != nil {
			t.Fatalf("invalid patch accepted: %v, out=%v, err=%v", patch, out, err)
		}
	}
}

func TestTitleBlockLandedRequiresOverallVisibility(t *testing.T) {
	cfg, calls, closeFn := newAutolayoutTestDaemon(t, func(_ int, _ autolayoutTestCall) string {
		return autolayoutOK("page-1", `{"showTitleBlock":true,"titleBlockData":{"Name":{"value":"same","showTitle":false,"showValue":true}}}`)
	})
	defer closeFn()
	if landed, missing := tbPatchLanded(cfg, "w1", map[string]any{"Name": "same"}, false); landed || len(missing) == 0 {
		t.Fatalf("matching text masked failed --hide: %v %v", landed, missing)
	}
	before := len(calls.snapshot())
	if landed, _ := tbPatchLanded(cfg, "w1", map[string]any{}); landed {
		t.Fatal("empty patch supplied success evidence")
	}
	if len(calls.snapshot()) != before {
		t.Fatal("empty fallback queried editor")
	}
}

func TestTitleBlockUnknownOverallVisibilityDoesNotAutoShow(t *testing.T) {
	cfg, _, closeFn := newAutolayoutTestDaemon(t, func(_ int, _ autolayoutTestCall) string {
		return autolayoutOK("page-1", `{"showTitleBlock":null,"titleBlockData":{"Name":{"value":"old","showTitle":false,"showValue":true}}}`)
	})
	defer closeFn()
	if _, needShow, err := schTitleBlockMerge(cfg, "w1", map[string]any{"Name": "new"}); err != nil || needShow {
		t.Fatalf("unknown overall visibility caused auto-show: %v %v", needShow, err)
	}
}

func TestTitleBlockLandedRequiresVisibility(t *testing.T) {
	current := map[string]any{"value": "same", "showTitle": true, "showValue": true}
	if tbItemMatches(current, map[string]any{"value": "same", "showTitle": false}) {
		t.Fatal("unchanged text masked unlanded visibility")
	}
	if tbItemMatches(current, map[string]any{"showValue": false}) {
		t.Fatal("visibility-only failure masked")
	}
	if tbItemMatches(map[string]any{"value": "same", "showTitle": nil}, map[string]any{"value": "same", "showTitle": false}) {
		t.Fatal("unknown visibility guessed false")
	}
	if !tbItemMatches(current, map[string]any{"value": "same", "showTitle": true}) || !tbItemMatches(current, "same") {
		t.Fatal("matching state rejected")
	}
	cfg, _, closeFn := newAutolayoutTestDaemon(t, func(_ int, call autolayoutTestCall) string {
		if !strings.HasPrefix(call.Action, "schematic.titleblock.") {
			t.Errorf("unexpected action %s", call.Action)
		}
		return autolayoutOK("page-1", `{"titleBlockData":{"Name":{"value":"same","showTitle":false,"showValue":false}}}`)
	})
	defer closeFn()
	if landed, missing := tbPatchLanded(cfg, "w1", map[string]any{"Name": map[string]any{"value": "same", "showTitle": false, "showValue": false}}); !landed || len(missing) != 0 {
		t.Fatalf("matching full patch refused: %v %v", landed, missing)
	}
}
