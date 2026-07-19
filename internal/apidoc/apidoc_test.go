package apidoc

import "testing"

// TestIndexNamespacesAreRuntimeCallable pins the gen.py attribution fix for
// issue #133 Bug 2: the old generator's class regex required `{` right after the
// class name, so all 57 `class X implements/extends …` bodies fell into the
// previous plain class — 368 methods were lumped under eda.sch_Netlist and 449
// under eda.pcb_Net, and `api search` handed out names that were undefined at
// runtime. The index must speak RUNTIME property names (the `class EDA` surface
// map), including union-typed properties (SCH_PrimitiveComponent |
// SCH_PrimitiveComponent3).
func TestIndexNamespacesAreRuntimeCallable(t *testing.T) {
	has := func(ns, method string) bool {
		for _, r := range loaded.Records {
			if r.NS == ns && r.Method == method {
				return true
			}
		}
		return false
	}
	// The methods issue #133 chased, under their runtime-callable names.
	for _, probe := range []struct{ ns, method string }{
		{"eda.sch_PrimitiveWire", "create"},
		{"eda.sch_PrimitiveComponent", "createNetFlag"}, // union-typed surface property
		{"eda.sch_PrimitiveComponent", "getAll"},
		{"eda.pcb_PrimitiveVia", "create"},
		{"eda.sch_ManufactureData", "getNetlistFile"},
	} {
		if !has(probe.ns, probe.method) {
			t.Errorf("index missing %s.%s — gen.py attribution regressed", probe.ns, probe.method)
		}
	}
	// The lump signature: sch_Netlist really has only getNetlist/setNetlist.
	n := 0
	for _, r := range loaded.Records {
		if r.NS == "eda.sch_Netlist" {
			n++
		}
	}
	if n > 5 {
		t.Errorf("eda.sch_Netlist holds %d methods — the class-regex lumping bug is back", n)
	}
}
