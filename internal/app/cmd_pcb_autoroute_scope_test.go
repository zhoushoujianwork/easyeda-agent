package app

import (
	"bytes"
	"strings"
	"testing"
)

// Live 2026-11-26 (Web 4.1.105): `pcb autoroute --net OSC_IN,OSC_OUT,NRST`
// routed every net. The connector now scopes through ignoreNets and reports
// outOfScopeNets; the CLI must fail when copper still lands outside the scope.
func TestFinishAutorouteJobFailsOutsideNetScope(t *testing.T) {
	job := func(out []any) map[string]any {
		return map[string]any{
			"jobId": "autoroute-1", "state": "completed", "tracked": true,
			"created":        map[string]any{"tracks": 3.0, "arcs": 0.0, "vias": 0.0},
			"createdByNet":   map[string]any{"NRST": 2.0, "GND": 1.0},
			"outOfScopeNets": out,
		}
	}
	var stdout, stderr bytes.Buffer
	err := finishAutorouteJob(job([]any{"GND"}), "", &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "outside the requested --net scope") || !strings.Contains(err.Error(), "GND") {
		t.Fatalf("copper on GND outside --net NRST must fail, got %v", err)
	}
	if err := finishAutorouteJob(job([]any{}), "", &stdout, &stderr); err != nil {
		t.Fatalf("an in-scope run must pass, got %v", err)
	}
	unscoped := job(nil)
	delete(unscoped, "outOfScopeNets")
	if err := finishAutorouteJob(unscoped, "", &stdout, &stderr); err != nil {
		t.Fatalf("a run without --net has no scope to break, got %v", err)
	}
}
