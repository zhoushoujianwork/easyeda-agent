// Package daemon hosts the long-running local server that bridges Skills, the
// CLI, and the EasyEDA connector extension. Phase 1 exposes a /health endpoint
// on the first free port in 49620-49629; WebSocket action dispatch, artifact
// storage, and audit logging are added next.
package daemon
