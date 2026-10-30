package app

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestComposeSchemaCommandIsOfflineAndDescribesRealNestedInputs(t *testing.T) {
	var out bytes.Buffer
	cmd := newSchComposeCmd(&out, &out)
	cmd.SetArgs([]string{"schema"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	if err := json.Unmarshal(out.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if data["schemaVersion"] != float64(1) {
		t.Fatal(data)
	}
	props := func(schema any) map[string]any { return schema.(map[string]any)["properties"].(map[string]any) }
	c := props(data["compositionSchema"])
	module := props(c["modules"].(map[string]any)["items"])
	if module["placements"] == nil || module["wires"] == nil || module["flags"] == nil || module["components"] != nil || module["pins"] != nil {
		t.Fatal(module)
	}
	canonical := props(c["connectivity"])
	member := props(canonical["modules"].(map[string]any)["items"])
	if member["coreComponents"] == nil || member["peripheralComponents"] == nil || member["members"] != nil || member["componentIds"] != nil {
		t.Fatal(member)
	}
	component := props(canonical["components"].(map[string]any)["items"])
	if component["uniqueId"] == nil || props(component["device"])["deviceUuid"] == nil {
		t.Fatal(component)
	}
	pin := props(component["pins"].(map[string]any)["items"])
	states := pin["connectionState"].(map[string]any)["enum"].([]any)
	if len(states) != 2 || states[1] != "unconnected" {
		t.Fatal(states)
	}
	edit := props(props(data["connectivityEditsSchema"])["pins"].(map[string]any)["items"])
	for _, key := range []string{"componentId", "ref", "pinNumber", "pinName", "before", "after"} {
		if edit[key] == nil {
			t.Fatal("missing edit key", key)
		}
	}
	if edit["pin"] != nil || edit["net"] != nil {
		t.Fatal(edit)
	}
	if props(edit["before"])["noConnected"].(map[string]any)["type"] != "boolean" {
		t.Fatal(edit["before"])
	}
	if len(data["notes"].([]any)) == 0 {
		t.Fatal("semantic boundaries missing")
	}
}
