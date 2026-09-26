package app

import (
	"bytes"
	"encoding/json"
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
	for _, mode := range []string{"standalone", "gate-preload", "gate-no-preload", "gate-old-preload"} {
		t.Run(mode, func(t *testing.T) {
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
					if req.Payload["includePins"] != true || req.Payload["includeBBox"] != true || req.Payload["includeWires"] != true || req.Payload["includeConnectivitySummary"] != true {
						t.Errorf("incomplete snapshot request: %v", req.Payload)
					}
					// Match the connector contract: requesting wires does not
					// implicitly request the separate connectivity inventory.
					returned := map[string]any{}
					for key, value := range raw {
						returned[key] = value
					}
					if req.Payload["includeConnectivitySummary"] != true {
						delete(returned, "connectivitySummary")
					}
					json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": returned})
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
			if mode != "standalone" {
				var snapshot *schGeomSnapshot
				switch mode {
				case "gate-preload":
					snapshot = gatePreloadGeometry(cfg, "fixture", false)
				case "gate-old-preload":
					// A prior request without the summary flag cannot be reused,
					// even if an over-complete mock happened to return a summary.
					comps, err := parseLayoutComps(raw)
					if err != nil {
						t.Fatal(err)
					}
					snapshot = &schGeomSnapshot{comps: comps, res: &actionResult{Result: raw}, withPins: true, withWires: true}
				}
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
				t.Fatalf("wanted one shared components+pins+wires+inventory snapshot, got %d", reads)
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
	snapshot := &schGeomSnapshot{comps: comps, res: &actionResult{Result: raw}, withPins: true, withWires: true, withConnectivitySummary: true}
	// No daemon is configured: a complete-snapshot failure must return before
	// attempting any legacy second read or ownership lookup.
	stage := gateClustersStage(&appConfig{}, "fixture", true, snapshot)
	if stage.Status != gateStatusError || !strings.Contains(stage.Error, "wire inventory missing") {
		t.Fatalf("unknown geometry must block strict gate: %+v", stage)
	}
}

func TestSchClustersStrictOmittedRawSegmentIsBlocked(t *testing.T) {
	raw := clusterP2Snapshot(t)
	wires := raw["wires"].([]any)
	kept := make([]any, 0, len(wires)-1)
	for _, value := range wires {
		w := value.(map[string]any)
		if w["primitiveId"] == "95785aa8040cc979" && w["segmentIndex"] == 1.0 {
			continue // SW2 vertical X segment; same wire's segment zero remains.
		}
		kept = append(kept, value)
	}
	if len(kept) != len(wires)-1 {
		t.Fatal("fixture must omit exactly the crossing segment")
	}
	raw["wires"] = kept
	comps, err := parseLayoutComps(raw)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := schClusterSnapshotWires(raw)
	if err != nil {
		t.Fatal(err)
	}
	cs, _ := buildSchClusters(comps, observed)
	if clusterSW2R5Overlap(judgeSchClustersWith(cs, nil, 0, nil)) {
		t.Fatal("counterexample requires the missing segment to hide the collision")
	}
	// Keep the real summary count of 30 primitives: a unique-ID-only check
	// cannot detect this omission. If that check fails open, ownership lookup
	// is harmlessly handled by the local HTTP fixture.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			io.WriteString(w, `{"service":"easyeda-agent","windows":[]}`)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": map[string]any{"code": "FIXTURE_NO_CONTEXT", "message": "no group metadata"}})
	}))
	defer srv.Close()
	host, port, _ := strings.Cut(strings.TrimPrefix(srv.URL, "http://"), ":")
	cfg := &appConfig{host: host, ports: port + "-" + port}
	snapshot := &schGeomSnapshot{comps: comps, res: &actionResult{Result: raw}, withPins: true, withWires: true, withConnectivitySummary: true}
	stage := gateClustersStage(cfg, "fixture", true, snapshot)
	if stage.Status != gateStatusError || !strings.Contains(stage.Error, "raw segments omitted") {
		t.Fatalf("missing raw segment must block before judging incomplete geometry: status=%s error=%q summary=%s", stage.Status, stage.Error, stage.Summary)
	}
}

func TestSchClustersStrictUnknownInventoryBlocksEvenWithoutCollisions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"missing summary", func(r map[string]any) { delete(r, "connectivitySummary") }},
		{"wrong scope", func(r map[string]any) { r["connectivitySummary"].(map[string]any)["scope"] = "allPages" }},
		{"missing marker count", func(r map[string]any) { delete(r["connectivitySummary"].(map[string]any), "netports") }},
		{"unknown bus inventory", func(r map[string]any) { delete(r["connectivitySummary"].(map[string]any), "buses") }},
		{"unsupported short symbol", func(r map[string]any) { r["connectivitySummary"].(map[string]any)["shortSymbols"] = 1 }},
		{"unaccounted marker", func(r map[string]any) { r["connectivitySummary"].(map[string]any)["netflags"] = 1 }},
		{"unaccounted wire", func(r map[string]any) { r["connectivitySummary"].(map[string]any)["wires"] = 1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// No clusters/collisions: the missing inventory itself must be
			// reported, rather than silently converting unknown into pass.
			raw := map[string]any{"components": []any{}, "count": 0, "wiresAvailable": true, "wires": []any{},
				"connectivitySummary": map[string]any{"scope": "activePage", "wires": 0, "netflags": 0, "netports": 0, "netlabels": 0, "buses": 0, "shortSymbols": 0}}
			tc.mutate(raw)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/health" {
					io.WriteString(w, `{"service":"easyeda-agent","windows":[]}`)
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
				if req.Action != "schematic.components.list" || req.Payload["includeConnectivitySummary"] != true {
					t.Errorf("must stop after the incomplete snapshot: %+v", req)
				}
				json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": raw})
			}))
			defer srv.Close()
			host, port, _ := strings.Cut(strings.TrimPrefix(srv.URL, "http://"), ":")
			cfg := &appConfig{host: host, ports: port + "-" + port}
			stage := gateClustersStage(cfg, "fixture", true, gatePreloadGeometry(cfg, "fixture", false))
			if stage.Status != gateStatusError || !strings.Contains(stage.Error, "inventory") {
				t.Fatalf("unknown inventory must block strict gate: %+v", stage)
			}
			var out, errOut bytes.Buffer
			err := runSchClusters(cfg, "fixture", 0, true, true, false, &out, &errOut)
			if err == nil || !strings.Contains(err.Error(), "inventory") || out.Len() != 0 {
				t.Fatalf("unknown inventory must not produce a passed report: err=%v out=%s", err, out.String())
			}
		})
	}
}
