package app

// easyeda apply — declarative playbook replay (docs/design-apply-playbook.md).
//
// A playbook is a JSON file of ordered steps. Each step is exactly one of:
//   action: a typed daemon action (validated against the protocol catalog)
//   run:    a Cobra subcommand line (the composite-tool layer)
//   notify: a toast inside EasyEDA (sugar for system.notify)
//
// Error discipline (design §错误处理): steps are interdependent, so the
// default is fail-fast. Read-only steps auto-retry transient errors; a
// MUTATING step that times out is NOT retried (the mutation may have landed —
// measured on this project) — it stops, or runs its `verify` block to decide.

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

// ── playbook file model ─────────────────────────────────────────────────────

type playbookMeta struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Project     string `json:"project,omitempty"`
	Window      string `json:"window,omitempty"`
	Doc         string `json:"doc,omitempty"`
}

type stepPolicy struct {
	TimeoutSec      *int  `json:"timeoutSec,omitempty"`
	Retry           *int  `json:"retry,omitempty"`
	ContinueOnError *bool `json:"continueOnError,omitempty"`
}

type verifyBlock struct {
	Action  string            `json:"action,omitempty"`
	Run     string            `json:"run,omitempty"`
	Payload map[string]any    `json:"payload,omitempty"`
	Flags   map[string]any    `json:"flags,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Assert  map[string]string `json:"assert,omitempty"`
}

type playbookStep struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`

	Action  string         `json:"action,omitempty"`
	Payload map[string]any `json:"payload,omitempty"`

	Run   string         `json:"run,omitempty"`
	Flags map[string]any `json:"flags,omitempty"`
	Args  []string       `json:"args,omitempty"`

	Notify string `json:"notify,omitempty"`

	Capture map[string]string `json:"capture,omitempty"`
	Assert  map[string]string `json:"assert,omitempty"`
	OnFail  string            `json:"onFail,omitempty"` // stop|continue|prompt

	stepPolicy
	Confirm    *bool        `json:"confirm,omitempty"`
	Checkpoint bool         `json:"checkpoint,omitempty"`
	Verify     *verifyBlock `json:"verify,omitempty"`
}

type playbook struct {
	Version  int               `json:"version"`
	Meta     playbookMeta      `json:"meta"`
	Defaults stepPolicy        `json:"defaults"`
	Vars     map[string]string `json:"vars,omitempty"`
	Steps    []playbookStep    `json:"steps"`
}

// ── journal model ───────────────────────────────────────────────────────────

type journalHeader struct {
	PlaybookSha string `json:"playbookSha256"`
	Name        string `json:"name"`
	Project     string `json:"project,omitempty"`
	StartedAt   string `json:"startedAt"`
}

type journalEntry struct {
	Idx      int               `json:"idx"`
	ID       string            `json:"id"`
	Status   string            `json:"status"` // ok | ok(verified) | fail | skipped
	Ms       int64             `json:"ms"`
	Captured map[string]string `json:"captured,omitempty"`
	Error    string            `json:"error,omitempty"`
}

// ── command ─────────────────────────────────────────────────────────────────

func newApplyCmd(cfg *appConfig, stdout, stderr io.Writer) *cobra.Command {
	var (
		dryRun, resume, yes, quiet bool
		fromRef, toRef             string
		journalPath, window, doc   string
		varOverrides               []string
		stepDelay                  float64
	)

	cmd := &cobra.Command{
		Use:   "apply <playbook.json>",
		Short: "Replay a declarative step file (playbook) against EasyEDA",
		Long: `Execute a playbook (see docs/design-apply-playbook.md) step by step:
typed actions, CLI subcommands, capture/assert gates, journal + resume.
Precedence: CLI flag > playbook file > built-in default.`,
		Example: `  easyeda apply sch.playbook.json
  easyeda apply pcb.playbook.json --project demo2 --var LIB=<uuid>
  easyeda apply pcb.playbook.json --resume
  easyeda apply pcb.playbook.json --dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pb, raw, err := loadPlaybook(args[0])
			if err != nil {
				return err
			}

			// precedence: CLI flag > file
			if cfg.project == "" {
				cfg.project = pb.Meta.Project
			}
			effWindow := window
			if effWindow == "" {
				effWindow = pb.Meta.Window
			}
			if doc != "" { // CLI flag > playbook meta.doc
				pb.Meta.Doc = doc
			}
			vars := map[string]string{}
			maps.Copy(vars, pb.Vars)
			for _, kv := range varOverrides {
				k, v, ok := strings.Cut(kv, "=")
				if !ok {
					return fmt.Errorf("invalid --var %q, expected KEY=VALUE", kv)
				}
				vars[k] = v
			}

			if errs := preflight(pb, vars); len(errs) > 0 {
				for _, e := range errs {
					fmt.Fprintf(stderr, "preflight: %s\n", e)
				}
				return fmt.Errorf("playbook failed preflight (%d problem(s))", len(errs))
			}

			if journalPath == "" {
				journalPath = args[0] + ".journal.jsonl"
			}

			r := &applyRunner{
				cfg: cfg, stdout: stdout, stderr: stderr,
				pb: pb, pbPath: args[0], sha: sha256Hex(raw), vars: vars,
				window: effWindow, yes: yes, quiet: quiet,
				journalPath: journalPath,
				stepDelay:   time.Duration(stepDelay * float64(time.Second)),
			}
			if dryRun {
				return r.printPlan()
			}
			if resume {
				if err := r.loadJournal(); err != nil {
					return err
				}
			}
			if err := r.resolveRange(fromRef, toRef); err != nil {
				return err
			}
			return r.execute()
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "validate + print the plan, execute nothing")
	cmd.Flags().BoolVar(&resume, "resume", false, "skip steps already ok in the journal (restores captured vars)")
	cmd.Flags().BoolVar(&yes, "yes", false, "auto-approve confirmation-gated steps")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "only print failures and the final summary")
	cmd.Flags().StringVar(&fromRef, "from", "", "start at step (id or 1-based index)")
	cmd.Flags().StringVar(&toRef, "to", "", "stop after step (id or 1-based index)")
	cmd.Flags().StringVar(&journalPath, "journal", "", "journal path (default <playbook>.journal.jsonl)")
	cmd.Flags().StringVar(&window, "window", "", "EasyEDA window ID (overrides meta.window)")
	cmd.Flags().StringVar(&doc, "doc", "", "target document to switch to first (overrides meta.doc)")
	cmd.Flags().StringArrayVar(&varOverrides, "var", nil, "override/add a playbook var: KEY=VALUE (repeatable)")
	cmd.Flags().Float64Var(&stepDelay, "step-delay", 0, "pause N seconds between steps (demo / recording pacing)")
	return cmd
}

// ── load + preflight ────────────────────────────────────────────────────────

func loadPlaybook(path string) (*playbook, []byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read playbook: %w", err)
	}
	var pb playbook
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&pb); err != nil {
		return nil, nil, fmt.Errorf("parse playbook %s: %w", filepath.Base(path), err)
	}
	return &pb, raw, nil
}

// actionCatalog indexes the typed-action specs once.
func actionCatalog() map[string]protocol.ActionSpec {
	m := map[string]protocol.ActionSpec{}
	for _, a := range protocol.AllActions() {
		m[a.Name] = a
	}
	return m
}

// readOnlyRunPrefixes lists `run:` targets that are safe to auto-retry
// (read-only or idempotent). Everything else is treated as mutating.
var readOnlyRunPrefixes = []string{
	"sch layout-lint", "pcb layout-lint", "sch check", "pcb check",
	"sch drc", "pcb drc", "sch list", "pcb list", "sch read",
	"sch sheet-geometry", "pcb report", "pcb layers", "pcb nets",
	"pcb track-list", "pcb via-list", "pcb pour-list", "pcb drc-rules",
	"pcb outline-get", "pcb board-info", "pcb docs", "board list",
	"doc ls", "doc switch", "project", "daemon health", "lib ", "api ",
	"audit", "view ", "actions", "version",
}

// confirmRunNeedles marks `run:` targets that are destructive and default to
// a confirmation gate (design: delete/clear/import 类自动置真).
var confirmRunNeedles = []string{"delete", "clear", "rip-up", "import-changes", "import-autoroute"}

func runIsReadOnly(run string) bool {
	for _, p := range readOnlyRunPrefixes {
		if strings.HasPrefix(run, p) {
			return true
		}
	}
	return false
}

func runNeedsConfirm(run string) bool {
	for _, n := range confirmRunNeedles {
		if strings.Contains(run, n) {
			return true
		}
	}
	return false
}

func stepKind(s *playbookStep) string {
	switch {
	case s.Action != "":
		return "action"
	case s.Run != "":
		return "run"
	case s.Notify != "":
		return "notify"
	default:
		return ""
	}
}

func preflight(pb *playbook, vars map[string]string) []string {
	var errs []string
	if pb.Version != 1 {
		errs = append(errs, fmt.Sprintf("unsupported version %d (want 1)", pb.Version))
	}
	if pb.Meta.Name == "" {
		errs = append(errs, "meta.name is required")
	}
	if len(pb.Steps) == 0 {
		errs = append(errs, "steps is empty")
	}
	catalog := actionCatalog()
	known := map[string]bool{}
	for k := range vars {
		known[k] = true
	}
	seenIDs := map[string]bool{}
	for i := range pb.Steps {
		s := &pb.Steps[i]
		ref := s.ID
		if ref == "" {
			ref = fmt.Sprintf("s%d", i+1)
		}
		if seenIDs[ref] {
			errs = append(errs, fmt.Sprintf("step %d: duplicate id %q", i+1, ref))
		}
		seenIDs[ref] = true

		n := 0
		if s.Action != "" {
			n++
		}
		if s.Run != "" {
			n++
		}
		if s.Notify != "" {
			n++
		}
		if n != 1 {
			errs = append(errs, fmt.Sprintf("step %s: exactly one of action|run|notify required", ref))
			continue
		}
		if s.Action != "" {
			if _, ok := catalog[s.Action]; !ok {
				errs = append(errs, fmt.Sprintf("step %s: unknown action %q", ref, s.Action))
			}
		}
		if s.OnFail != "" && s.OnFail != "stop" && s.OnFail != "continue" && s.OnFail != "prompt" {
			errs = append(errs, fmt.Sprintf("step %s: invalid onFail %q", ref, s.OnFail))
		}
		// static ${var} resolution: names must be file vars or a prior capture
		for _, miss := range unresolvedVars(s, known) {
			errs = append(errs, fmt.Sprintf("step %s: ${%s} is not a var and not captured by any earlier step", ref, miss))
		}
		for name := range s.Capture {
			known[name] = true
		}
	}
	return errs
}

// unresolvedVars walks every string in the step's inputs and reports ${names}
// missing from the known set.
func unresolvedVars(s *playbookStep, known map[string]bool) []string {
	var missing []string
	seen := map[string]bool{}
	walk := func(v any) {
		walkStrings(v, func(str string) string {
			for _, name := range varRefs(str) {
				if !known[name] && !seen[name] {
					seen[name] = true
					missing = append(missing, name)
				}
			}
			return str
		})
	}
	walk(s.Payload)
	walk(s.Flags)
	for _, a := range s.Args {
		walk(a)
	}
	if s.Verify != nil {
		walk(s.Verify.Payload)
		walk(s.Verify.Flags)
		for _, a := range s.Verify.Args {
			walk(a)
		}
	}
	return missing
}

// varRefs extracts ${NAME} references from a string.
func varRefs(s string) []string {
	var out []string
	for i := 0; i+1 < len(s); i++ {
		if s[i] == '$' && s[i+1] == '{' {
			if j := strings.IndexByte(s[i+2:], '}'); j >= 0 {
				out = append(out, s[i+2:i+2+j])
				i += 2 + j
			}
		}
	}
	return out
}

// walkStrings applies fn to every string inside a JSON-ish value, returning a
// deep copy with replacements applied.
func walkStrings(v any, fn func(string) string) any {
	switch t := v.(type) {
	case string:
		return fn(t)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, vv := range t {
			out[k] = walkStrings(vv, fn)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, vv := range t {
			out[i] = walkStrings(vv, fn)
		}
		return out
	default:
		return v
	}
}

// substVars replaces ${NAME} in every string of v. Unknown names error.
func substVars(v any, vars map[string]string) (any, error) {
	var missing []string
	out := walkStrings(v, func(s string) string {
		return replaceVarRefs(s, vars, &missing)
	})
	if len(missing) > 0 {
		return nil, fmt.Errorf("undefined variable(s): %s", strings.Join(missing, ", "))
	}
	return out, nil
}

func replaceVarRefs(s string, vars map[string]string, missing *[]string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '$' && i+1 < len(s) && s[i+1] == '{' {
			if j := strings.IndexByte(s[i+2:], '}'); j >= 0 {
				name := s[i+2 : i+2+j]
				if val, ok := vars[name]; ok {
					b.WriteString(val)
				} else {
					*missing = append(*missing, name)
				}
				i += 2 + j + 1
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// ── jsonpath-lite + predicates ─────────────────────────────────────────────

// jsonPathLite resolves a "$.a.b[2].c" style path (dots + numeric indexes
// only — no filters) against a decoded JSON value. Returns (nil, false) when
// any hop is missing.
func jsonPathLite(root any, path string) (any, bool) {
	p := strings.TrimSpace(path)
	if p == "$" {
		return root, true
	}
	p = strings.TrimPrefix(p, "$")
	cur := root
	for len(p) > 0 {
		switch {
		case p[0] == '.':
			p = p[1:]
			end := strings.IndexAny(p, ".[")
			var key string
			if end < 0 {
				key, p = p, ""
			} else {
				key, p = p[:end], p[end:]
			}
			m, ok := cur.(map[string]any)
			if !ok {
				return nil, false
			}
			cur, ok = m[key]
			if !ok {
				return nil, false
			}
		case p[0] == '[':
			end := strings.IndexByte(p, ']')
			if end < 0 {
				return nil, false
			}
			idx, err := strconv.Atoi(p[1:end])
			if err != nil {
				return nil, false
			}
			arr, ok := cur.([]any)
			if !ok || idx < 0 || idx >= len(arr) {
				return nil, false
			}
			cur = arr[idx]
			p = p[end+1:]
		default:
			return nil, false
		}
	}
	return cur, true
}

// evalPredicate checks value against a predicate string:
//
//	"==X" "!=X" ">=N" "<=N" ">N" "<N" "exists" "true" "false"
//	"len==N" "len>=N" "len<=N" "len>N" "len<N"  (arrays/maps/strings)
func evalPredicate(val any, found bool, pred string) (bool, string) {
	pred = strings.TrimSpace(pred)
	if pred == "exists" {
		return found, fmt.Sprintf("exists=%v", found)
	}
	if !found {
		return false, "path not found"
	}
	if strings.HasPrefix(pred, "len") {
		n := -1
		switch t := val.(type) {
		case []any:
			n = len(t)
		case map[string]any:
			n = len(t)
		case string:
			n = len(t)
		default:
			return false, fmt.Sprintf("len unsupported on %T", val)
		}
		return compareNumeric(float64(n), strings.TrimPrefix(pred, "len"))
	}
	switch pred {
	case "true", "false":
		b, ok := val.(bool)
		return ok && strconv.FormatBool(b) == pred, fmt.Sprintf("value=%v", val)
	}
	if num, ok := toFloat(val); ok {
		if okc, detail := compareNumeric(num, pred); detail != "bad-op" {
			return okc, detail
		}
	}
	// string equality forms
	if strings.HasPrefix(pred, "==") {
		want := strings.TrimSpace(pred[2:])
		return fmt.Sprintf("%v", val) == want, fmt.Sprintf("value=%v", val)
	}
	if strings.HasPrefix(pred, "!=") {
		want := strings.TrimSpace(pred[2:])
		return fmt.Sprintf("%v", val) != want, fmt.Sprintf("value=%v", val)
	}
	return false, fmt.Sprintf("unsupported predicate %q", pred)
}

func compareNumeric(have float64, op string) (bool, string) {
	op = strings.TrimSpace(op)
	parse := func(s string) (float64, bool) {
		f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		return f, err == nil
	}
	detail := fmt.Sprintf("value=%g", have)
	switch {
	case strings.HasPrefix(op, ">="):
		if w, ok := parse(op[2:]); ok {
			return have >= w, detail
		}
	case strings.HasPrefix(op, "<="):
		if w, ok := parse(op[2:]); ok {
			return have <= w, detail
		}
	case strings.HasPrefix(op, "=="):
		if w, ok := parse(op[2:]); ok {
			return have == w, detail
		}
	case strings.HasPrefix(op, "!="):
		if w, ok := parse(op[2:]); ok {
			return have != w, detail
		}
	case strings.HasPrefix(op, ">"):
		if w, ok := parse(op[1:]); ok {
			return have > w, detail
		}
	case strings.HasPrefix(op, "<"):
		if w, ok := parse(op[1:]); ok {
			return have < w, detail
		}
	}
	return false, "bad-op"
}

func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

// formatCaptured renders a captured JSON value as a substitution-friendly string.
func formatCaptured(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprintf("%v", t)
		}
		return string(b)
	}
}

// ── runner ──────────────────────────────────────────────────────────────────

type applyRunner struct {
	cfg            *appConfig
	stdout, stderr io.Writer
	pb             *playbook
	pbPath         string
	sha            string
	vars           map[string]string
	window         string
	yes, quiet     bool

	journalPath string
	journal     *os.File
	doneOK      map[int]bool // idx (0-based) -> already ok (resume)
	stepDelay   time.Duration

	fromIdx, toIdx int // 0-based inclusive range
}

func (r *applyRunner) stepRef(i int) string {
	if r.pb.Steps[i].ID != "" {
		return r.pb.Steps[i].ID
	}
	return fmt.Sprintf("s%d", i+1)
}

func (r *applyRunner) resolveRange(fromRef, toRef string) error {
	r.fromIdx, r.toIdx = 0, len(r.pb.Steps)-1
	find := func(ref string) (int, error) {
		if ref == "" {
			return -1, nil
		}
		for i := range r.pb.Steps {
			if r.stepRef(i) == ref {
				return i, nil
			}
		}
		if n, err := strconv.Atoi(ref); err == nil && n >= 1 && n <= len(r.pb.Steps) {
			return n - 1, nil
		}
		return -1, fmt.Errorf("step %q not found (id or 1-based index)", ref)
	}
	if i, err := find(fromRef); err != nil {
		return err
	} else if i >= 0 {
		r.fromIdx = i
	}
	if i, err := find(toRef); err != nil {
		return err
	} else if i >= 0 {
		r.toIdx = i
	}
	if r.fromIdx > r.toIdx {
		return fmt.Errorf("--from is after --to")
	}
	return nil
}

func (r *applyRunner) printPlan() error {
	fmt.Fprintf(r.stdout, "playbook %q — %d step(s), project=%s\n", r.pb.Meta.Name, len(r.pb.Steps), r.cfg.project)
	catalog := actionCatalog()
	for i := range r.pb.Steps {
		s := &r.pb.Steps[i]
		target := s.Action + s.Run
		if s.Notify != "" {
			target = "notify: " + s.Notify
		}
		flags := ""
		if r.needsConfirm(s, catalog) {
			flags += " [confirm]"
		}
		if len(s.Assert) > 0 {
			flags += " [gate]"
		}
		if s.Verify != nil {
			flags += " [verify]"
		}
		if s.Checkpoint {
			flags += " [checkpoint]"
		}
		fmt.Fprintf(r.stdout, "  [%d/%d] %-16s %-7s %s%s\n", i+1, len(r.pb.Steps), r.stepRef(i), stepKind(s), target, flags)
	}
	fmt.Fprintln(r.stdout, "dry-run: preflight passed, nothing executed")
	return nil
}

func (r *applyRunner) needsConfirm(s *playbookStep, catalog map[string]protocol.ActionSpec) bool {
	if s.Confirm != nil {
		return *s.Confirm
	}
	if s.Action != "" {
		return catalog[s.Action].NeedsConfirm
	}
	if s.Run != "" {
		return runNeedsConfirm(s.Run)
	}
	return false
}

func (r *applyRunner) isReadOnly(s *playbookStep, catalog map[string]protocol.ActionSpec) bool {
	if s.Action != "" {
		spec, ok := catalog[s.Action]
		return ok && !spec.Mutates
	}
	if s.Run != "" {
		return runIsReadOnly(s.Run)
	}
	return true // notify
}

func (r *applyRunner) policy(s *playbookStep) (timeout time.Duration, retry int, contOnErr bool) {
	timeout = defaultActionTimeout
	retry = 2 // design: factory default retry for read-only steps
	if r.pb.Defaults.TimeoutSec != nil {
		timeout = time.Duration(*r.pb.Defaults.TimeoutSec) * time.Second
	}
	if r.pb.Defaults.Retry != nil {
		retry = *r.pb.Defaults.Retry
	}
	if r.pb.Defaults.ContinueOnError != nil {
		contOnErr = *r.pb.Defaults.ContinueOnError
	}
	if s.TimeoutSec != nil {
		timeout = time.Duration(*s.TimeoutSec) * time.Second
	}
	if s.Retry != nil {
		retry = *s.Retry
	}
	if s.ContinueOnError != nil {
		contOnErr = *s.ContinueOnError
	}
	return timeout, retry, contOnErr
}

// loadJournal reads a prior journal for --resume: verifies the playbook hash,
// marks ok steps, restores captured vars.
func (r *applyRunner) loadJournal() error {
	f, err := os.Open(r.journalPath)
	if err != nil {
		return fmt.Errorf("resume: open journal: %w", err)
	}
	defer f.Close()
	r.doneOK = map[int]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	first := true
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if first {
			first = false
			var h journalHeader
			if err := json.Unmarshal([]byte(line), &h); err != nil {
				return fmt.Errorf("resume: bad journal header: %w", err)
			}
			if h.PlaybookSha != r.sha {
				return fmt.Errorf("resume: playbook content changed since the journal was written — rerun without --resume (or use --from)")
			}
			continue
		}
		var e journalEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue // tolerate a torn tail line (crash mid-write)
		}
		if strings.HasPrefix(e.Status, "ok") && e.Idx >= 1 && e.Idx <= len(r.pb.Steps) {
			r.doneOK[e.Idx-1] = true
			maps.Copy(r.vars, e.Captured)
		}
	}
	return sc.Err()
}

func (r *applyRunner) writeJournal(v any) {
	if r.journal == nil {
		return
	}
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	_, _ = r.journal.Write(append(b, '\n'))
}

func (r *applyRunner) execute() error {
	f, err := os.OpenFile(r.journalPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open journal: %w", err)
	}
	r.journal = f
	defer f.Close()
	r.writeJournal(journalHeader{
		PlaybookSha: r.sha, Name: r.pb.Meta.Name,
		Project: r.cfg.project, StartedAt: time.Now().Format(time.RFC3339),
	})

	// initial doc switch (meta.doc)
	if r.pb.Meta.Doc != "" {
		if !r.quiet {
			fmt.Fprintf(r.stdout, "→ doc switch %s\n", r.pb.Meta.Doc)
		}
		if _, err := r.runSubcommand("doc switch", nil, []string{r.pb.Meta.Doc}); err != nil {
			return fmt.Errorf("meta.doc switch failed: %w", err)
		}
	}

	catalog := actionCatalog()
	okCount, skipCount := 0, 0
	for i := range r.pb.Steps {
		s := &r.pb.Steps[i]
		ref := r.stepRef(i)
		if i < r.fromIdx || i > r.toIdx || (r.doneOK != nil && r.doneOK[i]) {
			skipCount++
			if !r.quiet {
				fmt.Fprintf(r.stdout, "[%d/%d] %-16s skipped\n", i+1, len(r.pb.Steps), ref)
			}
			continue
		}

		if r.needsConfirm(s, catalog) && !r.yes {
			ok, err := r.promptYesNo(fmt.Sprintf("step %s (%s %s) needs confirmation — run it?", ref, stepKind(s), s.Action+s.Run))
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("step %s: declined — stopping (resume later with --resume)", ref)
			}
		}

		start := time.Now()
		captured, execErr := r.executeStep(s, catalog)
		ms := time.Since(start).Milliseconds()

		if execErr == nil {
			okCount++
			status := "ok"
			r.writeJournal(journalEntry{Idx: i + 1, ID: ref, Status: status, Ms: ms, Captured: captured})
			if !r.quiet {
				mark := ""
				if s.Checkpoint {
					mark = " 💾"
				}
				fmt.Fprintf(r.stdout, "[%d/%d] %-16s ok (%.1fs)%s\n", i+1, len(r.pb.Steps), ref, float64(ms)/1000, mark)
			}
			if r.stepDelay > 0 && i < r.toIdx {
				time.Sleep(r.stepDelay)
			}
			continue
		}

		// failure path
		r.writeJournal(journalEntry{Idx: i + 1, ID: ref, Status: "fail", Ms: ms, Error: execErr.Error()})
		_, _, contOnErr := r.policy(s)
		onFail := s.OnFail
		if onFail == "" {
			if contOnErr {
				onFail = "continue"
			} else {
				onFail = "stop"
			}
		}
		fmt.Fprintf(r.stderr, "\n✗ step [%d/%d] %s failed: %v\n", i+1, len(r.pb.Steps), ref, execErr)
		switch onFail {
		case "continue":
			fmt.Fprintf(r.stderr, "  onFail=continue — proceeding (step marked fail in journal)\n")
			continue
		case "prompt":
			ok, perr := r.promptYesNo("continue anyway?")
			if perr == nil && ok {
				continue
			}
			fallthrough
		default: // stop
			fmt.Fprintf(r.stderr, "  journal: %s\n  修复后续跑: easyeda apply %s --resume\n",
				r.journalPath, r.pbPath)
			if !r.isReadOnly(s, catalog) {
				fmt.Fprintf(r.stderr, "  ⚠ 变更类步骤失败/超时:变更可能已生效——先读回校验,再 --resume(必要时 --from 跳过本步)\n")
			}
			return fmt.Errorf("playbook stopped at step %s (%d ok, %d skipped)", ref, okCount, skipCount)
		}
	}

	fmt.Fprintf(r.stdout, "✓ playbook %q done: %d ok, %d skipped, journal %s\n",
		r.pb.Meta.Name, okCount, skipCount, r.journalPath)
	return nil
}

// executeStep runs one step with retry/verify semantics and returns captured vars.
func (r *applyRunner) executeStep(s *playbookStep, catalog map[string]protocol.ActionSpec) (map[string]string, error) {
	timeout, retry, _ := r.policy(s)
	readOnly := r.isReadOnly(s, catalog)
	// design §错误处理-2: default retries apply to read-only steps only; an
	// explicit per-step retry is author intent and applies regardless.
	if !readOnly && s.Retry == nil {
		retry = 0
	}

	var lastErr error
	for attempt := 0; ; attempt++ {
		result, err := r.executeOnce(s, timeout)
		if err == nil {
			captured, aerr := r.captureAndAssert(s.Capture, s.Assert, result)
			if aerr == nil {
				return captured, nil
			}
			err = aerr
		}
		lastErr = err

		// verify block: did the mutation actually land?
		if s.Verify != nil {
			if ok := r.runVerify(s.Verify, timeout); ok {
				// treat as success; captures from the verify result are not
				// supported in v1 (verify is a boolean gate)
				return nil, nil
			}
		}
		if attempt >= retry {
			return nil, lastErr
		}
		backoff := 2 * time.Second
		if attempt >= 1 {
			backoff = 5 * time.Second
		}
		fmt.Fprintf(r.stderr, "  retry %d/%d in %s: %v\n", attempt+1, retry, backoff, err)
		time.Sleep(backoff)
	}
}

// executeOnce dispatches the step body once and returns the decoded JSON result.
func (r *applyRunner) executeOnce(s *playbookStep, timeout time.Duration) (any, error) {
	switch {
	case s.Notify != "":
		msg, err := substVars(s.Notify, r.vars)
		if err != nil {
			return nil, err
		}
		return r.runAction("system.notify", map[string]any{"message": msg, "type": "info"}, timeout)
	case s.Action != "":
		payload, err := substVars(s.Payload, r.vars)
		if err != nil {
			return nil, err
		}
		var pm map[string]any
		if payload != nil {
			pm, _ = payload.(map[string]any)
		}
		return r.runAction(s.Action, pm, timeout)
	case s.Run != "":
		flags, err := substVars(s.Flags, r.vars)
		if err != nil {
			return nil, err
		}
		var fm map[string]any
		if flags != nil {
			fm, _ = flags.(map[string]any)
		}
		args := make([]string, len(s.Args))
		for i, a := range s.Args {
			v, err := substVars(a, r.vars)
			if err != nil {
				return nil, err
			}
			args[i] = v.(string)
		}
		return r.runSubcommand(s.Run, fm, args)
	}
	return nil, errors.New("empty step")
}

// runAction POSTs a typed action and returns its decoded `result`.
func (r *applyRunner) runAction(action string, payload map[string]any, timeout time.Duration) (any, error) {
	respBody, err := postAction(r.cfg, action, r.window, payload, timeout)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		OK     bool           `json:"ok"`
		Result map[string]any `json:"result"`
		Error  *struct {
			Message string `json:"message"`
			Detail  string `json:"detail"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("decode %s response: %w", action, err)
	}
	if !parsed.OK {
		msg := "ok=false"
		if parsed.Error != nil {
			msg = parsed.Error.Message
			if parsed.Error.Detail != "" {
				msg += " — " + parsed.Error.Detail
			}
		}
		return nil, fmt.Errorf("%s: %s", action, msg)
	}
	return anyResult(parsed.Result), nil
}

func anyResult(m map[string]any) any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// runSubcommand re-enters the CLI in-process ("run:" steps), capturing stdout
// and best-effort parsing the trailing JSON object for capture/assert.
func (r *applyRunner) runSubcommand(run string, flags map[string]any, args []string) (any, error) {
	argv := strings.Fields(run)
	argv = append(argv, args...)
	for k, v := range flags {
		switch t := v.(type) {
		case bool:
			argv = append(argv, fmt.Sprintf("--%s=%v", k, t))
		case []any: // repeatable flag (e.g. region create --rule a --rule b)
			for _, item := range t {
				argv = append(argv, "--"+k, fmt.Sprintf("%v", item))
			}
		default:
			argv = append(argv, "--"+k, fmt.Sprintf("%v", t))
		}
	}
	// thread routing globals through
	if r.cfg.project != "" {
		argv = append(argv, "--project", r.cfg.project)
	}
	if r.window != "" && supportsWindowFlag(run) {
		argv = append(argv, "--window", r.window)
	}
	argv = append(argv, "--host", r.cfg.host, "--ports", r.cfg.ports)

	var out, errBuf strings.Builder
	code := Run(argv, &out, &errBuf)
	if !r.quiet {
		// stream the subcommand's own output, indented, for observability
		for _, line := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
			if line != "" {
				fmt.Fprintf(r.stdout, "    %s\n", line)
			}
		}
	}
	if code != 0 {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			msg = strings.TrimSpace(lastLine(out.String()))
		}
		return nil, fmt.Errorf("run %q exited %d: %s", run, code, msg)
	}
	return parseTrailingJSON(out.String()), nil
}

// supportsWindowFlag: `--window` is a per-command flag (not persistent); only
// pass it to commands known to accept it. Conservative: the action-backed
// domains accept it, the offline ones don't.
func supportsWindowFlag(run string) bool {
	for _, p := range []string{"api", "actions", "version", "audit", "daemon"} {
		if strings.HasPrefix(run, p) {
			return false
		}
	}
	return true
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) == 0 {
		return ""
	}
	return lines[len(lines)-1]
}

// parseTrailingJSON best-effort extracts the last top-level JSON object from
// mixed CLI output. Returns nil when none parses.
func parseTrailingJSON(out string) any {
	trimmed := strings.TrimSpace(out)
	var v any
	if err := json.Unmarshal([]byte(trimmed), &v); err == nil {
		return normalizeResult(v)
	}
	// scan for the last balanced {...} block
	depth, end := 0, -1
	for i := len(trimmed) - 1; i >= 0; i-- {
		switch trimmed[i] {
		case '}':
			if end < 0 {
				end = i
			}
			depth++
		case '{':
			depth--
			if depth == 0 && end >= 0 {
				if err := json.Unmarshal([]byte(trimmed[i:end+1]), &v); err == nil {
					return normalizeResult(v)
				}
				end = -1
			}
		}
	}
	return nil
}

// normalizeResult unwraps a full /action envelope ({ok,result,...}) to its
// result body so capture/assert paths are uniform across action and run steps.
func normalizeResult(v any) any {
	if m, ok := v.(map[string]any); ok {
		if _, hasOK := m["ok"]; hasOK {
			if res, hasRes := m["result"]; hasRes {
				return res
			}
		}
	}
	return v
}

// captureAndAssert evaluates assert gates then captures variables.
func (r *applyRunner) captureAndAssert(capture, assert map[string]string, result any) (map[string]string, error) {
	for path, pred := range assert {
		val, found := jsonPathLite(result, path)
		ok, detail := evalPredicate(val, found, pred)
		if !ok {
			return nil, fmt.Errorf("assert %s %s failed (%s)", path, pred, detail)
		}
	}
	if len(capture) == 0 {
		return nil, nil
	}
	captured := map[string]string{}
	for name, path := range capture {
		val, found := jsonPathLite(result, path)
		if !found {
			return nil, fmt.Errorf("capture %s: path %s not found in result", name, path)
		}
		captured[name] = formatCaptured(val)
		r.vars[name] = captured[name]
	}
	return captured, nil
}

// runVerify executes a step's verify block (read-back + assert). True = the
// original mutation is confirmed landed.
func (r *applyRunner) runVerify(v *verifyBlock, timeout time.Duration) bool {
	var result any
	var err error
	switch {
	case v.Action != "":
		payload, serr := substVars(v.Payload, r.vars)
		if serr != nil {
			return false
		}
		var pm map[string]any
		if payload != nil {
			pm, _ = payload.(map[string]any)
		}
		result, err = r.runAction(v.Action, pm, timeout)
	case v.Run != "":
		flags, serr := substVars(v.Flags, r.vars)
		if serr != nil {
			return false
		}
		var fm map[string]any
		if flags != nil {
			fm, _ = flags.(map[string]any)
		}
		result, err = r.runSubcommand(v.Run, fm, v.Args)
	default:
		return false
	}
	if err != nil {
		return false
	}
	for path, pred := range v.Assert {
		val, found := jsonPathLite(result, path)
		if ok, _ := evalPredicate(val, found, pred); !ok {
			return false
		}
	}
	fmt.Fprintf(r.stderr, "  verify passed — treating the step as landed\n")
	return true
}

func (r *applyRunner) promptYesNo(q string) (bool, error) {
	st, err := os.Stdin.Stat()
	if err != nil || (st.Mode()&os.ModeCharDevice) == 0 {
		return false, fmt.Errorf("%s — non-interactive session, pass --yes to approve", q)
	}
	fmt.Fprintf(r.stderr, "%s [y/N] ", q)
	var answer string
	_, _ = fmt.Scanln(&answer)
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes", nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
