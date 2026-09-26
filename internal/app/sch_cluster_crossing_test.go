package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/internal/schguard"
)

func clusterP2Snapshot(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile("testdata/sch-clusters-p2-crossing.json")
	if err != nil {
		t.Fatal(err)
	}
	var r map[string]any
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSchClustersP2VerifiedInternalCrossing(t *testing.T) {
	r := clusterP2Snapshot(t)
	comps, err := parseLayoutComps(r)
	if err != nil {
		t.Fatal(err)
	}
	wires, err := schClusterSnapshotWires(r)
	if err != nil {
		t.Fatal(err)
	}
	cs, _ := buildSchClusters(comps, wires)
	if len(cs) != 16 || len(wires) != 30 {
		t.Fatalf("fixture inventory changed: clusters=%d wires=%d", len(cs), len(wires))
	}
	old := judgeSchClustersWith(cs, nil, 0, nil)
	if len(old) != 1 || !clusterSW2R5Overlap(old) || old[0].OvX != 1 || old[0].OvY != 1 {
		t.Fatalf("must reproduce exact old false overlap before evidence exception: %+v", old)
	}
	proof, err := schguard.VerifiedWireCrossings(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range judgeSchClustersWithCrossings(cs, nil, 0, nil, proof) {
		if f.Type == "overlap" && ((f.A == "SW2" && f.B == "R5") || (f.A == "R5" && f.B == "SW2")) {
			t.Fatalf("complete fresh P2 bare internal X must not be a cluster collision: %+v", f)
		}
	}
}

func clusterSW2R5Overlap(fs []schClusterFinding) bool {
	for _, f := range fs {
		if f.Type == "overlap" && ((f.A == "SW2" && f.B == "R5") || (f.A == "R5" && f.B == "SW2")) {
			return true
		}
	}
	return false
}

func TestSchClustersP2UnknownEvidenceKeepsCollision(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"pin netlist unavailable", func(r map[string]any) { r["pinNetsAvailable"] = false }},
		{"wire inventory unavailable", func(r map[string]any) { r["wiresAvailable"] = false }},
		{"raw omitted", func(r map[string]any) { delete(r["wires"].([]any)[0].(map[string]any), "rawLine") }},
		{"raw incomplete", func(r map[string]any) { r["wires"] = r["wires"].([]any)[1:] }},
		{"marker inventory incomplete", func(r map[string]any) { r["connectivitySummary"].(map[string]any)["netports"] = 20.0 }},
		{"actual pin net absent", func(r map[string]any) {
			for _, v := range r["components"].([]any) {
				c := v.(map[string]any)
				if c["designator"] == "SW2" {
					for _, p := range c["pins"].([]any) {
						p.(map[string]any)["net"] = nil
					}
				}
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := clusterP2Snapshot(t)
			comps, _ := parseLayoutComps(r)
			wires, _ := schClusterSnapshotWires(r)
			cs, _ := buildSchClusters(comps, wires)
			tc.mutate(r)
			proof, _ := schguard.VerifiedWireCrossings(r)
			if !clusterSW2R5Overlap(judgeSchClustersWithCrossings(cs, nil, 0, nil, proof)) {
				t.Fatal("unknown evidence must retain the original collision")
			}
		})
	}
}

func TestSchClustersCrossingProofNeverExemptsBodiesOrMarkers(t *testing.T) {
	for _, kind := range []string{"part", "netport", "wire-without-provenance"} {
		t.Run(kind, func(t *testing.T) {
			r := clusterP2Snapshot(t)
			comps, _ := parseLayoutComps(r)
			wires, _ := schClusterSnapshotWires(r)
			cs, _ := buildSchClusters(comps, wires)
			proof, err := schguard.VerifiedWireCrossings(r)
			if err != nil {
				t.Fatal(err)
			}
			for i := range cs {
				if cs[i].Designator == "R5" {
					b := layoutBBox{MinX: 259.5, MaxX: 260.5, MinY: 429.5, MaxY: 430.5}
					cs[i].Members = append(cs[i].Members, b)
					cs[i].Typed = append(cs[i].Typed, schClusterTyped{Kind: kind, BBox: b})
					cs[i].Box = schUnionBBox(cs[i].Box, b)
				}
			}
			if !clusterSW2R5Overlap(judgeSchClustersWithCrossings(cs, nil, 0, nil, proof)) {
				t.Fatal("wire proof must not exempt a body/marker/untyped member at the crossing")
			}
		})
	}
}

func TestSchClustersCommandsUseOneCompleteSnapshot(t *testing.T) {
	for _, gate := range []bool{false, true} {
		t.Run(fmt.Sprintf("gate=%v", gate), func(t *testing.T) {
			raw := clusterP2Snapshot(t)
			reads := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/health" {
					io.WriteString(w, `{"service":"easyeda-agent","windows":[]}`)
					return
				}
				if r.URL.Path != "/action" {
					http.NotFound(w, r)
					return
				}
				var req struct {
					Action  string         `json:"action"`
					Payload map[string]any `json:"payload"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					return
				}
				switch req.Action {
				case "schematic.components.list":
					reads++
					if req.Payload["includePins"] != true || req.Payload["includeBBox"] != true || req.Payload["includeWires"] != true {
						t.Errorf("incomplete snapshot request: %v", req.Payload)
					}
					json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": raw})
				case "document.current":
					// No persisted ownership fixture is needed to prove this collision.
					json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": map[string]any{"code": "FIXTURE_NO_CONTEXT", "message": "no group metadata"}})
				default:
					t.Errorf("unexpected action (no second wire fetch allowed): %s", req.Action)
					json.NewEncoder(w).Encode(map[string]any{"ok": false})
				}
			}))
			defer srv.Close()
			host, port, _ := strings.Cut(strings.TrimPrefix(srv.URL, "http://"), ":")
			cfg := &appConfig{host: host, ports: port + "-" + port}
			if gate {
				snapshot := gatePreloadGeometry(cfg, "fixture", false)
				stage := gateClustersStage(cfg, "fixture", true, snapshot)
				report, ok := stage.Detail.(schClusterReport)
				if !ok {
					t.Fatalf("missing gate result: %+v", stage)
				}
				if clusterSW2R5Overlap(report.Findings) || stage.Errors != 0 {
					t.Fatalf("P2 false overlap survived gate: %+v", stage)
				}
			} else {
				var out, errOut bytes.Buffer
				if err := runSchClusters(cfg, "fixture", 0, true, false, false, &out, &errOut); err != nil {
					t.Fatalf("clusters error: %v %s", err, errOut.String())
				}
				var report schClusterReport
				if err := json.Unmarshal(out.Bytes(), &report); err != nil {
					t.Fatal(err)
				}
				if clusterSW2R5Overlap(report.Findings) {
					t.Fatal("P2 false overlap survived command")
				}
			}
			if reads != 1 {
				t.Fatalf("wanted one shared components+pins+wires snapshot, got %d", reads)
			}
		})
	}
}

func TestSchClustersStrictMissingWireSnapshotIsBlocked(t *testing.T) {
	raw := clusterP2Snapshot(t)
	delete(raw, "wires")
	comps, err := parseLayoutComps(raw)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := &schGeomSnapshot{comps: comps, res: &actionResult{Result: raw}, withPins: true, withWires: true}
	// No daemon is configured: a complete-snapshot failure must return before
	// attempting any legacy second read or ownership lookup.
	stage := gateClustersStage(&appConfig{}, "fixture", true, snapshot)
	if stage.Status != gateStatusError || !strings.Contains(stage.Error, "wire inventory missing") {
		t.Fatalf("unknown geometry must block strict gate: %+v", stage)
	}
}
