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
	SchemaVersion string       `json:"schemaVersion"`
	ProjectID     string       `json:"projectId,omitempty"`
	Components    []Component  `json:"components"`
	Nets          []Net        `json:"nets"`
	Connections   []Connection `json:"connections"`
	Modules       []Module     `json:"modules,omitempty"`
}
type Component struct {
	ID, Ref   string `json:"id","ref"`
	Device    Device `json:"device"`
	Footprint string `json:"footprint,omitempty"`
	Pins      []Pin  `json:"pins"`
}
type Device struct {
	LibraryUUID string `json:"libraryUuid,omitempty"`
	UUID        string `json:"deviceUuid"`
	Name        string `json:"name,omitempty"`
}
type Pin struct {
	Number string `json:"number"`
	Name   string `json:"name,omitempty"`
	Type   string `json:"type,omitempty"`
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
	if d.SchemaVersion == "" {
		return fmt.Errorf("schemaVersion is required")
	}
	comps := map[string]bool{}
	refs := map[string]bool{}
	for _, c := range d.Components {
		if c.ID == "" || c.Ref == "" {
			return fmt.Errorf("component id/ref required")
		}
		if comps[c.ID] || refs[c.Ref] {
			return fmt.Errorf("duplicate component %s", c.ID)
		}
		comps[c.ID] = true
		refs[c.Ref] = true
	}
	nets := map[string]bool{}
	for _, n := range d.Nets {
		if n.ID == "" || nets[n.ID] {
			return fmt.Errorf("duplicate/empty net id")
		}
		nets[n.ID] = true
	}
	for _, c := range d.Connections {
		if !comps[c.ComponentID] {
			return fmt.Errorf("connection references unknown component %s", c.ComponentID)
		}
		if !nets[c.NetID] {
			return fmt.Errorf("connection references unknown net %s", c.NetID)
		}
	}
	return nil
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
