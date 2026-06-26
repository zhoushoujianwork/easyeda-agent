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
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/zhoushoujianwork/easyeda-agent/internal/daemon"
	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
	"github.com/zhoushoujianwork/easyeda-agent/internal/version"
)

const (
	defaultHost      = "127.0.0.1"
	defaultPortStart = 49620
	defaultPortEnd   = 49629
)

func Run(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stdout)
		return 0
	}

	switch args[0] {
	case "help", "-h", "--help":
		printUsage(stdout)
		return 0
	case "version":
		fmt.Fprintf(stdout, "%s %s\n", version.Name, version.Version)
		return 0
	case "phase1":
		printPhase1(stdout)
		return 0
	case "actions":
		return printActions(stdout, stderr)
	case "health":
		return health(args[1:], stdout, stderr)
	case "daemon":
		return runDaemon(args[1:], stdout, stderr)
	case "call":
		return runCall(args[1:], stdout, stderr)
	case "audit":
		return runAudit(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintf(w, `%s

Usage:
  easyeda version
  easyeda phase1
  easyeda actions
  easyeda health [--host 127.0.0.1] [--ports 49620-49629]
  easyeda daemon [--host 127.0.0.1] [--ports 49620-49629]
  easyeda call <action> [--window id] [--payload '{...}'] [--host 127.0.0.1] [--ports 49620-49629]
  easyeda audit tail [-n N] [--dir <dir>]   show recent dispatches from the JSONL log

`, version.Name)
}

func printPhase1(w io.Writer) {
	fmt.Fprintln(w, "Phase 1: schematic automation")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Goals:")
	fmt.Fprintln(w, "  - connect to an active EasyEDA schematic window")
	fmt.Fprintln(w, "  - inspect project, document, pages, components, wires, and selections")
	fmt.Fprintln(w, "  - place and modify schematic components")
	fmt.Fprintln(w, "  - create wires, net flags, and ports")
	fmt.Fprintln(w, "  - run DRC, save, export netlist/BOM, and capture snapshots")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Out of scope: PCB editing, footprint authoring, manufacturing export beyond schematic BOM/netlist.")
}

func printActions(stdout io.Writer, stderr io.Writer) int {
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(protocol.AllActions()); err != nil {
		fmt.Fprintf(stderr, "encode actions: %v\n", err)
		return 1
	}
	return 0
}

// daemonPIDFile returns the path where the daemon records its PID.
func daemonPIDFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".easyeda-agent", "daemon.pid")
}

// killExistingDaemon reads the PID file and terminates any running daemon so
// the new one can bind the preferred port (49620) instead of spilling to the
// next one.
func killExistingDaemon(log io.Writer) {
	pidFile := daemonPIDFile()
	if pidFile == "" {
		return
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		_ = os.Remove(pidFile)
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		_ = os.Remove(pidFile)
		return
	}
	// Signal 0 checks liveness without sending a real signal.
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		_ = os.Remove(pidFile)
		return
	}
	fmt.Fprintf(log, "easyeda-agent: killing existing daemon (pid %d)\n", pid)
	_ = proc.Signal(syscall.SIGTERM)
	for range 20 {
		time.Sleep(100 * time.Millisecond)
		if proc.Signal(syscall.Signal(0)) != nil {
			break
		}
	}
	_ = proc.Signal(syscall.SIGKILL)
	_ = os.Remove(pidFile)
	time.Sleep(200 * time.Millisecond) // let the OS release the port
}

// writeDaemonPID writes our PID to the PID file and returns a cleanup func
// that removes it on exit.
func writeDaemonPID(log io.Writer) func() {
	pidFile := daemonPIDFile()
	if pidFile == "" {
		return func() {}
	}
	if err := os.MkdirAll(filepath.Dir(pidFile), 0755); err != nil {
		fmt.Fprintf(log, "easyeda-agent: create pid dir: %v\n", err)
		return func() {}
	}
	if err := os.WriteFile(pidFile, fmt.Appendf(nil, "%d\n", os.Getpid()), 0644); err != nil {
		fmt.Fprintf(log, "easyeda-agent: write pid file: %v\n", err)
		return func() {}
	}
	return func() { _ = os.Remove(pidFile) }
}

func runDaemon(args []string, stdout io.Writer, stderr io.Writer) int {
	opts, err := parseHostPortOptions(args)
	if err != nil {
		fmt.Fprintf(stderr, "daemon: %v\n", err)
		return 2
	}

	killExistingDaemon(stdout)
	cleanup := writeDaemonPID(stdout)
	defer cleanup()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srv := daemon.New(daemon.Options{
		Host:      opts.host,
		PortStart: opts.portStart,
		PortEnd:   opts.portEnd,
		Version:   version.Version,
	})
	if err := srv.Run(ctx, stdout); err != nil {
		fmt.Fprintf(stderr, "daemon: %v\n", err)
		return 1
	}
	return 0
}

type callOptions struct {
	action    string
	window    string
	payload   string
	host      string
	portStart int
	portEnd   int
}

func runCall(args []string, stdout io.Writer, stderr io.Writer) int {
	opts, err := parseCallOptions(args)
	if err != nil {
		fmt.Fprintf(stderr, "call: %v\n", err)
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	scan := scanHealth(ctx, hostPortOptions{host: opts.host, portStart: opts.portStart, portEnd: opts.portEnd})
	if scan.Found == nil {
		fmt.Fprintf(stderr, "call: no easyeda-agent daemon found on %s:%s (start it with `easyeda daemon`)\n", opts.host, scan.Ports)
		return 1
	}

	body := map[string]any{"action": opts.action}
	if opts.window != "" {
		body["windowId"] = opts.window
	}
	if opts.payload != "" {
		var payload map[string]any
		if err := json.Unmarshal([]byte(opts.payload), &payload); err != nil {
			fmt.Fprintf(stderr, "call: invalid --payload json: %v\n", err)
			return 2
		}
		body["payload"] = payload
	}

	buf, err := json.Marshal(body)
	if err != nil {
		fmt.Fprintf(stderr, "call: encode request: %v\n", err)
		return 1
	}

	url := fmt.Sprintf("http://%s:%d/action", opts.host, scan.Found.Port)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		fmt.Fprintf(stderr, "call: build request: %v\n", err)
		return 1
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "call: %v\n", err)
		return 1
	}
	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	closeErr := resp.Body.Close()
	if readErr != nil {
		fmt.Fprintf(stderr, "call: read response: %v\n", readErr)
		return 1
	}
	if closeErr != nil {
		fmt.Fprintf(stderr, "call: close response: %v\n", closeErr)
		return 1
	}

	stdout.Write(respBody)
	if len(respBody) > 0 && respBody[len(respBody)-1] != '\n' {
		fmt.Fprintln(stdout)
	}

	var parsed struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil || !parsed.OK {
		return 1
	}
	return 0
}

func parseCallOptions(args []string) (callOptions, error) {
	opts := callOptions{
		host:      defaultHost,
		portStart: defaultPortStart,
		portEnd:   defaultPortEnd,
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--window":
			i++
			if i >= len(args) {
				return opts, errors.New("--window requires a value")
			}
			opts.window = args[i]
		case "--payload":
			i++
			if i >= len(args) {
				return opts, errors.New("--payload requires a value")
			}
			opts.payload = args[i]
		case "--host":
			i++
			if i >= len(args) {
				return opts, errors.New("--host requires a value")
			}
			opts.host = args[i]
		case "--ports":
			i++
			if i >= len(args) {
				return opts, errors.New("--ports requires a value")
			}
			start, end, err := parsePortRange(args[i])
			if err != nil {
				return opts, err
			}
			opts.portStart = start
			opts.portEnd = end
		default:
			if strings.HasPrefix(arg, "-") {
				return opts, fmt.Errorf("unknown call option: %s", arg)
			}
			if opts.action != "" {
				return opts, fmt.Errorf("unexpected argument: %s", arg)
			}
			opts.action = arg
		}
	}

	if opts.action == "" {
		return opts, errors.New("action name is required, e.g. `easyeda call system.health`")
	}
	return opts, nil
}

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

func health(args []string, stdout io.Writer, stderr io.Writer) int {
	opts, err := parseHostPortOptions(args)
	if err != nil {
		fmt.Fprintf(stderr, "health: %v\n", err)
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	result := scanHealth(ctx, opts)
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		fmt.Fprintf(stderr, "encode health: %v\n", err)
		return 1
	}
	if result.Found == nil {
		return 1
	}
	return 0
}

func parseHostPortOptions(args []string) (hostPortOptions, error) {
	opts := hostPortOptions{
		host:      defaultHost,
		portStart: defaultPortStart,
		portEnd:   defaultPortEnd,
	}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--host":
			i++
			if i >= len(args) {
				return opts, errors.New("--host requires a value")
			}
			opts.host = args[i]
		case "--ports":
			i++
			if i >= len(args) {
				return opts, errors.New("--ports requires a value")
			}
			start, end, err := parsePortRange(args[i])
			if err != nil {
				return opts, err
			}
			opts.portStart = start
			opts.portEnd = end
		default:
			return opts, fmt.Errorf("unknown health option: %s", args[i])
		}
	}

	return opts, nil
}

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

		service := serviceName(body)
		checked.Status = "ok"
		result.Checked = append(result.Checked, checked)
		if service == "easyeda-agent" {
			raw := append(json.RawMessage(nil), body...)
			result.Status = "found"
			result.Found = &daemonHealth{Port: port, Service: service, Raw: raw}
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
