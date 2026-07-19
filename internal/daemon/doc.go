// Package daemon hosts the long-running local server that bridges Skills, the
// CLI, and the EasyEDA connector extension. Phase 1 exposes a /health endpoint
// on the first free port in 60832-60841; WebSocket action dispatch, artifact
// storage, and audit logging are added next.
package daemon
