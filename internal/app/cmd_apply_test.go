package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustPlaybook(t *testing.T, src string) (*playbook, []byte) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "pb.json")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	pb, raw, err := loadPlaybook(p)
	if err != nil {
		t.Fatalf("loadPlaybook: %v", err)
	}
	return pb, raw
}

const samplePB = `{
  "version": 1,
  "meta": { "name": "t", "project": "ceshi", "doc": "PCB1" },
  "vars": { "LIB": "lib-uuid" },
  "defaults": { "timeoutSec": 30, "retry": 1 },
  "steps": [
    { "id": "place", "action": "schematic.component.place",
      "payload": { "libraryUuid": "${LIB}", "uuid": "dev", "x": 10, "y": 20 },
      "capture": { "U1": "$.primitiveId" } },
    { "id": "desig", "action": "schematic.component.modify",
      "payload": { "primitiveId": "${U1}", "patch": { "designator": "U1" } } },
    { "id": "gate", "run": "sch layout-lint", "flags": { "json": true },
      "assert": { "$.overlaps": "==0" } },
    { "id": "note", "notify": "done ${LIB}" }
  ]
}`

func TestLoadAndPreflightOK(t *testing.T) {
	pb, _ := mustPlaybook(t, samplePB)
	if errs := preflight(pb, pb.Vars); len(errs) != 0 {
		t.Fatalf("preflight errs: %v", errs)
	}
	if pb.Meta.Doc != "PCB1" || len(pb.Steps) != 4 {
		t.Fatalf("parsed wrong: %+v", pb.Meta)
	}
}

func TestPreflightCatchesProblems(t *testing.T) {
	pb, _ := mustPlaybook(t, `{
	  "version": 2, "meta": { "name": "" },
	  "steps": [
	    { "id": "a", "action": "no.such.action" },
	    { "id": "a", "run": "pcb drc", "action": "pcb.save" },
	    { "id": "b", "action": "schematic.save", "onFail": "explode" },
	    { "id": "c", "action": "schematic.component.modify",
	      "payload": { "primitiveId": "${NEVER}" } }
	  ]}`)
	errs := preflight(pb, map[string]string{})
	joined := strings.Join(errs, "\n")
	for _, want := range []string{
		"unsupported version", "meta.name", "duplicate id",
		"unknown action", "exactly one of", "invalid onFail", "${NEVER}",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("preflight missing %q in:\n%s", want, joined)
		}
	}
}

func TestPreflightAcceptsCapturedVarsInLaterSteps(t *testing.T) {
	pb, _ := mustPlaybook(t, samplePB)
	// ${U1} in step 2 is defined by step 1's capture — must NOT error
	if errs := preflight(pb, pb.Vars); len(errs) != 0 {
		t.Fatalf("capture-defined var flagged: %v", errs)
	}
}

func TestSubstVars(t *testing.T) {
	out, err := substVars(map[string]any{
		"a": "${X}-suffix", "b": []any{"${Y}", 5.0}, "c": true,
	}, map[string]string{"X": "xx", "Y": "yy"})
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["a"] != "xx-suffix" || m["b"].([]any)[0] != "yy" || m["c"] != true {
		t.Fatalf("subst wrong: %v", m)
	}
	if _, err := substVars("${MISSING}", map[string]string{}); err == nil {
		t.Fatal("missing var not flagged")
	}
}

func TestJSONPathLite(t *testing.T) {
	var root any
	_ = json.Unmarshal([]byte(`{"a":{"b":[{"c":42},{"c":"str"}]},"n":3,"ok":true}`), &root)
	cases := []struct {
		path string
		want any
		hit  bool
	}{
		{"$", nil, true},
		{"$.n", 3.0, true},
		{"$.a.b[0].c", 42.0, true},
		{"$.a.b[1].c", "str", true},
		{"$.a.b[9].c", nil, false},
		{"$.missing", nil, false},
		{"$.ok", true, true},
	}
	for _, c := range cases {
		got, hit := jsonPathLite(root, c.path)
		if hit != c.hit {
			t.Errorf("%s: hit=%v want %v", c.path, hit, c.hit)
			continue
		}
		if c.hit && c.path != "$" && got != c.want {
			t.Errorf("%s: got %v want %v", c.path, got, c.want)
		}
	}
}

func TestEvalPredicate(t *testing.T) {
	cases := []struct {
		val   any
		found bool
		pred  string
		want  bool
	}{
		{0.0, true, "==0", true},
		{3.0, true, ">=2", true},
		{3.0, true, "<2", false},
		{"ok", true, "==ok", true},
		{"ok", true, "!=bad", true},
		{true, true, "true", true},
		{false, true, "true", false},
		{nil, false, "exists", false},
		{1.0, true, "exists", true},
		{[]any{1, 2, 3}, true, "len==3", true},
		{[]any{}, true, "len>0", false},
		{95.0, true, ">=95", true},
	}
	for _, c := range cases {
		got, detail := evalPredicate(c.val, c.found, c.pred)
		if got != c.want {
			t.Errorf("pred %q on %v: got %v want %v (%s)", c.pred, c.val, got, c.want, detail)
		}
	}
}

func TestParseTrailingJSON(t *testing.T) {
	// pure JSON
	if v := parseTrailingJSON(`{"score": 100}`); v == nil {
		t.Fatal("pure json not parsed")
	}
	// mixed human text + JSON tail
	v := parseTrailingJSON("layout-lint: blah\nsome line\n{\"overlaps\": 0, \"score\": 96}")
	m, ok := v.(map[string]any)
	if !ok || m["score"] != 96.0 {
		t.Fatalf("trailing json wrong: %v", v)
	}
	// /action envelope unwraps to result
	v = parseTrailingJSON(`{"ok":true,"result":{"primitiveId":"abc"},"context":{}}`)
	m, ok = v.(map[string]any)
	if !ok || m["primitiveId"] != "abc" {
		t.Fatalf("envelope not unwrapped: %v", v)
	}
	// no json at all
	if v := parseTrailingJSON("plain text only"); v != nil {
		t.Fatalf("expected nil, got %v", v)
	}
}

func TestFormatCaptured(t *testing.T) {
	if formatCaptured("s") != "s" || formatCaptured(760.0) != "760" ||
		formatCaptured(1.5) != "1.5" || formatCaptured(true) != "true" {
		t.Fatal("formatCaptured wrong")
	}
}

func TestRunClassification(t *testing.T) {
	if !runIsReadOnly("pcb layout-lint") || !runIsReadOnly("doc switch") {
		t.Fatal("read-only misclassified")
	}
	if runIsReadOnly("pcb auto-place") || runIsReadOnly("pcb pour-fit") {
		t.Fatal("mutating misclassified as read-only")
	}
	if !runNeedsConfirm("pcb rip-up") || !runNeedsConfirm("sch clear") ||
		!runNeedsConfirm("pcb pour-delete") || runNeedsConfirm("pcb drc") {
		t.Fatal("confirm classification wrong")
	}
}

func TestJournalResume(t *testing.T) {
	pb, raw := mustPlaybook(t, samplePB)
	dir := t.TempDir()
	jp := filepath.Join(dir, "j.jsonl")
	sha := sha256Hex(raw)
	lines := []string{
		`{"playbookSha256":"` + sha + `","name":"t","startedAt":"x"}`,
		`{"idx":1,"id":"place","status":"ok","ms":10,"captured":{"U1":"pid-1"}}`,
		`{"idx":2,"id":"desig","status":"fail","ms":5,"error":"boom"}`,
	}
	if err := os.WriteFile(jp, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &applyRunner{pb: pb, sha: sha, vars: map[string]string{}, journalPath: jp}
	if err := r.loadJournal(); err != nil {
		t.Fatalf("loadJournal: %v", err)
	}
	if !r.doneOK[0] || r.doneOK[1] {
		t.Fatalf("doneOK wrong: %v", r.doneOK)
	}
	if r.vars["U1"] != "pid-1" {
		t.Fatalf("captured var not restored: %v", r.vars)
	}
	// changed playbook content → refuse resume
	r2 := &applyRunner{pb: pb, sha: "different", vars: map[string]string{}, journalPath: jp}
	if err := r2.loadJournal(); err == nil {
		t.Fatal("sha mismatch not refused")
	}
}

func TestResolveRange(t *testing.T) {
	pb, _ := mustPlaybook(t, samplePB)
	r := &applyRunner{pb: pb}
	if err := r.resolveRange("desig", "note"); err != nil {
		t.Fatal(err)
	}
	if r.fromIdx != 1 || r.toIdx != 3 {
		t.Fatalf("range wrong: %d..%d", r.fromIdx, r.toIdx)
	}
	if err := r.resolveRange("3", ""); err != nil || r.fromIdx != 2 {
		t.Fatalf("index ref wrong: %v %d", err, r.fromIdx)
	}
	if err := r.resolveRange("nope", ""); err == nil {
		t.Fatal("unknown ref not flagged")
	}
	if err := r.resolveRange("note", "place"); err == nil {
		t.Fatal("inverted range not flagged")
	}
}

func TestPolicyPrecedence(t *testing.T) {
	pb, _ := mustPlaybook(t, samplePB) // defaults: timeout 30, retry 1
	r := &applyRunner{pb: pb}
	// step without overrides → defaults
	to, retry, cont := r.policy(&pb.Steps[0])
	if to.Seconds() != 30 || retry != 1 || cont {
		t.Fatalf("defaults not applied: %v %d %v", to, retry, cont)
	}
	// step-level override wins
	five := 5
	tr := 0
	s := playbookStep{stepPolicy: stepPolicy{TimeoutSec: &five, Retry: &tr}}
	to, retry, _ = r.policy(&s)
	if to.Seconds() != 5 || retry != 0 {
		t.Fatalf("step override lost: %v %d", to, retry)
	}
}
