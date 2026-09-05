package app

import (
	"encoding/json"
	"github.com/zhoushoujianwork/easyeda-agent/internal/connectivity"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func planFixtures() (connectivity.Document, connectivity.Document) {
	a := connectivity.Document{SchemaVersion: "1.4", ProjectID: "p", DocumentID: "d", Components: []connectivity.Component{{ID: "c", Ref: "U1", Pins: []connectivity.Pin{{Number: "1"}, {Number: "2"}}}}, Nets: []connectivity.Net{{ID: "n", Name: "GND"}}}
	b := a
	b.Connections = []connectivity.Connection{{ComponentID: "c", PinNumber: "1", NetID: "n", Kind: "ground"}}
	return a, b
}
func TestPlanIsExecutablePlaybook(t *testing.T) {
	a, b := planFixtures()
	p, e := buildSchPlan(a, b)
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Steps) != 5 || p.Steps[0].ExpectedConnectivity == nil || p.Steps[1].Run != "sch autoconnect" || p.Steps[2].ExpectedConnectivity == nil {
		t.Fatalf("wrong queue: %+v", p.Steps)
	}
	if len(p.Steps[0].ExpectedConnectivity.Connections) != 0 {
		t.Fatal("baseline mutated")
	}
	bytes, e := json.Marshal(p)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "plan.json")
	os.WriteFile(path, bytes, 0600)
	loaded, _, e := loadPlaybook(path)
	if e != nil {
		t.Fatal(e)
	}
	if errs := preflight(loaded, nil); len(errs) > 0 {
		t.Fatal(errs)
	}
}
func TestPlanRejectsUnsupportedChanges(t *testing.T) {
	a, b := planFixtures()
	b.Connections[0].Kind = "wire"
	if _, e := buildSchPlan(a, b); e == nil {
		t.Fatal("wire accepted")
	}
	a, b = planFixtures()
	if _, e := buildSchPlan(b, a); e == nil {
		t.Fatal("disconnect accepted")
	}
	a, b = planFixtures()
	b.DocumentID = "other"
	if _, e := buildSchPlan(a, b); e == nil {
		t.Fatal("wrong doc accepted")
	}
}
func TestObservedCheckpointRejectsNoOpAndCollateral(t *testing.T) {
	_, b := planFixtures()
	parse := func(s string) map[string]any {
		var v map[string]any
		if e := json.Unmarshal([]byte(s), &v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	for _, s := range []string{
		`{"components":[{"componentType":"part","designator":"U1","pins":[{"number":"1","net":null},{"number":"2","net":null}]}]}`,
		`{"components":[{"componentType":"part","designator":"U1","pins":[{"number":"1","net":"GND"},{"number":"2","net":"GND"}]}]}`,
	} {
		if compareObserved(b, parse(s)) == nil {
			t.Fatal("incorrect readback accepted")
		}
	}
	good := parse(`{"components":[{"componentType":"part","designator":"U1","pins":[{"number":"1","net":"GND"},{"number":"2","net":null}]}]}`)
	if e := compareObserved(b, good); e != nil {
		t.Fatal(e)
	}
}

func TestGuardedQueueStopsBeforeNextWrite(t *testing.T) {
	_, expected := planFixtures()
	cfg, daemon, closeServer := newAutolayoutTestDaemon(t, func(_ int, c autolayoutTestCall) string {
		result := `{}`
		switch c.Action {
		case "document.current":
			result = `{"uuid":"d"}`
		case "schematic.pages.list":
			result = `{"pages":[{"uuid":"d","name":"d"}]}`
		case "pcb.documents.list":
			result = `{"pcbs":[]}`
		case "schematic.read":
			result = `{"components":[{"componentType":"part","designator":"U1","pins":[{"number":"1","net":null},{"number":"2","net":null}]}]}`
		}
		return `{"ok":true,"context":{"projectUuid":"p","documentUuid":"d","documentType":"schematic"},"result":` + result + `}`
	})
	defer closeServer()
	zero := 0
	pb := &playbook{Version: 1, Meta: playbookMeta{Name: "stop-test"}, Defaults: stepPolicy{Retry: &zero}, Steps: []playbookStep{{Action: "schematic.read", ExpectedConnectivity: &expected}, {Action: "schematic.save"}}}
	r := &applyRunner{cfg: cfg, pb: pb, stdout: io.Discard, stderr: io.Discard, window: "w1", journalPath: filepath.Join(t.TempDir(), "journal.jsonl"), toIdx: 1, vars: map[string]string{}}
	if e := r.execute(); e == nil {
		t.Fatal("stale baseline accepted")
	}
	read := false
	for _, c := range daemon.snapshot() {
		if c.Action == "schematic.read" {
			read = true
		}
		if c.Action == "schematic.save" {
			t.Fatal("write occurred after failed checkpoint")
		}
	}
	if !read {
		t.Fatal("test failed before reaching checkpoint")
	}
}
