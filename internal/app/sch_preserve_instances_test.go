package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/internal/connectivity"
)

func preserveComposeFixture(t *testing.T) (*schCompositionPlan, map[string]any) {
	p, env := composeApplyFixture(t, false)
	env["ok"] = true
	result := env["result"].(map[string]any)
	for _, v := range result["components"].([]any) {
		raw := v.(map[string]any)
		if raw["componentType"] != "part" {
			continue
		}
		ref := raw["designator"].(string)
		var canonical connectivity.Component
		for _, c := range p.Connectivity.Components {
			if c.Ref == ref {
				canonical = c
			}
		}
		raw["uniqueId"] = "gge-" + ref
		raw["name"] = "={Value}"
		raw["subPartName"] = "device.1"
		raw["addIntoBom"], raw["addIntoPcb"] = false, true
		// A modern connector answers the netlist question explicitly; without it the
		// designator plan must refuse (a null pin net can mean "not connected" or
		// "could not read"), so the fixture has to state it like the real one does.
		raw["pinsAvailable"], raw["netlistAvailable"] = true, true
		for _, key := range []string{"manufacturer", "manufacturerId", "supplier", "supplierId"} {
			raw[key] = ""
		}
		for _, key := range []string{"component", "symbol", "footprint"} {
			raw[key] = map[string]any{"uuid": "native-" + key + "-" + ref}
		}
		raw["otherProperty"] = map[string]any{connectivity.ComponentIDProperty: canonical.ID, "Value": "10kΩ", "a.b": "${literal}", "empty": "", "bool": false, "zero": 0.0}
		if canonical.Role != "" {
			raw["otherProperty"].(map[string]any)[connectivity.ComponentRoleProperty] = canonical.Role
		}
	}
	return p, env
}

func preserveFirstPart(env map[string]any) map[string]any {
	return env["result"].(map[string]any)["components"].([]any)[1].(map[string]any)
}

func TestPreserveInstancesQueueNeverRecreatesAndGuardsEveryPhase(t *testing.T) {
	p, env := preserveComposeFixture(t)
	pb, err := schCompositionPlaybook(p, composeApplyBytes(t, env), true, true)
	if err != nil {
		t.Fatal(err)
	}
	moves := 0
	for _, step := range pb.Steps {
		if step.Action == "schematic.component.place" || step.ID == "reset-target-preserving-sheet" {
			t.Fatal("preservation queue recreates instances")
		}
		if strings.HasPrefix(step.ID, "move-preserved-") {
			moves++
			if step.Payload["preserveInstance"] != true || len(step.Payload["patch"].(map[string]any)) != 4 {
				t.Fatal("non-geometric or unprotected move", step)
			}
		}
	}
	if moves != len(p.Connectivity.Components) {
		t.Fatal("missing preserved moves")
	}
	_, before := composeStep(t, pb, "verify-source-before-reset")
	if before.ExpectSchematic.SourceScene == nil {
		t.Fatal("missing exact source scene")
	}
	if err = before.ExpectSchematic.check(env["result"], nil); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"verify-preserved-parts-after-clear", "verify-physical-pins-before-wiring", "verify-all-pins-nets-nc", "verify-saved-instance-preservation"} {
		_, step := composeStep(t, pb, id)
		for ref, part := range step.ExpectSchematic.Parts {
			if part.Instance == nil || part.PrimitiveID == "" {
				t.Fatalf("%s lacks %s protection", id, ref)
			}
		}
	}
	_, clear := composeStep(t, pb, "reset-drawing-preserving-instances")
	if clear.Action != "schematic.page.clear" || clear.Payload["preserveParts"] != true || len(clear.Payload["preservePartIds"].([]string)) != len(p.Connectivity.Components) || clear.Payload["expectedPagePrimitives"] == "" || len(clear.LiteralPayloadKeys) != 1 || clear.LiteralPayloadKeys[0] != "expectedPagePrimitives" || clear.Assert["$.instancesPreserved"] != "==true" {
		t.Fatal("clear has no explicit protected set")
	}
	substituted, err := substPayloadVars(clear.Payload, clear.LiteralPayloadKeys, map[string]string{"literal": "would-corrupt-source"})
	if err != nil || !strings.Contains(substituted["expectedPagePrimitives"].(string), "${literal}") {
		t.Fatalf("native snapshot was treated as a variable template: %v", err)
	}
}

func TestPreserveInstancesCanWireFullyUnwiredPageOnly(t *testing.T) {
	p, env := preserveComposeFixture(t)
	result := env["result"].(map[string]any)
	parts := []any{}
	for _, item := range result["components"].([]any) {
		part := item.(map[string]any)
		if part["componentType"] == "netflag" {
			continue
		}
		if part["componentType"] == "part" {
			for _, item := range part["pins"].([]any) {
				pin := item.(map[string]any)
				pin["net"], pin["noConnected"] = "", false
			}
		}
		parts = append(parts, part)
	}
	result["components"], result["count"], result["wires"] = parts, len(parts), []any{}
	result["connectivitySummary"] = map[string]any{"scope": "activePage", "wires": 0, "buses": 0, "netflags": 0, "netports": 0, "netlabels": 0, "shortSymbols": 0}
	if !schComposeFullyUnwired(result) {
		t.Fatal("complete empty drawing was not recognized")
	}
	pb, err := schCompositionPlaybook(p, composeApplyBytes(t, env), true, true)
	if err != nil {
		t.Fatal(err)
	}
	composeStep(t, pb, "verify-fully-unwired-before-move")
	for _, step := range pb.Steps {
		if step.Action == "schematic.page.clear" || step.Run == "sch clear" {
			t.Fatal("fully unwired source must not be cleared")
		}
	}
	composeStep(t, pb, "move-preserved-000")
	composeStep(t, pb, "verify-all-pins-nets-nc")
	first := parts[1].(map[string]any)["pins"].([]any)[0].(map[string]any)
	first["net"] = "foreign-net"
	if schComposeFullyUnwired(result) {
		t.Fatal("partially wired page accepted as fresh unwired source")
	}
	if _, err := schCompositionPlaybook(p, composeApplyBytes(t, env), true, true); err == nil {
		t.Fatal("mixed/unknown source pin net accepted")
	}
}

func TestPreserveInstancesSourceGuardSurvivesPlaybookJSON(t *testing.T) {
	for _, withWire := range []bool{false, true} {
		t.Run(fmt.Sprint("with-wire-", withWire), func(t *testing.T) {
			p, env := preserveComposeFixture(t)
			result := env["result"].(map[string]any)
			result["wires"] = []any{}
			if withWire {
				result["wires"] = []any{map[string]any{"primitiveId": "wire-source", "net": "", "x0": 0.0, "y0": 0.0, "x1": 20.0, "y1": 0.0}}
			}
			result["connectivitySummary"].(map[string]any)["wires"] = float64(len(result["wires"].([]any)))
			pb, err := schCompositionPlaybook(p, composeApplyBytes(t, env), true, true)
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(pb)
			if err != nil {
				t.Fatal(err)
			}
			var loaded playbook
			if err := json.Unmarshal(data, &loaded); err != nil {
				t.Fatal(err)
			}
			_, step := composeStep(t, &loaded, "verify-source-before-reset")
			if err := step.ExpectSchematic.check(result, nil); err != nil {
				t.Fatal("unchanged source rejected after queue reload:", err)
			}
			result["wires"] = append(result["wires"].([]any), map[string]any{"primitiveId": "unexpected-wire", "x0": 5.0, "y0": 0.0, "x1": 25.0, "y1": 0.0})
			if err := step.ExpectSchematic.check(result, nil); err == nil || !strings.Contains(err.Error(), "source-drift") {
				t.Fatal("wire drift accepted after queue reload:", err)
			}
		})
	}
}

func TestPreserveInstancesRejectsIncompleteOrChangedSourceBeforeQueue(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*schCompositionPlan, map[string]any)
	}{
		{"no-unique", func(_ *schCompositionPlan, e map[string]any) { delete(preserveFirstPart(e), "uniqueId") }},
		{"no-properties", func(_ *schCompositionPlan, e map[string]any) { delete(preserveFirstPart(e), "otherProperty") }},
		{"null-properties", func(_ *schCompositionPlan, e map[string]any) { preserveFirstPart(e)["otherProperty"] = nil }},
		{"unsupported-property", func(_ *schCompositionPlan, e map[string]any) {
			preserveFirstPart(e)["otherProperty"].(map[string]any)["nested"] = map[string]any{"a": 1}
		}},
		{"wrong-library", func(_ *schCompositionPlan, e map[string]any) {
			preserveFirstPart(e)["device"].(map[string]any)["libraryUuid"] = "wrong"
		}},
		{"wrong-stable-id", func(_ *schCompositionPlan, e map[string]any) {
			preserveFirstPart(e)["otherProperty"].(map[string]any)[connectivity.ComponentIDProperty] = "foreign-id"
		}},
		{"wrong-nc", func(_ *schCompositionPlan, e map[string]any) {
			q := preserveFirstPart(e)["pins"].([]any)[0].(map[string]any)
			q["noConnected"] = !q["noConnected"].(bool)
		}},
		{"wrong-net", func(_ *schCompositionPlan, e map[string]any) {
			preserveFirstPart(e)["pins"].([]any)[0].(map[string]any)["net"] = "foreign-net"
		}},
		{"missing-net", func(_ *schCompositionPlan, e map[string]any) {
			delete(preserveFirstPart(e)["pins"].([]any)[0].(map[string]any), "net")
		}},
		{"missing-native-field", func(_ *schCompositionPlan, e map[string]any) { delete(preserveFirstPart(e), "supplier") }},
		{"wrong-page", func(_ *schCompositionPlan, e map[string]any) {
			e["context"].(map[string]any)["documentUuid"] = "other-page"
		}},
		{"duplicate-unique", func(_ *schCompositionPlan, e map[string]any) {
			for _, v := range e["result"].(map[string]any)["components"].([]any) {
				r := v.(map[string]any)
				if r["componentType"] == "part" {
					r["uniqueId"] = "duplicate"
				}
			}
		}},
		{"duplicate-binding", func(_ *schCompositionPlan, e map[string]any) {
			id := preserveFirstPart(e)["otherProperty"].(map[string]any)[connectivity.ComponentIDProperty]
			for _, v := range e["result"].(map[string]any)["components"].([]any) {
				r := v.(map[string]any)
				if r["componentType"] == "part" {
					r["otherProperty"].(map[string]any)[connectivity.ComponentIDProperty] = id
				}
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, e := preserveComposeFixture(t)
			tc.edit(p, e)
			if pb, err := schCompositionPlaybook(p, composeApplyBytes(t, e), true, true); err == nil || pb != nil {
				t.Fatal("unsafe queue compiled", err)
			}
		})
	}
}

func TestPreserveInstancesSourceDriftAndLiteralAttributeChecks(t *testing.T) {
	for _, key := range []string{"uniqueId", "name", "Value", "a.b", "bool", "zero", "extra"} {
		t.Run(key, func(t *testing.T) {
			p, e := preserveComposeFixture(t)
			pb, err := schCompositionPlaybook(p, composeApplyBytes(t, e), true, true)
			if err != nil {
				t.Fatal(err)
			}
			_, step := composeStep(t, pb, "verify-source-before-reset")
			if err = step.ExpectSchematic.check(e["result"], map[string]string{"literal": "do-not-substitute"}); err != nil {
				t.Fatal("literal changed", err)
			}
			raw := preserveFirstPart(e)
			if key == "uniqueId" || key == "name" {
				raw[key] = "changed"
			} else {
				raw["otherProperty"].(map[string]any)[key] = "changed"
			}
			if err = step.ExpectSchematic.check(e["result"], nil); err == nil || !strings.Contains(err.Error(), "source-drift") {
				t.Fatal("source drift accepted", err)
			}
			step.ExpectSchematic.SourceScene = nil
			if err = step.ExpectSchematic.check(e["result"], nil); err == nil || !strings.Contains(err.Error(), "instance-preservation") {
				t.Fatal("instance drift accepted", err)
			}
		})
	}
}

func TestPreserveInstancesDefaultDestructiveReplaceBlocked(t *testing.T) {
	p, e := preserveComposeFixture(t)
	if pb, err := schCompositionPlaybook(p, composeApplyBytes(t, e), true); err == nil || pb != nil || !strings.Contains(err.Error(), "--preserve-instances") {
		t.Fatal("bound relayout fell through destructive replace", err)
	}
	if _, err := schCompositionPlaybook(p, composeApplyBytes(t, e), false, true); err == nil {
		t.Fatal("preserve without replace accepted")
	}
}

func TestPreserveInstancesCompileFailureWritesNoOutputs(t *testing.T) {
	_, e := preserveComposeFixture(t)
	delete(preserveFirstPart(e), "uniqueId")
	dir := t.TempDir()
	source, before, out, queue := filepath.Join(dir, "source.json"), filepath.Join(dir, "before.json"), filepath.Join(dir, "out.json"), filepath.Join(dir, "queue.json")
	raw, _ := json.Marshal(composeFixture(2))
	if err := os.WriteFile(source, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(before, composeApplyBytes(t, e), 0600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	cmd := newSchComposeCmd(&stdout, &stderr)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--from", source, "--before", before, "--out", out, "--playbook", queue, "--replace", "--preserve-instances"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "uniqueId") {
		t.Fatal("invalid source did not fail at the instance precondition", err)
	}
	for _, path := range []string{out, queue} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("failed compile wrote output", path, err)
		}
	}
}

func TestPreserveInstanceLiteralsDoNotDisableLocatorVariables(t *testing.T) {
	p, e := preserveComposeFixture(t)
	pb, err := schCompositionPlaybook(p, composeApplyBytes(t, e), true, true)
	if err != nil {
		t.Fatal(err)
	}
	_, step := composeStep(t, pb, "verify-source-before-reset")
	step.ExpectSchematic.SourceScene = nil
	ref := preserveFirstPart(e)["designator"].(string)
	part := step.ExpectSchematic.Parts[ref]
	pid := part.PrimitiveID
	part.PrimitiveID = "${pid}"
	step.ExpectSchematic.Parts[ref] = part
	if missing := unresolvedVars(step, map[string]bool{}); len(missing) != 1 || missing[0] != "pid" {
		t.Fatal("locator/literal distinction lost", missing)
	}
	if err := step.ExpectSchematic.check(e["result"], map[string]string{"pid": pid}); err != nil {
		t.Fatal(err)
	}
	if err := step.ExpectSchematic.check(e["result"], map[string]string{"pid": "wrong"}); err == nil {
		t.Fatal("locator was not substituted")
	}
}
