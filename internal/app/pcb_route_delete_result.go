package app

import (
	"fmt"
	"strings"
)

// routeDeleteResultError turns a partially effective pcb.route.delete into a
// failure (#312). The connector deletes every id it can still resolve and
// reports the rest in notFound / notDeleted / notDeletable / skippedLocked, but
// the envelope stays ok:true as soon as one id worked — so `track-delete` /
// `via-delete` exited 0 and batched callers silently left copper behind. The
// common cause is id churn: a later write or delete splits or merges
// collinear segments into primitives with NEW ids, so ids exported earlier
// stop resolving while the merged copper survives.
func routeDeleteResultError(action string, result map[string]any) error {
	if action != "pcb.route.delete" || result == nil {
		return nil
	}
	var parts []string
	for _, f := range []struct{ key, label string }{
		{"notFound", "not found"},
		{"notDeleted", "survived readback"},
		{"notDeletable", "footprint-embedded"},
		{"skippedLocked", "locked"},
	} {
		if n := len(asAnySlice(result[f.key])); n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s (%s)", n, f.label, f.key))
		}
	}
	if len(parts) == 0 {
		if ok, set := result["ok"].(bool); set && !ok {
			parts = append(parts, "connector reported ok:false")
		}
	}
	if len(parts) == 0 {
		return nil
	}
	hint := "ids churn after any split/merge write or delete, so re-export (`pcb track-list` / `pcb via-list` / `pcb dump --include-copper`), reconcile copper per net and layer, and delete the fresh ids"
	if locked := asAnySlice(result["skippedLocked"]); len(locked) > 0 {
		// #310: locked copper is skipped on purpose; re-exporting ids would not help.
		ids := strings.Join(anyStrings(locked), ",")
		if len(locked) > 8 {
			ids = "<the skippedLocked ids>"
		}
		hint = fmt.Sprintf("locked copper is never deleted — unlock it first with `easyeda pcb track-lock --ids %s --unlock` (or `--net <net> --unlock` / `--all --unlock`), then delete again; ", ids) + hint
	}
	return fmt.Errorf("%s: partial delete — removed %v, but %s; %s", action, result["count"], strings.Join(parts, ", "), hint)
}

func asAnySlice(v any) []any {
	switch s := v.(type) {
	case []any:
		return s
	case []string:
		out := make([]any, len(s))
		for i, x := range s {
			out[i] = x
		}
		return out
	}
	return nil
}

func anyStrings(v []any) []string {
	out := make([]string, 0, len(v))
	for _, x := range v {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
