package protocol

import "time"

// DispatchResponseGrace reserves time inside the caller's round-trip budget for
// the daemon to return a structured error after the connector wait expires.
const DispatchResponseGrace = 2 * time.Second
