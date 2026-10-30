package app

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

func newProjectDrawingsCmd(stdout io.Writer) *cobra.Command {
	var from, out string
	c := &cobra.Command{
		Use:   "drawings",
		Short: "Package verified local drawing inputs without accessing EasyEDA",
		Long: `Create a portable drawing directory from a versioned JSON specification.
This single-schematic-page, single-PCB command is entirely offline: it copies official exports byte-for-byte,
checks file hashes and readback identities, verifies actual-render SVG metadata,
and writes a one-page README plus manifest. It never accepts a candidate SVG as
an actual readback render and never infers design acceptance from generation.

The specification uses schemaVersion 1; title, revision, projectUuid,
schematicDocumentUuid, pcbDocumentUuid; readbacks.schematic and readbacks.pcb
with path, sha256 and JSON-pointer identity/time bindings; artifacts with role,
path, sha256 and optional capture evidence; and explicit status. Required roles:
schematic-svg, schematic-png, pcb-official-top-png, pcb-official-bottom-png,
pcb-actual-top-svg, pcb-actual-bottom-svg. schematic-pdf is optional.
status.pcbReview declares labelDensity, textCollisions and copperGeometry separately:
pending (also the default when omitted), pass or fail. designAccepted=true requires
explicit pass for all three; packaging does not detect label or copper overlaps.
Read the synthetic example at internal/app/testdata/project-drawings/spec.json.
Relative source paths are resolved beside the spec and cannot contain '..'.
The output directory must not exist; its parent must exist. Source symlinks,
external SVG resources, malformed images and mismatched evidence are rejected.`,
		Args:    cobra.NoArgs,
		Example: "  easyeda project drawings --from drawings-spec.json --out ./drawings-r1",
		RunE: func(cmd *cobra.Command, args []string) error {
			if from == "" || out == "" {
				return fmt.Errorf("--from and --out are required")
			}
			manifest, err := generateProjectDrawings(from, out)
			if err != nil {
				return err
			}
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(manifest)
		},
	}
	c.Flags().StringVar(&from, "from", "", "local schemaVersion 1 drawing specification JSON (required)")
	c.Flags().StringVar(&out, "out", "", "new output directory; existing paths are never overwritten (required)")
	return c
}
