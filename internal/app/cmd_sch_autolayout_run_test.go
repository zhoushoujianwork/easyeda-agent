package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type autolayoutTestCall struct {
	Action    string
	Payload   map[string]any
	TimeoutMs int
}

type autolayoutTestDaemon struct {
	mu    sync.Mutex
	calls []autolayoutTestCall
}

func (d *autolayoutTestDaemon) snapshot() []autolayoutTestCall {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]autolayoutTestCall(nil), d.calls...)
}

// newAutolayoutTestDaemon provides the minimum daemon surface needed by the
// orchestration safety tests. responder returns a complete /action JSON body.
func newAutolayoutTestDaemon(t *testing.T, responder func(int, autolayoutTestCall) string) (*appConfig, *autolayoutTestDaemon, func()) {
	t.Helper()
	state := &autolayoutTestDaemon{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_, _ = w.Write([]byte(`{"service":"easyeda-agent","windows":[{"windowId":"w1"}]}`))
		case "/action":
			var body struct {
				Action    string         `json:"action"`
				Payload   map[string]any `json:"payload"`
				TimeoutMs int            `json:"timeoutMs"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode action request: %v", err)
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			call := autolayoutTestCall{Action: body.Action, Payload: body.Payload, TimeoutMs: body.TimeoutMs}
			state.mu.Lock()
			state.calls = append(state.calls, call)
			idx := len(state.calls) - 1
			state.mu.Unlock()
			resp := responder(idx, call)
			if resp == "" {
				resp = `{"ok":true,"result":{}}`
			}
			_, _ = w.Write([]byte(resp))
		default:
			http.NotFound(w, r)
		}
	}))

	hostPort := strings.TrimPrefix(srv.URL, "http://")
	host, portText, _ := strings.Cut(hostPort, ":")
	port, err := strconv.Atoi(portText)
	if err != nil {
		srv.Close()
		t.Fatalf("parse test daemon port: %v", err)
	}
	return &appConfig{host: host, ports: fmt.Sprintf("%d-%d", port, port)}, state, srv.Close
}

func autolayoutOK(docUUID, resultJSON string) string {
	context := ""
	if docUUID != "" {
		context = fmt.Sprintf(`,"context":{"documentUuid":%q,"documentType":"schematic"}`, docUUID)
	}
	return `{"ok":true,"result":` + resultJSON + context + `}`
}

func autolayoutTargetMetadata(call autolayoutTestCall, docUUID, docName string) (string, bool) {
	switch call.Action {
	case "document.current":
		return autolayoutOK(docUUID, fmt.Sprintf(`{"uuid":%q}`, docUUID)), true
	case "schematic.pages.list":
		return autolayoutOK(docUUID, fmt.Sprintf(`{"pages":[{"uuid":%q,"name":%q}]}`, docUUID, docName)), true
	case "pcb.documents.list":
		return autolayoutOK(docUUID, `{"pcbs":[]}`), true
	default:
		return "", false
	}
}

func autolayoutScene(docUUID string, anchorX, anchorY float64, wires int) string {
	return autolayoutOK(docUUID, fmt.Sprintf(`{"components":[
		{"componentType":"sheet","bbox":{"minX":0,"minY":0,"maxX":1000,"maxY":800}},
		{"componentType":"part","designator":"U1","primitiveId":"id-U1","x":%v,"y":%v,"rotation":0,
		 "bbox":{"minX":%v,"minY":%v,"maxX":%v,"maxY":%v},"pinsAvailable":true,
		 "pins":[{"pinNumber":"1","x":%v,"y":%v}]}
	],"count":2,"connectivitySummary":{"scope":"activePage","wires":%d,"buses":0,"netflags":0,"netports":0,"netlabels":0,"shortSymbols":0}}`,
		anchorX, anchorY,
		anchorX-10, anchorY-10, anchorX+10, anchorY+10,
		anchorX-10, anchorY, wires))
}

// TestBuildAutolayoutZoneClaims (issue #142): spec modules → schematic zone
// claims. Modules without a zone or parts are skipped; an unknown zone name is
// skipped (never aborts) so a successful --apply still draws the valid zones.
func TestBuildAutolayoutZoneClaims(t *testing.T) {
	mods := []alSpecModule{
		{Name: "POWER", Zone: "left-top", Parts: []string{"U1", "C1"}},
		{Name: "MCU", Zone: "CENTER", Parts: []string{"U2"}},                // case-insensitive
		{Name: "NOZONE", Zone: "", Parts: []string{"R1"}},                   // skipped: no zone
		{Name: "NOPARTS", Zone: "right", Parts: nil},                        // skipped: no parts
		{Name: "BADZONE", Zone: "middle-of-nowhere", Parts: []string{"D1"}}, // skipped: unknown zone
	}
	claims := buildAutolayoutZoneClaims(mods, io.Discard)
	if len(claims) != 2 {
		t.Fatalf("got %d claims, want 2 (POWER + MCU): %+v", len(claims), claims)
	}
	if claims["POWER"] == nil || claims["POWER"].Zone != "left-top" {
		t.Errorf("POWER claim wrong: %+v", claims["POWER"])
	}
	if claims["MCU"] == nil || claims["MCU"].Zone != "center" {
		t.Errorf("MCU zone not lowercased: %+v", claims["MCU"])
	}
	if claims["MCU"].Note != "autolayout" {
		t.Errorf("MCU note = %q, want autolayout", claims["MCU"].Note)
	}
	for _, bad := range []string{"NOZONE", "NOPARTS", "BADZONE"} {
		if _, ok := claims[bad]; ok {
			t.Errorf("%s should have been skipped", bad)
		}
	}
}

func TestRunAutolayoutApplyRefusesWiredPageBeforePlanning(t *testing.T) {
	cfg, daemon, cleanup := newAutolayoutTestDaemon(t, func(_ int, call autolayoutTestCall) string {
		if response, ok := autolayoutTargetMetadata(call, "page-1", "Power"); ok {
			return response
		}
		if call.Action == "schematic.components.list" {
			return autolayoutOK("page-1", `{"components":[],"count":0,"connectivitySummary":{"scope":"activePage","wires":3,"buses":0,"netflags":0,"netports":0,"netlabels":0,"shortSymbols":0}}`)
		}
		return autolayoutOK("page-1", `{}`)
	})
	defer cleanup()

	err := runAutolayout(cfg, "", alSpec{Page: "page-1"}, defaultAutolayoutRules(), true, false, false, false, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "wires=3") || !strings.Contains(err.Error(), "before planning") {
		t.Fatalf("got error %v, want wired-page refusal", err)
	}
	calls := daemon.snapshot()
	for _, call := range calls {
		if call.Action == "schematic.component.modify" || call.Action == "debug.exec_js" {
			t.Fatalf("read-only connectivity guard must stop before mutation/debug execution; calls=%+v", calls)
		}
	}
}

func TestRunAutolayoutApplyFailsClosedWhenWireCountUnavailable(t *testing.T) {
	cfg, daemon, cleanup := newAutolayoutTestDaemon(t, func(_ int, call autolayoutTestCall) string {
		if response, ok := autolayoutTargetMetadata(call, "page-1", "Power"); ok {
			return response
		}
		return autolayoutOK("page-1", `{"components":[],"count":0}`)
	})
	defer cleanup()

	err := runAutolayout(cfg, "", alSpec{Page: "page-1"}, defaultAutolayoutRules(), true, false, false, false, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "omitted connectivitySummary") {
		t.Fatalf("got error %v, want fail-closed connectivity-summary error", err)
	}
	calls := daemon.snapshot()
	for _, call := range calls {
		if call.Action == "schematic.component.modify" {
			t.Fatalf("unavailable summary must stop before mutation; calls=%+v", calls)
		}
	}
}

func TestRunAutolayoutApplyFailsClosedOnMalformedWireCount(t *testing.T) {
	cfg, daemon, cleanup := newAutolayoutTestDaemon(t, func(_ int, call autolayoutTestCall) string {
		if response, ok := autolayoutTargetMetadata(call, "page-1", "Power"); ok {
			return response
		}
		return autolayoutOK("page-1", `{"components":[],"count":0,"connectivitySummary":{"scope":"activePage","wires":"unknown","buses":0,"netflags":0,"netports":0,"netlabels":0,"shortSymbols":0}}`)
	})
	defer cleanup()

	err := runAutolayout(cfg, "", alSpec{Page: "page-1"}, defaultAutolayoutRules(), true, false, false, false, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "connectivitySummary.wires") {
		t.Fatalf("got error %v, want malformed-count refusal", err)
	}
	for _, call := range daemon.snapshot() {
		if call.Action == "schematic.component.modify" {
			t.Fatalf("malformed count must stop before mutation; calls=%+v", daemon.snapshot())
		}
	}
}

func TestRunAutolayoutApplyAllPagesFailsClosed(t *testing.T) {
	err := runAutolayout(&appConfig{}, "", alSpec{}, defaultAutolayoutRules(), true, true, false, false, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "cannot be combined with --all-pages") {
		t.Fatalf("got error %v, want active-page-scope refusal", err)
	}
}

func TestRunAutolayoutApplyRequiresExplicitTargetPage(t *testing.T) {
	cfg, daemon, cleanup := newAutolayoutTestDaemon(t, func(_ int, call autolayoutTestCall) string {
		return autolayoutOK("", `{}`)
	})
	defer cleanup()
	err := runAutolayout(cfg, "", alSpec{}, defaultAutolayoutRules(), true, false, false, false, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "requires a target page") {
		t.Fatalf("error=%v, want explicit target-page refusal", err)
	}
	if len(daemon.snapshot()) != 0 {
		t.Fatalf("target refusal should happen before any action call: %+v", daemon.snapshot())
	}
}

func TestRunAutolayoutRejectsResponsePageDrift(t *testing.T) {
	cfg, daemon, cleanup := newAutolayoutTestDaemon(t, func(_ int, call autolayoutTestCall) string {
		if response, ok := autolayoutTargetMetadata(call, "page-1", "Power"); ok {
			return response
		}
		if call.Action == "schematic.components.list" {
			return autolayoutScene("page-2", 100, 100, 0)
		}
		return autolayoutOK("page-1", `{}`)
	})
	defer cleanup()

	spec := alSpec{Page: "page-1", Modules: []alSpecModule{{Name: "MCU", Zone: "center", Core: "U1", Parts: []string{"U1"}}}}
	err := runAutolayout(cfg, "", spec, defaultAutolayoutRules(), true, false, false, false, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "page drift") {
		t.Fatalf("error=%v, want response-context page drift refusal", err)
	}
	for _, call := range daemon.snapshot() {
		if call.Action == "schematic.component.modify" {
			t.Fatalf("page drift reached mutation: %+v", daemon.snapshot())
		}
	}
}

func TestRunAutolayoutRejectsGeometryDriftAfterPlanning(t *testing.T) {
	sceneRead := 0
	cfg, daemon, cleanup := newAutolayoutTestDaemon(t, func(_ int, call autolayoutTestCall) string {
		if response, ok := autolayoutTargetMetadata(call, "page-1", "Power"); ok {
			return response
		}
		if call.Action == "schematic.components.list" {
			sceneRead++
			if sceneRead == 1 {
				return autolayoutScene("page-1", 100, 100, 0)
			}
			return autolayoutScene("page-1", 105, 100, 0)
		}
		return autolayoutOK("page-1", `{}`)
	})
	defer cleanup()

	spec := alSpec{Page: "page-1", Modules: []alSpecModule{{Name: "MCU", Zone: "center", Core: "U1", Parts: []string{"U1"}}}}
	err := runAutolayout(cfg, "", spec, defaultAutolayoutRules(), true, false, false, false, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "page changed after planning") {
		t.Fatalf("error=%v, want stale-input refusal", err)
	}
	for _, call := range daemon.snapshot() {
		if call.Action == "schematic.component.modify" {
			t.Fatalf("stale plan reached mutation: %+v", daemon.snapshot())
		}
	}
}

func TestRunAutolayoutRechecksWiresImmediatelyBeforeApply(t *testing.T) {
	summaryReads := 0
	cfg, daemon, cleanup := newAutolayoutTestDaemon(t, func(_ int, call autolayoutTestCall) string {
		if response, ok := autolayoutTargetMetadata(call, "page-1", "Power"); ok {
			return response
		}
		if call.Action == "schematic.components.list" {
			summaryReads++
			wires := 0
			if summaryReads == 2 {
				wires = 2
			}
			return autolayoutOK("page-1", fmt.Sprintf(`{"components":[
				{"componentType":"sheet","bbox":{"minX":0,"minY":0,"maxX":1000,"maxY":800}},
				{"componentType":"part","designator":"U1","primitiveId":"id-U1","x":100,"y":100,"rotation":0,
				 "bbox":{"minX":90,"minY":90,"maxX":110,"maxY":110},"pinsAvailable":true,"pins":[]}
			],"count":2,"connectivitySummary":{"scope":"activePage","wires":%d,"buses":0,"netflags":0,"netports":0,"netlabels":0,"shortSymbols":0}}`, wires))
		}
		return autolayoutOK("page-1", `{}`)
	})
	defer cleanup()

	spec := alSpec{Page: "page-1", Modules: []alSpecModule{{Name: "MCU", Zone: "center", Core: "U1", Parts: []string{"U1"}}}}
	err := runAutolayout(cfg, "", spec, defaultAutolayoutRules(), true, false, false, false, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "wires=2") || !strings.Contains(err.Error(), "immediately before apply") {
		t.Fatalf("got error %v, want pre-mutation recheck refusal", err)
	}
	calls := daemon.snapshot()
	for _, call := range calls {
		if call.Action == "schematic.component.modify" {
			t.Fatalf("pre-apply connectivity recheck must prevent mutation; calls=%+v", calls)
		}
	}
}

func TestAutolayoutConnectivityCountsShortSymbols(t *testing.T) {
	summary, err := parseAutolayoutConnectivity(map[string]any{
		"connectivitySummary": map[string]any{
			"scope": "activePage", "wires": 0, "buses": 0,
			"netflags": 0, "netports": 0, "netlabels": 0, "shortSymbols": 1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := rejectConnectedTemplatePage(summary, "before planning"); err == nil || !strings.Contains(err.Error(), "shortSymbols=1") {
		t.Fatalf("short_symbol did not block template apply: %v", err)
	}
}

func TestRunAutolayoutRequiresSheetEvenWhenTitleBlockAvoidanceDisabled(t *testing.T) {
	cfg, _, cleanup := newAutolayoutTestDaemon(t, func(_ int, call autolayoutTestCall) string {
		if response, ok := autolayoutTargetMetadata(call, "page-1", "Power"); ok {
			return response
		}
		if call.Action == "schematic.components.list" {
			return autolayoutOK("page-1", `{"components":[
				{"componentType":"part","designator":"U1","primitiveId":"id-U1","x":100,"y":100,"rotation":0,
				 "bbox":{"minX":90,"minY":90,"maxX":110,"maxY":110},"pinsAvailable":true,"pins":[]}
			],"count":1,"connectivitySummary":{"scope":"activePage","wires":0,"buses":0,"netflags":0,"netports":0,"netlabels":0,"shortSymbols":0}}`)
		}
		return autolayoutOK("page-1", `{}`)
	})
	defer cleanup()

	rules := defaultAutolayoutRules()
	rules.AvoidTitleBlock = false
	spec := alSpec{Page: "page-1", Modules: []alSpecModule{{Name: "MCU", Zone: "center", Core: "U1", Parts: []string{"U1"}}}}
	err := runAutolayout(cfg, "", spec, rules, true, false, false, false, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "no sheet bbox") {
		t.Fatalf("sheetless apply error=%v", err)
	}
}

func TestAutolayoutTemplateRejectsRewireFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	window := ""
	cmd := newAutolayoutCmd(&appConfig{}, &window, &stdout, &stderr)
	cmd.SetArgs([]string{"--engine", "template", "--rewire"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "template has no safe capture+rewire path") {
		t.Fatalf("got error %v, want explicit no-fake-override refusal", err)
	}
}

func TestApplyAutolayoutRollsBackAttemptedMoveOnModifyFailure(t *testing.T) {
	modifyCall := 0
	cfg, daemon, cleanup := newAutolayoutTestDaemon(t, func(_ int, call autolayoutTestCall) string {
		if call.Action == "schematic.component.modify" {
			modifyCall++
			if modifyCall == 2 {
				return `{"ok":false,"error":{"message":"injected modify failure"}}`
			}
		}
		if call.Action == "schematic.components.list" {
			return autolayoutOK("", `{"components":[
				{"componentType":"part","designator":"U1","primitiveId":"id-U1","x":10,"y":20,"rotation":0},
				{"componentType":"part","designator":"U2","primitiveId":"id-U2","x":30,"y":40,"rotation":0}
			],"count":2}`)
		}
		if call.Action == "schematic.save" {
			return autolayoutOK("", `{"saved":true}`)
		}
		return `{"ok":true,"result":{}}`
	})
	defer cleanup()

	rep := alReport{OK: true, Placements: []alPlacement{
		{Designator: "U1", PrimitiveID: "id-U1", X: 100, Y: 200, OriginalX: 10, OriginalY: 20, HasOriginal: true},
		{Designator: "U2", PrimitiveID: "id-U2", X: 300, Y: 400, OriginalX: 30, OriginalY: 40, HasOriginal: true},
	}}
	applyAutolayout(cfg, "", &rep, io.Discard)

	if rep.OK {
		t.Fatal("modify failure must fail the report")
	}
	if !strings.Contains(rep.Note, "rollback confirmed 2/2") {
		t.Fatalf("note=%q, want fully verified rollback", rep.Note)
	}
	calls := daemon.snapshot()
	if len(calls) != 6 {
		t.Fatalf("calls=%+v, want 2 attempts + 2 reverse rollback calls + readback + save", calls)
	}
	assertAutolayoutPatch(t, calls[2], "id-U2", 30, 40)
	assertAutolayoutPatch(t, calls[3], "id-U1", 10, 20)
	if calls[4].Action != "schematic.components.list" || calls[5].Action != "schematic.save" {
		t.Fatalf("rollback was not read back and saved: %+v", calls)
	}
}

func TestApplyAutolayoutRollsBackWhenPostCheckFails(t *testing.T) {
	listCall := 0
	cfg, daemon, cleanup := newAutolayoutTestDaemon(t, func(_ int, call autolayoutTestCall) string {
		if call.Action == "schematic.components.list" {
			listCall++
			if listCall == 1 {
				return `{"ok":false,"error":{"message":"injected post-check failure"}}`
			}
			return autolayoutOK("", `{"components":[
				{"componentType":"part","designator":"U1","primitiveId":"id-U1","x":10,"y":20,"rotation":0},
				{"componentType":"part","designator":"U2","primitiveId":"id-U2","x":30,"y":40,"rotation":0}
			],"count":2}`)
		}
		if call.Action == "schematic.save" {
			return autolayoutOK("", `{"saved":true}`)
		}
		return `{"ok":true,"result":{}}`
	})
	defer cleanup()

	rep := alReport{OK: true, Placements: []alPlacement{
		{Designator: "U1", PrimitiveID: "id-U1", X: 100, Y: 200, OriginalX: 10, OriginalY: 20, HasOriginal: true},
		{Designator: "U2", PrimitiveID: "id-U2", X: 300, Y: 400, OriginalX: 30, OriginalY: 40, HasOriginal: true},
	}}
	applyAutolayout(cfg, "", &rep, io.Discard)

	if rep.OK {
		t.Fatal("post-check failure must fail the report")
	}
	if !strings.Contains(rep.Note, "rollback confirmed 2/2") {
		t.Fatalf("note=%q, want fully verified rollback", rep.Note)
	}
	calls := daemon.snapshot()
	if len(calls) != 7 || calls[2].Action != "schematic.components.list" {
		t.Fatalf("calls=%+v, want 2 moves + failed post-check + 2 rollback calls + readback + save", calls)
	}
	assertAutolayoutPatch(t, calls[3], "id-U2", 30, 40)
	assertAutolayoutPatch(t, calls[4], "id-U1", 10, 20)
	if calls[5].Action != "schematic.components.list" || calls[6].Action != "schematic.save" {
		t.Fatalf("rollback was not read back and saved: %+v", calls)
	}
}

func TestApplyAutolayoutVerifiesTargetsAndSaves(t *testing.T) {
	cfg, daemon, cleanup := newAutolayoutTestDaemon(t, func(_ int, call autolayoutTestCall) string {
		if call.Action == "schematic.components.list" {
			return autolayoutScene("", 500, 400, 0)
		}
		if call.Action == "schematic.save" {
			return autolayoutOK("", `{"saved":true}`)
		}
		return autolayoutOK("", `{}`)
	})
	defer cleanup()

	rep := alReport{
		OK: true,
		Placements: []alPlacement{{
			Designator: "U1", PrimitiveID: "id-U1",
			X: 500, Y: 400, OriginalX: 100, OriginalY: 100, HasOriginal: true,
		}},
		baselineParts: []layoutComp{{
			ID: "id-U1", Designator: "U1", ComponentType: "part",
			X: 100, Y: 100, AnchorAvailable: true,
			BBox:           bb(90, 90, 110, 110),
			Pins:           []layoutPin{{Number: "1", X: 90, Y: 100}},
			PinsAvailable:  true,
			PinsProofKnown: true,
		}},
		rules: defaultAutolayoutRules(),
	}
	applyAutolayout(cfg, "", &rep, io.Discard)
	if !rep.OK {
		t.Fatalf("clean readback unexpectedly failed: errors=%v note=%q", rep.Errors, rep.Note)
	}
	if !strings.Contains(rep.Note, "geometry verified and schematic saved") {
		t.Fatalf("successful apply not verified/saved: %q", rep.Note)
	}
	calls := daemon.snapshot()
	if len(calls) != 3 || calls[0].Action != "schematic.component.modify" ||
		calls[1].Action != "schematic.components.list" || calls[2].Action != "schematic.save" {
		t.Fatalf("unexpected successful apply sequence: %+v", calls)
	}
}

func TestApplyAutolayoutEmptyPostSnapshotCannotPass(t *testing.T) {
	listCall := 0
	cfg, _, cleanup := newAutolayoutTestDaemon(t, func(_ int, call autolayoutTestCall) string {
		if call.Action == "schematic.components.list" {
			listCall++
			if listCall == 1 {
				return autolayoutOK("", `{"components":[],"count":0,"connectivitySummary":{"scope":"activePage","wires":0,"buses":0,"netflags":0,"netports":0,"netlabels":0,"shortSymbols":0}}`)
			}
			return autolayoutOK("", `{"components":[
				{"componentType":"part","designator":"U1","primitiveId":"id-U1","x":100,"y":100,"rotation":0}
			],"count":1}`)
		}
		if call.Action == "schematic.save" {
			return autolayoutOK("", `{"saved":true}`)
		}
		return autolayoutOK("", `{}`)
	})
	defer cleanup()

	rep := alReport{OK: true, Placements: []alPlacement{{
		Designator: "U1", PrimitiveID: "id-U1",
		X: 500, Y: 400, OriginalX: 100, OriginalY: 100, HasOriginal: true,
	}}}
	applyAutolayout(cfg, "", &rep, io.Discard)
	if rep.OK {
		t.Fatal("empty post-apply snapshot must fail and roll back")
	}
	if !strings.Contains(strings.Join(rep.Errors, " "), "contains no movable parts") {
		t.Fatalf("missing empty-snapshot diagnostic: %v", rep.Errors)
	}
	if !strings.Contains(rep.Note, "rollback confirmed 1/1") {
		t.Fatalf("rollback was not verified: %q", rep.Note)
	}
}

func TestValidateAppliedAutolayoutRejectsMissingUntouchedPart(t *testing.T) {
	baseline := []layoutComp{
		{ID: "id-U1", Designator: "U1", ComponentType: "part", X: 100, Y: 100, AnchorAvailable: true, BBox: bb(90, 90, 110, 110), PinsAvailable: true, PinsProofKnown: true},
		{ID: "id-J1", Designator: "J1", ComponentType: "part", X: 200, Y: 200, AnchorAvailable: true, BBox: bb(190, 190, 210, 210), PinsAvailable: true, PinsProofKnown: true},
	}
	post := []layoutComp{
		{ID: "id-U1", Designator: "U1", ComponentType: "part", X: 500, Y: 400, AnchorAvailable: true, BBox: bb(490, 390, 510, 410), PinsAvailable: true, PinsProofKnown: true},
	}
	sheet := &layoutBBox{MinX: 0, MinY: 0, MaxX: 1000, MaxY: 800}
	issues := validateAppliedAutolayout(post, baseline, sheet, []alPlacement{{
		Designator: "U1", PrimitiveID: "id-U1", X: 500, Y: 400,
	}}, autolayoutRules{})
	if !strings.Contains(strings.Join(issues, " "), "id-J1") {
		t.Fatalf("missing untouched baseline part was not detected: %v", issues)
	}
}

func TestApplyAutolayoutSavedFalseTriggersVerifiedRollback(t *testing.T) {
	listCall, saveCall := 0, 0
	cfg, _, cleanup := newAutolayoutTestDaemon(t, func(_ int, call autolayoutTestCall) string {
		switch call.Action {
		case "schematic.components.list":
			listCall++
			if listCall == 1 {
				return autolayoutScene("", 500, 400, 0)
			}
			return autolayoutOK("", `{"components":[
				{"componentType":"part","designator":"U1","primitiveId":"id-U1","x":100,"y":100,"rotation":0}
			],"count":1}`)
		case "schematic.save":
			saveCall++
			if saveCall == 1 {
				return autolayoutOK("", `{"saved":false}`)
			}
			return autolayoutOK("", `{"saved":true}`)
		default:
			return autolayoutOK("", `{}`)
		}
	})
	defer cleanup()

	rep := alReport{
		OK: true,
		Placements: []alPlacement{{
			Designator: "U1", PrimitiveID: "id-U1",
			X: 500, Y: 400, OriginalX: 100, OriginalY: 100, HasOriginal: true,
		}},
		baselineParts: []layoutComp{{
			ID: "id-U1", Designator: "U1", ComponentType: "part",
			X: 100, Y: 100, AnchorAvailable: true, BBox: bb(90, 90, 110, 110),
			Pins: []layoutPin{{Number: "1", X: 90, Y: 100}}, PinsAvailable: true, PinsProofKnown: true,
		}},
		rules: defaultAutolayoutRules(),
	}
	applyAutolayout(cfg, "", &rep, io.Discard)
	if rep.OK || !strings.Contains(strings.Join(rep.Errors, " "), "saved=false") {
		t.Fatalf("saved:false did not fail apply: ok=%v errors=%v", rep.OK, rep.Errors)
	}
	if !strings.Contains(rep.Note, "rollback confirmed 1/1") {
		t.Fatalf("save failure did not produce a verified rollback: %q", rep.Note)
	}
}

func assertAutolayoutPatch(t *testing.T, call autolayoutTestCall, primitiveID string, x, y float64) {
	t.Helper()
	if call.Action != "schematic.component.modify" || call.Payload["primitiveId"] != primitiveID {
		t.Fatalf("call=%+v, want modify %s", call, primitiveID)
	}
	patch, ok := call.Payload["patch"].(map[string]any)
	if !ok || asFloat(patch["x"]) != x || asFloat(patch["y"]) != y {
		t.Fatalf("patch=%v, want x=%v y=%v", call.Payload["patch"], x, y)
	}
}
