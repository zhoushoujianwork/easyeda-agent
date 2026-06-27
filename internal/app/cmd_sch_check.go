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
// connectivity. This file renders that report and (with --strict) gates on it.
// Output is by designator + pin number — feed it straight into `sch no-connect`.

type checkPinDetail struct {
	Number string  `json:"number"`
	Name   string  `json:"name,omitempty"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
}

type checkFinding struct {
	Type        string           `json:"type"`
	Level       string           `json:"level"`
	Designator  string           `json:"designator,omitempty"`
	PrimitiveId string           `json:"primitiveId,omitempty"`
	Pins        []string         `json:"pins,omitempty"`
	PinDetails  []checkPinDetail `json:"pinDetails,omitempty"`
	Count       int              `json:"count,omitempty"`
	Message     string           `json:"message,omitempty"`
	At          *struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
	} `json:"at,omitempty"`
}

type checkSummary struct {
	FloatingPins           int `json:"floatingPins"`
	ComponentsWithFloating int `json:"componentsWithFloating"`
	WireCrossings          int `json:"wireCrossings"`
	WireOverPins           int `json:"wireOverPins"`
	Total                  int `json:"total"`
}

type checkReport struct {
	Passed   bool           `json:"passed"`
	Summary  checkSummary   `json:"summary"`
	Findings []checkFinding `json:"findings"`
}

// runSchCheck runs the reconstructed design check, renders it, and (only with
// strict) returns a non-zero exit when there are findings. By default it is
// informational — floating IO pins are normal on an MCU board until NC-marked.
func runSchCheck(cfg *appConfig, window string, allPages, strict, asJSON bool, stdout, stderr io.Writer) error {
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

	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
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
	fmt.Fprintf(w, "sch check: %d finding(s) — %d floating pin(s)/%d comp, %d wire-crossing(s), %d wire-over-pin(s)\n",
		s.Total, s.FloatingPins, s.ComponentsWithFloating, s.WireCrossings, s.WireOverPins)

	for _, f := range rep.Findings {
		tag := checkLevelTag(f.Level)
		msg := f.Message
		if msg == "" {
			msg = f.Type
		}
		line := fmt.Sprintf("  %-5s  %-14s  ", tag, f.Type)
		// Prefer the human designator; fall back to the primitiveId so a finding on
		// a component with an empty designator is still identifiable.
		switch {
		case f.Designator != "":
			line += f.Designator + "  "
		case f.PrimitiveId != "":
			line += f.PrimitiveId + "  "
		}
		line += msg
		if len(f.Pins) > 0 {
			line += "  [" + strings.Join(f.Pins, ",") + "]"
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
	if s.WireCrossings > 0 || s.WireOverPins > 0 {
		fmt.Fprintln(w, "→ routing: reroute crossings in clear channels (L-bends); never run a wire through a pin")
	}
}
