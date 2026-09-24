package app

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func executeExamCommand(t *testing.T, cmdArgs []string, build func(*appConfig, *bytes.Buffer, *bytes.Buffer) commandExecutor) *capturedRequest {
	t.Helper()
	cfg, captured, cleanup := newCapturingDaemon(t)
	t.Cleanup(cleanup)
	var stdout, stderr bytes.Buffer
	cmd := build(cfg, &stdout, &stderr)
	cmd.SetArgs(cmdArgs)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute %v: %v (stderr=%s)", cmdArgs, err, stderr.String())
	}
	return captured
}

// commandExecutor is the small Cobra surface shared by the command groups in
// this file. Keeping the helper interface local avoids coupling these payload
// tests to a concrete root command.
type commandExecutor interface {
	SetArgs([]string)
	Execute() error
}

func TestProjectCreateWiresExplicitFields(t *testing.T) {
	captured := executeExamCommand(t,
		[]string{"create", "--name", "AT32F415 demo", "--internal-name", "at32-demo", "--team", "team-1", "--folder", "folder-1", "--description", "exam", "--open"},
		func(cfg *appConfig, stdout, stderr *bytes.Buffer) commandExecutor {
			return newProjectCmd(cfg, stdout, stderr)
		})
	captured.mu.Lock()
	defer captured.mu.Unlock()
	if captured.action != "project.create" {
		t.Fatalf("action=%q, want project.create", captured.action)
	}
	want := map[string]any{
		"friendlyName": "AT32F415 demo", "projectName": "at32-demo", "teamUuid": "team-1",
		"folderUuid": "folder-1", "description": "exam", "open": true,
	}
	if !reflect.DeepEqual(captured.payload, want) {
		t.Fatalf("payload=%#v, want %#v", captured.payload, want)
	}
}

func TestProjectFindWiresExactIdentity(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want map[string]any
	}{
		{"team-root", []string{"find", "--name", "Exact Name", "--team", "team-1"}, map[string]any{"friendlyName": "Exact Name", "teamUuid": "team-1"}},
		{"unscoped", []string{"find", "--name", "Exact Name"}, map[string]any{"friendlyName": "Exact Name"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			captured := executeExamCommand(t, tc.args, func(cfg *appConfig, stdout, stderr *bytes.Buffer) commandExecutor {
				return newProjectCmd(cfg, stdout, stderr)
			})
			captured.mu.Lock()
			defer captured.mu.Unlock()
			if captured.action != "project.find" || !reflect.DeepEqual(captured.payload, tc.want) {
				t.Fatalf("action=%q payload=%#v, want %#v", captured.action, captured.payload, tc.want)
			}
			if captured.timeoutMs != 90000 {
				t.Fatalf("default lookup budget=%d, want 90000", captured.timeoutMs)
			}
		})
	}
}

func TestProjectFindTimeout(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  int
	}{{"20s", 20000}, {"90s", 90000}, {"10m", 600000}} {
		t.Run(tc.value, func(t *testing.T) {
			captured := executeExamCommand(t, []string{"find", "--name", "Exact", "--timeout", tc.value}, func(cfg *appConfig, stdout, stderr *bytes.Buffer) commandExecutor {
				return newProjectCmd(cfg, stdout, stderr)
			})
			captured.mu.Lock()
			defer captured.mu.Unlock()
			if captured.timeoutMs != tc.want {
				t.Fatalf("wire timeout=%d, want %d", captured.timeoutMs, tc.want)
			}
		})
	}
	for _, value := range []string{"0s", "-1s", "4s", "601s"} {
		t.Run("reject-"+value, func(t *testing.T) {
			var out bytes.Buffer
			cmd := newProjectCmd(nil, &out, &out)
			cmd.SetArgs([]string{"find", "--name", "Exact", "--timeout", value})
			if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "--timeout must be") {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestPcbNetClassCreateWiresMembers(t *testing.T) {
	captured := executeExamCommand(t,
		[]string{"net-class", "create", "--name", "PWR_Class", "--net", "+5V", "--net", "+3V3,GND"},
		func(cfg *appConfig, stdout, stderr *bytes.Buffer) commandExecutor {
			return newPcbCmd(cfg, stdout, stderr)
		})
	captured.mu.Lock()
	defer captured.mu.Unlock()
	if captured.action != "pcb.net_class.create" {
		t.Fatalf("action=%q, want pcb.net_class.create", captured.action)
	}
	if captured.payload["name"] != "PWR_Class" || !reflect.DeepEqual(captured.payload["nets"], []any{"+5V", "+3V3", "GND"}) {
		t.Fatalf("payload=%#v", captured.payload)
	}
}

func TestPcbDrcRulesSetFromJSONWiresOpaqueConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.json")
	if err := os.WriteFile(path, []byte(`{"ruleConfiguration":{"clearance":6},"netRules":[{"class":"PWR_Class","rule":"PWR"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	captured := executeExamCommand(t,
		[]string{"drc-rules-set", "--from", path, "--dry-run"},
		func(cfg *appConfig, stdout, stderr *bytes.Buffer) commandExecutor {
			return newPcbCmd(cfg, stdout, stderr)
		})
	captured.mu.Lock()
	defer captured.mu.Unlock()
	if captured.action != "pcb.drc.rules.set" {
		t.Fatalf("action=%q, want pcb.drc.rules.set", captured.action)
	}
	if captured.payload["dryRun"] != true {
		t.Fatalf("dryRun=%v, want true", captured.payload["dryRun"])
	}
	rules, ok := captured.payload["ruleConfiguration"].(map[string]any)
	if !ok || rules["clearance"] != float64(6) {
		t.Fatalf("ruleConfiguration=%#v", captured.payload["ruleConfiguration"])
	}
}

func TestPcbDrcRulesSetAcceptsCommandEnvelopeAndExportShapes(t *testing.T) {
	tests := []struct {
		name string
		json string
	}{
		{"action-envelope", `{"ok":true,"result":{"rules":{"name":"自定义配置","config":{"clearance":6}}}}`},
		{"top-level-wrapper", `{"name":"自定义配置","config":{"clearance":6}}`},
		{"bare-config", `{"clearance":6,"track":{"default":8}}`},
		{"legacy-rules", `{"rules":{"clearance":6},"netRules":[{"class":"PWR_Class","rule":"PWR"}]}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "rules.json")
			if err := os.WriteFile(path, []byte(tc.json), 0o600); err != nil {
				t.Fatal(err)
			}
			captured := executeExamCommand(t,
				[]string{"drc-rules-set", "--from", path, "--dry-run"},
				func(cfg *appConfig, stdout, stderr *bytes.Buffer) commandExecutor {
					return newPcbCmd(cfg, stdout, stderr)
				})
			captured.mu.Lock()
			defer captured.mu.Unlock()
			if captured.action != "pcb.drc.rules.set" || captured.payload["dryRun"] != true {
				t.Fatalf("action=%q payload=%#v", captured.action, captured.payload)
			}
			rules, ok := captured.payload["ruleConfiguration"].(map[string]any)
			if !ok {
				t.Fatalf("ruleConfiguration=%#v", captured.payload["ruleConfiguration"])
			}
			if config, wrapped := rules["config"].(map[string]any); wrapped {
				if config["clearance"] != float64(6) {
					t.Fatalf("wrapped config=%#v", config)
				}
			} else if rules["clearance"] != float64(6) {
				t.Fatalf("rules=%#v", rules)
			}
		})
	}
}

func TestPcbSilkFontFamilyWiresAddAndSet(t *testing.T) {
	for _, tc := range []struct {
		name, action string
		args         []string
	}{
		{"add", "pcb.silk.add", []string{"silk-add", "--text", "CAN", "--x", "10", "--y", "20", "--font-family", "Arial"}},
		{"set", "pcb.silk.set", []string{"silk-set", "--ids", "s1,s2", "--font-family", "Arial"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			captured := executeExamCommand(t, tc.args,
				func(cfg *appConfig, stdout, stderr *bytes.Buffer) commandExecutor {
					return newPcbCmd(cfg, stdout, stderr)
				})
			captured.mu.Lock()
			defer captured.mu.Unlock()
			if captured.action != tc.action || captured.payload["fontFamily"] != "Arial" {
				t.Fatalf("action=%q payload=%#v", captured.action, captured.payload)
			}
		})
	}
}

func TestFootprintRegionWiresGeometryAndRule(t *testing.T) {
	captured := executeExamCommand(t,
		[]string{"footprint", "region", "--uuid", "fp-copy", "--library", "lib-1", "--points", "[[0,0],[1200,0],[1200,900],[0,900]]", "--rule", "no-components", "--name", "LCD_BODY"},
		func(cfg *appConfig, stdout, stderr *bytes.Buffer) commandExecutor {
			return newLibCmd(cfg, stdout, stderr)
		})
	captured.mu.Lock()
	defer captured.mu.Unlock()
	if captured.action != "library.footprint.region_create" {
		t.Fatalf("action=%q, want library.footprint.region_create", captured.action)
	}
	if captured.payload["uuid"] != "fp-copy" || captured.payload["libraryUuid"] != "lib-1" || captured.payload["ruleType"] != "no-components" || captured.payload["name"] != "LCD_BODY" || captured.payload["locked"] != true {
		t.Fatalf("payload=%#v", captured.payload)
	}
	points, ok := captured.payload["points"].([]any)
	if !ok || len(points) != 4 {
		t.Fatalf("points=%#v", captured.payload["points"])
	}
}
