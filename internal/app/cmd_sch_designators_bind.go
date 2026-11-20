package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/zhoushoujianwork/easyeda-agent/internal/connectivity"
)

// A page whose parts were placed one by one carries no stable component
// binding, so preserve-instances cannot recognize them. Binding keeps every
// reference, pin and net exactly as read: the target is the page's own
// connectivity, where an unbound part takes the default cmp-<Ref> identity.
func schDesignatorsBindTarget(before schDesignatorBaseline) (connectivity.Document, error) {
	doc, err := connectivity.FromRead(before.Result)
	if err != nil {
		return doc, err
	}
	doc.ProjectID, doc.DocumentID = before.Context.ProjectUUID, before.Context.DocumentUUID
	return doc, nil
}

func newSchDesignatorsBindCmd(stdout io.Writer) *cobra.Command {
	var beforePath, targetOut, outPath string
	c := &cobra.Command{Use: "bind", Short: "Generate a guarded queue that binds stable component IDs to the page's existing parts without changing references or connectivity (offline)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if beforePath == "" || targetOut == "" || outPath == "" {
			return fmt.Errorf("--before, --target-out and --out are required")
		}
		if err := schDesignatorsDistinctFiles(beforePath, targetOut, outPath); err != nil {
			return err
		}
		beforeAbs, err := filepath.Abs(beforePath)
		if err != nil {
			return err
		}
		targetAbs, err := filepath.Abs(targetOut)
		if err != nil {
			return err
		}
		beforeRaw, err := os.ReadFile(beforeAbs)
		if err != nil {
			return err
		}
		before, err := parseSchDesignatorBaseline(beforeRaw)
		if err != nil {
			return err
		}
		target, err := schDesignatorsBindTarget(before)
		if err != nil {
			return err
		}
		if err = schDesignatorsWriteJSON(targetAbs, target, stdout); err != nil {
			return err
		}
		targetRaw, err := os.ReadFile(targetAbs)
		if err != nil {
			return err
		}
		pb, err := buildSchDesignatorsPlan(before, target, beforeAbs, targetAbs, sha256Hex(beforeRaw), sha256Hex(targetRaw))
		if err != nil {
			return err
		}
		refs := []string{}
		for _, step := range pb.Steps {
			// The bind target describes only this page; other pages must just not conflict.
			if step.Run == "sch designators verify" {
				step.Flags["page-scope"] = true
			}
			if step.Action == "schematic.component.modify" {
				if patch, ok := step.Payload["patch"].(map[string]any); ok {
					refs = append(refs, stringVal(patch["designator"]))
				}
			}
		}
		if len(refs) == 0 {
			return fmt.Errorf("every part already carries its stable component binding; nothing to bind")
		}
		outAbs, err := filepath.Abs(outPath)
		if err != nil {
			return err
		}
		if err = schDesignatorsWriteJSON(outAbs, pb, stdout); err != nil {
			return err
		}
		// A structured summary on stdout is the caller's proof that the
		// command finished and what it compiled.
		return schDesignatorsWriteJSON("", map[string]any{"ok": true, "bindings": refs, "playbook": outAbs, "target": targetAbs}, stdout)
	}}
	c.Flags().StringVar(&beforePath, "before", "", "fresh sch list envelope with pins, wires and official device identity (required)")
	c.Flags().StringVar(&targetOut, "target-out", "", "write the identity-only target the queue's guards verify against (required)")
	c.Flags().StringVar(&outPath, "out", "", "write guarded playbook (required)")
	return c
}
