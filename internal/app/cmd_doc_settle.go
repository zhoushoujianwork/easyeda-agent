package app

import (
	"time"
)

// Doc/page state is an implicit global in EasyEDA: `document.open` returns as
// soon as the tab is created, BEFORE the page's primitives/netlist finish
// (re)loading. A read command fired immediately after a switch therefore
// samples a half-loaded page and gets empty/stale data (issue #67). These
// helpers close that race by (1) confirming the target is the live active
// document and (2) waiting until its data stops changing.

// settleTracker decides when a sequence of sampled primitive counts has
// "settled" — the connector exposes no load-complete signal, so we treat a
// count that is identical across two consecutive polls as loaded. A non-empty
// stable count settles immediately; a stable count of 0 only settles after a
// grace window (minEmptySamples), so a page mid-load (0 → 93) is not mistaken
// for a genuinely empty page.
type settleTracker struct {
	last            int
	hasLast         bool
	samples         int
	minEmptySamples int
}

// observe records one sample and reports whether the page has settled.
func (s *settleTracker) observe(count int) bool {
	s.samples++
	prev, had := s.last, s.hasLast
	s.last, s.hasLast = count, true
	if !had || prev != count {
		return false
	}
	if count > 0 {
		return true
	}
	return s.samples >= s.minEmptySamples
}

// docSettleDeadline bounds how long waitDocSettle polls before giving up and
// letting the caller proceed with whatever the page currently holds.
const docSettleDeadline = 8 * time.Second

// docSettleInterval is the gap between primitive-count samples.
const docSettleInterval = 400 * time.Millisecond

// countActivePagePrimitives reads the active page's component count via
// schematic.components.list (active page only — no allPages). A read error
// yields (0,false) so the caller keeps polling rather than aborting.
func countActivePagePrimitives(cfg *appConfig, window string) (int, bool) {
	res, err := requestAction(cfg, "schematic.components.list", window, nil)
	if err != nil || res.Result == nil {
		return 0, false
	}
	if c, ok := res.Result["count"].(float64); ok {
		return int(c), true
	}
	if comps, ok := res.Result["components"].([]any); ok {
		return len(comps), true
	}
	return 0, false
}

// waitDocSettle polls the active page's primitive count until it stabilizes
// (two identical consecutive reads) or the deadline passes. It returns true if
// the page settled, false on timeout — callers proceed either way, using the
// bool to flag ready:false when the page never quieted down.
func waitDocSettle(cfg *appConfig, window string) bool {
	tracker := &settleTracker{minEmptySamples: 3}
	deadline := time.Now().Add(docSettleDeadline)
	for {
		count, ok := countActivePagePrimitives(cfg, window)
		if ok && tracker.observe(count) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(docSettleInterval)
	}
}

// pageScope captures the state switchToPage changed so a caller can restore the
// original active document after a page-scoped read.
type pageScope struct {
	window     string
	prevActive string // active document uuid before the switch (may be "")
	switched   bool   // true if we actually changed the active document
	settled    bool   // true if the target page settled before the deadline
}

// switchToPage resolves a --page target (name or uuid) in the given window,
// brings it to the front if it is not already active, and waits for its data to
// settle. It returns a pageScope so the caller can optionally restore the prior
// active document. Making the page an explicit parameter removes the implicit
// global-state race: check/read/list act on the page the caller named, not
// whatever tab happened to be foreground.
func switchToPage(cfg *appConfig, window, target string) (pageScope, error) {
	docs, activeUUID, win, err := discoverDocs(cfg, window)
	if err != nil {
		return pageScope{}, err
	}
	match, err := resolveDoc(docs, target)
	if err != nil {
		return pageScope{}, err
	}
	sc := pageScope{window: win, prevActive: activeUUID}
	if match.UUID != activeUUID {
		if _, err := requestAction(cfg, "document.open", win,
			map[string]any{"uuid": match.UUID}); err != nil {
			return pageScope{}, err
		}
		sc.switched = true
	}
	sc.settled = waitDocSettle(cfg, win)
	return sc, nil
}

// restore switches back to the document that was active before switchToPage, if
// switchToPage actually changed it. Best-effort: a restore failure is returned
// so the caller can surface it, but the primary read has already happened.
func (sc pageScope) restore(cfg *appConfig) error {
	if !sc.switched || sc.prevActive == "" {
		return nil
	}
	_, err := requestAction(cfg, "document.open", sc.window,
		map[string]any{"uuid": sc.prevActive})
	return err
}
