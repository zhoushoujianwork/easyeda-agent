package app

import "testing"

// A non-empty count that repeats settles immediately; the whole point of the
// settle wait is to catch the 0 → N load, not to stall a page that already has
// its parts.
func TestSettleTracker_NonEmptyStable(t *testing.T) {
	s := &settleTracker{minEmptySamples: 3}
	if s.observe(93) {
		t.Fatal("first sample must not settle (no prior to compare)")
	}
	if !s.observe(93) {
		t.Fatal("two identical non-empty samples should settle")
	}
}

// A page mid-load (0 → 0 → 93 → 93) must NOT settle on the early zeros before
// the grace window, or `sch check` fired right after a switch gets empty
// findings.
func TestSettleTracker_LoadingNotMistakenEmpty(t *testing.T) {
	s := &settleTracker{minEmptySamples: 3}
	if s.observe(0) {
		t.Fatal("first zero must not settle")
	}
	if s.observe(0) {
		t.Fatal("second zero is within the grace window, must not settle")
	}
	if s.observe(93) {
		t.Fatal("count changed 0→93, must not settle yet")
	}
	if !s.observe(93) {
		t.Fatal("two identical non-empty samples after load should settle")
	}
}

// A genuinely empty page (stable 0 past the grace window) eventually settles so
// the wait doesn't burn the full deadline on every empty page.
func TestSettleTracker_GenuinelyEmptySettles(t *testing.T) {
	s := &settleTracker{minEmptySamples: 3}
	got := []bool{s.observe(0), s.observe(0), s.observe(0)}
	if got[0] || got[1] {
		t.Fatalf("zeros before grace window must not settle: %v", got)
	}
	if !got[2] {
		t.Fatal("stable zero past minEmptySamples should settle")
	}
}
