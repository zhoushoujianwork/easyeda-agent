package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func identityCompatResolvedSceneFixture(t *testing.T, custom ...bool) map[string]any {
	t.Helper()
	original, hit := identityCompatFixture()
	if len(custom) != 0 && custom[0] {
		original, hit, _ = identityCompatCustomFixture("MOTOBOX-TP-1MM", false)
	}
	original["deviceIdentityCandidates"] = []any{map[string]any{"footprintLibraryUuid": "old-personal-copy"}}
	cfg, _, cleanup := newBlockApplyTestDaemon(t, func(call blockApplyTestCall) string {
		switch call.Action {
		case "schematic.components.list":
			return identityCompatEnvelope(map[string]any{"components": []any{original}, "wires": []any{}, "connectivitySummary": map[string]any{"netCount": 1}}, "page")
		case "debug.exec_js":
			proof := identityCompatProof(hit)
			if len(custom) != 0 && custom[0] {
				proof.Query.LCSCIDs = []string{}
				proof.Query.Searches = schematicIdentitySearches([]map[string]any{original})
				proof.NativeFootprints[0].MetadataLines[1] = strings.Replace(proof.NativeFootprints[0].MetadataLines[1], "|system", "|personal", 1)
			}
			return identityCompatEnvelope(map[string]any{"value": proof}, "page")
		default:
			t.Fatalf("unexpected action %s", call.Action)
			return ""
		}
	})
	defer cleanup()
	runner := applyRunner{cfg: cfg, window: "w1"}
	result, err := runner.runAction("schematic.components.list", map[string]any{"includeDeviceIdentity": true}, defaultActionTimeout)
	if err != nil {
		t.Fatal(err)
	}
	return result.(map[string]any)
}

func identityCompatCloneScene(t *testing.T, scene map[string]any) map[string]any {
	t.Helper()
	b, err := json.Marshal(scene)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSchematicIdentityCompatSceneIgnoresOnlySupersededDiagnostics(t *testing.T) {
	original := identityCompatResolvedSceneFixture(t)
	rawBefore, _ := json.Marshal(original)
	wanted, err := schDesignatorScene(original)
	if err != nil {
		t.Fatal(err)
	}
	live := identityCompatCloneScene(t, original)
	c := live["components"].([]any)[0].(map[string]any)
	c["deviceIdentityCandidates"] = []any{map[string]any{"footprintLibraryUuid": "different-rejected-copy"}, map[string]any{"uuid": "another-rejected-copy"}}
	r := schematicIdentityMap(c, "deviceResolution")
	r["probeId"], r["connectorError"] = "new-fresh-probe", "same failure with reordered candidates"
	if err := checkSchPreservedSource(wanted, live); err != nil {
		t.Fatalf("fresh exact identity rejected volatile old diagnostics: %v", err)
	}
	rawAfter, _ := json.Marshal(original)
	if string(rawBefore) != string(rawAfter) {
		t.Fatal("scene normalization modified raw identity evidence")
	}
	for _, scenario := range []string{"device", "device-library", "instance-footprint", "instance-footprint-library", "footprint-name", "native-source", "native-source-library", "association", "association-library", "property", "position", "rotation", "pin-net", "pin-position", "assembly", "active-identity-error", "active-compat-error"} {
		t.Run(scenario, func(t *testing.T) {
			live := identityCompatCloneScene(t, original)
			c := live["components"].([]any)[0].(map[string]any)
			r := schematicIdentityMap(c, "deviceResolution")
			switch scenario {
			case "device":
				schematicIdentityMap(c, "device")["uuid"] = strings.Repeat("a", 32)
			case "device-library":
				schematicIdentityMap(c, "device")["libraryUuid"] = "different"
			case "instance-footprint":
				schematicIdentityMap(c, "footprint")["uuid"] = strings.Repeat("b", 16)
			case "instance-footprint-library":
				schematicIdentityMap(c, "footprint")["libraryUuid"] = "different"
			case "footprint-name":
				schematicIdentityMap(c, "footprint")["name"] = "different"
			case "native-source":
				schematicIdentityMap(r, "footprintSource")["uuid"] = strings.Repeat("c", 32)
			case "native-source-library":
				schematicIdentityMap(r, "footprintSource")["libraryUuid"] = "different"
			case "association":
				schematicIdentityMap(r, "libraryFootprint")["uuid"] = strings.Repeat("d", 32)
			case "association-library":
				schematicIdentityMap(r, "libraryFootprint")["libraryUuid"] = "different"
			case "property":
				c["otherProperty"] = map[string]any{"Value": "different"}
			case "position":
				c["x"] = 191.0
			case "rotation":
				c["rotation"] = 90.0
			case "pin-net":
				c["pins"].([]any)[0].(map[string]any)["net"] = "different"
			case "pin-position":
				c["pins"].([]any)[0].(map[string]any)["x"] = 144.0
			case "assembly":
				c["addIntoBom"] = false
			case "active-identity-error":
				c["deviceIdentityError"] = "unresolved"
			case "active-compat-error":
				c["deviceIdentityCompatibilityError"] = "unresolved"
			}
			if err := checkSchPreservedSource(wanted, live); err == nil {
				t.Fatal("stable resolved identity or circuit drift was ignored")
			}
		})
	}
}

func TestSchematicIdentityCompatSceneCustomExactProof(t *testing.T) {
	original := identityCompatResolvedSceneFixture(t, true)
	wanted, err := schDesignatorScene(original)
	if err != nil {
		t.Fatal(err)
	}
	live := identityCompatCloneScene(t, original)
	c := live["components"].([]any)[0].(map[string]any)
	c["deviceIdentityCandidates"] = []any{map[string]any{"footprintLibraryUuid": "different-rejected-copy"}}
	schematicIdentityMap(c, "deviceResolution")["probeId"] = "fresh-custom-probe"
	if err := checkSchPreservedSource(wanted, live); err != nil {
		t.Fatalf("custom exact identity rejected volatile diagnostics: %v", err)
	}
}

func TestSchematicIdentityCompatSceneKeepsUnprovenDiagnostics(t *testing.T) {
	for _, scenario := range []string{"no-proof", "unknown-resolver", "missing-source", "conflicting-association", "active-error"} {
		t.Run(scenario, func(t *testing.T) {
			original := identityCompatResolvedSceneFixture(t)
			c := original["components"].([]any)[0].(map[string]any)
			r := schematicIdentityMap(c, "deviceResolution")
			switch scenario {
			case "no-proof":
				delete(c, "deviceResolution")
			case "unknown-resolver":
				r["resolver"] = "not-the-verified-cli-resolver"
			case "missing-source":
				delete(r, "footprintSource")
			case "conflicting-association":
				schematicIdentityMap(r, "libraryFootprint")["uuid"] = strings.Repeat("a", 32)
			case "active-error":
				c["deviceIdentityError"] = "unresolved"
			}
			wanted, err := schDesignatorScene(original)
			if err != nil {
				t.Fatal(err)
			}
			live := identityCompatCloneScene(t, original)
			live["components"].([]any)[0].(map[string]any)["deviceIdentityCandidates"] = []any{map[string]any{"footprintLibraryUuid": "different"}}
			if err := checkSchPreservedSource(wanted, live); err == nil {
				t.Fatal("unproven connector diagnostics silently discarded")
			}
		})
	}
}
