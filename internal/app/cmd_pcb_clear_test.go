package app

import (
	"reflect"
	"testing"
)

func TestBuildPcbClearPayload_Defaults(t *testing.T) {
	got, err := buildPcbClearPayload("", false, false, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// No --only → no "only" key (daemon defaults to all scopes).
	if _, ok := got["only"]; ok {
		t.Errorf("expected no 'only' key when --only is empty, got %v", got["only"])
	}
	if got["dryRun"] != false || got["includeLocked"] != false {
		t.Errorf("expected dryRun/includeLocked false by default, got %+v", got)
	}
	if got["preserveOutline"] != true {
		t.Errorf("expected preserveOutline true by default (outline kept), got %v", got["preserveOutline"])
	}
}

func TestBuildPcbClearPayload_FlagsInvert(t *testing.T) {
	got, err := buildPcbClearPayload("", true, true, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// --no-preserve-outline must flip preserveOutline to false.
	if got["preserveOutline"] != false {
		t.Errorf("--no-preserve-outline should set preserveOutline=false, got %v", got["preserveOutline"])
	}
	if got["dryRun"] != true || got["includeLocked"] != true {
		t.Errorf("expected dryRun+includeLocked true, got %+v", got)
	}
}

func TestBuildPcbClearPayload_OnlyParsing(t *testing.T) {
	got, err := buildPcbClearPayload(" Routing , copper , Routing ", false, false, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	scopes, ok := got["only"].([]string)
	if !ok {
		t.Fatalf("expected only to be []string, got %T", got["only"])
	}
	// Lower-cased, trimmed, de-duplicated, order preserved.
	if want := []string{"routing", "copper"}; !reflect.DeepEqual(scopes, want) {
		t.Errorf("expected %v, got %v", want, scopes)
	}
}

func TestBuildPcbClearPayload_AllScopesValid(t *testing.T) {
	got, err := buildPcbClearPayload("components,routing,copper,regions,silk", false, false, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if scopes := got["only"].([]string); len(scopes) != 5 {
		t.Errorf("expected 5 scopes, got %v", scopes)
	}
}

func TestBuildPcbClearPayload_InvalidScope(t *testing.T) {
	_, err := buildPcbClearPayload("components,bogus", false, false, false)
	if err == nil {
		t.Fatal("expected an error for an unknown scope, got nil")
	}
}

func TestBuildPcbClearPayload_OnlyWhitespaceIsAll(t *testing.T) {
	// A comma-only / whitespace --only yields no scopes → omit the key (all).
	got, err := buildPcbClearPayload(" , ", false, false, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := got["only"]; ok {
		t.Errorf("expected no 'only' key for whitespace-only input, got %v", got["only"])
	}
}
