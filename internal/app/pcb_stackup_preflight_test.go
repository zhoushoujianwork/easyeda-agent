package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

// The fake board has enough geometry to complete either power plan. This makes
// a missing stackup guard observable as a successful plan or a copper write,
// rather than an unrelated missing-outline error.
func stackupTestBoardResponse(c autolayoutTestCall, layersResponse string) string {
	switch c.Action {
	case "pcb.layers.list":
		return layersResponse
	case "pcb.components.list":
		return `{"ok":true,"result":{"components":[{"designator":"U1","pads":[{"net":"GND","padNumber":"1","x":100,"y":100,"width":20,"height":20,"layer":1},{"net":"GND","padNumber":"2","x":200,"y":100,"width":20,"height":20,"layer":1}]}]}}`
	case "pcb.outline.get":
		return `{"ok":true,"result":{"bbox":{"minX":0,"minY":0,"maxX":1000,"maxY":1000}}}`
	case "pcb.pour.create":
		return `{"ok":true,"result":{"primitiveId":"fixture-pour","verified":true,"poured":true}}`
	default:
		return `{"ok":true,"result":{}}`
	}
}

func assertNoStackupTestMutations(t *testing.T, calls []autolayoutTestCall) {
	t.Helper()
	mutates := map[string]bool{}
	for _, action := range protocol.AllActions() {
		mutates[action.Name] = action.Mutates
	}
	for _, call := range calls {
		if mutates[call.Action] {
			t.Errorf("unexpected mutation: %+v", call)
		}
	}
}

func TestPcbRouteCriticalStackupPreflightCLI(t *testing.T) {
	for _, tc := range []struct {
		name, response, wantError string
		declared                  int
		dryRun, skipPower         bool
	}{
		{"unknown-with-spec", `{"ok":true,"result":{}}`, "no reliable copper-layer evidence", 2, false, false},
		{"unknown-dry-run", `{"ok":true,"result":{}}`, "no reliable copper-layer evidence", 2, true, false},
		{"unknown-without-spec", `{"ok":true,"result":{}}`, "no reliable copper-layer evidence", 0, false, false},
		{"read-failure", `{"ok":false,"error":{"code":"EDA_API_ERROR","message":"unavailable"}}`, "read copper layers", 0, false, false},
		{"stale-read", `{"ok":false,"error":{"code":"STALE_READ","message":"reload required"}}`, "reload", 2, false, false},
		{"spec-conflict", `{"ok":true,"result":{"copperLayerCount":2}}`, "stackup conflict", 4, false, false},
		{"spec-conflict-dry-run", `{"ok":true,"result":{"copperLayerCount":2}}`, "stackup conflict", 4, true, false},
		{"spec-conflict-skip-power", `{"ok":true,"result":{"copperLayerCount":2}}`, "stackup conflict", 4, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, state, closeFn := newAutolayoutTestDaemon(t, func(_ int, c autolayoutTestCall) string {
				return stackupTestBoardResponse(c, tc.response)
			})
			defer closeFn()
			var out, errOut bytes.Buffer
			cmd := newPcbCmd(cfg, &out, &errOut)
			cmd.SilenceErrors, cmd.SilenceUsage = true, true
			args := []string{"route-critical", "--window", "w1", "--allow-stackup-change"}
			if tc.declared > 0 {
				path := filepath.Join(t.TempDir(), "spec.json")
				if err := os.WriteFile(path, []byte(fmt.Sprintf(`{"stackup":{"layers":%d}}`, tc.declared)), 0600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "--spec", path)
			}
			if tc.dryRun {
				args = append(args, "--dry-run")
			}
			if tc.skipPower {
				args = append(args, "--skip-power")
			}
			cmd.SetArgs(args)
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("want %q, got %v", tc.wantError, err)
			}
			calls := state.snapshot()
			assertNoStackupTestMutations(t, calls)
			if len(calls) != 1 || calls[0].Action != "pcb.layers.list" {
				t.Fatalf("preflight should stop before other board work: %+v", calls)
			}
		})
	}
}

func TestPcbRouteCriticalTwoLayerUsesPowerPourCLI(t *testing.T) {
	// Include all 32 disabled inner layers: the original incident's live shape.
	raw, err := json.Marshal(map[string]any{"ok": true, "result": map[string]any{"layers": twoLayerBoard()}})
	if err != nil {
		t.Fatal(err)
	}
	cfg, state, closeFn := newAutolayoutTestDaemon(t, func(_ int, c autolayoutTestCall) string { return stackupTestBoardResponse(c, string(raw)) })
	defer closeFn()
	var out, errOut bytes.Buffer
	cmd := newPcbCmd(cfg, &out, &errOut)
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	cmd.SetArgs([]string{"route-critical", "--window", "w1", "--allow-stackup-change", "--dry-run", "--skip-diff", "--no-lock"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v; %s", err, errOut.String())
	}
	var result map[string]any
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode %s: %v", out.String(), err)
	}
	if !strings.HasPrefix(asString(result["power"]), "power-pour (2-layer") {
		t.Fatalf("wrong power recipe: %s", out.String())
	}
	if result["copperLayerCount"] != float64(2) {
		t.Fatalf("wrong live layer count: %s", out.String())
	}
	assertNoStackupTestMutations(t, state.snapshot())
}

func TestPcbPowerPlanesStackupPreflightCLI(t *testing.T) {
	for _, tc := range []struct {
		name          string
		live          int
		allow, dryRun bool
		wantError     string
		wantTarget    int
	}{
		{"two-layer-refused", 2, false, false, "refusing to re-stack", 0},
		{"two-layer-dry-refused", 2, false, true, "refusing to re-stack", 0},
		{"unknown-refused", 0, false, false, "no reliable copper-layer evidence", 0},
		{"unknown-allow-refused", 0, true, false, "no reliable copper-layer evidence", 0},
		{"unknown-allow-dry-refused", 0, true, true, "no reliable copper-layer evidence", 0},
		{"read-failure-allow-refused", -1, true, false, "read copper layers", 0},
		{"read-failure-allow-dry-refused", -1, true, true, "read copper layers", 0},
		{"four-layer-dry-preserved", 4, false, true, "", 4},
		{"six-layer-dry-preserved", 6, true, true, "", 6},
		{"six-layer-execution-preserved", 6, true, false, "", 6},
		{"explicit-upgrade-plan", 2, true, true, "", 4},
		{"explicit-upgrade-execution", 2, true, false, "", 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, state, closeFn := newAutolayoutTestDaemon(t, func(_ int, c autolayoutTestCall) string {
				layers := fmt.Sprintf(`{"ok":true,"result":{"copperLayerCount":%d}}`, tc.live)
				if tc.live == -1 {
					layers = `{"ok":false,"error":{"code":"EDA_API_ERROR","message":"unavailable"}}`
				}
				return stackupTestBoardResponse(c, layers)
			})
			defer closeFn()
			var out, errOut bytes.Buffer
			cmd := newPcbCmd(cfg, &out, &errOut)
			cmd.SilenceErrors, cmd.SilenceUsage = true, true
			args := []string{"power-planes", "--window", "w1"}
			if tc.allow {
				args = append(args, "--allow-stackup-change")
			}
			if tc.dryRun {
				args = append(args, "--dry-run")
			}
			cmd.SetArgs(args)
			err := cmd.Execute()
			calls := state.snapshot()
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("want %q, got %v", tc.wantError, err)
				}
				assertNoStackupTestMutations(t, calls)
				if len(calls) != 1 || calls[0].Action != "pcb.layers.list" {
					t.Fatalf("preflight should stop immediately: %+v", calls)
				}
				return
			}
			if err != nil {
				t.Fatalf("execute: %v; %s", err, errOut.String())
			}
			if tc.dryRun {
				assertNoStackupTestMutations(t, calls)
				var result struct {
					Stackup struct {
						Current int    `json:"currentCopperLayers"`
						Target  int    `json:"targetCopperLayers"`
						Change  bool   `json:"changeRequired"`
						Source  string `json:"source"`
					} `json:"stackup"`
				}
				if err := json.Unmarshal(out.Bytes(), &result); err != nil {
					t.Fatalf("decode %s: %v", out.String(), err)
				}
				if result.Stackup.Current != tc.live || result.Stackup.Target != tc.wantTarget || result.Stackup.Change != (tc.live != tc.wantTarget) || result.Stackup.Source == "" {
					t.Fatalf("wrong stackup plan: %s", out.String())
				}
			} else {
				countWrites, copperWrites := 0, 0
				for _, c := range calls {
					if c.Action == "pcb.stackup.set" {
						if n, ok := asFloatOK(c.Payload["count"]); ok {
							countWrites++
							if int(n) != tc.wantTarget {
								t.Errorf("wrong count mutation: %+v", c)
							}
						}
					}
					if c.Action == "pcb.pour.create" {
						copperWrites++
					}
				}
				wantCountWrites := 0
				if tc.live != tc.wantTarget {
					wantCountWrites = 1
				}
				if countWrites != wantCountWrites || copperWrites == 0 {
					t.Fatalf("want %d layer-count writes and copper work; got %d and %d: %+v", wantCountWrites, countWrites, copperWrites, calls)
				}
			}
		})
	}
}
