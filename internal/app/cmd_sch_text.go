package app

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

func newSchTextCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	group := &cobra.Command{Use: "text", Short: "Free schematic text operations"}
	var content string
	var x, y, fontSize float64
	var rotation int
	cmd := &cobra.Command{
		Use: "create", Short: "Create one free text and verify fresh active-page readback",
		Long: `Create exactly one text on the active real schematic page. Content preserves
Chinese and LF newlines (at most 16000 Unicode characters, nonblank, no NUL).
Coordinates and font size use schematic raw units (0.01 inch), Y up.
Only rotation 0/90/180/270 is supported. No other style controls are accepted.
Uncertain or partial results return a nonzero exit with the original response.
Never retry automatically: first inspect fresh text-list on the exact target page.
This does not prove persistence; explicitly save, reload and read back afterwards.`,
		Example: "  easyeda sch text create --content \"模块说明\" --x 0 --y 20 --font-size 10 --rotation 90 --window <id>",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, key := range []string{"content", "x", "y"} {
				if !cmd.Flags().Changed(key) {
					return fmt.Errorf("--%s is required", key)
				}
			}
			if !utf8.ValidString(content) || strings.TrimSpace(content) == "" || strings.ContainsRune(content, 0) || utf8.RuneCountInString(content) > 16000 {
				return fmt.Errorf("--content must be nonblank valid Unicode, no NUL, at most 16000 characters")
			}
			finite := func(n float64) bool { return !math.IsNaN(n) && !math.IsInf(n, 0) }
			if !finite(x) || !finite(y) {
				return fmt.Errorf("--x/--y must be finite")
			}
			if rotation != 0 && rotation != 90 && rotation != 180 && rotation != 270 {
				return fmt.Errorf("--rotation must be 0, 90, 180 or 270")
			}
			payload := map[string]any{"content": content, "x": x, "y": y, "rotation": rotation}
			if cmd.Flags().Changed("font-size") {
				if !finite(fontSize) || fontSize <= 0 {
					return fmt.Errorf("--font-size must be positive and finite")
				}
				payload["fontSize"] = fontSize
			}
			// Single request, keeping the original envelope and known IDs even on partial.
			raw, err := postAction(cfg, "schematic.text.create", *window, payload, defaultActionTimeout)
			if len(raw) > 0 {
				_, _ = stdout.Write(raw)
				if raw[len(raw)-1] != '\n' {
					fmt.Fprintln(stdout)
				}
				printArtifactPaths(raw, stderr)
			}
			if err != nil {
				return fmt.Errorf("schematic.text.create: %w; inspect fresh target-page texts before any retry", err)
			}
			var response struct {
				OK     bool           `json:"ok"`
				Result map[string]any `json:"result"`
			}
			if err := json.Unmarshal(raw, &response); err != nil {
				return fmt.Errorf("schematic.text.create: invalid response; do not retry automatically: %w", err)
			}
			if !response.OK {
				return errActionFailed
			}
			return schTextCreateResultError(response.Result)
		},
	}
	cmd.Flags().StringVar(&content, "content", "", "Text content, Chinese and LF preserved (required; max 16000 Unicode characters)")
	cmd.Flags().Float64Var(&x, "x", 0, "Explicit X in schematic raw units; zero is valid (required)")
	cmd.Flags().Float64Var(&y, "y", 0, "Explicit Y in schematic raw units, Y up; zero is valid (required)")
	cmd.Flags().Float64Var(&fontSize, "font-size", 0, "Optional positive finite font size in schematic raw units")
	cmd.Flags().IntVar(&rotation, "rotation", 0, "Rotation: 0, 90, 180 or 270 degrees")
	group.AddCommand(cmd)
	return group
}

func schTextCreateResultError(result map[string]any) error {
	id, _ := result["primitiveId"].(string)
	if result["verified"] != true || result["partial"] != false || strings.TrimSpace(id) == "" || result["knownID"] != id {
		return fmt.Errorf("schematic.text.create: not verified; preserve returned knownID and fresh target-page state; do not retry or delete automatically")
	}
	return nil
}
