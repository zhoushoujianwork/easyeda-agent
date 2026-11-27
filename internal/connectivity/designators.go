package connectivity

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// These explicit instance properties keep component identity and functional
// role separate from the displayed reference designator across a rename.
const ComponentIDProperty = "EasyEDA Agent Component ID"
const ComponentRoleProperty = "EasyEDA Agent Role"

const nonstandardDesignatorIssue = "nonstandard-designator"

var numberedDesignator = regexp.MustCompile(`^([A-Za-z]+)([0-9]+)$`)
var libraryDesignatorPrefix = regexp.MustCompile(`^([A-Za-z]+)\??$`)

type DesignatorChange struct {
	ComponentID string `json:"componentId"`
	Before      string `json:"before"`
	After       string `json:"after"`
}

func componentBinding(properties map[string]any, key string) (string, bool, error) {
	raw, exists := properties[key]
	if !exists {
		return "", false, nil
	}
	value, ok := raw.(string)
	if !ok || strings.TrimSpace(value) == "" {
		return "", true, fmt.Errorf("%s must be a nonempty string when explicitly bound", key)
	}
	return value, true, nil
}

// Refresh only our derived designator findings. Existing electrical findings
// are preserved, and repeated validation never duplicates this warning.
func (d *Document) refreshDesignatorIssues() {
	issues := make([]Issue, 0, len(d.Issues))
	for _, issue := range d.Issues {
		if issue.Code != nonstandardDesignatorIssue {
			issues = append(issues, issue)
		}
	}
	matcher, err := d.designatorRegexp()
	if err != nil {
		return // Validate reports the policy error before this derived diagnosis.
	}
	for _, c := range d.Components {
		if !matcher.MatchString(c.Ref) {
			message := fmt.Sprintf("%s is not a numbered reference designator; keep its functional name in role and assign the official library prefix plus a number before placement", c.Ref)
			if d.DesignatorPolicy != nil && d.DesignatorPolicy.Mode == "custom" {
				message = fmt.Sprintf("%s does not match the declared V4 custom designator pattern %q", c.Ref, d.DesignatorPolicy.Pattern)
			}
			issues = append(issues, Issue{Code: nonstandardDesignatorIssue, Severity: "warning", ComponentID: c.ID, Message: message})
		}
	}
	if len(issues) == 0 && d.Issues == nil {
		d.Issues = nil
	} else {
		d.Issues = issues
	}
}

func (d Document) designatorRegexp() (*regexp.Regexp, error) {
	if d.DesignatorPolicy == nil || d.DesignatorPolicy.Mode == "" || d.DesignatorPolicy.Mode == "classic" {
		return numberedDesignator, nil
	}
	if d.DesignatorPolicy.Mode != "custom" {
		return nil, fmt.Errorf("unsupported designatorPolicy.mode %q", d.DesignatorPolicy.Mode)
	}
	pattern := strings.TrimSpace(d.DesignatorPolicy.Pattern)
	if pattern == "" || !strings.HasPrefix(pattern, "^") || !strings.HasSuffix(pattern, "$") {
		return nil, fmt.Errorf("custom designatorPolicy.pattern must be a nonempty anchored RE2 expression (^...$)")
	}
	r, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid custom designatorPolicy.pattern %q: %w", pattern, err)
	}
	return r, nil
}

// ValidatePlacementDesignators is the mutation preflight. Legacy functional
// references remain readable through Validate, but cannot be replayed as newly
// placed reference designators. This function does not rename or mutate input.
func ValidatePlacementDesignators(d Document) error {
	probe := d
	probe.Issues = nil
	if err := probe.Validate(); err != nil {
		return err
	}
	matcher, err := d.designatorRegexp()
	if err != nil {
		return err
	}
	for _, c := range d.Components {
		if !matcher.MatchString(c.Ref) {
			if d.DesignatorPolicy != nil && d.DesignatorPolicy.Mode == "custom" {
				return fmt.Errorf("%s (%s): designator does not match the declared V4 custom designator policy", c.ID, c.Ref)
			}
			return fmt.Errorf("%s (%s): nonstandard designator; allocate a numbered reference from the official library prefix before placement", c.ID, c.Ref)
		}
	}
	return nil
}

// AllocateDesignators repairs only non-numbered references. Prefixes must be
// measured library defaults keyed by libraryUuid/deviceUuid, never guessed
// from roles, MPNs or part namespaces. Existing numbered references are kept
// byte-for-byte; their case and leading zeroes still reserve the same number.
func AllocateDesignators(d Document, prefixes map[string]string) (Document, []DesignatorChange, error) {
	probe := d
	probe.Issues = nil
	if err := probe.Validate(); err != nil {
		return Document{}, nil, err
	}
	if d.DesignatorPolicy != nil && d.DesignatorPolicy.Mode == "custom" {
		if err := ValidatePlacementDesignators(d); err != nil {
			return Document{}, nil, fmt.Errorf("V4 custom designators are preserved, not auto-incremented: %w", err)
		}
		out := cloneDesignatorDocument(d)
		out.refreshDesignatorIssues()
		return out, nil, nil
	}
	used := map[string]map[string]bool{}
	reserve := func(prefix, number string) {
		key := strings.ToUpper(prefix)
		if used[key] == nil {
			used[key] = map[string]bool{}
		}
		number = strings.TrimLeft(number, "0")
		if number == "" {
			number = "0"
		}
		used[key][number] = true
	}
	for _, c := range d.Components {
		if parts := numberedDesignator.FindStringSubmatch(c.Ref); parts != nil {
			reserve(parts[1], parts[2])
		}
	}
	out := cloneDesignatorDocument(d)
	var changes []DesignatorChange
	for i := range out.Components {
		c := &out.Components[i]
		if numberedDesignator.MatchString(c.Ref) {
			continue
		}
		key := c.Device.LibraryUUID + "/" + c.Device.UUID
		prefix, exists := prefixes[key]
		if c.Device.LibraryUUID == "" || c.Device.UUID == "" || !exists {
			return Document{}, nil, fmt.Errorf("%s: official library designator prefix is missing for %s", c.Ref, key)
		}
		parsed := libraryDesignatorPrefix.FindStringSubmatch(strings.TrimSpace(prefix))
		if parsed == nil {
			return Document{}, nil, fmt.Errorf("%s: invalid official library designator prefix %q for %s; expected letters with an optional ?", c.Ref, prefix, key)
		}
		prefix = parsed[1]
		number := 1
		for used[strings.ToUpper(prefix)][strconv.Itoa(number)] {
			number++
		}
		next := prefix + strconv.Itoa(number)
		reserve(prefix, strconv.Itoa(number))
		changes = append(changes, DesignatorChange{ComponentID: c.ID, Before: c.Ref, After: next})
		if c.Role == "" {
			c.Role = c.Ref
		}
		c.Ref = next
	}
	out.refreshDesignatorIssues()
	return out, changes, nil
}

// Clone the complete IR, including all nested geometry and module references;
// allocation must never mutate caller-owned snapshots through shared slices.
func cloneDesignatorDocument(d Document) Document {
	out := d
	if d.DesignatorPolicy != nil {
		policy := *d.DesignatorPolicy
		out.DesignatorPolicy = &policy
	}
	out.Components = slices.Clone(d.Components)
	for i := range out.Components {
		c := &out.Components[i]
		c.Pins = slices.Clone(c.Pins)
		if c.Placement != nil {
			placement := *c.Placement
			if placement.BBox != nil {
				box := *placement.BBox
				placement.BBox = &box
			}
			c.Placement = &placement
		}
		c.SubParts = slices.Clone(c.SubParts)
		for j := range c.SubParts {
			sub := &c.SubParts[j]
			if sub.Placement != nil {
				placement := *sub.Placement
				if placement.BBox != nil {
					box := *placement.BBox
					placement.BBox = &box
				}
				sub.Placement = &placement
			}
		}
	}
	out.Nets = slices.Clone(d.Nets)
	out.Connections = slices.Clone(d.Connections)
	out.Issues = slices.Clone(d.Issues)
	out.Modules = slices.Clone(d.Modules)
	for i := range out.Modules {
		m := &out.Modules[i]
		m.CoreComponents = slices.Clone(m.CoreComponents)
		m.PeripheralComponents = slices.Clone(m.PeripheralComponents)
		m.InternalNets = slices.Clone(m.InternalNets)
		m.Ports = slices.Clone(m.Ports)
		for j := range m.Ports {
			m.Ports[j].PinRefs = slices.Clone(m.Ports[j].PinRefs)
		}
	}
	return out
}
