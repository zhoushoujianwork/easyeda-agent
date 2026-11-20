package app

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/internal/connectivity"
)

func designatorFixture(t *testing.T) ([]byte, schDesignatorBaseline, connectivity.Document) {
	t.Helper()
	raw := []byte(`{"ok":true,"seq":42,"context":{"projectUuid":"project","documentUuid":"page1","documentType":"schematic"},"result":{"components":[
{"primitiveId":"sheet","componentType":"sheet","otherProperty":{"@Update Date":"2026-09-06","@Update Time":"10:00:00","Width":"1170"}},
{"primitiveId":"p-rf","componentType":"part","designator":"U_RF","uniqueId":"unique-rf","x":100,"y":200,"rotation":0,"mirror":false,"device":{"libraryUuid":"official","uuid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"pinsAvailable":true,"netlistAvailable":true,"otherProperty":{"Datasheet":"source"},"bbox":{"minX":90,"minY":190,"maxX":110,"maxY":210},"pins":[{"pinNumber":"1","pinName":"VCC","x":90,"y":200,"noConnected":false,"net":"VCC"},{"pinNumber":"2","pinName":"NC","x":110,"y":200,"noConnected":true,"net":""}]},
{"primitiveId":"p-existing","componentType":"part","designator":"U1","uniqueId":"unique-1","x":300,"y":200,"rotation":90,"mirror":true,"device":{"libraryUuid":"official","uuid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"pinsAvailable":true,"netlistAvailable":true,"otherProperty":{},"pins":[{"pinNumber":"1","pinName":"VCC","x":300,"y":210,"noConnected":false,"net":"VCC"}]},
{"primitiveId":"flag","componentType":"netflag","x":70,"y":200,"rotation":0,"net":"VCC"}
],"wires":[{"x0":70,"y0":200,"x1":90,"y1":200,"net":"VCC"}],"connectivitySummary":{"scope":"activePage","wires":1,"buses":0,"netflags":1,"netports":0,"netlabels":0,"shortSymbols":0}}}`)
	before, err := parseSchDesignatorBaseline(raw)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := connectivity.FromRead(before.Result)
	if err != nil {
		t.Fatal(err)
	}
	doc.ProjectID, doc.DocumentID = "project", "page2" // Full-project target may retain a different originating page.
	target, _, err := connectivity.AllocateDesignators(doc, map[string]string{"official/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": "U?"})
	if err != nil {
		t.Fatal(err)
	}
	return raw, before, target
}

func designatorAfter(t *testing.T, before schDesignatorBaseline, target connectivity.Document) schDesignatorBaseline {
	t.Helper()
	raw, err := json.Marshal(actionResult{OK: true, Context: &before.Context, Result: before.Result})
	if err != nil {
		t.Fatal(err)
	}
	after, err := parseSchDesignatorBaseline(raw)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]connectivity.Component{}
	for _, c := range target.Components {
		byID[c.ID] = c
	}
	for pid, id := range before.IDs {
		c := after.Parts[pid]
		next := byID[id]
		c["designator"] = next.Ref
		props, _ := c["otherProperty"].(map[string]any)
		if props == nil {
			props = map[string]any{}
		}
		props[connectivity.ComponentIDProperty] = id
		if next.Role != "" {
			props[connectivity.ComponentRoleProperty] = next.Role
		}
		c["otherProperty"] = props
	}
	return after
}

func TestSchDesignatorsAllocateCLI(t *testing.T) {
	_, before, _ := designatorFixture(t)
	doc, err := connectivity.FromRead(before.Result)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	input := filepath.Join(dir, "source.json")
	prefixes := filepath.Join(dir, "prefixes.json")
	out := filepath.Join(dir, "target.json")
	changes := filepath.Join(dir, "changes.json")
	if err := schDesignatorsWriteJSON(input, doc, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prefixes, []byte(`{"official/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa":"U?"}`), 0600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	window := ""
	cmd := newSchDesignatorsCmd(&appConfig{}, &window, &buf, io.Discard)
	cmd.SetArgs([]string{"allocate", input, "--prefixes", prefixes, "--out", out, "--changes", changes})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var got connectivity.Document
	if err := schDesignatorsReadJSON(out, &got); err != nil {
		t.Fatal(err)
	}
	refs := map[string]string{}
	for _, c := range got.Components {
		refs[c.ID] = c.Ref
	}
	if refs["cmp-U_RF"] != "U2" || refs["cmp-U1"] != "U1" {
		t.Fatalf("wrong refs: %v", refs)
	}
	var delta []connectivity.DesignatorChange
	if err := schDesignatorsReadJSON(changes, &delta); err != nil {
		t.Fatal(err)
	}
	if len(delta) != 1 || delta[0].Before != "U_RF" || delta[0].After != "U2" {
		t.Fatalf("bad changes: %+v", delta)
	}
	if buf.Len() != 0 {
		t.Fatalf("unexpected stdout: %s", buf.String())
	}
}

func TestSchDesignatorsOutputAliasesRejected(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "source.json")
	if err := os.WriteFile(input, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"relative", "symlink", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			alias := filepath.Join(dir, kind)
			switch kind {
			case "relative":
				alias = dir + "/./source.json"
			case "symlink":
				if err := os.Symlink(input, alias); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(input, alias); err != nil {
					t.Fatal(err)
				}
			}
			if err := schDesignatorsDistinctFiles(input, alias); err == nil {
				t.Fatal("file alias accepted")
			}
			window := ""
			cmd := newSchDesignatorsCmd(&appConfig{}, &window, io.Discard, io.Discard)
			cmd.SetArgs([]string{"allocate", input, "--out", alias})
			if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "distinct") {
				t.Fatalf("wrong error: %v", err)
			}
			data, _ := os.ReadFile(input)
			if string(data) != "unchanged" {
				t.Fatal("input overwritten")
			}
		})
	}
	if err := schDesignatorsDistinctFiles(filepath.Join(dir, "prefix.json"), filepath.Join(dir, "prefix.json")); err == nil {
		t.Fatal("future output collision accepted")
	}
}

func TestSchDesignatorsPlanOnlyRepairsExistingInstances(t *testing.T) {
	_, before, target := designatorFixture(t)
	pb, err := buildSchDesignatorsPlan(before, target, "/baseline.json", "/target.json", "abc", "def")
	if err != nil {
		t.Fatal(err)
	}
	if !pb.RequireFullExecution || pb.Meta.Doc != "page1" || pb.Meta.Project != "project" || *pb.Defaults.Retry != 0 || *pb.Defaults.ContinueOnError {
		t.Fatalf("unguarded plan: %+v", pb)
	}
	if len(pb.Steps) != 5 {
		t.Fatalf("wrong steps: %+v", pb.Steps)
	}
	if pb.Steps[0].Run != "sch designators verify" || pb.Steps[0].Flags["phase"] != "before" || pb.Steps[0].Flags["target-sha"] != "def" {
		t.Fatal("missing file guard")
	}
	if pb.Steps[3].Run != "sch designators verify" || pb.Steps[3].Flags["sync-groups"] != true || pb.Steps[4].Action != "schematic.save" {
		t.Fatal("missing after/save")
	}
	for _, s := range pb.Steps[1:3] {
		if s.Action != "schematic.component.modify" {
			t.Fatalf("unexpected mutation: %+v", s)
		}
		patch := s.Payload["patch"].(map[string]any)
		if len(patch) != 2 || patch["uniqueId"] != nil || patch["x"] != nil || patch["customAttributes"] == nil {
			t.Fatalf("unexpected patch: %v", patch)
		}
	}
	after := designatorAfter(t, before, target)
	raw, _ := json.Marshal(actionResult{OK: true, Context: &after.Context, Result: after.Result})
	after, err = parseSchDesignatorBaseline(raw)
	if err != nil {
		t.Fatal(err)
	}
	next, err := buildSchDesignatorsPlan(after, target, "/a", "/b", "a", "b")
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Steps) != 3 {
		t.Fatalf("idempotent plan contains writes: %+v", next.Steps)
	}
}

func TestSchDesignatorsRejectIncompleteBaseline(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"context", func(m map[string]any) { delete(m, "context") }},
		{"summary", func(m map[string]any) { delete(m["result"].(map[string]any), "connectivitySummary") }},
		{"wires", func(m map[string]any) { m["result"].(map[string]any)["wires"] = []any{} }},
		{"identity", func(m map[string]any) {
			c := m["result"].(map[string]any)["components"].([]any)[1].(map[string]any)
			c["device"].(map[string]any)["uuid"] = "instance-id"
		}},
		{"net unavailable", func(m map[string]any) {
			c := m["result"].(map[string]any)["components"].([]any)[1].(map[string]any)
			c["pins"].([]any)[0].(map[string]any)["net"] = nil
		}},
		{"nc unavailable", func(m map[string]any) {
			c := m["result"].(map[string]any)["components"].([]any)[1].(map[string]any)
			delete(c["pins"].([]any)[0].(map[string]any), "noConnected")
		}},
		{"unique id", func(m map[string]any) {
			c := m["result"].(map[string]any)["components"].([]any)[1].(map[string]any)
			delete(c, "uniqueId")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, _, _ := designatorFixture(t)
			var m map[string]any
			_ = json.Unmarshal(raw, &m)
			tc.mutate(m)
			raw, _ = json.Marshal(m)
			if _, err := parseSchDesignatorBaseline(raw); err == nil {
				t.Fatal("incomplete evidence accepted")
			}
		})
	}
}

func TestSchDesignatorsSceneProtectsElectricalAndPhysicalData(t *testing.T) {
	_, before, target := designatorFixture(t)
	if err := verifySchDesignatorScene(before, target, before, "before"); err != nil {
		t.Fatal(err)
	}
	after := designatorAfter(t, before, target)
	if err := verifySchDesignatorScene(before, target, after, "after"); err != nil {
		t.Fatal(err)
	}
	after.Parts["p-rf"]["bbox"] = map[string]any{"minX": float64(80)}
	sheet := after.Result["components"].([]any)[0].(map[string]any)
	sheet["otherProperty"].(map[string]any)["@Update Time"] = "11:00:00"
	if err := verifySchDesignatorScene(before, target, after, "after"); err != nil {
		t.Fatalf("allowed render/timestamp change blocked: %v", err)
	}
	// Modifying a part hydrates device text metadata the baseline lacked.
	after.Parts["p-rf"]["manufacturer"] = "Espressif"
	if err := verifySchDesignatorScene(before, target, after, "after"); err != nil {
		t.Fatalf("hydrated manufacturer blocked: %v", err)
	}
	after.Parts["p-rf"]["manufacturerFlags"] = float64(1)
	if err := verifySchDesignatorScene(before, target, after, "after"); err == nil {
		t.Fatal("a new non-text field passed")
	}
	delete(after.Parts["p-rf"], "manufacturerFlags")
	for _, tc := range []struct {
		name, path string
		mutate     func(schDesignatorBaseline)
	}{
		{"position", "p-rf.x", func(s schDesignatorBaseline) { s.Parts["p-rf"]["x"] = float64(101) }},
		{"identity", "p-rf.uniqueId", func(s schDesignatorBaseline) { s.Parts["p-rf"]["uniqueId"] = "changed" }},
		{"library", "p-rf.device.uuid", func(s schDesignatorBaseline) {
			s.Parts["p-rf"]["device"].(map[string]any)["uuid"] = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		}},
		{"nc", "pins.2.noConnected", func(s schDesignatorBaseline) {
			s.Parts["p-rf"]["pins"].([]any)[1].(map[string]any)["noConnected"] = false
		}},
		{"net", "pins.1.net", func(s schDesignatorBaseline) { s.Parts["p-rf"]["pins"].([]any)[0].(map[string]any)["net"] = "GND" }},
		{"property", "otherProperty.Datasheet", func(s schDesignatorBaseline) { s.Parts["p-rf"]["otherProperty"].(map[string]any)["Datasheet"] = "lost" }},
		{"binding", "EasyEDA Agent Component ID", func(s schDesignatorBaseline) {
			delete(s.Parts["p-rf"]["otherProperty"].(map[string]any), connectivity.ComponentIDProperty)
		}},
		{"wire", "wires[0]", func(s schDesignatorBaseline) { s.Result["wires"].([]any)[0].(map[string]any)["x1"] = float64(91) }},
		{"flag", "flag.rotation", func(s schDesignatorBaseline) {
			s.Result["components"].([]any)[3].(map[string]any)["rotation"] = float64(180)
		}},
		{"sheet", "sheet.otherProperty.Width", func(s schDesignatorBaseline) {
			s.Result["components"].([]any)[0].(map[string]any)["otherProperty"].(map[string]any)["Width"] = "bad"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			live := designatorAfter(t, before, target)
			tc.mutate(live)
			err := verifySchDesignatorScene(before, target, live, "after")
			if err == nil || !strings.Contains(err.Error(), tc.path) {
				t.Fatalf("missing actionable difference %q: %v", tc.path, err)
			}
		})
	}
	if err := verifySchDesignatorScene(before, target, after, "before"); err == nil {
		t.Fatal("before guard accepted already changed state")
	}
}

func TestSchDesignatorsGlobalConflictsAndGroups(t *testing.T) {
	_, before, target := designatorFixture(t)
	global := &actionResult{OK: true, Context: &before.Context, Result: before.Result}
	if err := verifySchDesignatorGlobal(before, target, global, false); err != nil {
		t.Fatal(err)
	}
	copyGlobal := *global
	copyGlobal.Result = map[string]any{"components": append(append([]any{}, before.Result["components"].([]any)...), map[string]any{"componentType": "part", "designator": "U2", "otherProperty": map[string]any{connectivity.ComponentIDProperty: "cmp-U1"}})}
	if err := verifySchDesignatorGlobal(before, target, &copyGlobal, false); err == nil {
		t.Fatal("foreign page ref conflict accepted")
	}
	groups := []*schGroup{{ID: "one", Members: []string{"U_RF", "U1"}, Roles: map[string]string{"radio": "U_RF", "existing": "U1"}, Annotations: []string{"frame1"}}, {ID: "other", Members: []string{"CN1"}}}
	if !renameSchDesignatorGroups(groups, before, target) || !reflect.DeepEqual(groups[0].Members, []string{"U2", "U1"}) || groups[0].Roles["radio"] != "U2" || groups[0].Annotations[0] != "frame1" || groups[1].Members[0] != "CN1" {
		t.Fatalf("group sync changed wrong fields/order: %+v", groups)
	}
	if renameSchDesignatorGroups(groups, before, target) {
		t.Fatal("group sync not idempotent")
	}
}

func TestSchDesignatorsVerifySHAStopsBeforeNetwork(t *testing.T) {
	raw, _, target := designatorFixture(t)
	dir := t.TempDir()
	beforePath := filepath.Join(dir, "before.json")
	targetPath := filepath.Join(dir, "target.json")
	if err := os.WriteFile(beforePath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := schDesignatorsWriteJSON(targetPath, target, io.Discard); err != nil {
		t.Fatal(err)
	}
	window := ""
	cmd := newSchDesignatorsVerifyCmd(&appConfig{}, &window, io.Discard)
	cmd.SetArgs([]string{"--before", beforePath, "--target", targetPath, "--before-sha", "changed", "--target-sha", "changed", "--phase", "before"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "SHA mismatch") {
		t.Fatalf("wrong error: %v", err)
	}
}

func TestSchDesignatorsVerifyUsesOnlyFreshReads(t *testing.T) {
	raw, before, target := designatorFixture(t)
	dir := t.TempDir()
	beforePath := filepath.Join(dir, "before.json")
	targetPath := filepath.Join(dir, "target.json")
	if err := os.WriteFile(beforePath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := schDesignatorsWriteJSON(targetPath, target, io.Discard); err != nil {
		t.Fatal(err)
	}
	targetRaw, _ := os.ReadFile(targetPath)
	cfg, state, close := newAutolayoutTestDaemon(t, func(_ int, call autolayoutTestCall) string {
		if response, ok := autolayoutTargetMetadata(call, "page1", "Page 1"); ok {
			return response
		}
		if call.Action != "schematic.components.list" {
			t.Errorf("unexpected action %s", call.Action)
		}
		return string(raw)
	})
	defer close()
	window := "w1"
	var out bytes.Buffer
	cmd := newSchDesignatorsVerifyCmd(cfg, &window, &out)
	cmd.SetArgs([]string{"--before", beforePath, "--target", targetPath, "--before-sha", sha256Hex(raw), "--target-sha", sha256Hex(targetRaw), "--phase", "before"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var calls []autolayoutTestCall
	for _, call := range state.snapshot() {
		if call.Action == "schematic.components.list" {
			calls = append(calls, call)
		}
	}
	if len(calls) != 2 || calls[0].Payload["allPages"] != true || calls[1].Payload["includeDeviceIdentity"] != true || calls[1].Payload["includeWires"] != true || calls[1].Payload["includeBBox"] != false {
		t.Fatalf("wrong read evidence: %+v", calls)
	}
	if !strings.Contains(out.String(), before.Context.DocumentUUID) {
		t.Fatalf("missing verified context: %s", out.String())
	}
}

func TestSchDesignatorsVerifyRejectsConflictOnUnvisitedPage(t *testing.T) {
	raw, _, target := designatorFixture(t)
	dir := t.TempDir()
	beforePath := filepath.Join(dir, "before.json")
	targetPath := filepath.Join(dir, "target.json")
	if err := os.WriteFile(beforePath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := schDesignatorsWriteJSON(targetPath, target, io.Discard); err != nil {
		t.Fatal(err)
	}
	targetRaw, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	var full map[string]any
	if err := json.Unmarshal(raw, &full); err != nil {
		t.Fatal(err)
	}
	result := full["result"].(map[string]any)
	result["components"] = append(result["components"].([]any), map[string]any{
		"primitiveId": "p-unvisited", "componentType": "part", "designator": "U2",
		"uniqueId": "unique-unvisited", "documentUuid": "page-unvisited",
		"otherProperty": map[string]any{connectivity.ComponentIDProperty: "unvisited-component"},
	})
	fullRaw, err := json.Marshal(full)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, close := newAutolayoutTestDaemon(t, func(_ int, call autolayoutTestCall) string {
		if response, ok := autolayoutTargetMetadata(call, "page1", "Page 1"); ok {
			return response
		}
		if call.Action != "schematic.components.list" {
			t.Errorf("unexpected action %s", call.Action)
		}
		// Model the official API's session cache: an allPages getAll alone
		// cannot expose a conflicting component on a page never activated.
		if call.Payload["allPages"] == true && call.Payload["tagPages"] == true {
			return string(fullRaw)
		}
		return string(raw)
	})
	defer close()
	window := "w1"
	cmd := newSchDesignatorsVerifyCmd(cfg, &window, io.Discard)
	cmd.SetArgs([]string{"--before", beforePath, "--target", targetPath, "--before-sha", sha256Hex(raw), "--target-sha", sha256Hex(targetRaw), "--phase", "before"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "unvisited-component is missing from target or duplicated") {
		t.Fatalf("conflict on an unvisited page was not rejected: %v", err)
	}
}

func TestSchDesignatorsGlobalPageScope(t *testing.T) {
	_, before, target := designatorFixture(t)
	page := before.Context.DocumentUUID
	tagged := func(extra ...map[string]any) *actionResult {
		comps := []any{}
		for _, item := range before.Result["components"].([]any) {
			c := map[string]any{}
			for k, v := range item.(map[string]any) {
				c[k] = v
			}
			c["pageUuid"] = page
			comps = append(comps, c)
		}
		for _, c := range extra {
			comps = append(comps, c)
		}
		return &actionResult{OK: true, Context: &before.Context, Result: map[string]any{"components": comps}}
	}
	offPage := func(ref, id, pageUUID string) map[string]any {
		c := map[string]any{"componentType": "part", "designator": ref, "otherProperty": map[string]any{connectivity.ComponentIDProperty: id}}
		if pageUUID != "" {
			c["pageUuid"] = pageUUID
		}
		return c
	}
	other := offPage("X9", "cmp-X9", "page-other")
	if err := verifySchDesignatorGlobal(before, target, tagged(other), true); err != nil {
		t.Fatalf("page scope rejected a non-conflicting part on another page: %v", err)
	}
	if err := verifySchDesignatorGlobal(before, target, tagged(other), false); err == nil {
		t.Fatal("full-project scope accepted a part missing from the target")
	}
	first := target.Components[0]
	for name, c := range map[string]map[string]any{
		"duplicate id":  offPage("X8", first.ID, "page-other"),
		"occupied ref":  offPage(first.Ref, "cmp-other", "page-other"),
		"untagged part": offPage("X7", "cmp-X7", ""),
	} {
		if err := verifySchDesignatorGlobal(before, target, tagged(c), true); err == nil {
			t.Fatalf("page scope accepted %s", name)
		}
	}
}
