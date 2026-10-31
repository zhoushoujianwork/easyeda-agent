package app

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
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
		{append(append([]string{"cbb", "symbol-open"}, id...), "--symbol", "s"), "library.cbb.symbol_open"},
		{append(append([]string{"cbb", "symbol-open"}, id...), "--symbol", "local", "--target-project", "p", "--imported-schematic", "schema"), "library.cbb.symbol_open"},
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
	for _, action := range []string{"library.cbb.create", "library.cbb.delete", "library.cbb.open", "library.cbb.symbol_open", "library.cbb.symbol_build", "library.cbb.symbol_rectangle_delete", "schematic.cbb.place", "schematic.cbb.project_place"} {
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

func TestSchCbbProjectVariantRequiresExplicitVariantUnlessPreview(t *testing.T) {
	base := []string{"cbb-project-place", "--project-uuid", "p", "--page", "d", "--schematic", "schema", "--expected-name", "EA_AGENT__TEST", "--x", "0", "--y", "0"}
	var out bytes.Buffer
	c := newSchCmd(&appConfig{}, &out, &out)
	c.SetArgs(base)
	if c.Execute() == nil {
		t.Fatal("accepted execution without explicit project symbol")
	}
	for _, flags := range [][]string{{"--dry-run"}, {"--symbol", "s"}} {
		cfg, captured, cleanup := newCapturingDaemon(t)
		c := newSchCmd(cfg, &out, &out)
		c.SetArgs(append(append([]string{}, base...), flags...))
		var unverified *unverifiedLibraryWriteError
		if err := c.Execute(); !errors.As(err, &unverified) {
			cleanup()
			t.Fatalf("accepted unverified response: %v", err)
		}
		captured.mu.Lock()
		matched := captured.action == "schematic.cbb.project_place" && captured.payload["schematicUuid"] == "schema" && captured.payload["viaSymbolApi"] == true && captured.timeoutMs == 35000
		captured.mu.Unlock()
		cleanup()
		if !matched {
			t.Fatal("wrong project CBB request")
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

func TestSchCbbSourceProbeIsExplicitAndHasBoundedBudget(t *testing.T) {
	cfg, captured, cleanup := newCapturingDaemon(t)
	defer cleanup()
	var out, stderr bytes.Buffer
	c := newSchCmd(cfg, &out, &stderr)
	c.SetArgs([]string{"cbb-place", "--uuid", "c", "--library", "l", "--expected-name", "EA_AGENT__TEST", "--symbol", "s", "--project-uuid", "p", "--page", "d", "--x", "0", "--y", "0", "--allow-source-project-probe"})
	var unverified *unverifiedLibraryWriteError
	if err := c.Execute(); !errors.As(err, &unverified) {
		t.Fatalf("unverified fixture response was accepted: %v", err)
	}
	captured.mu.Lock()
	defer captured.mu.Unlock()
	if captured.action != "schematic.cbb.place" || captured.payload["allowSourceProjectProbe"] != true || captured.timeoutMs != 60000 {
		t.Fatalf("request action=%s payload=%#v timeout=%d", captured.action, captured.payload, captured.timeoutMs)
	}
}

func TestCbbRectangleDeleteDispatch(t *testing.T) {
	spec := filepath.Join(t.TempDir(), "rectangle.json")
	if err := os.WriteFile(spec, []byte(`{"id":"r","x":0,"y":0,"width":140,"height":140,"rotation":0}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, captured, cleanup := newCapturingDaemon(t)
	defer cleanup()
	var out bytes.Buffer
	cmd := newLibCmd(cfg, &out, &out)
	cmd.SetArgs([]string{"cbb", "symbol-rectangle-delete", "--uuid", "c", "--library", "l", "--expected-name", "EA_AGENT__TEST", "--symbol", "s", "--spec", spec, "--dry-run"})
	var unverified *unverifiedLibraryWriteError
	if err := cmd.Execute(); !errors.As(err, &unverified) {
		t.Fatalf("accepted unverified rectangle response: %v", err)
	}
	captured.mu.Lock()
	defer captured.mu.Unlock()
	if captured.action != "library.cbb.symbol_rectangle_delete" || captured.payload["dryRun"] != true || captured.payload["symbolUuid"] != "s" {
		t.Fatalf("wrong request: %s %#v", captured.action, captured.payload)
	}
}
