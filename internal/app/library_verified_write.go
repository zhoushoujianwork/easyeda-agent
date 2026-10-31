package app

import "fmt"

// Keep the complete partial response so neither retries nor downstream binding
// can hide a library authoring failure.
type unverifiedLibraryWriteError struct {
	action      string
	rawResponse string
	cause       error
}

func (e *unverifiedLibraryWriteError) Error() string {
	message := fmt.Sprintf("%s: library write not verified; preserve the response and fresh-read before any retry", e.action)
	if e.cause != nil {
		message += ": " + e.cause.Error()
	}
	return message
}

func (e *unverifiedLibraryWriteError) Unwrap() error { return e.cause }

func isVerifiedLibraryWrite(action string) bool {
	switch action {
	case "library.footprint.build", "schematic.rebind.footprint", "library.cbb.create", "library.cbb.delete", "library.cbb.open", "library.cbb.symbol_open", "library.cbb.symbol_build", "library.cbb.symbol_rectangle_delete", "schematic.cbb.place", "schematic.cbb.project_place":
		return true
	default:
		return false
	}
}

// A timeout, rejected envelope or undecodable response does not prove that a
// library mutation never ran. Stop every orchestration recovery path until a
// human/fresh read resolves the outcome, while retaining the original cause.
func libraryWriteFailureError(action string, raw []byte, cause error) error {
	if !isVerifiedLibraryWrite(action) {
		return cause
	}
	return &unverifiedLibraryWriteError{action: action, rawResponse: string(raw), cause: cause}
}

func libraryWriteResultError(action string, result map[string]any, raw []byte) error {
	if !isVerifiedLibraryWrite(action) {
		return nil
	}
	if result["verified"] != true || result["partial"] == true {
		return libraryWriteFailureError(action, raw, nil)
	}
	return nil
}
