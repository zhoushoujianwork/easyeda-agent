package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
)

func newPcbConfigCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	group := &cobra.Command{
		Use: "config", Short: "Read and configure real PCB design rules (配置设置)",
		Long: `Read or update the active PCB's design rules through typed EDA actions.
Preserves unspecified settings. Values default to mil; --unit mm converts to
the rule's storage unit. Every write supports --dry-run and readback.
This changes rules, not existing tracks/vias or global editor preferences.
Use --project and --doc to select the target. Save, reload and read back to
verify persistence. See pcb stackup/origin and silk-add/silk-set for other
exam settings. Grid/snap/global preferences are currently unsupported.`,
		Example: `  easyeda pcb config get --project ceshi
  easyeda pcb config track --name copperThickness1oz --min 8 --default 8 --dry-run --project ceshi --doc PCB1`,
	}
	group.AddCommand(&cobra.Command{
		Use: "get", Short: "Export current rules, net classes and assignments", Args: cobra.NoArgs,
		Example: "  easyeda pcb config get --project ceshi > config-before.json",
		RunE: func(cmd *cobra.Command, args []string) error {
			return dispatch(cfg, "pcb.config.get", *window, nil, stdout, stderr)
		},
	})
	group.AddCommand(newPcbConfigNetColorCmd(cfg, window, stdout, stderr))
	type dimension struct{ flag, field, help string }
	for _, op := range []struct {
		kind, short, example string
		dimensions           []dimension
	}{
		{"clearance", "Set only Track-to-Track clearance across existing layer tables", "--name copperThickness1oz --track-to-track 6", []dimension{{"track-to-track", "trackToTrack", "Track-to-Track minimum clearance"}}},
		{"track", "Set track width limits, or copy an existing rule into a new named rule", "--name PWR --copy-from copperThickness1oz --min 8 --default 20", []dimension{{"min", "min", "minimum track width"}, {"default", "default", "default track width"}, {"max", "max", "maximum track width"}}},
		{"via", "Set via outer/hole diameter limits without changing existing vias", "--name viaSize --min-outer 24 --min-hole 12", []dimension{{"min-outer", "minOuter", "minimum outer diameter"}, {"default-outer", "defaultOuter", "default outer diameter"}, {"max-outer", "maxOuter", "maximum outer diameter"}, {"min-hole", "minHole", "minimum hole diameter"}, {"default-hole", "defaultHole", "default hole diameter"}, {"max-hole", "maxHole", "maximum hole diameter"}}},
		{"bind", "Assign a track rule to an existing net class and every member", "--class PWR_Class --track-rule PWR", nil},
	} {
		op := op
		var ruleName, unit, copyFrom, className, trackRule string
		var dryRun bool
		values := map[string]*float64{}
		cmd := &cobra.Command{
			Use: op.kind, Short: op.short, Args: cobra.NoArgs,
			Long: op.short + `.
Read current state, preview or write a parameterized patch, then verify readback.
Unknown host schemas/units fail before writing. Unspecified fields stay intact.
Track/clearance update all existing layer-table entries. New track rules require
--copy-from and do not become the default rule. Bind requires a non-empty class
created using "pcb net-class create"; it does not alter members.
Use pcb save, doc reload, config get to verify persistence.`,
			Example: "  easyeda pcb config " + op.kind + " " + op.example + " --dry-run --project ceshi --doc PCB1\n  easyeda pcb config " + op.kind + " " + op.example + " --project ceshi --doc PCB1",
			RunE: func(cmd *cobra.Command, args []string) error {
				payload := map[string]any{"kind": op.kind, "dryRun": dryRun}
				if op.kind == "bind" {
					if strings.TrimSpace(className) == "" || strings.TrimSpace(trackRule) == "" {
						return fmt.Errorf("--class and --track-rule must be non-empty")
					}
					payload["netClass"], payload["trackRule"] = className, trackRule
				} else {
					if strings.TrimSpace(ruleName) == "" {
						return fmt.Errorf("--name must be non-empty")
					}
					if unit != "mil" && unit != "mm" {
						return fmt.Errorf("--unit must be mil or mm")
					}
					payload["name"], payload["unit"] = ruleName, unit
					n := 0
					for _, d := range op.dimensions {
						if !cmd.Flags().Changed(d.flag) {
							continue
						}
						v := *values[d.flag]
						if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
							return fmt.Errorf("--%s must be finite and positive", d.flag)
						}
						payload[d.field] = v
						n++
					}
					if n == 0 {
						return fmt.Errorf("provide at least one dimension to set")
					}
					if cmd.Flags().Changed("copy-from") {
						if strings.TrimSpace(copyFrom) == "" {
							return fmt.Errorf("--copy-from must be non-empty")
						}
						payload["copyFrom"] = copyFrom
					}
				}
				if !dryRun {
					fmt.Fprintln(stderr, knownBugPCBConfig)
				}
				var response bytes.Buffer
				err := dispatch(cfg, "pcb.config.set", *window, payload, &response, stderr)
				if _, writeErr := stdout.Write(response.Bytes()); writeErr != nil {
					return writeErr
				}
				if err != nil {
					return err
				}
				return checkPcbConfigResponse(response.Bytes(), dryRun)
			},
		}
		cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show before/requested/changes without writing")
		if op.kind == "bind" {
			cmd.Flags().StringVar(&className, "class", "", "existing net class (required)")
			cmd.Flags().StringVar(&trackRule, "track-rule", "", "existing named track rule (required)")
			_ = cmd.MarkFlagRequired("class")
			_ = cmd.MarkFlagRequired("track-rule")
		} else {
			cmd.Flags().StringVar(&ruleName, "name", "", "exact rule name from config get (required)")
			_ = cmd.MarkFlagRequired("name")
			cmd.Flags().StringVar(&unit, "unit", "mil", "input dimensions: mil or mm; converted to the stored unit")
			for _, d := range op.dimensions {
				values[d.flag] = cmd.Flags().Float64(d.flag, 0, d.help+" (in --unit)")
			}
			if op.kind == "track" {
				cmd.Flags().StringVar(&copyFrom, "copy-from", "", "existing track rule to clone if --name does not exist")
			}
		}
		group.AddCommand(cmd)
	}
	return group
}

func newPcbConfigNetColorCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var net, color string
	var dryRun bool
	cmd := &cobra.Command{
		Use: "net-color", Short: "Set one network's RGB color, preserving transparency",
		Args:    cobra.NoArgs,
		Example: "  easyeda pcb config net-color --net +5V --color '#FF8000' --dry-run --project ceshi --doc PCB1",
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(net) == "" || !regexp.MustCompile(`^#[0-9a-fA-F]{6}$`).MatchString(color) {
				return fmt.Errorf("provide --net and --color '#RRGGBB'")
			}
			var response bytes.Buffer
			err := dispatch(cfg, "pcb.net.color.set", *window, map[string]any{"net": net, "color": color, "dryRun": dryRun}, &response, stderr)
			if _, writeErr := stdout.Write(response.Bytes()); writeErr != nil {
				return writeErr
			}
			if err != nil {
				return err
			}
			return checkPcbConfigResponse(response.Bytes(), dryRun)
		},
	}
	cmd.Flags().StringVar(&net, "net", "", "existing exact net name (required)")
	cmd.Flags().StringVar(&color, "color", "", "RGB hex including #: #RRGGBB (required)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "read and show requested color without writing")
	_ = cmd.MarkFlagRequired("net")
	_ = cmd.MarkFlagRequired("color")
	return cmd
}

func checkPcbConfigResponse(data []byte, dryRun bool) error {
	var response struct {
		OK     bool           `json:"ok"`
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return fmt.Errorf("decode config readback: %w", err)
	}
	result := response.Result
	if !response.OK || result["partial"] == true || result["writeFailed"] == true {
		return fmt.Errorf("PCB configuration failed or partially applied; inspect response before retrying")
	}
	key := "verified"
	if dryRun {
		key = "dryRun"
	}
	if result[key] != true {
		return fmt.Errorf("PCB configuration %s was not confirmed; inspect response before continuing", key)
	}
	return nil
}
