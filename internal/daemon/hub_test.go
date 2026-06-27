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

func TestConnectorVersionOK(t *testing.T) {
	tt := []struct {
		conn, daemon, newestPeer string
		want                     *bool // nil = no verdict
	}{
		{"0.5.5", "v0.5.5", "0.5.5", boolp(true)},
		{"v0.5.5", "0.5.5", "0.5.5", boolp(true)},
		{"0.1.0", "0.5.5", "0.5.5", boolp(false)}, // stale vs daemon
		{"0.5.5-dirty", "v0.5.5", "0.5.5", boolp(true)},
		{"dev", "0.5.5", "0.5.5", nil}, // non-semver connector → no verdict
		{"0.5.5", "dev", "0.5.5", nil}, // dev daemon, leads peers → no verdict
		{"", "0.5.5", "0.5.5", nil},    // missing connector version
		{"0.5", "0.5.0", "0.5.0", nil}, // not x.y.z
		{"0.5.x", "0.5.0", "", nil},    // non-numeric component
		// cross-window: behind a peer is stale even when the daemon is non-semver
		{"0.1.0", "dev", "0.5.6", boolp(false)},
		{"0.5.6", "dev", "0.5.6", nil}, // newest peer, dev daemon → no verdict
		{"0.5.6", "v0.5.6", "0.5.6", boolp(true)},
	}
	for _, c := range tt {
		got := connectorVersionOK(c.conn, c.daemon, c.newestPeer)
		if (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
			t.Errorf("connectorVersionOK(%q,%q,%q)=%v, want %v", c.conn, c.daemon, c.newestPeer, fmtBoolp(got), fmtBoolp(c.want))
		}
	}
}

func boolp(b bool) *bool { return &b }
func fmtBoolp(p *bool) string {
	if p == nil {
		return "nil"
	}
	if *p {
		return "true"
	}
	return "false"
}
