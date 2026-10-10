package app

// Compatibility for old connectors that compare a placed 16-hex footprint
// handle with a 32-hex library footprint UUID. This is a read adapter shared by
// sch list and every Apply state guard; it never edits the schematic or an
// on-disk snapshot, and does not weaken comparisons between real library IDs.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

var schematicPlacedIdentityRE = regexp.MustCompile(`^[0-9a-fA-F]{16}$`)
var schematicLCSCIdentityRE = regexp.MustCompile(`^C[0-9]+$`)

// The old typed LCSC action neither accepts allowMultiMatch nor returns asset
// associations. Its single-result output cannot prove uniqueness. This fixed
// compatibility script calls only official read APIs, requests all matches and
// returns bounded identity fields (no full properties/geometry payloads).
func schematicIdentityProbeCode(ids, footprintIDs []string, project, document string, searches ...schematicIdentitySearch) string {
	b, _ := json.Marshal(ids)
	footprints, _ := json.Marshal(footprintIDs)
	if searches == nil {
		searches = []schematicIdentitySearch{}
	}
	queries, _ := json.Marshal(searches)
	scope, _ := json.Marshal(map[string]string{"projectUuid": project, "documentUuid": document})
	return schematicIdentityArchiveCode + `
const expectedContext = ` + string(scope) + `;
async function assertIdentityContext() {
 const project = await eda.dmt_Project.getCurrentProjectInfo();
 const document = await eda.dmt_SelectControl.getCurrentDocumentInfo();
 if (!project || !document || project.uuid !== expectedContext.projectUuid || document.uuid !== expectedContext.documentUuid) throw new Error("project/document changed during official identity reads");
}
await assertIdentityContext();
const ids = ` + string(b) + `;
const footprintIds = new Set(` + string(footprints) + `);
const nativeFootprints = []; let sourceError = "";
try {
 const sources = await eda.sys_FileManager.getDocumentFootprintSources();
 if (!Array.isArray(sources)) throw new Error("native footprint sources unavailable");
 for (const item of sources) {
  if (!item || !footprintIds.has(item.footprintUuid)) continue;
  if (typeof item.documentSource !== "string") { nativeFootprints.push({footprintUuid:item.footprintUuid,metadataLines:[]}); continue; }
  const metadataLines = item.documentSource.split(/\r?\n/).filter(line => /"type"\s*:\s*"(?:DOCHEAD|META)"/.test(line));
  nativeFootprints.push({footprintUuid:item.footprintUuid,sourceKind:"document-footprint-sources",metadataLines});
 }
} catch(error) { sourceError = String(error); }
const missingFootprints = new Set([...footprintIds].filter(id => !nativeFootprints.some(item => item.footprintUuid === id)));
if (missingFootprints.size) {
 try {
  await assertIdentityContext();
  const file = await eda.sys_FileManager.getProjectFile("identity-provenance", undefined, "epro2");
  await assertIdentityContext();
  const exported = await identityFootprintsFromProject(file, missingFootprints, expectedContext.documentUuid);
  nativeFootprints.push(...exported); sourceError = "";
 } catch(error) { sourceError = "official project footprint source unavailable: " + String(error); }
}
const text = x => typeof x === "string" ? x : "";
const hits = ids.length ? await eda.lib_Device.getByLcscIds(ids, undefined, true) : [];
if (!Array.isArray(hits) || hits.length > 128) throw new Error("identity lookup is missing or exceeds 128 candidates");
const searches = ` + string(queries) + `;
for (const query of searches) {
 await assertIdentityContext();
 const found = await eda.lib_Device.search(query.query, query.libraryUuid);
 if (!Array.isArray(found) || found.length > 128) throw new Error("scoped identity search is missing or exceeds 128 candidates");
 for (const hit of found) {
  if (hit && text(hit.libraryUuid) === query.libraryUuid) hits.push(hit);
 }
}
const candidates = [];
const seen = new Set();
for (const hit of hits) {
 const key = text(hit.libraryUuid) + "/" + text(hit.uuid);
 if (seen.has(key)) continue;
 seen.add(key);
 if (seen.size > 128) throw new Error("merged identity lookup exceeds 128 candidates");
 const f = hit.footprint && typeof hit.footprint === "object" ? hit.footprint : {uuid:hit.footprintUuid,libraryUuid:hit.footprintLibraryUuid,name:hit.footprintName};
 const out = {uuid:text(hit.uuid),libraryUuid:text(hit.libraryUuid),supplierId:text(hit.supplierId),manufacturerId:text(hit.manufacturerId),name:text(hit.name),footprint:{uuid:text(f.uuid),libraryUuid:text(f.libraryUuid),name:text(f.name)}};
 try {
  const d = await eda.lib_Device.get(out.uuid,out.libraryUuid);
  if (!d) throw new Error("library Device.get returned no identity");
  const a = d.association || {}, p = d.property || {}, af = a.footprint || {};
  out.device = {uuid:text(d.uuid),libraryUuid:text(d.libraryUuid),name:text(d.name),supplierId:text(p.supplierId),manufacturerId:text(p.manufacturerId),footprint:{uuid:text(af.uuid),libraryUuid:text(af.libraryUuid)}};
  // Search rows may omit properties or associations. Only the fresh official
  // detail supplies missing fields; nonempty search/detail conflicts survive.
  if (!out.supplierId) out.supplierId = out.device.supplierId;
  if (!out.manufacturerId) out.manufacturerId = out.device.manufacturerId;
  if (!out.footprint.uuid) out.footprint.uuid = out.device.footprint.uuid;
  if (!out.footprint.libraryUuid) out.footprint.libraryUuid = out.device.footprint.libraryUuid;
 } catch(error) { out.error = String(error); }
 candidates.push(out);
}
await assertIdentityContext();
return {schemaVersion:1,query:{lcscIds:ids,allowMultiMatch:true,searches},candidates,nativeFootprints,sourceError};`
}

type schematicIdentitySearch struct {
	Query       string `json:"query"`
	LibraryUUID string `json:"libraryUuid"`
}

// The placed instance tells us which library to search. A bare C-number lookup
// can be shadowed by personal copies, even with allowMultiMatch=true. These
// searches add candidates; they never authorize a name or footprint guess.
func schematicIdentitySearches(components []map[string]any) []schematicIdentitySearch {
	unique := map[schematicIdentitySearch]bool{}
	for _, c := range components {
		query := schematicIdentityString(c, "manufacturerId")
		if query == "" {
			query = schematicStableDeviceName(c)
		}
		library := schematicIdentityString(schematicIdentityMap(c, "device"), "libraryUuid")
		if query != "" && library != "" {
			unique[schematicIdentitySearch{Query: query, LibraryUUID: library}] = true
		}
	}
	result := make([]schematicIdentitySearch, 0, len(unique))
	for query := range unique {
		result = append(result, query)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].LibraryUUID != result[j].LibraryUUID {
			return result[i].LibraryUUID < result[j].LibraryUUID
		}
		return result[i].Query < result[j].Query
	})
	return result
}

type schematicIdentityAsset struct {
	UUID        string `json:"uuid"`
	LibraryUUID string `json:"libraryUuid"`
	Name        string `json:"name,omitempty"`
}
type schematicIdentityDevice struct {
	UUID           string                 `json:"uuid"`
	LibraryUUID    string                 `json:"libraryUuid"`
	Name           string                 `json:"name"`
	SupplierID     string                 `json:"supplierId"`
	ManufacturerID string                 `json:"manufacturerId"`
	Footprint      schematicIdentityAsset `json:"footprint"`
}
type schematicIdentityCandidate struct {
	UUID           string                   `json:"uuid"`
	LibraryUUID    string                   `json:"libraryUuid"`
	SupplierID     string                   `json:"supplierId"`
	ManufacturerID string                   `json:"manufacturerId"`
	Name           string                   `json:"name"`
	Footprint      schematicIdentityAsset   `json:"footprint"`
	Device         *schematicIdentityDevice `json:"device,omitempty"`
	Error          string                   `json:"error,omitempty"`
}
type schematicNativeFootprint struct {
	FootprintUUID string   `json:"footprintUuid"`
	MetadataLines []string `json:"metadataLines"`
	SourceKind    string   `json:"sourceKind,omitempty"`
}

type schematicIdentityProbe struct {
	SchemaVersion int `json:"schemaVersion"`
	Query         struct {
		LCSCIDs         []string                  `json:"lcscIds"`
		AllowMultiMatch bool                      `json:"allowMultiMatch"`
		Searches        []schematicIdentitySearch `json:"searches"`
	} `json:"query"`
	Candidates       []schematicIdentityCandidate `json:"candidates"`
	NativeFootprints []schematicNativeFootprint   `json:"nativeFootprints"`
	SourceError      string                       `json:"sourceError,omitempty"`
}

func schematicIdentityString(m map[string]any, key string) string { v, _ := m[key].(string); return v }
func schematicIdentityMap(m map[string]any, key string) map[string]any {
	v, _ := m[key].(map[string]any)
	return v
}
func needsSchematicIdentityCompat(c map[string]any) bool {
	if schematicIdentityString(c, "componentType") != "part" {
		return false
	}
	device, part, fp := schematicIdentityMap(c, "device"), schematicIdentityMap(c, "component"), schematicIdentityMap(c, "footprint")
	supplier := schematicIdentityString(c, "supplierId")
	model := schematicIdentityString(c, "manufacturerId")
	// Personal library assets need not have a supplier C-number. They still
	// need an exact searchable model/name and a fixed placed device library.
	identityKey := schematicLCSCIdentityRE.MatchString(supplier) ||
		(supplier == "" && (model != "" || schematicStableDeviceName(c) != "") &&
			strings.TrimSpace(schematicIdentityString(device, "libraryUuid")) != "")
	return schematicPlacedIdentityRE.MatchString(schematicIdentityString(device, "uuid")) &&
		schematicPlacedIdentityRE.MatchString(schematicIdentityString(part, "uuid")) &&
		schematicIdentityString(device, "uuid") == schematicIdentityString(part, "uuid") &&
		schematicPlacedIdentityRE.MatchString(schematicIdentityString(fp, "uuid")) &&
		strings.TrimSpace(schematicIdentityString(fp, "libraryUuid")) != "" && identityKey
}

func schematicStableDeviceName(c map[string]any) string {
	for _, name := range []string{schematicIdentityString(c, "name"), schematicIdentityString(schematicIdentityMap(c, "component"), "name")} {
		if name != "" && !strings.HasPrefix(name, "={") {
			return name
		}
	}
	return ""
}

// Parse only the authoritative owner header and metadata emitted by the native
// footprint source API. Names never substitute for a missing/conflicting source.
func schematicNativeFootprintIdentity(instance string, entries []schematicNativeFootprint) (schematicIdentityAsset, error) {
	var zero schematicIdentityAsset
	var target *schematicNativeFootprint
	for i := range entries {
		if entries[i].FootprintUUID == instance {
			if target != nil {
				return zero, fmt.Errorf("duplicate native footprint source for %s", instance)
			}
			target = &entries[i]
		}
	}
	if target == nil {
		return zero, fmt.Errorf("native footprint source unavailable for %s", instance)
	}
	headers, metadata := 0, 0
	var source schematicIdentityAsset
	for _, line := range target.MetadataLines {
		left, right, ok := strings.Cut(strings.TrimSpace(line), "||")
		if !ok {
			return zero, fmt.Errorf("malformed native footprint metadata record")
		}
		right = strings.TrimSuffix(right, "|")
		var header struct {
			Type string `json:"type"`
		}
		var data map[string]any
		if json.Unmarshal([]byte(left), &header) != nil || json.Unmarshal([]byte(right), &data) != nil {
			return zero, fmt.Errorf("invalid JSON in native footprint metadata")
		}
		switch header.Type {
		case "DOCHEAD":
			headers++
			if headers != 1 || metadata != 0 || schematicIdentityString(data, "docType") != "FOOTPRINT" || schematicIdentityString(data, "uuid") != instance {
				return zero, fmt.Errorf("native footprint owner header disagrees with instance %s", instance)
			}
		case "META":
			metadata++
			if headers != 1 || metadata != 1 {
				return zero, fmt.Errorf("native footprint needs one ordered META source")
			}
			parts := strings.Split(schematicIdentityString(data, "source"), "|")
			if len(parts) != 2 || !isDeviceLibraryUUID(parts[0]) || parts[1] == "" || strings.ContainsAny(parts[1], " \t\r\n") {
				return zero, fmt.Errorf("native footprint has no exact source asset/library")
			}
			source = schematicIdentityAsset{UUID: parts[0], LibraryUUID: parts[1], Name: schematicIdentityString(data, "title")}
		default:
			return zero, fmt.Errorf("unexpected native footprint metadata record %q", header.Type)
		}
	}
	if headers != 1 || metadata != 1 {
		return zero, fmt.Errorf("native footprint source needs complete owner header and META")
	}
	return source, nil
}

func resolveSchematicIdentityCompat(c map[string]any, candidates []schematicIdentityCandidate, source schematicIdentityAsset) (schematicIdentityCandidate, error) {
	var zero schematicIdentityCandidate
	if !needsSchematicIdentityCompat(c) {
		return zero, fmt.Errorf("not a proven placed-instance identity domain")
	}
	fp := schematicIdentityMap(c, "footprint")
	if !isDeviceLibraryUUID(source.UUID) || source.LibraryUUID != schematicIdentityString(fp, "libraryUuid") {
		return zero, fmt.Errorf("native footprint source/library proof is unavailable or disagrees")
	}
	lcsc := schematicIdentityString(c, "supplierId")
	mpn := schematicIdentityString(c, "manufacturerId")
	name := schematicStableDeviceName(c)
	placedLibrary := schematicIdentityString(schematicIdentityMap(c, "device"), "libraryUuid")
	label := lcsc
	if label == "" {
		label = mpn
		if label == "" {
			label = name
		}
	}
	var matches []schematicIdentityCandidate
	seen := map[string]bool{}
	for _, hit := range candidates {
		if hit.SupplierID != lcsc {
			continue
		}
		if lcsc == "" && hit.LibraryUUID != placedLibrary {
			continue
		}
		if hit.Error != "" || hit.Device == nil {
			return zero, fmt.Errorf("complete candidate identity unavailable for %s", label)
		}
		d := hit.Device
		if !isDeviceLibraryUUID(hit.UUID) || strings.TrimSpace(hit.LibraryUUID) == "" || d.UUID != hit.UUID || d.LibraryUUID != hit.LibraryUUID || d.SupplierID != lcsc {
			return zero, fmt.Errorf("candidate library identity or exact supplier proof disagrees for %s", label)
		}
		if mpn != "" {
			if hit.ManufacturerID != mpn || d.ManufacturerID != mpn {
				continue
			}
		} else {
			if name == "" || hit.Name != name || d.Name != name {
				continue
			}
		}
		if !isDeviceLibraryUUID(hit.Footprint.UUID) || hit.Footprint.UUID != source.UUID || d.Footprint.UUID != source.UUID || d.Footprint.LibraryUUID != source.LibraryUUID {
			continue
		}
		if hit.Footprint.LibraryUUID != "" && hit.Footprint.LibraryUUID != d.Footprint.LibraryUUID {
			continue
		}
		key := hit.LibraryUUID + "/" + hit.UUID
		if seen[key] {
			continue
		} // repeated rows do not create another asset identity
		seen[key] = true
		matches = append(matches, hit)
	}
	if len(matches) != 1 {
		return zero, fmt.Errorf("%s has %d exact supplier/model/native-footprint-source candidates; requires exactly one", label, len(matches))
	}
	return matches[0], nil
}

// Only a completed compatibility read may separate obsolete connector
// diagnostics from stable identity evidence. Keep the resolved device, native
// instance, source asset/library and official association in the scene.
func schematicIdentityCompatSceneProof(c map[string]any) bool {
	if _, present := c["deviceIdentityError"]; present {
		return false
	}
	if _, present := c["deviceIdentityCompatibilityError"]; present {
		return false
	}
	r := schematicIdentityMap(c, "deviceResolution")
	if r["resolver"] != "cli-legacy-placed-footprint-v1" || r["sameFootprintUUID"] != true {
		return false
	}
	supplier := schematicIdentityString(c, "supplierId")
	if supplier == "" {
		if r["via"] != "scoped-library-footprint-source" {
			return false
		}
	} else if !schematicLCSCIdentityRE.MatchString(supplier) || r["via"] != "lcsc-footprint-source" || r["lcsc"] != supplier {
		return false
	}
	d, fp := schematicIdentityMap(c, "device"), schematicIdentityMap(c, "footprint")
	s, association := schematicIdentityMap(r, "footprintSource"), schematicIdentityMap(r, "libraryFootprint")
	return isDeviceLibraryUUID(schematicIdentityString(d, "uuid")) && schematicIdentityString(d, "libraryUuid") != "" &&
		schematicPlacedIdentityRE.MatchString(schematicIdentityString(fp, "uuid")) &&
		schematicIdentityString(s, "instanceUuid") == schematicIdentityString(fp, "uuid") &&
		isDeviceLibraryUUID(schematicIdentityString(s, "uuid")) &&
		schematicIdentityString(s, "libraryUuid") != "" && schematicIdentityString(s, "libraryUuid") == schematicIdentityString(fp, "libraryUuid") &&
		schematicIdentityString(association, "uuid") == schematicIdentityString(s, "uuid") &&
		schematicIdentityString(association, "libraryUuid") == schematicIdentityString(s, "libraryUuid")
}

func schematicIdentityCompatStableScene(c map[string]any) {
	if !schematicIdentityCompatSceneProof(c) {
		return
	}
	delete(c, "deviceIdentityCandidates")
	resolution := schematicIdentityMap(c, "deviceResolution")
	stable := make(map[string]any, len(resolution))
	for key, value := range resolution {
		if key != "probeId" && key != "connectorError" {
			stable[key] = value
		}
	}
	c["deviceResolution"] = stable
}

// Called once at the shared postAction choke point. Library reads never recurse
// into this path. Original daemon context/sequence/error evidence is preserved.
func hydrateSchematicIdentityCompatibility(cfg *appConfig, action, window string, payload any, body []byte, timeout time.Duration) ([]byte, error) {
	if action != "schematic.components.list" {
		return body, nil
	}
	var requested struct {
		IncludeDeviceIdentity bool `json:"includeDeviceIdentity"`
	}
	encoded, err := json.Marshal(payload)
	if err != nil || json.Unmarshal(encoded, &requested) != nil || !requested.IncludeDeviceIdentity {
		return body, nil
	}
	var envelope map[string]any
	if err := json.Unmarshal(body, &envelope); err != nil || envelope["ok"] != true {
		return body, nil
	}
	result := schematicIdentityMap(envelope, "result")
	rows, _ := result["components"].([]any)
	var needing []map[string]any
	ids := map[string]bool{}
	for _, item := range rows {
		c, ok := item.(map[string]any)
		if ok && needsSchematicIdentityCompat(c) {
			needing = append(needing, c)
			if supplier := schematicIdentityString(c, "supplierId"); supplier != "" {
				ids[supplier] = true
			}
		}
	}
	if len(needing) == 0 {
		return body, nil
	}
	fail := func(reason string) ([]byte, error) {
		for _, c := range needing {
			c["deviceIdentityCompatibilityError"] = reason
		}
		return json.Marshal(envelope)
	}
	if dispatchDryRunActive() {
		return fail("legacy identity lookup is unavailable in dry-run; no debug action was dispatched")
	}
	context := schematicIdentityMap(envelope, "context")
	project, doc := schematicIdentityString(context, "projectUuid"), schematicIdentityString(context, "documentUuid")
	if project == "" || doc == "" {
		return fail("legacy identity lookup requires a fixed project/document response context")
	}
	footprintIDs := []string{}
	seenFootprints := map[string]bool{}
	for _, c := range needing {
		id := schematicIdentityString(schematicIdentityMap(c, "footprint"), "uuid")
		if !seenFootprints[id] {
			seenFootprints[id] = true
			footprintIDs = append(footprintIDs, id)
		}
	}
	sort.Strings(footprintIDs)
	ordered := make([]string, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	searches := schematicIdentitySearches(needing)
	if len(ordered) > 64 {
		return fail("legacy identity lookup exceeds 64 distinct C-numbers; upgrade connector or read a smaller page")
	}
	if len(searches) > 128 {
		return fail("legacy identity lookup exceeds 128 scoped model queries; read a smaller page")
	}
	// Copy the routing configuration instead of changing the caller's project or
	// document selection. The response context must still agree after the probe.
	scoped := *cfg
	scoped.project = project
	scoped.doc = "" // library reads need no navigation; reject context drift after the probe
	probeResult, err := requestActionTimed(&scoped, "debug.exec_js", window, map[string]any{"code": schematicIdentityProbeCode(ordered, footprintIDs, project, doc, searches...)}, timeout)
	if err != nil {
		return fail("legacy identity lookup failed: " + err.Error())
	}
	if probeResult.Context == nil || probeResult.Context.ProjectUUID != project || probeResult.Context.DocumentUUID != doc {
		return fail("project/document changed during identity lookup")
	}
	raw, err := json.Marshal(probeResult.Result["value"])
	if err != nil {
		return fail("invalid identity proof")
	}
	var proof schematicIdentityProbe
	if err = json.Unmarshal(raw, &proof); err != nil {
		return fail("invalid identity proof: " + err.Error())
	}
	if proof.SchemaVersion != 1 || !proof.Query.AllowMultiMatch || len(proof.Query.LCSCIDs) != len(ordered) || proof.Candidates == nil || len(proof.Candidates) > 128 {
		return fail("incomplete all-candidate identity proof")
	}
	actual := append([]string(nil), proof.Query.LCSCIDs...)
	sort.Strings(actual)
	for i, id := range ordered {
		if actual[i] != id {
			return fail("identity proof query differs from requested C-numbers")
		}
	}
	if len(proof.Query.Searches) != len(searches) {
		return fail("identity proof scoped searches differ from requested model/library queries")
	}
	for i, query := range searches {
		if proof.Query.Searches[i] != query {
			return fail("identity proof scoped searches differ from requested model/library queries")
		}
	}
	for _, c := range needing {
		instanceFootprintUUID := schematicIdentityString(schematicIdentityMap(c, "footprint"), "uuid")
		native, err := schematicNativeFootprintIdentity(instanceFootprintUUID, proof.NativeFootprints)
		if proof.SourceError != "" {
			err = fmt.Errorf("native footprint source query failed: %s", proof.SourceError)
		}
		if err != nil {
			c["deviceIdentityCompatibilityError"] = err.Error()
			continue
		}
		hit, err := resolveSchematicIdentityCompat(c, proof.Candidates, native)
		if err != nil {
			c["deviceIdentityCompatibilityError"] = err.Error()
			continue
		}
		original := c["device"]
		previousError := c["deviceIdentityError"]
		c["placedDevice"] = original
		c["device"] = map[string]any{"uuid": hit.UUID, "libraryUuid": hit.LibraryUUID, "name": schematicIdentityString(c, "name")}
		resolution := map[string]any{"via": "lcsc-footprint-source", "resolver": "cli-legacy-placed-footprint-v1", "lcsc": hit.SupplierID, "footprint": hit.Footprint.Name, "instanceFootprint": c["footprint"], "libraryFootprint": hit.Device.Footprint, "footprintSource": map[string]string{"instanceUuid": instanceFootprintUUID, "uuid": native.UUID, "libraryUuid": native.LibraryUUID}, "sameFootprintUUID": true, "proof": "all LCSC candidates; exact MPN or stable device name; native footprint META.source matches official Device.get asset association", "connectorError": previousError, "probeId": probeResult.ID}
		if hit.SupplierID == "" {
			resolution["via"] = "scoped-library-footprint-source"
			resolution["proof"] = "all scoped library candidates; exact MPN or stable device name; native footprint META.source matches official Device.get asset association"
			delete(resolution, "lcsc")
		}
		for _, entry := range proof.NativeFootprints {
			if entry.FootprintUUID == instanceFootprintUUID && entry.SourceKind != "" {
				resolution["sourceKind"] = entry.SourceKind
			}
		}
		c["deviceResolution"] = resolution
		delete(c, "deviceIdentityError")
		delete(c, "deviceIdentityCompatibilityError")
	}
	return json.Marshal(envelope)
}
