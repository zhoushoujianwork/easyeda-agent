package daemon

import (
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

func connWith(windowID, project, docType string) *conn {
	c := &conn{windowID: windowID}
	c.ctx = protocol.Context{ProjectName: project, DocumentType: docType}
	return c
}

// TestWindowForProject covers project→windowId routing, including the
// multi-window-per-project case disambiguated by document type (a project open
// in both a schematic and a PCB window).
func TestWindowForProject(t *testing.T) {
	h := &hub{windows: map[string]*conn{
		"w1": connWith("w1", "ceshi", "pcb"),
		"w2": connWith("w2", "motobox", "schematic"),
		"w3": connWith("w3", "motobox", "pcb"), // motobox open in TWO windows
	}}

	cases := []struct {
		name      string
		project   string
		preferDoc string
		wantID    string
		wantFound bool
		wantAmbig bool
	}{
		{"single match", "ceshi", "pcb", "w1", true, false},
		{"match by uuid is also supported", "ceshi", "", "w1", true, false},
		{"no match", "nope", "pcb", "", false, false},
		{"multi-window, prefer pcb", "motobox", "pcb", "w3", true, false},
		{"multi-window, prefer schematic", "motobox", "schematic", "w2", true, false},
		{"multi-window, no preference -> ambiguous", "motobox", "", "", false, true},
		{"multi-window, preference matches none -> ambiguous", "motobox", "panel", "", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, found, ambig := h.windowForProject(tc.project, tc.preferDoc)
			if id != tc.wantID || found != tc.wantFound || ambig != tc.wantAmbig {
				t.Errorf("windowForProject(%q,%q) = (%q,%v,%v), want (%q,%v,%v)",
					tc.project, tc.preferDoc, id, found, ambig, tc.wantID, tc.wantFound, tc.wantAmbig)
			}
		})
	}
}

// TestDocTypeForAction verifies the action→documentType mapping used to
// disambiguate multi-window projects.
func TestDocTypeForAction(t *testing.T) {
	cases := map[string]string{
		"pcb.layers.list":           "pcb",
		"pcb.component.modify":      "pcb",
		"schematic.components.list": "schematic",
		"schematic.wire.create":     "schematic",
		"document.current":          "",
		"project.current":           "",
		"system.health":             "",
	}
	for action, want := range cases {
		if got := docTypeForAction(action); got != want {
			t.Errorf("docTypeForAction(%q) = %q, want %q", action, got, want)
		}
	}
}
