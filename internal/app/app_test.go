package app

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunVersion(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := Run([]string{"version"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "easyeda-agent") {
		t.Fatalf("version output missing project name: %q", stdout.String())
	}
}

func TestRunUnknownCommand(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := Run([]string{"wat"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected exit 2, got %d", code)
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("stderr missing unknown command message: %q", stderr.String())
	}
}

func TestParseCallOptions(t *testing.T) {
	opts, err := parseCallOptions([]string{"system.health", "--window", "win-1", "--payload", `{"allPages":true}`})
	if err != nil {
		t.Fatalf("parseCallOptions returned error: %v", err)
	}
	if opts.action != "system.health" {
		t.Fatalf("unexpected action: %q", opts.action)
	}
	if opts.window != "win-1" {
		t.Fatalf("unexpected window: %q", opts.window)
	}
	if opts.payload != `{"allPages":true}` {
		t.Fatalf("unexpected payload: %q", opts.payload)
	}
}

func TestParseCallOptionsRequiresAction(t *testing.T) {
	if _, err := parseCallOptions([]string{"--window", "win-1"}); err == nil {
		t.Fatal("expected error when action is missing")
	}
}

func TestParsePortRange(t *testing.T) {
	start, end, err := parsePortRange("49620-49629")
	if err != nil {
		t.Fatalf("parsePortRange returned error: %v", err)
	}
	if start != 49620 || end != 49629 {
		t.Fatalf("unexpected range: %d-%d", start, end)
	}
}
