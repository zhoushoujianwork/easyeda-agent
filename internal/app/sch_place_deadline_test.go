package app

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

func TestPlaceStructuredDeadlinePreservesErrorAndReadbackAdvice(t *testing.T) {
	response := `{"ok":false,"error":{"code":"DISPATCH_FAILED","message":"connector did not respond","detail":"context deadline exceeded"}}`
	cfg, calls, cleanup := newAutolayoutTestDaemon(t, func(_ int, _ autolayoutTestCall) string { return response })
	defer cleanup()
	var out, stderr bytes.Buffer
	cmd := newSchCmd(cfg, &out, &stderr)
	cmd.SetArgs([]string{"place", "--lib", "lib1", "--uuid", "dev1", "--x", "100", "--y", "200"})
	err := cmd.Execute()
	var ae *actionError
	if !errors.As(err, &ae) || ae.Code != "DISPATCH_FAILED" || ae.Detail != "context deadline exceeded" {
		t.Fatalf("lost structured daemon error: %v", err)
	}
	if !errors.Is(err, errActionFailed) {
		t.Fatal("generic failure sentinel compatibility lost")
	}
	for _, want := range []string{"Read back", "may already exist", "lib search"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("missing %q in %v", want, err)
		}
	}
	if strings.TrimSpace(out.String()) != response {
		t.Fatalf("changed machine-readable response: %s", out.String())
	}
	actual := calls.snapshot()
	if len(actual) != 1 || time.Duration(actual[0].TimeoutMs)*time.Millisecond-protocol.DispatchResponseGrace != 8*time.Second {
		t.Fatalf("expected one request with eight seconds of connector wait, got %+v", actual)
	}
}

func TestPlaceDeadlineAdviceOnlyForDeadlines(t *testing.T) {
	for _, err := range []error{
		&actionError{Code: "DISPATCH_FAILED", Detail: "context canceled"},
		&actionError{Code: "EDA_CALL_FAILED", Detail: "context deadline exceeded"},
		errors.New("invalid payload"),
		nil,
	} {
		if got := placeDispatchError(err); got != err {
			t.Fatalf("non-deadline error changed: %v -> %v", err, got)
		}
	}
	got := placeDispatchError(context.DeadlineExceeded)
	if !errors.Is(got, context.DeadlineExceeded) || !strings.Contains(got.Error(), "Read back") {
		t.Fatalf("HTTP timeout lost cause or advice: %v", got)
	}
}
