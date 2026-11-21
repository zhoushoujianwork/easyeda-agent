package app

import (
	"encoding/json"
	"testing"
)

// A platform-generated net name is renumbered on redraw: the expectation keeps
// the grouping, not the name.
func TestSchematicAutoNetChecksGroupingNotName(t *testing.T) {
	const expected = `{"parts":{"R1":{"pins":{"1":{"autoNet":"$2N1","noConnected":false},"2":{"net":"VCC","noConnected":false}}},` +
		`"R2":{"pins":{"1":{"autoNet":"$2N1","noConnected":false},"2":{"autoNet":"$2N2","noConnected":false}}}}}`
	result := func(r11, r12, r21, r22 string) map[string]any {
		var r map[string]any
		raw := `{"components":[{"componentType":"part","designator":"R1","pinsAvailable":true,"pins":[{"pinNumber":"1","net":"` + r11 + `","noConnected":false},{"pinNumber":"2","net":"` + r12 + `","noConnected":false}]},` +
			`{"componentType":"part","designator":"R2","pinsAvailable":true,"pins":[{"pinNumber":"1","net":"` + r21 + `","noConnected":false},{"pinNumber":"2","net":"` + r22 + `","noConnected":false}]}]}`
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	var e schematicStateExpectation
	if err := json.Unmarshal([]byte(expected), &e); err != nil {
		t.Fatal(err)
	}
	if err := e.check(result("$2N9", "VCC", "$2N9", "$2N10"), nil); err != nil {
		t.Fatalf("renumbered auto nets rejected: %v", err)
	}
	for name, r := range map[string]map[string]any{
		"group split":      result("$2N9", "VCC", "$2N8", "$2N10"),
		"groups merged":    result("$2N9", "VCC", "$2N9", "$2N9"),
		"joined named net": result("VCC", "VCC", "VCC", "$2N10"),
		"disconnected":     result("", "VCC", "$2N9", "$2N10"),
	} {
		if err := e.check(r, nil); err == nil {
			t.Fatalf("%s passed", name)
		}
	}
	var bad schematicStateExpectation
	if err := json.Unmarshal([]byte(`{"parts":{"R1":{"pins":{"1":{"autoNet":"$2N1","net":"VCC"}}}}}`), &bad); err == nil {
		if err = bad.validate(); err == nil {
			t.Fatal("autoNet together with net accepted")
		}
	}
}
