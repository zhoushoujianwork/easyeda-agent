package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHealthHandler(t *testing.T) {
	srv := New(Options{Host: "127.0.0.1", Version: "0.1.0-test"})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	srv.routes(49620).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body health
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode health body: %v", err)
	}
	if body.Service != Service {
		t.Fatalf("expected service %q, got %q", Service, body.Service)
	}
	if body.Port != 49620 {
		t.Fatalf("expected reported port 49620, got %d", body.Port)
	}
	if body.Version != "0.1.0-test" {
		t.Fatalf("unexpected version: %q", body.Version)
	}
	if body.Windows == nil {
		t.Fatal("windows should be an empty array, not null")
	}
}

func TestHealthHandlerRejectsNonGet(t *testing.T) {
	srv := New(Options{Version: "0.1.0-test"})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	srv.routes(49620).ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestListenInvalidRange(t *testing.T) {
	srv := New(Options{Host: "127.0.0.1", PortStart: 100, PortEnd: 10})
	if _, _, err := srv.listen(); err == nil {
		t.Fatal("expected error for inverted port range")
	}
}

func TestRunServesAndShutsDown(t *testing.T) {
	port := freePort(t)
	srv := New(Options{
		Host:      "127.0.0.1",
		PortStart: port,
		PortEnd:   port,
		Version:   "0.1.0-test",
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- srv.Run(ctx, io.Discard)
	}()

	url := fmt.Sprintf("http://127.0.0.1:%d/health", port)
	body := waitForHealth(t, url)
	if body.Service != Service {
		t.Fatalf("expected service %q, got %q", Service, body.Service)
	}
	if body.Port != port {
		t.Fatalf("expected reported port %d, got %d", port, body.Port)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error on shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not shut down within timeout")
	}
}

// freePort reserves an OS-assigned port, then releases it so the daemon can
// claim it. There is a small race window, but it keeps the test self-contained.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("release free port: %v", err)
	}
	return port
}

func waitForHealth(t *testing.T, url string) health {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	client := http.Client{Timeout: 500 * time.Millisecond}
	for {
		resp, err := client.Get(url)
		if err == nil {
			defer resp.Body.Close()
			var body health
			if decodeErr := json.NewDecoder(resp.Body).Decode(&body); decodeErr != nil {
				t.Fatalf("decode health: %v", decodeErr)
			}
			return body
		}
		if time.Now().After(deadline) {
			t.Fatalf("daemon never became reachable at %s: %v", url, err)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
