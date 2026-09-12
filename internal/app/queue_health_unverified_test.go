package app

import (
	"testing"
	"time"
)

func TestUnverifiedConnectorHealthDoesNotEnterQueueWaitLoop(t *testing.T) {
	err := &actionError{Action: "schematic.save", Code: "CONNECTOR_HEALTH_UNVERIFIED", Message: "bypass health unknown"}
	calls := 0
	got := retryWhileQueueBlocked("save", func() error { calls++; return err }, queueBlockRetryPolicy{
		sleep: func(time.Duration) { t.Fatal("unverified health must not cause automatic queue waiting") },
	})
	if got != err || calls != 1 {
		t.Fatalf("got %v after %d calls", got, calls)
	}
}
