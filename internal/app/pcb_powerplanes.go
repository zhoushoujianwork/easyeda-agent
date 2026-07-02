package app

// pcb power-planes — the 4-layer power-distribution heuristic (#26 increment 2).
//
// The 2-layer pour conflict (two power nets can't both connect on one shared layer —
// ceshi stranded 5 of the 3V3 pads) is solved properly by dedicated inner planes.
// This command, validated on ceshi (DRC 31 → 10 → 3, No-Connection → 0):
//   1. ensures the board has ≥4 copper layers (pcb.stackup.set),
//   2. assigns GND to one inner layer and the power nets to another,
//   3. via-stitches every power/ground pad DOWN to its plane (a through via on that
//      net — the connection point the inner pour needs; without it the inner pour is
//      all isolated islands and deposits nothing),
//   4. pours each net on its inner layer, then rebuilds.
// The pour MUST come after the vias (empty otherwise). Power/ground nets are the
// isGlobalNet set (GND/3V3/VCC/…); everything else stays a routed signal.

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

type ppNet struct {
	Net   string      `json:"net"`
	Layer int         `json:"layer"`
	Pads  [][2]float64 `json:"-"`
	Vias  int         `json:"viasPlaced"`
	Poured bool       `json:"poured"`
}

// runPowerPlanes orchestrates the 4-layer power-plane build. gndLayer/powerLayer are
// inner-layer ids (15=Inner1, 16=Inner2 on a 4-layer board). gndAsPlane flips the GND
// inner layer to 内电层/PLANE after pouring (the verified pour-while-SIGNAL → flip →
// rebuild recipe; DRC stays clean — see the pcb-inner-plane-fill memory).
func runPowerPlanes(cfg *appConfig, window string, gndLayer, powerLayer int, gndAsPlane, dryRun bool, stdout, stderr io.Writer) error {
	// 1. Pads → group power/ground pads by net.
	res, err := requestAction(cfg, "pcb.components.list", window, map[string]any{"includePads": true})
	if err != nil {
		return fmt.Errorf("read pads: %w", err)
	}
	comps := parseApComps(res.Result)
	byNet := map[string][][2]float64{}
	for _, c := range comps {
		for _, p := range c.pads {
			if isGlobalNet(p.net) {
				byNet[p.net] = append(byNet[p.net], [2]float64{p.x, p.y})
			}
		}
	}
	if len(byNet) == 0 {
		return fmt.Errorf("no power/ground nets found (nothing matches isGlobalNet: GND/VCC/3V3/…)")
	}

	// 2. Assign a layer per net: GND → gndLayer; other power nets → powerLayer (in
	//    name order). Two power nets sharing powerLayer would re-create the conflict,
	//    so warn past the first — they need their own plane (6+ layers).
	nets := make([]string, 0, len(byNet))
	for n := range byNet {
		nets = append(nets, n)
	}
	sort.Strings(nets)
	plan := make([]ppNet, 0, len(nets))
	powerAssigned := 0
	var warnings []string
	for _, n := range nets {
		layer := powerLayer
		if isGndNetName(n) {
			layer = gndLayer
		} else {
			if powerAssigned > 0 {
				warnings = append(warnings, fmt.Sprintf("net %q shares plane layer %d with another power net — give it a dedicated inner layer (6+ layers) to avoid a split conflict", n, powerLayer))
			}
			powerAssigned++
		}
		plan = append(plan, ppNet{Net: n, Layer: layer, Pads: byNet[n]})
	}

	// Board outline → the pour rectangle (inset a hair so the plane sits inside).
	rect, rerr := outlineRect(cfg, window, 10)
	if rerr != nil {
		return fmt.Errorf("read board outline (needed for the plane pour): %w", rerr)
	}

	if dryRun {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"dryRun": true, "plan": plan, "warnings": warnings, "pourRect": rect, "gndAsPlane": gndAsPlane, "gndLayer": gndLayer})
	}

	// 3. Ensure ≥4 copper layers.
	if _, err := requestAction(cfg, "pcb.stackup.set", window, map[string]any{"count": 4}); err != nil {
		return fmt.Errorf("set 4 copper layers: %w", err)
	}

	// 4. Per net: via-stitch each pad, then pour on the net's inner layer.
	for i := range plan {
		p := &plan[i]
		for _, xy := range p.Pads {
			if _, err := requestAction(cfg, "pcb.via.create", window, map[string]any{"x": xy[0], "y": xy[1], "net": p.Net}); err != nil {
				continue // best-effort; a failed via just leaves that pad for DRC
			}
			p.Vias++
		}
		pres, perr := requestAction(cfg, "pcb.pour.create", window, map[string]any{
			"net": p.Net, "layer": p.Layer, "points": rectCorners(rect[0], rect[1], rect[2], rect[3]),
		})
		if perr == nil && pres != nil {
			if b, ok := mnav(pres.Result, "poured").(bool); ok {
				p.Poured = b
			}
		}
	}

	// 4b. Flip the GND inner layer to 内电层/PLANE. The verified recipe is pour-while-
	//     SIGNAL (done above) → flip type → rebuild; the net-bound fill survives and
	//     DRC stays clean. The power layer stays SIGNAL so its net pour behaves as an
	//     ordinary positive plane (matches the customer stackup: GND=内电层, VCC=信号层).
	planeFlipped := false
	if gndAsPlane {
		if _, err := requestAction(cfg, "pcb.stackup.set", window, map[string]any{
			"layers": []map[string]any{{"id": gndLayer, "type": "plane"}},
		}); err != nil {
			fmt.Fprintf(stderr, "power-planes: could not set layer %d to 内电层/PLANE: %v\n", gndLayer, err)
		} else {
			planeFlipped = true
		}
	}

	// 5. Rebuild all pours so they reflow around the vias/obstacles (and settle the
	//    GND layer as a proper inner plane after the type flip).
	if _, err := requestAction(cfg, "pcb.pour.rebuild", window, nil); err != nil {
		fmt.Fprintf(stderr, "power-planes: pour rebuild failed: %v\n", err)
	}

	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{
		"ok": true, "gndLayer": gndLayer, "powerLayer": powerLayer,
		"gndAsPlane": gndAsPlane, "gndPlaneFlipped": planeFlipped,
		"planes": plan, "warnings": warnings,
	})
}

// isGndNetName reports whether a net is a ground net (for plane assignment).
func isGndNetName(net string) bool {
	return strings.Contains(strings.ToLower(net), "gnd")
}

// outlineRect returns the board outline bbox inset by `inset` mil as [minX,minY,maxX,maxY].
func outlineRect(cfg *appConfig, window string, inset float64) ([4]float64, error) {
	res, err := requestAction(cfg, "pcb.outline.get", window, nil)
	if err != nil || res == nil {
		return [4]float64{}, fmt.Errorf("outline.get failed: %v", err)
	}
	bb, ok := mnav(res.Result, "bbox").(map[string]any)
	if !ok {
		return [4]float64{}, fmt.Errorf("no board outline set — run `pcb outline-fit` first")
	}
	minX, _ := asFloatOK(bb["minX"])
	minY, _ := asFloatOK(bb["minY"])
	maxX, _ := asFloatOK(bb["maxX"])
	maxY, _ := asFloatOK(bb["maxY"])
	return [4]float64{minX + inset, minY + inset, maxX - inset, maxY - inset}, nil
}
