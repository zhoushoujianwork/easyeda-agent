package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

// dispatchTimeout bounds how long the daemon waits for a connector to answer a
// forwarded action. Heavy reads on real schematics (full netlist extraction,
// BOM generation, multi-page snapshot) routinely take 20-40s, so the cap is
// generous; the connector still keeps its own ping/pong liveness, and HTTP
// callers can layer their own shorter timeouts on top.
const dispatchTimeout = 60 * time.Second

// knownActions is the set of Phase 1 action names the daemon will accept.
var knownActions = func() map[string]bool {
	set := map[string]bool{}
	for _, a := range protocol.AllActions() {
		set[a.Name] = true
	}
	return set
}()

// actionDomain maps each action to its domain, used to pick the right window
// when a project is open in several (a pcb.* action → the project's PCB window).
var actionDomain = func() map[string]protocol.Domain {
	m := map[string]protocol.Domain{}
	for _, a := range protocol.AllActions() {
		m[a.Name] = a.Domain
	}
	return m
}()

// docTypeForAction returns the documentType an action targets ("pcb" or
// "schematic"), matching the connector's documentType labels. Domain-agnostic
// actions (project/document/system/debug) return "" (no preference).
func docTypeForAction(action string) string {
	switch actionDomain[action] {
	case protocol.DomainPcb:
		return "pcb"
	case protocol.DomainSchematic:
		return "schematic"
	default:
		return ""
	}
}

// handleAction accepts a typed action request, validates it, answers daemon-local
// actions directly, and forwards the rest to the target connector.
func (s *Server) handleAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req protocol.Request
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse(req.ID, "BAD_REQUEST", "invalid action request body", err.Error()))
		return
	}

	// Assign a request id up front so every response, including errors, carries one.
	if req.ID == "" {
		req.ID = s.nextRequestID()
	}

	if req.Action == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse(req.ID, "ACTION_REQUIRED", "action is required", "include an \"action\" field"))
		return
	}
	if !knownActions[req.Action] {
		writeJSON(w, http.StatusBadRequest, errorResponse(req.ID, "UNKNOWN_ACTION", fmt.Sprintf("unknown action: %s", req.Action), "run `easyeda actions` for the supported set"))
		return
	}

	// system.health is answered by the daemon itself; it needs no connector.
	if req.Action == "system.health" {
		started := time.Now().UTC()
		resp := s.systemHealthResponse(req.ID)
		s.audit.Append(fromResponse(started, &req, &resp))
		writeJSON(w, http.StatusOK, resp)
		return
	}

	// Stable-identity routing: resolve a --project hint to a windowId. The
	// ephemeral windowId churns on reconnect, so callers can target a project
	// name/uuid and let the daemon find its current window.
	if req.WindowID == "" && req.Project != "" {
		id, found, ambiguous := s.hub.windowForProject(req.Project, docTypeForAction(req.Action))
		if ambiguous {
			started := time.Now().UTC()
			errResp := errorResponse(req.ID, "AMBIGUOUS_PROJECT", fmt.Sprintf("multiple connected windows match project %q", req.Project), "pass --window to pick one (see `easyeda health`)")
			s.audit.Append(fromResponse(started, &req, &errResp))
			writeJSON(w, http.StatusConflict, errResp)
			return
		}
		if !found {
			started := time.Now().UTC()
			errResp := errorResponse(req.ID, "NO_CONNECTOR", fmt.Sprintf("no connected window for project %q", req.Project), "open the project in EasyEDA (connector enabled), or run `easyeda health`")
			s.audit.Append(fromResponse(started, &req, &errResp))
			writeJSON(w, http.StatusServiceUnavailable, errResp)
			return
		}
		req.WindowID = id
	}

	target, ok := s.hub.target(req.WindowID)
	if !ok {
		started := time.Now().UTC()
		detail := "start EasyEDA with the connector extension, then retry"
		if req.WindowID != "" {
			detail = fmt.Sprintf("no connector registered for window %q", req.WindowID)
		}
		errResp := errorResponse(req.ID, "NO_CONNECTOR", "no EasyEDA connector is available", detail)
		s.audit.Append(fromResponse(started, &req, &errResp))
		writeJSON(w, http.StatusServiceUnavailable, errResp)
		return
	}

	req.Type = protocol.TypeRequest
	if req.Version == "" {
		req.Version = "v1"
	}
	req.CreatedAt = time.Now().UTC()
	req.WindowID = target.id()

	ctx, cancel := context.WithTimeout(r.Context(), dispatchTimeout)
	defer cancel()

	started := time.Now().UTC()
	resp, err := target.dispatch(ctx, req)
	if err != nil {
		errResp := errorResponse(req.ID, "DISPATCH_FAILED", "connector did not respond", err.Error())
		s.audit.Append(fromResponse(started, &req, &errResp))
		writeJSON(w, http.StatusGatewayTimeout, errResp)
		return
	}
	// The connector echoes id/version/ok/result/context/artifacts but does not
	// stamp createdAt; the daemon owns the wall-clock for forwarded responses.
	if resp.CreatedAt.IsZero() {
		resp.CreatedAt = time.Now().UTC()
	}
	if resp.Type == "" {
		resp.Type = protocol.TypeResponse
	}
	s.persistArtifacts(resp, s.artifactDir(req.OutputDir))
	s.audit.Append(fromResponse(started, &req, resp))
	// After a successful content-changing action, arm a debounced autosave so the
	// work reaches disk without the agent having to remember to save (no-op when
	// autosave is disabled or the action doesn't mutate). See autosave.go.
	if resp.OK {
		s.maybeAutosave(&req)
	}
	writeJSON(w, http.StatusOK, resp)
}

// artifactDir picks where to persist artifacts. The CLI sends its own working
// directory (outputDir) so files land in the user's project under a hidden
// .easyeda/artifacts dir — not the daemon's cwd. Callers that don't send one
// (tests, raw HTTP) fall back to the configured ArtifactDir, then "artifacts".
func (s *Server) artifactDir(outputDir string) string {
	if outputDir != "" {
		return filepath.Join(outputDir, ".easyeda", "artifacts")
	}
	if s.opts.ArtifactDir != "" {
		return s.opts.ArtifactDir
	}
	return "artifacts"
}

// artifactFileName builds a sortable, findable filename: a local timestamp
// prefix (YYYYMMDD-HHMMSS) so files list in chronological order, plus the kind
// and a short id for findability and uniqueness within the same second.
//
//	e.g. 20260627-143022-schematic_snapshot-1a2b3c4d.png
func artifactFileName(a *protocol.Artifact, ts time.Time) string {
	parts := []string{ts.Local().Format("20060102-150405")}
	if a.Kind != "" {
		parts = append(parts, a.Kind)
	}
	short := strings.TrimPrefix(a.ID, "art_")
	if len(short) > 8 {
		short = short[:8]
	}
	if short != "" {
		parts = append(parts, short)
	}
	return strings.Join(parts, "-") + filepath.Ext(a.FileName)
}

// persistArtifacts writes any inline (base64) artifact bytes returned by the
// connector to dir, fills Path/Size/SHA256, and clears the inline bytes so they
// are not echoed back to the caller. Failures are reported as warnings rather
// than failing the whole action.
func (s *Server) persistArtifacts(resp *protocol.Response, dir string) {
	if resp == nil || len(resp.Artifacts) == 0 {
		return
	}

	for i := range resp.Artifacts {
		a := &resp.Artifacts[i]
		if a.InlineBase64 == "" {
			continue
		}

		data, err := base64.StdEncoding.DecodeString(a.InlineBase64)
		a.InlineBase64 = ""
		if err != nil {
			resp.Warnings = append(resp.Warnings, fmt.Sprintf("artifact %s: invalid base64: %v", a.ID, err))
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			resp.Warnings = append(resp.Warnings, fmt.Sprintf("artifact %s: create dir: %v", a.ID, err))
			continue
		}

		full := filepath.Join(dir, artifactFileName(a, resp.CreatedAt))
		if err := os.WriteFile(full, data, 0o644); err != nil {
			resp.Warnings = append(resp.Warnings, fmt.Sprintf("artifact %s: write: %v", a.ID, err))
			continue
		}

		if abs, err := filepath.Abs(full); err == nil {
			a.Path = abs
		} else {
			a.Path = full
		}
		sum := sha256.Sum256(data)
		a.Size = int64(len(data))
		a.SHA256 = hex.EncodeToString(sum[:])
	}
}

// systemHealthResponse reports daemon liveness and the connected windows.
func (s *Server) systemHealthResponse(id string) protocol.Response {
	windows := s.hub.list()
	ids := make([]string, 0, len(windows))
	for _, w := range windows {
		ids = append(ids, w.WindowID)
	}
	return protocol.Response{
		Envelope: protocol.Envelope{
			ID:        id,
			Type:      protocol.TypeResponse,
			Version:   "v1",
			CreatedAt: time.Now().UTC(),
		},
		OK: true,
		Result: map[string]any{
			"service":   Service,
			"version":   s.opts.Version,
			"windows":   windows,
			"windowIds": ids,
		},
	}
}

func (s *Server) nextRequestID() string {
	return fmt.Sprintf("req_%d", s.reqSeq.Add(1))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func errorResponse(id, code, message, detail string) protocol.Response {
	return protocol.Response{
		Envelope: protocol.Envelope{
			ID:        id,
			Type:      protocol.TypeResponse,
			Version:   "v1",
			CreatedAt: time.Now().UTC(),
		},
		OK: false,
		Error: &protocol.ErrorInfo{
			Code:    code,
			Message: message,
			Detail:  detail,
		},
	}
}
