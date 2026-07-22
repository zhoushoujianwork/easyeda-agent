package app

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// ── sch check: reconstructed per-item design check ──────────────────────────
//
// The EDA schematic DRC API (eda.sch_Drc.check) returns only an aggregate
// {count,type} — the per-item detail the UI panel shows is not exposed by any
// public API. schematic.check reconstructs the actionable findings from the
// primitives directly (connector side). Rule 1: floating pins via geometric
// connectivity CROSS-CHECKED against the JSON-authoritative netlist — a net in
// the netlist drops #15-class geometric false positives, while geometry that
// touches a pin the netlist puts on no net surfaces as a geom-net-mismatch
// (suspected missed report). This file renders that report and (with --strict)
// gates on it. Output is by designator + pin number — feed it straight into
// `sch no-connect`.
//
// Every finding carries a kebab-case rule Type (same convention as pcb check),
// so findings can be counted/gated per rule. Connector-produced types:
//
//	floating-pin        WARN  pin with no wire and no NC marker
//	geom-net-mismatch   WARN  wire touches pin but netlist puts it on no net
//	net-marker-mismatch WARN  netflag/port/label name ≠ the wire's net name
//	multi-net-wire      WARN  one wire primitive carrying multiple net names
//	wire-crossing       WARN  two wires cross mid-segment
//	wire-over-pin       WARN  a wire body runs through a pin it doesn't end on
//	zero-length-wire    WARN  degenerate zero-length segment
//	dangling-wire       WARN  wire end anchored to nothing (incl. orphan stubs)
//
// checkSummary mirrors these with one per-type count field each.

type checkPinDetail struct {
	Number string  `json:"number"`
	Name   string  `json:"name,omitempty"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
}

// checkPoint is a finding's anchor coordinate. Named (not an anonymous struct) so
// the Go-side geometric rules can construct it, while the JSON shape stays exactly
// what the connector emits for `at`.
type checkPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// checkOverlapSide describes the OTHER primitive in a pairwise geometric finding
// (marker-overlap), so a caller has both primitive IDs + types to act on. See #148.
type checkOverlapSide struct {
	PrimitiveId   string      `json:"primitiveId"`
	ComponentType string      `json:"componentType,omitempty"`
	Designator    string      `json:"designator,omitempty"`
	Net           string      `json:"net,omitempty"`
	BBox          *layoutBBox `json:"bbox,omitempty"`
}

type checkFinding struct {
	Type              string           `json:"type"`
	Level             string           `json:"level"`
	Designator        string           `json:"designator,omitempty"`
	PrimitiveId       string           `json:"primitiveId,omitempty"`
	WirePrimitiveId   string           `json:"wirePrimitiveId,omitempty"`
	MarkerPrimitiveId string           `json:"markerPrimitiveId,omitempty"`
	WireNet           string           `json:"wireNet,omitempty"`
	MarkerNet         string           `json:"markerNet,omitempty"`
	Nets              []string         `json:"nets,omitempty"`
	Pins              []string         `json:"pins,omitempty"`
	PinDetails        []checkPinDetail `json:"pinDetails,omitempty"`
	Count             int              `json:"count,omitempty"`
	Message           string           `json:"message,omitempty"`
	At                *checkPoint      `json:"at,omitempty"`
	// Geometric marker rules (issues #146/#147/#148), computed Go-side from the
	// components.list bboxes/anchors — the electrical check never sees them.
	ComponentType    string            `json:"componentType,omitempty"`    // primary primitive's type
	PrimitiveIds     []string          `json:"primitiveIds,omitempty"`     // duplicate-net-marker: ALL coincident ids; marker-overlap: [a,b]
	SuggestKeepId    string            `json:"suggestKeepId,omitempty"`    // duplicate-net-marker: id to keep
	SuggestDeleteIds []string          `json:"suggestDeleteIds,omitempty"` // duplicate-net-marker: ids to prim-delete
	BBox             *layoutBBox       `json:"bbox,omitempty"`             // primary primitive bbox (titleblock/marker overlap)
	Keepout          *layoutBBox       `json:"keepout,omitempty"`         // titleblock-overlap: the keep-out rect
	Other            *checkOverlapSide `json:"other,omitempty"`           // marker-overlap: the B side
	OverlapX         float64           `json:"overlapX,omitempty"`        // overlap extent (marker/titleblock)
	OverlapY         float64           `json:"overlapY,omitempty"`
}

type checkSummary struct {
	FloatingPins           int `json:"floatingPins"`
	ComponentsWithFloating int `json:"componentsWithFloating"`
	GeomNetMismatches      int `json:"geomNetMismatches"`
	NetMarkerMismatches    int `json:"netMarkerMismatches"`
	MultiNetWires          int `json:"multiNetWires"`
	WireCrossings          int `json:"wireCrossings"`
	WireOverPins           int `json:"wireOverPins"`
	ZeroLengthWires        int `json:"zeroLengthWires"`
	DanglingWires          int `json:"danglingWires"`
	// Go-side geometric marker rules (issues #146/#147/#148).
	DuplicateNetMarkers int `json:"duplicateNetMarkers"`
	TitleblockOverlaps  int `json:"titleblockOverlaps"`
	MarkerOverlaps      int `json:"markerOverlaps"`
	Total               int `json:"total"`
}

type checkReport struct {
	Passed   bool           `json:"passed"`
	Summary  checkSummary   `json:"summary"`
	Findings []checkFinding `json:"findings"`
}

// runSchCheck runs the reconstructed design check, renders it, and (only with
// strict) returns a non-zero exit when there are findings. By default it is
// informational — floating IO pins are normal on an MCU board until NC-marked.
func runSchCheck(cfg *appConfig, window string, allPages, strict, asJSON bool, overlapEps float64, stdout, stderr io.Writer) error {
	payload := map[string]any{}
	if allPages {
		payload["allPages"] = true
	}
	res, err := requestAction(cfg, "schematic.check", window, payload)
	if err != nil {
		return err
	}

	rep, perr := parseCheckReport(res.Result)
	if perr != nil {
		if b, mErr := json.MarshalIndent(res.Result, "", "  "); mErr == nil {
			_, _ = stdout.Write(b)
			fmt.Fprintln(stdout)
		}
		return perr
	}

	// Go-side geometric marker rules (issues #146/#147/#148): the connector's
	// electrical schematic.check can't see coincident net markers, a netport that
	// landed on the A4 title block, or a marker body overlapping a part/marker —
	// those are pure bbox/anchor geometry. Compute them here from the SAME
	// components.list `sch layout-lint` uses, and fold the findings into the report
	// so `sch check` is the single delivery gate. Best-effort: a components.list
	// failure leaves the electrical findings intact.
	mergeMarkerGeomFindings(cfg, window, allPages, overlapEps, &rep, stderr)

	if asJSON {
		// Wrap the reconstructed report in the same {id,type,version,ok,result}
		// envelope the transparent commands (sch list/read/place) stream, so a
		// uniform-envelope parser reading result.findings works here too (#66).
		if err := encodeResultEnvelope(res, rep, stdout); err != nil {
			return err
		}
	} else {
		renderCheckReport(rep, stdout)
	}

	if strict && len(rep.Findings) > 0 {
		return fmt.Errorf("sch check: %d finding(s) (--strict)", len(rep.Findings))
	}
	return nil
}

func parseCheckReport(result map[string]any) (checkReport, error) {
	var rep checkReport
	if result == nil {
		return rep, fmt.Errorf("empty check result")
	}
	b, err := json.Marshal(result)
	if err != nil {
		return rep, err
	}
	if err := json.Unmarshal(b, &rep); err != nil {
		return rep, fmt.Errorf("unexpected check result shape: %w", err)
	}
	return rep, nil
}

func checkLevelTag(level string) string {
	switch strings.ToLower(level) {
	case "fatal":
		return "FATAL"
	case "error":
		return "ERROR"
	case "warn":
		return "WARN"
	case "info":
		return "INFO"
	default:
		return "?????"
	}
}

func renderCheckReport(rep checkReport, w io.Writer) {
	s := rep.Summary
	fmt.Fprintf(w, "sch check: %d finding(s) — %d floating pin(s)/%d comp, %d geom-net mismatch(es), %d net-marker mismatch(es), %d multi-net wire(s), %d wire-crossing(s), %d wire-over-pin(s), %d zero-length wire(s), %d dangling wire(s), %d duplicate-net-marker(s), %d titleblock-overlap(s), %d marker-overlap(s)\n",
		s.Total, s.FloatingPins, s.ComponentsWithFloating, s.GeomNetMismatches, s.NetMarkerMismatches, s.MultiNetWires, s.WireCrossings, s.WireOverPins, s.ZeroLengthWires, s.DanglingWires, s.DuplicateNetMarkers, s.TitleblockOverlaps, s.MarkerOverlaps)

	for _, f := range rep.Findings {
		tag := checkLevelTag(f.Level)
		msg := f.Message
		if msg == "" {
			msg = f.Type
		}
		// Rule-type column aligned with renderPcbCheckReport's %-17s style so sch
		// and pcb findings can be grepped/gated the same way by type name.
		line := fmt.Sprintf("  %-5s  %-17s  ", tag, f.Type)
		// Prefer the human designator; fall back to the primitiveId so a finding on
		// a component with an empty designator is still identifiable.
		switch {
		case f.Designator != "":
			line += f.Designator + "  "
		case f.PrimitiveId != "":
			line += f.PrimitiveId + "  "
		case f.WirePrimitiveId != "":
			line += f.WirePrimitiveId + "  "
		case f.MarkerPrimitiveId != "":
			line += f.MarkerPrimitiveId + "  "
		}
		line += msg
		if len(f.Pins) > 0 {
			line += "  [" + strings.Join(f.Pins, ",") + "]"
		}
		if f.WireNet != "" || f.MarkerNet != "" {
			line += fmt.Sprintf("  marker=%s wire=%s", f.MarkerNet, f.WireNet)
		}
		if len(f.Nets) > 0 {
			line += "  nets=[" + strings.Join(f.Nets, ",") + "]"
		}
		if f.At != nil {
			line += fmt.Sprintf("  @(%.2f,%.2f)", f.At.X, f.At.Y)
		}
		fmt.Fprintln(w, line)
		// Per-pin breakdown (floating-pin): pin number/name + coords so the report is
		// actionable without a second lookup.
		for _, pd := range f.PinDetails {
			label := pd.Number
			if pd.Name != "" {
				label += " (" + pd.Name + ")"
			}
			fmt.Fprintf(w, "          pin %s  @(%.2f,%.2f)\n", label, pd.X, pd.Y)
		}
	}

	if rep.Passed {
		fmt.Fprintln(w, "✓ no findings")
		return
	}
	if s.FloatingPins > 0 {
		// The floating-pin list is the exact input `sch no-connect` takes.
		fmt.Fprintln(w, "→ floating pins: wire them, or (where supported) mark intentional ones NC")
	}
	if s.GeomNetMismatches > 0 {
		// Geometry touches the pin but the authoritative netlist has no net for it —
		// recompile the netlist, or fix the wire so it electrically connects.
		fmt.Fprintln(w, "→ geom-net mismatch: wire touches pin but netlist has no net — recompile netlist, or fix the stub so it truly connects")
	}
	if s.WireCrossings > 0 || s.WireOverPins > 0 {
		fmt.Fprintln(w, "→ routing: reroute crossings in clear channels (L-bends); never run a wire through a pin")
	}
	if s.NetMarkerMismatches > 0 || s.MultiNetWires > 0 {
		fmt.Fprintln(w, "→ net names: fix mismatched flags/ports/labels before treating schematic DRC as clean")
	}
	if s.ZeroLengthWires > 0 || s.DanglingWires > 0 {
		fmt.Fprintln(w, "→ stray wires: delete the zero-length/dangling segments (sch prim-delete <wirePrimitiveId>)")
	}
	if s.DuplicateNetMarkers > 0 {
		fmt.Fprintln(w, "→ duplicate markers: a partial autoconnect stacked coincident flags/ports — sch prim-delete the suggested IDs (keep one)")
	}
	if s.TitleblockOverlaps > 0 {
		fmt.Fprintln(w, "→ title-block: a part/marker intrudes the A4 图签 keep-out — move it out or pick another connect direction")
	}
	if s.MarkerOverlaps > 0 {
		fmt.Fprintln(w, "→ marker overlap: net markers cover a part/each other — stagger the labels or re-run autoconnect with more offset")
	}
}
