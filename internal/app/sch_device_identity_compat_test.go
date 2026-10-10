package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func identityCompatFixture() (map[string]any, schematicIdentityCandidate) {
	c := map[string]any{"componentType": "part", "primitiveId": "native-1", "designator": "U1", "supplierId": "C6186", "manufacturerId": "AMS1117-3.3", "name": "={Manufacturer Part}", "device": map[string]any{"uuid": "6e8a0f3cb342d055", "libraryUuid": "system"}, "component": map[string]any{"uuid": "6e8a0f3cb342d055", "libraryUuid": "system", "name": "AMS1117-3.3"}, "footprint": map[string]any{"uuid": "abe23dba1def1246", "libraryUuid": "system", "name": "SOT-223-3_L6.5-W3.4-P2.30-LS7.0-BR"}, "deviceIdentityError": "package-variant mismatch", "x": 190.0, "y": 740.0, "pins": []any{map[string]any{"pinNumber": "1", "x": 145.0, "y": 730.0, "net": "GND", "noConnected": false}}}
	hit := schematicIdentityCandidate{UUID: "9f9c6cb41c7449fd8acf96aceed2661a", LibraryUUID: "system", SupplierID: "C6186", ManufacturerID: "AMS1117-3.3", Name: "AMS1117-3.3", Footprint: schematicIdentityAsset{UUID: "20c29e37a9b84b4197418483096f9c05", Name: "SOT-223-3_L6.5-W3.4-P2.30-LS7.0-BR"}, Device: &schematicIdentityDevice{UUID: "9f9c6cb41c7449fd8acf96aceed2661a", LibraryUUID: "system", SupplierID: "C6186", ManufacturerID: "AMS1117-3.3", Name: "AMS1117-3.3", Footprint: schematicIdentityAsset{UUID: "20c29e37a9b84b4197418483096f9c05", LibraryUUID: "system"}}}
	return c, hit
}
func identityCompatSource() schematicIdentityAsset {
	return schematicIdentityAsset{UUID: "20c29e37a9b84b4197418483096f9c05", LibraryUUID: "system"}
}
func identityCompatNativeFootprint() schematicNativeFootprint {
	return schematicNativeFootprint{FootprintUUID: "abe23dba1def1246", MetadataLines: []string{`{"type":"DOCHEAD"}||{"docType":"FOOTPRINT","uuid":"abe23dba1def1246"}|`, `{"type":"META"}||{"title":"SOT-223","source":"20c29e37a9b84b4197418483096f9c05|system"}|`}}
}

func TestSchematicIdentityCompatNativeSourceOwnerAndConflict(t *testing.T) {
	source, err := schematicNativeFootprintIdentity("abe23dba1def1246", []schematicNativeFootprint{identityCompatNativeFootprint()})
	if err != nil || source.UUID != identityCompatSource().UUID {
		t.Fatalf("native proof parse failed %+v %v", source, err)
	}
	for _, scenario := range []string{"missing", "duplicate-entry", "wrong-header-owner", "wrong-header-type", "missing-meta", "duplicate-meta", "wrong-source-shape", "extra-record", "bad-json"} {
		t.Run(scenario, func(t *testing.T) {
			entry := identityCompatNativeFootprint()
			entries := []schematicNativeFootprint{entry}
			switch scenario {
			case "missing":
				entries = nil
			case "duplicate-entry":
				entries = append(entries, entry)
			case "wrong-header-owner":
				entries[0].MetadataLines[0] = strings.Replace(entry.MetadataLines[0], "abe23dba1def1246", "6f7ca9a9603aeb19", 1)
			case "wrong-header-type":
				entries[0].MetadataLines[0] = strings.Replace(entry.MetadataLines[0], "FOOTPRINT", "DEVICE", 1)
			case "missing-meta":
				entries[0].MetadataLines = entry.MetadataLines[:1]
			case "duplicate-meta":
				entries[0].MetadataLines = append(entry.MetadataLines, entry.MetadataLines[1])
			case "wrong-source-shape":
				entries[0].MetadataLines[1] = strings.Replace(entry.MetadataLines[1], "|system", "|system|extra", 1)
			case "extra-record":
				entries[0].MetadataLines = append(entry.MetadataLines, `{"type":"OTHER"}||{}|`)
			case "bad-json":
				entries[0].MetadataLines[1] = "not-json"
			}
			if _, err := schematicNativeFootprintIdentity(entry.FootprintUUID, entries); err == nil {
				t.Fatal("missing/conflicting native source accepted")
			}
		})
	}
	c, hit := identityCompatFixture()
	hit.Footprint.Name = "renamed-but-same-source-asset"
	if _, err := resolveSchematicIdentityCompat(c, []schematicIdentityCandidate{hit}, identityCompatSource()); err != nil {
		t.Fatalf("verified asset source should outrank display name %v", err)
	}
}

func TestSchematicIdentityCompatRealNativeSourceFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "hong-en-native-footprint-identities.json"))
	if err != nil {
		t.Fatal(err)
	}
	var entries []schematicNativeFootprint
	if err = json.Unmarshal(raw, &entries); err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{
		"31e887476b49ae34": "66e2cc0f8deb4eadb03b0d57d0844669",
		"a99d5799f5993404": "603d7582b3e443d3a1216eba088f6037",
		"abe23dba1def1246": "20c29e37a9b84b4197418483096f9c05",
		"7c075d0795252a7b": "14a08379d3874582bdd84020bde81eba",
		"6f7ca9a9603aeb19": "ccb32feceadc4298b406326a506ce8e7",
		"d7d457afcd84d923": "df9dfd54cabd403f88cae9927dcd418d",
	}
	if len(entries) != len(expected) {
		t.Fatalf("fixture should cover all six footprints used by the eleven Hong-en components: got %d", len(entries))
	}
	for local, library := range expected {
		origin, err := schematicNativeFootprintIdentity(local, entries)
		if err != nil || origin.UUID != library || origin.LibraryUUID != "0819f05c4eef4c71ace90d822a990e87" {
			t.Fatalf("real native %s evidence failed %+v %v", local, origin, err)
		}
	}
}

func TestSchematicIdentityCompatExactDomainAndProof(t *testing.T) {
	c, hit := identityCompatFixture()
	matched, err := resolveSchematicIdentityCompat(c, []schematicIdentityCandidate{hit}, identityCompatSource())
	if err != nil || matched.UUID != hit.UUID {
		t.Fatalf("official instance/library domains failed: %+v %v", matched, err)
	}
	tests := []struct {
		name   string
		change func(map[string]any, *schematicIdentityCandidate)
	}{
		{"real 32 footprint conflict", func(c map[string]any, h *schematicIdentityCandidate) {
			schematicIdentityMap(c, "footprint")["uuid"] = strings.Repeat("a", 32)
		}},
		{"unknown domain", func(c map[string]any, h *schematicIdentityCandidate) {
			schematicIdentityMap(c, "component")["uuid"] = "unverified"
		}},
		{"missing instance library", func(c map[string]any, h *schematicIdentityCandidate) {
			delete(schematicIdentityMap(c, "footprint"), "libraryUuid")
		}},
		{"wrong exact C", func(c map[string]any, h *schematicIdentityCandidate) { h.SupplierID = "C999" }},
		{"wrong search MPN", func(c map[string]any, h *schematicIdentityCandidate) { h.ManufacturerID = "AMS1117-5.0" }},
		{"wrong detailed MPN", func(c map[string]any, h *schematicIdentityCandidate) { h.Device.ManufacturerID = "AMS1117-5.0" }},
		{"missing detailed proof", func(c map[string]any, h *schematicIdentityCandidate) { h.Device = nil }},
		{"wrong association footprint", func(c map[string]any, h *schematicIdentityCandidate) {
			h.Device.Footprint.UUID = strings.Repeat("a", 32)
		}},
		{"same named other asset library", func(c map[string]any, h *schematicIdentityCandidate) { h.Device.Footprint.LibraryUUID = "another" }},
		{"device detail identity changed", func(c map[string]any, h *schematicIdentityCandidate) { h.Device.UUID = strings.Repeat("b", 32) }},
		{"query error", func(c map[string]any, h *schematicIdentityCandidate) { h.Error = "library request failed" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, h := identityCompatFixture()
			tt.change(c, &h)
			if _, err := resolveSchematicIdentityCompat(c, []schematicIdentityCandidate{h}, identityCompatSource()); err == nil {
				t.Fatal("incomplete or conflicting identity was accepted")
			}
		})
	}
	c, h := identityCompatFixture()
	if _, err := resolveSchematicIdentityCompat(c, []schematicIdentityCandidate{h, h}, identityCompatSource()); err != nil {
		t.Fatalf("identical candidate rows are one asset: %v", err)
	}
	other := h
	other.UUID = strings.Repeat("c", 32)
	d := *h.Device
	d.UUID = other.UUID
	other.Device = &d
	if _, err := resolveSchematicIdentityCompat(c, []schematicIdentityCandidate{h, other}, identityCompatSource()); err == nil {
		t.Fatal("multiple exact library identities accepted")
	}
	c, h = identityCompatFixture()
	c["manufacturerId"] = ""
	h.ManufacturerID = ""
	h.Device.ManufacturerID = ""
	if _, err := resolveSchematicIdentityCompat(c, []schematicIdentityCandidate{h}, identityCompatSource()); err != nil {
		t.Fatalf("exact stable device name fallback failed: %v", err)
	}
}

func identityCompatCustomFixture(model string, stableName bool) (map[string]any, schematicIdentityCandidate, schematicIdentityAsset) {
	c, hit := identityCompatFixture()
	c["supplierId"] = ""
	c["manufacturerId"] = model
	c["name"] = model
	schematicIdentityMap(c, "device")["libraryUuid"] = "personal"
	schematicIdentityMap(c, "component")["libraryUuid"] = "personal"
	schematicIdentityMap(c, "component")["name"] = model
	schematicIdentityMap(c, "footprint")["libraryUuid"] = "personal"
	hit.LibraryUUID, hit.SupplierID, hit.ManufacturerID, hit.Name = "personal", "", model, model
	hit.Footprint.LibraryUUID = "personal"
	hit.Device.LibraryUUID, hit.Device.SupplierID, hit.Device.ManufacturerID, hit.Device.Name = "personal", "", model, model
	hit.Device.Footprint.LibraryUUID = "personal"
	if stableName {
		c["manufacturerId"] = ""
		hit.ManufacturerID, hit.Device.ManufacturerID = "", ""
	}
	source := identityCompatSource()
	source.LibraryUUID = "personal"
	return c, hit, source
}

func TestSchematicIdentityCompatCustomNoSupplierExactProof(t *testing.T) {
	for _, model := range []string{"RT0603BRD07583KL", "MOTOBOX-TP-1MM"} {
		for _, stableName := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/name=%t", model, stableName), func(t *testing.T) {
				c, hit, source := identityCompatCustomFixture(model, stableName)
				matched, err := resolveSchematicIdentityCompat(c, []schematicIdentityCandidate{hit}, source)
				if err != nil || matched.UUID != hit.UUID {
					t.Fatalf("exact custom library proof rejected: %+v %v", matched, err)
				}
			})
		}
	}
	for _, scenario := range []string{"missing-model-and-name", "malformed-supplier", "wrong-search-supplier", "wrong-detail-supplier", "wrong-model", "wrong-device-library", "wrong-footprint-source", "wrong-detail-association", "missing-detail", "ambiguous"} {
		t.Run(scenario, func(t *testing.T) {
			c, hit, source := identityCompatCustomFixture("RT0603BRD07583KL", false)
			hits := []schematicIdentityCandidate{hit}
			switch scenario {
			case "missing-model-and-name":
				c["manufacturerId"], c["name"] = "", "={Manufacturer Part}"
				schematicIdentityMap(c, "component")["name"] = "={Manufacturer Part}"
			case "malformed-supplier":
				c["supplierId"], hits[0].SupplierID, hits[0].Device.SupplierID = "not-a-C-number", "not-a-C-number", "not-a-C-number"
			case "wrong-search-supplier":
				hits[0].SupplierID = "C6186"
			case "wrong-detail-supplier":
				hits[0].Device.SupplierID = "C6186"
			case "wrong-model":
				hits[0].ManufacturerID, hits[0].Device.ManufacturerID = "different", "different"
			case "wrong-device-library":
				hits[0].LibraryUUID, hits[0].Device.LibraryUUID = "other", "other"
			case "wrong-footprint-source":
				source.UUID = strings.Repeat("f", 32)
			case "wrong-detail-association":
				hits[0].Device.Footprint.LibraryUUID = "other"
			case "missing-detail":
				hits[0].Device = nil
			case "ambiguous":
				other := hit
				other.UUID = strings.Repeat("c", 32)
				d := *hit.Device
				d.UUID = other.UUID
				other.Device = &d
				hits = append(hits, other)
			}
			if _, err := resolveSchematicIdentityCompat(c, hits, source); err == nil {
				t.Fatal("custom identity accepted incomplete or conflicting proof")
			}
		})
	}
}

func TestSchematicIdentityCompatCustomNoSupplierSharedRead(t *testing.T) {
	original, hit, _ := identityCompatCustomFixture("RT0603BRD07583KL", false)
	before, _ := json.Marshal(original)
	cfg, _, cleanup := newBlockApplyTestDaemon(t, func(call blockApplyTestCall) string {
		if call.Action == "schematic.components.list" {
			return identityCompatEnvelope(map[string]any{"components": []any{original}}, "page")
		}
		if call.Action != "debug.exec_js" {
			t.Fatalf("unexpected action %s", call.Action)
		}
		code, _ := call.Payload["code"].(string)
		if !strings.Contains(code, "const ids = [];") {
			t.Fatal("empty supplier sent to LCSC lookup")
		}
		p := identityCompatProof(hit)
		p.Query.LCSCIDs = []string{}
		p.Query.Searches = schematicIdentitySearches([]map[string]any{original})
		p.NativeFootprints[0].MetadataLines[1] = strings.Replace(p.NativeFootprints[0].MetadataLines[1], "|system", "|personal", 1)
		return identityCompatEnvelope(map[string]any{"value": p}, "page")
	})
	defer cleanup()
	runner := applyRunner{cfg: cfg, window: "w1"}
	result, err := runner.runAction("schematic.components.list", map[string]any{"includeDeviceIdentity": true}, defaultActionTimeout)
	if err != nil {
		t.Fatal(err)
	}
	c := result.(map[string]any)["components"].([]any)[0].(map[string]any)
	if schematicIdentityString(schematicIdentityMap(c, "device"), "uuid") != hit.UUID || c["deviceIdentityError"] != nil {
		t.Fatalf("custom identity unresolved %+v", c)
	}
	resolution := schematicIdentityMap(c, "deviceResolution")
	if resolution["via"] != "scoped-library-footprint-source" || resolution["lcsc"] != nil || resolution["sameFootprintUUID"] != true {
		t.Fatalf("custom proof provenance inaccurate %+v", resolution)
	}
	after, _ := json.Marshal(original)
	if string(before) != string(after) {
		t.Fatal("custom read adapter mutated caller snapshot")
	}
}

func TestSchematicIdentityProbeCustomNoSupplierSkipsLCSCLookup(t *testing.T) {
	c, _, source := identityCompatCustomFixture("MOTOBOX-TP-1MM", false)
	code, _ := json.Marshal(schematicIdentityProbeCode([]string{}, []string{"abe23dba1def1246"}, "project", "page", schematicIdentitySearches([]map[string]any{c})...))
	entry := identityCompatNativeFootprint()
	entry.MetadataLines[1] = strings.Replace(entry.MetadataLines[1], "|system", "|personal", 1)
	native, _ := json.Marshal(entry)
	js := `
const entry = ` + string(native) + `;
const eda = {
 dmt_Project:{getCurrentProjectInfo:async()=>({uuid:"project"})},
 dmt_SelectControl:{getCurrentDocumentInfo:async()=>({uuid:"page"})},
 sys_FileManager:{getDocumentFootprintSources:async()=>[{footprintUuid:entry.footprintUuid,documentSource:entry.metadataLines.join("\n")}]},
 lib_Device:{
  getByLcscIds:async()=>{throw new Error("custom device must not query LCSC");},
  search:async(query,library)=>{if(query!=="MOTOBOX-TP-1MM"||library!=="personal")throw new Error("wrong scoped query");return [{uuid:"9f9c6cb41c7449fd8acf96aceed2661a",libraryUuid:library,name:query}];},
  get:async(uuid,library)=>({uuid,libraryUuid:library,name:"MOTOBOX-TP-1MM",property:{supplierId:"",manufacturerId:"MOTOBOX-TP-1MM"},association:{footprint:{uuid:"20c29e37a9b84b4197418483096f9c05",libraryUuid:library}}})
 }
};
(async()=>{const value=await new (Object.getPrototypeOf(async function(){}).constructor)("eda",` + string(code) + `)(eda);process.stdout.write(JSON.stringify(value));})().catch(e=>{process.stderr.write(String(e));process.exitCode=1;});`
	var proof schematicIdentityProbe
	if err := json.Unmarshal(runIdentityJavaScript(t, js), &proof); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveSchematicIdentityCompat(c, proof.Candidates, source); err != nil {
		t.Fatal(err)
	}
}

func identityCompatEnvelope(result any, doc string) string {
	b, _ := json.Marshal(map[string]any{"ok": true, "id": "probe-test", "result": result, "context": map[string]any{"projectUuid": "project", "documentUuid": doc, "documentType": "schematic"}})
	return string(b)
}
func identityCompatProof(hit schematicIdentityCandidate) schematicIdentityProbe {
	var proof schematicIdentityProbe
	proof.SchemaVersion = 1
	proof.Query.LCSCIDs = []string{"C6186"}
	proof.Query.AllowMultiMatch = true
	proof.Query.Searches = []schematicIdentitySearch{{Query: "AMS1117-3.3", LibraryUUID: "system"}}
	proof.Candidates = []schematicIdentityCandidate{hit}
	proof.NativeFootprints = []schematicNativeFootprint{identityCompatNativeFootprint()}
	return proof
}

func TestSchematicIdentityCompatSharedDispatchAndApplyRead(t *testing.T) {
	original, hit := identityCompatFixture()
	before, _ := json.Marshal(original)
	cfg, daemon, cleanup := newBlockApplyTestDaemon(t, func(call blockApplyTestCall) string {
		switch call.Action {
		case "schematic.components.list":
			return identityCompatEnvelope(map[string]any{"components": []any{original}}, "page")
		case "debug.exec_js":
			code, _ := call.Payload["code"].(string)
			if !strings.Contains(code, "getByLcscIds(ids, undefined, true)") || !strings.Contains(code, "lib_Device.get(") {
				t.Error("probe did not collect complete official identity evidence")
			}
			for _, bad := range []string{".create(", ".modify(", ".delete(", ".open("} {
				if strings.Contains(code, bad) {
					t.Error("read probe contains a mutation")
				}
			}
			return identityCompatEnvelope(map[string]any{"value": identityCompatProof(hit)}, "page")
		default:
			t.Errorf("unexpected action %s", call.Action)
			return ""
		}
	})
	defer cleanup()
	// Both raw stdout dispatch (sch list) and Apply's own runAction pass through
	// the shared choke point. No command can hydrate only a user-supplied baseline.
	var out, stderr bytes.Buffer
	if err := dispatch(cfg, "schematic.components.list", "w1", map[string]any{"includeDeviceIdentity": true}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	_ = json.Unmarshal(out.Bytes(), &envelope)
	check := func(result map[string]any) {
		c := result["components"].([]any)[0].(map[string]any)
		if schematicIdentityString(schematicIdentityMap(c, "device"), "uuid") != hit.UUID {
			t.Fatalf("identity unresolved %+v", c)
		}
		if _, ok := c["deviceIdentityError"]; ok {
			t.Fatal("resolved identity retained active error")
		}
		resolution := schematicIdentityMap(c, "deviceResolution")
		if resolution["via"] != "lcsc-footprint-source" || resolution["sameFootprintUUID"] != true {
			t.Fatalf("fallback strength hidden %+v", resolution)
		}
		if !reflect.DeepEqual(c["pins"], original["pins"]) || c["x"] != original["x"] || c["y"] != original["y"] {
			t.Fatal("read adapter changed circuit data")
		}
	}
	check(schematicIdentityMap(envelope, "result"))
	runner := applyRunner{cfg: cfg, window: "w1"}
	result, err := runner.runAction("schematic.components.list", map[string]any{"includeDeviceIdentity": true}, defaultActionTimeout)
	if err != nil {
		t.Fatal(err)
	}
	check(result.(map[string]any))
	after, _ := json.Marshal(original)
	if string(before) != string(after) {
		t.Fatal("mutated caller snapshot")
	}
	if calls := daemon.snapshot(); len(calls) != 4 {
		t.Fatalf("expected one official probe per fresh list (no stale cache): %+v", calls)
	}
}

func TestSchematicIdentityCompatFailureNeverWeakensGuard(t *testing.T) {
	for _, scenario := range []string{"wrong-context", "missing-candidates", "missing-all-match-proof", "missing-scoped-search", "wrong-scoped-library", "ambiguous", "probe-error", "missing-native-source", "bad-native-source", "source-error", "dry-run"} {
		t.Run(scenario, func(t *testing.T) {
			original, hit := identityCompatFixture()
			cfg, daemon, cleanup := newBlockApplyTestDaemon(t, func(call blockApplyTestCall) string {
				if call.Action == "schematic.components.list" {
					return identityCompatEnvelope(map[string]any{"components": []any{original}}, "page")
				}
				if call.Action != "debug.exec_js" {
					t.Fatalf("unexpected action %s", call.Action)
				}
				p := identityCompatProof(hit)
				doc := "page"
				switch scenario {
				case "wrong-context":
					doc = "other-page"
				case "missing-candidates":
					p.Candidates = nil
				case "missing-all-match-proof":
					p.Query.AllowMultiMatch = false
				case "missing-scoped-search":
					p.Query.Searches = nil
				case "wrong-scoped-library":
					p.Query.Searches[0].LibraryUUID = "personal"
				case "ambiguous":
					other := hit
					other.UUID = strings.Repeat("c", 32)
					detail := *hit.Device
					detail.UUID = other.UUID
					other.Device = &detail
					p.Candidates = append(p.Candidates, other)
				case "missing-native-source":
					p.NativeFootprints = nil
				case "bad-native-source":
					p.NativeFootprints[0].MetadataLines[1] = strings.Replace(p.NativeFootprints[0].MetadataLines[1], "20c29e37a9b84b4197418483096f9c05", strings.Repeat("f", 32), 1)
				case "source-error":
					p.SourceError = "native source unavailable"
				case "probe-error":
					return `{"ok":false,"error":{"code":"FAILED","message":"offline"}}`
				}
				return identityCompatEnvelope(map[string]any{"value": p}, doc)
			})
			defer cleanup()
			if scenario == "dry-run" {
				restore := setDispatchDryRun(true)
				defer restore()
			}
			res, err := requestAction(cfg, "schematic.components.list", "w1", map[string]any{"includeDeviceIdentity": true})
			if err != nil {
				t.Fatal(err)
			}
			c := res.Result["components"].([]any)[0].(map[string]any)
			if _, err := measuredSchematicDevice("U1", c); err == nil {
				t.Fatalf("%s escaped device guard: %+v", scenario, c)
			}
			if c["deviceIdentityCompatibilityError"] == nil {
				t.Fatalf("failure reason was hidden: %+v", c)
			}
			if scenario == "dry-run" && len(daemon.snapshot()) != 1 {
				t.Fatalf("dry-run ran debug probe %+v", daemon.snapshot())
			}
		})
	}
}

func TestSchematicIdentityScopedSearchKeepsExactNativeSourceProof(t *testing.T) {
	original, _ := identityCompatFixture()
	queries := schematicIdentitySearches([]map[string]any{original, original})
	if len(queries) != 1 || queries[0].Query != "AMS1117-3.3" || queries[0].LibraryUUID != "system" {
		t.Fatalf("placed model/library queries are not exact and deduplicated: %+v", queries)
	}
	code, _ := json.Marshal(schematicIdentityProbeCode([]string{"C6186"}, []string{"abe23dba1def1246"}, "project", "page", queries...))
	entry, _ := json.Marshal(identityCompatNativeFootprint())
	for _, scenario := range []string{"personal-shadow", "wrong-C", "wrong-MPN", "wrong-footprint", "wrong-search-library", "ambiguous", "search-error"} {
		t.Run(scenario, func(t *testing.T) {
			scenarioJSON, _ := json.Marshal(scenario)
			js := `
const scenario = ` + string(scenarioJSON) + `, entry = ` + string(entry) + `;
const asset = "9f9c6cb41c7449fd8acf96aceed2661a", second = "cccccccccccccccccccccccccccccccc", personal = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb";
let searches = 0;
const eda = {
 dmt_Project:{getCurrentProjectInfo:async()=>({uuid:"project"})},
 dmt_SelectControl:{getCurrentDocumentInfo:async()=>({uuid:"page"})},
 sys_FileManager:{getDocumentFootprintSources:async()=>[{footprintUuid:entry.footprintUuid,documentSource:entry.metadataLines.join("\n")}]},
 lib_Device:{
  getByLcscIds:async(ids,unused,all)=>{if(all !== true)throw new Error("not all matches");return [{uuid:personal,libraryUuid:"personal",supplierId:"C6186",manufacturerId:"AMS1117-3.3",name:"AMS1117-3.3"}];},
  search:async(query,library)=>{searches++;if(query!=="AMS1117-3.3"||library!=="system")throw new Error("unscoped or fuzzy query");if(scenario==="search-error")throw new Error("unavailable");const row={uuid:asset,libraryUuid:scenario==="wrong-search-library"?"personal":"system",name:"AMS1117-3.3"};return scenario==="ambiguous"?[row,{...row,uuid:second}]:[row];},
  get:async(uuid,library)=>({uuid,libraryUuid:library,name:"AMS1117-3.3",property:{supplierId:scenario==="wrong-C"&&uuid!==personal?"C999":"C6186",manufacturerId:scenario==="wrong-MPN"&&uuid!==personal?"AMS1117-5.0":"AMS1117-3.3"},association:{footprint:{uuid:uuid===personal||scenario==="wrong-footprint"?"ffffffffffffffffffffffffffffffff":"20c29e37a9b84b4197418483096f9c05",libraryUuid:library}}})
 }
};
(async()=>{try{const value=await new (Object.getPrototypeOf(async function(){}).constructor)("eda",` + string(code) + `)(eda);process.stdout.write(JSON.stringify({value,searches}));}catch(error){process.stdout.write(JSON.stringify({error:String(error),searches}));}})();`
			var result struct {
				Value    schematicIdentityProbe `json:"value"`
				Error    string                 `json:"error"`
				Searches int                    `json:"searches"`
			}
			if err := json.Unmarshal(runIdentityJavaScript(t, js), &result); err != nil {
				t.Fatal(err)
			}
			if result.Searches != 1 {
				t.Fatalf("expected one official scoped search, got %d", result.Searches)
			}
			if scenario == "search-error" {
				if result.Error == "" {
					t.Fatal("failed candidate search silently accepted")
				}
				return
			}
			if result.Error != "" {
				t.Fatal(result.Error)
			}
			hit, err := resolveSchematicIdentityCompat(original, result.Value.Candidates, identityCompatSource())
			if scenario == "personal-shadow" {
				if err != nil || hit.UUID != "9f9c6cb41c7449fd8acf96aceed2661a" {
					t.Fatalf("official system asset lost behind personal lookup: %+v %v", hit, err)
				}
			} else if err == nil {
				t.Fatal("scoped search weakened exact C/MPN/native-source uniqueness")
			}
		})
	}
}
func TestSchematicIdentityCompatDoesNotProbeHealthyOrUnrequestedReads(t *testing.T) {
	for _, healthy := range []bool{false, true} {
		t.Run(fmt.Sprint(healthy), func(t *testing.T) {
			c, hit := identityCompatFixture()
			if healthy {
				c["device"] = map[string]any{"uuid": hit.UUID, "libraryUuid": hit.LibraryUUID}
				delete(c, "deviceIdentityError")
			}
			cfg, daemon, cleanup := newBlockApplyTestDaemon(t, func(call blockApplyTestCall) string {
				if call.Action != "schematic.components.list" {
					t.Fatalf("unexpected probe: %s", call.Action)
				}
				return identityCompatEnvelope(map[string]any{"components": []any{c}}, "page")
			})
			defer cleanup()
			if _, err := requestAction(cfg, "schematic.components.list", "w1", map[string]any{"includeDeviceIdentity": healthy}); err != nil {
				t.Fatal(err)
			}
			if len(daemon.snapshot()) != 1 {
				t.Fatal("unnecessary identity query")
			}
		})
	}
}
