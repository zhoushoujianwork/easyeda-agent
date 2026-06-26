package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	defaultHost      = "127.0.0.1"
	defaultPortStart = 49620
	defaultPortEnd   = 49629
)

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
	portStart, portEnd, err := cfg.portRange()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	scan := scanHealth(ctx, hostPortOptions{host: cfg.host, portStart: portStart, portEnd: portEnd})
	if scan.Found == nil {
		return fmt.Errorf("no easyeda-agent daemon found on %s:%s (start it with `easyeda daemon start`)", cfg.host, scan.Ports)
	}

	body := map[string]any{"action": action}
	if window != "" {
		body["windowId"] = window
	}
	if cfg.project != "" {
		body["project"] = cfg.project
	}
	if payload != nil {
		body["payload"] = payload
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}

	url := fmt.Sprintf("http://%s:%d/action", cfg.host, scan.Found.Port)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	closeErr := resp.Body.Close()
	if readErr != nil {
		return fmt.Errorf("read response: %w", readErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close response: %w", closeErr)
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
