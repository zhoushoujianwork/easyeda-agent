package app

import (
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/internal/schguard"
)

// Minimal native P2 geometry from one fresh complete snapshot. The two marker
// symbols and the existing calibrated GND text estimate are all retained.
func markerCollisionP2Fixture() ([]layoutComp, []schGroupWire) {
	gndRotation, simRotation := 270.0, 90.0
	comps := []layoutComp{
		clPart("J201", 1389.5, 359.5, 1440.5, 450.5, [2]float64{1380, 400}),
		clPart("R210", 1339.5, 380.5, 1360.5, 389.5, [2]float64{1330, 385}),
		{ID: "gnd-marker", ComponentType: "netflag", Net: "GND", X: 1255, Y: 400, AnchorAvailable: true, Rotation: &gndRotation,
			BBox: &layoutBBox{MinX: 1235.5, MinY: 389.5, MaxX: 1245.5, MaxY: 410.5}},
		{ID: "sim-vdd-marker", ComponentType: "netflag", Net: "SIM_VDD", X: 1280, Y: 385, AnchorAvailable: true, Rotation: &simRotation,
			BBox: &layoutBBox{MinX: 1269.5, MinY: 379.5, MaxX: 1275.5, MaxY: 390.5}},
	}
	wires := []schGroupWire{
		{ID: "gnd-stub", Points: []float64{1380, 400, 1255, 400}, ObservedSegments: [][4]float64{{1380, 400, 1255, 400}}},
		{ID: "sim-stub", Points: []float64{1330, 385, 1280, 385}, ObservedSegments: [][4]float64{{1330, 385, 1280, 385}}},
	}
	return comps, wires
}

func TestMarkerCollisionP2EmptyUnionCorner(t *testing.T) {
	comps, wires := markerCollisionP2Fixture()
	gnd, sim := comps[2], comps[3]
	band := flagTextBand(gnd)
	if band == nil || boxesIntersect(*gnd.BBox, *sim.BBox) || boxesIntersect(*band, *sim.BBox) {
		t.Fatal("fixture must isolate the empty symbol/text envelope corner")
	}
	x, y, hit := overlapExtent(markerJudgeBBox(gnd), *sim.BBox)
	if !hit || x != 3.5 || y != 1 {
		t.Fatalf("original P2 envelope evidence changed: hit=%v x=%v y=%v", hit, x, y)
	}
	cs, unowned := buildSchClusters(comps, wires)
	if unowned != 0 {
		t.Fatalf("fresh owned marker geometry became unowned: %d", unowned)
	}
	if got := judgeSchClusters(cs, nil, 0); len(got) != 0 {
		t.Fatalf("empty marker union corner is not occupied: %+v", got)
	}
	// eps=0 proves this comes from actual occupancy, not the checker's eps=1
	// floor hiding the original 3.5 x 1 envelope intersection.
	if got := markerOverlapFindings([]layoutComp{gnd, sim}, 0); len(got) != 0 {
		t.Fatalf("checker must share the separate symbol/text geometry: %+v", got)
	}
}

func TestMarkerCollisionPreservesTerminalEnvelopeAndSheetGuard(t *testing.T) {
	comps, wires := markerCollisionP2Fixture()
	cs, _ := buildSchClusters(comps, wires)
	var j *schCluster
	for i := range cs {
		if cs[i].Designator == "J201" {
			j = &cs[i]
		}
	}
	if j == nil || j.Markers != 1 || len(j.Typed) != len(j.Members) {
		t.Fatalf("legacy marker count/member alignment changed: %+v", j)
	}
	envelope := markerJudgeBBox(comps[2])
	seen := 0
	for i, member := range j.Typed {
		if member.Kind != "netflag" {
			continue
		}
		seen++
		if member.BBox != envelope || j.Members[i] != envelope || len(member.CollisionBoxes) != 2 {
			t.Fatalf("terminal envelope must remain separate from collision rectangles: %+v", member)
		}
		if member.CollisionBoxes[0] != *comps[2].BBox || member.CollisionBoxes[1] != *flagTextBand(comps[2]) {
			t.Fatalf("collision geometry lost symbol or text: %+v", member)
		}
	}
	g := zfGroupFromCluster(*j, 1, nil)
	if seen != 1 || len(g.Terms) != 1 || g.Terms[0].W != 37.5 || g.Terms[0].H != 22.5 {
		t.Fatalf("fallback terminal count and occupied envelope must remain unchanged: %+v", g)
	}
	usable := &layoutBBox{MinX: 1240, MinY: 0, MaxX: 1500, MaxY: 500}
	got := judgeSchClusters(cs, usable, 0)
	if len(got) != 1 || got[0].Type != "out-of-sheet" || got[0].A != "J201" || got[0].Level != "ERROR" {
		t.Fatalf("sheet occupancy must still use the complete envelope: %+v", got)
	}
}

func TestMarkerCollisionRealOccupiedIntersectionsRemainErrors(t *testing.T) {
	for _, kind := range []string{"symbol-part", "text-part", "text-marker", "text-wire", "symbol-wire"} {
		t.Run(kind, func(t *testing.T) {
			fixture, lead := markerCollisionP2Fixture()
			comps := []layoutComp{fixture[0], fixture[2]}
			wires := []schGroupWire{lead[0]}
			switch kind {
			case "symbol-part":
				comps = append(comps, clPart("C1", 1237, 393, 1243, 397))
			case "text-part":
				comps = append(comps, clPart("C1", 1260, 404, 1266, 410))
			case "text-marker":
				comps = append(comps, clPart("C1", 1300, 425, 1350, 445, [2]float64{1300, 405}),
					layoutComp{ID: "foreign-marker", ComponentType: "netlabel", Net: "SIG", X: 1266, Y: 405,
						BBox: &layoutBBox{MinX: 1260, MinY: 404, MaxX: 1266, MaxY: 410}})
				wires = append(wires, schGroupWire{ID: "foreign-lead", Points: []float64{1300, 405, 1266, 405}, ObservedSegments: [][4]float64{{1300, 405, 1266, 405}}})
			case "text-wire":
				comps = append(comps, clPart("C1", 1300, 425, 1350, 445, [2]float64{1300, 405}))
				wires = append(wires, schGroupWire{ID: "foreign-wire", Points: []float64{1300, 405, 1260, 405}, ObservedSegments: [][4]float64{{1300, 405, 1260, 405}}})
			case "symbol-wire":
				comps = append(comps, clPart("C1", 1200, 425, 1250, 445, [2]float64{1200, 395}))
				wires = append(wires, schGroupWire{ID: "foreign-wire", Points: []float64{1200, 395, 1240, 395}, ObservedSegments: [][4]float64{{1200, 395, 1240, 395}}})
			}
			cs, unowned := buildSchClusters(comps, wires)
			if unowned != 0 {
				t.Fatalf("fixture marker lost ownership: %d", unowned)
			}
			got := judgeSchClusters(cs, nil, 0)
			if len(got) != 1 || got[0].Type != "overlap" || got[0].Level != "ERROR" {
				t.Fatalf("real occupied %s collision must remain an ERROR: %+v", kind, got)
			}
			if kind != "text-wire" && kind != "symbol-wire" {
				got := markerOverlapFindings(comps, schMarkerOverlapEps)
				if len(got) != 1 || got[0].Type != "marker-overlap" || got[0].Level != "warn" {
					t.Fatalf("checker lost real %s collision: %+v", kind, got)
				}
			}
		})
	}
}

func TestMarkerCollisionTextNeverExemptedByWireCrossingProof(t *testing.T) {
	r := clusterP2Snapshot(t)
	comps, err := parseLayoutComps(r)
	if err != nil {
		t.Fatal(err)
	}
	wires, err := schClusterSnapshotWires(r)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := schguard.VerifiedWireCrossings(r)
	if err != nil {
		t.Fatal(err)
	}
	cs, _ := buildSchClusters(comps, wires)
	for i := range cs {
		if cs[i].Designator != "R5" {
			continue
		}
		text := layoutBBox{MinX: 259.5, MinY: 429.5, MaxX: 260.5, MaxY: 430.5}
		body := layoutBBox{MinX: 280, MinY: 429, MaxX: 281, MaxY: 431}
		envelope := schUnionBBox(body, text)
		cs[i].Members = append(cs[i].Members, envelope)
		cs[i].Typed = append(cs[i].Typed, schClusterTyped{Kind: "netflag", Net: "GND", BBox: envelope, CollisionBoxes: []layoutBBox{body, text}})
		cs[i].Box = schUnionBBox(cs[i].Box, envelope)
	}
	if !clusterSW2R5Overlap(judgeSchClustersWithCrossings(cs, nil, 0, nil, proof)) {
		t.Fatal("verified bare wire crossing must never exempt occupied marker text")
	}
}
