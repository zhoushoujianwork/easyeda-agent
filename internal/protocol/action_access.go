package protocol

// ActionAccess describes effects independently of the historical Mutates flag.
// Consumers must reject unknown versions/categories rather than guess from names.
// Values are attached to each registration in actions.go and copied by value.
type ActionAccess struct {
	SchemaVersion int    `json:"schemaVersion"`
	Scope         string `json:"scope"`
	Effect        string `json:"effect"`
	Confirmation  string `json:"confirmation"`
	Verification  string `json:"verification"`
	Batch         string `json:"batch"`
	Adapter       string `json:"adapter,omitempty"`
}

var (
	accessSystemRead       = ActionAccess{1, "system", "read", "none", "none", "allowed", ""}
	accessSourceRead       = ActionAccess{1, "document", "read", "dedicated", "dedicated", "isolated", ""}
	accessDedicatedDesign  = ActionAccess{1, "document", "write", "dedicated", "dedicated", "isolated", ""}
	accessDocumentRead     = ActionAccess{1, "document", "read", "none", "none", "allowed", ""}
	accessProjectRead      = ActionAccess{1, "project", "read", "none", "none", "allowed", ""}
	accessLibraryRead      = ActionAccess{1, "library", "read", "none", "none", "allowed", ""}
	accessWorkspaceRead    = ActionAccess{1, "workspace", "read", "dedicated", "dedicated", "isolated", ""}
	accessDesignWrite      = ActionAccess{1, "document", "write", "design", "save-reload", "allowed", ""}
	accessIsolatedDesign   = ActionAccess{1, "document", "write", "design", "save-reload", "isolated", ""}
	accessLayoutWrite      = ActionAccess{1, "document", "write", "layout", "save-reload", "allowed", ""}
	accessBoardCopy        = ActionAccess{1, "project", "write", "design", "save-reload", "isolated", "board-copy"}
	accessLibraryWrite     = ActionAccess{1, "library", "write", "dedicated", "dedicated", "isolated", ""}
	accessEditorView       = ActionAccess{1, "editor", "view", "dedicated", "none", "isolated", ""}
	accessEditorLifecycle  = ActionAccess{1, "editor", "lifecycle", "dedicated", "dedicated", "isolated", ""}
	accessProjectLifecycle = ActionAccess{1, "project", "lifecycle", "dedicated", "dedicated", "isolated", ""}
	accessArtifact         = ActionAccess{1, "artifact", "export", "dedicated", "dedicated", "isolated", ""}
	accessScript           = ActionAccess{1, "system", "script", "dedicated", "dedicated", "isolated", ""}
)
