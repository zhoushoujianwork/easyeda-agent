package app

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zhoushoujianwork/easyeda-agent/internal/connectivity"
)

// A designator repair is a same-instance operation. The original list envelope
// remains the authority for everything except the reference and agent bindings.
type schDesignatorBaseline struct {
	Context actionContext
	Result  map[string]any
	Parts   map[string]map[string]any // primitive ID -> complete observed record
	IDs     map[string]string         // primitive ID -> canonical component ID
}

func parseSchDesignatorBaseline(raw []byte, allowUnwired ...bool) (schDesignatorBaseline, error) {
	// Runtime actionResult.Seq is a compound internal struct; wire envelopes
	// contain a numeric seq. Decode only the public evidence needed here.
	var envelope struct {
		OK      bool           `json:"ok"`
		Context *actionContext `json:"context"`
		Result  map[string]any `json:"result"`
	}
	var out schDesignatorBaseline
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return out, err
	}
	if !envelope.OK || envelope.Context == nil || envelope.Context.ProjectUUID == "" || envelope.Context.DocumentUUID == "" || envelope.Context.DocumentType != "schematic" {
		return out, fmt.Errorf("successful sch list envelope with schematic project/document context required")
	}
	out.Context, out.Result = *envelope.Context, envelope.Result
	verifiedUnwired := len(allowUnwired) > 0 && allowUnwired[0] && schComposeFullyUnwired(envelope.Result)
	out.Parts, out.IDs = map[string]map[string]any{}, map[string]string{}
	summary, ok := envelope.Result["connectivitySummary"].(map[string]any)
	if !ok || summary["scope"] != "activePage" {
		return out, fmt.Errorf("active-page connectivitySummary missing; capture sch list --include-pins --include-wires --include-device-identity")
	}
	for _, key := range []string{"wires", "buses", "netflags", "netports", "netlabels", "shortSymbols"} {
		v, ok := finiteFloat(summary[key])
		if !ok || v < 0 || v != float64(int(v)) {
			return out, fmt.Errorf("connectivitySummary.%s unavailable", key)
		}
	}
	wires, ok := envelope.Result["wires"].([]any)
	if !ok || (summary["wires"].(float64) > 0 && len(wires) == 0) {
		return out, fmt.Errorf("wire geometry inventory unavailable")
	}
	for _, w := range wires {
		m, ok := w.(map[string]any)
		if !ok {
			return out, fmt.Errorf("invalid wire geometry")
		}
		for _, key := range []string{"x0", "y0", "x1", "y1"} {
			if _, ok := finiteFloat(m[key]); !ok {
				return out, fmt.Errorf("wire %s unavailable", key)
			}
		}
		if _, ok := m["net"].(string); !ok {
			return out, fmt.Errorf("wire net unavailable")
		}
	}
	components, ok := envelope.Result["components"].([]any)
	if !ok {
		return out, fmt.Errorf("component inventory unavailable")
	}
	seen, canonical := map[string]bool{}, map[string]bool{}
	for _, item := range components {
		c, ok := item.(map[string]any)
		if !ok {
			return out, fmt.Errorf("invalid component record")
		}
		pid := stringVal(c["primitiveId"])
		if pid == "" || seen[pid] {
			return out, fmt.Errorf("missing/duplicate primitive ID %q", pid)
		}
		seen[pid] = true
		if stringVal(c["componentType"]) == "" {
			return out, fmt.Errorf("%s: component type unavailable", pid)
		}
		if c["componentType"] != "part" {
			continue
		}
		id, err := schDesignatorComponentID(c)
		if err != nil {
			return out, err
		}
		if canonical[id] {
			return out, fmt.Errorf("duplicate component binding %s", id)
		}
		canonical[id] = true
		if _, ok := c["uniqueId"].(string); !ok || (stringVal(c["uniqueId"]) == "" && !verifiedUnwired) {
			return out, fmt.Errorf("%s: uniqueId unavailable", pid)
		}
		for _, key := range []string{"x", "y", "rotation"} {
			if _, ok := finiteFloat(c[key]); !ok {
				return out, fmt.Errorf("%s: %s unavailable", pid, key)
			}
		}
		if _, ok := c["mirror"].(bool); !ok {
			return out, fmt.Errorf("%s: mirror unavailable", pid)
		}
		dev, ok := c["device"].(map[string]any)
		if !ok || len(stringVal(dev["uuid"])) != 32 || stringVal(dev["libraryUuid"]) == "" || c["deviceIdentityError"] != nil || c["deviceIdentityCompatibilityError"] != nil {
			return out, fmt.Errorf("%s: exact official device/library identity unavailable", pid)
		}
		if c["pinsAvailable"] != true || c["netAmbiguous"] == true {
			return out, fmt.Errorf("%s: unambiguous pin inventory unavailable", pid)
		}
		// Pin geometry being proven is not pin→NET being proven: a muted netlist
		// export leaves every pin's `net` null while pinsAvailable stays true, and this
		// plan must not bind a designator off a null that only means "could not read".
		if !verifiedUnwired && (c["netlistAvailable"] != true || c["netlistError"] != nil) {
			return out, fmt.Errorf("%s: pin→net attribution unavailable (netlist export failed, so pin nets are null rather than absent)", pid)
		}
		pins, ok := c["pins"].([]any)
		if !ok || len(pins) == 0 {
			return out, fmt.Errorf("%s: pin inventory unavailable", pid)
		}
		for _, item := range pins {
			p, ok := item.(map[string]any)
			if !ok {
				return out, fmt.Errorf("%s: invalid pin", pid)
			}
			if _, ok := p["net"].(string); !ok && !(verifiedUnwired && p["net"] == nil) {
				return out, fmt.Errorf("%s.%s: net evidence unavailable", pid, schDesignatorPinNumber(p))
			}
			if _, ok := p["noConnected"].(bool); !ok {
				return out, fmt.Errorf("%s: NC evidence unavailable", pid)
			}
			for _, key := range []string{"x", "y"} {
				if _, ok := finiteFloat(p[key]); !ok {
					return out, fmt.Errorf("%s: pin %s unavailable", pid, key)
				}
			}
		}
		out.Parts[pid], out.IDs[pid] = c, id
	}
	if len(out.Parts) == 0 {
		return out, fmt.Errorf("no placed parts in baseline")
	}
	if _, err := connectivity.FromRead(out.Result); err != nil {
		return out, err
	}
	return out, nil
}

func schDesignatorPinNumber(p map[string]any) string {
	if s := stringVal(p["pinNumber"]); s != "" {
		return s
	}
	return stringVal(p["number"])
}

func schDesignatorComponentID(c map[string]any) (string, error) {
	ref := stringVal(c["designator"])
	if ref == "" {
		return "", fmt.Errorf("part designator unavailable")
	}
	if props, ok := c["otherProperty"].(map[string]any); ok {
		if raw, bound := props[connectivity.ComponentIDProperty]; bound {
			if id, ok := raw.(string); ok && strings.TrimSpace(id) != "" {
				return id, nil
			}
			return "", fmt.Errorf("%s: invalid canonical component binding", ref)
		}
	}
	return "cmp-" + ref, nil
}

func schDesignatorTargets(before schDesignatorBaseline, target connectivity.Document) (map[string]connectivity.Component, error) {
	if err := connectivity.ValidatePlacementDesignators(target); err != nil {
		return nil, err
	}
	// A merged full-project target can retain its originating page's documentId.
	// The fresh baseline pins the repair page; project identity must still match.
	if target.ProjectID != before.Context.ProjectUUID {
		return nil, fmt.Errorf("target project does not match baseline")
	}
	byID, refs := map[string]connectivity.Component{}, map[string]string{}
	for _, c := range target.Components {
		key := strings.ToUpper(c.Ref)
		if owner, found := refs[key]; found {
			return nil, fmt.Errorf("target ref %s conflicts between %s and %s", c.Ref, owner, c.ID)
		}
		byID[c.ID], refs[key] = c, c.ID
	}
	names := map[string]string{}
	for _, n := range target.Nets {
		names[n.ID] = n.Name
	}
	nets := map[[2]string]string{}
	for _, c := range target.Connections {
		nets[[2]string{c.ComponentID, c.PinNumber}] = names[c.NetID]
	}
	for pid, old := range before.Parts {
		id := before.IDs[pid]
		c, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("%s: placed component %s missing from full-project target", pid, id)
		}
		dev := old["device"].(map[string]any)
		if c.Device.UUID != stringVal(dev["uuid"]) || c.Device.LibraryUUID != stringVal(dev["libraryUuid"]) {
			return nil, fmt.Errorf("%s: target changes official library identity", id)
		}
		pins := map[string]connectivity.Pin{}
		for _, p := range c.Pins {
			pins[p.Number] = p
		}
		observed := old["pins"].([]any)
		if len(pins) != len(observed) {
			return nil, fmt.Errorf("%s: target changes pin inventory", id)
		}
		for _, item := range observed {
			p := item.(map[string]any)
			number := schDesignatorPinNumber(p)
			expected, ok := pins[number]
			name := stringVal(p["pinName"])
			if name == "" {
				name = stringVal(p["name"])
			}
			if !ok || expected.Name != name || expected.NoConnected != p["noConnected"].(bool) || nets[[2]string{id, number}] != p["net"].(string) {
				return nil, fmt.Errorf("%s.%s: target changes pin name/net/NC; designator repair cannot change connectivity", id, number)
			}
		}
	}
	return byID, nil
}

func buildSchDesignatorsPlan(before schDesignatorBaseline, target connectivity.Document, beforePath, targetPath, beforeSHA, targetSHA string) (*playbook, error) {
	byID, err := schDesignatorTargets(before, target)
	if err != nil {
		return nil, err
	}
	zero, stop := 0, false
	pb := &playbook{Version: 1, RequireFullExecution: true, Meta: playbookMeta{Name: "Repair schematic designators and stable bindings", Project: before.Context.ProjectUUID, Doc: before.Context.DocumentUUID}, Defaults: stepPolicy{Retry: &zero, ContinueOnError: &stop}}
	guard := func(phase string) playbookStep {
		flags := map[string]any{"before": beforePath, "target": targetPath, "phase": phase, "before-sha": beforeSHA, "target-sha": targetSHA}
		if phase == "after" {
			flags["sync-groups"] = true
		}
		return playbookStep{ID: "verify-" + phase, Run: "sch designators verify", Flags: flags}
	}
	pb.Steps = append(pb.Steps, guard("before"))
	// Follow the canonical document's declaration order; API enumeration order
	// must never alter numbering or the queued repair order.
	pids := map[string]string{}
	for pid, id := range before.IDs {
		pids[id] = pid
	}
	for _, c := range target.Components {
		pid, found := pids[c.ID]
		if !found {
			continue
		}
		old := before.Parts[pid]
		props, _ := old["otherProperty"].(map[string]any)
		if stringVal(old["designator"]) == c.Ref && props[connectivity.ComponentIDProperty] == c.ID && (c.Role == "" || props[connectivity.ComponentRoleProperty] == c.Role) {
			continue
		}
		attributes := map[string]any{connectivity.ComponentIDProperty: c.ID}
		if byID[c.ID].Role != "" {
			attributes[connectivity.ComponentRoleProperty] = c.Role
		}
		pb.Steps = append(pb.Steps, playbookStep{ID: fmt.Sprintf("bind-%03d", len(pb.Steps)), Action: "schematic.component.modify", Payload: map[string]any{"primitiveId": pid, "patch": map[string]any{"designator": c.Ref, "customAttributes": attributes}}})
	}
	pb.Steps = append(pb.Steps, guard("after"), playbookStep{ID: "save", Action: "schematic.save"})
	return pb, nil
}

func newSchDesignatorsPlanCmd(stdout io.Writer) *cobra.Command {
	var beforePath, outPath string
	c := &cobra.Command{Use: "plan <target.json>", Short: "Generate a guarded same-page renumbering sch apply queue (offline)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if beforePath == "" {
			return fmt.Errorf("--before is required")
		}
		if err := schDesignatorsDistinctFiles(beforePath, args[0], outPath); err != nil {
			return err
		}
		beforeAbs, err := filepath.Abs(beforePath)
		if err != nil {
			return err
		}
		targetAbs, err := filepath.Abs(args[0])
		if err != nil {
			return err
		}
		beforeRaw, err := os.ReadFile(beforeAbs)
		if err != nil {
			return err
		}
		targetRaw, err := os.ReadFile(targetAbs)
		if err != nil {
			return err
		}
		before, err := parseSchDesignatorBaseline(beforeRaw)
		if err != nil {
			return err
		}
		var target connectivity.Document
		if err = json.Unmarshal(targetRaw, &target); err != nil {
			return err
		}
		pb, err := buildSchDesignatorsPlan(before, target, beforeAbs, targetAbs, sha256Hex(beforeRaw), sha256Hex(targetRaw))
		if err != nil {
			return err
		}
		return schDesignatorsWriteJSON(outPath, pb, stdout)
	}}
	c.Flags().StringVar(&beforePath, "before", "", "fresh sch list envelope with pins, wires and official device identity (required)")
	c.Flags().StringVar(&outPath, "out", "", "write guarded playbook (default: stdout)")
	return c
}

// Compare all persistent instance data, irrespective of enumeration order.
// Normalize completed independent identity proofs; the rendered part bbox and
// the two intentionally changed fields are not persistent scene differences.
func schDesignatorScene(result map[string]any) (map[string]any, error) {
	out := map[string]any{}
	components, ok := result["components"].([]any)
	if !ok {
		return nil, fmt.Errorf("components unavailable")
	}
	byID := map[string]any{}
	for _, item := range components {
		c, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid component")
		}
		copy := maps.Clone(c)
		if c["componentType"] == "sheet" {
			// The editor updates these timestamps on autosave. All other sheet
			// data, including paper bounds and title text, remains protected.
			if props, ok := copy["otherProperty"].(map[string]any); ok {
				props = maps.Clone(props)
				delete(props, "@Update Date")
				delete(props, "@Update Time")
				copy["otherProperty"] = props
			}
		}
		if c["componentType"] == "part" {
			delete(copy, "bbox")
			// An exact compatibility proof supersedes the old connector's
			// unordered rejected candidate diagnostics, not its native assets.
			schematicIdentityCompatStableScene(copy)
			pins, ok := copy["pins"].([]any)
			if !ok {
				return nil, fmt.Errorf("pins unavailable")
			}
			pinMap := map[string]any{}
			for _, item := range pins {
				p, ok := item.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("invalid pin")
				}
				n := schDesignatorPinNumber(p)
				if n == "" || pinMap[n] != nil {
					return nil, fmt.Errorf("invalid/duplicate pin %s", n)
				}
				pinMap[n] = p
			}
			copy["pins"] = pinMap
		}
		id := stringVal(c["primitiveId"])
		if id == "" || byID[id] != nil {
			return nil, fmt.Errorf("invalid/duplicate primitive %s", id)
		}
		byID[id] = copy
	}
	out["components"] = byID
	wires, ok := result["wires"].([]any)
	if !ok {
		return nil, fmt.Errorf("wires unavailable")
	}
	encoded := make([]string, 0, len(wires))
	for _, w := range wires {
		b, err := json.Marshal(w)
		if err != nil {
			return nil, err
		}
		encoded = append(encoded, string(b))
	}
	sort.Strings(encoded)
	// Source scenes also travel through playbook JSON. Keep the array in the
	// same JSON-native form produced by Unmarshal, so unchanged wires compare
	// equally before and after saving/reloading the queue (including no wires).
	wireValues := make([]any, len(encoded))
	for i, wire := range encoded {
		wireValues[i] = wire
	}
	out["wires"], out["connectivitySummary"] = wireValues, result["connectivitySummary"]
	if page, present := result["pagePrimitives"]; present {
		out["pagePrimitives"] = page
	}
	return out, nil
}

func verifySchDesignatorScene(before schDesignatorBaseline, target connectivity.Document, live schDesignatorBaseline, phase string) error {
	if phase != "before" && phase != "after" {
		return fmt.Errorf("phase must be before or after")
	}
	byID, err := schDesignatorTargets(before, target)
	if err != nil {
		return err
	}
	if live.Context.ProjectUUID != before.Context.ProjectUUID || live.Context.DocumentUUID != before.Context.DocumentUUID {
		return fmt.Errorf("live project/document differs from repair baseline")
	}
	expected, err := schDesignatorScene(before.Result)
	if err != nil {
		return err
	}
	if phase == "after" {
		cs := expected["components"].(map[string]any)
		for pid, id := range before.IDs {
			c := cs[pid].(map[string]any)
			next := byID[id]
			c["designator"] = next.Ref
			props, _ := c["otherProperty"].(map[string]any)
			props = maps.Clone(props)
			if props == nil {
				props = map[string]any{}
			}
			props[connectivity.ComponentIDProperty] = id
			if next.Role != "" {
				props[connectivity.ComponentRoleProperty] = next.Role
			}
			c["otherProperty"] = props
		}
	}
	actual, err := schDesignatorScene(live.Result)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(expected, actual) {
		want, _ := json.Marshal(expected)
		got, _ := json.Marshal(actual)
		return fmt.Errorf("%s verification failed: %s (expected SHA %s, observed SHA %s)", phase, schDesignatorFirstDifference("scene", expected, actual), sha256Hex(want), sha256Hex(got))
	}
	return nil
}

func schDesignatorFirstDifference(path string, expected, observed any) string {
	if reflect.DeepEqual(expected, observed) {
		return ""
	}
	if a, ok := expected.(map[string]any); ok {
		if b, ok := observed.(map[string]any); ok {
			keys := map[string]bool{}
			for k := range a {
				keys[k] = true
			}
			for k := range b {
				keys[k] = true
			}
			ordered := make([]string, 0, len(keys))
			for k := range keys {
				ordered = append(ordered, k)
			}
			sort.Strings(ordered)
			for _, k := range ordered {
				av, aok := a[k]
				bv, bok := b[k]
				if !aok || !bok {
					return fmt.Sprintf("%s.%s: field present expected=%t observed=%t", path, k, aok, bok)
				}
				if d := schDesignatorFirstDifference(path+"."+k, av, bv); d != "" {
					return d
				}
			}
		}
	}
	a, b := reflect.ValueOf(expected), reflect.ValueOf(observed)
	if a.IsValid() && b.IsValid() && a.Kind() == reflect.Slice && b.Kind() == reflect.Slice {
		if a.Len() != b.Len() {
			return fmt.Sprintf("%s.length: expected %d, observed %d", path, a.Len(), b.Len())
		}
		for i := 0; i < a.Len(); i++ {
			if d := schDesignatorFirstDifference(fmt.Sprintf("%s[%d]", path, i), a.Index(i).Interface(), b.Index(i).Interface()); d != "" {
				return d
			}
		}
	}
	brief := func(v any) string {
		raw, _ := json.Marshal(v)
		if len(raw) > 160 {
			return string(raw[:160]) + "…"
		}
		return string(raw)
	}
	return fmt.Sprintf("%s: expected %s, observed %s", path, brief(expected), brief(observed))
}

func verifySchDesignatorGlobal(before schDesignatorBaseline, target connectivity.Document, global *actionResult) error {
	if global == nil || !global.OK || global.Context == nil || global.Context.ProjectUUID != before.Context.ProjectUUID || global.Context.DocumentUUID != before.Context.DocumentUUID {
		return fmt.Errorf("global reference inventory project/document mismatch")
	}
	refs := map[string]string{}
	wantIDs := map[string]bool{}
	for _, c := range target.Components {
		refs[strings.ToUpper(c.Ref)] = c.ID
		wantIDs[c.ID] = true
	}
	components, ok := global.Result["components"].([]any)
	if !ok {
		return fmt.Errorf("global reference inventory missing")
	}
	seen := map[string]bool{}
	for _, item := range components {
		c, ok := item.(map[string]any)
		if !ok {
			return fmt.Errorf("invalid global component")
		}
		if c["componentType"] != "part" {
			continue
		}
		id, err := schDesignatorComponentID(c)
		if err != nil {
			return err
		}
		if !wantIDs[id] || seen[id] {
			return fmt.Errorf("global component %s is missing from target or duplicated", id)
		}
		seen[id] = true
		if owner, found := refs[strings.ToUpper(stringVal(c["designator"]))]; found && owner != id {
			return fmt.Errorf("target reference %s is already occupied by %s", c["designator"], id)
		}
	}
	for id := range wantIDs {
		if !seen[id] {
			return fmt.Errorf("target component %s absent from full-project reference inventory", id)
		}
	}
	return nil
}

func renameSchDesignatorGroups(groups []*schGroup, before schDesignatorBaseline, target connectivity.Document) bool {
	byID := map[string]string{}
	for _, c := range target.Components {
		byID[c.ID] = c.Ref
	}
	renames := map[string]string{}
	for pid, id := range before.IDs {
		old := stringVal(before.Parts[pid]["designator"])
		if next := byID[id]; next != "" && next != old {
			renames[old] = next
		}
	}
	changed := false
	for _, g := range groups {
		if g == nil {
			continue
		}
		for i, old := range g.Members {
			if next, ok := renames[old]; ok {
				g.Members[i] = next
				changed = true
			}
		}
		for role, old := range g.Roles {
			if next, ok := renames[old]; ok {
				g.Roles[role] = next
				changed = true
			}
		}
	}
	return changed
}

func newSchDesignatorsVerifyCmd(cfg *appConfig, window *string, stdout io.Writer) *cobra.Command {
	var beforePath, targetPath, beforeSHA, targetSHA, phase string
	var syncGroups bool
	c := &cobra.Command{Use: "verify", Short: "Verify a file-pinned designator repair against fresh EDA reads", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if phase != "before" && phase != "after" {
			return fmt.Errorf("--phase must be before or after")
		}
		if syncGroups && phase != "after" {
			return fmt.Errorf("--sync-groups requires --phase after")
		}
		if beforePath == "" || targetPath == "" || beforeSHA == "" || targetSHA == "" {
			return fmt.Errorf("--before, --target, --before-sha and --target-sha are required; use the guarded plan")
		}
		beforeRaw, err := os.ReadFile(beforePath)
		if err != nil {
			return err
		}
		targetRaw, err := os.ReadFile(targetPath)
		if err != nil {
			return err
		}
		if sha256Hex(beforeRaw) != beforeSHA || sha256Hex(targetRaw) != targetSHA {
			return fmt.Errorf("repair input SHA mismatch; regenerate the plan from fresh inputs")
		}
		before, err := parseSchDesignatorBaseline(beforeRaw)
		if err != nil {
			return err
		}
		var target connectivity.Document
		if err = json.Unmarshal(targetRaw, &target); err != nil {
			return err
		}
		if _, err = schDesignatorTargets(before, target); err != nil {
			return err
		}
		local := *cfg
		if local.doc != "" && local.doc != before.Context.DocumentUUID {
			return fmt.Errorf("configured document differs from repair baseline")
		}
		// Preserve standard --doc behavior: activate the existing baseline page
		// before reading. This never creates a page or edits circuit primitives.
		local.doc = before.Context.DocumentUUID
		// tagPages activates every existing page: getAll alone can omit pages
		// that this editor session has not loaded yet.
		global, err := requestAction(&local, "schematic.components.list", *window, map[string]any{"allPages": true, "tagPages": true})
		if err != nil {
			return err
		}
		if err = verifySchDesignatorGlobal(before, target, global); err != nil {
			return err
		}
		includeBBox := false
		for _, item := range before.Result["components"].([]any) {
			c := item.(map[string]any)
			if c["componentType"] != "part" && c["bbox"] != nil {
				includeBBox = true
				break
			}
		}
		res, err := requestAction(&local, "schematic.components.list", *window, map[string]any{"includePins": true, "includeBBox": includeBBox, "includeDeviceIdentity": true, "includeWires": true, "includeConnectivitySummary": true})
		if err != nil {
			return err
		}
		liveRaw, err := json.Marshal(res)
		if err != nil {
			return err
		}
		live, err := parseSchDesignatorBaseline(liveRaw)
		if err != nil {
			return err
		}
		if err = verifySchDesignatorScene(before, target, live, phase); err != nil {
			return err
		}
		groupsChanged := false
		if syncGroups {
			_, _, doc, _, st, groups, err := loadSchGroupsContext(&local, *window)
			if err != nil {
				return err
			}
			if doc != before.Context.DocumentUUID || st.ProjectUUID != before.Context.ProjectUUID {
				return fmt.Errorf("group state project/document differs from verified page")
			}
			groupsChanged = renameSchDesignatorGroups(groups, before, target)
			if groupsChanged {
				if err = saveSchGroups(st, doc, groups); err != nil {
					return err
				}
			}
		}
		return json.NewEncoder(stdout).Encode(map[string]any{"ok": true, "phase": phase, "projectId": before.Context.ProjectUUID, "documentId": before.Context.DocumentUUID, "components": len(before.Parts), "groupsChanged": groupsChanged})
	}}
	c.Flags().StringVar(&beforePath, "before", "", "baseline sch list envelope")
	c.Flags().StringVar(&targetPath, "target", "", "allocated full-project connectivity target")
	c.Flags().StringVar(&beforeSHA, "before-sha", "", "baseline file SHA-256 recorded by plan")
	c.Flags().StringVar(&targetSHA, "target-sha", "", "target file SHA-256 recorded by plan")
	c.Flags().StringVar(&phase, "phase", "", "before or after")
	c.Flags().BoolVar(&syncGroups, "sync-groups", false, "after verification, update this page's local group members/roles")
	return c
}
