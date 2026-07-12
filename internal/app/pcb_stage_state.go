package app

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// pcb_stage_state.go — persistable PCB flow gating (issue #97).
//
// The design-flow skill describes P2 placement-confirm, P3 outline-confirm and a
// P6 routability gate, but nothing in the CLI persisted or enforced them: a
// caller could run `pcb route-short` straight after import with a score-32
// layout and bank 30+ DRC errors. This is the missing state machine.
//
// State lives per project at <cwd>/.easyeda/pcb-stage/<project>.json — a plain
// file, no daemon, in the same .easyeda tree stage-snapshot already uses. The
// gate is advisory-but-enforced: route commands refuse without
// outline_confirmed + pre_route_passed unless the caller passes an explicit
// --force <reason>, which is recorded for audit rather than silently accepted.

// pcbStage is one node of the flow. Ordered by rank (see pcbStageRank).
type pcbStage string

const (
	stageImported           pcbStage = "imported"
	stagePlacementReady     pcbStage = "placement_ready"
	stagePlacementConfirmed pcbStage = "placement_confirmed"
	stageOutlineConfirmed   pcbStage = "outline_confirmed"
	stagePreRoutePassed     pcbStage = "pre_route_passed"
	stageRoutingAuthorized  pcbStage = "routing_authorized"
)

// pcbStageOrder is the canonical progression; index = rank.
var pcbStageOrder = []pcbStage{
	stageImported,
	stagePlacementReady,
	stagePlacementConfirmed,
	stageOutlineConfirmed,
	stagePreRoutePassed,
	stageRoutingAuthorized,
}

// pcbStageRank returns the ordinal of a stage (−1 if unknown).
func pcbStageRank(s pcbStage) int {
	for i, v := range pcbStageOrder {
		if v == s {
			return i
		}
	}
	return -1
}

// pcbStageEvent is one confirm / gate / force entry (audit trail).
type pcbStageEvent struct {
	Stage  pcbStage `json:"stage"`
	At     string   `json:"at"`
	Action string   `json:"action"` // confirm | gate-pass | force | invalidate
	Note   string   `json:"note,omitempty"`
	Reason string   `json:"reason,omitempty"` // for force
}

// pcbStageState is the persisted per-project record.
type pcbStageState struct {
	Project   string                `json:"project"`
	Confirmed map[pcbStage]bool     `json:"confirmed"`
	Layout    *pcbLayoutGateSummary `json:"layoutGate,omitempty"`
	History   []pcbStageEvent       `json:"history,omitempty"`
	UpdatedAt string                `json:"updatedAt"`
}

// pcbLayoutGateSummary is the machine-readable layout-lint gate snapshot stored
// when a pre_route gate passes (so route commands can show WHAT it passed on).
type pcbLayoutGateSummary struct {
	Score         int    `json:"score"`
	Verdict       string `json:"verdict"`
	Overlaps      int    `json:"overlaps"`
	OffBoard      int    `json:"offBoard"`
	CrossingCount int    `json:"crossingCount"`
	At            string `json:"at"`
}

// has reports whether a stage confirmation is currently set.
func (s *pcbStageState) has(st pcbStage) bool {
	return s != nil && s.Confirmed != nil && s.Confirmed[st]
}

// pcbStageDir is the per-project state root under the cwd's .easyeda tree.
func pcbStageDir() string {
	return filepath.Join(".easyeda", "pcb-stage")
}

// sanitizeProjectKey turns a project name/uuid into a safe file stem. Falls back
// to "_active" when routing is by --window (no project name available).
func sanitizeProjectKey(project string) string {
	p := stageSanitizeRe.ReplaceAllString(strings.TrimSpace(project), "-")
	p = strings.Trim(p, "-")
	if p == "" {
		return "_active"
	}
	return p
}

// pcbStagePath is the state file for a project.
func pcbStagePath(project string) string {
	return filepath.Join(pcbStageDir(), sanitizeProjectKey(project)+".json")
}

// loadPcbStageState reads the state; a missing file yields a fresh imported
// state rather than an error (the flow always starts at imported).
func loadPcbStageState(project string) (*pcbStageState, error) {
	path := pcbStagePath(project)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &pcbStageState{Project: project, Confirmed: map[pcbStage]bool{}}, nil
		}
		return nil, err
	}
	var s pcbStageState
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if s.Confirmed == nil {
		s.Confirmed = map[pcbStage]bool{}
	}
	return &s, nil
}

// savePcbStageState atomically persists the state.
func savePcbStageState(s *pcbStageState) error {
	if err := os.MkdirAll(pcbStageDir(), 0o755); err != nil {
		return err
	}
	s.UpdatedAt = time.Now().Format(time.RFC3339)
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	path := pcbStagePath(s.Project)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// confirmStage sets a stage confirmation and records the event.
func (s *pcbStageState) confirmStage(st pcbStage, action, note string) {
	if s.Confirmed == nil {
		s.Confirmed = map[pcbStage]bool{}
	}
	s.Confirmed[st] = true
	s.History = append(s.History, pcbStageEvent{
		Stage: st, At: time.Now().Format(time.RFC3339), Action: action, Note: note,
	})
}

// invalidateFrom clears the given stage and every stage after it, so a mutation
// (place-constrained / outline-fit / outline-round) can never leave a stale
// downstream confirmation standing. Records one invalidate event per cleared
// stage and drops the layout gate snapshot when pre_route is cleared.
func (s *pcbStageState) invalidateFrom(from pcbStage, cause string) []pcbStage {
	if s.Confirmed == nil {
		return nil
	}
	fromRank := pcbStageRank(from)
	if fromRank < 0 {
		return nil
	}
	var cleared []pcbStage
	for _, st := range pcbStageOrder {
		if pcbStageRank(st) >= fromRank && s.Confirmed[st] {
			delete(s.Confirmed, st)
			cleared = append(cleared, st)
		}
	}
	if pcbStageRank(stagePreRoutePassed) >= fromRank {
		s.Layout = nil
	}
	if len(cleared) > 0 {
		s.History = append(s.History, pcbStageEvent{
			Stage: from, At: time.Now().Format(time.RFC3339),
			Action: "invalidate", Note: cause,
		})
	}
	return cleared
}

// routeGate is the verdict for a routing command's precondition check.
type routeGate struct {
	Allowed bool     `json:"allowed"`
	Missing []string `json:"missing,omitempty"`
	Message string   `json:"message,omitempty"`
}

// checkRouteGate reports whether routing may proceed: it needs both
// outline_confirmed and pre_route_passed. When force is set it always allows and
// records the override reason so the bypass is auditable, not silent.
func checkRouteGate(s *pcbStageState, force bool, reason string) routeGate {
	var missing []string
	if !s.has(stageOutlineConfirmed) {
		missing = append(missing, string(stageOutlineConfirmed))
	}
	if !s.has(stagePreRoutePassed) {
		missing = append(missing, string(stagePreRoutePassed))
	}
	if len(missing) == 0 {
		return routeGate{Allowed: true}
	}
	if force {
		s.History = append(s.History, pcbStageEvent{
			Stage: stageRoutingAuthorized, At: time.Now().Format(time.RFC3339),
			Action: "force", Reason: reason,
			Note: "routing forced past missing: " + strings.Join(missing, ", "),
		})
		return routeGate{
			Allowed: true, Missing: missing,
			Message: "gate FORCED past " + strings.Join(missing, ", ") + " (reason: " + reason + ")",
		}
	}
	return routeGate{
		Allowed: false, Missing: missing,
		Message: "routing blocked: missing " + strings.Join(missing, ", ") +
			". Confirm layout (`pcb stage confirm-layout`), outline (`pcb stage confirm-outline`) " +
			"and pass the routability gate (`pcb layout-lint`), or override with `--force <reason>`.",
	}
}

// gateRouteCommand loads the project stage state and enforces the route gate. It
// returns errActionFailed (already-explained) when routing is blocked, nil when
// allowed (recording a forced override when --force <reason> is given). The
// bypass reason is required when forcing so an override is never anonymous.
func gateRouteCommand(cfg *appConfig, cmdName, forceReason string, stderr io.Writer) error {
	force := strings.TrimSpace(forceReason) != ""
	st, err := loadPcbStageState(cfg.project)
	if err != nil {
		fmt.Fprintf(stderr, "⚠️  %s: could not read PCB stage state: %v — treating as un-gated\n", cmdName, err)
		return nil
	}
	gate := checkRouteGate(st, force, strings.TrimSpace(forceReason))
	if !gate.Allowed {
		fmt.Fprintf(stderr, "❌ %s: %s\n", cmdName, gate.Message)
		return errActionFailed
	}
	if force {
		// checkRouteGate already recorded the force audit event (with the reason);
		// just flag routing_authorized so downstream steps see the (forced)
		// authorization, without a duplicate history entry.
		st.Confirmed[stageRoutingAuthorized] = true
		if serr := savePcbStageState(st); serr != nil {
			fmt.Fprintf(stderr, "⚠️  %s: gate override could not be persisted: %v\n", cmdName, serr)
		}
		fmt.Fprintf(stderr, "⚠️  %s: %s\n", cmdName, gate.Message)
	}
	return nil
}

// invalidatePcbStageFrom loads the project state, clears the given stage and
// everything downstream, and persists. Returns the cleared stages (as strings)
// so a command can surface what its mutation invalidated. Best-effort: a load /
// save failure is non-fatal to the caller (the mutation itself succeeded).
func invalidatePcbStageFrom(cfg *appConfig, from pcbStage, cause string) []string {
	st, err := loadPcbStageState(cfg.project)
	if err != nil {
		return nil
	}
	cleared := st.invalidateFrom(from, cause)
	if len(cleared) == 0 {
		return nil
	}
	_ = savePcbStageState(st)
	out := make([]string, len(cleared))
	for i, c := range cleared {
		out[i] = string(c)
	}
	return out
}
