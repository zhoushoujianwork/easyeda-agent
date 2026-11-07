package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zhoushoujianwork/easyeda-agent/internal/daemon"
)

const testOwnerID = "75d0f0d8-9dd4-4c11-a4f7-606fa1a7b6cd"

// Subprocesses exercise real OS pipes, without touching any installed daemon.
func TestOwnedDaemonProcessHelper(t *testing.T) {
	port := os.Getenv("EASYEDA_TEST_OWNED_PORT")
	if port == "" {
		return
	}
	if os.Getenv("EASYEDA_TEST_OWNED_PARENT") == "1" {
		child := exec.Command(os.Args[0], "-test.run=^TestOwnedDaemonProcessHelper$")
		child.Env = append(os.Environ(), "EASYEDA_TEST_OWNED_PARENT=0")
		pipe, err := child.StdinPipe()
		if err != nil {
			os.Exit(2)
		}
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		fmt.Fprintln(os.Stdout, child.Process.Pid)
		_, _ = io.Copy(pipe, os.Stdin)
		_ = pipe.Close()
		if child.Wait() != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	c := newDaemonServeOwnedCmd(io.Discard)
	c.SetOut(io.Discard)
	c.SetErr(io.Discard)
	c.SetArgs([]string{"--port", port, "--owner-id", testOwnerID, "--autosave-debounce", "0"})
	err := c.Execute()
	if receipt := os.Getenv("EASYEDA_TEST_OWNED_RECEIPT"); receipt != "" {
		_ = os.WriteFile(receipt, []byte(fmt.Sprint(err)), 0600)
	}
	if err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

type ownedHealth struct {
	PID       int               `json:"pid"`
	Port      int               `json:"port"`
	Version   string            `json:"version"`
	Lifecycle *daemon.Lifecycle `json:"lifecycle"`
}

func readOwnedHealth(port int) (ownedHealth, error) {
	var h ownedHealth
	client := &http.Client{Timeout: 150 * time.Millisecond}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/health", port))
	if err != nil {
		return h, err
	}
	defer resp.Body.Close()
	err = json.NewDecoder(resp.Body).Decode(&h)
	return h, err
}

func waitOwnedHealth(t *testing.T, port int) ownedHealth {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		h, err := readOwnedHealth(port)
		if err == nil && h.Lifecycle != nil {
			return h
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("owned daemon did not become healthy")
	return ownedHealth{}
}

func TestOwnedDaemonEOFStopsExactChild(t *testing.T) {
	port := freeTCPPort(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestOwnedDaemonProcessHelper$")
	cmd.Env = append(os.Environ(), "EASYEDA_TEST_OWNED_PORT="+strconv.Itoa(port))
	pipe, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() { _ = pipe.Close(); _ = cmd.Process.Kill() })
	h := waitOwnedHealth(t, port)
	if h.PID != cmd.Process.Pid || h.Port != port || h.Lifecycle.Mode != "stdin" || h.Lifecycle.OwnerID != testOwnerID || len(h.Lifecycle.InstanceID) != 32 {
		t.Fatalf("wrong health identity: %+v / %+v", h, h.Lifecycle)
	}
	// The same held pipe remains healthy, rather than stopping on first input.
	if _, err := pipe.Write([]byte("keep-alive is optional\n")); err != nil {
		t.Fatal(err)
	}
	if next := waitOwnedHealth(t, port); next.Lifecycle.InstanceID != h.Lifecycle.InstanceID {
		t.Fatal("instance changed")
	}
	_ = pipe.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("owned process remained after EOF")
	}
	if !portFree("127.0.0.1", port) {
		t.Fatal("port remained after process exit")
	}
}

func TestOwnedDaemonParentCrashClosesPipe(t *testing.T) {
	port := freeTCPPort(t)
	receipt := filepath.Join(t.TempDir(), "child-exit")
	parent := exec.Command(os.Args[0], "-test.run=^TestOwnedDaemonProcessHelper$")
	parent.Env = append(os.Environ(), "EASYEDA_TEST_OWNED_PORT="+strconv.Itoa(port), "EASYEDA_TEST_OWNED_PARENT=1", "EASYEDA_TEST_OWNED_RECEIPT="+receipt)
	stdin, err := parent.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := parent.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close(); _ = parent.Process.Kill() })
	pidLine, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	childPID, err := strconv.Atoi(strings.TrimSpace(pidLine))
	if err != nil || childPID <= 0 {
		t.Fatalf("bad child pid %q", pidLine)
	}
	child, _ := os.FindProcess(childPID)
	childStopped := false
	t.Cleanup(func() {
		if child != nil {
			if !childStopped {
				_ = child.Kill()
			}
			_ = child.Release()
		}
	})
	if h := waitOwnedHealth(t, port); h.PID != childPID {
		t.Fatal("health was not the spawned child")
	}
	if err := parent.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = parent.Wait()
	deadline := time.Now().Add(7 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(receipt)
		if err == nil && string(data) == "<nil>" && portFree("127.0.0.1", port) {
			childStopped = true
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("daemon did not complete graceful shutdown after its parent crashed")
}

func TestOwnedDaemonInstancesAndOwnersRemainIndependent(t *testing.T) {
	type running struct {
		pipe   *io.PipeWriter
		done   chan error
		port   int
		health ownedHealth
	}
	var services []running
	for _, owner := range []string{testOwnerID, "75d0f0d8-9dd4-4c11-a4f7-606fa1a7b6ce"} {
		reader, writer := io.Pipe()
		t.Cleanup(func() { _ = writer.Close(); _ = reader.Close() })
		port := freeTCPPort(t)
		c := newDaemonServeOwnedCmd(io.Discard)
		c.SetIn(reader)
		c.SetOut(io.Discard)
		c.SetErr(io.Discard)
		c.SetArgs([]string{"--port", strconv.Itoa(port), "--owner-id", owner, "--autosave-debounce", "0"})
		done := make(chan error, 1)
		go func() { done <- c.Execute() }()
		h := waitOwnedHealth(t, port)
		if h.Lifecycle.OwnerID != owner {
			t.Fatal("owner identity crossed instances")
		}
		services = append(services, running{writer, done, port, h})
	}
	if services[0].health.Lifecycle.InstanceID == services[1].health.Lifecycle.InstanceID {
		t.Fatal("instance nonce reused")
	}
	for i, service := range services {
		_ = service.pipe.Close()
		select {
		case err := <-service.done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(7 * time.Second):
			t.Fatal("service did not exit")
		}
		if !portFree("127.0.0.1", service.port) {
			t.Fatal("closed service still listening")
		}
		if i == 0 {
			waitOwnedHealth(t, services[1].port)
		}
	}
}

func TestDaemonStopExpectedIdentityRejectsDrift(t *testing.T) {
	port := freeTCPPort(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestOwnedDaemonProcessHelper$")
	cmd.Env = append(os.Environ(), "EASYEDA_TEST_OWNED_PORT="+strconv.Itoa(port))
	pipe, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pipe.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() })
	h := waitOwnedHealth(t, port)
	for _, expected := range []daemonStopExpectation{
		{PID: h.PID + 1}, {PID: h.PID, Version: "wrong-version"}, {PID: h.PID, InstanceID: "wrong-instance"},
	} {
		err := stopLocalDaemonMatching("127.0.0.1", port, io.Discard, expected)
		if err == nil || !strings.Contains(err.Error(), "no process was terminated") {
			t.Fatalf("accepted drift: %+v, %v", expected, err)
		}
		if next := waitOwnedHealth(t, port); next.PID != h.PID || next.Lifecycle.InstanceID != h.Lifecycle.InstanceID {
			t.Fatal("rejected stop changed the service")
		}
	}
	if err := stopLocalDaemonMatching("127.0.0.1", port, io.Discard, daemonStopExpectation{PID: h.PID, Version: h.Version, InstanceID: h.Lifecycle.InstanceID}); err != nil {
		t.Fatal(err)
	}
	if !portFree("127.0.0.1", port) {
		t.Fatal("matching stop did not close port")
	}
}

func TestDaemonStopExpectedFlagsValidateBeforeAccess(t *testing.T) {
	for _, args := range [][]string{{"--expected-pid", "0"}, {"--expected-pid", "-1"}, {"--expected-version", "v1.0.0"}, {"--expected-instance-id", "nonce"}, {"--expected-pid", "1", "--expected-version", ""}} {
		c := newDaemonStopCmd(&appConfig{host: "127.0.0.1", ports: "60833-60833"}, io.Discard)
		c.SetOut(io.Discard)
		c.SetErr(io.Discard)
		c.SetArgs(args)
		if c.Execute() == nil {
			t.Fatalf("accepted invalid expectation %v", args)
		}
	}
}

func TestOwnedDaemonRejectsOccupiedPort(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "existing owner") }))
	defer srv.Close()
	_, p, _ := strings.Cut(strings.TrimPrefix(srv.URL, "http://"), ":")
	input, output := io.Pipe()
	defer input.Close()
	defer output.Close()
	c := newDaemonServeOwnedCmd(io.Discard)
	c.SetOut(io.Discard)
	c.SetErr(io.Discard)
	c.SetIn(input)
	c.SetArgs([]string{"--port", p, "--owner-id", testOwnerID})
	if c.Execute() == nil {
		t.Fatal("occupied port accepted")
	}
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal("existing service was affected", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if string(data) != "existing owner" {
		t.Fatal(string(data))
	}
}

func TestOwnedDaemonInputErrorShutsDown(t *testing.T) {
	port := freeTCPPort(t)
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	done := make(chan error, 1)
	go func() {
		done <- runOwnedDaemon(context.Background(), reader, daemon.Options{Host: "127.0.0.1", PortStart: port, PortEnd: port,
			Lifecycle: &daemon.Lifecycle{Mode: "stdin", OwnerID: testOwnerID, InstanceID: "test-instance"}}, io.Discard)
	}()
	waitOwnedHealth(t, port)
	_ = writer.CloseWithError(errors.New("test broken pipe"))
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "test broken pipe") {
			t.Fatal(err)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("stdin error did not stop daemon")
	}
	if !portFree("127.0.0.1", port) {
		t.Fatal("input error left port listening")
	}
}

func TestOwnedDaemonRejectsMissingOrInvalidIdentity(t *testing.T) {
	for _, args := range [][]string{{}, {"--port", "80", "--owner-id", testOwnerID}, {"--port", "65536", "--owner-id", testOwnerID}, {"--port", "60833", "--owner-id", "bad"}, {"--port", "60833", "--owner-id", testOwnerID, "--autosave-debounce", "-1s"}} {
		c := newDaemonServeOwnedCmd(io.Discard)
		c.SetOut(io.Discard)
		c.SetErr(io.Discard)
		c.SetArgs(args)
		if c.Execute() == nil {
			t.Fatalf("accepted invalid args %v", args)
		}
	}
}
