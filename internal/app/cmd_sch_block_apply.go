package app

// cmd_sch_block_apply.go — the PURE planner for `sch block-apply`.
//
// This is the vertical-slice experiment from chat/2026-07-16-blocks-data-model.md:
// blocks have proven useful as a structured KNOWLEDGE base, but nothing yet
// proves the JSON is worth maintaining as an executable IR. The only way to find
// out is to actually execute one: load a block → place its parts → wire its
// internal_nets → bind its ports → check → emit a traceable instance manifest.
//
// Scope is deliberately minimal (per that doc): parts / internal_nets / ports
// only. pcb_layout, placement, signals and silk are NOT consumed here — and the
// manifest says so explicitly rather than implying the whole block was honoured.
//
// The planner is PURE and deterministic: same block + parts library + scene
// always yields the same designators, coordinates and nets, so it is unit
// testable with no connector. The I/O side lives in cmd_sch_block_apply_run.go.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/zhoushoujianwork/easyeda-agent/internal/blocks"
)

// ── the role-id → device bridge ─────────────────────────────────────────────

// bapDevice is the standard-parts.json entry a block's parts[role].part points
// at. This is the bridge internal/blocks/placement.go documents as missing: a
// block declares an internal role-id ("res.1k_0402"), and only this table turns
// it into something placeable ({libraryUuid, deviceUuid}).
type bapDevice struct {
	LibraryUUID string
	DeviceUUID  string
	MPN         string
	LCSC        string
	Value       string
	Basic       bool
}

// ── designator prefixes ─────────────────────────────────────────────────────

// bapPrefixes maps a part-key NAMESPACE (the segment before the dot in
// "res.1k_0402") to a reference-designator prefix.
//
// Provenance matters here, so it is recorded rather than asserted. Six prefixes
// are EVIDENCED — they are what this project's own validated boards actually
// used (examples/esp32-mini playbooks): C, R, J, U, SW, LED. The rest follow the
// standard IEEE-315 / IPC reference-designator classes. They are marked below so
// a future reader knows which ones are backed by a real board and which are
// convention awaiting first use.
//
// An UNKNOWN namespace is a hard error, never a guessed prefix: silently filing
// a new part class under the wrong designator would corrupt a board in a way
// that is tedious to unpick.
var bapPrefixes = map[string]string{
	// evidenced on real validated boards (examples/esp32-mini)
	"cap":  "C",
	"res":  "R",
	"conn": "J",
	"ic":   "U",
	"mcu":  "U",
	"sw":   "SW",
	"led":  "LED",
	// conventional (IEEE-315 classes) — not yet exercised by a placed board
	"ind":       "L",
	"fb":        "FB",
	"diode":     "D",
	"zener":     "D",
	"tvs":       "D",
	"esd":       "D",
	"bjt":       "Q",
	"pmos":      "Q",
	"xtal":      "X",
	"fuse":      "F",
	"ant":       "ANT",
	"opto":      "U",
	"ldo":       "U",
	"buck":      "U",
	"buckboost": "U",
	"charger":   "U",
	"pmu":       "U",
	"gnss":      "U",
	"storage":   "U",
}

// bapPrefixFor resolves a part key to its designator prefix.
func bapPrefixFor(partKey string) (string, error) {
	ns, _, ok := strings.Cut(partKey, ".")
	if !ok || ns == "" {
		return "", fmt.Errorf("part key %q has no namespace (expected e.g. res.1k_0402)", partKey)
	}
	p, ok := bapPrefixes[ns]
	if !ok {
		return "", fmt.Errorf("part namespace %q has no designator prefix mapping — add it to bapPrefixes "+
			"(guessing a prefix would misfile the part)", ns)
	}
	return p, nil
}

// ── flag kinds ──────────────────────────────────────────────────────────────

// bapFlagKind picks the netflag kind for a net name. Blocks do not declare flag
// kinds, so this is a small deterministic rule rather than a judgment call:
// ground names get a ground flag, supply-looking names get a power flag, and
// everything else gets a bidirectional net port (which merges purely by name and
// is therefore always structurally safe).
//
// It is a NAME heuristic and can be wrong for an unconventionally named rail —
// `--kind NET=kind` overrides it per net.
func bapFlagKind(net string) string {
	u := strings.ToUpper(strings.TrimSpace(net))
	switch u {
	case "GND", "VSS", "0V":
		return "gnd"
	case "AGND":
		return "agnd"
	case "PGND", "EARTH":
		return "pgnd"
	}
	if strings.HasSuffix(u, "_GND") || strings.HasSuffix(u, "GND") {
		return "gnd"
	}
	// Supply rails: a leading + (+3V3), a V-prefixed rail (VCC/VBAT/VBUS/VSYS/VIN),
	// or an nVn / nV form (3V3, 5V).
	if strings.HasPrefix(u, "+") ||
		strings.HasPrefix(u, "VCC") || strings.HasPrefix(u, "VDD") || strings.HasPrefix(u, "VBAT") ||
		strings.HasPrefix(u, "VBUS") || strings.HasPrefix(u, "VSYS") || strings.HasPrefix(u, "VIN") {
		return "power"
	}
	if bapLooksLikeRail(u) {
		return "power"
	}
	return "netport"
}

// bapLooksLikeRail reports whether a name reads as a voltage rail: 5V, 3V3,
// 12V, 1V8 — a leading digit, then V, then optional digits, and nothing else.
func bapLooksLikeRail(u string) bool {
	i := 0
	for i < len(u) && u[i] >= '0' && u[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(u) || u[i] != 'V' {
		return false
	}
	for j := i + 1; j < len(u); j++ {
		if u[j] < '0' || u[j] > '9' {
			return false
		}
	}
	return true
}

// ── plan shapes ─────────────────────────────────────────────────────────────

// bapPlacement is one part the plan will create.
type bapPlacement struct {
	Role        string  `json:"role"`
	PartKey     string  `json:"part"`
	Designator  string  `json:"designator"`
	LibraryUUID string  `json:"libraryUuid"`
	DeviceUUID  string  `json:"deviceUuid"`
	LCSC        string  `json:"lcsc,omitempty"`
	X           float64 `json:"x"`
	Y           float64 `json:"y"`
}

// bapNet is one internal net, resolved to placed-pin references.
type bapNet struct {
	Net     string   `json:"net"`
	Kind    string   `json:"kind"`
	Port    string   `json:"port,omitempty"`   // set when this net is a block boundary
	Bound   bool     `json:"bound,omitempty"`  // true when the host supplied --bind
	Members []string `json:"members"`          // DESIGNATOR:PIN, ready for autoconnect
	Roles   []string `json:"roles"`            // ROLE.pin, for traceability back to the block
}

// bapPlan is the full deterministic plan + the instance manifest skeleton.
type bapPlan struct {
	BlockID    string         `json:"blockId"`
	Revision   int            `json:"revision,omitempty"`
	Instance   string         `json:"instance"`
	Placements []bapPlacement `json:"placements"`
	Nets       []bapNet       `json:"nets"`
	// Unconsumed names the block's constraint maps that this command does NOT
	// execute, so a caller never reads a successful apply as "block fully honoured".
	Unconsumed []string `json:"unconsumedConstraints,omitempty"`
}

// ── the planner ─────────────────────────────────────────────────────────────

// bapInput is everything the pure planner needs.
type bapInput struct {
	Block    blocks.Block
	Topology [][]string          // internal_nets, already parsed out of Raw
	Devices  map[string]bapDevice // part key → device (from standard-parts.json)
	Existing map[string]bool     // designators already on the page (upper-cased)
	Instance string              // instance id, e.g. "led1"
	OriginX  float64
	OriginY  float64
	Spacing  float64
	PerRow   int
	Bind     map[string]string // PORT → host net
	KindOver map[string]string // NET → flag kind override
}

// planBlockApply turns a block + a parts library + the current page into a
// deterministic placement/wiring plan. It never touches the connector.
func planBlockApply(in bapInput) (bapPlan, error) {
	plan := bapPlan{
		BlockID:  in.Block.ID,
		Revision: in.Block.Revision,
		Instance: in.Instance,
	}
	// NOTE — the instance id is what keeps two instances of the same block apart,
	// because it names every PORT-less internal net. Deriving it from the BLOCK id
	// would hand both instances the same synthetic net names, and same-name nets
	// MERGE: instance 2's internal node would silently short to instance 1's. So an
	// unset instance is resolved AFTER designator allocation, from the first
	// allocated designator (LED1 → LED1_N2, LED2 → LED2_N2) — designators are
	// already collision-free against the page, so the net names inherit that.

	// Roles in a stable order so designators and coordinates are reproducible.
	roles := make([]string, 0, len(in.Block.Parts))
	for r := range in.Block.Parts {
		roles = append(roles, r)
	}
	sort.Strings(roles)

	// A block may bind a port the host never mentioned; validate --bind early so a
	// typo'd port name fails before anything is placed.
	for port := range in.Bind {
		if _, ok := in.Block.Ports[port]; !ok {
			return plan, fmt.Errorf("--bind %s=…: block %s has no port %q", port, in.Block.ID, port)
		}
	}

	// 1. Allocate designators + coordinates.
	used := map[string]bool{}
	for d := range in.Existing {
		used[strings.ToUpper(d)] = true
	}
	next := map[string]int{}
	roleDesig := map[string]string{}
	perRow := in.PerRow
	if perRow < 1 {
		perRow = 4
	}
	spacing := in.Spacing
	if spacing <= 0 {
		spacing = 100
	}
	for i, role := range roles {
		p := in.Block.Parts[role]
		if p.Qty != 1 {
			// qty>1 would need one designator per instance; the minimal slice does
			// not do it, and silently placing one part would under-build the block.
			return plan, fmt.Errorf("role %s: qty=%d not supported yet (block-apply v1 places qty=1 roles only)", role, p.Qty)
		}
		dev, ok := in.Devices[p.Part]
		if !ok {
			return plan, fmt.Errorf("role %s: part %q not found in standard-parts.json", role, p.Part)
		}
		if dev.DeviceUUID == "" {
			return plan, fmt.Errorf("role %s: part %q has no deviceUuid in standard-parts.json", role, p.Part)
		}
		prefix, err := bapPrefixFor(p.Part)
		if err != nil {
			return plan, fmt.Errorf("role %s: %w", role, err)
		}
		d := bapNextDesignator(prefix, used, next)
		roleDesig[role] = d
		plan.Placements = append(plan.Placements, bapPlacement{
			Role:        role,
			PartKey:     p.Part,
			Designator:  d,
			LibraryUUID: dev.LibraryUUID,
			DeviceUUID:  dev.DeviceUUID,
			LCSC:        dev.LCSC,
			X:           in.OriginX + float64(i%perRow)*spacing,
			Y:           in.OriginY + float64(i/perRow)*spacing,
		})
	}

	if strings.TrimSpace(in.Instance) == "" {
		if len(plan.Placements) == 0 {
			return plan, fmt.Errorf("block %s has no parts", in.Block.ID)
		}
		in.Instance = plan.Placements[0].Designator
	}
	plan.Instance = in.Instance

	// 2. Resolve internal_nets → named nets over placed pins.
	for i, net := range in.Topology {
		var (
			members []string
			roleRef []string
			port    string
		)
		for _, m := range net {
			if strings.HasPrefix(m, "PORT:") {
				name := strings.TrimPrefix(m, "PORT:")
				if port == "" {
					port = name
				}
				continue
			}
			role, pin, ok := strings.Cut(m, ".")
			if !ok {
				return plan, fmt.Errorf("internal_nets[%d]: bad pin ref %q", i, m)
			}
			d, ok := roleDesig[role]
			if !ok {
				return plan, fmt.Errorf("internal_nets[%d]: unknown role %q", i, role)
			}
			members = append(members, d+":"+pin)
			roleRef = append(roleRef, m)
		}
		if len(members) == 0 {
			// A net of nothing but PORT markers has no pin to attach a flag to.
			return plan, fmt.Errorf("internal_nets[%d]: no pin members", i)
		}

		name, bound := bapNetName(in, port, i)
		kind := bapFlagKind(name)
		if k, ok := in.KindOver[strings.ToUpper(name)]; ok {
			kind = k
		}
		plan.Nets = append(plan.Nets, bapNet{
			Net: name, Kind: kind, Port: port, Bound: bound,
			Members: members, Roles: roleRef,
		})
	}

	plan.Unconsumed = bapUnconsumed(in.Block)
	return plan, nil
}

// bapNetName picks a net's name: an explicit --bind wins, then the port's
// default_net, then the port name; a purely internal net gets an
// instance-scoped synthetic name that cannot collide with a host net.
func bapNetName(in bapInput, port string, idx int) (string, bool) {
	if port != "" {
		if host, ok := in.Bind[port]; ok && strings.TrimSpace(host) != "" {
			return host, true
		}
		p := in.Block.Ports[port]
		if p.DefaultNet != "" {
			return p.DefaultNet, false
		}
		return port, false
	}
	return fmt.Sprintf("%s_N%d", strings.ToUpper(in.Instance), idx+1), false
}

// bapNextDesignator returns the next free designator for a prefix, skipping any
// already on the page so a second instance never collides with the first.
func bapNextDesignator(prefix string, used map[string]bool, next map[string]int) string {
	for {
		next[prefix]++
		d := prefix + strconv.Itoa(next[prefix])
		if !used[strings.ToUpper(d)] {
			used[strings.ToUpper(d)] = true
			return d
		}
	}
}

// ── post-apply reconciliation (issue #135) ──────────────────────────────────
//
// Per-stub autoconnect success is NOT proof the block's topology landed:
// EasyEDA auto-merges touching wires, so a stub can silently fuse into a
// foreign net AFTER it was "successfully" created — and both check and
// bridge-check have historically missed the swallowed-flag shape. The netlist
// is the only authority on what actually connected, so block-apply now closes
// the loop: read it back and diff every planned net against reality.

// bapNetDiff is one planned net whose live membership does not match the plan.
type bapNetDiff struct {
	Net     string            `json:"net"`
	Missing []string          `json:"missing"`           // planned members absent from the live net (DESIGNATOR.PIN)
	FoundIn map[string]string `json:"foundIn,omitempty"` // missing member → the net it actually landed in
}

// bapPinKey resolves a planned member "DESIGNATOR:PINREF" (name or number) to
// the netlist's "DESIGNATOR.NUMBER" key via the designator→(name|number)→number
// map from the live component read.
func bapPinKey(member string, pinNumbers map[string]map[string]string) (string, bool) {
	desig, ref, ok := strings.Cut(member, ":")
	if !ok {
		return "", false
	}
	byRef := pinNumbers[strings.ToUpper(desig)]
	if byRef == nil {
		return "", false
	}
	num, ok := byRef[strings.ToUpper(ref)]
	if !ok {
		return "", false
	}
	return desig + "." + num, true
}

// reconcileBlockNets diffs the plan against the live netlist. liveNets maps net
// name → set of "DESIGNATOR.NUMBER"; pinNumbers maps DESIGNATOR → pin name/number
// → number. A planned net passes when every planned member is present in that
// live net — the live net MAY hold extra pins (bound host nets legitimately
// aggregate the rest of the board).
func reconcileBlockNets(plan bapPlan, liveNets map[string]map[string]bool, pinNumbers map[string]map[string]string) []bapNetDiff {
	var diffs []bapNetDiff
	for _, n := range plan.Nets {
		live := liveNets[n.Net]
		var missing []string
		foundIn := map[string]string{}
		for _, m := range n.Members {
			key, ok := bapPinKey(m, pinNumbers)
			if !ok {
				// Pin ref did not resolve — count as missing so it can't hide.
				missing = append(missing, m)
				continue
			}
			if live != nil && live[key] {
				continue
			}
			missing = append(missing, key)
			for otherNet, pins := range liveNets {
				if otherNet != n.Net && pins[key] {
					foundIn[key] = otherNet
					break
				}
			}
		}
		if len(missing) > 0 {
			d := bapNetDiff{Net: n.Net, Missing: missing}
			if len(foundIn) > 0 {
				d.FoundIn = foundIn
			}
			diffs = append(diffs, d)
		}
	}
	return diffs
}

// bapUnconsumed lists the constraint maps present in the block that this command
// does not execute. Honesty surface: `must` constraints that no consumer honours
// must not hide behind a green exit code.
func bapUnconsumed(b blocks.Block) []string {
	var raw map[string]any
	if err := json.Unmarshal(b.Raw, &raw); err != nil {
		return nil
	}
	var out []string
	for _, k := range []string{"pcb_layout", "placement", "signals", "silk", "keepout", "openings"} {
		if v, ok := raw[k]; ok && v != nil {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
