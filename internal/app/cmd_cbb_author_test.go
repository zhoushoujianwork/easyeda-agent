package app

import (
	"bytes"
	"errors"
	"testing"
)

func TestCbbAuthorDispatch(t *testing.T) {
	build := func(cfg *appConfig, out, err *bytes.Buffer) commandExecutor { return newLibCmd(cfg, out, err) }
	id := []string{"--uuid", "c", "--library", "l", "--expected-name", "EA_AGENT__TEST"}
	for _, tc := range []struct {
		args   []string
		action string
	}{
		{[]string{"cbb", "create", "--name", "test"}, "library.cbb.create"},
		{append([]string{"cbb", "delete"}, id...), "library.cbb.delete"},
		{append([]string{"cbb", "symbol-open"}, id...), "library.cbb.symbol_open"},
		{append(append([]string{"cbb", "open"}, id...), "--from-project", "p", "--allow-project-switch"), "library.cbb.open"},
	} {
		cfg, c, cleanup := newCapturingDaemon(t)
		t.Cleanup(cleanup)
		var out, stderr bytes.Buffer
		cmd := build(cfg, &out, &stderr)
		cmd.SetArgs(tc.args)
		err := cmd.Execute()
		// The capturing daemon deliberately returns no verified flag. A CBB
		// write must reject that response while preserving its request/output.
		var unverified *unverifiedLibraryWriteError
		if !errors.As(err, &unverified) {
			t.Fatalf("unverified capture response accepted: %v", err)
		}
		c.mu.Lock()
		action := c.action
		c.mu.Unlock()
		if action != tc.action {
			t.Fatalf("%v dispatched %s", tc.args, action)
		}
	}
}
func TestCbbAuthorRejectsIncompleteInput(t *testing.T) {
	for _, args := range [][]string{{"cbb", "create"}, {"cbb", "delete", "--uuid", "c", "--library", "l", "--expected-name", "not-owned"}, {"cbb", "open", "--uuid", "c", "--library", "l", "--expected-name", "EA_AGENT__TEST", "--from-project", "p"}} {
		var out bytes.Buffer
		c := newLibCmd(&appConfig{}, &out, &out)
		c.SetArgs(args)
		if c.Execute() == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
func TestCbbWritesRequireExplicitVerifiedResult(t *testing.T) {
	for _, action := range []string{"library.cbb.create", "library.cbb.delete", "library.cbb.open", "library.cbb.symbol_open", "schematic.cbb.place"} {
		for _, result := range []map[string]any{{}, {"verified": false}, {"verified": true, "partial": true}} {
			if libraryWriteResultError(action, result, []byte("original response")) == nil {
				t.Fatalf("%s accepted %#v", action, result)
			}
		}
		if err := libraryWriteResultError(action, map[string]any{"verified": true, "partial": false}, nil); err != nil {
			t.Fatal(err)
		}
	}
}
func TestSchCbbPlaceRequiresExactTargetAndCoordinates(t *testing.T) {
	for _, args := range [][]string{{"cbb-place"}, {"cbb-place", "--uuid", "c", "--library", "l", "--expected-name", "EA_AGENT__TEST", "--symbol", "s", "--project-uuid", "p", "--page", "d", "--x", "0"}} {
		var out bytes.Buffer
		c := newSchCmd(&appConfig{}, &out, &out)
		c.SetArgs(args)
		if c.Execute() == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
