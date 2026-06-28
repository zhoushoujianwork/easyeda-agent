package app

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// ── sch drc: official SDK electrical rule check ────────────────────────────
//
// EasyEDA's schematic DRC SDK may return only boolean/aggregate data even when
// includeVerboseError=true. The connector normalizes whatever shape the runtime
// provides into drcReport; itemized UI-panel warnings are reconstructed by
// `sch check`, not by this command.

// drcViolation mirrors one normalized violation from the connector.
type drcViolation struct {
	Level        string   `json:"level"`
	Type         string   `json:"type,omitempty"`
	Rule         string   `json:"rule,omitempty"`
	Message      string   `json:"message,omitempty"`
	PrimitiveIDs []string        `json:"primitiveIds,omitempty"`
	Designators  []string        `json:"designators,omitempty"`
	X            *float64        `json:"x,omitempty"`
	Y            *float64        `json:"y,omitempty"`
	Count        *int            `json:"count,omitempty"`
	Raw          json.RawMessage `json:"raw,omitempty"`
}

// drcSummary mirrors the connector's severity tally.
type drcSummary struct {
	Fatal   int `json:"fatal"`
	Error   int `json:"error"`
	Warn    int `json:"warn"`
	Info    int `json:"info"`
	Unknown int `json:"unknown"`
	Total   int `json:"total"`
}

// drcReport is the normalized DRC result the connector returns.
type drcReport struct {
	Passed     bool           `json:"passed"`
	Fatal      int            `json:"fatal"`
	Summary    drcSummary     `json:"summary"`
	Violations []drcViolation `json:"violations"`
}

// runSchDrc runs schematic DRC, renders the normalized violations, and returns a
// non-nil error (non-zero exit) only when the fatal count is > 0.
func runSchDrc(cfg *appConfig, window string, strict, verbose, asJSON bool, stdout, stderr io.Writer) error {
	payload := map[string]any{
		// Always request the verbose/array overload — it's what yields per-item
		// detail. The connector reads this field (no longer hardcoded). issue #7
		"includeVerboseError": true,
	}
	if strict {
		payload["strict"] = true
	}

	res, err := requestAction(cfg, "schematic.drc.check", window, payload)
	if err != nil {
		return err
	}

	rep, perr := parseDrcReport(res.Result)
	if perr != nil {
		// Fall back to streaming the raw result so the user still sees something
		// useful if the shape is unexpected.
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
		renderDrcReport(rep, verbose, stdout)
	}

	if rep.Fatal > 0 {
		return fmt.Errorf("sch drc: %d fatal violation(s)", rep.Fatal)
	}
	return nil
}

// parseDrcReport re-marshals the generic result map into the typed drcReport.
func parseDrcReport(result map[string]any) (drcReport, error) {
	var rep drcReport
	if result == nil {
		return rep, fmt.Errorf("empty DRC result")
	}
	b, err := json.Marshal(result)
	if err != nil {
		return rep, err
	}
	if err := json.Unmarshal(b, &rep); err != nil {
		return rep, fmt.Errorf("unexpected DRC result shape: %w", err)
	}
	return rep, nil
}

// drcLevelTag maps a severity to the left-column tag used in the human view.
func drcLevelTag(level string) string {
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

// renderDrcReport prints a compact, per-violation human summary.
func renderDrcReport(rep drcReport, verbose bool, w io.Writer) {
	s := rep.Summary
	fmt.Fprintf(w, "sch drc: %d violation(s) — %d fatal, %d error, %d warn, %d info\n",
		s.Total, s.Fatal, s.Error, s.Warn, s.Info)

	for _, v := range rep.Violations {
		tag := drcLevelTag(v.Level)
		rule := v.Rule
		if rule == "" {
			rule = v.Type
		}
		if rule == "" {
			rule = "-"
		}
		msg := v.Message
		if msg == "" && v.Count != nil {
			// Aggregate-only node: the build gave a count but no per-item detail.
			msg = fmt.Sprintf("%d issue(s) — EDA returned no per-item detail", *v.Count)
		}
		line := fmt.Sprintf("  %-5s  %s  %s", tag, rule, msg)
		if v.X != nil && v.Y != nil {
			line += fmt.Sprintf("  @(%.2f,%.2f)", *v.X, *v.Y)
		}
		if refs := append(append([]string{}, v.Designators...), v.PrimitiveIDs...); len(refs) > 0 {
			line += "  [" + strings.Join(refs, ",") + "]"
		}
		fmt.Fprintln(w, line)
	}

	if verbose {
		for _, v := range rep.Violations {
			if len(v.Raw) > 0 {
				fmt.Fprintf(w, "    raw: %s\n", string(v.Raw))
			}
		}
	}

	if rep.Passed {
		fmt.Fprintln(w, "✓ DRC clean — no violations")
	} else if rep.Fatal == 0 {
		fmt.Fprintf(w, "✓ 0 fatal, %d warning(s) — gate passes (warnings should still be reviewed)\n", s.Warn+s.Info+s.Unknown)
	} else {
		fmt.Fprintf(w, "✗ %d fatal violation(s) — must be fixed before the S5 gate passes\n", rep.Fatal)
	}
}
