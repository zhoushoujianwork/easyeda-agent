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
	// ForceReason explicitly overrides a workflow stage gate for THIS request
	// only (e.g. routing actions before outline_confirmed + pre_route_passed).
	// The daemon records it in the project's stage history so the bypass is
	// auditable, never silent. Empty = no override.
	ForceReason string `json:"forceReason,omitempty"`
	// TimeoutMs is the caller's round-trip budget. The daemon shortens its own
	// connector wait to (TimeoutMs - grace) so the caller receives a structured
	// DISPATCH_FAILED instead of a raw HTTP timeout when the connector hangs
	// (e.g. DRC recompute on a background window never finishes). 0 = daemon
	// default.
	TimeoutMs int `json:"timeoutMs,omitempty"`
	// ClientID identifies the calling client process for audit attribution and
	// the concurrent-writer advisory (issue #108): multiple CLIs/agents can
	// drive the same board through one daemon, and without an identity field
	// the audit log cannot say WHO replayed a stale plan. The CLI fills it once
	// per process as "<hostname>:<pid>[:<EASYEDA_CLIENT_LABEL>]". Optional —
	// raw HTTP callers that omit it simply stay unattributed.
	ClientID string         `json:"clientId,omitempty"`
	Payload  map[string]any `json:"payload,omitempty"`
}

type Response struct {
	Envelope
	OK        bool           `json:"ok"`
	Result    map[string]any `json:"result,omitempty"`
	Context   *Context       `json:"context,omitempty"`
	Artifacts []Artifact     `json:"artifacts,omitempty"`
	Warnings  []string       `json:"warnings,omitempty"`
	Error     *ErrorInfo     `json:"error,omitempty"`
	// StaleRisk is a daemon-attached, non-blocking advisory set on PCB read
	// actions (list/DRC/report …) that arrive after a PCB mutation but before a
	// `doc reload`: the per-document engine state may serve stale data (SKILL
	// iron rule 5). Purely additive — absent when there is no risk.
	StaleRisk string `json:"staleRisk,omitempty"`
	// ConcurrentWriter is a daemon-attached, non-blocking advisory set on a
	// mutating action when a DIFFERENT client mutated the same window recently
	// (issue #108): two clients driving one board with no mutex silently fight
	// each other. Purely additive — absent when the last writer is the same
	// client, the request is a read, or the last write is old enough to no
	// longer conflict.
	ConcurrentWriter string `json:"concurrentWriter,omitempty"`
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
