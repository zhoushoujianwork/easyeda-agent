package app

import (
	"encoding/json"
	"strings"
	"testing"
)

// `sch destagger` rolls back when an electrical counter gets worse. `wire-contact`
// joined that set in issue #299's fix, so both the snapshot projection and the
// regression judgment must carry it — otherwise a de-stagger batch could introduce
// a foreign contact and still report success.
func TestElectricalOfCarriesWireContacts(t *testing.T) {
	e := electricalOf(checkSummary{WireContacts: 3, WireCrossings: 1})
	if e.WireContacts != 3 || e.WireCrossings != 1 {
		t.Fatalf("snapshot lost wireContacts: %+v", e)
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"wireContacts":3`) {
		t.Fatalf("snapshot json missing wireContacts: %s", b)
	}
}

func TestDestaggerRegressionsTreatMoreWireContactsAsRegression(t *testing.T) {
	before := destaggerElectrical{WireContacts: 1}
	after := destaggerElectrical{WireContacts: 2}
	got := before.regressions(after)
	if len(got) != 1 || !strings.Contains(got[0], "wireContacts 1→2") {
		t.Fatalf("increased wireContacts must be a regression: %v", got)
	}
	if got := after.regressions(before); len(got) != 0 {
		t.Fatalf("a decrease is a repair, not a regression: %v", got)
	}
	if got := before.regressions(before); len(got) != 0 {
		t.Fatalf("an unchanged counter must not roll back: %v", got)
	}
}
