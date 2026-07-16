package blocks

import (
	"encoding/json"
	"fmt"
	"strings"
)

var validCategories = map[string]bool{
	"button": true, "comms": true, "indicator": true, "mcu": true,
	"mcu-support": true, "power": true, "protection": true, "rf": true,
	"sensing": true, "storage": true, "usb": true, "usb-serial": true,
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
			if previous, ok := pinNet[member]; ok && previous != i {
				add(field, fmt.Sprintf("pin %q already belongs to internal_nets[%d]", member, previous))
			} else {
				pinNet[member] = i
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
			if _, exists := pinNet[p.At]; !exists {
				add(field+".at", "PORT marker exists but anchor is absent from internal_nets")
			}
		}
	}

	if b.Verification != nil {
		validateVerification(b, add)
	}
	return errs
}

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
