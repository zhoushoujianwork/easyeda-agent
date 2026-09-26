package app

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestTitleBlockTextUnknownVisibilityRejectedBeforeWrite(t *testing.T) {
	for _, tc := range []struct {
		name    string
		current map[string]any
		patch   map[string]any
		missing []string
	}{
		{"both null string", map[string]any{"showTitle": nil, "showValue": nil}, map[string]any{"Name": "new"}, []string{"Name.showTitle", "Name.showValue"}},
		{"both missing object", map[string]any{}, map[string]any{"Name": map[string]any{"value": "new"}}, []string{"Name.showTitle", "Name.showValue"}},
		{"title missing", map[string]any{"showValue": false}, map[string]any{"Name": "new"}, []string{"Name.showTitle"}},
		{"value flag missing", map[string]any{"showTitle": false}, map[string]any{"Name": "new"}, []string{"Name.showValue"}},
		{"title nonboolean", map[string]any{"showTitle": "false", "showValue": true}, map[string]any{"Name": "new"}, []string{"Name.showTitle"}},
		{"value flag nonboolean", map[string]any{"showTitle": true, "showValue": 0}, map[string]any{"Name": "new"}, []string{"Name.showValue"}},
		{"explicit title leaves value unknown", map[string]any{}, map[string]any{"Name": map[string]any{"value": "new", "showTitle": false}}, []string{"Name.showValue"}},
		{"visibility-only requires title initialization", map[string]any{}, map[string]any{"Name": map[string]any{"showValue": false}}, []string{"Name.showTitle"}},
		{"mixed fields atomic refusal", map[string]any{}, map[string]any{"Drawed": "team", "Name": "new"}, []string{"Name.showTitle", "Name.showValue"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.current["value"] = "old"
			full := map[string]any{"Name": tc.current, "Drawed": map[string]any{"value": "old team", "showTitle": false, "showValue": true}}
			cfg, calls, closeFn := newAutolayoutTestDaemon(t, func(_ int, call autolayoutTestCall) string {
				switch call.Action {
				case "schematic.titleblock.get":
					b, _ := json.Marshal(map[string]any{"showTitleBlock": true, "titleBlockData": full})
					return autolayoutOK("page-1", string(b))
				case "schematic.titleblock.modify":
					// Deliberately permissive transport: CLI must refuse before
					// relying on a host error or post-write checks.
					for key, item := range call.Payload["titleBlockData"].(map[string]any) {
						cur := full[key].(map[string]any)
						for field, value := range item.(map[string]any) {
							cur[field] = value
						}
					}
					return autolayoutOK("page-1", `{"ok":true,"verified":true}`)
				case "schematic.components.list":
					return autolayoutOK("page-1", `{"components":[{"componentType":"sheet","primitiveId":"sheet-1","bbox":{"minX":0,"minY":0,"maxX":1000,"maxY":800}}]}`)
				default:
					t.Errorf("unexpected action %s", call.Action)
					return `{"ok":false}`
				}
			})
			defer closeFn()
			patch, _ := json.Marshal(tc.patch)
			var out, stderr bytes.Buffer
			cmd := newSchCmd(cfg, &out, &stderr)
			cmd.SetArgs([]string{"titleblock", "--data", string(patch)})
			cmd.SilenceErrors, cmd.SilenceUsage = true, true
			err := cmd.Execute()
			if err == nil {
				t.Errorf("unknown visibility update unexpectedly accepted: %s", out.String())
			} else {
				for _, missing := range tc.missing {
					if !strings.Contains(err.Error(), missing) {
						t.Errorf("missing exact diagnostic %s: %v", missing, err)
					}
				}
			}
			got := calls.snapshot()
			if len(got) != 1 || got[0].Action != "schematic.titleblock.get" {
				t.Errorf("must stop after one read, before every write/recovery: %+v", got)
			}
		})
	}
}

func TestTitleBlockKnownOrExplicitVisibilityPreserved(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		current, patch, want map[string]any
	}{
		{"preserve false true", map[string]any{"showTitle": false, "showValue": true}, map[string]any{"value": "new"}, map[string]any{"value": "new", "showTitle": false, "showValue": true}},
		{"preserve true false", map[string]any{"showTitle": true, "showValue": false}, map[string]any{"value": "new"}, map[string]any{"value": "new", "showTitle": true, "showValue": false}},
		{"explicit both", map[string]any{"showTitle": nil, "showValue": nil}, map[string]any{"value": "new", "showTitle": false, "showValue": false}, map[string]any{"value": "new", "showTitle": false, "showValue": false}},
		{"only missing flag explicit", map[string]any{"showTitle": true}, map[string]any{"value": "new", "showValue": false}, map[string]any{"value": "new", "showTitle": true, "showValue": false}},
		{"title-only does not request value flag", map[string]any{}, map[string]any{"showTitle": false}, map[string]any{"showTitle": false}},
		{"value visibility with known title", map[string]any{"showTitle": false}, map[string]any{"showValue": false}, map[string]any{"showTitle": false, "showValue": false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, _, err := buildTitleBlockTextPatch(map[string]any{"Name": tc.current}, map[string]any{"Name": tc.patch}, true)
			if err != nil || !reflect.DeepEqual(out, map[string]any{"Name": tc.want}) {
				t.Fatalf("known/explicit flags changed: %#v %v", out, err)
			}
		})
	}
}

func TestTitleBlockCobraExplicitUnknownVisibilityWritesOnce(t *testing.T) {
	// Generic form of the live P3 009/012 pair: two unknown fields receive an
	// explicit design decision, while Drawed keeps its known visibility.
	full := map[string]any{
		"Name":        map[string]any{"value": "", "showTitle": nil, "showValue": nil},
		"Description": map[string]any{"value": "", "showTitle": nil, "showValue": nil},
		"Drawed":      map[string]any{"value": "", "showTitle": false, "showValue": true},
		"Border":      map[string]any{"value": "1"},
	}
	patch := map[string]any{
		"Name":        map[string]any{"value": "MCU AND LED", "showTitle": false, "showValue": false},
		"Description": map[string]any{"value": "Common 3V3", "showTitle": false, "showValue": false},
		"Drawed":      map[string]any{"value": "Design team", "showTitle": false, "showValue": true},
	}
	cfg, calls, closeFn := newAutolayoutTestDaemon(t, func(_ int, call autolayoutTestCall) string {
		switch call.Action {
		case "schematic.titleblock.get":
			b, _ := json.Marshal(map[string]any{"showTitleBlock": true, "titleBlockData": full})
			return autolayoutOK("page-1", string(b))
		case "schematic.titleblock.modify":
			wanted := map[string]any{"titleBlockData": patch}
			if !reflect.DeepEqual(call.Payload, wanted) {
				t.Errorf("explicit request changed or unrelated state written: %#v", call.Payload)
			}
			for key, item := range patch {
				full[key] = item
			}
			return autolayoutOK("page-1", `{"ok":true,"verified":true}`)
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
	b, _ := json.Marshal(patch)
	cmd.SetArgs([]string{"titleblock", "--data", string(b)})
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	if err := cmd.Execute(); err != nil {
		t.Fatalf("explicit design request rejected: %v %s", err, stderr.String())
	}
	var actions []string
	for _, call := range calls.snapshot() {
		actions = append(actions, call.Action)
	}
	want := []string{"schematic.titleblock.get", "schematic.titleblock.modify", "schematic.components.list", "schematic.titleblock.get"}
	if !reflect.DeepEqual(actions, want) {
		t.Fatalf("wanted one write plus fresh verification, got %v", actions)
	}
}

func TestTitleBlockCobraOverallVisibilityOnlyUnchanged(t *testing.T) {
	for _, flag := range []string{"--show", "--hide"} {
		t.Run(flag, func(t *testing.T) {
			cfg, calls, closeFn := newAutolayoutTestDaemon(t, func(_ int, call autolayoutTestCall) string {
				switch call.Action {
				case "schematic.titleblock.modify":
					if !reflect.DeepEqual(call.Payload, map[string]any{"showTitleBlock": flag == "--show"}) {
						t.Errorf("overall visibility changed: %v", call.Payload)
					}
					return autolayoutOK("page-1", `{"ok":true,"verified":true}`)
				case "schematic.components.list":
					return autolayoutOK("page-1", `{"components":[{"componentType":"sheet","primitiveId":"sheet-1","bbox":{"minX":0,"minY":0,"maxX":1000,"maxY":800}}]}`)
				default:
					t.Errorf("no per-field update requested: %s", call.Action)
					return `{"ok":false}`
				}
			})
			defer closeFn()
			var out, stderr bytes.Buffer
			cmd := newSchCmd(cfg, &out, &stderr)
			cmd.SetArgs([]string{"titleblock", flag})
			cmd.SilenceErrors, cmd.SilenceUsage = true, true
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if len(calls.snapshot()) != 2 {
				t.Fatalf("changed overall visibility flow: %v", calls.snapshot())
			}
		})
	}
}
