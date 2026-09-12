package app

import (
	"testing"
	"time"
)

func TestLayoutConnectBudgetAndDocumentGuard(t *testing.T) {
	for _, tc := range []struct {
		action    string
		doc       string
		want      time.Duration
		wantError bool
	}{
		{"schematic.power.connect_pin", "page", acConnectPinTimeout, false},
		{"schematic.component.modify", "page", defaultActionTimeout, false},
		{"schematic.power.connect_pin", "wrong-page", acConnectPinTimeout, true},
	} {
		t.Run(tc.action+tc.doc, func(t *testing.T) {
			cfg, state, cleanup := newAutolayoutTestDaemon(t, func(_ int, call autolayoutTestCall) string {
				return autolayoutOK(tc.doc, `{}`)
			})
			defer cleanup()
			_, err := requestAutolayoutAction(cfg, tc.action, "w1", map[string]any{"pinX": 100, "pinY": 100}, "page", "reconnect")
			if (err != nil) != tc.wantError {
				t.Fatalf("error=%v, wantError=%v", err, tc.wantError)
			}
			calls := state.snapshot()
			if len(calls) != 1 || calls[0].TimeoutMs != int(tc.want/time.Millisecond) {
				t.Fatalf("wrong wire budget: %+v", calls)
			}
		})
	}
}
