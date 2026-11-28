package app

import (
	"io"
	"strings"
	"testing"
)

// The exact playbook the EdaDesk built-in model wrote (2026-11-27): it passed
// --dry-run and then failed at run time on every assert path. Preflight must
// now reject it before anything runs, naming the expected "$." form.
func TestPreflightRejectsAssertAndCapturePathsWithoutDollar(t *testing.T) {
	pb, _ := mustPlaybook(t, `{"version":1,"meta":{"name":"t","doc":"p"},"steps":[
	  {"id":"zero","action":"schematic.text.list","assert":{"count":"== 0"}},
	  {"id":"add","action":"schematic.text.create","payload":{"content":"X","x":1,"y":2},"capture":{"T":"primitiveId"}},
	  {"id":"check","action":"schematic.text.list","assert":{"result.count":"== 1","$.result.count":"==1"}},
	  {"id":"save","action":"schematic.save","assert":{"saved":"== true"}}]}`)
	joined := strings.Join(preflight(pb, nil), "\n")
	for _, want := range []string{
		`step zero: assert path "count" must start with "$"`,
		`step add: capture path "primitiveId" must start with "$"`,
		`step check: assert path "result.count" must start with "$"`,
		`step save: assert path "saved" must start with "$"`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in:\n%s", want, joined)
		}
	}
	// "$.result.count" is valid syntax; whether the field exists is only known at run time.
	if strings.Contains(joined, `"$.result.count" must`) {
		t.Errorf("valid path rejected:\n%s", joined)
	}
}

func TestPreflightRejectsUnsupportedPredicatesAndVarsInPaths(t *testing.T) {
	pb, _ := mustPlaybook(t, `{"version":1,"meta":{"name":"t"},"steps":[
	  {"id":"a","action":"schematic.text.list","assert":{"$.count":"eq 0","$.texts":"len=0"}},
	  {"id":"b","action":"schematic.text.list","assert":{"$.texts[${I}].x":"==1"}}]}`)
	joined := strings.Join(preflight(pb, map[string]string{"I": "0"}), "\n")
	for _, want := range []string{
		`assert "$.count": unsupported predicate "eq 0"`,
		`assert "$.texts": unsupported predicate "len=0"`,
		`assert path "$.texts[${I}].x": ${...} is not replaced in paths`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in:\n%s", want, joined)
		}
	}
}

func TestPreflightAcceptsValidGatesIncludingCapturedVarsInPredicates(t *testing.T) {
	pb, _ := mustPlaybook(t, `{"version":1,"meta":{"name":"t"},"steps":[
	  {"id":"a","action":"schematic.text.list","assert":{"$.texts":"len==0","$":"exists"}},
	  {"id":"b","action":"schematic.text.create","payload":{"content":"X","x":1,"y":2},"capture":{"T":"$.primitiveId"}},
	  {"id":"c","action":"schematic.text.list","assert":{"$.count":">=1","$.texts[0].primitiveId":"==${T}","$.component.otherProperty.EasyEDA Agent Role":"exists"}},
	  {"id":"d","action":"schematic.save","assert":{"$.saved":"true"}}]}`)
	if errs := preflight(pb, nil); len(errs) != 0 {
		t.Fatalf("valid gates rejected: %v", errs)
	}
}

func TestPreflightRejectsUnknownVarInPredicate(t *testing.T) {
	pb, _ := mustPlaybook(t, `{"version":1,"meta":{"name":"t"},"steps":[
	  {"id":"a","action":"schematic.text.list","assert":{"$.texts[0].primitiveId":"==${NEVER}"}}]}`)
	if joined := strings.Join(preflight(pb, nil), "\n"); !strings.Contains(joined, "${NEVER}") {
		t.Fatalf("unknown predicate var not reported:\n%s", joined)
	}
}

func TestAssertPredicateSubstitutesCapturedVars(t *testing.T) {
	r := &applyRunner{vars: map[string]string{"T": "abc"}, stderr: io.Discard}
	result := map[string]any{"texts": []any{map[string]any{"primitiveId": "abc"}}}
	if _, err := r.captureAndAssert(nil, map[string]string{"$.texts[0].primitiveId": "==${T}"}, result); err != nil {
		t.Fatalf("substituted predicate should pass: %v", err)
	}
	r.vars["T"] = "other"
	if _, err := r.captureAndAssert(nil, map[string]string{"$.texts[0].primitiveId": "==${T}"}, result); err == nil {
		t.Fatal("predicate compared against a different captured value must fail")
	}
}

func TestMissingPathNamesTheResultFields(t *testing.T) {
	r := &applyRunner{vars: map[string]string{}, stderr: io.Discard}
	result := map[string]any{"count": 0.0, "scope": "activePage", "texts": []any{}}
	_, err := r.captureAndAssert(nil, map[string]string{"$.result.count": "==0"}, result)
	if err == nil || !strings.Contains(err.Error(), "result fields: $.count, $.scope, $.texts") {
		t.Fatalf("missing-path error should list fields, got %v", err)
	}
	_, err = r.captureAndAssert(map[string]string{"T": "$.primitiveId"}, nil, result)
	if err == nil || !strings.Contains(err.Error(), "result fields: $.count") {
		t.Fatalf("missing capture error should list fields, got %v", err)
	}
}

func TestValidJSONPathMatchesResolver(t *testing.T) {
	for path, want := range map[string]bool{
		"$": true, "$.a": true, "$.a.b[0].c": true, "$[2]": true, "$.a b": true,
		"a": false, "$.": false, "$..a": false, "$.a[": false, "$.a[x]": false, "$.a[-1]": false, "result.a": false,
	} {
		if got := validJSONPath(path); got != want {
			t.Errorf("validJSONPath(%q)=%v want %v", path, got, want)
		}
	}
}
