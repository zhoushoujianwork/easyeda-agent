package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/zhoushoujianwork/easyeda-agent/internal/version"
)

type schLayoutReport struct {
	SchemaVersion             int                  `json:"schemaVersion"`
	Operation                 string               `json:"operation"`
	AlgorithmVersion          string               `json:"algorithmVersion"`
	Scope                     string               `json:"scope"`
	Status                    string               `json:"status"`
	Phase                     string               `json:"phase"`
	Zones                     bool                 `json:"zones"`
	LayoutMode                string               `json:"layoutMode,omitempty"`
	SourceSHA256              string               `json:"sourceSha256,omitempty"`
	Error                     string               `json:"error,omitempty"`
	FailureClass              string               `json:"failureClass,omitempty"`
	RoutingDurationMS         int64                `json:"routingDurationMs,omitempty"`
	Diagnostics               []any                `json:"diagnostics"`
	GlobalInfeasibilityProven bool                 `json:"globalInfeasibilityProven"`
	ZoneReview                *SchematicZoneReview `json:"zoneReview,omitempty"`
}

func schLayoutReportPaths(from, out, report string) error {
	paths := []struct{ name, path string }{{"from", from}, {"out", out}, {"report", report}}
	for i, a := range paths {
		if a.path == "" {
			continue
		}
		x, err := schLayoutCanonicalPath(a.path, 0)
		if err != nil {
			return err
		}
		fi, _ := os.Stat(a.path)
		for _, b := range paths[:i] {
			if b.path == "" {
				continue
			}
			y, err := schLayoutCanonicalPath(b.path, 0)
			if err != nil {
				return err
			}
			fj, _ := os.Stat(b.path)
			if x == y || (fi != nil && fj != nil && os.SameFile(fi, fj)) {
				return fmt.Errorf("--%s must not overwrite --%s; input, geometry and diagnostic report must be distinct", a.name, b.name)
			}
		}
	}
	return nil
}

// Also resolve not-yet-created output files through existing directory aliases
// and dangling file symlinks, before either writer can overwrite the other.
func schLayoutCanonicalPath(path string, depth int) (string, error) {
	if depth >= 64 {
		return "", fmt.Errorf("too many symbolic links in layout path %q", path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if fi, err := os.Lstat(abs); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(abs)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(abs), target)
		}
		return schLayoutCanonicalPath(target, depth+1)
	}
	if parent, err := filepath.EvalSymlinks(filepath.Dir(abs)); err == nil {
		return filepath.Join(parent, filepath.Base(abs)), nil
	}
	return abs, nil // A missing parent makes the later file write fail explicitly.
}

func writeSchLayoutReport(path string, source []byte, phase string, zones bool, result any, cause error, mode ...string) error {
	r := schLayoutReport{SchemaVersion: 1, Operation: "schematic.layout-plan", AlgorithmVersion: version.Version,
		Scope: "offline-layout-only", Status: "planned", Phase: "complete", Zones: zones, Diagnostics: []any{}}
	if len(mode) > 0 {
		r.LayoutMode = mode[0]
	}
	if source != nil {
		r.SourceSHA256 = sha256Hex(source)
		if zones {
			r.ZoneReview, _ = reviewSchematicZonesJSON(source)
		}
	}
	if cause != nil {
		r.Status, r.Phase, r.Error = "failed", phase, cause.Error()
		r.FailureClass = schLayoutFailureClass(cause, phase)
		r.RoutingDurationMS = schLayoutFailureRoutingDuration(cause).Milliseconds()
		r.Diagnostics = schLayoutFailureDiagnostics(cause)
	} else {
		add := func(zone string, layout *SchematicLayoutResult) {
			if layout == nil {
				return
			}
			r.Diagnostics = append(r.Diagnostics, map[string]any{"type": "layout-search", "zoneId": zone,
				"candidatesUsed": layout.CandidatesUsed, "search": layout.Search, "feasibility": layout.FeasibilityReport, "optimization": layout.OptimizationReport, "routing": layout.Routing})
			if layout.Routing != nil {
				r.RoutingDurationMS += layout.Routing.duration.Milliseconds()
			}
		}
		switch out := result.(type) {
		case *SchematicLayoutResult:
			add("", out)
		case *SchematicZonesResult:
			for _, zone := range out.Zones {
				add(zone.ID, zone.Layout)
			}
		}
	}
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0644)
}

func schLayoutFailureClass(cause error, phase string) string {
	if phase == "read" || phase == "decode" {
		return "data-missing"
	}
	class, priority := "final-validation-failed", 1
	walkSchLayoutErrors(cause, func(err error) {
		// Detect the exact sentinel while using the same bounded traversal as
		// every other diagnostic. Calling errors.Is on an adversarial cyclic
		// wrapper can recurse forever before the traversal guard gets a chance.
		if typ := reflect.TypeOf(err); typ != nil && typ.Comparable() {
			switch err {
			case errLibLayoutBudget, errSchematicCandidateReserve:
				if priority < 4 {
					class, priority = "candidate-budget-exhausted", 4
				}
			case errSchematicExpandedBudget:
				class, priority = "expanded-node-budget-exhausted", 5
			}
		}
		failure, ok := err.(*schematicRoutingFailure)
		if !ok {
			return
		}
		candidatePriority := map[string]int{
			"final-validation-failed":        1,
			"no-path-within-bounds":          2,
			"terminal-frontier-rejected":     2,
			"data-missing":                   3,
			"route-attempt-node-limit":       4,
			"relocation-budget-reserved":     4,
			"expanded-node-budget-exhausted": 5,
		}[failure.Kind]
		if candidatePriority > priority {
			class, priority = failure.Kind, candidatePriority
		}
	})
	return class
}

func schLayoutFailureRoutingDuration(cause error) time.Duration {
	var longest time.Duration
	walkSchLayoutErrors(cause, func(err error) {
		if failure, ok := err.(*schematicRoutingFailure); ok && failure.Routing != nil && failure.Routing.duration > longest {
			// Routing snapshots are cumulative. Max avoids counting the same
			// context again through nested diagnostic wrappers.
			longest = failure.Routing.duration
		}
	})
	return longest
}

func schLayoutFailureDiagnostics(cause error) []any {
	diagnostics := []any{}
	// Repair termination may wrap both the stop reason and the last conflict.
	// Traverse both single- and multi-error wrappers without parsing messages.
	if !walkSchLayoutErrors(cause, func(e error) {
		if details, ok := e.(interface{ FailureDetails() any }); ok {
			diagnostics = append(diagnostics, details.FailureDetails())
		}
		if feasibility, ok := e.(*schematicFeasibilityError); ok {
			diagnostics = append(diagnostics, map[string]any{"type": "allowed-pose-feasibility", "report": feasibility.Report})
		}
	}) {
		diagnostics = append(diagnostics, map[string]any{"type": "diagnostic-traversal-limit", "complete": false})
	}
	return diagnostics
}

// Bound traversal even for malformed cyclic wrappers; do not require errors to
// be comparable (an error may contain a slice). False explicitly reports loss.
func walkSchLayoutErrors(root error, visit func(error)) bool {
	remaining := 1024
	var walk func(error, int) bool
	walk = func(err error, depth int) bool {
		if err == nil {
			return true
		}
		if remaining == 0 || depth == 64 {
			return false
		}
		remaining--
		visit(err)
		switch e := err.(type) {
		case interface{ Unwrap() []error }:
			complete := true
			for _, child := range e.Unwrap() {
				if !walk(child, depth+1) {
					complete = false
				}
			}
			return complete
		case interface{ Unwrap() error }:
			return walk(e.Unwrap(), depth+1)
		}
		return true
	}
	return walk(root, 0)
}
