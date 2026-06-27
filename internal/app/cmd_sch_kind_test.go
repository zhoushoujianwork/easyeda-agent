package app

import "testing"

// TestResolveNetflagKind locks the CLI shorthand→canonical mapping so the help
// text and the connector's accepted enum can't drift apart again (issue #6).
func TestResolveNetflagKind(t *testing.T) {
	cases := map[string]string{
		// shorthands → canonical connector enum
		"gnd":     "ground",
		"agnd":    "analog_ground",
		"pgnd":    "protective_ground",
		"netport": "net_port_bi",
		// canonical names pass through unchanged
		"power":             "power",
		"ground":            "ground",
		"analog_ground":     "analog_ground",
		"protective_ground": "protective_ground",
		"protect_ground":    "protect_ground",
		"net_port_in":       "net_port_in",
		"net_port_out":      "net_port_out",
		"net_port_bi":       "net_port_bi",
	}
	for in, want := range cases {
		got, err := resolveNetflagKind(in)
		if err != nil {
			t.Errorf("resolveNetflagKind(%q) unexpected error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("resolveNetflagKind(%q) = %q, want %q", in, got, want)
		}
	}

	// Every value the resolver accepts must be a kind the connector actually
	// supports (NET_FLAG_KINDS ∪ NET_PORT_KINDS in extension/src/actions.ts).
	connectorAccepts := map[string]bool{
		"power": true, "ground": true, "analog_ground": true,
		"protective_ground": true, "protect_ground": true,
		"net_port_in": true, "net_port_out": true, "net_port_bi": true,
	}
	for _, canonical := range netflagKindAliases {
		if !connectorAccepts[canonical] {
			t.Errorf("alias maps to %q which the connector does not accept", canonical)
		}
	}

	// Unknown kinds get a friendly CLI error (not leaked to the connector).
	if _, err := resolveNetflagKind("short"); err == nil {
		t.Error("resolveNetflagKind(\"short\") expected an error, got nil")
	}
	if _, err := resolveNetflagKind("bogus"); err == nil {
		t.Error("resolveNetflagKind(\"bogus\") expected an error, got nil")
	}
}
