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

// Parts placed one by one carry no binding; bind only adds the default
// cmp-<Ref> identity, never a new reference, pose or net.
func TestSchDesignatorsBindAddsOnlyMissingBindings(t *testing.T) {
	raw, _, _ := designatorFixture(t)
	raw = []byte(strings.Replace(string(raw), `"designator":"U_RF"`, `"designator":"U2"`, 1))
	raw = []byte(strings.Replace(string(raw), `"otherProperty":{},`, `"otherProperty":{"`+connectivity.ComponentIDProperty+`":"cmp-U1"},`, 1))
	dir := t.TempDir()
	before, target, out := filepath.Join(dir, "before.json"), filepath.Join(dir, "target.json"), filepath.Join(dir, "queue.json")
	if err := os.WriteFile(before, raw, 0600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	window := ""
	run := func() error {
		cmd := newSchDesignatorsCmd(&appConfig{}, &window, &buf, io.Discard)
		cmd.SetArgs([]string{"bind", "--before", before, "--target-out", target, "--out", out})
		return cmd.Execute()
	}
	if err := run(); err != nil {
		t.Fatal(err)
	}
	var summary struct {
		OK       bool     `json:"ok"`
		Bindings []string `json:"bindings"`
		Playbook string   `json:"playbook"`
	}
	if err := json.Unmarshal(buf.Bytes(), &summary); err != nil || !summary.OK || !reflect.DeepEqual(summary.Bindings, []string{"U2"}) || summary.Playbook != out {
		t.Fatalf("missing structured summary: %s (%v)", buf.String(), err)
	}
	buf.Reset()
	var pb playbook
	if err := schDesignatorsReadJSON(out, &pb); err != nil {
		t.Fatal(err)
	}
	if len(pb.Steps) != 4 || pb.Steps[0].Run != "sch designators verify" || pb.Steps[2].Run != "sch designators verify" || pb.Steps[3].Action != "schematic.save" {
		t.Fatalf("unexpected queue: %+v", pb.Steps)
	}
	patch := pb.Steps[1].Payload["patch"].(map[string]any)
	attrs := patch["customAttributes"].(map[string]any)
	if pb.Steps[1].Action != "schematic.component.modify" || pb.Steps[1].Payload["primitiveId"] != "p-rf" || patch["designator"] != "U2" || len(patch) != 2 || attrs[connectivity.ComponentIDProperty] != "cmp-U2" {
		t.Fatalf("unexpected bind step: %+v", pb.Steps[1])
	}
	// The guards verify against the exact target that was written.
	targetRaw, _ := os.ReadFile(target)
	if pb.Steps[0].Flags["target-sha"] != sha256Hex(targetRaw) || pb.Steps[0].Flags["before-sha"] != sha256Hex(raw) {
		t.Fatalf("guards not bound to files: %+v", pb.Steps[0].Flags)
	}
	var doc connectivity.Document
	if err := json.Unmarshal(targetRaw, &doc); err != nil || doc.ProjectID != "project" || doc.DocumentID != "page1" {
		t.Fatalf("target identity: %+v %v", doc, err)
	}
	// A fully bound page has nothing to do and says so.
	raw = []byte(strings.Replace(string(raw), `"otherProperty":{"Datasheet":"source"}`, `"otherProperty":{"Datasheet":"source","`+connectivity.ComponentIDProperty+`":"cmp-U2"}`, 1))
	if err := os.WriteFile(before, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(); err == nil || !strings.Contains(err.Error(), "nothing to bind") {
		t.Fatalf("expected nothing-to-bind refusal, got %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("unexpected stdout: %s", buf.String())
	}
}
