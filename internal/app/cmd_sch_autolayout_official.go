package app

// cmd_sch_autolayout_official.go — `sch autolayout --engine official`: the
// generic FALLBACK engine that wraps the platform's own schematic auto-layout.
//
// Our template/spec planner (--engine template, the default) produces clean
// functional-group layouts for KNOWN blocks. For un-templated scattered parts,
// the official eda.sch_Document.autoLayout() is the generic fallback (the
// "方案 B" of the placement methodology research): a connectivity-clustered
// radial placement. It is messier than a template but needs no spec.
//
// Status probed live 2026-07-19 on 3.2.148: the API graduated @alpha→@beta and
// now runs. It is a genuine LONG operation — measured ~138s to resolve — and
// returns an empty object (no coordinate payload), so this wrapper reads the
// positions back via components.list afterward and runs layout-lint as the
// self-check. It does NOT need polling: the connector's await resolves on its
// own; it just needs a long dispatch budget. It also needs the schematic to be
// the ACTIVE (foreground) document — it shows a progress bar in the editor.
//
// This goes through the debug.exec_js hatch (no connector handler / re-import),
// same as `sch zone-draw` and the doc/pcb internals.

import (
	"fmt"
	"io"
	"time"
)

// officialAutolayoutTimeout is the dispatch budget for the platform call. The
// operation measured ~138s; 300s leaves margin. The daemon shortens its
// connector wait to (budget - grace) and the platform's promise resolves well
// inside that (proven: a 200s budget returned at 138s).
const officialAutolayoutTimeout = 300 * time.Second

// runOfficialAutolayout runs eda.sch_Document.autoLayout() on the ACTIVE
// schematic page, then reads back positions and self-checks overlap. mutate
// gates the actual call: without --apply it only reports what would happen (the
// platform API has no dry-run, so a dry run just prints the plan-of-action).
func runOfficialAutolayout(cfg *appConfig, window string, apply bool, stdout, stderr io.Writer) error {
	// Guard: the platform lays out the ACTIVE document, so it must be a schematic
	// page and foreground. Verify via the live context before a 2-minute call.
	win, err := resolveTargetWindow(cfg, window)
	if err != nil {
		return err
	}
	cur, err := requestAction(cfg, "document.current", win, nil)
	if err != nil {
		return fmt.Errorf("read active document: %w", err)
	}
	if cur.Context == nil || cur.Context.DocumentType != "schematic" {
		dt := "unknown"
		if cur.Context != nil {
			dt = cur.Context.DocumentType
		}
		return fmt.Errorf("active document is %q, not a schematic — `easyeda doc switch <page>` to the target schematic page first (the platform lays out whatever page is foreground)", dt)
	}

	// Count parts up front so the report can state the scope.
	before := fetchSchObstacles(cfg, win) // real part bboxes on the active page
	fmt.Fprintf(stderr, "official autolayout: %d part(s) on the active page\n", len(before))

	if !apply {
		fmt.Fprintf(stdout, "dry-run: would run the platform eda.sch_Document.autoLayout() over %d part(s) on this page\n", len(before))
		fmt.Fprintln(stdout, "(the platform API has no preview — pass --apply to actually run it; it is a LONG op, ~2min, and rearranges the WHOLE active page)")
		return nil
	}

	fmt.Fprintln(stderr, "running platform autoLayout — this is a LONG operation (~2min); the editor shows a progress bar…")

	// The platform call resolves with an empty object; we only need to know it
	// completed without error. Long dispatch budget; no polling.
	code := "await eda.sch_Document.autoLayout(); return {done:true};"
	if _, err := requestActionTimed(cfg, "debug.exec_js", win,
		map[string]any{"code": code}, officialAutolayoutTimeout); err != nil {
		return fmt.Errorf("platform autoLayout failed (it needs the schematic foreground; a background page can hang): %w", err)
	}
	fmt.Fprintln(stderr, "platform autoLayout finished — reading back positions")

	// The API returns no coordinates, so read them back + self-check overlap
	// (the platform avoids overlaps, but this is the same mechanical gate the
	// template engine passes, so both engines are judged the same way).
	res, err := requestAction(cfg, "schematic.components.list", win, map[string]any{"includeBBox": true})
	if err != nil {
		return fmt.Errorf("read back positions: %w", err)
	}
	comps, perr := parseLayoutComps(res.Result)
	if perr != nil {
		return perr
	}
	kept, _ := filterLayoutComps(comps, false)
	rep := analyzeLayout(kept, 0, -1)
	if len(rep.Overlaps) == 0 {
		fmt.Fprintf(stdout, "✓ official autolayout applied — %d part(s) placed, 0 overlap (layout-lint clean)\n", len(kept))
	} else {
		fmt.Fprintf(stdout, "⚠ official autolayout applied — %d part(s), but %d overlap(s) remain; run `sch layout-lint` and adjust\n",
			len(kept), len(rep.Overlaps))
	}
	fmt.Fprintln(stdout, "note: the platform engine is connectivity-clustered (radial), messier than `--engine template` for a known block — prefer template + `sch align`/`distribute` refinement for a clean result")
	return nil
}
