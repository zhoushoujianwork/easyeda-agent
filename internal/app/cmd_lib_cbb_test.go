package app

import (
	"bytes"
	"testing"
)

func TestLibCbbDispatch(t *testing.T) {
	build := func(cfg *appConfig, stdout, stderr *bytes.Buffer) commandExecutor {
		return newLibCmd(cfg, stdout, stderr)
	}
	for _, tc := range []struct {
		args   []string
		action string
		key    string
		want   any
	}{
		{[]string{"cbb", "search", "--query", "iso", "--library", "lib", "--limit", "5", "--page", "2"}, "library.cbb.search", "page", float64(2)},
		{[]string{"cbb", "get", "--uuid", "cbb", "--library", "lib"}, "library.cbb.get", "uuid", "cbb"},
	} {
		c := executeExamCommand(t, tc.args, build)
		c.mu.Lock()
		got := c.payload[tc.key]
		action := c.action
		lib := c.payload["libraryUuid"]
		c.mu.Unlock()
		if action != tc.action || got != tc.want || lib != "lib" {
			t.Fatalf("%v: action=%s payload=%#v", tc.args, action, c.payload)
		}
	}
}
func TestLibCbbInvalidBeforeDispatch(t *testing.T) {
	for _, args := range [][]string{{"cbb", "search"}, {"cbb", "search", "--query", " "}, {"cbb", "search", "--query", "x", "--limit", "0"}, {"cbb", "search", "--query", "x", "--page", "0"}, {"cbb", "get"}} {
		var out bytes.Buffer
		c := newLibCmd(&appConfig{}, &out, &out)
		c.SetArgs(args)
		if c.Execute() == nil {
			t.Fatalf("accepted invalid request: %v", args)
		}
	}
}
