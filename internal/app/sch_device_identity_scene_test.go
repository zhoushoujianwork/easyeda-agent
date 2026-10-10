package app

import (
	"encoding/json"
	"reflect"
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

// Exercise the shared fresh-read adapter with both connector outputs. A short
// connector proof is never promoted directly into canonical scene evidence.
func identityCompatConnectorSceneFixture(t *testing.T, connector bool, change func(map[string]any, *schematicIdentityProbe)) map[string]any {
	t.Helper()
	original, hit := identityCompatFixture()
	if connector {
		delete(original, "deviceIdentityError")
		original["placedDevice"] = original["device"]
		original["device"] = map[string]any{"uuid": hit.UUID, "libraryUuid": hit.LibraryUUID, "name": original["name"]}
		original["deviceResolution"] = map[string]any{"via": "lcsc-footprint-source", "lcsc": hit.SupplierID, "sameFootprintUUID": true, "footprintSource": map[string]any{"instanceUuid": "abe23dba1def1246", "uuid": identityCompatSource().UUID, "libraryUuid": "system", "sourceKind": "project-epro2"}}
	}
	proof := identityCompatProof(hit)
	if change != nil {
		change(original, &proof)
	}
	rawBefore, _ := json.Marshal(original)
	cfg, daemon, cleanup := newBlockApplyTestDaemon(t, func(call blockApplyTestCall) string {
		switch call.Action {
		case "schematic.components.list":
			return identityCompatEnvelope(map[string]any{"components": []any{original}, "wires": []any{}, "connectivitySummary": map[string]any{"netCount": 1}}, "page")
		case "debug.exec_js":
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
	if len(daemon.snapshot()) != 2 {
		t.Fatal("connector-resolved identity did not receive independent fresh official proof")
	}
	rawAfter, _ := json.Marshal(original)
	if string(rawBefore) != string(rawAfter) {
		t.Fatal("identity proof hydration modified caller snapshot")
	}
	return result.(map[string]any)
}

func TestSchematicIdentityCompatSceneUnifiesConnectorAndCompatProofs(t *testing.T) {
	before := identityCompatConnectorSceneFixture(t, false, nil)
	wanted, err := schDesignatorScene(before)
	if err != nil {
		t.Fatal(err)
	}
	// Protected playbook checkpoints cross JSON on disk before Apply compares
	// them with the newly read scene.
	wanted = identityCompatCloneScene(t, wanted)
	for _, scenario := range []string{"connector-short-proof", "connector-named-proof", "compat-name-omitted", "source-read-path"} {
		t.Run(scenario, func(t *testing.T) {
			live := identityCompatConnectorSceneFixture(t, strings.HasPrefix(scenario, "connector"), func(c map[string]any, p *schematicIdentityProbe) {
				switch scenario {
				case "connector-named-proof":
					schematicIdentityMap(c, "deviceResolution")["footprint"] = "optional search display name"
				case "compat-name-omitted":
					p.Candidates[0].Footprint.Name = ""
				case "source-read-path":
					p.NativeFootprints[0].SourceKind = "document-footprint-sources"
				}
			})
			c := live["components"].([]any)[0].(map[string]any)
			if !schematicIdentityCompatSceneProof(c) {
				t.Fatalf("fresh complete independent proof unavailable: %+v", c)
			}
			if err := checkSchPreservedSource(wanted, live); err != nil {
				t.Fatalf("equivalent native/official identity proof changed with lookup output shape: %v", err)
			}
			got, _ := schDesignatorScene(live)
			if !reflect.DeepEqual(wanted, got) {
				t.Fatal("identity proof did not reach one exact canonical scene structure")
			}
		})
	}
}

func TestSchematicIdentityCompatConnectorProofStillRejectsConflictAndMissingEvidence(t *testing.T) {
	before := identityCompatConnectorSceneFixture(t, false, nil)
	wanted, err := schDesignatorScene(before)
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"resolved-device-conflict", "resolved-library-conflict", "missing-official-detail", "missing-native-source", "association-conflict", "source-library-conflict", "ambiguous"} {
		t.Run(scenario, func(t *testing.T) {
			live := identityCompatConnectorSceneFixture(t, true, func(c map[string]any, p *schematicIdentityProbe) {
				switch scenario {
				case "resolved-device-conflict":
					schematicIdentityMap(c, "device")["uuid"] = strings.Repeat("a", 32)
				case "resolved-library-conflict":
					schematicIdentityMap(c, "device")["libraryUuid"] = "other"
				case "missing-official-detail":
					p.Candidates[0].Device = nil
				case "missing-native-source":
					p.NativeFootprints = nil
				case "association-conflict":
					p.Candidates[0].Device.Footprint.UUID = strings.Repeat("b", 32)
				case "source-library-conflict":
					p.NativeFootprints[0].MetadataLines[1] = strings.Replace(p.NativeFootprints[0].MetadataLines[1], "|system", "|other", 1)
				case "ambiguous":
					other := p.Candidates[0]
					other.UUID = strings.Repeat("c", 32)
					detail := *other.Device
					detail.UUID = other.UUID
					other.Device = &detail
					p.Candidates = append(p.Candidates, other)
				}
			})
			c := live["components"].([]any)[0].(map[string]any)
			if c["deviceIdentityCompatibilityError"] == nil {
				t.Fatal("connector identity escaped independent conflict/missing-evidence guard")
			}
			if _, err := measuredSchematicDevice("U1", c); err == nil {
				t.Fatal("failed independent proof remained replayable")
			}
			if err := checkSchPreservedSource(wanted, live); err == nil {
				t.Fatal("failed identity proof was normalized into an equivalent scene")
			}
		})
	}
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
