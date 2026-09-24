package app

import (
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"
)

// web reload refreshes the top-level EasyEDA Web page, unlike doc reload's
// save/close/open of one editor tab. The connector acknowledges the scheduled
// refresh before its WebSocket disappears; success requires a new registration.
func newWebCmd(cfg *appConfig, stdout io.Writer) *cobra.Command {
	var window string
	var timeout time.Duration
	web := &cobra.Command{Use: "web", Short: "Control the connected EasyEDA Web editor page"}
	web.PersistentFlags().StringVar(&window, "window", "", "current window ID; --project UUID remains required after reconnect")
	reload := &cobra.Command{
		Use:     "reload",
		Short:   "Save the active document, refresh the whole Web page, and verify reconnect",
		Long:    "Save the exact active document, schedule a full browser-page refresh through the typed connector, then wait for a NEW connector registration on the same project and document. Other open documents must already be saved. Unlike doc reload, this restarts the Web editor and connector runtime. The JSON result includes elapsed milliseconds; timeout is a failure, never a success receipt.",
		Example: "  easyeda web reload --project <project-uuid> --doc <document-uuid> --timeout 30s",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cfg.project == "" || cfg.doc == "" {
				return fmt.Errorf("web reload requires exact --project and --doc UUIDs")
			}
			if timeout < time.Second || timeout > 2*time.Minute {
				return fmt.Errorf("--timeout must be between 1s and 2m")
			}
			report, err := reloadWebPage(cfg, window, timeout)
			if err != nil {
				return err
			}
			return writeJSON(stdout, report)
		},
	}
	reload.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "maximum time to wait for a verified new connector registration")
	web.AddCommand(reload)
	return web
}

func reloadWebPage(cfg *appConfig, window string, timeout time.Duration) (map[string]any, error) {
	started := time.Now()
	oldWindow, err := resolveTargetWindow(cfg, window)
	if err != nil {
		return nil, err
	}
	// Do not let the global --doc guard switch the editor while inspecting it:
	// this command requires the requested document to ALREADY be foreground.
	pinned := *cfg
	pinned.doc = ""
	project, err := requestAction(&pinned, "project.current", oldWindow, nil)
	if err != nil {
		return nil, fmt.Errorf("read current project before Web reload: %w", err)
	}
	if project.Result["uuid"] != cfg.project {
		return nil, fmt.Errorf("Web reload refused: --project must be the exact current UUID (%v)", project.Result["uuid"])
	}
	doc, err := requestAction(&pinned, "document.current", oldWindow, nil)
	if err != nil {
		return nil, fmt.Errorf("read current document before Web reload: %w", err)
	}
	if doc.Result["uuid"] != cfg.doc || doc.Context == nil || doc.Context.DocumentUUID != cfg.doc || doc.Context.ProjectUUID != cfg.project {
		return nil, fmt.Errorf("Web reload refused: --doc must be the exact active UUID (%v)", doc.Result["uuid"])
	}
	docType, _ := doc.Result["documentType"].(string)
	saveAction := "schematic.save"
	if docType == "pcb" {
		saveAction = "pcb.save"
	} else if docType != "schematic" {
		return nil, fmt.Errorf("Web reload refused: unsupported active document type %q", docType)
	}
	saveStarted := time.Now()
	saved, err := requestAction(&pinned, saveAction, oldWindow, nil)
	if err != nil {
		return nil, fmt.Errorf("save active document before Web reload: %w", err)
	}
	if saved.Result["saved"] != true {
		return nil, fmt.Errorf("Web reload refused: %s did not return saved:true", saveAction)
	}
	if saved.Context == nil || saved.Context.ProjectUUID != cfg.project || saved.Context.DocumentUUID != cfg.doc {
		return nil, fmt.Errorf("Web reload refused: save response context no longer matches the requested project/document")
	}
	saveMs := time.Since(saveStarted).Milliseconds()
	trigger, err := requestAction(&pinned, "system.page_reload", oldWindow, map[string]any{
		"projectUuid": cfg.project, "documentUuid": cfg.doc,
	})
	if err != nil {
		return nil, fmt.Errorf("schedule Web page reload: %w", err)
	}
	if trigger.Result["scheduled"] != true {
		return nil, fmt.Errorf("Web page reload action did not confirm scheduling")
	}
	scheduledAt := time.Now()
	deadline := scheduledAt.Add(timeout)
	for time.Now().Before(deadline) {
		windows, healthErr := listWindows(&pinned)
		if healthErr == nil {
			for _, candidate := range windows {
				if candidate.WindowID == oldWindow || candidate.Context.ProjectUUID != cfg.project || candidate.Context.DocumentUUID != cfg.doc {
					continue
				}
				current, currentErr := requestAction(&pinned, "document.current", candidate.WindowID, nil)
				if currentErr != nil || current.Result["uuid"] != cfg.doc || current.Context == nil || current.Context.DocumentUUID != cfg.doc || current.Context.ProjectUUID != cfg.project {
					continue
				}
				if !waitDocSettleFor(&pinned, candidate.WindowID, docType) {
					return nil, fmt.Errorf("Web page reconnected as %s, but %s objects did not settle; treat readback as unavailable", candidate.WindowID, docType)
				}
				return map[string]any{
					"reloaded": true, "saved": true, "ready": true,
					"projectUuid": cfg.project, "documentUuid": cfg.doc, "documentType": docType,
					"oldWindowId": oldWindow, "newWindowId": candidate.WindowID,
					"saveMs": saveMs, "reconnectMs": time.Since(scheduledAt).Milliseconds(),
					"elapsedMs": time.Since(started).Milliseconds(),
				}, nil
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	return nil, fmt.Errorf("Web page reload was scheduled, but no new connector with project %s and document %s became readable within %s; state is unknown", cfg.project, cfg.doc, timeout)
}
