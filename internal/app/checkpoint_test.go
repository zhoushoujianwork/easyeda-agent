package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Other tests in this package drive postAction against fake daemons; automatic
// capture would add requests they do not expect and write into the real home.
func init() { os.Setenv(envCheckpoint, "off") }

func checkpointPage(client string, parts ...string) string {
	lines := []string{`{"type":"DOCHEAD"}||{"docType":"SCH_PAGE","client":"` + client + `"}|`, `{"type":"CANVAS","ticket":1,"id":"CANVAS"}||{"originX":0}|`}
	ticket := 2
	for _, id := range parts {
		lines = append(lines,
			fmt.Sprintf(`{"type":"COMPONENT","ticket":%d,"id":"%s"}||{"x":10,"y":20,"attrs":{"isSwitchClose":false}}|`, ticket, id),
			fmt.Sprintf(`{"type":"ATTR","ticket":%d,"id":"a-%s"}||{"value":"%s","key":"Designator","parentId":"%s"}|`, ticket+1, id, strings.ToUpper(id), id),
			fmt.Sprintf(`{"type":"ATTR","ticket":%d,"id":"d-%s"}||{"value":"dev-%s","key":"Device","parentId":"%s"}|`, ticket+2, id, id, id))
		ticket += 3
	}
	return strings.Join(lines, "\n")
}

func TestCheckpointStoreRebuildsEveryStateFromFullAndDeltas(t *testing.T) {
	store, err := openCheckpointStore(t.TempDir(), "p", "d")
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{"r1", "r2", "r3", "r4", "r5", "r6", "r7", "r8", "r9", "c1", "c2", "c3"}
	states := []string{
		checkpointPage("aaaa", ids...),
		checkpointPage("bbbb", ids...),                           // only the volatile header moved
		checkpointPage("cccc", append(ids[:3:3], ids[4:]...)...), // r4 deleted: every later ticket shifts
		checkpointPage("dddd", append(ids[:3:3], append(ids[4:], "u1")...)...),
	}
	var entries []checkpointEntry
	for _, state := range states {
		e, err := store.add(checkpointEntry{Project: "p", Document: "d", Command: "easyeda sch test"}, state)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, e)
	}
	if kinds := fmt.Sprint(entries[0].Kind, entries[1].Kind, entries[2].Kind, entries[3].Kind); kinds != "fullsamedeltadelta" {
		t.Fatalf("kinds=%s", kinds)
	}
	if entries[2].Stored >= entries[0].Stored {
		t.Fatalf("delta after a ticket shift (%d bytes) is not smaller than the full copy (%d)", entries[2].Stored, entries[0].Stored)
	}
	listed, err := store.entries()
	if err != nil || len(listed) != len(states) {
		t.Fatalf("entries=%d err=%v", len(listed), err)
	}
	for i, state := range states {
		got, err := store.source(listed, entries[i].ID)
		if err != nil {
			t.Fatalf("state %d: %v", i, err)
		}
		// A "same" entry stands for its base, whose header differs; the body is the state.
		if sourceBody(got) != sourceBody(state) || (entries[i].Kind != "same" && got != state) {
			t.Fatalf("state %d was not rebuilt exactly", i)
		}
	}
	// Damage is reported, never papered over.
	if err = os.WriteFile(store.blob(entries[0]), gzipBytes([]byte(states[3])), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = store.source(listed, entries[2].ID); err == nil {
		t.Fatal("rebuilt a state from a tampered base")
	}
	if _, err = store.source(listed, "missing"); err == nil {
		t.Fatal("accepted an unknown checkpoint id")
	}
	if _, err = openCheckpointStore(t.TempDir(), "../p", "d"); err == nil {
		t.Fatal("accepted a path-like project id")
	}
}

func TestCheckpointPruneKeepsOnlyRebuildableEntries(t *testing.T) {
	store, _ := openCheckpointStore(t.TempDir(), "p", "d")
	var last checkpointEntry
	for i := 0; i < checkpointKeep+3*checkpointMaxDepth; i++ {
		e, err := store.add(checkpointEntry{Project: "p", Document: "d"}, checkpointPage("aaaa", fmt.Sprintf("r%d", i), "c1", "c2", "c3", "c4", "c5", "c6", "c7", "c8"))
		if err != nil {
			t.Fatal(err)
		}
		last = e
	}
	entries, err := store.entries()
	if err != nil || len(entries) == 0 || len(entries) > checkpointKeep+checkpointMaxDepth+1 || entries[0].Kind != "full" {
		t.Fatalf("kept %d entries, first kind %q, err=%v", len(entries), entries[0].Kind, err)
	}
	for _, e := range entries {
		if _, err = store.source(entries, e.ID); err != nil {
			t.Fatalf("kept entry %s no longer rebuilds: %v", e.ID, err)
		}
	}
	if entries[len(entries)-1].ID != last.ID {
		t.Fatal("the newest checkpoint was pruned")
	}
	blobs, _ := filepath.Glob(filepath.Join(store.dir, "*.gz"))
	if len(blobs) > len(entries) {
		t.Fatalf("%d blobs left for %d entries", len(blobs), len(entries))
	}
}

func TestCompareSourcesAcceptsEditorReExpansionButNotRealDifferences(t *testing.T) {
	checkpoint := checkpointPage("aaaa", "r1", "r2")
	// What the editor returns after re-creating r2: new tickets and order, the
	// nested attrs rewritten, library attributes added, Device emptied.
	restored := strings.Join([]string{
		`{"type":"DOCHEAD"}||{"docType":"SCH_PAGE","client":"zzzz"}|`,
		`{"type":"COMPONENT","ticket":9,"id":"r2"}||{"x":10,"y":20,"attrs":{"isSwitchClose":null}}|`,
		`{"type":"ATTR","ticket":10,"id":"n1"}||{"value":"R2","key":"Designator","parentId":"r2"}|`,
		`{"type":"ATTR","ticket":11,"id":"n2"}||{"value":"","key":"Device","parentId":"r2"}|`,
		`{"type":"ATTR","ticket":12,"id":"n3"}||{"value":"C1525","key":"Supplier Part","parentId":"r2"}|`,
		`{"type":"CANVAS","ticket":1,"id":"CANVAS"}||{"originX":0}|`,
		`{"type":"COMPONENT","ticket":2,"id":"r1"}||{"x":10,"y":20,"attrs":{"isSwitchClose":false}}|`,
		`{"type":"ATTR","ticket":3,"id":"a-r1"}||{"value":"R1","key":"Designator","parentId":"r1"}|`,
		`{"type":"ATTR","ticket":4,"id":"d-r1"}||{"value":"dev-r1","key":"Device","parentId":"r1"}|`,
	}, "\n")
	diff, err := compareSources(checkpoint, restored)
	if err != nil || !diff.equivalent() || diff.DeviceAttributeReset != 1 || diff.AttributesAdded != 1 {
		t.Fatalf("diff=%+v err=%v", diff, err)
	}
	for name, page := range map[string]string{
		"moved part":        strings.Replace(restored, `{"x":10,"y":20,"attrs":{"isSwitchClose":null}}`, `{"x":11,"y":20,"attrs":{"isSwitchClose":null}}`, 1),
		"renamed part":      strings.Replace(restored, `"value":"R2"`, `"value":"R9"`, 1),
		"missing part":      checkpointPage("zzzz", "r1"),
		"extra part":        checkpointPage("zzzz", "r1", "r2", "r3"),
		"lost designator":   strings.Replace(restored, `{"type":"ATTR","ticket":10,"id":"n1"}||{"value":"R2","key":"Designator","parentId":"r2"}|`+"\n", "", 1),
		"other value reset": strings.Replace(restored, `"value":"R1"`, `"value":""`, 1),
	} {
		if diff, err = compareSources(checkpoint, page); err != nil || diff.equivalent() {
			t.Errorf("%s was accepted as equivalent: %+v err=%v", name, diff, err)
		}
	}
	if _, err = compareSources(checkpoint, "header\nnot a record"); err == nil {
		t.Fatal("accepted a page that does not use the known record form")
	}
}

// One CLI process writing a page: the page is read and recorded once, before
// the first write, and never for reads, dry-runs, PCB pages or when disabled.
func TestAutoCheckpointRecordsThePageOnceBeforeTheFirstWrite(t *testing.T) {
	page := checkpointPage("aaaa", "r1", "r2")
	run := func(t *testing.T, docType, mode string, actions ...[2]any) (order []string, entries []checkpointEntry) {
		t.Helper()
		root := t.TempDir()
		t.Setenv(envCheckpoint, mode)
		t.Setenv(envCheckpointDir, root)
		t.Setenv(envCheckpointLabel, "record-7")
		checkpointProcess = &checkpointState{settled: map[string]bool{}, covered: map[string]bool{}}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/health" {
				_, _ = w.Write([]byte(`{"service":"easyeda-agent","windows":[{"windowId":"w"}]}`))
				return
			}
			var request map[string]any
			_ = json.NewDecoder(r.Body).Decode(&request)
			order = append(order, request["action"].(string))
			context := map[string]any{"projectUuid": "p", "documentUuid": "d", "documentType": docType}
			result := map[string]any{}
			if request["action"] == "document.source.get" {
				result = map[string]any{"schemaVersion": 1, "target": documentSourceTarget{"p", "d", "schematic"}, "source": page,
					"availability": map[string]bool{"getDocumentSource": true}, "verified": true, "partial": false, "incomplete": false}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result, "context": context})
		}))
		defer srv.Close()
		host, port, _ := strings.Cut(strings.TrimPrefix(srv.URL, "http://"), ":")
		cfg := &appConfig{host: host, ports: port + "-" + port, command: "easyeda sch test"}
		for _, a := range actions {
			if _, err := postAction(cfg, a[0].(string), "w", a[1], 0); err != nil {
				t.Fatal(err)
			}
		}
		if store, err := openCheckpointStore(root, "p", "d"); err == nil {
			entries, _ = store.entries()
		}
		return order, entries
	}
	write := [2]any{"schematic.text.create", map[string]any{"content": "x"}}
	order, entries := run(t, "schematic", "", [2]any{"schematic.read", nil}, write, write)
	if got := strings.Join(order, " "); got != "schematic.read document.current document.source.get schematic.text.create schematic.text.create" {
		t.Fatalf("order: %s", got)
	}
	if len(entries) != 1 || entries[0].Kind != "full" || entries[0].Label != "record-7" || entries[0].Action != "schematic.text.create" || entries[0].Command != "easyeda sch test" {
		t.Fatalf("entries: %+v", entries)
	}
	for name, tc := range map[string]struct {
		docType, mode string
		action        [2]any
	}{
		"disabled": {"schematic", "off", write},
		"dry run":  {"schematic", "", [2]any{"schematic.text.create", map[string]any{"dryRun": true}}},
		"pcb page": {"pcb", "", write},
	} {
		if _, entries = run(t, tc.docType, tc.mode, tc.action); len(entries) != 0 {
			t.Errorf("%s recorded a checkpoint: %+v", name, entries)
		}
	}
}
