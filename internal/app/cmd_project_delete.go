package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"
)

func newProjectDeleteCmd(cfg *appConfig, stdout, stderr io.Writer, window *string) *cobra.Command {
	var uuid, name, team string
	var dryRun, yes bool
	c := &cobra.Command{Use: "delete", Short: "Delete one inactive project after exact identity preflight (official internal API)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if uuid == "" || name == "" || *window == "" {
				return fmt.Errorf("--project-uuid, --expected-name and explicit --window are required")
			}
			if !dryRun && (!yes || team == "") {
				return fmt.Errorf("deletion requires --yes and --expected-team from a fresh --dry-run")
			}
			payload := map[string]any{"projectUuid": uuid, "expectedFriendlyName": name}
			if team != "" {
				payload["expectedTeamUuid"] = team
			}
			action := "project.delete_preview"
			if !dryRun {
				action = "project.delete"
				payload["confirmDelete"] = true
			}
			var output bytes.Buffer
			err := dispatchTimed(cfg, action, *window, payload, 90*time.Second, &output, stderr)
			if _, writeErr := stdout.Write(output.Bytes()); writeErr != nil {
				return writeErr
			}
			if err != nil {
				return err
			}
			if !dryRun && !projectDeletionVerified(output.Bytes()) {
				return fmt.Errorf("project deletion is unverified or partial; preserve the response and do not retry blindly")
			}
			return nil
		}}
	c.Flags().StringVar(&uuid, "project-uuid", "", "exact target project UUID; never the active reference project")
	c.Flags().StringVar(&name, "expected-name", "", "exact target friendly name")
	c.Flags().StringVar(&team, "expected-team", "", "exact owning team read from preflight")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "read-only identity and API-support preflight")
	c.Flags().BoolVar(&yes, "yes", false, "confirm deletion of this exact UUID/name/team")
	return c
}

func projectDeletionVerified(data []byte) bool {
	var response struct {
		Result map[string]any `json:"result"`
	}
	return json.Unmarshal(data, &response) == nil && response.Result["deleted"] == true && response.Result["verified"] == true && response.Result["partial"] == false
}
