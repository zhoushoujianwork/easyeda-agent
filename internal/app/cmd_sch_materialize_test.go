package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/internal/connectivity"
)

func materializeBindingFixture() connectivity.Document {
	d := composeFixture(3).Connectivity
	for i, ref := range []string{"U03", "u4", "J007"} {
		c := &d.Components[i]
		c.Ref = ref
		c.PageID, c.PageName = "page-1", "Power"
		c.Placement = &connectivity.Placement{X: float64(i*100 + 50), Y: 100, Rotation: float64(i * 90), Mirror: i == 2}
	}
	d.Components[0].Role = "U_RF"
	d.Components[2].Role = "J_PROG_RF"
	return d
}

// Exercise the same assertion evaluator used during Apply, including the
// space-containing official property keys. A successful SDK acknowledgement
// without the exact canonical identity must not advance the queue.
func assertComponentBindingReadback(t *testing.T, step *playbookStep, c connectivity.Component) {
	t.Helper()
	result := func() map[string]any {
		properties := map[string]any{connectivity.ComponentIDProperty: c.ID}
		if c.Role != "" {
			properties[connectivity.ComponentRoleProperty] = c.Role
		}
		return map[string]any{"component": map[string]any{"designator": c.Ref, "otherProperty": properties}}
	}
	r := &applyRunner{}
	if _, err := r.captureAndAssert(nil, step.Assert, result()); err != nil {
		t.Fatalf("correct canonical readback was rejected: %v", err)
	}
	for _, broken := range []string{"missing-id", "wrong-id", "wrong-ref", "wrong-role"} {
		if broken == "wrong-role" && c.Role == "" {
			continue
		}
		got := result()
		component := got["component"].(map[string]any)
		properties := component["otherProperty"].(map[string]any)
		switch broken {
		case "missing-id":
			delete(properties, connectivity.ComponentIDProperty)
		case "wrong-id":
			properties[connectivity.ComponentIDProperty] = "rebuilt-from-" + c.Ref
		case "wrong-ref":
			component["designator"] = "U999"
		case "wrong-role":
			properties[connectivity.ComponentRoleProperty] = c.Ref
		}
		if _, err := r.captureAndAssert(nil, step.Assert, got); err == nil {
			t.Fatalf("%s binding readback was accepted for %s", broken, c.Ref)
		}
	}
}

func TestSchMaterializePreservesCanonicalPlacementBindings(t *testing.T) {
	d := materializeBindingFixture()
	dir := t.TempDir()
	input := filepath.Join(dir, "canonical.json")
	original, _ := json.Marshal(d)
	if err := os.WriteFile(input, original, 0600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	cmd := newSchMaterializeCmd(&stdout, &stderr)
	cmd.SetArgs([]string{input})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var pb playbook
	if err := json.Unmarshal(stdout.Bytes(), &pb); err != nil {
		t.Fatal(err)
	}
	if errs := preflight(&pb, nil); len(errs) > 0 {
		t.Fatalf("generated queue failed public Apply preflight: %v", errs)
	}
	var refs []string
	for i := range pb.Steps {
		step := &pb.Steps[i]
		if step.Action == "schematic.power.connect_pin" {
			t.Fatal("placement-only queue must not emit unplanned wires")
		}
		if step.Action != "schematic.component.place" {
			continue
		}
		c := d.Components[len(refs)]
		refs = append(refs, step.Payload["designator"].(string))
		if step.Capture[c.Ref] != "$.primitiveId" || step.Payload["x"] != c.Placement.X || step.Payload["y"] != c.Placement.Y {
			t.Fatalf("placement lost authored identity or coordinates: %+v", step)
		}
		rotation, _ := toFloat(step.Payload["rotation"])
		mirror, _ := step.Payload["mirror"].(bool)
		if rotation != 0 || mirror {
			t.Fatal("create must use neutral pose; stored pose belongs in modify")
		}
		if step.Payload["otherProperty"] != nil || step.Payload["customAttributes"] != nil {
			t.Fatal("custom properties must be written through modify, not place")
		}
		if i+1 >= len(pb.Steps) {
			t.Fatal("fresh placement has no binding step")
		}
		binding := &pb.Steps[i+1]
		if binding.Action != "schematic.component.modify" || binding.Payload["primitiveId"] != "${"+c.Ref+"}" {
			t.Fatalf("binding did not use the fresh primitive capture: %+v", binding)
		}
		patch := binding.Payload["patch"].(map[string]any)
		if patch["rotation"] != c.Placement.Rotation || patch["mirror"] != c.Placement.Mirror {
			t.Fatal("modify did not restore the measured absolute pose")
		}
		properties := patch["otherProperty"].(map[string]any)
		if patch["designator"] != c.Ref || properties[connectivity.ComponentIDProperty] != c.ID {
			t.Fatalf("binding derived identity from visible reference: %+v", patch)
		}
		if c.Role == "" {
			if _, exists := properties[connectivity.ComponentRoleProperty]; exists {
				t.Fatal("binding invented an absent role")
			}
		} else if properties[connectivity.ComponentRoleProperty] != c.Role {
			t.Fatal("functional role was not preserved")
		}
		assertComponentBindingReadback(t, binding, c)
	}
	if !reflect.DeepEqual(refs, []string{"U03", "u4", "J007"}) {
		t.Fatalf("queue reordered/rewrote references: %v", refs)
	}
	after, err := os.ReadFile(input)
	if err != nil || !bytes.Equal(original, after) {
		t.Fatal("materialize rewrote the canonical source")
	}
}

func TestSchMaterializeRejectsUnsafeWiringWithoutOverwritingPlan(t *testing.T) {
	dir := t.TempDir()
	input, output := filepath.Join(dir, "input.json"), filepath.Join(dir, "plan.json")
	raw, _ := json.Marshal(materializeBindingFixture())
	if err := os.WriteFile(input, raw, 0600); err != nil {
		t.Fatal(err)
	}
	const existing = "reviewed plan"
	if err := os.WriteFile(output, []byte(existing), 0600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	cmd := newSchMaterializeCmd(&stdout, &stderr)
	cmd.SetArgs([]string{input, "--with-connectivity", "--out", output})
	if err := cmd.Execute(); err == nil {
		t.Fatal("unsafe wiring accepted")
	}
	after, err := os.ReadFile(output)
	if err != nil || string(after) != existing || stdout.Len() != 0 {
		t.Fatal("rejected wiring emitted or overwrote a queue")
	}
}

func TestSchMaterializeRejectsLegacyDesignatorsWithoutOutput(t *testing.T) {
	for _, legacy := range []string{"U_RF", "J_PROG_RF"} {
		t.Run(legacy, func(t *testing.T) {
			d := materializeBindingFixture()
			d.Components[1].Ref = legacy
			dir := t.TempDir()
			input, output := filepath.Join(dir, "source.json"), filepath.Join(dir, "apply.json")
			raw, _ := json.Marshal(d)
			if err := os.WriteFile(input, raw, 0600); err != nil {
				t.Fatal(err)
			}
			const existing = "existing reviewed queue\n"
			if err := os.WriteFile(output, []byte(existing), 0600); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{input}, {input, "--out", output}} {
				var stdout, stderr bytes.Buffer
				cmd := newSchMaterializeCmd(&stdout, &stderr)
				cmd.SetArgs(args)
				if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "nonstandard designator") || !strings.Contains(err.Error(), legacy) {
					t.Fatalf("legacy reference produced a placement queue: %v", err)
				}
				if stdout.Len() != 0 {
					t.Fatalf("failed validation emitted a partial queue: %s", stdout.String())
				}
			}
			got, err := os.ReadFile(output)
			if err != nil || string(got) != existing {
				t.Fatal("failed validation overwrote the previously reviewed Apply file")
			}
		})
	}
}

func TestIsDeviceLibraryUUID(t *testing.T) {
	if !isDeviceLibraryUUID("87bb635d0b2f489a9f60e7cd225beb3c") {
		t.Fatal("32-character hexadecimal device uuid should be accepted")
	}
	for _, got := range []string{
		"2116294927a134e2",                 // placed-instance uuid from sch list
		"87bb635d0b2f489a9f60e7cd225beb3",  // 31 chars
		"87bb635d0b2f489a9f60e7cd225beb3z", // non-hex
	} {
		if isDeviceLibraryUUID(got) {
			t.Errorf("%q should be rejected as a device-library uuid", got)
		}
	}
}

func TestOnSchematicGrid(t *testing.T) {
	for _, v := range []float64{0, 5, 145, 250, -10, 250.0000001} {
		if !onSchematicGrid(v) {
			t.Errorf("%v should be accepted on the 5-unit grid", v)
		}
	}
	for _, v := range []float64{2.5, 145.25, 582.57341743} {
		if onSchematicGrid(v) {
			t.Errorf("%v should be rejected as off-grid", v)
		}
	}
}
