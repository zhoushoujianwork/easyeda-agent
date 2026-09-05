package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultHost = "127.0.0.1"
	// 0xEDA0-0xEDA9 — "EDA" spelled in hex. Deliberately NOT 49620-49629: that
	// range is what the OFFICIAL eext-run-api-gateway ecosystem scans (we had
	// copied its convention), so both sides raced to bind the same port.
	defaultPortStart = 0xeda0 // 60832
	defaultPortEnd   = 0xeda9 // 60841
)

// defaultActionTimeout bounds how long the CLI waits for a single /action
// round-trip before giving up. Most actions return well under a second; a
// hang here means the connector's underlying eda.* call never settled.
const defaultActionTimeout = 20 * time.Second

// errActionFailed is returned by dispatch when the daemon responds with
// ok=false. The response body has already been written to stdout so the
// caller must NOT print an additional error message.
var errActionFailed = errors.New("action returned ok=false")

// appConfig holds the shared host/ports settings threaded through all
// action subcommands. The fields are bound directly to Cobra persistent
// flags so they are populated before any RunE executes.
type appConfig struct {
	host    string
	ports   string // "60832-60841"
	project string // optional stable routing hint (project name/uuid) → windowId
	// forceReason, when set by a route command's --force <reason>, is attached to
	// every action request so the daemon-side workflow stage gate honors the same
	// audited override (per-run; see internal/daemon/stagegate.go).
	forceReason string
	// forceUnsafe escalates forceReason past a fully-unconfirmed mechanical
	// skeleton (issue #132) — set only by --force-unsafe.
	forceUnsafe bool
	// skipVersionCheck disarms the CLI↔daemon↔connector version consistency gate
	// (--skip-version-check, or EASYEDA_SKIP_VERSION_CHECK=1). Every bypass of a
	// BLOCKING verdict writes a `cli.version_check.skip` audit row — see
	// version_gate.go.
	skipVersionCheck bool
	// staleReadReason is the ONE-CALL write-then-read opt-in past the daemon's
	// STALE_READ gate. It is NOT bound to any flag and must never be set on the
	// shared config: the only legal way to set it is staleReadOptIn(), which
	// hands back a scoped COPY consumed by exactly one dispatch. See
	// stale_read_optin.go for why this is a per-call value rather than a second
	// process-wide force switch.
	staleReadReason string
	// forceStaleRead is the HUMAN escape hatch past the same gate
	// (--force-stale-read "<理由>"). Unlike staleReadReason it IS bound to a
	// persistent flag, so its lifetime is the whole process — that is the price
	// of a hatch a human can reach. What keeps it from becoming a second
	// forceReason is that it is not a second wire field at all: it feeds the SAME
	// narrowing predicate (staleReadForceReason → staleReadEligibleRequest), so it
	// can only ever attach to a PCB read with RequiresGate=="" and can never
	// unlock the routing stage gate. See stale_read_optin.go.
	forceStaleRead string
	// doc, when set (--doc <uuid|name>), PINS every action — mutating AND read —
	// to that document: the daemon-choke-point guard (ensureActiveDoc) switches
	// to it and confirms via LIVE document.current BEFORE the action dispatches,
	// and refuses rather than run it on whatever page happens to be foreground
	// (a read that silently returned the foreground page's data was page drift
	// in read form). This
	// is the mechanism that removes the doc-switch race — a long op (autoLayout)
	// can no longer scatter the wrong page because the foreground drifted.
	doc string
}

// portRange parses the ports string and returns (start, end, err).
func (c *appConfig) portRange() (int, int, error) {
	return parsePortRange(c.ports)
}

// dispatch finds a live daemon, POSTs the typed action, writes the raw
// response body to stdout, and returns errActionFailed when ok=false (the
// caller must return that error without printing again). Any other error
// (daemon not found, network, etc.) is a fresh error the caller may print.
func dispatch(cfg *appConfig, action, window string, payload any, stdout, stderr io.Writer) error {
	return dispatchTimed(cfg, action, window, payload, defaultActionTimeout, stdout, stderr)
}

// dispatchTimed is dispatch with a caller-chosen round-trip timeout. Use it for
// actions that should fail fast instead of hanging the full default window when
// the connector's eda.* call never settles (e.g. `sch place` with a bad uuid).
func dispatchTimed(cfg *appConfig, action, window string, payload any, timeout time.Duration, stdout, stderr io.Writer) error {
	respBody, err := postAction(cfg, action, window, payload, timeout)
	if err != nil {
		return err
	}

	_, _ = stdout.Write(respBody)
	if len(respBody) > 0 && respBody[len(respBody)-1] != '\n' {
		fmt.Fprintln(stdout)
	}
	printArtifactPaths(respBody, stderr)

	var parsed struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil || !parsed.OK {
		return errActionFailed
	}
	return nil
}

// printArtifactPaths surfaces each persisted artifact's ABSOLUTE path as one
// unmissable stderr line. The JSON already carries artifacts[].path (and the
// daemon mirrors it into result.artifactPath), but agents scanning the result
// block have repeatedly failed to locate the file — an explicit line removes
// the treasure hunt without touching the machine-readable stdout stream.
func printArtifactPaths(respBody []byte, stderr io.Writer) {
	if stderr == nil {
		return
	}
	var parsed struct {
		Artifacts []artifactRef `json:"artifacts"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return
	}
	for _, a := range parsed.Artifacts {
		if a.Path != "" {
			fmt.Fprintf(stderr, "📎 artifact saved: %s\n", a.Path)
		}
	}
}

// actionContext mirrors the live project/document context the connector attaches
// to every response (see protocol.Context). Used by the aggregating `doc`
// commands.
type actionContext struct {
	ProjectUUID  string `json:"projectUuid,omitempty"`
	ProjectName  string `json:"projectName,omitempty"`
	DocumentUUID string `json:"documentUuid,omitempty"`
	DocumentType string `json:"documentType,omitempty"`
	TabID        string `json:"tabId,omitempty"`
}

// artifactRef is the subset of a response artifact a CLI caller needs to locate
// the persisted file (the daemon fills Path after decoding the connector's
// inlineBase64). Mirrors protocol.Artifact without importing it here.
type artifactRef struct {
	Path     string `json:"path,omitempty"`
	FileName string `json:"fileName,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// saveFirstArtifact copies the response's first persisted artifact to `out`.
// The daemon already decoded the connector's inlineBase64 into its artifact
// directory and filled Path; this just puts it where the caller asked, so
// `--out` behaves like any other tool's output flag instead of making the user
// hunt through the artifact dir.
func saveFirstArtifact(res *actionResult, out string, stderr io.Writer) error {
	if res == nil || len(res.Artifacts) == 0 {
		return fmt.Errorf("the action returned no artifact to write to %s", out)
	}
	src := res.Artifacts[0].Path
	if src == "" {
		return fmt.Errorf("artifact %q has no persisted path (daemon did not decode it)", res.Artifacts[0].FileName)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read artifact %s: %w", src, err)
	}
	if dir := filepath.Dir(out); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create output dir %s: %w", dir, err)
		}
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", out, err)
	}
	abs, aerr := filepath.Abs(out)
	if aerr != nil {
		abs = out
	}
	fmt.Fprintf(stderr, "✓ wrote %s (%d bytes)\n", abs, len(data))
	return nil
}

// actionResult is the parsed form of an /action response, for callers that need
// to read the result programmatically instead of streaming it to stdout. The
// envelope fields (ID/Type/Version) are preserved so reconstruct-then-render
// commands (sch check/drc/sheet) can re-wrap their typed report in the same
// {id,type,version,ok,result} envelope the transparent commands stream (#66).
type actionResult struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"`
	Version   string         `json:"version"`
	OK        bool           `json:"ok"`
	Result    map[string]any `json:"result"`
	Artifacts []artifactRef  `json:"artifacts"`
	Context   *actionContext `json:"context"`
	// Seq is the connector's FIFO ordering evidence carried on this response
	// (connector ≥ 1.0.3). Known=false means the connector is older and sent no
	// such fields — callers MUST then fall back to a weaker judgement rather
	// than assume anything. See conn_seq.go / sch_place_adopt.go.
	Seq      schSeqCounters
	errorMsg string
	// errorCode is the daemon/connector error.code that came with ok=false
	// ("STALE_READ", "STAGE_BLOCKED", …). Callers that must branch on WHY a
	// call failed read this instead of matching on the message text.
	errorCode string
}

// requestAction POSTs a typed action and returns the parsed response without
// touching stdout. A non-nil error means the daemon was unreachable or the
// action returned ok=false (with the connector's error message attached).
func requestAction(cfg *appConfig, action, window string, payload any) (*actionResult, error) {
	return requestActionTimed(cfg, action, window, payload, defaultActionTimeout)
}

// requestActionTimed is requestAction with a caller-chosen round-trip timeout,
// for heavy actions (DRC on a real board routinely exceeds the default).
func requestActionTimed(cfg *appConfig, action, window string, payload any, timeout time.Duration) (*actionResult, error) {
	// 连接器队列被上一条 handler 堵住 → daemon 拒绝派发并明说「动作未发出、
	// 等它排空就行」。这里统一等(见 queue_blocked_retry.go):这是所有动作的
	// 唯一底层出口,--doc guard 的 pages.list 也从这里走 —— 而恢复段正是被
	// 那道 guard 挡在门外的(2026-08-26 U2 七条连接因此丢失)。
	var res *actionResult
	err := retryWhileQueueBlocked(action, func() error {
		var e error
		res, e = requestActionOnce(cfg, action, window, payload, timeout)
		return e
	}, queueBlockRetryPolicy{Stderr: queueWaitProgress})
	return res, err
}

// queueWaitProgress 是「正在等队列排空」的进度出口。默认 stderr:静默地等
// 90 秒比直接失败更让人困惑,用户得看得见工具在等什么、还要等多久。
var queueWaitProgress io.Writer = os.Stderr

// requestActionOnce 是单次往返(不含队列阻塞等待)。
func requestActionOnce(cfg *appConfig, action, window string, payload any, timeout time.Duration) (*actionResult, error) {
	respBody, err := postAction(cfg, action, window, payload, timeout)
	if err != nil {
		return nil, err
	}

	var parsed struct {
		ID        string         `json:"id"`
		Type      string         `json:"type"`
		Version   string         `json:"version"`
		OK        bool           `json:"ok"`
		Result    map[string]any `json:"result"`
		Artifacts []artifactRef  `json:"artifacts"`
		Context   *actionContext `json:"context"`
		Error     *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("decode %s response: %w", action, err)
	}
	res := &actionResult{ID: parsed.ID, Type: parsed.Type, Version: parsed.Version, OK: parsed.OK, Result: parsed.Result, Artifacts: parsed.Artifacts, Context: parsed.Context, Seq: parseSeqCounters(respBody)}
	if !parsed.OK {
		msg := "ok=false"
		code := ""
		if parsed.Error != nil {
			code = parsed.Error.Code
			if parsed.Error.Message != "" {
				msg = parsed.Error.Message
			}
		}
		res.errorMsg = msg
		res.errorCode = code
		// 结构化错误(actionError):文本与旧版逐字一致,额外带上 error.code,
		// 于是调用方能把「机械门拦下的」(STALE_READ)和「真的读失败了」分开处理
		// —— 两者的下一步完全不同。见 stale_read_optin.go。
		return res, &actionError{Action: action, Code: code, Message: msg}
	}
	return res, nil
}

// encodeResultEnvelope writes a reconstructed typed report wrapped in the same
// {id,type,version,ok,result} envelope the transparent (stdout-streaming)
// commands emit, so `sch check/drc/sheet --json` are consistent with `sch
// list/read/place` and a uniform-envelope parser reading result.* works across
// all of them (#66). The envelope metadata is taken from the daemon's response
// (res); ok mirrors res.OK.
func encodeResultEnvelope(res *actionResult, report any, stdout io.Writer) error {
	env := map[string]any{
		"ok":     res.OK,
		"result": report,
	}
	if res.ID != "" {
		env["id"] = res.ID
	}
	if res.Type != "" {
		env["type"] = res.Type
	}
	if res.Version != "" {
		env["version"] = res.Version
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(env)
}

// dispatchCapture runs an action like dispatch (streaming the raw response to
// stdout, preserving the exact output shape) but also returns the parsed result
// so the caller can post-process artifacts. The streamed bytes are unchanged;
// callers read res.Artifacts for the persisted file path.
func dispatchCapture(cfg *appConfig, action, window string, payload any, stdout io.Writer) (*actionResult, error) {
	respBody, err := postAction(cfg, action, window, payload, defaultActionTimeout)
	if err != nil {
		return nil, err
	}

	_, _ = stdout.Write(respBody)
	if len(respBody) > 0 && respBody[len(respBody)-1] != '\n' {
		fmt.Fprintln(stdout)
	}
	printArtifactPaths(respBody, os.Stderr)

	var parsed struct {
		OK        bool           `json:"ok"`
		Result    map[string]any `json:"result"`
		Artifacts []artifactRef  `json:"artifacts"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil || !parsed.OK {
		return nil, errActionFailed
	}
	return &actionResult{OK: parsed.OK, Result: parsed.Result, Artifacts: parsed.Artifacts}, nil
}

// healthWindow is the subset of a /health window entry the doc commands need to
// resolve a routing target.
type healthWindow struct {
	WindowID         string    `json:"windowId"`
	ConnectorVersion string    `json:"connectorVersion"`
	ConnectedAt      time.Time `json:"connectedAt"`
	Context          struct {
		ProjectUUID  string `json:"projectUuid"`
		ProjectName  string `json:"projectName"`
		DocumentUUID string `json:"documentUuid"`
		DocumentType string `json:"documentType"`
		TabID        string `json:"tabId"`
	} `json:"context"`
}

// listWindows scans for the live daemon and returns its connected windows.
func listWindows(cfg *appConfig) ([]healthWindow, error) {
	portStart, portEnd, err := cfg.portRange()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	scan := scanHealth(ctx, hostPortOptions{host: cfg.host, portStart: portStart, portEnd: portEnd})
	if scan.Found == nil {
		return nil, fmt.Errorf("no easyeda-agent daemon found on %s:%s (start it with `easyeda daemon start`)", cfg.host, scan.Ports)
	}
	var parsed struct {
		Windows []healthWindow `json:"windows"`
	}
	if err := json.Unmarshal(scan.Found.Raw, &parsed); err != nil {
		return nil, fmt.Errorf("parse health windows: %w", err)
	}
	return parsed.Windows, nil
}

// resolveTargetWindow picks the single window a multi-call command (e.g. `doc
// ls`/`doc switch`) should act on and returns its concrete windowId, so every
// sub-call pins to that id — immune to a second window appearing or the
// single-window auto-target racing mid-command. An explicit --window wins; then
// --project; else the sole connected window. Ambiguity is a hard error naming
// the fix.
func resolveTargetWindow(cfg *appConfig, window string) (string, error) {
	if window != "" {
		return window, nil
	}
	windows, err := listWindows(cfg)
	if err != nil {
		return "", err
	}
	return selectWindow(windows, cfg.project, window)
}

// selectWindow is the pure routing rule resolveTargetWindow applies once a window
// list is in hand: explicit --window wins; then --project (exactly one match);
// else the sole connected window. Ambiguity / no-match is a hard error naming the
// fix. Kept separate from the HTTP scan so it is unit-testable.
func selectWindow(windows []healthWindow, project, window string) (string, error) {
	if window != "" {
		return window, nil
	}
	if project != "" {
		var matches []healthWindow
		for _, w := range windows {
			if w.Context.ProjectName == project || w.Context.ProjectUUID == project {
				matches = append(matches, w)
			}
		}
		switch len(matches) {
		case 1:
			return matches[0].WindowID, nil
		case 0:
			return "", fmt.Errorf("no connected window for project %q (run `easyeda daemon health`)", project)
		default:
			// Extension reloads can leave several registrations for the exact same
			// project/document tab. They are transport duplicates; route to the
			// newest registration and let daemon TTL cleanup retire the rest.
			if id, ok := newestDuplicateWindow(matches); ok {
				return id, nil
			}
			return "", fmt.Errorf("project %q maps to %d windows — pass --window <id>", project, len(matches))
		}
	}
	switch len(windows) {
	case 1:
		return windows[0].WindowID, nil
	case 0:
		return "", fmt.Errorf("no EasyEDA connector is available")
	default:
		return "", fmt.Errorf("%d windows connected — pass --project <name> or --window <id>", len(windows))
	}
}

func newestDuplicateWindow(matches []healthWindow) (string, bool) {
	if len(matches) < 2 {
		return "", false
	}
	first := matches[0]
	if first.Context.ProjectUUID == "" || first.Context.DocumentUUID == "" || first.Context.DocumentType == "" || first.Context.TabID == "" {
		return "", false
	}
	newest := first
	for _, w := range matches[1:] {
		if w.Context.ProjectUUID != first.Context.ProjectUUID || w.Context.DocumentUUID != first.Context.DocumentUUID || w.Context.DocumentType != first.Context.DocumentType || w.Context.TabID != first.Context.TabID {
			return "", false
		}
		if w.ConnectedAt.After(newest.ConnectedAt) {
			newest = w
		}
	}
	return newest.WindowID, true
}

// cliClientID identifies this CLI process to the daemon, computed once per
// process: "<hostname>:<pid>", plus an optional session label from
// EASYEDA_CLIENT_LABEL (e.g. "mikas-mbp:12345:e2e-regression"). The daemon
// stamps it into every audit entry (pid = precise per-process attribution) and
// uses it to detect a different client writing to the same window
// (concurrentWriter advisory, issue #108). NOTE: the advisory compares SESSION
// identity — hostname+label, or hostname alone when unlabeled — because the
// pid churns on every one-shot CLI invocation; set EASYEDA_CLIENT_LABEL per
// agent/session to make same-host concurrent writers detectable.
var cliClientID = sync.OnceValue(func() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown-host"
	}
	id := fmt.Sprintf("%s:%d", host, os.Getpid())
	if label := os.Getenv("EASYEDA_CLIENT_LABEL"); label != "" {
		id += ":" + label
	}
	return id
})

// staleRiskSeen deduplicates stale-read warnings within one CLI invocation:
// composite commands (pcb check, report …) fire many read actions and would
// otherwise repeat the identical advisory for each. Daemon messages are
// timestamp-free precisely so identical risks collapse here.
var (
	staleRiskMu   sync.Mutex
	staleRiskSeen = map[string]bool{}
)

// warnStaleRisk surfaces a daemon-attached staleRisk advisory (PCB read after
// an un-reloaded PCB mutation — SKILL iron rule 5) on STDERR, so JSON/table
// output on stdout stays machine-parseable. Best-effort and non-blocking.
func warnStaleRisk(respBody []byte, stderr io.Writer) {
	var parsed struct {
		StaleRisk string `json:"staleRisk"`
	}
	if json.Unmarshal(respBody, &parsed) != nil || parsed.StaleRisk == "" {
		return
	}
	staleRiskMu.Lock()
	seen := staleRiskSeen[parsed.StaleRisk]
	staleRiskSeen[parsed.StaleRisk] = true
	staleRiskMu.Unlock()
	if seen {
		return
	}
	fmt.Fprintf(stderr, "⚠ staleRisk: %s\n", parsed.StaleRisk)
}

// concurrentWriterSeen deduplicates concurrent-writer warnings within one CLI
// invocation, keyed by the last writer's identity+action (the "N seconds ago"
// part churns per action, so the raw message would never collapse). Same
// rationale as staleRiskSeen: composite commands fire many actions.
var (
	concurrentWriterMu   sync.Mutex
	concurrentWriterSeen = map[string]bool{}
)

// warnConcurrentWriter surfaces a daemon-attached concurrentWriter advisory
// (another client mutated this window recently — issue #108) on STDERR, so
// JSON/table output on stdout stays machine-parseable. Best-effort and
// non-blocking, aligned with warnStaleRisk.
func warnConcurrentWriter(respBody []byte, stderr io.Writer) {
	var parsed struct {
		ConcurrentWriter string `json:"concurrentWriter"`
	}
	if json.Unmarshal(respBody, &parsed) != nil || parsed.ConcurrentWriter == "" {
		return
	}
	// Dedup on the stable tail ("… last writer <id> ran <action>"); fall back
	// to the whole message when the marker is absent.
	key := parsed.ConcurrentWriter
	if i := strings.Index(key, "last writer "); i >= 0 {
		key = key[i:]
	}
	concurrentWriterMu.Lock()
	seen := concurrentWriterSeen[key]
	concurrentWriterSeen[key] = true
	concurrentWriterMu.Unlock()
	if seen {
		return
	}
	fmt.Fprintf(stderr, "⚠ concurrentWriter: %s\n", parsed.ConcurrentWriter)
}

// responseWarningsSeen deduplicates connector-attached response warnings within
// one CLI invocation, aligned with staleRiskSeen (composite commands fire many
// actions and would repeat identical advisories).
var (
	responseWarningsMu   sync.Mutex
	responseWarningsSeen = map[string]bool{}
)

// warnResponseWarnings surfaces connector-attached top-level response warnings
// (e.g. a partial property application on schematic.component.modify, issue
// #151, or the rebind "new primitiveId" advisory) on STDERR, so JSON/table
// output on stdout stays machine-parseable. Best-effort and non-blocking,
// aligned with warnStaleRisk.
func warnResponseWarnings(respBody []byte, stderr io.Writer) {
	var parsed struct {
		Warnings []string `json:"warnings"`
	}
	if json.Unmarshal(respBody, &parsed) != nil || len(parsed.Warnings) == 0 {
		return
	}
	for _, w := range parsed.Warnings {
		if w == "" {
			continue
		}
		responseWarningsMu.Lock()
		seen := responseWarningsSeen[w]
		responseWarningsSeen[w] = true
		responseWarningsMu.Unlock()
		if seen {
			continue
		}
		fmt.Fprintf(stderr, "⚠ %s\n", w)
	}
}

// ── --doc guard: pin mutating actions to a chosen document ───────────────────
//
// Every action that MOVES/creates/deletes primitives operates on whatever
// document is foreground. doc switch is async, so a long op (autoLayout, ~2min)
// or a follow-up command could land its edit on the WRONG page after the
// foreground drifted — the real cause of the 2026-07-20 P1/P2 thrash. `--doc`
// removes that class of bug MECHANICALLY: before a mutating action dispatches,
// ensureActiveDoc switches to the requested page and confirms it via LIVE
// document.current, refusing rather than editing the wrong page.

// docGuardCatalog caches the typed-action catalog for the mutating-action lookup.
var docGuardCatalog = sync.OnceValue(actionCatalog)

// actionMutates reports whether an action edits the document (drives the guard).
func actionMutates(action string) bool {
	spec, ok := docGuardCatalog()[action]
	return ok && spec.Mutates
}

// docGuardExempt are actions the guard must NEVER gate: its own navigation/read
// tools. They are non-mutating today (so the guard skips them anyway), but the
// explicit set keeps a future Mutates flip from causing infinite recursion.
var docGuardExempt = map[string]bool{
	"document.current": true, "document.open": true, "schematic.page.open": true,
	"schematic.pages.list": true, "pcb.documents.list": true,
	// Daemon-local; touches no document, so pinning a page for it is meaningless.
	"system.health": true,
}

// docGuardApplies reports whether the --doc guard must run before dispatching
// action. It gates EVERY action when --doc is set — mutations so the edit can
// never land on a drifted foreground page, and reads because accepting --doc
// while silently returning the FOREGROUND page's data is the same page-drift
// bug in read form (reads used to skip the guard entirely). Only the guard's
// own navigation/read tools (docGuardExempt) are excluded, or ensureActiveDoc
// would recurse through itself.
func docGuardApplies(doc, action string) bool {
	return doc != "" && !docGuardExempt[action]
}

// ensureActiveDoc makes cfg.doc the active document before a mutating action.
// No-op when --doc is unset. Verifies via LIVE document.current (never the
// cached /health snapshot, which is what fooled the hand-rolled checks), and
// returns an error rather than proceed on the wrong page.
func ensureActiveDoc(cfg *appConfig, window string) error {
	if cfg.doc == "" {
		return nil
	}
	docs, activeUUID, rw, err := discoverDocs(cfg, window)
	if err != nil {
		return fmt.Errorf("--doc guard: %w", err)
	}
	target, err := resolveDoc(docs, cfg.doc)
	if err != nil {
		return fmt.Errorf("--doc %q: %w", cfg.doc, err)
	}
	if activeUUID == target.UUID {
		return nil
	}
	for i := 0; i < 6; i++ {
		if _, oerr := requestAction(cfg, "document.open", rw, map[string]any{"uuid": target.UUID}); oerr != nil {
			return fmt.Errorf("--doc guard: open %s: %w", target.Name, oerr)
		}
		time.Sleep(1200 * time.Millisecond)
		cur, cerr := requestAction(cfg, "document.current", rw, nil)
		if cerr == nil && cur.Context != nil && cur.Context.DocumentUUID == target.UUID {
			return nil
		}
	}
	return fmt.Errorf("--doc %q: could not confirm it is the active page after retries — refusing to run a mutating action on the wrong page", cfg.doc)
}

// printCascadeCleanup surfaces a delete response's cascaded cleanup (ADR-0004
// Decision 5: component delete removes its exclusive stub trees + riding flags)
// as one human-readable stderr line. No-op when the response has no cascaded
// block — safe to wire into any delete renderer.
func printCascadeCleanup(res *actionResult, stderr io.Writer) {
	if res == nil || res.Result == nil || stderr == nil {
		return
	}
	c, ok := res.Result["cascaded"].(map[string]any)
	if !ok {
		return
	}
	count := func(key string) int {
		arr, ok := c[key].([]any)
		if !ok {
			return 0
		}
		return len(arr)
	}
	wires, flags := count("wires"), count("flags")
	if wires == 0 && flags == 0 {
		return
	}
	fmt.Fprintf(stderr, "级联清理 %d 桩线 %d 旗\n", wires, flags)
}

// ── dry-run 纯计算铁律 (ADR-0004 Decision 4) ─────────────────────────────────
//
// `--dry-run`(或默认 dry-run)的路径必须是纯计算:可以读,但绝不允许发出任何
// Mutates=true 的 action。#181 实证「靠自觉」不成立(有 dry-run 真落件的路径),
// 所以在 CLI 派发层机械保证:标志开启期间,postAction 对 mutating action 直接
// 拒绝——想在 dry-run 里偷偷落件的新代码第一次跑就炸,而不是让用户在画布上
// 发现幽灵件。

var (
	dispatchDryRunMu sync.Mutex
	dispatchDryRunOn bool
)

// setDispatchDryRun switches the process-wide dry-run purity guard and returns
// the restore func. Idiomatic wiring at the top of a dry-run branch:
//
//	if dryRun {
//		defer setDispatchDryRun(true)()
//	}
//
// Restore semantics (save previous value) make nested wiring safe — e.g.
// route-critical --dry-run calling runPowerPlanes(dryRun=true).
func setDispatchDryRun(on bool) (restore func()) {
	dispatchDryRunMu.Lock()
	prev := dispatchDryRunOn
	dispatchDryRunOn = on
	dispatchDryRunMu.Unlock()
	return func() {
		dispatchDryRunMu.Lock()
		dispatchDryRunOn = prev
		dispatchDryRunMu.Unlock()
	}
}

// dispatchDryRunActive reports whether the dry-run purity guard is set.
func dispatchDryRunActive() bool {
	dispatchDryRunMu.Lock()
	defer dispatchDryRunMu.Unlock()
	return dispatchDryRunOn
}

// dryRunGuard rejects a Mutates=true action while the dry-run purity flag is
// set. Reads pass through untouched. NOTE: debug.exec_js is catalogued
// Mutates=true, so an exec_js READ inside a guarded dry-run branch is rejected
// too — best-effort probes (e.g. warnSchGroupsPresent) degrade gracefully;
// hard-required exec_js reads mean that dry-run path cannot be wired until the
// read is a typed action (runOfficialAutolayout is the known case).
func dryRunGuard(action string) error {
	if !dispatchDryRunActive() || !actionMutates(action) {
		return nil
	}
	return fmt.Errorf("dry-run 模式禁止 mutating action %q —— dry-run 必须纯计算不落画布;这是 bug,请报 issue (ADR-0004 Decision 4)", action)
}

// artifactOutputDir resolves the outputDir sent with every action request (the
// directory the daemon roots its .easyeda/artifacts tree under). Thin os.Getwd
// wrapper around the pure, unit-tested resolveOutputDir.
func artifactOutputDir() (string, bool) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	return resolveOutputDir(cwd, dirHasProjectMarker), true
}

// resolveOutputDir is the pure core: strip any .easyeda/artifacts nesting from
// cwd (a cwd INSIDE the artifact tree must not seed another level), then anchor
// to the nearest enclosing project root (isRoot marker walk-up); when no marker
// is found, fall back to the stripped cwd.
func resolveOutputDir(cwd string, isRoot func(string) bool) string {
	dir := stripArtifactNesting(cwd)
	for d := dir; ; {
		if isRoot(d) {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	return dir
}

// dirHasProjectMarker reports whether dir looks like a project root (.git or
// go.mod present) — the anchor for artifactOutputDir.
func dirHasProjectMarker(dir string) bool {
	for _, marker := range []string{".git", "go.mod"} {
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			return true
		}
	}
	return false
}

// stripArtifactNesting truncates a path to just BEFORE its first
// ".easyeda/artifacts" segment pair, so that Join(dir, ".easyeda", "artifacts")
// is idempotent however deeply the input had already nested. A path without the
// pair is returned cleaned but otherwise unchanged. The daemon applies the same
// normalization defensively (internal/daemon dispatch.go) — keep both in sync.
func stripArtifactNesting(p string) string {
	clean := filepath.Clean(p)
	sep := string(filepath.Separator)
	segs := strings.Split(clean, sep)
	for i := 0; i+1 < len(segs); i++ {
		if segs[i] == ".easyeda" && segs[i+1] == "artifacts" {
			trimmed := strings.Join(segs[:i], sep)
			if trimmed == "" {
				if filepath.IsAbs(clean) {
					return sep
				}
				return "."
			}
			return trimmed
		}
	}
	return clean
}

// postAction is the shared HTTP core: find a live daemon, POST the typed action,
// and return the raw response body.
func postAction(cfg *appConfig, action, window string, payload any, timeout time.Duration) ([]byte, error) {
	// dry-run 纯计算铁律 (ADR-0004 Decision 4): while the process-wide dry-run
	// flag is set, a Mutates=true action is refused HERE — before any network
	// traffic — so no dry-run path can ever write the canvas.
	if err := dryRunGuard(action); err != nil {
		return nil, err
	}

	// --doc guard: pin the action (mutating OR read — see docGuardApplies) to
	// the requested page first. Skipped for the guard's own navigation actions
	// (docGuardExempt) so it never recurses.
	if docGuardApplies(cfg.doc, action) {
		if err := ensureActiveDoc(cfg, window); err != nil {
			return nil, err
		}
	}

	portStart, portEnd, err := cfg.portRange()
	if err != nil {
		return nil, err
	}

	if timeout <= 0 {
		timeout = defaultActionTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	scan := scanHealth(ctx, hostPortOptions{host: cfg.host, portStart: portStart, portEnd: portEnd})
	if scan.Found == nil {
		return nil, fmt.Errorf("no easyeda-agent daemon found on %s:%s (start it with `easyeda daemon start`)", cfg.host, scan.Ports)
	}

	// 版本一致性门(issue #181):CLI / daemon / connector 错位会让**后续每一条
	// 排查都染上噪音**(改好的 bug 在旧 daemon 上照样复现)。判据用的就是上面这
	// 次 /health 扫描的报文 —— 零额外往返;每进程只判一次;`easyeda health` /
	// `version` / `update` 不走这条路,所以诊断与修复路径不会被自己拦死。
	if err := checkVersionGate(cfg, scan.Found.Raw, os.Stderr); err != nil {
		return nil, err
	}

	body := map[string]any{"action": action}
	// Identify this client process for audit attribution and the daemon's
	// concurrent-writer advisory (issue #108).
	body["clientId"] = cliClientID()
	// Send the round-trip budget: the daemon shortens its connector wait to
	// (budget - grace) so it answers with a structured DISPATCH_FAILED *before*
	// this HTTP client times out — instead of both sides hanging to their own
	// independent deadlines.
	body["timeoutMs"] = int(timeout / time.Millisecond)
	if window != "" {
		body["windowId"] = window
	}
	if cfg.project != "" {
		body["project"] = cfg.project
	}
	if cfg.forceReason != "" {
		body["forceReason"] = cfg.forceReason
		if cfg.forceUnsafe {
			body["forceUnsafe"] = true
		}
	} else if reason := staleReadForceReason(cfg, action, payload); reason != "" {
		// 写后回读放行位(stale_read_optin.go)。只在这一个咽喉上落到线上,并且
		// 只对「PCB 域 + 不改画布 + 不受布线门管辖」的动作生效 —— 所以它不可能
		// 顺带解锁 CheckRouteGate。daemon 收到后自己写 daemon.stale_read.force
		// 审计行,app 侧不另造格式。
		//
		// 显式排在 forceReason 之后:人手敲的 `--force <理由>`(布线阶段门)语义更强,
		// 不该被一个自动放行位覆盖掉(也不该把 forceUnsafe 带上 —— 那是布线门的东西)。
		// 人手敲的 STALE_READ 逃生口是 --force-stale-read,它走的正是下面这个函数。
		body["forceReason"] = reason
	}
	// Tell the daemon where to drop artifacts. Anchored to the project root
	// (nearest .git/go.mod ancestor), falling back to cwd — and NEVER a path
	// inside an existing .easyeda/artifacts tree: sending a raw cwd that had
	// drifted into the artifact dir made the daemon Join another
	// .easyeda/artifacts under it, recursively nesting the tree. Best-effort.
	if dir, ok := artifactOutputDir(); ok {
		body["outputDir"] = dir
	}
	if payload != nil {
		body["payload"] = payload
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}

	url := fmt.Sprintf("http://%s:%d/action", cfg.host, scan.Found.Port)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	closeErr := resp.Body.Close()
	if readErr != nil {
		return nil, fmt.Errorf("read response: %w", readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close response: %w", closeErr)
	}
	// Surface a daemon stale-read advisory here — the one choke point all
	// dispatch paths (dispatch/dispatchCapture/requestAction) share — so every
	// command warns without per-command wiring. stderr keeps stdout clean.
	warnStaleRisk(respBody, os.Stderr)
	// Same choke point for the concurrent-writer advisory (issue #108).
	warnConcurrentWriter(respBody, os.Stderr)
	// And for connector-attached warnings (partial property application #151,
	// rebind re-place advisory, …) — visible without per-command wiring.
	warnResponseWarnings(respBody, os.Stderr)
	// Record the connector's FIFO ordering counters at the SAME choke point, so
	// any later judgement has a baseline without every command threading one
	// through by hand (conn_seq.go). Read-only bookkeeping; never fails a call.
	connSeqObserve(window, cfg.project, respBody)
	return respBody, nil
}

// parsePortRange parses "start-end" into two ints.
func parsePortRange(raw string) (int, int, error) {
	parts := strings.Split(raw, "-")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid port range %q, expected start-end", raw)
	}
	start, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid start port %q", parts[0])
	}
	end, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid end port %q", parts[1])
	}
	if start <= 0 || end <= 0 || start > end {
		return 0, 0, fmt.Errorf("invalid port range %q", raw)
	}
	return start, end, nil
}

// ── health scan types ──────────────────────────────────────────────────────

type hostPortOptions struct {
	host      string
	portStart int
	portEnd   int
}

type healthResult struct {
	Status string        `json:"status"`
	Host   string        `json:"host"`
	Ports  string        `json:"ports"`
	Found  *daemonHealth `json:"found,omitempty"`
	// VersionGate is the CLI↔daemon↔connector consistency verdict, filled in by
	// the `health` command (not by scanHealth — the dispatch path computes its
	// own copy). It is the same judgement that refuses a mismatched dispatch,
	// so `health` answers "will my next command be blocked, and how do I fix
	// it" without the user having to trip the gate first.
	VersionGate *versionGateReport `json:"versionGate,omitempty"`
	Checked     []checkedHealth    `json:"checked"`
}

type checkedHealth struct {
	Port   int    `json:"port"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type daemonHealth struct {
	Port    int             `json:"port"`
	Service string          `json:"service,omitempty"`
	Raw     json.RawMessage `json:"raw,omitempty"`
}

// scanHealth probes each port in [portStart, portEnd] and returns the first
// port that responds with service=="easyeda-agent".
func scanHealth(ctx context.Context, opts hostPortOptions) healthResult {
	result := healthResult{
		Status: "not_found",
		Host:   opts.host,
		Ports:  fmt.Sprintf("%d-%d", opts.portStart, opts.portEnd),
	}

	client := http.Client{Timeout: 700 * time.Millisecond}
	for port := opts.portStart; port <= opts.portEnd; port++ {
		checked := checkedHealth{Port: port, Status: "unreachable"}
		url := fmt.Sprintf("http://%s:%d/health", opts.host, port)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			checked.Error = err.Error()
			result.Checked = append(result.Checked, checked)
			continue
		}

		resp, err := client.Do(req)
		if err != nil {
			checked.Error = err.Error()
			result.Checked = append(result.Checked, checked)
			continue
		}

		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		closeErr := resp.Body.Close()
		if readErr != nil {
			checked.Status = "read_error"
			checked.Error = readErr.Error()
			result.Checked = append(result.Checked, checked)
			continue
		}
		if closeErr != nil {
			checked.Status = "close_error"
			checked.Error = closeErr.Error()
			result.Checked = append(result.Checked, checked)
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			checked.Status = fmt.Sprintf("http_%d", resp.StatusCode)
			result.Checked = append(result.Checked, checked)
			continue
		}

		svc := serviceName(body)
		checked.Status = "ok"
		result.Checked = append(result.Checked, checked)
		if svc == "easyeda-agent" {
			raw := append(json.RawMessage(nil), body...)
			result.Status = "found"
			result.Found = &daemonHealth{Port: port, Service: svc, Raw: raw}
			return result
		}
	}

	return result
}

func serviceName(body []byte) string {
	var payload struct {
		Service string `json:"service"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	return payload.Service
}
