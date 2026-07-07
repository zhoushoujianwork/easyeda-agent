package protocol

import "time"

type Envelope struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Version   string    `json:"version"`
	WindowID  string    `json:"windowId,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

type Request struct {
	Envelope
	Action string `json:"action"`
	// Project is an optional stable routing hint: a project name or uuid the
	// daemon resolves to the current windowId. Use instead of WindowID when the
	// ephemeral windowId churns (reconnects) — multi-window/multi-agent routing.
	Project string `json:"project,omitempty"`
	// OutputDir is the CLI's working directory. The daemon (which has its own,
	// different cwd) writes artifacts under <OutputDir>/.easyeda/artifacts so
	// screenshots/exports land in the user's project, not the daemon's. Empty for
	// callers that don't set it (the daemon then falls back to its ArtifactDir).
	OutputDir string `json:"outputDir,omitempty"`
	// TimeoutMs is the caller's round-trip budget. The daemon shortens its own
	// connector wait to (TimeoutMs - grace) so the caller receives a structured
	// DISPATCH_FAILED instead of a raw HTTP timeout when the connector hangs
	// (e.g. DRC recompute on a background window never finishes). 0 = daemon
	// default.
	TimeoutMs int            `json:"timeoutMs,omitempty"`
	Payload   map[string]any `json:"payload,omitempty"`
}

type Response struct {
	Envelope
	OK        bool           `json:"ok"`
	Result    map[string]any `json:"result,omitempty"`
	Context   *Context       `json:"context,omitempty"`
	Artifacts []Artifact     `json:"artifacts,omitempty"`
	Warnings  []string       `json:"warnings,omitempty"`
	Error     *ErrorInfo     `json:"error,omitempty"`
}

type Context struct {
	ProjectUUID  string `json:"projectUuid,omitempty"`
	ProjectName  string `json:"projectName,omitempty"`
	DocumentUUID string `json:"documentUuid,omitempty"`
	DocumentType string `json:"documentType,omitempty"`
	TabID        string `json:"tabId,omitempty"`
	Unit         string `json:"unit,omitempty"`
}

type Artifact struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Path     string `json:"path,omitempty"`
	FileName string `json:"fileName,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Size     int64  `json:"size,omitempty"`
	SHA256   string `json:"sha256,omitempty"`

	// InlineBase64 carries the artifact bytes from the connector, which cannot
	// write to the daemon's disk. The daemon decodes it, persists the file, fills
	// Path/Size/SHA256, and clears this field before returning to the caller.
	InlineBase64 string `json:"inlineBase64,omitempty"`
}

type ErrorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Detail  string `json:"detail,omitempty"`
}
