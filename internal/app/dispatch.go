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
	"time"
)

const (
	defaultHost      = "127.0.0.1"
	defaultPortStart = 49620
	defaultPortEnd   = 49629
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
	ports   string // "49620-49629"
	project string // optional stable routing hint (project name/uuid) → windowId
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
// to read the result programmatically instead of streaming it to stdout.
type actionResult struct {
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
	res := &actionResult{OK: parsed.OK, Result: parsed.Result, Artifacts: parsed.Artifacts, Context: parsed.Context}
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

// postAction is the shared HTTP core: find a live daemon, POST the typed action,
// and return the raw response body.
func postAction(cfg *appConfig, action, window string, payload any, timeout time.Duration) ([]byte, error) {
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
