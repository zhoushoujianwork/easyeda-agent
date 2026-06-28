package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// newHangingDaemon stands up a fake daemon: /health identifies as easyeda-agent
// so scanHealth picks it, while /action blocks past the caller's timeout to
// emulate the connector hanging on a bad uuid. Returns a cfg pointed at it.
func newHangingDaemon(t *testing.T) (*appConfig, func()) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_, _ = w.Write([]byte(`{"service":"easyeda-agent","windows":[]}`))
		case "/action":
			// Block well past the client's timeout so the call fails with a
			// deadline, but bounded so Close() doesn't stall on a held conn.
			select {
			case <-r.Context().Done():
			case <-time.After(800 * time.Millisecond):
			}
		default:
			http.NotFound(w, r)
		}
	}))

	// httptest binds 127.0.0.1:<random>; point the port-scan straight at it.
	hostPort := strings.TrimPrefix(srv.URL, "http://")
	host, portStr, ok := strings.Cut(hostPort, ":")
	if !ok {
		t.Fatalf("unexpected test server URL %q", srv.URL)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse test server port: %v", err)
	}
	cfg := &appConfig{host: host, ports: fmt.Sprintf("%d-%d", port, port)}
	return cfg, srv.Close
}

// A hung /action must surface as context.DeadlineExceeded so the place command
// can translate it into the instance-uuid hint instead of stalling.
func TestPostActionFailsFastOnHang(t *testing.T) {
	cfg, cleanup := newHangingDaemon(t)
	defer cleanup()

	start := time.Now()
	_, err := postAction(cfg, "schematic.component.place", "", nil, 300*time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a timeout error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("postAction did not fail fast: took %s", elapsed)
	}
}

// The hint must name the instance-uuid trap and point at lib search.
func TestPlaceUUIDHint(t *testing.T) {
	msg := placeUUIDHint(placeTimeout).Error()
	for _, want := range []string{"lib search", "instance", "uuid"} {
		if !strings.Contains(strings.ToLower(msg), want) {
			t.Fatalf("hint missing %q: %s", want, msg)
		}
	}
}
