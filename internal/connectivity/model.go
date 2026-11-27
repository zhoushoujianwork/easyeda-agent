// Package connectivity defines the versioned, layout-independent schematic IR.
package connectivity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

type Document struct {
	SchemaVersion string `json:"schemaVersion"`
	ProjectID     string `json:"projectId,omitempty"`
	DocumentID    string `json:"documentId,omitempty"`
	// DesignatorPolicy keeps V4 custom reference formats explicit. Omitted (or
	// mode=classic) preserves the historical letters+digits rule. Custom mode
	// validates and preserves references but never invents the platform's
	// configured increment sequence.
	DesignatorPolicy *DesignatorPolicy `json:"designatorPolicy,omitempty"`
	Components       []Component       `json:"components"`
	Nets             []Net             `json:"nets"`
	Connections      []Connection      `json:"connections"`
	Modules          []Module          `json:"modules,omitempty"`
	Issues           []Issue           `json:"issues,omitempty"`
}
type DesignatorPolicy struct {
	Mode    string `json:"mode"`              // classic | custom
	Pattern string `json:"pattern,omitempty"` // anchored RE2 expression in custom mode
}
type Issue struct {
	Code        string `json:"code"`
	Severity    string `json:"severity"`
	ComponentID string `json:"componentId,omitempty"`
	PinNumber   string `json:"pinNumber,omitempty"`
	NetID       string `json:"netId,omitempty"`
	Message     string `json:"message"`
}
type Diff struct {
	AddedComponents    []string `json:"addedComponents,omitempty"`
	RemovedComponents  []string `json:"removedComponents,omitempty"`
	AddedNets          []string `json:"addedNets,omitempty"`
	RemovedNets        []string `json:"removedNets,omitempty"`
	ChangedConnections []string `json:"changedConnections,omitempty"`
	ChangedNoConnect   []string `json:"changedNoConnect,omitempty"`
	ChangedPinState    []string `json:"changedPinState,omitempty"`
}

func Compare(a, b Document) Diff {
	d := Diff{}
	ac, bc := map[string]Component{}, map[string]Component{}
	for _, c := range a.Components {
		ac[c.ID] = c
	}
	for _, c := range b.Components {
		bc[c.ID] = c
	}
	for id := range bc {
		if _, ok := ac[id]; !ok {
			d.AddedComponents = append(d.AddedComponents, id)
		}
	}
	for id := range ac {
		if _, ok := bc[id]; !ok {
			d.RemovedComponents = append(d.RemovedComponents, id)
		}
	}
	an, bn := map[string]bool{}, map[string]bool{}
	for _, n := range a.Nets {
		an[n.ID] = true
	}
	for _, n := range b.Nets {
		bn[n.ID] = true
	}
	for id := range bn {
		if !an[id] {
			d.AddedNets = append(d.AddedNets, id)
		}
	}
	for id := range an {
		if !bn[id] {
			d.RemovedNets = append(d.RemovedNets, id)
		}
	}
	am, bm := map[string]string{}, map[string]string{}
	for _, c := range a.Connections {
		am[c.ComponentID+":"+c.PinNumber] = c.NetID
	}
	for _, c := range b.Connections {
		bm[c.ComponentID+":"+c.PinNumber] = c.NetID
	}
	for k, v := range bm {
		if am[k] != v {
			d.ChangedConnections = append(d.ChangedConnections, k)
		}
	}
	for id, ca := range ac {
		cb, ok := bc[id]
		if !ok {
			continue
		}
		ap := map[string]bool{}
		for _, p := range ca.Pins {
			ap[p.Number] = p.NoConnected
		}
		bp := map[string]bool{}
		for _, p := range cb.Pins {
			bp[p.Number] = p.NoConnected
		}
		for n, v := range bp {
			if ap[n] != v {
				d.ChangedNoConnect = append(d.ChangedNoConnect, id+":"+n)
			}
		}
		states := map[string]string{}
		for _, p := range ca.Pins {
			states[p.Number] = p.ConnectionState
		}
		for _, p := range cb.Pins {
			if states[p.Number] != p.ConnectionState {
				d.ChangedPinState = append(d.ChangedPinState, id+":"+p.Number)
			}
		}
	}
	for k := range am {
		if _, ok := bm[k]; !ok {
			d.ChangedConnections = append(d.ChangedConnections, k)
		}
	}
	sort.Strings(d.AddedComponents)
	sort.Strings(d.RemovedComponents)
	sort.Strings(d.AddedNets)
	sort.Strings(d.RemovedNets)
	sort.Strings(d.ChangedConnections)
	sort.Strings(d.ChangedNoConnect)
	sort.Strings(d.ChangedPinState)
	return d
}

type Component struct {
	ID           string         `json:"id"`
	UniqueID     string         `json:"uniqueId,omitempty"`
	Ref          string         `json:"ref"`
	Role         string         `json:"role,omitempty"`
	Device       Device         `json:"device"`
	Footprint    string         `json:"footprint,omitempty"`
	FootprintRef map[string]any `json:"footprintRef,omitempty"`
	Pins         []Pin          `json:"pins"`
	Placement    *Placement     `json:"placement,omitempty"`
	PageID       string         `json:"pageId,omitempty"`
	PageName     string         `json:"pageName,omitempty"`
	// SubParts lists the placed units of a multi-unit device. Placement is
	// then nil: each unit keeps its own placement and page here.
	SubParts []SubPart `json:"subParts,omitempty"`
}
type Placement struct {
	// X/Y and orientation are part of the authored 1.4 placement plan. BBox is
	// observed geometry only; it is never replayed as a mutation input.
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Rotation float64 `json:"rotation,omitempty"`
	Mirror   bool    `json:"mirror,omitempty"`
	BBox     *BBox   `json:"bbox,omitempty"`
}
type BBox struct {
	MinX float64 `json:"minX"`
	MinY float64 `json:"minY"`
	MaxX float64 `json:"maxX"`
	MaxY float64 `json:"maxY"`
}
type Device struct {
	LibraryUUID string `json:"libraryUuid,omitempty"`
	UUID        string `json:"deviceUuid"`
	Name        string `json:"name,omitempty"`
}
type Pin struct {
	Number          string  `json:"number"`
	Name            string  `json:"name,omitempty"`
	Type            string  `json:"type,omitempty"`
	NoConnected     bool    `json:"noConnected,omitempty"`
	ConnectionState string  `json:"connectionState,omitempty"`
	X               float64 `json:"x,omitempty"`
	Y               float64 `json:"y,omitempty"`
	// SubPart names the unit that owns this pin on a multi-unit device.
	SubPart string `json:"subPart,omitempty"`
}
type Net struct {
	ID    string `json:"id"`
	Name  string `json:"name,omitempty"`
	Scope string `json:"scope,omitempty"`
	Role  string `json:"role,omitempty"`
}
type Connection struct {
	ComponentID string `json:"componentId"`
	PinNumber   string `json:"pinNumber"`
	NetID       string `json:"netId"`
	Kind        string `json:"kind,omitempty"`
}
type Module struct {
	ID                   string   `json:"id"`
	Name                 string   `json:"name"`
	CoreComponents       []string `json:"coreComponents,omitempty"`
	PeripheralComponents []string `json:"peripheralComponents,omitempty"`
	InternalNets         []string `json:"internalNets,omitempty"`
	Ports                []Port   `json:"ports,omitempty"`
}
type Port struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	NetID   string   `json:"netId"`
	PinRefs []string `json:"pinRefs,omitempty"`
}

func (d *Document) Validate() error {
	if d.SchemaVersion != "1.4" {
		return fmt.Errorf("schemaVersion is required")
	}
	if _, err := d.designatorRegexp(); err != nil {
		return err
	}
	comps := map[string]bool{}
	refs := map[string]bool{}
	pins := map[[2]string]bool{}
	for _, c := range d.Components {
		if c.ID == "" || c.Ref == "" {
			return fmt.Errorf("component id/ref required")
		}
		if comps[c.ID] || refs[c.Ref] {
			return fmt.Errorf("duplicate component %s", c.ID)
		}
		comps[c.ID] = true
		refs[c.Ref] = true
		for _, p := range c.Pins {
			k := [2]string{c.ID, p.Number}
			if p.ConnectionState != "" && p.ConnectionState != "unconnected" {
				return fmt.Errorf("%s.%s has unsupported connectionState %q", c.Ref, p.Number, p.ConnectionState)
			}
			if p.ConnectionState == "unconnected" && (p.NoConnected || connectedPin(d, c.ID, p.Number)) {
				return fmt.Errorf("%s.%s unconnected state conflicts with a net or NC", c.Ref, p.Number)
			}
			if p.Number == "" || pins[k] {
				return fmt.Errorf("empty/duplicate pin on %s", c.Ref)
			}
			pins[k] = true
		}
	}
	nets := map[string]bool{}
	for _, n := range d.Nets {
		if n.ID == "" || nets[n.ID] {
			return fmt.Errorf("duplicate/empty net id")
		}
		nets[n.ID] = true
	}
	seen := map[[2]string]bool{}
	for _, c := range d.Connections {
		k := [2]string{c.ComponentID, c.PinNumber}
		if !pins[k] || seen[k] {
			return fmt.Errorf("unknown/duplicate connection pin %v", k)
		}
		seen[k] = true
		if !comps[c.ComponentID] {
			return fmt.Errorf("connection references unknown component %s", c.ComponentID)
		}
		if !nets[c.NetID] {
			return fmt.Errorf("connection references unknown net %s", c.NetID)
		}
	}
	// Refresh this derived diagnosis instead of accumulating a stale warning on
	// every validate/export cycle or retaining it after the pin is connected.
	issues := d.Issues[:0]
	for _, issue := range d.Issues {
		if issue.Code != "unconnected-pin" {
			issues = append(issues, issue)
		}
	}
	d.Issues = issues
	for _, c := range d.Components {
		for _, p := range c.Pins {
			if !connectedPin(d, c.ID, p.Number) && !p.NoConnected {
				d.Issues = append(d.Issues, Issue{Code: "unconnected-pin", Severity: "warning", ComponentID: c.ID, PinNumber: p.Number, Message: fmt.Sprintf("%s pin %s has no net and no NC marker", c.Ref, p.Number)})
			}
		}
	}
	d.refreshDesignatorIssues()
	return nil
}

func connectedPin(d *Document, id, pin string) bool {
	for _, c := range d.Connections {
		if c.ComponentID == id && c.PinNumber == pin {
			return true
		}
	}
	return false
}
func (d *Document) TopologyHash() (string, error) {
	if err := d.Validate(); err != nil {
		return "", err
	}
	c := append([]Connection(nil), d.Connections...)
	sort.Slice(c, func(i, j int) bool {
		return c[i].ComponentID+c[i].PinNumber+c[i].NetID < c[j].ComponentID+c[j].PinNumber+c[j].NetID
	})
	b, e := json.Marshal(c)
	if e != nil {
		return "", e
	}
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:]), nil
}
