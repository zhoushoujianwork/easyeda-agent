package protocol

type Domain string

const (
	DomainProject   Domain = "project"
	DomainDocument  Domain = "document"
	DomainSchematic Domain = "schematic"
	DomainArtifact  Domain = "artifact"
	DomainSystem    Domain = "system"
	DomainDebug     Domain = "debug"
)

type ActionSpec struct {
	Name         string   `json:"name"`
	Domain       Domain   `json:"domain"`
	Phase        int      `json:"phase"`
	Mutates      bool     `json:"mutates"`
	NeedsWindow  bool     `json:"needsWindow"`
	NeedsConfirm bool     `json:"needsConfirm"`
	Description  string   `json:"description"`
	Inputs       []string `json:"inputs,omitempty"`
	Outputs      []string `json:"outputs,omitempty"`
	VerifyWith   []string `json:"verifyWith,omitempty"`
}

func Phase1Actions() []ActionSpec {
	return []ActionSpec{
		{
			Name:        "system.health",
			Domain:      DomainSystem,
			Phase:       1,
			Description: "Check Go daemon and EasyEDA connector availability.",
			Outputs:     []string{"daemon status", "connected windows", "active window"},
		},
		{
			Name:        "project.current",
			Domain:      DomainProject,
			Phase:       1,
			NeedsWindow: true,
			Description: "Read current EasyEDA project information.",
			Outputs:     []string{"project uuid", "project name", "team/workspace context"},
		},
		{
			Name:        "document.current",
			Domain:      DomainDocument,
			Phase:       1,
			NeedsWindow: true,
			Description: "Read active editor document and schematic page context.",
			Outputs:     []string{"document uuid", "document type", "tab id"},
		},
		{
			Name:        "schematic.pages.list",
			Domain:      DomainSchematic,
			Phase:       1,
			NeedsWindow: true,
			Description: "List schematic documents and schematic pages in the current project.",
			Outputs:     []string{"schematic uuid list", "schematic page uuid list"},
		},
		{
			Name:        "schematic.page.open",
			Domain:      DomainSchematic,
			Phase:       1,
			Mutates:     false,
			NeedsWindow: true,
			Description: "Open or activate a schematic page by uuid.",
			Inputs:      []string{"schematicPageUuid"},
			Outputs:     []string{"tab id", "current document"},
		},
		{
			Name:        "schematic.components.list",
			Domain:      DomainSchematic,
			Phase:       1,
			NeedsWindow: true,
			Description: "List components on the active schematic page.",
			Inputs:      []string{"allPages optional"},
			Outputs:     []string{"component primitives", "designator", "name", "pins"},
		},
		{
			Name:        "schematic.component.place",
			Domain:      DomainSchematic,
			Phase:       1,
			Mutates:     true,
			NeedsWindow: true,
			Description: "Place a device/component from library identity at coordinates.",
			Inputs:      []string{"libraryUuid", "uuid", "x", "y", "rotation optional", "mirror optional"},
			Outputs:     []string{"primitive id", "component state"},
			VerifyWith:  []string{"schematic.component.get", "schematic.snapshot"},
		},
		{
			Name:        "schematic.component.modify",
			Domain:      DomainSchematic,
			Phase:       1,
			Mutates:     true,
			NeedsWindow: true,
			Description: "Modify component position, designator, name, BOM flags, or custom properties.",
			Inputs:      []string{"primitiveId", "patch"},
			Outputs:     []string{"component state"},
			VerifyWith:  []string{"schematic.component.get"},
		},
		{
			Name:         "schematic.component.delete",
			Domain:       DomainSchematic,
			Phase:        1,
			Mutates:      true,
			NeedsWindow:  true,
			NeedsConfirm: true,
			Description:  "Delete schematic component primitives.",
			Inputs:       []string{"primitiveIds"},
			Outputs:      []string{"deleted"},
			VerifyWith:   []string{"schematic.components.list"},
		},
		{
			Name:        "schematic.wire.create",
			Domain:      DomainSchematic,
			Phase:       1,
			Mutates:     true,
			NeedsWindow: true,
			Description: "Create a schematic wire polyline.",
			Inputs:      []string{"points", "net optional", "style optional"},
			Outputs:     []string{"primitive id", "wire state"},
			VerifyWith:  []string{"schematic.primitive.get", "schematic.snapshot"},
		},
		{
			Name:        "schematic.netflag.create",
			Domain:      DomainSchematic,
			Phase:       1,
			Mutates:     true,
			NeedsWindow: true,
			Description: "Create power, ground, analog ground, protective ground, net port, or short-circuit flag.",
			Inputs:      []string{"kind", "net", "x", "y", "rotation optional"},
			Outputs:     []string{"primitive id", "component state"},
			VerifyWith:  []string{"schematic.snapshot"},
		},
		{
			Name:        "schematic.library.search",
			Domain:      DomainSchematic,
			Phase:       1,
			NeedsWindow: true,
			Description: "Search the EasyEDA device library by free-text query (e.g. '1kΩ 0603', '0.1uF X7R', 'tact button'). Returns a ranked list of matching devices with their libraryUuid + uuid ready for schematic.component.place. Replaces ad-hoc debug.exec_js lookups.",
			Inputs:      []string{"query", "limit optional"},
			Outputs:     []string{"components[].libraryUuid", "components[].uuid", "components[].name", "components[].value", "components[].footprintName", "components[].lcsc", "components[].description"},
		},
		{
			Name:        "schematic.power.connect_pin",
			Domain:      DomainSchematic,
			Phase:       1,
			Mutates:     true,
			NeedsWindow: true,
			Description: "Composite: draw a short wire out of a pin and place a netflag / netport at its far end in one call. Prevents the 'netflag overlaps pin' DRC fatal by structurally requiring the wire offset. Default direction is inferred from kind (power=up, ground=down, net_port_in=left, net_port_out/bi=right); default offset is 30 units.",
			Inputs:      []string{"pinX", "pinY", "kind", "net", "direction optional", "offset optional"},
			Outputs:     []string{"wire primitiveId", "flag primitiveId", "end point"},
			VerifyWith:  []string{"schematic.snapshot", "schematic.drc.check"},
		},
		{
			Name:        "schematic.select",
			Domain:      DomainSchematic,
			Phase:       1,
			NeedsWindow: true,
			Description: "Select schematic primitives by id and return the active selection.",
			Inputs:      []string{"primitiveIds"},
			Outputs:     []string{"selected primitive ids"},
		},
		{
			Name:        "schematic.snapshot",
			Domain:      DomainSchematic,
			Phase:       1,
			NeedsWindow: true,
			Description: "Capture current rendered area image as an artifact.",
			Outputs:     []string{"artifact id", "file path", "mime type"},
		},
		{
			Name:        "schematic.drc.check",
			Domain:      DomainSchematic,
			Phase:       1,
			NeedsWindow: true,
			Description: "Run schematic DRC and normalize the result.",
			Inputs:      []string{"strict", "includeVerboseError"},
			Outputs:     []string{"passed", "violations"},
		},
		{
			Name:        "schematic.save",
			Domain:      DomainSchematic,
			Phase:       1,
			Mutates:     true,
			NeedsWindow: true,
			Description: "Save active schematic document.",
			Outputs:     []string{"saved"},
		},
		{
			Name:        "schematic.export.netlist",
			Domain:      DomainArtifact,
			Phase:       1,
			NeedsWindow: true,
			Description: "Export schematic netlist as an artifact.",
			Inputs:      []string{"netlistType optional"},
			Outputs:     []string{"artifact id", "file path", "netlist type"},
		},
		{
			Name:        "schematic.export.bom",
			Domain:      DomainArtifact,
			Phase:       1,
			NeedsWindow: true,
			Description: "Export schematic BOM as csv or xlsx artifact.",
			Inputs:      []string{"fileType", "template optional", "columns optional"},
			Outputs:     []string{"artifact id", "file path", "file type"},
		},
		{
			Name:         "debug.exec_js",
			Domain:       DomainDebug,
			Phase:        1,
			Mutates:      true,
			NeedsWindow:  true,
			NeedsConfirm: true,
			Description:  "Run raw eda.* JavaScript in the connector. Escape hatch for operations without a typed action; confirmation-gated, not for normal workflows.",
			Inputs:       []string{"code"},
			Outputs:      []string{"value"},
		},
	}
}
