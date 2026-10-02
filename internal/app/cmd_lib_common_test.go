package app

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestLibraryCommonListDispatchesSeparateCatalogueAction(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want map[string]any
	}{
		{"all", []string{"common", "list"}, map[string]any{}},
		{"model", []string{"common", "list", "--category", "安装器件", "--group", "螺丝", "--query", "M3"}, map[string]any{"category": "安装器件", "group": "螺丝", "query": "M3"}},
		{"ungrouped", []string{"common", "list", "--group", ""}, map[string]any{"group": ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			captured := executeExamCommand(t, tc.args, func(cfg *appConfig, stdout, stderr *bytes.Buffer) commandExecutor {
				return newLibCmd(cfg, stdout, stderr)
			})
			captured.mu.Lock()
			defer captured.mu.Unlock()
			if captured.action != "library.common.list" || !reflect.DeepEqual(captured.payload, tc.want) {
				t.Fatalf("action=%s payload=%#v, want catalogue action and %#v", captured.action, captured.payload, tc.want)
			}
		})
	}
}

func TestLibraryCommonListHelpDescribesIdentityBoundary(t *testing.T) {
	var out bytes.Buffer
	cmd := newLibCmd(nil, &out, &out)
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"common", "list", "--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"常用库", "--category", "--group", "--query", "lib device get", "Personal", "selected model"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("help missing %q", want)
		}
	}
}
