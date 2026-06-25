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
	"time"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

// dispatchTimeout bounds how long the daemon waits for a connector to answer a
// forwarded action.
const dispatchTimeout = 15 * time.Second

// knownActions is the set of Phase 1 action names the daemon will accept.
var knownActions = func() map[string]bool {
	set := map[string]bool{}
	for _, a := range protocol.Phase1Actions() {
		set[a.Name] = true
	}
	return set
}()

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
		writeJSON(w, http.StatusOK, s.systemHealthResponse(req.ID))
		return
	}

	target, ok := s.hub.target(req.WindowID)
	if !ok {
		detail := "start EasyEDA with the connector extension, then retry"
		if req.WindowID != "" {
			detail = fmt.Sprintf("no connector registered for window %q", req.WindowID)
		}
		writeJSON(w, http.StatusServiceUnavailable, errorResponse(req.ID, "NO_CONNECTOR", "no EasyEDA connector is available", detail))
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

	resp, err := target.dispatch(ctx, req)
	if err != nil {
		writeJSON(w, http.StatusGatewayTimeout, errorResponse(req.ID, "DISPATCH_FAILED", "connector did not respond", err.Error()))
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
	s.persistArtifacts(resp)
	writeJSON(w, http.StatusOK, resp)
}

// persistArtifacts writes any inline (base64) artifact bytes returned by the
// connector to the artifact directory, fills Path/Size/SHA256, and clears the
// inline bytes so they are not echoed back to the caller. Failures are reported
// as warnings rather than failing the whole action.
func (s *Server) persistArtifacts(resp *protocol.Response) {
	if resp == nil || len(resp.Artifacts) == 0 {
		return
	}

	dir := s.opts.ArtifactDir
	if dir == "" {
		dir = "artifacts"
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

		name := a.ID
		if ext := filepath.Ext(a.FileName); ext != "" {
			name += ext
		}
		full := filepath.Join(dir, name)
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
