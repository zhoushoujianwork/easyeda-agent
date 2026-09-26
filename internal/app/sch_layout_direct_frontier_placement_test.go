package app

import "testing"

func TestDirectAttachmentSkipsSealedOneGridJoin(t *testing.T) {
	for _, policy := range []string{"direct", "local_power", "local_ground", "module_port"} {
		t.Run(policy, func(t *testing.T) {
			core := powerLayoutPlacement{
				Designator: "A", BBox: layoutBBox{-20, -10, 0, 10},
				Pins: []powerLayoutPin{{Number: "1", Net: "N", X: 5, Y: 0, Rotation: mazeTestRotation(0)}},
			}
			peripheral := powerLayoutPlacement{
				Designator: "B", BBox: layoutBBox{-10, -10, 10, 10},
				Pins: []powerLayoutPin{{Number: "1", Net: "N", X: -20, Y: 0, Rotation: mazeTestRotation(180)}},
			}
			pair := libAttachmentPair{host: core.Pins[0], own: peripheral.Pins[0], side: "right"}
			budget := 1000
			result, err := libPlacePeripheralPairsWithRouting(powerLayoutPlan{Placements: []powerLayoutPlacement{core}}, peripheral,
				[]libAttachmentPair{pair}, map[string]string{"N": policy}, &budget, nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if result == nil || len(result.Placements) != 2 {
				t.Fatalf("no complete placement: %+v", result)
			}
			placed := result.Placements[1]
			if placed.Pins[0].X <= 10 || placed.Pins[0].Y != 0 {
				t.Fatalf("sealed 5 raw join was selected instead of a branchable join: %+v", placed.Pins[0])
			}
			if !libPinsShareIsland(result, core.Pins[0], placed.Pins[0]) || !libIslandMergeCanContinue(result, core.Pins[0], placed.Pins[0]) {
				t.Fatal("selected direct join is not a branchable physical island")
			}
		})
	}
}
