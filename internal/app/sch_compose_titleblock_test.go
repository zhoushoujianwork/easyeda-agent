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

// Exercise the public JSON decoder, selected-page adapter and protected writer
// together. No editor or daemon is involved in this source-to-queue contract.
func composeTitleBlockCLI(t *testing.T, fields string, selected bool) ([]byte, []byte, error) {
	t.Helper()
	src, page := composePreplacedFixture(t)
	plan, err := planSchCompositionWithPage(src, &page)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(src)
	var source map[string]json.RawMessage
	if err := json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	source["titleBlock"] = json.RawMessage(fields)
	dir := t.TempDir()
	from, before := filepath.Join(dir, "source.json"), filepath.Join(dir, "before.json")
	pagePath, out, queue := filepath.Join(dir, "page.json"), filepath.Join(dir, "plan.json"), filepath.Join(dir, "apply.json")
	for path, value := range map[string]any{from: source, before: composeEmptyPageBefore(plan), pagePath: page} {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := newSchComposeCmd(&bytes.Buffer{}, &bytes.Buffer{})
	args := []string{"--from", from, "--before", before, "--replace", "--out", out, "--playbook", queue}
	if selected {
		args = append(args, "--layout-page", pagePath)
	}
	cmd.SetArgs(args)
	err = cmd.Execute()
	if err != nil {
		for _, path := range []string{out, queue} {
			if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
				t.Fatalf("rejected source produced output %s: %v", path, statErr)
			}
		}
		return nil, nil, err
	}
	planRaw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	queueRaw, err := os.ReadFile(queue)
	if err != nil {
		t.Fatal(err)
	}
	return planRaw, queueRaw, nil
}

func TestComposeTitleBlockCLIVisibilitySurvivesPlanQueueAndHash(t *testing.T) {
	fields := `{"Name":{"value":"Power and USB","showTitle":false,"showValue":false},"Drawed":{"value":"Design team","showTitle":false,"showValue":true},"Description":"Input and regulator","Reviewed":{"value":"Review team"}}`
	for _, selected := range []bool{false, true} {
		planRaw, queueRaw, err := composeTitleBlockCLI(t, fields, selected)
		if err != nil {
			t.Fatalf("selected=%t: %v", selected, err)
		}
		var plan map[string]any
		var expected map[string]any
		json.Unmarshal(planRaw, &plan)
		json.Unmarshal([]byte(fields), &expected)
		if !reflect.DeepEqual(plan["titleBlock"], expected) {
			t.Fatalf("source visibility changed in plan: %#v", plan["titleBlock"])
		}
		var pb playbook
		if err := json.Unmarshal(queueRaw, &pb); err != nil {
			t.Fatal(err)
		}
		if !pb.RequireFullExecution {
			t.Fatal("title metadata disabled full-execution protection")
		}
		index, step := composeStep(t, &pb, "apply-page-titleblock")
		gate, _ := composeStep(t, &pb, "strict-schematic-gate")
		var patch map[string]any
		if err := json.Unmarshal([]byte(step.Flags["data"].(string)), &patch); err != nil {
			t.Fatal(err)
		}
		expected["Description"] = map[string]any{"value": "Input and regulator"}
		if !reflect.DeepEqual(patch, expected) || step.Run != "sch titleblock" || index >= gate {
			t.Fatalf("queue lost metadata or bypassed titleblock CLI: %+v", step)
		}
		_, repeated, err := composeTitleBlockCLI(t, fields, selected)
		if err != nil || sha256Hex(queueRaw) != sha256Hex(repeated) {
			t.Fatalf("same source must produce same protected queue hash: %v", err)
		}
		changed := strings.Replace(fields, `"showValue":false`, `"showValue":true`, 1)
		_, changedQueue, err := composeTitleBlockCLI(t, changed, selected)
		if err != nil || sha256Hex(queueRaw) == sha256Hex(changedQueue) {
			t.Fatalf("explicit visibility must affect the Apply input hash: %v", err)
		}
	}
}

func TestComposeTitleBlockCLIRejectsInvalidMetadata(t *testing.T) {
	for name, fields := range map[string]string{
		"null map":          `null`,
		"empty map":         `{}`,
		"null field":        `{"Name":null}`,
		"number field":      `{"Name":42}`,
		"array field":       `{"Name":[]}`,
		"empty update":      `{"Name":{}}`,
		"visibility only":   `{"Name":{"showValue":false}}`,
		"empty text":        `{"Name":{"value":"  "}}`,
		"nonstring text":    `{"Name":{"value":true}}`,
		"null text":         `{"Name":{"value":null}}`,
		"null visibility":   `{"Name":{"value":"x","showValue":null}}`,
		"string visibility": `{"Name":{"value":"x","showValue":"false"}}`,
		"number visibility": `{"Name":{"value":"x","showTitle":0}}`,
		"unknown option":    `{"Name":{"value":"x","visible":false}}`,
		"nonexact option":   `{"Name":{"Value":"x"}}`,
		"derived field":     `{"@Page Name":{"value":"x","showValue":false}}`,
		"structure field":   `{"Border":{"value":"x","showValue":false}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := composeTitleBlockCLI(t, fields, true)
			if err == nil || !strings.Contains(err.Error(), "titleBlock") {
				t.Fatalf("invalid titleBlock metadata accepted or unattributed: %v", err)
			}
		})
	}
}

func composeLegacyTitleFields(fields map[string]string) schCompositionTitleBlock {
	out := make(schCompositionTitleBlock, len(fields))
	for key, value := range fields {
		out[key] = schCompositionTitleBlockField{Value: value, legacyText: true}
	}
	return out
}

func TestComposeTitleBlockSourceCompilesBeforeStrictGate(t *testing.T) {
	fields := map[string]string{
		"Name": "Power and USB", "Drawed": "Design team", "Description": "Input and regulator",
	}
	src := composeFixture(1)
	src.TitleBlock = composeLegacyTitleFields(fields)
	plan, err := planSchComposition(src)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.TitleBlock, src.TitleBlock) {
		t.Fatalf("page title block was lost from composition source: %+v", plan.TitleBlock)
	}
	for _, matching := range []bool{false, true} {
		p, before := composeApplyFixture(t, matching)
		p.TitleBlock = plan.TitleBlock
		pb, err := schCompositionPlaybook(p, composeApplyBytes(t, before), !matching)
		if err != nil {
			t.Fatal(err)
		}
		writeIndex, write := composeStep(t, pb, "apply-page-titleblock")
		gateIndex, _ := composeStep(t, pb, "strict-schematic-gate")
		saveIndex, _ := composeStep(t, pb, "save-composition")
		if write.Run != "sch titleblock" || writeIndex >= gateIndex || gateIndex >= saveIndex {
			t.Fatalf("title block must use the guarded CLI before gate/save: %+v", write)
		}
		var patch map[string]map[string]string
		if err := json.Unmarshal([]byte(write.Flags["data"].(string)), &patch); err != nil {
			t.Fatal(err)
		}
		for key, value := range fields {
			if patch[key]["value"] != value {
				t.Fatalf("titleBlock.%s source value changed: %+v", key, patch)
			}
		}
		if len(patch) != len(fields) {
			t.Fatalf("unexpected title block fields: %+v", patch)
		}
	}
}

func TestComposeTitleBlockRejectsUnsafeOrEmptySource(t *testing.T) {
	for name, fields := range map[string]map[string]string{
		"empty object":  {},
		"empty value":   {"Name": "   "},
		"derived field": {"@Project Name": "project"},
		"structure":     {"Border": "1"},
		"paper size":    {"Width": "1170"},
		"nonexact key":  {" Name": "sheet"},
	} {
		t.Run(name, func(t *testing.T) {
			src := composeFixture(1)
			src.TitleBlock = composeLegacyTitleFields(fields)
			if _, err := planSchComposition(src); err == nil || !strings.Contains(err.Error(), "titleBlock") {
				t.Fatalf("unsafe titleBlock accepted: %v", err)
			}
		})
	}
}

func TestComposeLegacySourceDoesNotRewriteTitleBlock(t *testing.T) {
	p, before := composeApplyFixture(t, true)
	pb, err := schCompositionPlaybook(p, composeApplyBytes(t, before), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range pb.Steps {
		if step.ID == "apply-page-titleblock" || step.Run == "sch titleblock" {
			t.Fatal("legacy composition unexpectedly edits title block")
		}
	}
}

func TestComposeSelectedPageKeepsTitleBlockFromCompositionSource(t *testing.T) {
	src, page := composePreplacedFixture(t)
	src.TitleBlock = composeLegacyTitleFields(map[string]string{"Name": "Selected page"})
	plan, err := planSchCompositionWithPage(src, &page)
	if err != nil {
		t.Fatal(err)
	}
	if plan.TitleBlock["Name"].Value != "Selected page" {
		t.Fatalf("selected page lost per-page source metadata: %+v", plan.TitleBlock)
	}
	plan.TitleBlock["Name"] = schCompositionTitleBlockField{Value: "changed plan", legacyText: true}
	if src.TitleBlock["Name"].Value != "Selected page" {
		t.Fatal("planning reused the mutable source titleBlock map")
	}
}

func TestComposeTitleBlockVisibilityPointersDoNotAliasSource(t *testing.T) {
	src, page := composePreplacedFixture(t)
	if err := json.Unmarshal([]byte(`{"Name":{"value":"Selected page","showTitle":false,"showValue":false}}`), &src.TitleBlock); err != nil {
		t.Fatal(err)
	}
	plan, err := planSchCompositionWithPage(src, &page)
	if err != nil {
		t.Fatal(err)
	}
	*plan.TitleBlock["Name"].ShowTitle = true
	if *src.TitleBlock["Name"].ShowTitle || *plan.TitleBlock["Name"].ShowValue {
		t.Fatal("visibility pointers alias source or one another")
	}
}

func TestComposeTitleBlockMetadataOnMatchingCircuit(t *testing.T) {
	plan, before := composeApplyFixture(t, true)
	fields := `{"Name":{"value":"Page metadata","showTitle":false,"showValue":false},"Drawed":{"value":"Design team","showValue":true}}`
	if err := json.Unmarshal([]byte(fields), &plan.TitleBlock); err != nil {
		t.Fatal(err)
	}
	pb, err := schCompositionPlaybook(plan, composeApplyBytes(t, before), false)
	if err != nil {
		t.Fatal(err)
	}
	_, step := composeStep(t, pb, "apply-page-titleblock")
	var got, want map[string]any
	if err := json.Unmarshal([]byte(step.Flags["data"].(string)), &got); err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(fields), &want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("matching circuit lost explicit/omitted visibility: %#v", got)
	}
	for _, step := range pb.Steps {
		if step.Run == "sch clear" || step.Action == "schematic.component.place" || step.Action == "schematic.wire.create" {
			t.Fatalf("metadata recovery unexpectedly rebuilds existing circuit: %+v", step)
		}
	}
}
