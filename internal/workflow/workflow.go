// Package workflow is the persistable per-project design-flow state machine
// (issue #97 follow-up). It is shared by the CLI (internal/app) and the daemon
// (internal/daemon) so stage gates are enforced at BOTH layers:
//
//   - the CLI gates its composite commands (route-short / autoroute) and adds
//     document-fingerprint verification (drift → auto-invalidate);
//   - the daemon gates the low-level typed actions (pcb.line.create /
//     pcb.via.create / pcb.import_autoroute) at dispatch, so a raw /action
//     caller cannot bypass the flow, and invalidates downstream confirmations
//     after any placement/outline mutation (catalog-driven, like autosave).
//
// State lives per project at Dir()/<key>.json — a global directory
// (~/.easyeda-agent/workflow by default, EASYEDA_WORKFLOW_DIR to override), NOT
// the CLI's cwd, so the gate cannot be blinded by running the CLI from a
// different directory. The pre-#98 cwd-relative location
// (<cwd>/.easyeda/pcb-stage/<key>.json) is read as a legacy fallback and
// migrates to the global path on the next save.
package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Stage is one node of the flow. Ordered by rank (see Rank).
type Stage string

const (
	StageImported           Stage = "imported"
	StagePlacementReady     Stage = "placement_ready"
	StagePlacementConfirmed Stage = "placement_confirmed"
	StageOutlineConfirmed   Stage = "outline_confirmed"
	StagePreRoutePassed     Stage = "pre_route_passed"
	StageRoutingAuthorized  Stage = "routing_authorized"
	// StagePostRouteChecked is the "布完必查" gate: after routing, the pcb-check
	// audit must come back with zero hard ERRORs, zero power-not-poured and zero
	// width-under-spec findings before silkscreen/delivery. Any routing-class
	// mutation (track/via/pour/fill/beautify/import_autoroute/…) invalidates it
	// (catalog-driven, like the placement/outline stages).
	StagePostRouteChecked Stage = "post_route_checked"
)

// Order is the canonical progression; index = rank.
var Order = []Stage{
	StageImported,
	StagePlacementReady,
	StagePlacementConfirmed,
	StageOutlineConfirmed,
	StagePreRoutePassed,
	StageRoutingAuthorized,
	StagePostRouteChecked,
}

// Rank returns the ordinal of a stage (−1 if unknown).
func Rank(s Stage) int {
	for i, v := range Order {
		if v == s {
			return i
		}
	}
	return -1
}

// Event is one confirm / gate / force / invalidate entry (audit trail).
type Event struct {
	Stage  Stage  `json:"stage"`
	At     string `json:"at"`
	Action string `json:"action"` // confirm | gate-pass | force | invalidate | init
	Note   string `json:"note,omitempty"`
	Reason string `json:"reason,omitempty"` // for force
}

// GateSummary is the machine-readable layout-lint gate snapshot stored when a
// pre_route gate passes (so route commands can show WHAT it passed on).
type GateSummary struct {
	Score         int     `json:"score"`
	Verdict       string  `json:"verdict"`
	Overlaps      int     `json:"overlaps"`
	OffBoard      int     `json:"offBoard"`
	CrossingCount int     `json:"crossingCount"`
	MinGapMil     float64 `json:"minGapMil"`
	TightPairs    int     `json:"tightPairs"`
	// AccessMil / AccessBlocked capture the hand-solder iron-access check
	// (issue #99): every component must keep ≥1 bbox side with AccessMil of
	// clear corridor; AccessBlocked counts components boxed in on all sides.
	AccessMil     float64 `json:"accessMil,omitempty"`
	AccessBlocked int     `json:"accessBlocked,omitempty"`
	Assembly      string  `json:"assembly,omitempty"`
	At            string  `json:"at"`
}

// CheckGateSummary is the machine-readable `pcb check` gate snapshot stored
// when the post_route_checked gate passes (the routed-copper twin of the
// layout-lint GateSummary): what the board's DFM audit looked like at sign-off.
// Tracks is the routed track count at gate time — a WEAK drift check (there is
// no routing-geometry fingerprint yet; reconcile compares it to the live count).
type CheckGateSummary struct {
	Errors         int    `json:"errors"`
	Warnings       int    `json:"warnings"`
	WidthUnderSpec int    `json:"widthUnderSpec"`
	PowerNotPoured int    `json:"powerNotPoured"`
	Tracks         int    `json:"tracks"`
	At             string `json:"at"`
}

// AssemblyProfile is the project-level assembly/hand-solder contract. It is
// persisted so a later agent cannot silently fall back to electrical clearance
// after the user selected hand assembly (issue #99).
type AssemblyProfile struct {
	Profile           string  `json:"profile"` // hand-solder | reflow
	MinGapMil         float64 `json:"minGapMil"`
	LargePadAccessMil float64 `json:"largePadAccessMil,omitempty"`
	At                string  `json:"at"`
}

// Fingerprint pins a confirmation to the document state it was given on: a
// deterministic hash of the placement poses (or outline geometry) plus the
// element count. A later gate re-derives the hash from the live document and a
// mismatch auto-invalidates the confirmation — so a GUI drag, a debug.exec_js
// edit or another agent's move can never ride a stale sign-off into routing.
type Fingerprint struct {
	Hash  string `json:"hash"`
	Count int    `json:"count"`
	At    string `json:"at"`
}

// State is the persisted per-project record.
type State struct {
	Project   string            `json:"project"`
	Confirmed map[Stage]bool    `json:"confirmed"`
	Assembly  *AssemblyProfile  `json:"assembly,omitempty"`
	Layout    *GateSummary      `json:"layoutGate,omitempty"`
	Check     *CheckGateSummary `json:"checkGate,omitempty"`
	LayoutFP  *Fingerprint      `json:"layoutFingerprint,omitempty"`
	OutlineFP *Fingerprint      `json:"outlineFingerprint,omitempty"`
	// PowerTracksNets are the power nets `pcb power-planes` DELIBERATELY did not
	// pour: every inner plane layer was already assigned (GND + the largest power
	// net), so it routed these as fat tracks instead (its `routedAsTracks`
	// output + warning). The post_route_checked gate reads this list and does NOT
	// count their `power-not-poured` findings as blocking — without it the two
	// tools contradict each other ("this net must be tracks" vs "no pour, no
	// pass") and the agent deadlocks: pouring it collides with the net that owns
	// the plane, not pouring it fails the gate (issue #114). Exempt findings are
	// still PRINTED, just not blocking.
	PowerTracksNets []string `json:"powerTracksNets,omitempty"`
	// PlanePouredNets are the power nets `pcb power-planes` poured onto an inner
	// layer it then flipped to 内电层/PLANE. The platform stores PLANE-layer pours
	// in negative-image form with ZERO extension-API read path after a doc reload
	// (issue #110) — pcb.pour.list simply cannot see them — so the
	// power-not-poured rule would flag the very net the tool just poured, and its
	// suggested fix would be to re-run the command that poured it (issue #117:
	// the gate deadlocks). The post_route_checked gate reads this list and does
	// NOT count those findings as blocking; they are still printed as exempt.
	PlanePouredNets []string `json:"planePouredNets,omitempty"`
	// PlacementTiers are the per-tier placement sign-offs (issue #125): the
	// design-flow ladder 档1 孔/结构件 → 档2 边缘接口件 → 档3 主芯片+RF → 档4 卫星件
	// was prose-only ("agent 自觉"), so it got skipped — confirm-layout could seal
	// all four tiers in one stroke without any tier ever being reviewed. Each
	// tier now records WHICH designators it covers and a pose hash of exactly
	// those parts, so tiers invalidate independently: moving a satellite kills
	// tier 4 only, tiers 1–3 sign-offs survive.
	PlacementTiers map[int]*TierConfirm `json:"placementTiers,omitempty"`
	// Zones are the S0 spec's functional-zone claims (issue #126): module name →
	// {grid zone, designators}. Consumed by `pcb place-constrained` (parts placed
	// into their zone's board sub-rect) and `pcb check`'s zone-violation rule.
	Zones map[string]*ZoneClaim `json:"zones,omitempty"`
	// SchZones are the SCHEMATIC-side functional-zone claims: the same S0
	// modules[].zone contract, but resolved against the page's sheet bbox
	// (y-down) by `sch zones` / `sch layout-lint`'s zone-violation rule instead
	// of the board outline. Kept separate from Zones because the same module
	// legitimately claims different zones on sheet vs board.
	SchZones  map[string]*SchZoneClaim `json:"schZones,omitempty"`
	History   []Event                  `json:"history,omitempty"`
	UpdatedAt string                   `json:"updatedAt"`
}

// SchZoneClaim is one module's schematic zone claim. Page records which
// schematic page the module lives on (from S0 modules[].page); violation checks
// only see parts present on the ACTIVE page, so Page is documentation + future
// --all-pages routing, not a hard filter.
type SchZoneClaim struct {
	Zone  string   `json:"zone"`
	Page  string   `json:"page,omitempty"`
	Parts []string `json:"parts"` // designators, upper-case, sorted
	At    string   `json:"at"`
	Note  string   `json:"note,omitempty"`
}

// SetSchZones replaces the schematic zone claim table (module name → claim).
func (s *State) SetSchZones(z map[string]*SchZoneClaim) {
	s.SchZones = z
	s.History = append(s.History, Event{
		Stage: "sch-zones", At: time.Now().Format(time.RFC3339), Action: "confirm",
		Note: fmt.Sprintf("%d module schematic zone claim(s)", len(z)),
	})
}

// ZoneClaim is one functional zone's part claim (issue #126): the S0 spec's
// modules[].zone made executable. Keyed by module name in State.Zones; the zone
// value is a shared grid name (left/right/top/bottom/center and their -top/-bottom
// combos — same vocabulary the schematic autolayout uses), resolved to a board
// sub-rectangle at check/placement time from the LIVE outline. Zones are a spec
// contract, not a placement state: they survive placement invalidations and are
// only removed by `pcb zones clear` / re-set.
type ZoneClaim struct {
	Zone  string   `json:"zone"`
	Parts []string `json:"parts"` // designators, upper-case, sorted
	At    string   `json:"at"`
	Note  string   `json:"note,omitempty"`
}

// SetZones replaces the zone claim table (module name → claim).
func (s *State) SetZones(z map[string]*ZoneClaim) {
	s.Zones = z
	s.History = append(s.History, Event{
		Stage: "zones", At: time.Now().Format(time.RFC3339), Action: "confirm",
		Note: fmt.Sprintf("%d module zone claim(s)", len(z)),
	})
}

// ZoneOf resolves a designator to (module, zone). Case-insensitive.
func (s *State) ZoneOf(designator string) (module, zone string, ok bool) {
	if s == nil {
		return "", "", false
	}
	u := strings.ToUpper(strings.TrimSpace(designator))
	for name, zc := range s.Zones {
		if zc == nil {
			continue
		}
		for _, d := range zc.Parts {
			if strings.ToUpper(d) == u {
				return name, zc.Zone, true
			}
		}
	}
	return "", "", false
}

// TierConfirm is one placement tier's recorded sign-off (issue #125).
type TierConfirm struct {
	At          string   `json:"at"`
	Note        string   `json:"note,omitempty"`
	Designators []string `json:"designators,omitempty"` // normalized upper-case, sorted
	Hash        string   `json:"hash,omitempty"`        // HashLayout over exactly these parts
	Empty       bool     `json:"empty,omitempty"`       // deliberately empty tier (e.g. no RF parts)
}

// PlacementTierCount is the ladder length; TierNames documents each rung.
const PlacementTierCount = 4

// TierNames labels the placement ladder (design-flow P2 分档).
var TierNames = map[int]string{
	1: "孔/结构件",
	2: "边缘接口件(朝向须确认)",
	3: "主芯片+RF",
	4: "卫星件",
}

// Tier returns tier n's confirmation, nil when unconfirmed.
func (s *State) Tier(n int) *TierConfirm {
	if s == nil || s.PlacementTiers == nil {
		return nil
	}
	return s.PlacementTiers[n]
}

// ConfirmTier records tier n's sign-off (and the audit event).
func (s *State) ConfirmTier(n int, tc *TierConfirm) {
	if s.PlacementTiers == nil {
		s.PlacementTiers = map[int]*TierConfirm{}
	}
	s.PlacementTiers[n] = tc
	s.History = append(s.History, Event{
		Stage: Stage(fmt.Sprintf("placement_tier%d", n)),
		At:    time.Now().Format(time.RFC3339), Action: "confirm", Note: tc.Note,
	})
}

// InvalidateTiersFrom clears tier n and every later tier (a tier's parts moved,
// so its sign-off — and everything staged on top of it — is stale), plus the
// placement_confirmed seal downstream. Returns the cleared tier numbers.
func (s *State) InvalidateTiersFrom(n int, cause string) []int {
	if s == nil || s.PlacementTiers == nil {
		return nil
	}
	var cleared []int
	for t := n; t <= PlacementTierCount; t++ {
		if s.PlacementTiers[t] != nil {
			delete(s.PlacementTiers, t)
			cleared = append(cleared, t)
		}
	}
	if len(cleared) > 0 {
		s.History = append(s.History, Event{
			Stage: Stage(fmt.Sprintf("placement_tier%d", n)),
			At:    time.Now().Format(time.RFC3339), Action: "invalidate", Note: cause,
		})
		s.InvalidateFrom(StagePlacementConfirmed, cause)
	}
	return cleared
}

// ClaimedTiers maps designator (upper-case) → owning tier number.
func (s *State) ClaimedTiers() map[string]int {
	out := map[string]int{}
	if s == nil {
		return out
	}
	for n, tc := range s.PlacementTiers {
		if tc == nil {
			continue
		}
		for _, d := range tc.Designators {
			out[strings.ToUpper(d)] = n
		}
	}
	return out
}

// SetPowerTracksNets records power-planes' "route these as tracks, don't pour
// them" decision (normalized: trimmed, de-duplicated, sorted). Passing an empty
// list CLEARS the record — a power-planes run that poured everything must not
// leave a stale exemption standing.
func (s *State) SetPowerTracksNets(nets []string) {
	s.PowerTracksNets = normalizeNetList(nets)
}

// IsPowerTracksNet reports whether a net was recorded as "routed as tracks by
// power-planes" (case-insensitive: net names round-trip through several readers).
func (s *State) IsPowerTracksNet(net string) bool {
	if s == nil {
		return false
	}
	return netListContains(s.PowerTracksNets, net)
}

// SetPlanePouredNets records the nets power-planes poured onto a layer it then
// flipped to 内电层/PLANE — invisible to pour.list after reload (#110), so the
// gate must trust this record instead (#117). Same clear-on-empty semantics as
// SetPowerTracksNets: a run that flipped nothing wipes any stale exemption.
func (s *State) SetPlanePouredNets(nets []string) {
	s.PlanePouredNets = normalizeNetList(nets)
}

// IsPlanePouredNet reports whether a net was recorded as poured into an inner
// PLANE by power-planes (case-insensitive, like IsPowerTracksNet).
func (s *State) IsPlanePouredNet(net string) bool {
	if s == nil {
		return false
	}
	return netListContains(s.PlanePouredNets, net)
}

// normalizeNetList trims, de-duplicates and sorts a net list; empty in, nil out.
func normalizeNetList(nets []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(nets))
	for _, n := range nets {
		n = strings.TrimSpace(n)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil
	}
	return out
}

func netListContains(list []string, net string) bool {
	net = strings.TrimSpace(net)
	if net == "" {
		return false
	}
	for _, n := range list {
		if strings.EqualFold(strings.TrimSpace(n), net) {
			return true
		}
	}
	return false
}

// Has reports whether a stage confirmation is currently set.
func (s *State) Has(st Stage) bool {
	return s != nil && s.Confirmed != nil && s.Confirmed[st]
}

// Confirm sets a stage confirmation and records the event.
func (s *State) Confirm(st Stage, action, note string) {
	if s.Confirmed == nil {
		s.Confirmed = map[Stage]bool{}
	}
	s.Confirmed[st] = true
	s.History = append(s.History, Event{
		Stage: st, At: time.Now().Format(time.RFC3339), Action: action, Note: note,
	})
}

// InvalidateFrom clears the given stage and every stage after it, so a mutation
// can never leave a stale downstream confirmation standing. Records one
// invalidate event and drops the gate snapshot / fingerprints tied to the
// cleared stages.
func (s *State) InvalidateFrom(from Stage, cause string) []Stage {
	if s.Confirmed == nil {
		return nil
	}
	fromRank := Rank(from)
	if fromRank < 0 {
		return nil
	}
	var cleared []Stage
	for _, st := range Order {
		if Rank(st) >= fromRank && s.Confirmed[st] {
			delete(s.Confirmed, st)
			cleared = append(cleared, st)
		}
	}
	if Rank(StagePreRoutePassed) >= fromRank {
		s.Layout = nil
	}
	if Rank(StagePostRouteChecked) >= fromRank {
		s.Check = nil
	}
	if Rank(StagePlacementConfirmed) >= fromRank {
		s.LayoutFP = nil
		// PowerTracksNets dies with the PLACEMENT, not with the routing. The
		// decision is derived from the stackup + the net set + pad counts (which
		// net owns each plane layer), so:
		//   • a routing-class mutation (InvalidateFrom(post_route_checked)) must
		//     KEEP it — power-planes' verdict is still the truth about this board,
		//     and clearing it here would resurrect the #114 deadlock on the very
		//     next `workflow advance`;
		//   • a placement-class invalidation (this branch: placement_confirmed or
		//     earlier — re-import, parts added/removed, board redesign) DROPS it:
		//     the net set may be different now, so the old exemption could hide a
		//     genuinely un-poured power net. The next power-planes run re-derives
		//     and re-records it.
		s.PowerTracksNets = nil
		// PlanePouredNets follows the same lifecycle (#117): the PLANE pour is a
		// stackup/net-set fact, untouched by routing edits, but a placement-class
		// rebuild may change which net owns the plane — drop it and let the next
		// power-planes run re-record.
		s.PlanePouredNets = nil
	}
	if Rank(StageOutlineConfirmed) >= fromRank {
		s.OutlineFP = nil
	}
	// A reset back to placement_ready (or earlier) restarts placement from
	// scratch — the per-tier sign-offs (issue #125) go with it. A
	// placement_confirmed-level invalidation deliberately KEEPS them: tiers
	// carry their own pose hashes and invalidate individually on drift, so an
	// unrelated move only costs the tier it touched, not the whole ladder.
	if Rank(StagePlacementReady) >= fromRank {
		s.PlacementTiers = nil
	}
	if len(cleared) > 0 {
		s.History = append(s.History, Event{
			Stage: from, At: time.Now().Format(time.RFC3339),
			Action: "invalidate", Note: cause,
		})
	}
	return cleared
}

// ── storage ────────────────────────────────────────────────────────────────

// EnvDir overrides the state directory (tests, sandboxes).
const EnvDir = "EASYEDA_WORKFLOW_DIR"

// Dir is the global state root: EASYEDA_WORKFLOW_DIR, else
// ~/.easyeda-agent/workflow (the same tree the daemon audit log uses).
func Dir() string {
	if d := strings.TrimSpace(os.Getenv(EnvDir)); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.Getenv("HOME")
	}
	return filepath.Join(home, ".easyeda-agent", "workflow")
}

var sanitizeRe = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// SanitizeKey turns a project name/uuid into a safe file stem. Falls back to
// "_active" when routing is by --window (no project name available).
func SanitizeKey(project string) string {
	p := sanitizeRe.ReplaceAllString(strings.TrimSpace(project), "-")
	p = strings.Trim(p, "-")
	if p == "" {
		return "_active"
	}
	return p
}

// Path is the state file for a project.
func Path(project string) string {
	return filepath.Join(Dir(), SanitizeKey(project)+".json")
}

// legacyPath is the pre-global (cwd-relative) location PR #98 used.
func legacyPath(project string) string {
	return filepath.Join(".easyeda", "pcb-stage", SanitizeKey(project)+".json")
}

// Load reads the state; a missing file yields a fresh imported state rather
// than an error (the flow always starts at imported). When the global file is
// missing but the legacy cwd-relative one exists, the legacy state is loaded
// (and lands at the global path on the next Save).
func Load(project string) (*State, error) {
	for _, path := range []string{Path(project), legacyPath(project)} {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		var s State
		if err := json.Unmarshal(data, &s); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		if s.Confirmed == nil {
			s.Confirmed = map[Stage]bool{}
		}
		return &s, nil
	}
	return &State{Project: project, Confirmed: map[Stage]bool{}}, nil
}

// Exists reports whether a persisted state file exists for the project (global
// or legacy path).
func Exists(project string) bool {
	for _, path := range []string{Path(project), legacyPath(project)} {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}

// LoadAny loads the state for the first candidate key that has a persisted
// file; when none exists it returns a fresh state for the first non-empty
// candidate. Candidates let a caller that knows several identities for the same
// project (request hint, window project name, project uuid) find the record the
// CLI wrote, whichever key it used.
func LoadAny(candidates ...string) (*State, error) {
	first := ""
	for _, c := range candidates {
		if strings.TrimSpace(c) == "" {
			continue
		}
		if first == "" {
			first = c
		}
		if Exists(c) {
			return Load(c)
		}
	}
	return &State{Project: first, Confirmed: map[Stage]bool{}}, nil
}

// Save atomically persists the state at the global path.
func Save(s *State) error {
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}
	s.UpdatedAt = time.Now().Format(time.RFC3339)
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	path := Path(s.Project)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// InvalidateAll clears from-and-downstream on every candidate key that has a
// persisted state file (a project may have been recorded under its name AND its
// uuid). Returns the union of cleared stage names. Best-effort: save failures
// are skipped (the mutation that triggered this already succeeded).
func InvalidateAll(candidates []string, from Stage, cause string) []string {
	seenKey := map[string]bool{}
	clearedSet := map[string]bool{}
	for _, c := range candidates {
		if strings.TrimSpace(c) == "" {
			continue
		}
		key := SanitizeKey(c)
		if seenKey[key] || !Exists(c) {
			continue
		}
		seenKey[key] = true
		st, err := Load(c)
		if err != nil {
			continue
		}
		cleared := st.InvalidateFrom(from, cause)
		if len(cleared) == 0 {
			continue
		}
		if err := Save(st); err != nil {
			continue
		}
		for _, cl := range cleared {
			clearedSet[string(cl)] = true
		}
	}
	out := make([]string, 0, len(clearedSet))
	for k := range clearedSet {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ── route gate ─────────────────────────────────────────────────────────────

// Gate is the verdict for a routing command's precondition check.
type Gate struct {
	Allowed bool     `json:"allowed"`
	Forced  bool     `json:"forced,omitempty"`
	Missing []string `json:"missing,omitempty"`
	Message string   `json:"message,omitempty"`
	// Audited is set when the verdict appended a history event (force /
	// force-unsafe / force-refused) — the caller must persist the state so the
	// audit trail survives.
	Audited bool `json:"-"`
}

// CheckRouteGate reports whether routing may proceed: it needs both
// outline_confirmed and pre_route_passed.
//
// The override is TIERED (issue #132 — plan 1, 分级放行). #116's live run
// proved an unconditional --force is a footgun: with ZERO confirmations it
// produced 257 tracks + 92 vias that all had to be ripped. So:
//   - force (--force <reason>) bypasses SOFT gaps only — the mechanical
//     skeleton must be at least partly confirmed (placement_confirmed OR
//     outline_confirmed present). Typical legitimate uses: pre_route_passed
//     not re-run yet, outline pending while placement is signed off.
//   - forceUnsafe (--force-unsafe <reason>) bypasses everything, including a
//     zero-confirmation board — the deliberate, higher-friction escape hatch.
// Every bypass is recorded in the state history (action "force" /
// "force-unsafe"); a refused --force is recorded too ("force-refused"), so the
// attempt is auditable even though nothing ran. Authorization is for THIS run
// only: nothing is confirmed, the next un-forced call is gated again.
func CheckRouteGate(s *State, force, forceUnsafe bool, reason string) Gate {
	var missing []string
	if !s.Has(StageOutlineConfirmed) {
		missing = append(missing, string(StageOutlineConfirmed))
	}
	if !s.Has(StagePreRoutePassed) {
		missing = append(missing, string(StagePreRoutePassed))
	}
	if len(missing) == 0 {
		return Gate{Allowed: true}
	}
	// The mechanical skeleton (板框+布局) is unconfirmed when NEITHER placement
	// nor outline has a live sign-off — routing on such a board is the #116
	// rework case, not an override-worthy shortcut.
	hardMissing := !s.Has(StagePlacementConfirmed) && !s.Has(StageOutlineConfirmed)
	if forceUnsafe {
		s.History = append(s.History, Event{
			Stage: StageRoutingAuthorized, At: time.Now().Format(time.RFC3339),
			Action: "force-unsafe", Reason: reason,
			Note: "routing UNSAFE-forced past missing: " + strings.Join(missing, ", "),
		})
		return Gate{
			Allowed: true, Forced: true, Missing: missing, Audited: true,
			Message: "gate UNSAFE-FORCED past " + strings.Join(missing, ", ") + " (reason: " + reason + ")",
		}
	}
	if force {
		if hardMissing {
			s.History = append(s.History, Event{
				Stage: StageRoutingAuthorized, At: time.Now().Format(time.RFC3339),
				Action: "force-refused", Reason: reason,
				Note: "--force refused: mechanical skeleton unconfirmed (neither placement_confirmed nor outline_confirmed)",
			})
			return Gate{
				Allowed: false, Missing: missing, Audited: true,
				Message: "routing blocked: --force cannot bypass an UNCONFIRMED mechanical skeleton (neither placement_confirmed nor outline_confirmed is set — issue #132). " +
					"Confirm layout (`easyeda pcb stage confirm-layout`) + outline (`easyeda pcb stage confirm-outline`), " +
					"or escalate deliberately with `--force-unsafe <reason>`.",
			}
		}
		s.History = append(s.History, Event{
			Stage: StageRoutingAuthorized, At: time.Now().Format(time.RFC3339),
			Action: "force", Reason: reason,
			Note: "routing forced past missing: " + strings.Join(missing, ", "),
		})
		return Gate{
			Allowed: true, Forced: true, Missing: missing, Audited: true,
			Message: "gate FORCED past " + strings.Join(missing, ", ") + " (reason: " + reason + ")",
		}
	}
	return Gate{
		Allowed: false, Missing: missing,
		Message: "routing blocked: missing " + strings.Join(missing, ", ") +
			". Confirm layout (`easyeda pcb stage confirm-layout`), outline (`easyeda pcb stage confirm-outline`) " +
			"and pass the routability gate (`easyeda pcb layout-lint --gate`), or override with `--force <reason>`" +
			" (--force-unsafe <reason> if even the mechanical skeleton is unconfirmed).",
	}
}

// ── fingerprints ───────────────────────────────────────────────────────────

// ComponentPose is the placement identity of one component: what a layout
// fingerprint is derived from. Values are NORMALIZED before hashing (see
// normPose) so representation noise from a round-trip re-read — float tails,
// -0.0, rotation aliasing (360≡0), layer reported as number vs name — never
// reads as drift (issue #100: a plain `doc reload` used to invalidate
// placement_confirmed with zero components moved).
type ComponentPose struct {
	Designator string
	X, Y       float64
	Rotation   float64
	Layer      string
}

// normCoord rounds to 1 mil and squashes -0.0 → 0.0. 1 mil (vs the old 0.1)
// absorbs connector float tails; no real placement edit is sub-mil.
func normCoord(v float64) float64 {
	r := math.Round(v)
	if r == 0 {
		return 0 // canonicalize -0.0
	}
	return r
}

// normRotation folds rotation into [0,360) at 1° granularity (-90 ≡ 270,
// 360 ≡ 0) and squashes -0.0.
func normRotation(v float64) float64 {
	r := math.Mod(math.Round(v), 360)
	if r < 0 {
		r += 360
	}
	if r == 0 {
		return 0
	}
	return r
}

// normLayer canonicalizes the layer to "1"/"2"/… — the connector reports it
// as a number, some readers stringify it, and the old asString() coercion
// turned numeric layers into "" (so a TOP↔BOTTOM flip was INVISIBLE to the
// fingerprint). Names map to their EPCB ids; unknown strings pass through
// lowercased so at least equal inputs hash equally.
func normLayer(s string) string {
	t := strings.ToLower(strings.TrimSpace(s))
	switch t {
	case "", "0":
		return ""
	case "1", "top", "toplayer", "top layer":
		return "1"
	case "2", "bottom", "bottomlayer", "bottom layer":
		return "2"
	}
	return t
}

// HashLayout derives a deterministic fingerprint hash from component poses
// (sorted by designator; primitive ids are excluded — they churn across window
// reloads while the placement itself is unchanged). Poses are normalized so
// only a GEOMETRIC change (move/rotate/flip) changes the hash.
func HashLayout(poses []ComponentPose) string {
	sorted := make([]ComponentPose, len(poses))
	copy(sorted, poses)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Designator != sorted[j].Designator {
			return sorted[i].Designator < sorted[j].Designator
		}
		// Duplicate designators (should not happen, but never let map/order
		// nondeterminism produce a spurious drift): order by pose.
		return sorted[i].X < sorted[j].X || (sorted[i].X == sorted[j].X && sorted[i].Y < sorted[j].Y)
	})
	h := sha256.New()
	for _, p := range sorted {
		fmt.Fprintf(h, "%s|%.0f|%.0f|%.0f|%s\n", p.Designator,
			normCoord(p.X), normCoord(p.Y), normRotation(p.Rotation), normLayer(p.Layer))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// HashJSON derives a deterministic fingerprint hash from any JSON-encodable
// value (encoding/json sorts map keys, so equal content hashes equally). Used
// for the outline geometry snapshot.
func HashJSON(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// NewFingerprint stamps a hash + count with the current time.
func NewFingerprint(hash string, count int) *Fingerprint {
	return &Fingerprint{Hash: hash, Count: count, At: time.Now().Format(time.RFC3339)}
}
