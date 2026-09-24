package app

import (
	"fmt"
	"io"
	"time"

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
	proj.AddCommand(newProjectExportSourceCmd(cfg, stdout, stderr, &window))

	proj.AddCommand(
		func() *cobra.Command {
			var friendlyName, teamUUID string
			var timeout time.Duration
			c := &cobra.Command{
				Use:     "find",
				Short:   "Find projects by exact friendly name without opening them",
				Long:    "Read official project UUIDs and project info. With --team, the API inventories that team's root folder only. An empty or incomplete enumeration reports unknown, not absent; absence is only established within a complete team-root inventory.",
				Args:    cobra.NoArgs,
				Example: `  easyeda project find --window <window-id> --name "AT32F415 demo" --team <team-uuid>`,
				RunE: func(cmd *cobra.Command, args []string) error {
					if friendlyName == "" {
						return fmt.Errorf("--name is required")
					}
					if timeout < 5*time.Second || timeout > 10*time.Minute {
						return fmt.Errorf("--timeout must be between 5s and 10m")
					}
					payload := map[string]any{"friendlyName": friendlyName}
					if teamUUID != "" {
						payload["teamUuid"] = teamUUID
					}
					return dispatchTimed(cfg, "project.find", window, payload, timeout, stdout, stderr)
				},
			}
			c.Flags().StringVar(&friendlyName, "name", "", "exact project friendly name (required)")
			c.Flags().StringVar(&teamUUID, "team", "", "exact owning team UUID; scopes inventory to its root folder")
			// The official inventory needs one detail read per UUID. A real 52-project
			// team takes ~19s, exceeding the common 20s budget minus daemon grace.
			c.Flags().DurationVar(&timeout, "timeout", 90*time.Second, "total lookup wait budget (5s to 10m); incomplete inventory never proves absence")
			return c
		}(),
		func() *cobra.Command {
			var friendlyName, projectName, teamUUID, folderUUID, description string
			var open bool
			c := &cobra.Command{
				Use:     "create",
				Short:   "Create an EasyEDA project container through the official API",
				Args:    cobra.NoArgs,
				Example: `  easyeda project create --name "AT32F415 demo" --open`,
				RunE: func(cmd *cobra.Command, args []string) error {
					if friendlyName == "" {
						return fmt.Errorf("--name is required")
					}
					payload := map[string]any{"friendlyName": friendlyName, "open": open}
					if projectName != "" {
						payload["projectName"] = projectName
					}
					if teamUUID != "" {
						payload["teamUuid"] = teamUUID
					}
					if folderUUID != "" {
						payload["folderUuid"] = folderUUID
					}
					if description != "" {
						payload["description"] = description
					}
					return dispatch(cfg, "project.create", window, payload, stdout, stderr)
				},
			}
			c.Flags().StringVar(&friendlyName, "name", "", "project display name (required)")
			c.Flags().StringVar(&projectName, "internal-name", "", "optional internal project name")
			c.Flags().StringVar(&teamUUID, "team", "", "target team/workspace UUID")
			c.Flags().StringVar(&folderUUID, "folder", "", "target folder UUID")
			c.Flags().StringVar(&description, "description", "", "project description")
			c.Flags().BoolVar(&open, "open", false, "open the newly created project")
			return c
		}(),

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
			var uuid, projectUUID, pageUUID string
			var discard bool
			c := &cobra.Command{
				Use:     "open",
				Short:   "Open a project by --project-uuid, or a legacy document by --uuid",
				Args:    cobra.NoArgs,
				Example: `  easyeda project open --uuid 6b3a2f01-...`,
				RunE: func(cmd *cobra.Command, args []string) error {
					if projectUUID != "" {
						if uuid != "" {
							return fmt.Errorf("use only --project-uuid or --uuid")
						}
						if !discard {
							return fmt.Errorf("openProject can discard unsaved data; save all documents first, then explicitly pass --allow-discard-unsaved")
						}
						res, value, err := projectTransferRequest(cfg, window, "open", projectUUID, pageUUID)
						if err != nil {
							return err
						}
						return encodeResultEnvelope(res, value, stdout)
					}
					if pageUUID != "" {
						return fmt.Errorf("--page-uuid requires --project-uuid")
					}
					if uuid == "" {
						return fmt.Errorf("--project-uuid or legacy --uuid is required")
					}
					return dispatch(cfg, "document.open", window,
						map[string]any{"uuid": uuid}, stdout, stderr)
				},
			}
			c.Flags().StringVar(&uuid, "uuid", "", "legacy: document UUID within the current project")
			c.Flags().StringVar(&projectUUID, "project-uuid", "", "project UUID to open and verify")
			c.Flags().StringVar(&pageUUID, "page-uuid", "", "optional schematic page UUID; wait for its tree and verify active page")
			c.Flags().BoolVar(&discard, "allow-discard-unsaved", false, "acknowledge official API may discard unsaved data; save all documents first")
			return c
		}(),
	)

	proj.AddCommand(newProjectExportCmd(cfg, &window, stdout))
	return proj
}
