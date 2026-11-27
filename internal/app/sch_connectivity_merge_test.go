package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/internal/connectivity"
)

func TestConnectivityAllPagesRestoresOriginalAndRejectsMixedEvidence(t *testing.T) {
	for _, failure := range []string{"", "read-failed", "wrong-project", "wrong-pin-page", "missing-pins", "restore-failed"} {
		t.Run(failure, func(t *testing.T) {
			active := "a" // first listed page already active: old restore code missed this case
			cfg, daemon, closeServer := newAutolayoutTestDaemon(t, func(_ int, c autolayoutTestCall) string {
				project, doc := "p", active
				result := map[string]any{}
				switch c.Action {
				case "document.open":
					if failure == "restore-failed" && active == "b" && c.Payload["uuid"] == "a" {
						return `{"ok":false,"error":{"code":"EDA_CALL_FAILED","message":"test restore failure"}}`
					}
					active = c.Payload["uuid"].(string)
					doc = active
				case "document.current":
					result["uuid"] = active
				case "schematic.pages.list":
					result["pages"] = []any{map[string]any{"uuid": "a", "name": "A"}, map[string]any{"uuid": "b", "name": "B"}}
				case "pcb.documents.list":
					result["pcbs"] = []any{}
				case "schematic.components.list", "schematic.read":
					ref := "U1"
					if active == "b" {
						ref = "U2"
						if c.Action == "schematic.read" && failure == "read-failed" {
							return `{"ok":false,"error":{"code":"EDA_CALL_FAILED","message":"test read failure"}}`
						}
						if c.Action == "schematic.read" && failure == "wrong-project" {
							project = "other"
						}
						if c.Payload["includePins"] == true && failure == "wrong-pin-page" {
							doc = "a"
						}
					}
					result["count"] = 1
					if !(active == "b" && c.Payload["includePins"] == true && failure == "missing-pins") {
						result["components"] = []any{map[string]any{"componentType": "part", "designator": ref, "pins": []any{map[string]any{"number": "1", "net": "GND"}}}}
					}
				}
				b, _ := json.Marshal(result)
				return fmt.Sprintf(`{"ok":true,"context":{"projectUuid":%q,"documentUuid":%q,"documentType":"schematic"},"result":%s}`, project, doc, b)
			})
			defer closeServer()
			window := "w1"
			var stdout bytes.Buffer
			cmd := newSchConnectivityCmd(cfg, &window, &stdout, io.Discard)
			cmd.SetArgs([]string{"--all-pages"})
			err := cmd.Execute()
			if failure == "" {
				if err != nil {
					t.Fatal(err)
				}
				var got connectivity.Document
				if err := json.Unmarshal(stdout.Bytes(), &got); err != nil || len(got.Nets) != 1 || len(got.Components) != 2 || got.ProjectID != "p" || got.DocumentID != "" {
					t.Fatalf("invalid exported aggregate: %s (%v)", stdout.String(), err)
				}
			} else if err == nil || stdout.Len() != 0 {
				t.Fatalf("mixed/failed data emitted: %s (%v)", stdout.String(), err)
			}
			if failure == "restore-failed" && (err == nil || !strings.Contains(err.Error(), "restore original document")) {
				t.Fatalf("restore failure was hidden: %v", err)
			}
			if failure != "restore-failed" && active != "a" {
				t.Fatalf("original document was not restored after %s", failure)
			}
			calls := daemon.snapshot()
			last := calls[len(calls)-1]
			if last.Action != "document.open" || last.Payload["uuid"] != "a" {
				t.Fatalf("last action must restore page a: %+v", last)
			}
		})
	}
}

// Units of one multi-unit device may sit on different pages; the all-pages
// export folds them into one component with per-unit pin attribution, and
// still refuses two distinct parts sharing a designator across pages (#362).
func TestConnectivityAllPagesFoldsMultiUnitDevice(t *testing.T) {
	unit := func(uniqueID, subPart, primitiveID string, pins ...[2]string) map[string]any {
		var list []any
		for _, p := range pins {
			list = append(list, map[string]any{"number": p[0], "net": p[1], "noConnected": false})
		}
		return map[string]any{"componentType": "part", "designator": "U18", "primitiveId": primitiveID, "uniqueId": uniqueID, "subPartName": subPart, "x": 10.0, "y": 20.0, "pins": list}
	}
	single := map[string]any{"componentType": "part", "designator": "C1", "primitiveId": "c1", "uniqueId": "gge5", "subPartName": "CAP.1", "pins": []any{map[string]any{"number": "1", "net": "VDD_1V0"}, map[string]any{"number": "2", "net": "GND"}}}
	for _, tc := range []struct {
		name   string
		pageA  []any
		pageB  []any
		reject string
	}{
		{"cross-page-units", []any{single, unit("gge1_3", "BRIDGE.1", "a1", [2]string{"A1", "GND"}, [2]string{"A2", "VDD_1V0"})}, []any{unit("gge1_3", "BRIDGE.2", "b1", [2]string{"B1", "USB_DP"}, [2]string{"B2", ""})}, ""},
		{"distinct-parts", []any{single, unit("gge1_3", "BRIDGE.1", "a1", [2]string{"A1", "GND"})}, []any{unit("gge9", "BRIDGE.2", "b1", [2]string{"B1", "USB_DP"})}, "different uniqueId"},
		{"overlapping-pins", []any{single, unit("gge1_3", "BRIDGE.1", "a1", [2]string{"A1", "GND"})}, []any{unit("gge1_3", "BRIDGE.2", "b1", [2]string{"A1", "USB_DP"})}, "pin A1 appears on sub-parts"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			active := "a"
			cfg, _, closeServer := newAutolayoutTestDaemon(t, func(_ int, c autolayoutTestCall) string {
				result := map[string]any{}
				switch c.Action {
				case "document.open":
					active = c.Payload["uuid"].(string)
				case "document.current":
					result["uuid"] = active
				case "schematic.pages.list":
					result["pages"] = []any{map[string]any{"uuid": "a", "name": "A"}, map[string]any{"uuid": "b", "name": "B"}}
				case "pcb.documents.list":
					result["pcbs"] = []any{}
				case "schematic.components.list", "schematic.read":
					result["components"] = tc.pageA
					if active == "b" {
						result["components"] = tc.pageB
					}
				}
				b, _ := json.Marshal(result)
				return fmt.Sprintf(`{"ok":true,"context":{"projectUuid":"p","documentUuid":%q,"documentType":"schematic"},"result":%s}`, active, b)
			})
			defer closeServer()
			window := "w1"
			var stdout bytes.Buffer
			cmd := newSchConnectivityCmd(cfg, &window, &stdout, io.Discard)
			cmd.SetArgs([]string{"--all-pages"})
			err := cmd.Execute()
			if tc.reject != "" {
				if err == nil || !strings.Contains(err.Error(), tc.reject) || stdout.Len() != 0 {
					t.Fatalf("got %v (%s), want rejection containing %q", err, stdout.String(), tc.reject)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var got connectivity.Document
			if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if len(got.Components) != 2 || got.DocumentID != "" || got.ProjectID != "p" {
				t.Fatalf("want C1 + one U18: %s", stdout.String())
			}
			var u18, c1 connectivity.Component
			for _, c := range got.Components {
				if c.Ref == "U18" {
					u18 = c
				} else {
					c1 = c
				}
			}
			if c1.PageID != "a" || c1.SubParts != nil || c1.Placement != nil && c1.Placement.X != 0 {
				t.Fatalf("single-unit part changed shape: %+v", c1)
			}
			if u18.ID != "cmp-U18" || u18.UniqueID != "gge1_3" || u18.Placement != nil || u18.PageID != "" || len(u18.SubParts) != 2 {
				t.Fatalf("multi-unit device not folded: %+v", u18)
			}
			if s := u18.SubParts; s[0].Name != "BRIDGE.1" || s[0].PageID != "a" || s[0].PrimitiveID != "a1" || s[0].Placement == nil || s[1].Name != "BRIDGE.2" || s[1].PageID != "b" || s[1].PageName != "B" {
				t.Fatalf("sub-part provenance lost: %+v", s)
			}
			owners := map[string]string{}
			for _, p := range u18.Pins {
				owners[p.Number] = p.SubPart
			}
			if len(owners) != 4 || owners["B2"] != "BRIDGE.2" || owners["A1"] != "BRIDGE.1" || owners["A2"] != "BRIDGE.1" || owners["B1"] != "BRIDGE.2" {
				t.Fatalf("pin attribution lost: %+v", u18.Pins)
			}
			named, err := got.NamedPins()
			if err != nil {
				t.Fatal(err)
			}
			if *named[`"U18"/"A1"`] != "GND" || *named[`"U18"/"A2"`] != "VDD_1V0" || *named[`"U18"/"B1"`] != "USB_DP" || named[`"U18"/"B2"`] != nil || *named[`"C1"/"1"`] != "VDD_1V0" {
				t.Fatalf("pin nets crossed or lost: %v", named)
			}
		})
	}
}
