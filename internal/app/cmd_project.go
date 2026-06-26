package app

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// newProjectCmd returns the "project" subcommand group.
func newProjectCmd(cfg *appConfig, stdout, stderr io.Writer) *cobra.Command {
	var window string

	proj := &cobra.Command{
		Use:   "project",
		Short: "Read EasyEDA project and document context",
	}
	proj.PersistentFlags().StringVar(&window, "window", "", "EasyEDA window ID")

	proj.AddCommand(
		// project info → project.current
		&cobra.Command{
			Use:   "info",
			Short: "Read current EasyEDA project information",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				return dispatch(cfg, "project.current", window, nil, stdout, stderr)
			},
		},

		// project doc → document.current
		&cobra.Command{
			Use:   "doc",
			Short: "Read active editor document and schematic page context",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				return dispatch(cfg, "document.current", window, nil, stdout, stderr)
			},
		},

		// project open --uuid <uuid> → document.open
		func() *cobra.Command {
			var uuid string
			c := &cobra.Command{
				Use:     "open",
				Short:   "Open a document (schematic page or PCB) by UUID",
				Args:    cobra.NoArgs,
				Example: `  easyeda project open --uuid 6b3a2f01-...`,
				RunE: func(cmd *cobra.Command, args []string) error {
					if uuid == "" {
						return fmt.Errorf("--uuid is required")
					}
					return dispatch(cfg, "document.open", window,
						map[string]any{"uuid": uuid}, stdout, stderr)
				},
			}
			c.Flags().StringVar(&uuid, "uuid", "", "document UUID to open (required)")
			return c
		}(),
	)

	return proj
}
