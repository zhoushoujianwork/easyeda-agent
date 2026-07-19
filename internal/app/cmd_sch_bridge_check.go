package app

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// ── sch bridge-check: tree-granularity net-vs-copper consistency gate ────────
//
// `sch check`'s multi-net-wire rule looks at a SINGLE wire primitive at a time,
// but EasyEDA merges two collinear touching stubs of DIFFERENT nets into one
// wire tree spanning several wire primitives — no single wire then carries two
// net names, so the per-wire rule under-reports the short. bridge-check groups
// wires into trees by shared vertices (union-find, connector side) and
// aggregates the net names of the netflag/netport anchored on each tree:
//
//   • len(set(nets)) > 1                     → BRIDGE (real short, ERROR, gate)
//   • nets empty & tree touches a comp pin   → ORPHAN (dangling stub, WARN)
//
// Rule types (kebab-case, same convention as sch check / pcb check findings;
// derived from the connector's Kind enum in parseBridgeReport):
//
//	wire-bridge   ERROR  one wire tree carries ≥2 net names (real short) — gates
//	orphan-stub   WARN   wire tree touches pins but carries no net flag/port
//
// It is the third pillar of the S5 verification gate (network-semantics vs
// physical-copper consistency), alongside layout-lint and check/drc. Read-only:
// it reports each problem tree's wire ids / flag ids / touched pins so the fix
// can be driven by hand (sch prim-delete + sch connect) or a later --repair
// pass. BRIDGE findings drive a non-zero exit so it can gate a workflow.

type bridgeTree struct {
	Kind string `json:"kind"` // BRIDGE | ORPHAN (connector enum, kept for compat)
	// Type/Level are the kebab-case rule name + severity in the same convention
	// as sch check / pcb check findings, derived Go-side from Kind (the connector
	// only sends Kind): BRIDGE → wire-bridge/error, ORPHAN → orphan-stub/warn.
	Type    string   `json:"type,omitempty"`
	Level   string   `json:"level,omitempty"`
	WireIds []string `json:"wireIds"`
	FlagIds []string `json:"flagIds"`
	Pins    []string `json:"pins"` // designator:pin
	Nets    []string `json:"nets"`
}

// bridgeRuleType maps the connector's Kind enum to the kebab-case rule type +
// level used across sch check / pcb check findings.
func bridgeRuleType(kind string) (ruleType, level string) {
	switch strings.ToUpper(kind) {
	case "BRIDGE":
		return "wire-bridge", "error"
	case "ORPHAN":
		return "orphan-stub", "warn"
	case "ORPHAN_FLAG":
		// A netflag/netport attached to NO wire (issue #137): typically left behind
		// when a merged wire was deleted out from under it. Invisible until a new
		// wire passes through the point and silently inherits the stray net name.
		return "orphan-flag", "warn"
	default:
		return strings.ToLower(kind), "warn"
	}
}

type bridgeSummary struct {
	Trees          int `json:"trees"`
	Bridges        int `json:"bridges"`     // per-type count of wire-bridge trees
	Orphans        int `json:"orphans"`     // per-type count of orphan-stub trees
	OrphanFlags    int `json:"orphanFlags"` // per-type count of orphan-flag findings (flag on no wire)
	WireTreesTotal int `json:"wireTreesTotal"`
}

type bridgeReport struct {
	Passed  bool          `json:"passed"`
	Summary bridgeSummary `json:"summary"`
	Trees   []bridgeTree  `json:"trees"`
}

// runSchBridgeCheck runs the read-only tree-granularity bridge/orphan detection,
// renders it, and returns a non-zero exit when a BRIDGE (real short) exists so it
// can gate a workflow. ORPHAN findings are WARN and do not gate on their own.
func runSchBridgeCheck(cfg *appConfig, window string, allPages, asJSON bool, stdout, stderr io.Writer) error {
	payload := map[string]any{}
	if allPages {
		payload["allPages"] = true
	}
	res, err := requestAction(cfg, "schematic.bridgeCheck", window, payload)
	if err != nil {
		return err
	}

	rep, perr := parseBridgeReport(res.Result)
	if perr != nil {
		if b, mErr := json.MarshalIndent(res.Result, "", "  "); mErr == nil {
			_, _ = stdout.Write(b)
			fmt.Fprintln(stdout)
		}
		return perr
	}

	if asJSON {
		if err := encodeResultEnvelope(res, rep, stdout); err != nil {
			return err
		}
	} else {
		renderBridgeReport(rep, stdout)
	}

	if rep.Summary.Bridges > 0 {
		return fmt.Errorf("sch bridge-check: %d bridge(s) — real short(s) (net-vs-copper mismatch)", rep.Summary.Bridges)
	}
	return nil
}

func parseBridgeReport(result map[string]any) (bridgeReport, error) {
	var rep bridgeReport
	if result == nil {
		return rep, fmt.Errorf("empty bridge-check result")
	}
	b, err := json.Marshal(result)
	if err != nil {
		return rep, err
	}
	if err := json.Unmarshal(b, &rep); err != nil {
		return rep, fmt.Errorf("unexpected bridge-check result shape: %w", err)
	}
	// Stamp the kebab-case rule type + level (the connector only sends Kind), so
	// both --json consumers and the text render can gate/count by rule type the
	// same way sch check / pcb check findings are.
	for i := range rep.Trees {
		if rep.Trees[i].Type == "" || rep.Trees[i].Level == "" {
			ruleType, level := bridgeRuleType(rep.Trees[i].Kind)
			if rep.Trees[i].Type == "" {
				rep.Trees[i].Type = ruleType
			}
			if rep.Trees[i].Level == "" {
				rep.Trees[i].Level = level
			}
		}
	}
	return rep, nil
}

func renderBridgeReport(rep bridgeReport, w io.Writer) {
	s := rep.Summary
	fmt.Fprintf(w, "sch bridge-check: %d problem tree(s) — %d wire-bridge(s) (real short), %d orphan-stub(s) (dangling stub), %d orphan-flag(s) (flag on no wire) across %d wire tree(s)\n",
		s.Trees, s.Bridges, s.Orphans, s.OrphanFlags, s.WireTreesTotal)

	for _, t := range rep.Trees {
		ruleType, level := t.Type, t.Level
		if ruleType == "" || level == "" {
			// Trees built directly (not via parseBridgeReport) fall back to Kind.
			rt, lv := bridgeRuleType(t.Kind)
			if ruleType == "" {
				ruleType = rt
			}
			if level == "" {
				level = lv
			}
		}
		// Rule-type column aligned with sch check / pcb check's %-17s style.
		line := fmt.Sprintf("  %-5s  %-17s  %s", strings.ToUpper(level), ruleType, t.Kind)
		if len(t.Nets) > 0 {
			line += "  nets=[" + strings.Join(t.Nets, ",") + "]"
		}
		if len(t.Pins) > 0 {
			line += "  pins=[" + strings.Join(t.Pins, ",") + "]"
		}
		fmt.Fprintln(w, line)
		if len(t.WireIds) > 0 {
			fmt.Fprintf(w, "          wires: %s\n", strings.Join(t.WireIds, ", "))
		}
		if len(t.FlagIds) > 0 {
			fmt.Fprintf(w, "          flags: %s\n", strings.Join(t.FlagIds, ", "))
		}
	}

	if rep.Passed {
		fmt.Fprintln(w, "✓ no bridges or orphans")
		return
	}
	if s.Bridges > 0 {
		fmt.Fprintln(w, "→ bridge (共线合并短路): delete the whole tree (sch prim-delete <wireIds+flagIds>), then re-connect each pin to its own net (sch connect)")
	}
	if s.Orphans > 0 {
		fmt.Fprintln(w, "→ orphan (孤儿桩): the tree touches pins but carries no net flag/port — wire it to a real net or clear the stray stub (sch disconnect / prim-delete)")
	}
	if s.OrphanFlags > 0 {
		fmt.Fprintln(w, "→ orphan-flag (孤儿标志): a netflag/netport sits on NO wire — delete it (sch prim-delete <flagId>) before a new wire inherits its stray net name")
	}
}
