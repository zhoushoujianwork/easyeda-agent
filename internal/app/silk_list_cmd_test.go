package app

import (
	"bytes"
	"strings"
	"testing"
)

func TestSilkListCLIDispatchesReadOnlyAction(t *testing.T) {
	cfg, calls, cleanup := newAutolayoutTestDaemon(t, func(_ int, _ autolayoutTestCall) string {
		return `{"ok":true,"result":{"count":1,"texts":[{"primitiveId":"s1","kind":"string","text":"REV A","layer":3}]}}`
	})
	defer cleanup()
	var out, stderr bytes.Buffer
	cmd := newPcbCmd(cfg, &out, &stderr)
	cmd.SetArgs([]string{"silk-list"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	got := calls.snapshot()
	if len(got) != 1 || got[0].Action != "pcb.silk.list" || len(got[0].Payload) != 0 {
		t.Fatalf("calls=%+v", got)
	}
	if !strings.Contains(out.String(), `"REV A"`) {
		t.Fatalf("stdout=%q", out.String())
	}
}

func TestSilkListCLIRejectsArguments(t *testing.T) {
	cfg, calls, cleanup := newAutolayoutTestDaemon(t, func(_ int, _ autolayoutTestCall) string { return `{"ok":true,"result":{}}` })
	defer cleanup()
	var out, stderr bytes.Buffer
	cmd := newPcbCmd(cfg, &out, &stderr)
	cmd.SetArgs([]string{"silk-list", "extra"})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected an error for a positional argument")
	}
	if len(calls.snapshot()) != 0 {
		t.Fatalf("dispatched: %+v", calls.snapshot())
	}
}
