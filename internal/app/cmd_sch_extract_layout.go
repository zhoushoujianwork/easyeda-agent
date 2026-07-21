package app

// cmd_sch_extract_layout.go — `sch extract-layout`: the INVERSE of block-apply's
// template step (issue #140). Given a block id and a real placed instance of that
// block on the active page, it reads each role's measured anchor + rotation and
// emits the block's `schematic_layout` template (role → {dx,dy,rotation} relative
// to a deterministic anchor). This turns "place a peripheral well ONCE → freeze the
// template" into a data pipeline instead of eyeballing the board and hand-typing
// every offset (18 of 20 blocks still lack a template because that was manual).
//
// SCOPE (v1): it PRINTS the template JSON for review; it does NOT write it back
// into the go:embed block data. Writeback into internal/blocks/data/<id>.json must
// preserve the file's key order (Go map marshalling reorders every key → an
// unreadable diff), so the safe path is: run this, review the printed block,
// paste `schematic_layout` into the block file, and let `go test` validate it
// (full-role coverage + on-grid + legal rotation). A `--write` convenience can be
// added once the ordering-preserving writer is in place.
//
// The two design decisions the evaluation flagged are resolved deterministically:
//   1. role→designator: explicit `--role ROLE=DESIG` wins; `--from D1,D2,…`
//      auto-matches a designator to the UNIQUE block role sharing its prefix, and
//      ERRORS on ambiguity/miss rather than guessing.
//   2. anchor: the first role (sorted) whose designator prefix is "U" (chip/MCU),
//      else the first role in sorted order — so the same instance always yields
//      the same origin (no drift between exports).

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zhoushoujianwork/easyeda-agent/internal/blocks"
)

// extractRoleGeom is one placed part's measured anchor + rotation (schematic units).
type extractRoleGeom struct {
	X, Y     float64
	Rotation float64
}

// designatorAlphaPrefix returns a designator's leading alphabetic prefix, upper-cased
// (LED2 → "LED", U1 → "U", C10 → "C"). Empty for a designator with no alpha prefix.
func designatorAlphaPrefix(d string) string {
	i := 0
	for i < len(d) {
		c := d[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
			i++
			continue
		}
		break
	}
	return strings.ToUpper(d[:i])
}

// normalizeRotation snaps a measured rotation to the nearest legal {0,90,180,270}.
func normalizeRotation(r float64) float64 {
	n := math.Mod(math.Round(r/90)*90, 360)
	if n < 0 {
		n += 360
	}
	return n
}

// extractRolePrefixes maps every block role to its designator prefix (via the same
// bapPrefixFor block-apply uses), and rejects qty≠1 roles (block-apply v1, and thus
// this inverse, only handle single-instance roles).
func extractRolePrefixes(b blocks.Block) (map[string]string, error) {
	out := make(map[string]string, len(b.Parts))
	for role, p := range b.Parts {
		if p.Qty != 0 && p.Qty != 1 {
			return nil, fmt.Errorf("role %s: qty=%d not supported yet (extract-layout, like block-apply v1, handles qty=1 roles only)", role, p.Qty)
		}
		pfx, err := bapPrefixFor(p.Part)
		if err != nil {
			return nil, fmt.Errorf("role %s: %w", role, err)
		}
		out[role] = pfx
	}
	return out, nil
}

// resolveExtractRoleMap resolves role→designator from explicit `--role` pairs plus
// `--from` prefix auto-matching. Explicit pairs win; a `--from` designator matches
// the unique unmapped role sharing its prefix, erroring on ambiguity or a miss. All
// roles must end up mapped.
func resolveExtractRoleMap(b blocks.Block, prefixes, explicit map[string]string, from []string) (map[string]string, error) {
	roleDesig := map[string]string{}
	for role, desig := range explicit {
		if _, ok := b.Parts[role]; !ok {
			return nil, fmt.Errorf("--role %s=…: block %s has no role %q", role, b.ID, role)
		}
		d := strings.ToUpper(strings.TrimSpace(desig))
		if d == "" {
			return nil, fmt.Errorf("--role %s=: designator is empty", role)
		}
		roleDesig[role] = d
	}

	if len(from) > 0 {
		// prefix → unmapped roles carrying it.
		byPrefix := map[string][]string{}
		for role, pfx := range prefixes {
			if _, done := roleDesig[role]; done {
				continue
			}
			byPrefix[pfx] = append(byPrefix[pfx], role)
		}
		for _, raw := range from {
			d := strings.ToUpper(strings.TrimSpace(raw))
			if d == "" {
				continue
			}
			already := false
			for _, dd := range roleDesig {
				if dd == d {
					already = true
					break
				}
			}
			if already {
				continue
			}
			pfx := designatorAlphaPrefix(d)
			roles := byPrefix[pfx]
			switch {
			case len(roles) == 0:
				return nil, fmt.Errorf("--from %s: no unmapped block role has designator prefix %q — map it explicitly with --role ROLE=%s", d, pfx, d)
			case len(roles) > 1:
				sort.Strings(roles)
				return nil, fmt.Errorf("--from %s: prefix %q is ambiguous across roles %v — map explicitly with --role ROLE=%s", d, pfx, roles, d)
			}
			roleDesig[roles[0]] = d
			delete(byPrefix, pfx) // a second same-prefix designator now errors, not mis-assigns
		}
	}

	var missing []string
	for role := range b.Parts {
		if _, ok := roleDesig[role]; !ok {
			missing = append(missing, role)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("these roles were not mapped to a designator: %v — add `--role ROLE=DESIG` for each (or list them in --from)", missing)
	}
	return roleDesig, nil
}

// extractAnchorRole picks the deterministic block anchor role: the first role in
// sorted order whose designator prefix is "U" (chip/MCU), else the first role.
func extractAnchorRole(prefixes map[string]string) string {
	roles := make([]string, 0, len(prefixes))
	for r := range prefixes {
		roles = append(roles, r)
	}
	sort.Strings(roles)
	for _, r := range roles {
		if prefixes[r] == "U" {
			return r
		}
	}
	if len(roles) > 0 {
		return roles[0]
	}
	return ""
}

// buildExtractedLayout computes the schematic_layout from measured geometry: each
// role's offset is its anchor minus the anchor role's anchor, snapped to the 5-grid;
// the anchor role is (0,0). Rotation is normalized to {0,90,180,270}.
func buildExtractedLayout(roleDesig map[string]string, geom map[string]extractRoleGeom, anchorRole, note string) (blocks.SchematicLayout, error) {
	anchor, ok := geom[anchorRole]
	if !ok {
		return blocks.SchematicLayout{}, fmt.Errorf("anchor role %q (designator %s) has no measured geometry — is it placed on the active page?", anchorRole, roleDesig[anchorRole])
	}
	out := blocks.SchematicLayout{Note: note, Roles: map[string]blocks.SchematicLayoutHint{}}
	roles := make([]string, 0, len(roleDesig))
	for r := range roleDesig {
		roles = append(roles, r)
	}
	sort.Strings(roles)
	for _, role := range roles {
		g, ok := geom[role]
		if !ok {
			return blocks.SchematicLayout{}, fmt.Errorf("role %q (designator %s) not found on the active page — is the whole instance placed?", role, roleDesig[role])
		}
		out.Roles[role] = blocks.SchematicLayoutHint{
			DX:       snapAnchor(g.X - anchor.X),
			DY:       snapAnchor(g.Y - anchor.Y),
			Rotation: normalizeRotation(g.Rotation),
		}
	}
	return out, nil
}

// fetchExtractGeom reads the active page and returns designator(upper) → measured
// anchor + rotation for every placed part.
func fetchExtractGeom(cfg *appConfig, window string) (map[string]extractRoleGeom, error) {
	res, err := requestAction(cfg, "schematic.components.list", window, map[string]any{"includeBBox": true})
	if err != nil {
		return nil, err
	}
	parts, _ := parseAutolayoutParts(res.Result)
	out := make(map[string]extractRoleGeom, len(parts))
	for _, p := range parts {
		if p.Designator == "" {
			continue
		}
		out[strings.ToUpper(p.Designator)] = extractRoleGeom{X: p.AnchorX, Y: p.AnchorY, Rotation: p.Rotation}
	}
	return out, nil
}

// geomByRole re-keys the designator→geom map by role via the role→designator map.
func geomByRole(roleDesig map[string]string, byDesig map[string]extractRoleGeom) map[string]extractRoleGeom {
	out := make(map[string]extractRoleGeom, len(roleDesig))
	for role, desig := range roleDesig {
		if g, ok := byDesig[strings.ToUpper(desig)]; ok {
			out[role] = g
		}
	}
	return out
}

func newSchExtractLayoutCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var (
		roleFlags []string
		from      string
		note      string
	)
	c := &cobra.Command{
		Use:   "extract-layout <block-id>",
		Short: "Reverse-derive a block's schematic_layout template from a placed instance (issue #140)",
		Long: `Read a real, well-placed instance of a block on the active schematic page and
emit the block's ` + "`schematic_layout`" + ` template (role → {dx,dy,rotation}), so a
peripheral placed nicely ONCE can be frozen into the block library as a data
pipeline instead of hand-typing offsets.

ROLE → DESIGNATOR mapping (deterministic — never guessed):
  --role ROLE=DESIG   explicit, wins; repeatable.
  --from D1,D2,…       auto-match each designator to the UNIQUE block role sharing
                       its prefix; ambiguous/unmatched prefixes ERROR (map them
                       with --role). Every role must be mapped.

ANCHOR (origin of the template): the first role (sorted) whose designator prefix
is "U" (chip/MCU), else the first role — so re-exporting the same instance is
stable.

SCOPE (v1): PRINTS the template for review; it does NOT write into the go:embed
block data (writeback must preserve the file's key order). Review the printed
block, paste ` + "`schematic_layout`" + ` into internal/blocks/data/<id>.json, and let
` + "`go test ./internal/blocks/...`" + ` validate full-role coverage + on-grid + legal
rotation.`,
		Args: cobra.ExactArgs(1),
		Example: `  easyeda sch extract-layout led_indicator_gpio --from LED1,R1,U1 --project ceshi
  easyeda sch extract-layout ch340c_usb_serial --role usb=U2 --role tvs=D1 --from R3,R4,C5`,
		RunE: func(cmd *cobra.Command, args []string) error {
			blockID := args[0]
			b, ok, err := blocks.Get(blockID)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("no such block %q — `easyeda blocks ls` to list", blockID)
			}
			if len(b.Parts) == 0 {
				return fmt.Errorf("block %s has no parts to extract", blockID)
			}

			prefixes, err := extractRolePrefixes(b)
			if err != nil {
				return err
			}
			explicit, err := parseKV(roleFlags, "--role")
			if err != nil {
				return err
			}
			var fromList []string
			if strings.TrimSpace(from) != "" {
				fromList = strings.Split(from, ",")
			}
			roleDesig, err := resolveExtractRoleMap(b, prefixes, explicit, fromList)
			if err != nil {
				return err
			}

			byDesig, err := fetchExtractGeom(cfg, *window)
			if err != nil {
				return err
			}
			// Every mapped designator must actually be on the page.
			var absent []string
			for role, d := range roleDesig {
				if _, ok := byDesig[strings.ToUpper(d)]; !ok {
					absent = append(absent, fmt.Sprintf("%s=%s", role, d))
				}
			}
			if len(absent) > 0 {
				sort.Strings(absent)
				return fmt.Errorf("these mapped designators are not on the active page: %v — check they are placed and you are on the right page (`easyeda doc switch`)", absent)
			}

			anchorRole := extractAnchorRole(prefixes)
			if note == "" {
				note = fmt.Sprintf("extracted from a placed instance (anchor role %q); review before committing", anchorRole)
			}
			layout, err := buildExtractedLayout(roleDesig, geomByRole(roleDesig, byDesig), anchorRole, note)
			if err != nil {
				return err
			}

			// Emit wrapped so it drops straight into the block JSON.
			wrapper := map[string]any{"schematic_layout": layout}
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			enc.SetEscapeHTML(false)
			if err := enc.Encode(wrapper); err != nil {
				return err
			}
			fmt.Fprintf(stderr, "extracted %d role(s) for block %s (anchor=%s). Paste \"schematic_layout\" into internal/blocks/data/%s.json, then run `go test ./internal/blocks/...` to validate.\n",
				len(layout.Roles), blockID, anchorRole, blockID)
			return nil
		},
	}
	c.Flags().StringArrayVar(&roleFlags, "role", nil, "explicit role→designator, e.g. --role usb=U2 (repeatable)")
	c.Flags().StringVar(&from, "from", "", "comma-separated designators to auto-match by unique prefix, e.g. --from LED1,R1,U1")
	c.Flags().StringVar(&note, "note", "", "note recorded in the template (default: extraction provenance)")
	return c
}
