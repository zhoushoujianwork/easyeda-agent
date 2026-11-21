package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
)

// fakeRouteBoard models the host behaviour behind #312: deleting a T-branch
// merges the two collinear halves it split into one primitive with a NEW id.
type fakeRouteBoard struct{ tracks map[string]bool }

func (b *fakeRouteBoard) delete(ids []string) string {
	var removed, notFound []string
	for _, id := range ids {
		if b.tracks[id] {
			delete(b.tracks, id)
			removed = append(removed, id)
		} else {
			notFound = append(notFound, id)
		}
	}
	// Branch B gone while A1/A2 still exist → the editor merges them.
	if !b.tracks["B"] && b.tracks["A1"] && b.tracks["A2"] {
		delete(b.tracks, "A1")
		delete(b.tracks, "A2")
		b.tracks["Amerged"] = true
	}
	if len(removed) == 0 {
		return `{"ok":false,"error":{"code":"EDA_CALL_FAILED","message":"Nothing to delete"}}`
	}
	rem := make([]map[string]any, 0, len(removed))
	for _, id := range removed {
		rem = append(rem, map[string]any{"primitiveId": id, "kind": "track"})
	}
	if notFound == nil {
		notFound = []string{}
	}
	body, _ := json.Marshal(map[string]any{"ok": true, "result": map[string]any{
		"removed": rem, "count": len(removed), "skippedLocked": []string{}, "notFound": notFound}})
	return string(body)
}

func (b *fakeRouteBoard) left() []string {
	var out []string
	for id := range b.tracks {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func runTrackDelete(t *testing.T, board *fakeRouteBoard, ids string) (string, error) {
	t.Helper()
	cfg, _, cleanup := newAutolayoutTestDaemon(t, func(_ int, call autolayoutTestCall) string {
		if call.Action != "pcb.route.delete" || call.Payload["kind"] != "track" {
			return fmt.Sprintf(`{"ok":false,"error":{"code":"X","message":"unexpected %s"}}`, call.Action)
		}
		raw, _ := call.Payload["primitiveIds"].([]any)
		ids := make([]string, 0, len(raw))
		for _, v := range raw {
			ids = append(ids, asString(v))
		}
		return board.delete(ids)
	})
	defer cleanup()
	var stdout, stderr bytes.Buffer
	cmd := newPcbCmd(cfg, &stdout, &stderr)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	cmd.SetArgs([]string{"track-delete", "--ids", ids})
	err := cmd.Execute()
	return stdout.String(), err
}

func TestTrackDeleteExitContract(t *testing.T) {
	t.Run("all valid in one call", func(t *testing.T) {
		board := &fakeRouteBoard{tracks: map[string]bool{"B": true, "A1": true, "A2": true}}
		out, err := runTrackDelete(t, board, "B,A1,A2")
		if err != nil {
			t.Fatalf("all-valid delete must succeed: %v", err)
		}
		if len(board.left()) != 0 || !strings.Contains(out, `"count":3`) {
			t.Fatalf("left=%v out=%s", board.left(), out)
		}
	})
	t.Run("all invalid", func(t *testing.T) {
		board := &fakeRouteBoard{tracks: map[string]bool{"X": true}}
		if _, err := runTrackDelete(t, board, "gone1,gone2"); err == nil {
			t.Fatal("all-stale delete must fail")
		}
	})
	t.Run("T merge across calls is reported, not ok", func(t *testing.T) {
		board := &fakeRouteBoard{tracks: map[string]bool{"B": true, "A1": true, "A2": true, "C": true}}
		if _, err := runTrackDelete(t, board, "B"); err != nil {
			t.Fatalf("first batch: %v", err)
		}
		out, err := runTrackDelete(t, board, "A1,A2,C")
		if err == nil {
			t.Fatalf("stale ids in a partially valid batch must exit non-zero; out=%s", out)
		}
		for _, want := range []string{"2 not found", "removed 1", "re-export"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error %q missing %q", err, want)
			}
		}
		if !strings.Contains(out, `"notFound":["A1","A2"]`) {
			t.Fatalf("stdout must keep the full result: %s", out)
		}
		if got := board.left(); len(got) != 1 || got[0] != "Amerged" {
			t.Fatalf("fixture lost the merged survivor: %v", got)
		}
	})
}

func TestRouteDeleteResultError(t *testing.T) {
	for _, tc := range []struct {
		name, result string
		fail         bool
	}{
		{"clean", `{"count":2,"notFound":[],"skippedLocked":[]}`, false},
		{"notFound", `{"count":1,"notFound":["a"],"skippedLocked":[]}`, true},
		{"locked", `{"count":1,"notFound":[],"skippedLocked":["l"]}`, true},
		{"notDeleted", `{"ok":false,"count":1,"notFound":[],"skippedLocked":[],"notDeleted":["s"]}`, true},
		{"notDeletable", `{"ok":false,"count":1,"notFound":[],"skippedLocked":[],"notDeletable":[{"primitiveId":"e"}]}`, true},
		{"bare ok false", `{"ok":false,"count":0}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var res map[string]any
			if err := json.Unmarshal([]byte(tc.result), &res); err != nil {
				t.Fatal(err)
			}
			if err := routeDeleteResultError("pcb.route.delete", res); (err != nil) != tc.fail {
				t.Fatalf("err=%v fail=%v", err, tc.fail)
			}
			if routeDeleteResultError("pcb.line.list", res) != nil {
				t.Fatal("guard must only apply to pcb.route.delete")
			}
		})
	}
}

func TestApplyRouteDeletePartialFailsStep(t *testing.T) {
	cfg, _, cleanup := newAutolayoutTestDaemon(t, func(_ int, _ autolayoutTestCall) string {
		return `{"ok":true,"result":{"count":1,"removed":[{"primitiveId":"C"}],"skippedLocked":[],"notFound":["A1"]}}`
	})
	defer cleanup()
	r := applyRunner{cfg: cfg, window: "w1"}
	res, err := r.runAction("pcb.route.delete", map[string]any{"primitiveIds": []string{"A1", "C"}}, time.Second)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("apply must fail a partial delete step: %v", err)
	}
	if m, _ := res.(map[string]any); m == nil || m["count"] == nil {
		t.Fatalf("apply must keep the result for the journal: %#v", res)
	}
}
