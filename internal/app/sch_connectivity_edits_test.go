package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func connectivityEditsFixture(t *testing.T) (*schCompositionPlan, map[string]any, []byte, []byte, *schConnectivityEdits) {
	t.Helper()
	p, env := preserveComposeFixture(t)
	pins := preserveFirstPart(env)["pins"].([]any)
	pins[0].(map[string]any)["net"], pins[0].(map[string]any)["noConnected"] = "", true
	pins[1].(map[string]any)["net"] = "OLD_GND"
	pins[2].(map[string]any)["noConnected"] = false
	before := composeApplyBytes(t, env)
	src := composeFixture(2)
	src.Modules[0].Placements[0].Rotation = 90
	src.Modules[1].Placements[0].Rotation = 270
	src.Modules[1].Placements[0].Mirror = true
	source, err := json.Marshal(src)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("testdata/schematic-connectivity-edits/pins.json")
	if err != nil {
		t.Fatal(err)
	}
	var edits schConnectivityEdits
	if err := json.Unmarshal(raw, &edits); err != nil {
		t.Fatal(err)
	}
	edits.BeforeSHA256, edits.SourceSHA256 = schInputSHA256(before), schInputSHA256(source)
	return p, env, before, source, &edits
}

func TestConnectivityEditsQueuePreservesIdentityAndVerifiesNCBeforeWiring(t *testing.T) {
	p, env, before, source, edits := connectivityEditsFixture(t)
	if pb, err := schCompositionPlaybook(p, before, true, true); err == nil || pb != nil {
		t.Fatal("default preservation accepted circuit changes")
	}
	raw, _ := json.Marshal(edits)
	edits, err := decodeSchConnectivityEdits(raw, before, source)
	if err != nil {
		t.Fatal(err)
	}
	pb, err := schCompositionPlaybookWithEdits(p, before, true, true, edits)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(pb)
	var loaded playbook
	if err = json.Unmarshal(raw, &loaded); err != nil {
		t.Fatal(err)
	}
	_, baseline := composeStep(t, &loaded, "verify-source-before-reset")
	if !reflect.DeepEqual(baseline.ExpectSchematic.SourceConnectivityEdits, edits) {
		t.Fatal("compiled audit did not survive queue JSON")
	}
	if _, interpolated := baseline.ExpectSchematic.substitutionValue()["sourceConnectivityEdits"]; interpolated {
		t.Fatal("audit evidence must not be interpreted as variable expressions")
	}
	if err := baseline.ExpectSchematic.check(env["result"], nil); err != nil {
		t.Fatal(err)
	}
	clearIndex, clear := composeStep(t, &loaded, "clear-nc-000")
	if clear.Action != "schematic.pin.set_no_connect" || clear.Payload["noConnected"] != false || !reflect.DeepEqual(clear.Payload["pins"], []any{"1"}) {
		t.Fatalf("missing exact NC removal: %+v", clear)
	}
	checkIndex, check := composeStep(t, &loaded, "verify-edited-nc-before-wiring")
	wireIndex, _ := composeStep(t, &loaded, "wire-000")
	if clearIndex >= checkIndex || checkIndex >= wireIndex {
		t.Fatal("NC removal/readback must precede every wire")
	}
	for _, step := range loaded.Steps {
		if step.Run == "sch clear" || step.Action == "schematic.component.place" || step.Action == "schematic.page.clear" {
			t.Fatal("audited edits recreate or broadly clear instances")
		}
	}
	// Simulate the post-move, post-NC, still-empty drawing readback. Every
	// identity and property is retained; only planned pose and NC have changed.
	result := env["result"].(map[string]any)
	var parts []any
	for _, item := range result["components"].([]any) {
		part := item.(map[string]any)
		if part["componentType"] == "sheet" {
			parts = append(parts, part)
			continue
		}
		if part["componentType"] != "part" {
			continue
		}
		want := check.ExpectSchematic.Parts[part["designator"].(string)]
		part["x"], part["y"] = *want.X, *want.Y
		for _, item := range part["pins"].([]any) {
			pin := item.(map[string]any)
			pin["noConnected"] = *want.Pins[pin["pinNumber"].(string)].NC
			pin["net"] = nil
		}
		parts = append(parts, part)
	}
	result["components"], result["wires"] = parts, []any{}
	composeFixturePageInventory(result)
	if err := check.ExpectSchematic.check(result, nil); err != nil {
		t.Fatal("correct pre-wire NC readback rejected:", err)
	}
	pin := parts[1].(map[string]any)["pins"].([]any)[0].(map[string]any)
	pin["noConnected"] = true
	if err := check.ExpectSchematic.check(result, nil); err == nil {
		t.Fatal("failed NC removal accepted before wiring")
	}
	if err := baseline.ExpectSchematic.check(result, nil); err == nil {
		t.Fatal("original source guard no longer detects circuit drift")
	}
}

func TestConnectivityEditsRejectsIncompleteStaleOrBroadenedDelta(t *testing.T) {
	cases := []struct {
		name string
		edit func(*schConnectivityEdits)
	}{
		{"missing", func(e *schConnectivityEdits) { e.Pins = e.Pins[1:] }},
		{"duplicate", func(e *schConnectivityEdits) { e.Pins = append(e.Pins, e.Pins[0]) }},
		{"old net", func(e *schConnectivityEdits) { *e.Pins[1].Before.Net = "WRONG" }},
		{"old NC", func(e *schConnectivityEdits) { *e.Pins[0].Before.NoConnected = false }},
		{"target", func(e *schConnectivityEdits) { *e.Pins[0].After.Net = "+5V" }},
		{"pin name", func(e *schConnectivityEdits) { *e.Pins[0].PinName = "CHANGED" }},
		{"ref", func(e *schConnectivityEdits) { e.Pins[0].Ref = "U9" }},
		{"unknown pin", func(e *schConnectivityEdits) { e.Pins[0].PinNumber = "99" }},
		{"unknown component", func(e *schConnectivityEdits) { e.Pins[0].ComponentID = "stable-U9" }},
		{"missing old net", func(e *schConnectivityEdits) { e.Pins[0].Before.Net = nil }},
		{"missing false NC", func(e *schConnectivityEdits) { e.Pins[0].After.NoConnected = nil }},
		{"redundant unchanged", func(e *schConnectivityEdits) { e.Pins[0].After = e.Pins[0].Before }},
		{"contradictory NC", func(e *schConnectivityEdits) { *e.Pins[0].After.NoConnected = true }},
		{"before hash", func(e *schConnectivityEdits) { e.BeforeSHA256 = strings.Repeat("0", 64) }},
		{"source hash", func(e *schConnectivityEdits) { e.SourceSHA256 = strings.Repeat("0", 64) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, _, before, source, edits := connectivityEditsFixture(t)
			tc.edit(edits)
			raw, _ := json.Marshal(edits)
			decoded, err := decodeSchConnectivityEdits(raw, before, source)
			if err == nil {
				_, err = schCompositionPlaybookWithEdits(p, before, true, true, decoded)
			}
			if err == nil {
				t.Fatal("invalid delta generated a queue")
			}
		})
	}
	for _, changed := range []string{"device", "pinName"} {
		t.Run(changed, func(t *testing.T) {
			p, _, before, _, edits := connectivityEditsFixture(t)
			if changed == "device" {
				p.Connectivity.Components[0].Device.UUID = strings.Repeat("9", 32)
			} else {
				p.Connectivity.Components[0].Pins[0].Name = "CHANGED"
			}
			if _, err := schCompositionPlaybookWithEdits(p, before, true, true, edits); err == nil {
				t.Fatal("audit broadened non-connectivity permission")
			}
		})
	}
}

func TestConnectivityEditsCommandBindsExactFilesAndKeepsInputs(t *testing.T) {
	_, _, before, source, edits := connectivityEditsFixture(t)
	dir := t.TempDir()
	paths := map[string]string{}
	delta, _ := json.Marshal(edits)
	for name, raw := range map[string][]byte{"source": source, "before": before, "edits": delta} {
		paths[name] = filepath.Join(dir, name+".json")
		if err := os.WriteFile(paths[name], raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	queue := filepath.Join(dir, "apply.json")
	var stdout, stderr bytes.Buffer
	cmd := newSchComposeCmd(&stdout, &stderr)
	cmd.SetArgs([]string{"--from", paths["source"], "--before", paths["before"], "--replace", "--preserve-instances", "--connectivity-edits", paths["edits"], "--playbook", queue})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(paths["before"])
	if !bytes.Equal(got, before) {
		t.Fatal("compiler rewrote source evidence")
	}
	if _, err := os.Stat(queue); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{string(delta) + " {}", strings.Replace(string(delta), `"schemaVersion":1`, `"schemaVersion":1,"force":true`, 1), "null"} {
		if _, err := decodeSchConnectivityEdits([]byte(bad), before, source); err == nil {
			t.Fatal("malformed/unknown audit JSON accepted")
		}
	}
	if _, err := decodeSchConnectivityEdits(delta, append(before, ' '), source); err == nil {
		t.Fatal("before byte drift accepted")
	}
	if _, err := decodeSchConnectivityEdits(delta, before, append(source, ' ')); err == nil {
		t.Fatal("source byte drift accepted")
	}
	cmd = newSchComposeCmd(&stdout, &stderr)
	cmd.SetArgs([]string{"--from", paths["source"], "--connectivity-edits", paths["edits"]})
	if err := cmd.Execute(); err == nil {
		t.Fatal("audit accepted without preservation")
	}
	cmd = newSchComposeCmd(&stdout, &stderr)
	cmd.SetArgs([]string{"--from", paths["source"], "--before", paths["before"], "--replace", "--preserve-instances", "--connectivity-edits", paths["edits"], "--playbook", paths["edits"]})
	if err := cmd.Execute(); err == nil {
		t.Fatal("compiler allowed overwriting audit input")
	}
}

func TestComposePaperlessPreservedDrawingResetRetainsPartsAndNC(t *testing.T) {
	for _, withEdits := range []bool{false, true} {
		p, env, _, _, edits := connectivityEditsFixture(t)
		if !withEdits {
			p, env = preserveComposeFixture(t)
			edits = nil
		}
		p.Paperless = true
		r := env["result"].(map[string]any)
		r["components"] = r["components"].([]any)[1:]
		before := composeApplyBytes(t, env)
		if edits != nil {
			edits.BeforeSHA256 = schInputSHA256(before)
		}
		pb, err := schCompositionPlaybookWithEdits(p, before, true, true, edits)
		if err != nil {
			t.Fatal(err)
		}
		_, reset := composeStep(t, pb, "reset-drawing-preserving-instances")
		for _, id := range reset.Payload["primitiveIds"].([]string) {
			if strings.HasPrefix(id, "live-") || strings.HasPrefix(id, "pin-") {
				t.Fatal("paperless reset deletes protected instances", id)
			}
		}
		_, residual := composeStep(t, pb, "verify-preserved-parts-after-clear")
		if residual.ExpectSchematic.PagePrimitives == nil || residual.ExpectSchematic.OwnedAttributes == nil {
			t.Fatal("paperless reset lost residual/attribute guards")
		}
		composeStep(t, pb, "paperless-schematic-gate")
		// Unsupported primitive classes stay unsupported without paper too.
		r["pagePrimitives"].(map[string]any)["arcs"] = []any{map[string]any{"primitiveId": "arc-1"}}
		bad, _ := json.Marshal(env)
		if edits != nil {
			edits.BeforeSHA256 = schInputSHA256(bad)
		}
		if _, err := schCompositionPlaybookWithEdits(p, bad, true, true, edits); err == nil {
			t.Fatal("paperless reset accepted unsupported arc")
		}
	}
}

// Editing an existing page: group conflicts are checked before the first write,
// registration frees stale groups, and nothing is saved before the final check.
func TestPreservedComposeChecksGroupsFirstAndSavesOnlyAtEnd(t *testing.T) {
	p, _, before, source, edits := connectivityEditsFixture(t)
	raw, _ := json.Marshal(edits)
	edits, err := decodeSchConnectivityEdits(raw, before, source)
	if err != nil {
		t.Fatal(err)
	}
	pb, err := schCompositionPlaybookWithEdits(p, before, true, true, edits)
	if err != nil {
		t.Fatal(err)
	}
	firstWrite, firstPreflight, lastPreflight, saves := -1, -1, -1, []string{}
	for i, st := range pb.Steps {
		write := st.Action != "" && st.Action != "schematic.components.list" || st.Run != "" && st.Run != "sch group create"
		if write && firstWrite < 0 {
			firstWrite = i
		}
		if strings.HasPrefix(st.ID, "preflight-lib-") {
			if st.Flags["check"] != true || st.Flags["replace-overlapping"] != true {
				t.Fatalf("preflight must be check-only with the same release policy: %+v", st.Flags)
			}
			if firstPreflight < 0 {
				firstPreflight = i
			}
			lastPreflight = i
		}
		if strings.HasPrefix(st.ID, "register-lib-") && (st.Flags["check"] != nil || st.Flags["replace-overlapping"] != true) {
			t.Fatalf("registration must release stale groups and really save: %+v", st.Flags)
		}
		if st.Action == "schematic.save" {
			saves = append(saves, st.ID)
		}
	}
	if firstPreflight < 0 || firstWrite < 0 || lastPreflight > firstWrite {
		t.Fatalf("group preflight (%d..%d) must precede the first write (%d)", firstPreflight, lastPreflight, firstWrite)
	}
	if len(saves) != 1 || saves[0] != "save-composition" {
		t.Fatalf("existing-page edit must save once at the end, got %v", saves)
	}
}
