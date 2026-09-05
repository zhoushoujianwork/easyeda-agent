package connectivity

import (
	"encoding/json"
	"testing"
)

func TestSnapshot(t *testing.T) {
	var m map[string]any
	json.Unmarshal([]byte(`{"components":[{"componentType":"sheet"},{"componentType":"part","designator":"U1","pins":[{"number":"1","net":"Z"},{"number":"2","net":"A"}]}]}`), &m)
	d, e := FromRead(m)
	if e != nil {
		t.Fatal(e)
	}
	p, e := d.NamedPins()
	if e != nil {
		t.Fatal(e)
	}
	if *p[`"U1"/"1"`] != "Z" || *p[`"U1"/"2"`] != "A" {
		t.Fatal(p)
	}
	b := d
	b.Connections = b.Connections[:1]
	diff := Compare(d, b)
	if len(diff.ChangedConnections) != 1 {
		t.Fatal(diff)
	}
	d.Connections[0].PinNumber = "404"
	if d.Validate() == nil {
		t.Fatal("unknown pin accepted")
	}
}
func TestRejectShallowRead(t *testing.T) {
	_, e := FromRead(map[string]any{"components": []any{map[string]any{"componentType": "part", "designator": "U1", "pins": []any{}}}})
	if e == nil {
		t.Fatal("shallow read accepted")
	}
}
