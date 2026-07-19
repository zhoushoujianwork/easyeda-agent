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
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("stderr missing unknown command message: %q", stderr.String())
	}
}

func TestParsePortRange(t *testing.T) {
	start, end, err := parsePortRange("60832-60841")
	if err != nil {
		t.Fatalf("parsePortRange returned error: %v", err)
	}
	if start != 60832 || end != 60841 {
		t.Fatalf("unexpected range: %d-%d", start, end)
	}
}
