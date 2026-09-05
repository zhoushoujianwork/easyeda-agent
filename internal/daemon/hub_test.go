package daemon

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

func connWith(windowID, project, docType string) *conn {
	c := &conn{windowID: windowID}
	c.ctx = protocol.Context{ProjectName: project, DocumentType: docType}
	return c
}

func TestHubPruneStale(t *testing.T) {
	now := time.Now().UTC()
	stale := connWith("stale", "p", "schematic")
	stale.lastSeen = now.Add(-connectorTTL - time.Second)
	fresh := connWith("fresh", "p", "schematic")
	fresh.lastSeen = now
	h := newHub()
	h.windows["stale"] = stale
	h.windows["fresh"] = fresh
	if n := h.pruneStale(now); n != 1 {
		t.Fatalf("pruneStale removed %d, want 1", n)
	}
	if _, ok := h.windows["stale"]; ok {
		t.Fatal("stale registration remains")
	}
	if _, ok := h.windows["fresh"]; !ok {
		t.Fatal("fresh registration removed")
	}
	if _, ok := h.retired["stale"]; !ok {
		t.Fatal("stale identity was not retained for reconnect routing")
	}
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

func TestWindowForProjectReconnectDuplicateUsesNewest(t *testing.T) {
	old := connWith("old", "motobox", "pcb")
	old.connectedAt = time.Unix(10, 0)
	old.ctx.ProjectUUID = "project-1"
	old.ctx.DocumentUUID = "pcb-1"
	old.ctx.TabID = "tab-1"

	newer := connWith("new", "motobox", "pcb")
	newer.connectedAt = time.Unix(20, 0)
	newer.ctx.ProjectUUID = "project-1"
	newer.ctx.DocumentUUID = "pcb-1"
	newer.ctx.TabID = "tab-1"

	h := &hub{windows: map[string]*conn{"old": old, "new": newer}}
	id, found, ambiguous := h.windowForProject("motobox", "pcb")
	if id != "new" || !found || ambiguous {
		t.Fatalf("windowForProject duplicate reconnect = (%q,%v,%v), want (new,true,false)", id, found, ambiguous)
	}
}

func TestWindowForProjectDistinctTabsRemainAmbiguous(t *testing.T) {
	first := connWith("w1", "motobox", "pcb")
	first.ctx.ProjectUUID = "project-1"
	first.ctx.DocumentUUID = "pcb-1"
	first.ctx.TabID = "tab-1"
	second := connWith("w2", "motobox", "pcb")
	second.ctx.ProjectUUID = "project-1"
	second.ctx.DocumentUUID = "pcb-1"
	second.ctx.TabID = "tab-2"

	h := &hub{windows: map[string]*conn{"w1": first, "w2": second}}
	id, found, ambiguous := h.windowForProject("motobox", "pcb")
	if id != "" || found || !ambiguous {
		t.Fatalf("windowForProject distinct tabs = (%q,%v,%v), want (\"\",false,true)", id, found, ambiguous)
	}
}

func TestWindowForProjectIncompleteDuplicateIdentityRemainsAmbiguous(t *testing.T) {
	first := connWith("w1", "motobox", "pcb")
	first.ctx.DocumentUUID = "pcb-1"
	first.ctx.TabID = "tab-1"
	second := connWith("w2", "motobox", "pcb")
	second.ctx.DocumentUUID = "pcb-1"
	second.ctx.TabID = "tab-1"

	h := &hub{windows: map[string]*conn{"w1": first, "w2": second}}
	id, found, ambiguous := h.windowForProject("motobox", "pcb")
	if id != "" || found || !ambiguous {
		t.Fatalf("windowForProject incomplete identity = (%q,%v,%v), want (\"\",false,true)", id, found, ambiguous)
	}
}

func TestWindowForProjectDistinctDocumentsRemainAmbiguous(t *testing.T) {
	first := connWith("w1", "motobox", "pcb")
	first.ctx.ProjectUUID = "project-1"
	first.ctx.DocumentUUID = "pcb-1"
	first.ctx.TabID = "tab-1"
	second := connWith("w2", "motobox", "pcb")
	second.ctx.ProjectUUID = "project-1"
	second.ctx.DocumentUUID = "pcb-2"
	second.ctx.TabID = "tab-1"

	h := &hub{windows: map[string]*conn{"w1": first, "w2": second}}
	id, found, ambiguous := h.windowForProject("motobox", "pcb")
	if id != "" || found || !ambiguous {
		t.Fatalf("windowForProject distinct documents = (%q,%v,%v), want (\"\",false,true)", id, found, ambiguous)
	}
}

func TestTargetReconnectDuplicateUsesNewest(t *testing.T) {
	old := connWith("old", "motobox", "pcb")
	old.connectedAt = time.Unix(10, 0)
	old.ctx.ProjectUUID = "project-1"
	old.ctx.DocumentUUID = "pcb-1"
	old.ctx.TabID = "tab-1"
	newer := connWith("new", "motobox", "pcb")
	newer.connectedAt = time.Unix(20, 0)
	newer.ctx.ProjectUUID = "project-1"
	newer.ctx.DocumentUUID = "pcb-1"
	newer.ctx.TabID = "tab-1"

	h := &hub{windows: map[string]*conn{"old": old, "new": newer}}
	got, ok := h.target("")
	if !ok || got != newer {
		t.Fatalf("target duplicate reconnect = (%p,%v), want (%p,true)", got, ok, newer)
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
		{"dev", "0.5.5", "0.5.5", nil},            // non-semver connector → no verdict
		{"0.5.5", "dev", "0.5.5", nil},            // dev daemon, leads peers → no verdict
		{"", "0.5.5", "0.5.5", nil},               // missing connector version
		{"0.5", "0.5.0", "0.5.0", nil},            // not x.y.z
		{"0.5.x", "0.5.0", "", nil},               // non-numeric component
		// cross-window: behind a peer is stale even when the daemon is non-semver
		{"0.1.0", "dev", "0.5.6", boolp(false)},
		{"0.5.6", "dev", "0.5.6", nil}, // newest peer, dev daemon → no verdict
		{"0.5.6", "v0.5.6", "0.5.6", boolp(true)},
		// git-describe dev daemon must NOT yield a hard verdict (core 0.5.1 is a
		// stale tag, not the real code level) — only a clean release tag does
		{"0.5.7", "v0.5.1-19-ge9552d8", "0.5.7", nil},
		{"0.5.7", "v0.5.1-19-ge9552d8-dirty", "0.5.7", nil},
	}
	for _, c := range tt {
		got := connectorVersionOK(c.conn, c.daemon, c.newestPeer)
		if (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
			t.Errorf("connectorVersionOK(%q,%q,%q)=%v, want %v", c.conn, c.daemon, c.newestPeer, fmtBoolp(got), fmtBoolp(c.want))
		}
	}
}

func TestStaleConnectorNotice(t *testing.T) {
	// Behind the daemon on a clean release → actionable notice.
	if note := staleConnectorNotice("0.8.3", "v0.9.0"); note == "" {
		t.Error("0.8.3 < daemon 0.9.0 should yield a re-import notice")
	} else if !strings.Contains(note, "0.8.3") || !strings.Contains(note, "0.9.0") {
		t.Errorf("notice should name both versions: %q", note)
	}
	// Up to date / ahead → no notice.
	for _, c := range [][2]string{{"0.9.0", "v0.9.0"}, {"0.10.0", "v0.9.0"}} {
		if note := staleConnectorNotice(c[0], c[1]); note != "" {
			t.Errorf("staleConnectorNotice(%q,%q) should be empty, got %q", c[0], c[1], note)
		}
	}
	// Dev daemon (non-clean) → never a hard verdict, even if connector "looks" behind.
	if note := staleConnectorNotice("0.8.3", "v0.9.0-3-gabc-dirty"); note != "" {
		t.Errorf("dev daemon should not emit a stale notice, got %q", note)
	}
	// Non-semver connector → no notice.
	if note := staleConnectorNotice("dev", "v0.9.0"); note != "" {
		t.Errorf("non-semver connector should not emit a notice, got %q", note)
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

// ── stale-windowId re-routing ────────────────────────────────────────────
//
// The connector mints a fresh windowId on every handshake, so a plain page
// refresh silently invalidates whatever id the caller holds. Before this, such
// a request was answered "no EasyEDA connector is available" — false, and it
// sent agents off restarting a daemon that was never down (2026-08-04, user
// report + real-machine confirmation: 7e161cb4… → 72a3e7e5… across one reload).

func connWithDoc(windowID, projectUUID, project, docUUID, docType string) *conn {
	c := &conn{windowID: windowID}
	c.ctx = protocol.Context{
		ProjectUUID:  projectUUID,
		ProjectName:  project,
		DocumentUUID: docUUID,
		DocumentType: docType,
	}
	return c
}

func TestRemoveRetiresTheWindowIdentity(t *testing.T) {
	h := newHub()
	h.add(connWithDoc("old", "p-uuid", "ceshi", "doc-1", "schematic"))
	h.remove("old")

	if _, ok := h.windows["old"]; ok {
		t.Fatal("window must be gone from the live map")
	}
	prev, ok := h.retired["old"]
	if !ok {
		t.Fatal("the retired identity must be kept — after the delete there is no way to recover it")
	}
	if prev.ProjectName != "ceshi" || prev.DocumentUUID != "doc-1" {
		t.Fatalf("retired identity lost detail: %+v", prev)
	}
}

func TestResolveRetiredPrefersTheSameDocument(t *testing.T) {
	// documentUuid survives a refresh (it identifies the page itself), so a
	// window showing the same document is unambiguously the successor — even
	// with another window of the same project also connected.
	h := newHub()
	h.add(connWithDoc("old", "p-uuid", "ceshi", "doc-1", "schematic"))
	h.remove("old")
	h.add(connWithDoc("new", "p-uuid", "ceshi", "doc-1", "schematic"))
	h.add(connWithDoc("other", "p-uuid", "ceshi", "doc-2", "pcb"))

	id, prev, ok := h.resolveRetired("old")
	if !ok || id != "new" {
		t.Fatalf("resolveRetired = (%q,%v), want (\"new\",true)", id, ok)
	}
	if prev.ProjectName != "ceshi" {
		t.Fatalf("previous identity not returned: %+v", prev)
	}
}

func TestResolveRetiredFallsBackToAnUnambiguousProjectMatch(t *testing.T) {
	// The successor may sit on a different page than the one that died.
	h := newHub()
	h.add(connWithDoc("old", "p-uuid", "ceshi", "doc-1", "schematic"))
	h.remove("old")
	h.add(connWithDoc("new", "p-uuid", "ceshi", "doc-9", "pcb"))

	id, _, ok := h.resolveRetired("old")
	if !ok || id != "new" {
		t.Fatalf("resolveRetired = (%q,%v), want (\"new\",true)", id, ok)
	}
}

func TestResolveRetiredRefusesToGuessBetweenTwoWindowsOfTheSameProject(t *testing.T) {
	// A project legitimately open in a schematic AND a PCB window: guessing
	// could land a mutation on the wrong document, which is worse than an
	// honest error.
	h := newHub()
	h.add(connWithDoc("old", "p-uuid", "ceshi", "doc-1", "schematic"))
	h.remove("old")
	h.add(connWithDoc("a", "p-uuid", "ceshi", "doc-7", "schematic"))
	h.add(connWithDoc("b", "p-uuid", "ceshi", "doc-8", "pcb"))

	if id, _, ok := h.resolveRetired("old"); ok {
		t.Fatalf("must not guess a successor, got %q", id)
	}
}

func TestResolveRetiredDoesNotCrossProjects(t *testing.T) {
	h := newHub()
	h.add(connWithDoc("old", "p-uuid", "ceshi", "doc-1", "schematic"))
	h.remove("old")
	h.add(connWithDoc("new", "other-uuid", "motobox", "doc-2", "schematic"))

	if id, _, ok := h.resolveRetired("old"); ok {
		t.Fatalf("a different project must never absorb a retired id, got %q", id)
	}
}

func TestResolveRetiredExpiresAndIsUnknownForFreshIds(t *testing.T) {
	h := newHub()
	h.add(connWithDoc("old", "p-uuid", "ceshi", "doc-1", "schematic"))
	h.remove("old")
	h.add(connWithDoc("new", "p-uuid", "ceshi", "doc-1", "schematic"))

	stale := h.retired["old"]
	stale.RetiredAt = time.Now().UTC().Add(-2 * retiredWindowTTL)
	h.retired["old"] = stale
	if _, _, ok := h.resolveRetired("old"); ok {
		t.Fatal("an expired retirement must not resolve")
	}
	if _, _, ok := h.resolveRetired("never-seen"); ok {
		t.Fatal("an unknown id must not resolve")
	}
}

func TestPruneRetiredHonoursTheCap(t *testing.T) {
	h := newHub()
	for i := 0; i < retiredWindowMax+20; i++ {
		id := fmt.Sprintf("w%03d", i)
		h.add(connWithDoc(id, "p", "ceshi", "d", "schematic"))
		h.remove(id)
	}
	if len(h.retired) > retiredWindowMax {
		t.Fatalf("retired map grew unbounded: %d entries", len(h.retired))
	}
}

func TestLiveWindowSummaryDescribesWhatIsConnected(t *testing.T) {
	h := newHub()
	h.add(connWithDoc("w1", "p", "ceshi", "d1", "schematic"))
	h.add(connWithDoc("w2", "p2", "motobox", "d2", "pcb"))

	n, summary := h.liveWindowSummary()
	if n != 2 {
		t.Fatalf("count = %d, want 2", n)
	}
	for _, want := range []string{"w1", "ceshi", "schematic", "w2", "motobox", "pcb"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q: %s", want, summary)
		}
	}
	if empty, s := (newHub()).liveWindowSummary(); empty != 0 || s != "" {
		t.Fatalf("empty hub must summarise as (0,\"\"), got (%d,%q)", empty, s)
	}
}
