package blocks

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var validCategories = map[string]bool{
	"audio": true, "button": true, "comms": true, "display": true,
	"indicator": true, "mcu": true, "mcu-support": true, "power": true,
	"protection": true, "rf": true, "sensing": true, "storage": true,
	"usb": true, "usb-serial": true,
}

var validPortDirections = map[string]bool{"in": true, "out": true, "bidir": true}
var validVerificationStatuses = map[string]bool{
	"passed": true, "failed": true, "pending": true, "not_tested": true,
}

// ValidationError identifies a bad field without hiding which block caused it.
type ValidationError struct {
	BlockID string
	Field   string
	Problem string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("%s %s: %s", e.BlockID, e.Field, e.Problem)
}

// Validate checks the executable core contract. Unknown top-level extension maps
// remain forward-compatible, but known fields and all topology references are strict.
func Validate(b Block) []error {
	var errs []error
	add := func(field, problem string) {
		errs = append(errs, ValidationError{BlockID: b.ID, Field: field, Problem: problem})
	}

	if strings.TrimSpace(b.ID) == "" {
		add("id", "required")
	} else if !strings.HasPrefix(b.ID, "block.") {
		add("id", "must start with block.")
	}
	if strings.TrimSpace(b.Desc) == "" {
		add("desc", "required")
	}
	if !validCategories[b.Category] {
		add("category", fmt.Sprintf("unknown value %q", b.Category))
	}
	if len(b.Parts) == 0 {
		add("parts", "must contain at least one role")
	}
	for role, p := range b.Parts {
		if strings.TrimSpace(role) == "" || strings.HasPrefix(role, "_") {
			add("parts."+role, "invalid role")
		}
		if strings.TrimSpace(p.Part) == "" {
			add("parts."+role+".part", "required")
		}
		if p.Qty < 1 {
			add("parts."+role+".qty", "must be >= 1")
		}
	}

	validateBomNoteCount(b, add)

	var topology struct {
		InternalNets [][]string `json:"internal_nets"`
	}
	if err := json.Unmarshal(b.Raw, &topology); err != nil {
		add("internal_nets", err.Error())
		return errs
	}
	pinNet := map[string]int{}
	usedPorts := map[string]bool{}
	for i, net := range topology.InternalNets {
		field := fmt.Sprintf("internal_nets[%d]", i)
		if len(net) < 2 {
			add(field, "must contain at least two members")
		}
		seen := map[string]bool{}
		for _, member := range net {
			if seen[member] {
				add(field, fmt.Sprintf("duplicate member %q", member))
				continue
			}
			seen[member] = true
			if strings.HasPrefix(member, "PORT:") {
				name := strings.TrimPrefix(member, "PORT:")
				if _, ok := b.Ports[name]; !ok {
					add(field, fmt.Sprintf("references unknown port %q", name))
				}
				usedPorts[name] = true
				continue
			}
			role, _, ok := splitPinRef(member)
			if !ok {
				add(field, fmt.Sprintf("invalid pin ref %q", member))
				continue
			}
			if _, ok := b.Parts[role]; !ok {
				add(field, fmt.Sprintf("references unknown role %q", role))
			}
			// Index by the bare pin ref: a trailing "*" (bond EVERY pin sharing this
			// function name — a connector's redundant VBUS/GND/shield) names the same
			// boundary as the plain ref, so "J.VBUS*" and "J.VBUS" must not read as two
			// different pins here.
			key := strings.TrimSuffix(member, pinFanoutSuffix)
			if previous, ok := pinNet[key]; ok && previous != i {
				add(field, fmt.Sprintf("pin %q already belongs to internal_nets[%d]", member, previous))
			} else {
				pinNet[key] = i
			}
		}
	}

	for name, p := range b.Ports {
		field := "ports." + name
		if strings.TrimSpace(name) == "" || strings.HasPrefix(name, "_") {
			add(field, "invalid port name")
		}
		if !validPortDirections[p.Dir] {
			add(field+".dir", fmt.Sprintf("unknown value %q", p.Dir))
		}
		role, _, ok := splitPinRef(p.At)
		if !ok {
			add(field+".at", fmt.Sprintf("invalid pin ref %q", p.At))
		} else if _, exists := b.Parts[role]; !exists {
			add(field+".at", fmt.Sprintf("references unknown role %q", role))
		}
		// A direct one-pin boundary need not appear in internal_nets. When a PORT:
		// marker is present, however, its declared anchor must be on that same net.
		if usedPorts[name] {
			if _, exists := pinNet[strings.TrimSuffix(p.At, pinFanoutSuffix)]; !exists {
				add(field+".at", "PORT marker exists but anchor is absent from internal_nets")
			}
		}
	}

	if b.Verification != nil {
		validateVerification(b, add)
	}
	validateSchematicLayout(b, add)
	return errs
}

// validSchLayoutRotations mirrors what schematic.component.place accepts.
var validSchLayoutRotations = map[float64]bool{0: true, 90: true, 180: true, 270: true}

// schLayoutGrid is the schematic placement grid (see app.schAnchorGrid): an
// off-grid template offset would put every pin of that role off-grid and break
// connect_pin stubs, so it is a data error, not a runtime surprise.
const schLayoutGrid = 5

// validateSchematicLayout checks the optional schematic placement template:
// every referenced role must exist, every part role must be covered (a partial
// template silently mixes template and fallback-grid geometry — an authoring
// mistake), offsets must land on the placement grid, rotations must be legal.
func validateSchematicLayout(b Block, add func(string, string)) {
	layout, err := b.SchematicLayout()
	if err != nil {
		add("schematic_layout", err.Error())
		return
	}
	if layout == nil {
		return
	}
	if len(layout.Roles) == 0 {
		add("schematic_layout.roles", "must contain at least one role")
		return
	}
	onGrid := func(v float64) bool {
		m := v / schLayoutGrid
		return m == float64(int64(m))
	}
	for role, h := range layout.Roles {
		field := "schematic_layout.roles." + role
		if _, ok := b.Parts[role]; !ok {
			add(field, "references unknown role")
		}
		if !onGrid(h.DX) || !onGrid(h.DY) {
			add(field, fmt.Sprintf("offset (%g,%g) is off the %d-unit placement grid", h.DX, h.DY, schLayoutGrid))
		}
		if !validSchLayoutRotations[h.Rotation] {
			add(field+".rotation", fmt.Sprintf("must be 0/90/180/270, got %g", h.Rotation))
		}
	}
	for role := range b.Parts {
		if _, ok := layout.Roles[role]; !ok {
			add("schematic_layout.roles", fmt.Sprintf("role %q not covered — the template must place every part or be omitted", role))
		}
	}
}

// bom_note is prose, but a "共 N 件" claim inside it is a checkable fact: agents
// and humans use it to audit BOM completeness before ordering, so a stale count
// causes real mis-orders (#128). N must equal the sum of parts[].qty.
var reBomNoteCount = regexp.MustCompile(`共\s*(\d+)\s*件`)

func validateBomNoteCount(b Block, add func(string, string)) {
	var extra struct {
		BomNote string `json:"bom_note"`
	}
	if err := json.Unmarshal(b.Raw, &extra); err != nil || extra.BomNote == "" {
		return
	}
	m := reBomNoteCount.FindStringSubmatch(extra.BomNote)
	if m == nil {
		return
	}
	claimed, _ := strconv.Atoi(m[1])
	total := 0
	for _, p := range b.Parts {
		total += p.Qty
	}
	if claimed != total {
		add("bom_note", fmt.Sprintf("claims 共 %d 件 but parts qty sums to %d", claimed, total))
	}
}

// pinFanoutSuffix marks a pin ref that bonds EVERY pin sharing that function name
// on the part — "J.VBUS*" for a USB-C's two VBUS pins, its two GNDs, its four EP
// tabs. It exists because referring to such a pin by name alone is genuinely
// ambiguous (and `sch autoconnect` rightly refuses to pick one), while the intent
// for power/ground/shield is invariably "all of them"; USB-C's dual orientation in
// fact REQUIRES both the A- and B-side pins be connected. Blocks declare it
// explicitly rather than letting the planner infer it from the net's kind.
const pinFanoutSuffix = "*"

func splitPinRef(ref string) (role, pin string, ok bool) {
	i := strings.IndexByte(ref, '.')
	if i <= 0 || i == len(ref)-1 {
		return "", "", false
	}
	return ref[:i], ref[i+1:], true
}

func validateVerification(b Block, add func(string, string)) {
	v := b.Verification
	stages := []struct {
		name  string
		stage VerificationStage
	}{
		{"schematic", v.Schematic},
		{"component_selection", v.ComponentSelection},
		{"pcb_drc", v.PCBDRC},
		{"bringup", v.Bringup},
	}
	allPassed := true
	for _, item := range stages {
		field := "verification." + item.name
		if !validVerificationStatuses[item.stage.Status] {
			add(field+".status", fmt.Sprintf("unknown value %q", item.stage.Status))
			allPassed = false
			continue
		}
		if item.stage.Status != "passed" {
			allPassed = false
		}
		if item.stage.Status == "passed" && strings.TrimSpace(item.stage.Evidence) == "" {
			add(field+".evidence", "required when status is passed")
		}
		if item.stage.Status == "failed" && len(item.stage.Issues) == 0 {
			add(field+".issues", "required when status is failed")
		}
	}
	if v.ProductionReady && !allPassed {
		add("verification.production_ready", "cannot be true unless every verification stage passed")
	}
}

// ValidateAll returns every data error instead of failing at the first block.
func ValidateAll(blocks []Block) []error {
	var errs []error
	for _, b := range blocks {
		errs = append(errs, Validate(b)...)
	}
	return errs
}
