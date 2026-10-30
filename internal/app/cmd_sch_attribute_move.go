package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"reflect"
	"strings"

	"github.com/spf13/cobra"
)

var schAttributeStateFields = []string{"primitiveId", "X", "Y", "Rotation", "Color", "FontName", "FontSize", "Bold", "Italic", "UnderLine", "AlignMode", "FillColor", "Key", "Value", "KeyVisible", "ValueVisible", "ParentPrimitiveId"}

func newSchAttributeMoveCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var from string
	cmd := &cobra.Command{
		Use: "attribute-move --from <move.json>", Short: "Move one bound part or sheet text attribute using x/y only",
		Long: `Move one existing placed part's Name or Designator attribute. Explicit
ownerType:"sheet" instead allows the sheet's Name, Description or Drawed text.
The actual native parent must match ownerType (default "part"). The JSON file
contains projectUuid, documentUuid, primitiveId, finite raw x/y (Y-up), and
expected: the complete original pagePrimitives.attributes record. Preserve raw
formula Value text; do not replace it with rendered SVG text.

Only x/y are sent to the official modify API. Fresh before/after reads verify
that text, visibility, style and parent remain unchanged. Stale or incomplete
state is refused. Partial writes retain evidence and are never retried. Follow
with official image review and explicit save/reload/fresh readback; this command
does not prove visual clearance or persistence.`,
		Example: "  easyeda sch attribute-move --from move.json --project tracker --doc <page-uuid>",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(from) == "" {
				return fmt.Errorf("--from is required")
			}
			data, err := os.ReadFile(from)
			if err != nil {
				return err
			}
			var payload map[string]any
			if err := json.Unmarshal(data, &payload); err != nil {
				return err
			}
			if err := validateSchAttributeMovePayload(payload); err != nil {
				return err
			}
			return dispatch(cfg, "schematic.attribute.move", *window, payload, stdout, stderr)
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "JSON parameter file with exact project/document/attribute identity, complete expected state, and x/y")
	return cmd
}

func schAttributeFinite(value any) bool {
	number, ok := value.(float64)
	return ok && !math.IsNaN(number) && !math.IsInf(number, 0)
}

func validateSchAttributeState(state map[string]any) error {
	return validateSchAttributeStateOwner(state, "part")
}

func validateSchAttributeStateOwner(state map[string]any, owner string) error {
	if len(state) != len(schAttributeStateFields) {
		return fmt.Errorf("attribute expected state must contain every native field")
	}
	for _, key := range schAttributeStateFields {
		value, present := state[key]
		if !present {
			return fmt.Errorf("attribute state missing %s", key)
		}
		switch key {
		case "X", "Y":
			if !schAttributeFinite(value) {
				return fmt.Errorf("attribute %s must be finite", key)
			}
		case "Rotation", "FontSize", "AlignMode":
			if value != nil && !schAttributeFinite(value) {
				return fmt.Errorf("attribute %s must be numeric or null", key)
			}
		case "Bold", "Italic", "UnderLine", "KeyVisible", "ValueVisible":
			if value != nil {
				if _, ok := value.(bool); !ok {
					return fmt.Errorf("attribute %s must be boolean or null", key)
				}
			}
		default:
			if value == nil && (key == "Color" || key == "FontName" || key == "FillColor") {
				continue
			}
			if _, ok := value.(string); !ok {
				return fmt.Errorf("attribute %s must be a string", key)
			}
		}
	}
	key := state["Key"]
	allowed := owner == "part" && (key == "Name" || key == "Designator") || owner == "sheet" && (key == "Name" || key == "Description" || key == "Drawed")
	if strings.TrimSpace(strField(state, "primitiveId")) == "" || strings.TrimSpace(strField(state, "ParentPrimitiveId")) == "" || !allowed {
		return fmt.Errorf("attribute requires complete identity and a supported key for ownerType %s", owner)
	}
	return nil
}

func validateSchAttributeMovePayload(payload map[string]any) error {
	owner := "part"
	if value, present := payload["ownerType"]; present {
		if value != "part" && value != "sheet" {
			return fmt.Errorf("attribute move ownerType must be part or sheet")
		}
		owner = value.(string)
	}
	wantFields := 6
	if _, present := payload["ownerType"]; present {
		wantFields++
	}
	if len(payload) != wantFields {
		return fmt.Errorf("attribute move requires projectUuid, documentUuid, primitiveId, expected, x and y, with optional ownerType only")
	}
	for _, key := range []string{"projectUuid", "documentUuid", "primitiveId"} {
		if strings.TrimSpace(strField(payload, key)) == "" {
			return fmt.Errorf("attribute move requires %s", key)
		}
	}
	if !schAttributeFinite(payload["x"]) || !schAttributeFinite(payload["y"]) {
		return fmt.Errorf("attribute move x/y must be finite raw coordinates")
	}
	expected, ok := payload["expected"].(map[string]any)
	if !ok {
		return fmt.Errorf("attribute move requires the complete expected state")
	}
	if err := validateSchAttributeStateOwner(expected, owner); err != nil {
		return err
	}
	if expected["primitiveId"] != payload["primitiveId"] {
		return fmt.Errorf("attribute move ID differs from expected state")
	}
	return nil
}

type unverifiedSchAttributeMoveError struct {
	cause       error
	rawResponse string
}

func (e *unverifiedSchAttributeMoveError) Error() string {
	return "schematic.attribute.move: not verified; retain response and fresh state, do not retry automatically: " + e.cause.Error()
}
func (e *unverifiedSchAttributeMoveError) Unwrap() error { return e.cause }

func schAttributeMoveFailure(err error, raw string) error {
	if err == nil {
		return nil
	}
	var typed *unverifiedSchAttributeMoveError
	if errors.As(err, &typed) {
		return err
	}
	return &unverifiedSchAttributeMoveError{cause: err, rawResponse: raw}
}

func schAttributeMoveResultError(action string, result map[string]any) error {
	if action != "schematic.attribute.move" {
		return nil
	}
	raw, _ := json.Marshal(result)
	fail := &unverifiedSchAttributeMoveError{cause: fmt.Errorf("coordinates or preserved fields differ"), rawResponse: string(raw)}
	if result["scope"] != "attribute-position-only" || result["verified"] != true || result["partial"] != false || result["preserved"] != true {
		return fail
	}
	before, beforeOK := result["before"].(map[string]any)
	after, afterOK := result["after"].(map[string]any)
	req, reqOK := result["requested"].(map[string]any)
	owner := "part"
	if value, present := result["ownerType"]; present {
		owner, _ = value.(string)
	}
	if !beforeOK || !afterOK || !reqOK || len(req) != 2 || validateSchAttributeStateOwner(before, owner) != nil || validateSchAttributeStateOwner(after, owner) != nil || result["primitiveId"] != before["primitiveId"] {
		return fail
	}
	for _, key := range schAttributeStateFields {
		if key == "X" || key == "Y" {
			want := req[strings.ToLower(key)]
			if !schAttributeFinite(want) || !reflect.DeepEqual(after[key], want) {
				return fail
			}
		} else if !reflect.DeepEqual(before[key], after[key]) {
			return fail
		}
	}
	return nil
}
