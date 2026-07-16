package blocks

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	all, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("no blocks embedded — did `make sync-blocks` run?")
	}
	for _, b := range all {
		if b.ID == "" || b.Desc == "" {
			t.Errorf("block missing id/desc: %+v", b)
		}
		if b.Verification == nil && b.Ready() != (b.Validated != nil && *b.Validated != "") {
			t.Errorf("%s: legacy Ready() disagrees with Validated", b.ID)
		}
		if b.Verification != nil && b.Ready() != b.Verification.ProductionReady {
			t.Errorf("%s: Ready() disagrees with structured verification", b.ID)
		}
	}
}

func TestAllBlocksPassCoreValidation(t *testing.T) {
	all, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, err := range ValidateAll(all) {
		t.Error(err)
	}
}

func TestValidateRejectsBrokenTopology(t *testing.T) {
	raw := json.RawMessage(`{
		"id":"block.bad","desc":"bad","category":"power",
		"parts":{"U":{"part":"ic.bad","qty":1}},
		"ports":{"OUT":{"dir":"sideways","at":"MISSING.OUT"}},
		"internal_nets":[["U.OUT","PORT:NO_SUCH_PORT"],["U.OUT","U.GND"]]
	}`)
	var b Block
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatal(err)
	}
	b.Raw = raw
	errs := Validate(b)
	joined := make([]string, 0, len(errs))
	for _, err := range errs {
		joined = append(joined, err.Error())
	}
	got := strings.Join(joined, "\n")
	for _, want := range []string{"unknown port", "already belongs", "unknown value", "unknown role"} {
		if !strings.Contains(got, want) {
			t.Errorf("validation errors missing %q:\n%s", want, got)
		}
	}
}

func TestStructuredVerificationControlsReadiness(t *testing.T) {
	legacy := "old evidence"
	b := Block{Validated: &legacy}
	if !b.Ready() {
		t.Fatal("legacy validated block must remain ready during migration")
	}
	b.Verification = &Verification{
		Schematic:          VerificationStage{Status: "passed", Evidence: "netlist"},
		ComponentSelection: VerificationStage{Status: "failed", Issues: []string{"wrong ESD"}},
		PCBDRC:             VerificationStage{Status: "not_tested"},
		Bringup:            VerificationStage{Status: "not_tested"},
	}
	if b.Ready() || b.Status() != "verified" {
		t.Fatalf("structured verification must override legacy evidence: ready=%v status=%s", b.Ready(), b.Status())
	}
}

func TestGetPrefixOptional(t *testing.T) {
	all, err := Load()
	if err != nil || len(all) == 0 {
		t.Skip("no blocks")
	}
	want := all[0].ID // e.g. block.xxx
	bare := want[len("block."):]
	for _, id := range []string{want, bare} {
		b, ok, err := Get(id)
		if err != nil || !ok {
			t.Fatalf("Get(%q): ok=%v err=%v", id, ok, err)
		}
		if b.ID != want {
			t.Errorf("Get(%q) → %s, want %s", id, b.ID, want)
		}
	}
}

// TestFilenameMatchesID enforces the one-block-per-file contract: data/<id>.json
// where <id> is the block id minus the `block.` prefix.
func TestFilenameMatchesID(t *testing.T) {
	entries, err := data.ReadDir("data")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".json") || strings.HasPrefix(name, "_") {
			continue
		}
		raw, _ := data.ReadFile("data/" + name)
		var b Block
		if err := json.Unmarshal(raw, &b); err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		want := "block." + strings.TrimSuffix(name, ".json")
		if b.ID != want {
			t.Errorf("%s: id %q, want %q (filename minus block.)", name, b.ID, want)
		}
	}
}

// TestAttributionOnValidated: a validated (non-draft) block must carry author +
// added + updated (permanent, traceable credit).
func TestAttributionOnValidated(t *testing.T) {
	all, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range all {
		if !b.Ready() {
			continue
		}
		if b.Author == "" || b.Added == "" || b.Updated == "" {
			t.Errorf("%s: validated block missing author/added/updated", b.ID)
		}
	}
}

// standardPartsPath is the skill's part library; block parts cross-reference it.
const standardPartsPath = "../../skills/easyeda-agent/references/standard-parts.json"

// TestPartsExistInStandardParts: every block's parts[].part (and alt[]) must be a
// real key in standard-parts.json, so BOM/LCSC stays single-sourced.
func TestPartsExistInStandardParts(t *testing.T) {
	raw, err := os.ReadFile(standardPartsPath)
	if err != nil {
		t.Skipf("standard-parts.json not found (outside repo tree): %v", err)
	}
	var sp struct {
		Parts map[string]any `json:"parts"`
	}
	if err := json.Unmarshal(raw, &sp); err != nil {
		t.Fatalf("parse standard-parts.json: %v", err)
	}
	all, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range all {
		var parts map[string]struct {
			Part string   `json:"part"`
			Alt  []string `json:"alt"`
		}
		if err := json.Unmarshal(b.Raw, &struct {
			Parts *map[string]struct {
				Part string   `json:"part"`
				Alt  []string `json:"alt"`
			} `json:"parts"`
		}{Parts: &parts}); err != nil {
			t.Errorf("%s: parse parts: %v", b.ID, err)
			continue
		}
		for role, p := range parts {
			for _, key := range append([]string{p.Part}, p.Alt...) {
				if key == "" {
					continue
				}
				if _, ok := sp.Parts[key]; !ok {
					t.Errorf("%s role %s: part %q not in standard-parts.json", b.ID, role, key)
				}
			}
		}
	}
}

// Constraint-map validators — the extensible layout dimensions (placement, signals).
// A block carries these as top-level maps; adding a future dimension follows the same
// shape. The loader keeps unknown maps in Raw (forward-compatible); these tests validate
// the KNOWN ones so a contribution can't ship a malformed board-edge / diff-pair spec.

var okConstraintSeverity = map[string]bool{"must": true, "should": true, "": true}
var okPlacementEdge = map[string]bool{
	"any": true, "user-facing": true, "top": true, "bottom": true, "left": true, "right": true, "": true,
}
var okPlacementSide = map[string]bool{"top": true, "bottom": true, "either": true, "": true}

// TestPlacementMap: placement is keyed by <ROLE> (which structural part sits at a board
// edge / on which copper side). Every role must exist in parts; edge/side/severity are
// enum-checked. The `_doc` key is skipped (it's the map's inline doc string).
func TestPlacementMap(t *testing.T) {
	all, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range all {
		var wrap struct {
			Placement map[string]json.RawMessage `json:"placement"`
			Parts     map[string]json.RawMessage `json:"parts"`
		}
		if err := json.Unmarshal(b.Raw, &wrap); err != nil {
			t.Errorf("%s: parse placement: %v", b.ID, err)
			continue
		}
		for role, raw := range wrap.Placement {
			if role == "_doc" {
				continue
			}
			if _, ok := wrap.Parts[role]; !ok {
				t.Errorf("%s: placement role %q not in parts", b.ID, role)
			}
			var rec struct{ Severity, Edge, Side string }
			if err := json.Unmarshal(raw, &rec); err != nil {
				t.Errorf("%s placement %s: %v", b.ID, role, err)
				continue
			}
			if !okConstraintSeverity[rec.Severity] {
				t.Errorf("%s placement %s: bad severity %q", b.ID, role, rec.Severity)
			}
			if !okPlacementEdge[rec.Edge] {
				t.Errorf("%s placement %s: bad edge %q (want any/user-facing/top/bottom/left/right)", b.ID, role, rec.Edge)
			}
			if !okPlacementSide[rec.Side] {
				t.Errorf("%s placement %s: bad side %q (want top/bottom/either)", b.ID, role, rec.Side)
			}
		}
	}
}

// TestSignalsMap: signals is keyed by <signal-group>; each record needs a type + nets so
// a diff-pair / RF / high-speed spec is machine-usable. severity is enum-checked.
func TestSignalsMap(t *testing.T) {
	all, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range all {
		var wrap struct {
			Signals map[string]json.RawMessage `json:"signals"`
		}
		if err := json.Unmarshal(b.Raw, &wrap); err != nil {
			t.Errorf("%s: parse signals: %v", b.ID, err)
			continue
		}
		for name, raw := range wrap.Signals {
			if name == "_doc" {
				continue
			}
			var rec struct {
				Type     string   `json:"type"`
				Severity string   `json:"severity"`
				Nets     []string `json:"nets"`
			}
			if err := json.Unmarshal(raw, &rec); err != nil {
				t.Errorf("%s signal %s: %v", b.ID, name, err)
				continue
			}
			if rec.Type == "" {
				t.Errorf("%s signal %s: missing type", b.ID, name)
			}
			if len(rec.Nets) == 0 {
				t.Errorf("%s signal %s: missing nets", b.ID, name)
			}
			if !okConstraintSeverity[rec.Severity] {
				t.Errorf("%s signal %s: bad severity %q", b.ID, name, rec.Severity)
			}
		}
	}
}

// TestSilkMap: silk is keyed by <ROLE> (which part gets pin-level / polarity silk).
// Every role must exist in parts; severity is enum-checked; each record must carry at
// least one of label/pins/note (an empty silk record says nothing); pins (if present)
// must decode as a pinRef→text string map. The `_doc` key is skipped.
func TestSilkMap(t *testing.T) {
	all, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range all {
		var wrap struct {
			Silk  map[string]json.RawMessage `json:"silk"`
			Parts map[string]json.RawMessage `json:"parts"`
		}
		if err := json.Unmarshal(b.Raw, &wrap); err != nil {
			t.Errorf("%s: parse silk: %v", b.ID, err)
			continue
		}
		for role, raw := range wrap.Silk {
			if role == "_doc" {
				continue
			}
			if _, ok := wrap.Parts[role]; !ok {
				t.Errorf("%s: silk role %q not in parts", b.ID, role)
			}
			var rec struct {
				Label    string            `json:"label"`
				Pins     map[string]string `json:"pins"`
				Note     string            `json:"note"`
				Severity string            `json:"severity"`
			}
			if err := json.Unmarshal(raw, &rec); err != nil {
				t.Errorf("%s silk %s: %v", b.ID, role, err)
				continue
			}
			if rec.Label == "" && len(rec.Pins) == 0 && rec.Note == "" {
				t.Errorf("%s silk %s: empty — needs at least one of label/pins/note", b.ID, role)
			}
			if !okConstraintSeverity[rec.Severity] {
				t.Errorf("%s silk %s: bad severity %q", b.ID, role, rec.Severity)
			}
		}
	}
}
