package app

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestIsolatedDaemonRefusesOccupiedPortWithoutReplacingOwner(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "owner-alive") }))
	defer srv.Close()
	_, p, _ := strings.Cut(strings.TrimPrefix(srv.URL, "http://"), ":")
	port, _ := strconv.Atoi(p)
	state := filepath.Join(t.TempDir(), "isolated")
	var out bytes.Buffer
	c := newDaemonServeIsolatedCmd(&out)
	c.SetArgs([]string{"--port", fmt.Sprint(port), "--state-dir", state})
	if err := c.Execute(); err == nil {
		t.Fatal("occupied port accepted")
	}
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal("original owner was affected", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if string(data) != "owner-alive" {
		t.Fatal(string(data))
	}
	if _, err := os.Stat(filepath.Join(state, "daemon.pid")); !os.IsNotExist(err) {
		t.Fatal("temporary PID was not cleaned after failed bind")
	}
}

func TestIsolatedDaemonRequiresExplicitFreshStateAndPort(t *testing.T) {
	for _, args := range [][]string{{}, {"--port", "60842", "--state-dir", "relative"}, {"--port", "60842", "--state-dir", t.TempDir()}} {
		var out bytes.Buffer
		c := newDaemonServeIsolatedCmd(&out)
		c.SetArgs(args)
		if err := c.Execute(); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
