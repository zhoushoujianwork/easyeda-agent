package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/internal/blocks"
)

type blockApplyTestCall struct {
	Action  string
	Payload map[string]any
}

type blockApplyTestDaemon struct {
	mu    sync.Mutex
	calls []blockApplyTestCall
}

func (d *blockApplyTestDaemon) snapshot() []blockApplyTestCall {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]blockApplyTestCall(nil), d.calls...)
}

func newBlockApplyTestDaemon(t *testing.T, responder func(blockApplyTestCall) string) (*appConfig, *blockApplyTestDaemon, func()) {
	t.Helper()
	state := &blockApplyTestDaemon{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_, _ = w.Write([]byte(`{"service":"easyeda-agent","windows":[{"windowId":"w1"}]}`))
		case "/action":
			var body struct {
				Action  string         `json:"action"`
				Payload map[string]any `json:"payload"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode action request: %v", err)
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			call := blockApplyTestCall{Action: body.Action, Payload: body.Payload}
			state.mu.Lock()
			state.calls = append(state.calls, call)
			state.mu.Unlock()
			resp := responder(call)
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

func blockApplyPartsFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "standard-parts.json")
	raw := []byte(`{
		"libraryUuid": "lib",
		"parts": {
			"led.yellow_0805": {"deviceUuid": "dev-led", "lcsc": "C1"},
			"res.1k_0402": {"deviceUuid": "dev-res", "lcsc": "C2"}
		}
	}`)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write parts fixture: %v", err)
	}
	return path
}

const blockApplyOverlapGeometry = `{"ok":true,"result":{"components":[
	{"componentType":"part","designator":"LED1","primitiveId":"pid-led",
	 "bbox":{"minX":390,"minY":290,"maxX":410,"maxY":310},
	 "pinsAvailable":true,"netlistAvailable":true,"pins":[{"pinNumber":"1","x":400,"y":300}]},
	{"componentType":"part","designator":"R1","primitiveId":"pid-r",
	 "bbox":{"minX":400,"minY":295,"maxX":420,"maxY":315},
	 "pinsAvailable":true,"netlistAvailable":true,"pins":[{"pinNumber":"1","x":415,"y":305}]}
]}}`

const blockApplyCleanGeometry = `{"ok":true,"result":{"components":[
	{"componentType":"part","designator":"LED1","primitiveId":"pid-led",
	 "bbox":{"minX":390,"minY":290,"maxX":400,"maxY":300},
	 "pinsAvailable":true,"netlistAvailable":true,"pins":[{"pinNumber":"1","x":390,"y":295}]},
	{"componentType":"part","designator":"R1","primitiveId":"pid-r",
	 "bbox":{"minX":410,"minY":290,"maxX":420,"maxY":300},
	 "pinsAvailable":true,"netlistAvailable":true,"pins":[{"pinNumber":"1","x":420,"y":295}]}
]}}`

const blockApplyPinCoincidenceGeometry = `{"ok":true,"result":{"components":[
	{"componentType":"part","designator":"LED1","primitiveId":"pid-led",
	 "bbox":{"minX":390,"minY":290,"maxX":405,"maxY":310},
	 "pinsAvailable":true,"netlistAvailable":true,"pins":[{"pinNumber":"1","x":405,"y":300}]},
	{"componentType":"part","designator":"R1","primitiveId":"pid-r",
	 "bbox":{"minX":405,"minY":290,"maxX":420,"maxY":310},
	 "pinsAvailable":true,"netlistAvailable":true,"pins":[{"pinNumber":"2","x":405,"y":300}]}
]}}`

func blockApplyPlaceResponse(call int, includeID bool) string {
	id, designator := "pid-led", "LED1"
	if call == 2 {
		id, designator = "pid-r", "R1"
	}
	if !includeID {
		return fmt.Sprintf(`{"ok":true,"result":{"component":{"designator":%q}}}`, designator)
	}
	return fmt.Sprintf(`{"ok":true,"result":{"primitiveId":%q,"component":{"primitiveId":%q,"designator":%q}}}`,
		id, id, designator)
}

func assertBlockApplyStoppedBeforeWiring(t *testing.T, calls []blockApplyTestCall) {
	t.Helper()
	for _, call := range calls {
		switch call.Action {
		case "schematic.components.list", "schematic.component.place", "schematic.component.delete":
			// Placement and its compensating cleanup are the only allowed writes.
		case "library.device.get":
			// Pre-placement device resolution sweep: a read, and it is issued before
			// the first place, so it can never be a write that outlived the gate.
		case "document.current":
			// Read-only page pin issued by the group-registry leg of the delete
			// cascade (缺陷 2): a verified rollback strips the deleted designators
			// from the persistent-group table, which needs the active page uuid.
		default:
			t.Fatalf("action %q ran after the layout gate; calls=%+v", call.Action, calls)
		}
	}
}

func assertBlockRollbackReadbackIsDocumentWide(t *testing.T, calls []blockApplyTestCall) {
	t.Helper()
	for i := len(calls) - 1; i >= 0; i-- {
		if calls[i].Action != "schematic.components.list" {
			continue
		}
		if calls[i].Payload["allPages"] != true || calls[i].Payload["tagPages"] != true {
			t.Fatalf("rollback read-back payload=%v, want allPages+tagPages", calls[i].Payload)
		}
		return
	}
	t.Fatal("no rollback components.list read-back found")
}

func TestRunBlockApplyOverlapStopsBeforeWiringAndRollsBack(t *testing.T) {
	listCalls, placeCalls := 0, 0
	cfg, daemon, cleanup := newBlockApplyTestDaemon(t, func(call blockApplyTestCall) string {
		switch call.Action {
		case "schematic.components.list":
			listCalls++
			switch listCalls {
			case 1, 2:
				return `{"ok":true,"result":{"components":[]}}`
			case 3:
				return blockApplyOverlapGeometry
			default:
				return `{"ok":true,"result":{"components":[]}}`
			}
		case "schematic.component.place":
			placeCalls++
			return blockApplyPlaceResponse(placeCalls, true)
		case "schematic.component.delete":
			return `{"ok":true,"result":{"deleted":true,"removed":2}}`
		case "document.current":
			// Group-registry cascade page pin; empty result → cascade fail-softs.
			return `{"ok":true,"result":{}}`
		case "library.device.get":
			// The pre-placement resolution sweep; every fixture device resolves.
			return `{"ok":true,"result":{"device":{"uuid":"dev"}}}`
		default:
			t.Errorf("unexpected action %q", call.Action)
			return `{"ok":true,"result":{}}`
		}
	})
	defer cleanup()

	var stdout, stderr bytes.Buffer
	err := runBlockApply(cfg, "w1", "led_indicator_gpio", bapInput{},
		blockApplyPartsFixture(t), false, true, 0, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "layout verification found 1 overlap") {
		t.Fatalf("err=%v, want hard overlap failure", err)
	}
	if strings.Contains(stderr.String(), "layout ✓") {
		t.Fatalf("false-green layout output:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "rollback ✓") {
		t.Fatalf("verified rollback not reported:\n%s", stderr.String())
	}

	var manifest bapManifest
	if err := json.Unmarshal(stdout.Bytes(), &manifest); err != nil {
		t.Fatalf("decode failure manifest: %v\n%s", err, stdout.String())
	}
	if manifest.OK != "failed-rolled-back" || manifest.PartialState {
		t.Fatalf("manifest status=%q partial=%v, want failed-rolled-back/non-partial", manifest.OK, manifest.PartialState)
	}
	if manifest.Rollback == nil || !manifest.Rollback.Complete || !manifest.Rollback.Verified {
		t.Fatalf("rollback manifest=%+v, want verified complete", manifest.Rollback)
	}
	if len(manifest.Placed) != 2 || manifest.Placed[0].PrimitiveID == "" || manifest.Placed[1].PrimitiveID == "" {
		t.Fatalf("placed primitive IDs not retained: %+v", manifest.Placed)
	}
	calls := daemon.snapshot()
	assertBlockApplyStoppedBeforeWiring(t, calls)
	assertBlockRollbackReadbackIsDocumentWide(t, calls)
}

func TestRunBlockApplyReadOrParseFailureStopsBeforeWiring(t *testing.T) {
	for _, tc := range []struct {
		name       string
		verifyBody string
		want       string
	}{
		{
			name:       "read failure",
			verifyBody: `{"ok":false,"error":{"message":"injected geometry read failure"}}`,
			want:       "read components with real bbox/pin geometry",
		},
		{
			name:       "parse failure",
			verifyBody: `{"ok":true,"result":{}}`,
			want:       "missing components array",
		},
		{
			name:       "incomplete geometry",
			verifyBody: `{"ok":true,"result":{"components":[{"componentType":"part","designator":"LED1","primitiveId":"pid-led","pins":[]}]}}`,
			want:       "has no bbox",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listCalls, placeCalls := 0, 0
			cfg, daemon, cleanup := newBlockApplyTestDaemon(t, func(call blockApplyTestCall) string {
				switch call.Action {
				case "schematic.components.list":
					listCalls++
					if listCalls <= 2 {
						return `{"ok":true,"result":{"components":[]}}`
					}
					if listCalls == 3 {
						return tc.verifyBody
					}
					return `{"ok":true,"result":{"components":[]}}`
				case "schematic.component.place":
					placeCalls++
					return blockApplyPlaceResponse(placeCalls, true)
				case "schematic.component.delete":
					return `{"ok":true,"result":{"deleted":true}}`
				case "document.current":
					// Group-registry cascade page pin; empty result → cascade fail-softs.
					return `{"ok":true,"result":{}}`
				case "library.device.get":
					// The pre-placement resolution sweep; every fixture device resolves.
					return `{"ok":true,"result":{"device":{"uuid":"dev"}}}`
				default:
					t.Errorf("unexpected action %q", call.Action)
					return `{"ok":true,"result":{}}`
				}
			})
			defer cleanup()

			var stdout, stderr bytes.Buffer
			err := runBlockApply(cfg, "w1", "led_indicator_gpio", bapInput{},
				blockApplyPartsFixture(t), false, true, 0, &stdout, &stderr)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v, want %q", err, tc.want)
			}
			if strings.Contains(stderr.String(), "layout ✓") {
				t.Fatalf("false-green layout output:\n%s", stderr.String())
			}
			var manifest bapManifest
			if err := json.Unmarshal(stdout.Bytes(), &manifest); err != nil {
				t.Fatalf("decode failure manifest: %v\n%s", err, stdout.String())
			}
			if manifest.OK != "failed-rolled-back" || manifest.Rollback == nil || !manifest.Rollback.Complete {
				t.Fatalf("manifest=%+v, want verified rollback failure", manifest)
			}
			assertBlockApplyStoppedBeforeWiring(t, daemon.snapshot())
		})
	}
}

func TestRunBlockApplyPinCoincidenceStopsBeforeWiring(t *testing.T) {
	listCalls, placeCalls := 0, 0
	cfg, daemon, cleanup := newBlockApplyTestDaemon(t, func(call blockApplyTestCall) string {
		switch call.Action {
		case "schematic.components.list":
			listCalls++
			if listCalls <= 2 {
				return `{"ok":true,"result":{"components":[]}}`
			}
			if listCalls == 3 {
				return blockApplyPinCoincidenceGeometry
			}
			return `{"ok":true,"result":{"components":[]}}`
		case "schematic.component.place":
			placeCalls++
			return blockApplyPlaceResponse(placeCalls, true)
		case "schematic.component.delete":
			return `{"ok":true,"result":{"deleted":true}}`
		case "document.current":
			// Group-registry cascade page pin; empty result → cascade fail-softs.
			return `{"ok":true,"result":{}}`
		case "library.device.get":
			// The pre-placement resolution sweep; every fixture device resolves.
			return `{"ok":true,"result":{"device":{"uuid":"dev"}}}`
		default:
			t.Errorf("unexpected action %q", call.Action)
			return `{"ok":true,"result":{}}`
		}
	})
	defer cleanup()

	var stdout, stderr bytes.Buffer
	err := runBlockApply(cfg, "w1", "led_indicator_gpio", bapInput{},
		blockApplyPartsFixture(t), false, true, 0, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "0 overlap(s) and 1 pin coincidence") {
		t.Fatalf("err=%v, want hard pin-coincidence failure", err)
	}
	if !strings.Contains(stderr.String(), "PIN COINCIDENCE") || strings.Contains(stderr.String(), "layout ✓") {
		t.Fatalf("pin/fake-green output wrong:\n%s", stderr.String())
	}
	var manifest bapManifest
	if err := json.Unmarshal(stdout.Bytes(), &manifest); err != nil {
		t.Fatalf("decode failure manifest: %v\n%s", err, stdout.String())
	}
	if manifest.OK != "failed-rolled-back" || len(manifest.LayoutOverlaps) != 1 ||
		manifest.LayoutOverlaps[0].Type != "pin-coincidence" {
		t.Fatalf("manifest=%+v, want rolled-back pin-coincidence", manifest)
	}
	assertBlockApplyStoppedBeforeWiring(t, daemon.snapshot())
}

func TestRunBlockApplyRollbackSurvivorReportsPartialState(t *testing.T) {
	listCalls, placeCalls := 0, 0
	cfg, daemon, cleanup := newBlockApplyTestDaemon(t, func(call blockApplyTestCall) string {
		switch call.Action {
		case "schematic.components.list":
			listCalls++
			if listCalls <= 2 {
				return `{"ok":true,"result":{"components":[]}}`
			}
			// The fourth list is rollback verification; deliberately keep both
			// IDs alive to model a connector/platform delete no-op.
			return blockApplyOverlapGeometry
		case "schematic.component.place":
			placeCalls++
			return blockApplyPlaceResponse(placeCalls, true)
		case "schematic.component.delete":
			return `{"ok":true,"result":{"deleted":false,"survived":["pid-led","pid-r"]}}`
		case "library.device.get":
			// The pre-placement resolution sweep; every fixture device resolves.
			return `{"ok":true,"result":{"device":{"uuid":"dev"}}}`
		default:
			t.Errorf("unexpected action %q", call.Action)
			return `{"ok":true,"result":{}}`
		}
	})
	defer cleanup()

	var stdout, stderr bytes.Buffer
	err := runBlockApply(cfg, "w1", "led_indicator_gpio", bapInput{},
		blockApplyPartsFixture(t), false, true, 0, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "PARTIAL STATE") {
		t.Fatalf("err=%v, want explicit partial state", err)
	}
	if !strings.Contains(stderr.String(), "PARTIAL STATE") || strings.Contains(stderr.String(), "layout ✓") {
		t.Fatalf("partial/fake-green output wrong:\n%s", stderr.String())
	}
	var manifest bapManifest
	if err := json.Unmarshal(stdout.Bytes(), &manifest); err != nil {
		t.Fatalf("decode failure manifest: %v\n%s", err, stdout.String())
	}
	if manifest.OK != "failed-partial" || !manifest.PartialState || manifest.Rollback == nil || manifest.Rollback.Complete {
		t.Fatalf("manifest=%+v, want explicit failed-partial", manifest)
	}
	if len(manifest.Rollback.SurvivedPrimitiveIDs) != 2 {
		t.Fatalf("survivors=%v, want both placed IDs", manifest.Rollback.SurvivedPrimitiveIDs)
	}
	assertBlockApplyStoppedBeforeWiring(t, daemon.snapshot())
}

func TestRunBlockApplyMissingPlacedIDDoesNotGuessRollbackTarget(t *testing.T) {
	listCalls := 0
	cfg, daemon, cleanup := newBlockApplyTestDaemon(t, func(call blockApplyTestCall) string {
		switch call.Action {
		case "schematic.components.list":
			listCalls++
			if listCalls <= 2 {
				return `{"ok":true,"result":{"components":[]}}`
			}
			return `{"ok":true,"result":{"components":[{"primitiveId":"unknown-new-id","designator":"LED1"}]}}`
		case "schematic.component.place":
			return blockApplyPlaceResponse(1, false)
		case "library.device.get":
			// The pre-placement resolution sweep; every fixture device resolves.
			return `{"ok":true,"result":{"device":{"uuid":"dev"}}}`
		default:
			t.Errorf("unexpected action %q", call.Action)
			return `{"ok":true,"result":{}}`
		}
	})
	defer cleanup()

	var stdout, stderr bytes.Buffer
	err := runBlockApply(cfg, "w1", "led_indicator_gpio", bapInput{},
		blockApplyPartsFixture(t), false, true, 0, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "PARTIAL STATE") {
		t.Fatalf("err=%v, want explicit partial state", err)
	}
	calls := daemon.snapshot()
	for _, call := range calls {
		if call.Action == "schematic.component.delete" {
			t.Fatalf("rollback guessed a delete target without a returned primitiveId: %+v", calls)
		}
	}
	var manifest bapManifest
	if err := json.Unmarshal(stdout.Bytes(), &manifest); err != nil {
		t.Fatalf("decode failure manifest: %v\n%s", err, stdout.String())
	}
	if !manifest.PartialState || manifest.Rollback == nil ||
		len(manifest.Rollback.MissingPrimitiveIDs) != 1 ||
		manifest.Rollback.MissingPrimitiveIDs[0] != "LED1" {
		t.Fatalf("manifest=%+v, want LED1 listed as untracked partial state", manifest)
	}
	assertBlockApplyStoppedBeforeWiring(t, calls)
}

func TestVerifyBlockLayoutAcceptsCompleteCleanGeometry(t *testing.T) {
	cfg, _, cleanup := newBlockApplyTestDaemon(t, func(call blockApplyTestCall) string {
		if call.Action != "schematic.components.list" {
			t.Errorf("unexpected action %q", call.Action)
		}
		return blockApplyCleanGeometry
	})
	defer cleanup()

	findings, _, err := verifyBlockLayout(cfg, "w1", []bapPlacement{
		{Designator: "LED1", PrimitiveID: "pid-led"},
		{Designator: "R1", PrimitiveID: "pid-r"},
	})
	if err != nil || len(findings) != 0 {
		t.Fatalf("findings=%+v err=%v, want proven clean", findings, err)
	}
}

// ─── place 超时收编(假失败定律在 place 上的缺口)───────────────────────────
//
// 真机形态(工程 ceshi,block.esp32s3_wroom1_module):place 报
// "connector did not respond",**器件其实已经建好了**,只是回执丢在半路。Go 侧
// 拿不到 primitiveId,回滚无从下手,那个件就留在页上成了谁也删不掉的残件,并
// 随每次重试再生一个(一轮 U2/U2/U3 三件)。
//
// 下面两块 fixture 的区别只有一处,却正好是判据的分水岭:
//   - blockApplyBoard.loseResponseOn —— 件**照建**,回执报失败(假失败)
//   - blockApplyBoard.dropOn         —— 件**没建**,回执报失败(真失败,负对照)

// blockApplyBoard 是一块会记事的假画布:place 落件、delete 删件、components.list
// 回读当前活体。它让「回执」与「画布」可以分家 —— 而那正是本缺陷的全部。
type blockApplyBoard struct {
	mu             sync.Mutex
	comps          map[string]map[string]any
	order          []string
	placeCalls     int
	loseResponseOn int // 第 N 次 place:件照建,回执报失败
	dropOn         int // 第 N 次 place:什么都不建,回执报失败
	deleted        []string
}

func newBlockApplyBoard() *blockApplyBoard {
	return &blockApplyBoard{comps: map[string]map[string]any{}}
}

func (b *blockApplyBoard) seed(id, designator string, x, y float64) {
	b.comps[id] = map[string]any{
		"primitiveId": id, "componentType": "part", "designator": designator, "x": x, "y": y,
	}
	b.order = append(b.order, id)
}

func (b *blockApplyBoard) listJSON() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	items := make([]any, 0, len(b.order))
	for _, id := range b.order {
		if c, ok := b.comps[id]; ok {
			items = append(items, c)
		}
	}
	raw, _ := json.Marshal(map[string]any{"ok": true, "result": map[string]any{"components": items}})
	return string(raw)
}

func (b *blockApplyBoard) placeJSON(payload map[string]any) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.placeCalls++
	n := b.placeCalls
	if n == b.dropOn {
		return `{"ok":false,"error":{"message":"schematic.component.place failed: connector did not respond"}}`
	}
	id := fmt.Sprintf("pid-%d", n)
	designator, _ := payload["designator"].(string)
	x, _ := payload["x"].(float64)
	y, _ := payload["y"].(float64)
	b.seed(id, designator, x, y)
	if n == b.loseResponseOn {
		// 件已在画布上,回执丢了 —— 真机上最常见的一种失败。
		return `{"ok":false,"error":{"message":"schematic.component.place failed: connector did not respond"}}`
	}
	raw, _ := json.Marshal(map[string]any{"ok": true, "result": map[string]any{
		"primitiveId": id,
		"component":   map[string]any{"primitiveId": id, "designator": designator},
	}})
	return string(raw)
}

func (b *blockApplyBoard) deleteJSON(payload map[string]any) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	ids, _ := payload["primitiveIds"].([]any)
	removed := 0
	for _, item := range ids {
		id, _ := item.(string)
		if _, ok := b.comps[id]; ok {
			delete(b.comps, id)
			removed++
		}
		b.deleted = append(b.deleted, id)
	}
	raw, _ := json.Marshal(map[string]any{"ok": true, "result": map[string]any{
		"deleted": true, "removed": removed,
	}})
	return string(raw)
}

func (b *blockApplyBoard) alive(id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.comps[id]
	return ok
}

func (b *blockApplyBoard) deletedIDs() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.deleted...)
}

func blockApplyBoardDaemon(t *testing.T, board *blockApplyBoard) (*appConfig, *blockApplyTestDaemon, func()) {
	t.Helper()
	return newBlockApplyTestDaemon(t, func(call blockApplyTestCall) string {
		switch call.Action {
		case "schematic.components.list":
			return board.listJSON()
		case "schematic.component.place":
			return board.placeJSON(call.Payload)
		case "schematic.component.delete":
			return board.deleteJSON(call.Payload)
		case "document.current":
			return `{"ok":true,"result":{}}`
		case "library.device.get":
			// The pre-placement resolution sweep; every fixture device resolves.
			return `{"ok":true,"result":{"device":{"uuid":"dev"}}}`
		default:
			t.Errorf("unexpected action %q", call.Action)
			return `{"ok":true,"result":{}}`
		}
	})
}

// ledPlannedPlacements 离线算出同一次 apply 的落点,好让负对照能把「页面上原有的
// 同型器件」精确地摆在请求坐标上 —— 收编唯一的防误删门就是在那里被考验的。
func ledPlannedPlacements(t *testing.T, in bapInput) []bapPlacement {
	t.Helper()
	b, ok, err := blocks.Get("led_indicator_gpio")
	if err != nil || !ok {
		t.Fatalf("load led_indicator_gpio: ok=%v err=%v", ok, err)
	}
	in.Block = b
	if in.Topology, err = blockTopology(b); err != nil {
		t.Fatal(err)
	}
	if in.Layout, err = b.SchematicLayout(); err != nil {
		t.Fatal(err)
	}
	in.Devices = fixtureDevices()
	plan, err := planBlockApply(in)
	if err != nil {
		t.Fatal(err)
	}
	return plan.Placements
}

func TestRunBlockApplyAdoptsTheComponentAPlaceTimeoutLeftBehind(t *testing.T) {
	board := newBlockApplyBoard()
	board.loseResponseOn = 2 // 第二件:落地了,回执丢了
	cfg, daemon, cleanup := blockApplyBoardDaemon(t, board)
	defer cleanup()

	var stdout, stderr bytes.Buffer
	err := runBlockApply(cfg, "w1", "led_indicator_gpio", bapInput{},
		blockApplyPartsFixture(t), false, true, 0, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "connector did not respond") {
		t.Fatalf("err=%v, want the place failure surfaced", err)
	}
	if !strings.Contains(stderr.String(), "adopt ✓") {
		t.Fatalf("timed-out place was not adopted:\n%s", stderr.String())
	}

	var manifest bapManifest
	if err := json.Unmarshal(stdout.Bytes(), &manifest); err != nil {
		t.Fatalf("decode failure manifest: %v\n%s", err, stdout.String())
	}
	if manifest.Rollback == nil {
		t.Fatalf("no rollback report: %s", stdout.String())
	}
	// ① 收编:回执丢了的那个件被点名了 —— 不再是「no reliable primitiveId」。
	if !reflect.DeepEqual(manifest.Rollback.AdoptedPrimitiveIDs, []string{"pid-2"}) {
		t.Fatalf("adopted=%v, want the orphaned pid-2 identified", manifest.Rollback.AdoptedPrimitiveIDs)
	}
	if len(manifest.Rollback.MissingPrimitiveIDs) != 0 {
		t.Fatalf("missing=%v, want nothing left unnamed once adoption worked",
			manifest.Rollback.MissingPrimitiveIDs)
	}
	// ② 它照常被删干净了,页面回到原状 —— 残件不再留在页上。
	if board.alive("pid-2") || board.alive("pid-1") {
		t.Fatalf("residue survived the rollback: deleted=%v", board.deletedIDs())
	}
	if manifest.OK != "failed-rolled-back" || manifest.PartialState {
		t.Fatalf("manifest status=%q partial=%v, want a proven rollback", manifest.OK, manifest.PartialState)
	}
	if strings.Contains(stderr.String(), "PARTIAL STATE") {
		t.Fatalf("adoption still reported PARTIAL STATE:\n%s", stderr.String())
	}
	assertBlockApplyStoppedBeforeWiring(t, daemon.snapshot())
}

// 负对照:place **真的**失败(件没建)。既不许收编出一个不存在的 id,也不许碰
// 页面上原有的、恰好摆在请求坐标上的同型器件。
func TestRunBlockApplyPlaceThatNeverLandedAdoptsNothing(t *testing.T) {
	in := bapInput{OriginX: 400, OriginY: 300, AtExplicit: true}
	existing := map[string]bool{"R9": true, "R8": true}
	planIn := in
	planIn.Existing = existing
	placements := ledPlannedPlacements(t, planIn)

	board := newBlockApplyBoard()
	board.dropOn = 2 // 第二件:什么都没建
	// 页面上原有的同型器件,精确摆在每一个请求坐标上 —— 收编必须一个都不碰。
	twins := []string{"twin-a", "twin-b"}
	for i, p := range placements {
		if i >= len(twins) {
			break
		}
		board.seed(twins[i], []string{"R9", "R8"}[i], p.X, p.Y)
	}
	cfg, daemon, cleanup := blockApplyBoardDaemon(t, board)
	defer cleanup()

	var stdout, stderr bytes.Buffer
	err := runBlockApply(cfg, "w1", "led_indicator_gpio", in,
		blockApplyPartsFixture(t), false, true, 0, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "connector did not respond") {
		t.Fatalf("err=%v, want the place failure surfaced", err)
	}
	if !strings.Contains(stderr.String(), "确实没有落地") {
		t.Fatalf("a place that never landed must be reported as such:\n%s", stderr.String())
	}

	var manifest bapManifest
	if err := json.Unmarshal(stdout.Bytes(), &manifest); err != nil {
		t.Fatalf("decode failure manifest: %v\n%s", err, stdout.String())
	}
	if manifest.Rollback == nil || len(manifest.Rollback.AdoptedPrimitiveIDs) != 0 {
		t.Fatalf("adopted=%+v, want no invented id", manifest.Rollback)
	}
	for _, twin := range twins {
		if !board.alive(twin) {
			t.Fatalf("a pre-existing part at the requested spot was deleted: deleted=%v", board.deletedIDs())
		}
	}
	for _, id := range board.deletedIDs() {
		if id != "pid-1" {
			t.Fatalf("rollback touched something it did not create: %v", board.deletedIDs())
		}
	}
	// 证实没落地 ⇒ 没有残件 ⇒ 清理是完整的,不该再吓唬成 PARTIAL STATE。
	if manifest.OK != "failed-rolled-back" || manifest.PartialState {
		t.Fatalf("manifest status=%q partial=%v, want a clean rollback", manifest.OK, manifest.PartialState)
	}
	assertBlockApplyStoppedBeforeWiring(t, daemon.snapshot())
}

// 没有落地前快照 = 分不清「新出现」和「本来就在」。此时必须整个关掉收编,
// 宁可报 PARTIAL STATE,也不许按坐标猜(那等于允许误删)。
func TestRunBlockApplyWithoutPreplaceSnapshotRefusesToGuess(t *testing.T) {
	listCalls, placeCalls := 0, 0
	cfg, _, cleanup := newBlockApplyTestDaemon(t, func(call blockApplyTestCall) string {
		switch call.Action {
		case "schematic.components.list":
			listCalls++
			if listCalls == 2 {
				// 快照那一读失败 → preplaceIDs = nil。
				return `{"ok":false,"error":{"message":"injected snapshot read failure"}}`
			}
			return `{"ok":true,"result":{"components":[{"primitiveId":"ghost","componentType":"part","designator":"R1","x":0,"y":0}]}}`
		case "schematic.component.place":
			placeCalls++
			return `{"ok":false,"error":{"message":"connector did not respond"}}`
		case "document.current":
			return `{"ok":true,"result":{}}`
		case "library.device.get":
			// The pre-placement resolution sweep; every fixture device resolves.
			return `{"ok":true,"result":{"device":{"uuid":"dev"}}}`
		default:
			t.Errorf("unexpected action %q", call.Action)
			return `{"ok":true,"result":{}}`
		}
	})
	defer cleanup()

	var stdout, stderr bytes.Buffer
	err := runBlockApply(cfg, "w1", "led_indicator_gpio", bapInput{},
		blockApplyPartsFixture(t), false, true, 0, &stdout, &stderr)
	if err == nil {
		t.Fatal("want the place failure surfaced")
	}
	if !strings.Contains(stderr.String(), "adopt ✗") ||
		!strings.Contains(stderr.String(), "没有落地前的器件快照") {
		t.Fatalf("missing snapshot must disable adoption out loud:\n%s", stderr.String())
	}
	var manifest bapManifest
	if err := json.Unmarshal(stdout.Bytes(), &manifest); err != nil {
		t.Fatalf("decode failure manifest: %v\n%s", err, stdout.String())
	}
	if manifest.Rollback == nil || len(manifest.Rollback.AdoptedPrimitiveIDs) != 0 {
		t.Fatalf("adopted=%+v, want no guess without a snapshot", manifest.Rollback)
	}
	if !manifest.PartialState || len(manifest.Rollback.MissingPrimitiveIDs) != 1 {
		t.Fatalf("manifest=%+v, want an honest PARTIAL STATE", manifest.Rollback)
	}
}
