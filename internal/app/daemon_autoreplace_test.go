package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/internal/version"
)

func TestAutomaticDaemonReplacementAllowed(t *testing.T) {
	for _, tc := range []struct {
		name, cli, raw string
		want           bool
	}{
		{"legacy", "1.18.0", `{"service":"easyeda-agent","pid":17017,"version":"1.17.0"}`, true},
		{"localdev", "1.18.0-dev.1", `{"service":"easyeda-agent","pid":17017,"version":"1.17.0"}`, false},
		{"describe", "1.18.0-2-gabc", `{"service":"easyeda-agent","pid":17017,"version":"1.17.0"}`, false},
		{"dev", "dev", `{"service":"easyeda-agent","pid":17017,"version":"1.17.0"}`, false},
		{"malformed", "1.18.0", `{"version":"1.17.0",`, false},
		{"unreadable", "1.18.0", `null`, false},
		{"wrong type", "1.18.0", `{"service":"easyeda-agent","pid":17017,"version":17}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := automaticDaemonReplacementAllowed([]byte(tc.raw), tc.cli); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
	for _, pid := range []string{"", `,"pid":0`, `,"pid":-1`, `,"pid":1.5`, `,"pid":"17017"`, `,"pid":null`, `,"pid":999999999999999999999999999999`} {
		raw := `{"service":"easyeda-agent","version":"1.17.0"` + pid + `}`
		if automaticDaemonReplacementAllowed([]byte(raw), "1.18.0") {
			t.Fatalf("must reject invalid/missing PID: %s", raw)
		}
	}
	for _, lifecycle := range []string{`{"mode":"stdin","ownerId":"owner","instanceId":"instance"}`, `{"mode":"isolated"}`, `{"mode":"future"}`, `{}`, `null`, `"unreadable"`} {
		raw := `{"service":"easyeda-agent","pid":17017,"version":"1.17.0","lifecycle":` + lifecycle + `}`
		if automaticDaemonReplacementAllowed([]byte(raw), "1.18.0") {
			t.Fatalf("must protect lifecycle %s", lifecycle)
		}
	}
}

// Exercise real listWindows HTTP discovery, preserving windows and recording
// stop/start effects at their actual process boundary without touching processes.
func TestListWindowsDoesNotReplaceProtectedDaemon(t *testing.T) {
	old := version.Version
	oldStop, oldStart := restartDaemonStop, restartDaemonStart
	t.Cleanup(func() { version.Version = old; restartDaemonStop = oldStop; restartDaemonStart = oldStart })
	for _, tc := range []struct{ name, cli, lifecycle, optOut string }{
		{"localdev", "1.18.0-dev.1", "", ""},
		{"describe", "1.18.0-2-gabc", "", ""},
		{"owned", "1.18.0", `,"lifecycle":{"mode":"stdin","ownerId":"owner","instanceId":"instance"}`, ""},
		{"isolated", "1.18.0", `,"lifecycle":{"mode":"isolated"}`, ""},
		{"unknown", "1.18.0", `,"lifecycle":{"mode":"future"}`, ""},
		{"incomplete", "1.18.0", `,"lifecycle":{}`, ""},
		{"null", "1.18.0", `,"lifecycle":null`, ""},
		{"optout", "1.18.0", "", "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			version.Version = tc.cli
			t.Setenv("EASYEDA_NO_AUTOSTART", tc.optOut)
			stop, start, other := 0, 0, 0
			restartDaemonStop = func(string, int, io.Writer, daemonStopExpectation) error { stop++; return nil }
			restartDaemonStart = func(*appConfig, io.Writer, string, ...string) (int, error) {
				start++
				return 0, fmt.Errorf("unexpected start")
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/health":
					fmt.Fprintf(w, `{"service":"easyeda-agent","pid":17017,"version":"1.17.0","windows":[{"windowId":"ceshi"}]%s}`, tc.lifecycle)
				case "/stop", "/shutdown":
					stop++
					http.Error(w, "unexpected stop", 500)
				case "/start":
					start++
					http.Error(w, "unexpected start", 500)
				default:
					other++
					http.Error(w, "unexpected request", 500)
				}
			}))
			defer srv.Close()
			host, port, _ := strings.Cut(strings.TrimPrefix(srv.URL, "http://"), ":")
			windows, err := listWindows(&appConfig{host: host, ports: port + "-" + port})
			if err != nil || len(windows) != 1 || windows[0].WindowID != "ceshi" {
				t.Fatalf("windows=%+v err=%v", windows, err)
			}
			if stop != 0 || start != 0 || other != 0 {
				t.Fatalf("stop=%d start=%d other=%d", stop, start, other)
			}
		})
	}
}

// The stopping-time probe must protect a newly managed or unreadable service,
// even when discovery initially saw a stale legacy daemon.
func TestListWindowsRechecksReplacementHealth(t *testing.T) {
	oldVersion, oldStop, oldStart := version.Version, restartDaemonStop, restartDaemonStart
	t.Cleanup(func() { version.Version = oldVersion; restartDaemonStop = oldStop; restartDaemonStart = oldStart })
	version.Version = "1.18.0"
	t.Setenv("EASYEDA_NO_AUTOSTART", "")
	for _, second := range []string{`{"service":"easyeda-agent","pid":17017,"version":"1.17.0","lifecycle":{}}`, `{"service":"easyeda-agent","pid":17017,"version":"1.17.0",`, ``} {
		t.Run(second, func(t *testing.T) {
			stop, start, reads := 0, 0, 0
			restartDaemonStop = func(string, int, io.Writer, daemonStopExpectation) error { stop++; return nil }
			restartDaemonStart = func(*appConfig, io.Writer, string, ...string) (int, error) {
				start++
				return 0, fmt.Errorf("unexpected start")
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reads++
				if reads == 2 {
					fmt.Fprint(w, second)
					return
				}
				fmt.Fprint(w, `{"service":"easyeda-agent","pid":17017,"version":"1.17.0","windows":[{"windowId":"ceshi"}]}`)
			}))
			defer srv.Close()
			host, port, _ := strings.Cut(strings.TrimPrefix(srv.URL, "http://"), ":")
			windows, err := listWindows(&appConfig{host: host, ports: port + "-" + port})
			if err != nil || len(windows) != 1 || windows[0].WindowID != "ceshi" || stop != 0 || start != 0 || reads != 3 {
				t.Fatalf("windows=%+v err=%v stop=%d start=%d reads=%d", windows, err, stop, start, reads)
			}
		})
	}
}

// A release CLI must still upgrade an old legacy service exactly once.
func TestListWindowsReplacesLegacyDaemon(t *testing.T) {
	oldVersion, oldStop, oldStart := version.Version, restartDaemonStop, restartDaemonStart
	t.Cleanup(func() { version.Version = oldVersion; restartDaemonStop = oldStop; restartDaemonStart = oldStart })
	version.Version = "v1.18.0"
	t.Setenv("EASYEDA_NO_AUTOSTART", "")
	var upgraded atomic.Bool
	stop, start := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.Error(w, "unexpected request", 500)
			return
		}
		daemonVersion := "v1.17.0"
		if upgraded.Load() {
			daemonVersion = "v1.18.0"
		}
		fmt.Fprintf(w, `{"service":"easyeda-agent","pid":17017,"version":%q,"windows":[{"windowId":"ceshi"}]}`, daemonVersion)
	}))
	defer srv.Close()
	host, portText, _ := strings.Cut(strings.TrimPrefix(srv.URL, "http://"), ":")
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &appConfig{host: host, ports: portText + "-" + portText}
	restartDaemonStop = func(gotHost string, gotPort int, _ io.Writer, expected daemonStopExpectation) error {
		stop++
		if gotHost != host || gotPort != port || expected != (daemonStopExpectation{PID: 17017, Version: "v1.17.0"}) {
			t.Fatalf("unexpected stop target %s:%d", gotHost, gotPort)
		}
		return nil
	}
	restartDaemonStart = func(got *appConfig, _ io.Writer, exe string, extra ...string) (int, error) {
		start++
		if got != cfg || exe != "" || len(extra) != 0 || stop != 1 {
			t.Fatalf("unexpected start: cfg=%+v exe=%q extra=%v stop=%d", got, exe, extra, stop)
		}
		upgraded.Store(true)
		return port, nil
	}
	windows, err := listWindows(cfg)
	if err != nil || len(windows) != 1 || windows[0].WindowID != "ceshi" || stop != 1 || start != 1 || !upgraded.Load() {
		t.Fatalf("windows=%+v err=%v stop=%d start=%d upgraded=%v", windows, err, stop, start, upgraded.Load())
	}
}

// The port may be taken over after the automatic guard. The stop boundary must
// receive the guarded identity, never select the new PID as its expectation.
func TestListWindowsReplacementBindsGuardedIdentity(t *testing.T) {
	oldVersion, oldStop, oldStart := version.Version, restartDaemonStop, restartDaemonStart
	t.Cleanup(func() { version.Version = oldVersion; restartDaemonStop = oldStop; restartDaemonStart = oldStart })
	version.Version = "v1.18.0"
	t.Setenv("EASYEDA_NO_AUTOSTART", "")
	var takenOver atomic.Bool
	stop, start := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pid, v := 17017, "v1.17.0"
		if takenOver.Load() {
			pid, v = 18018, "v1.18.0"
		}
		fmt.Fprintf(w, `{"service":"easyeda-agent","pid":%d,"version":%q,"windows":[{"windowId":"ceshi"}]}`, pid, v)
	}))
	defer srv.Close()
	host, portText, _ := strings.Cut(strings.TrimPrefix(srv.URL, "http://"), ":")
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	restartDaemonStop = func(gotHost string, gotPort int, _ io.Writer, expected daemonStopExpectation) error {
		stop++
		takenOver.Store(true)
		resp, err := http.Get(srv.URL + "/health")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var current struct {
			PID     int    `json:"pid"`
			Version string `json:"version"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&current); err != nil {
			t.Fatal(err)
		}
		if gotHost != host || gotPort != port || expected != (daemonStopExpectation{PID: 17017, Version: "v1.17.0"}) || current.PID != 18018 || current.Version != "v1.18.0" {
			t.Fatalf("expected=%+v current=%+v target=%s:%d", expected, current, gotHost, gotPort)
		}
		return fmt.Errorf("identity drift: guarded PID/version no longer own endpoint")
	}
	restartDaemonStart = func(*appConfig, io.Writer, string, ...string) (int, error) {
		start++
		return 0, fmt.Errorf("unexpected start")
	}
	windows, err := listWindows(&appConfig{host: host, ports: portText + "-" + portText})
	if err != nil || len(windows) != 1 || windows[0].WindowID != "ceshi" || stop != 1 || start != 0 {
		t.Fatalf("windows=%+v err=%v stop=%d start=%d", windows, err, stop, start)
	}
}
