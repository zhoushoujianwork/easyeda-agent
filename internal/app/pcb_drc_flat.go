package app

// pcb drc --json — flatten the SDK's nested DRC violation tree into one row per
// violation, with board coordinates in REAL mil.
//
// The raw `pcb.drc.check` result mirrors the UI panel: groups nested by error
// type then net/object-type, with the actual violation leaves at the bottom
// ({errorType, explanation, pos, objs, …}). Two traps this flattener owns so
// callers stop hand-rolling python for them (optimization-loop.md B/P0 + A5):
//
//   - leaf pos {x,y} is in mil/10 — every coordinate is multiplied by 10 here
//     so the output aligns with `pcb list` / `pcb layout-lint` mil coordinates
//     (cross-checked: a 4mil clearance rule stores clearance=0.40157).
//   - the net is scattered: `net` on Connection leaves, errData.net, or only
//     embedded in the object suffix "(+3V3): C2_2" — normalized to one field.
//
// Pure functions, unit-tested against real leaves captured from the audit log.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"sort"
	"strings"
)

// drcTimeoutHint decorates a `pcb drc` dispatch error: when the round-trip
// timed out, it appends the one fix that actually works — bring EasyEDA to the
// foreground — because a background/occluded window never finishes the DRC
// canvas recompute (optimization-loop.md A4) and blind retries only pile more
// recompute tasks onto the webview. Non-timeout errors pass through untouched.
func drcTimeoutHint(err error, stderr io.Writer) error {
	if err == nil {
		return nil
	}
	var netErr net.Error
	timedOut := errors.Is(err, context.DeadlineExceeded) ||
		(errors.As(err, &netErr) && netErr.Timeout()) ||
		strings.Contains(err.Error(), "context deadline exceeded") ||
		strings.Contains(err.Error(), "Client.Timeout")
	if timedOut {
		fmt.Fprintln(stderr, "hint: DRC did not return in time — EasyEDA is likely in the BACKGROUND; its canvas recompute never finishes there. Bring the EasyEDA window to the foreground and run `pcb drc` ONCE (do not retry in a loop: each retry piles another recompute onto the webview). For a heavy board, raise --timeout.")
	}
	return err
}

// drcFlatViolation is one flattened DRC violation row.
type drcFlatViolation struct {
	Rule     string   `json:"rule"`               // error class, e.g. "Clearance Error"
	ObjType  string   `json:"objType,omitempty"`  // e.g. "Track to Track", "SMD Pad"
	RuleName string   `json:"ruleName,omitempty"` // rule that fired, e.g. "copperThickness1oz"
	Net      string   `json:"net,omitempty"`
	X        *float64 `json:"x,omitempty"` // mil (leaf pos ×10)
	Y        *float64 `json:"y,omitempty"` // mil
	Layer    string   `json:"layer,omitempty"`
	Objs     []string `json:"objs,omitempty"` // primitiveIds involved
	Message  string   `json:"message,omitempty"`
	Index    string   `json:"globalIndex,omitempty"`
}

// drcFlatReport is the `pcb drc --json` output shape.
type drcFlatReport struct {
	Passed     bool               `json:"passed"`
	Total      int                `json:"total"`
	Counts     map[string]int     `json:"counts"`
	Violations []drcFlatViolation `json:"violations"`
	Binding    map[string]any     `json:"binding,omitempty"` // Netlist-Error board-binding diagnostic, passed through
}

// suffixNetRe extracts the net name from an object suffix like "(+3V3): C2_2".
var suffixNetRe = regexp.MustCompile(`^\((.+?)\)`)

// flattenDrcResult converts a raw pcb.drc.check result map into the flat report.
func flattenDrcResult(result map[string]any) drcFlatReport {
	report := drcFlatReport{
		Passed: result["passed"] == true,
		Counts: map[string]int{},
		// Empty slice (not nil) so the JSON is always an array.
		Violations: []drcFlatViolation{},
	}
	if b, ok := result["binding"].(map[string]any); ok {
		report.Binding = b
	}
	collectDrcLeaves(result["violations"], &report.Violations)

	// Stable order: by error class, then net, then index — diff-friendly output.
	sort.SliceStable(report.Violations, func(i, j int) bool {
		a, b := report.Violations[i], report.Violations[j]
		if a.Rule != b.Rule {
			return a.Rule < b.Rule
		}
		if a.Net != b.Net {
			return a.Net < b.Net
		}
		return a.Index < b.Index
	})
	for _, v := range report.Violations {
		report.Counts[v.Rule]++
	}
	report.Total = len(report.Violations)
	return report
}

// collectDrcLeaves walks the nested group tree ({count,list,name,…} at every
// level) and appends one row per violation leaf. A leaf is any node carrying
// errorType + explanation; everything else recurses through maps and arrays,
// which keeps the walk robust to the panel's varying nesting depth.
func collectDrcLeaves(node any, out *[]drcFlatViolation) {
	switch n := node.(type) {
	case []any:
		for _, item := range n {
			collectDrcLeaves(item, out)
		}
	case map[string]any:
		_, hasErrType := n["errorType"]
		_, hasExplanation := n["explanation"]
		if hasErrType && hasExplanation {
			*out = append(*out, flattenDrcLeaf(n))
			return
		}
		collectDrcLeaves(n["list"], out)
	}
}

// flattenDrcLeaf projects one violation leaf into a flat row.
func flattenDrcLeaf(leaf map[string]any) drcFlatViolation {
	v := drcFlatViolation{
		Rule:     asString(leaf["errorType"]),
		ObjType:  asString(leaf["errorObjType"]),
		RuleName: asString(leaf["ruleName"]),
		Layer:    asString(leaf["layer"]),
		Index:    asString(leaf["globalIndex"]),
	}

	explanation, _ := leaf["explanation"].(map[string]any)
	var errData map[string]any
	if explanation != nil {
		errData, _ = explanation["errData"].(map[string]any)
	}

	// Net: leaf.net → errData.net → "(NET)" prefix of an object suffix.
	v.Net = asString(leaf["net"])
	if v.Net == "" && errData != nil {
		v.Net = asString(errData["net"])
	}
	obj1Suffix := objSuffix(leaf, "obj1")
	obj2Suffix := objSuffix(leaf, "obj2")
	if v.Net == "" {
		for _, suffix := range []string{obj1Suffix, obj2Suffix} {
			if m := suffixNetRe.FindStringSubmatch(suffix); m != nil {
				v.Net = m[1]
				break
			}
		}
	}

	// Coordinates: leaf pos {x,y} in mil/10 → mil (A5).
	if pos, ok := leaf["pos"].(map[string]any); ok {
		if x, okX := asFloatOK(pos["x"]); okX {
			if y, okY := asFloatOK(pos["y"]); okY {
				xm, ym := round2(x*10), round2(y*10)
				v.X, v.Y = &xm, &ym
			}
		}
	}

	// Involved primitive ids.
	if objs, ok := leaf["objs"].([]any); ok {
		for _, o := range objs {
			if id := asString(o); id != "" {
				v.Objs = append(v.Objs, id)
			}
		}
	}

	// Message: the explanation template with {obj1}/{obj2} bound to the real
	// object suffixes and the remaining {placeholders} filled from param.
	if explanation != nil {
		msg := asString(explanation["str"])
		fill := map[string]string{"obj1": obj1Suffix, "obj2": obj2Suffix}
		if param, ok := explanation["param"].(map[string]any); ok {
			for k, val := range param {
				if _, bound := fill[k]; bound && fill[k] != "" {
					continue // real object suffix beats the generic param label
				}
				fill[k] = asString(val)
			}
		}
		for k, val := range fill {
			if val != "" {
				msg = strings.ReplaceAll(msg, "{"+k+"}", val)
			}
		}
		v.Message = msg
	}
	return v
}

// objSuffix reads leaf.<key>.suffix ("(+3V3): C2_2"), tolerating absence.
func objSuffix(leaf map[string]any, key string) string {
	if obj, ok := leaf[key].(map[string]any); ok {
		return asString(obj["suffix"])
	}
	return ""
}

