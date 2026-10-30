package app

import (
	"io"
	"reflect"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zhoushoujianwork/easyeda-agent/internal/connectivity"
)

func newSchCompositionSchemaCmd(stdout io.Writer) *cobra.Command {
	return &cobra.Command{Use: "schema", Short: "Print offline input JSON Schemas from the actual composition/edit Go types", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return writeJSON(stdout, schCompositionContract()) }}
}

func schCompositionContract() map[string]any {
	composition := schInputShape(reflect.TypeOf(schCompositionSource{}))
	// Paperless source deliberately omits physical sheet geometry.
	required := []string{}
	for _, key := range composition["required"].([]string) {
		if key != "sheet" {
			required = append(required, key)
		}
	}
	composition["required"] = required
	composition["allOf"] = []any{map[string]any{
		"if":   map[string]any{"properties": map[string]any{"paperless": map[string]any{"const": true}}, "required": []string{"paperless"}},
		"then": map[string]any{"not": map[string]any{"anyOf": []any{map[string]any{"required": []string{"sheet"}}, map[string]any{"required": []string{"sheetBorder"}}, map[string]any{"required": []string{"titleBlock"}}}}},
		"else": map[string]any{"required": []string{"sheet"}},
	}}
	edits := schInputShape(reflect.TypeOf(schConnectivityEdits{}))
	for _, schema := range []map[string]any{composition, edits} {
		schema["$schema"] = "https://json-schema.org/draft/2020-12/schema"
		schema["properties"].(map[string]any)["schemaVersion"].(map[string]any)["const"] = 1
	}
	return map[string]any{
		"schemaVersion": 1, "scope": "accepted-input-shapes; semantic validation still required",
		"compositionSchema": composition, "connectivityEditsSchema": edits,
		"notes": []string{
			"No BOM, device UUIDs, pin coordinates or layout answers are supplied. Read real library and placed-instance facts first.",
			"Schematic geometry uses raw units, y up, normally grid 5. Placements, local pin coordinates, wires and flags must use one consistent module coordinate system.",
			"connectivity.modules declares coreComponents and peripheralComponents as canonical component IDs. Every component belongs to exactly one module; geometry modules use the same module IDs.",
			"Connected pins are represented in connectivity.connections; omit connectionState (or leave it empty). Only explicitly open pins use connectionState:unconnected; do not invent connected/net states.",
			"With preserve-instances, bind canonical component IDs to each actual part's otherProperty[" + connectivity.ComponentIDProperty + "] and retain its actual uniqueId, device and physical pins. Missing binding is not permission to guess it.",
			"connectivityEdits lists each changed physical pin once with componentId/ref/pinNumber/pinName and exact before/after net/noConnected. Desk injects the exact snapshot/source hashes; source evidence is never rewritten.",
			"Required title-block values stay visible. Do not hide Name/Description/Drawed to pass bounds; repair native sheet text through attribute-move with complete original state and x/y only.",
			"JSON Schema describes fields and primitive types, not circuit correctness. Compose still checks identities, complete pins, ownership, real wire trees, measured geometry and protection guards.",
		},
	}
}

// Shape metadata comes from the actual decoder types, never from a copied
// hand-authored field list. Optional pointers describe supplied evidence, not
// an assertion that null is usable engineering data.
func schInputShape(t reflect.Type) map[string]any {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == reflect.TypeOf(schCompositionTitleBlockField{}) {
		return map[string]any{"oneOf": []any{map[string]any{"type": "string", "minLength": 1}, map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"value"},
			"properties": map[string]any{"value": map[string]any{"type": "string", "minLength": 1}, "showTitle": map[string]any{"type": "boolean"}, "showValue": map[string]any{"type": "boolean"}},
		}}}
	}
	switch t.Kind() {
	case reflect.Struct:
		properties := map[string]any{}
		required := []string{}
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if field.PkgPath != "" {
				continue
			}
			tag := strings.Split(field.Tag.Get("json"), ",")
			name := tag[0]
			if name == "-" {
				continue
			}
			if name == "" {
				name = field.Name
			}
			properties[name] = schInputShape(field.Type)
			if !strings.Contains(field.Tag.Get("json"), ",omitempty") {
				required = append(required, name)
			}
		}
		if t == reflect.TypeOf(connectivity.Document{}) {
			properties["schemaVersion"].(map[string]any)["const"] = "1.4"
		}
		if t == reflect.TypeOf(connectivity.Pin{}) {
			properties["connectionState"].(map[string]any)["enum"] = []string{"", "unconnected"}
		}
		return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
	case reflect.Slice, reflect.Array:
		shape := map[string]any{"type": "array", "items": schInputShape(t.Elem())}
		if t.Kind() == reflect.Array {
			shape["minItems"], shape["maxItems"] = t.Len(), t.Len()
		}
		return shape
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": schInputShape(t.Elem())}
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	default:
		return map[string]any{}
	}
}
