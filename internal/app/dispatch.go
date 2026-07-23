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
	// doc, when set (--doc <uuid|name>), PINS every mutating action to that
	// document: the daemon-choke-point guard (ensureActiveDoc) switches to it and
	// confirms via LIVE document.current BEFORE the edit dispatches, and refuses
	// rather than land the edit on whatever page happens to be foreground. This
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

	var parsed struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil || !parsed.OK {
		return errActionFailed
	}
	return nil
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
	errorMsg  string
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
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("decode %s response: %w", action, err)
	}
	res := &actionResult{ID: parsed.ID, Type: parsed.Type, Version: parsed.Version, OK: parsed.OK, Result: parsed.Result, Artifacts: parsed.Artifacts, Context: parsed.Context}
	if !parsed.OK {
		msg := "ok=false"
		if parsed.Error != nil && parsed.Error.Message != "" {
			msg = parsed.Error.Message
		}
		res.errorMsg = msg
		return res, fmt.Errorf("%s failed: %s", action, msg)
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
	WindowID         string `json:"windowId"`
	ConnectorVersion string `json:"connectorVersion"`
	Context          struct {
		ProjectUUID  string `json:"projectUuid"`
		ProjectName  string `json:"projectName"`
		DocumentUUID string `json:"documentUuid"`
		DocumentType string `json:"documentType"`
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

// postAction is the shared HTTP core: find a live daemon, POST the typed action,
// and return the raw response body.
func postAction(cfg *appConfig, action, window string, payload any, timeout time.Duration) ([]byte, error) {
	// --doc guard: pin a mutating action to the requested page first. Skipped for
	// the guard's own navigation actions (docGuardExempt) so it never recurses.
	if cfg.doc != "" && actionMutates(action) && !docGuardExempt[action] {
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
	}
	// Tell the daemon where to drop artifacts: this CLI's working directory. The
	// daemon writes them under <cwd>/.easyeda/artifacts so screenshots/exports
	// land in the user's project, not the daemon's cwd. Best-effort.
	if cwd, err := os.Getwd(); err == nil {
		body["outputDir"] = cwd
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
	Status  string          `json:"status"`
	Host    string          `json:"host"`
	Ports   string          `json:"ports"`
	Found   *daemonHealth   `json:"found,omitempty"`
	Checked []checkedHealth `json:"checked"`
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
