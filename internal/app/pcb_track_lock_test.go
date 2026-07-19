package app

// The original test here covered trackLockJS, the debug.exec_js body builder
// that was the interim implementation of `pcb track-lock`. #127 graduated the
// action to the typed connector handler `pcb.track.lock` (the exec_js → typed
// action → Cobra dev loop), so the JS builder is gone; the handler's scope
// semantics (net filter / ids / all-with-net-guard / includeFills / idempotent
// re-lock) live connector-side in extension/src/actions.ts and are exercised by
// the real-machine flow (`pcb track-lock` + `pcb route-critical` lock step).
// What remains unit-testable Go-side is the flag → payload contract, which is
// covered implicitly by the route-critical tests (pcb_route_critical_test.go).
