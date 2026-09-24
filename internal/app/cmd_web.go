package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"sort"
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
		Long:    "Save the exact active document, schedule a full browser-page refresh through the typed connector, then wait for a NEW connector registration in the same project. If the host restores a different document, one typed document.open may restore the requested document. Success requires consecutive fresh object reads matching the saved component ID baseline, stable object state, and a final document.current check. An empty baseline needs a longer stable-empty observation and proves only empty-page routing. Other open documents must already be saved. Unlike doc reload, this restarts the Web editor and connector runtime. The JSON result includes elapsed milliseconds; timeout is a failure, never a success receipt.",
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
	reload.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "maximum time for reconnect, optional one-time document restore, and stable readback")
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
	if doc.Context.TabID == "" || saved.Context.TabID != doc.Context.TabID {
		return nil, fmt.Errorf("Web reload refused: save response no longer matches the active document tab")
	}
	saveMs := time.Since(saveStarted).Milliseconds()
	// A component count from a half-loaded page can be stably zero. Freeze the
	// saved page's actual IDs before refreshing and require the same set later.
	baseline, err := requestAction(&pinned, settleProbeAction(docType), oldWindow, nil)
	if err != nil {
		return nil, fmt.Errorf("read saved component baseline before Web reload: %w", err)
	}
	_, baselineIDs, err := webReloadInventory(baseline, cfg.project, cfg.doc, docType, doc.Context.TabID)
	if err != nil {
		return nil, fmt.Errorf("Web reload refused: saved component baseline is unavailable: %w", err)
	}
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
	var candidateWindow, lastIssue string
	var opened, sawTarget bool
	var lastFingerprint [32]byte
	var lastTab string
	var firstStableAt time.Time
	stableSamples := 0
	for time.Now().Before(deadline) {
		windows, healthErr := webReloadWindows(&pinned, deadline)
		if healthErr != nil {
			lastIssue = healthErr.Error()
			stableSamples = 0
			webReloadPause(deadline)
			continue
		}
		found := false
		var fallback string
		for _, candidate := range windows {
			if candidate.WindowID == oldWindow || candidate.Context.ProjectUUID != cfg.project {
				continue
			}
			if fallback == "" {
				fallback = candidate.WindowID
			}
			if candidate.WindowID == candidateWindow {
				found = true
				break
			}
		}
		if !found && fallback != "" {
			// A later registration may replace the first one; never carry its
			// snapshot across windows or issue another restore request.
			candidateWindow, stableSamples = fallback, 0
			found = true
		}
		if !found {
			lastIssue = "no new connector registration in the requested project"
			stableSamples = 0
			webReloadPause(deadline)
			continue
		}
		current, currentErr := webReloadAction(&pinned, "document.current", candidateWindow, nil, deadline, 5*time.Second)
		if currentErr != nil {
			lastIssue = fmt.Sprintf("document.current: %v", currentErr)
			stableSamples = 0
			webReloadPause(deadline)
			continue
		}
		active, tab, identityErr := webReloadCurrentIdentity(current, cfg.project)
		if identityErr != nil {
			lastIssue = identityErr.Error()
			stableSamples = 0
			webReloadPause(deadline)
			continue
		}
		if active != cfg.doc {
			stableSamples = 0
			lastIssue = fmt.Sprintf("active document drifted to %s", active)
			if opened && sawTarget {
				return nil, fmt.Errorf("Web reload restored %s but it drifted to %s; readback is unavailable", cfg.doc, active)
			}
			if !opened {
				// An open request may complete after its response times out. Mark it
				// before dispatch and never send a second request.
				opened = true
				sawTarget = false
				_, openErr := webReloadAction(&pinned, "document.open", candidateWindow,
					map[string]any{"uuid": cfg.doc}, deadline, 10*time.Second)
				if openErr != nil {
					lastIssue = fmt.Sprintf("one document.open was dispatched but not confirmed: %v", openErr)
				}
			}
			webReloadPause(deadline)
			continue
		}
		if current.Result["documentType"] != docType || current.Context.DocumentType != docType {
			lastIssue = "target document type does not match the saved document"
			stableSamples = 0
			webReloadPause(deadline)
			continue
		}
		sawTarget = true
		probe, probeErr := webReloadAction(&pinned, settleProbeAction(docType), candidateWindow, nil, deadline, 5*time.Second)
		if probeErr != nil {
			lastIssue = fmt.Sprintf("%s: %v", settleProbeAction(docType), probeErr)
			stableSamples = 0
			webReloadPause(deadline)
			continue
		}
		fingerprint, ids, inventoryErr := webReloadInventory(probe, cfg.project, cfg.doc, docType, tab)
		if inventoryErr != nil {
			lastIssue = inventoryErr.Error()
			stableSamples = 0
			webReloadPause(deadline)
			continue
		}
		if !webReloadSameIDs(ids, baselineIDs) {
			lastIssue = "object inventory does not match the saved component ID baseline"
			stableSamples = 0
			webReloadPause(deadline)
			continue
		}
		if stableSamples > 0 && lastTab == tab && lastFingerprint == fingerprint {
			stableSamples++
		} else {
			stableSamples = 1
			firstStableAt = time.Now()
		}
		lastFingerprint, lastTab = fingerprint, tab
		// A proven empty baseline has no primitive ID to anchor loading. Give
		// it a longer observation window; success then proves only stable
		// empty-page routing, not that the host signalled load completion.
		minimumSamples := 2
		minimumAge := time.Duration(0)
		if len(baselineIDs) == 0 {
			minimumSamples, minimumAge = 4, 700*time.Millisecond
		}
		if stableSamples >= minimumSamples && time.Since(firstStableAt) >= minimumAge {
			final, finalErr := webReloadAction(&pinned, "document.current", candidateWindow, nil, deadline, 5*time.Second)
			if finalErr != nil {
				return nil, fmt.Errorf("Web reload objects settled but final document.current failed: %w", finalErr)
			}
			finalActive, finalTab, finalIdentityErr := webReloadCurrentIdentity(final, cfg.project)
			if finalIdentityErr != nil || finalActive != cfg.doc || finalTab != tab || final.Result["documentType"] != docType || final.Context.DocumentType != docType || !time.Now().Before(deadline) {
				return nil, fmt.Errorf("Web reload objects settled but final document.current drifted or exceeded %s; readback is unavailable", timeout)
			}
			return map[string]any{
				"reloaded": true, "saved": true, "ready": true,
				"projectUuid": cfg.project, "documentUuid": cfg.doc, "documentType": docType,
				"componentCount": len(baselineIDs), "readbackScope": webReloadReadbackScope(baselineIDs),
				"oldWindowId": oldWindow, "newWindowId": candidateWindow,
				"saveMs": saveMs, "reconnectMs": time.Since(scheduledAt).Milliseconds(),
				"elapsedMs": time.Since(started).Milliseconds(),
			}, nil
		}
		webReloadPause(deadline)
	}
	return nil, fmt.Errorf("Web page reload was scheduled, but project %s document %s did not become stably readable within %s (%s); state is unknown", cfg.project, cfg.doc, timeout, lastIssue)
}

func webReloadReadbackScope(ids []string) string {
	if len(ids) == 0 {
		return "stable-empty-page-routing"
	}
	return "stable-component-inventory"
}

// These recovery probes never use the ordinary queue retry policy: every HTTP
// round trip and health scan must consume only the remaining reload budget.
func webReloadAction(cfg *appConfig, action, window string, payload any, deadline time.Time, cap time.Duration) (*actionResult, error) {
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return nil, fmt.Errorf("Web reload deadline elapsed before %s", action)
	}
	if remaining > cap {
		remaining = cap
	}
	return requestActionOnce(cfg, action, window, payload, remaining)
}

func webReloadWindows(cfg *appConfig, deadline time.Time) ([]healthWindow, error) {
	start, end, err := cfg.portRange()
	if err != nil {
		return nil, err
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return nil, fmt.Errorf("Web reload deadline elapsed before health scan")
	}
	if remaining > 2*time.Second {
		remaining = 2 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), remaining)
	defer cancel()
	scan := scanHealth(ctx, hostPortOptions{host: cfg.host, portStart: start, portEnd: end})
	if scan.Found == nil {
		return nil, fmt.Errorf("new connector not yet registered")
	}
	var health struct {
		Windows []healthWindow `json:"windows"`
	}
	if err := json.Unmarshal(scan.Found.Raw, &health); err != nil {
		return nil, fmt.Errorf("decode connector windows: %w", err)
	}
	return health.Windows, nil
}

func webReloadCurrentIdentity(res *actionResult, project string) (document, tab string, err error) {
	if res == nil || res.Context == nil || res.Context.ProjectUUID != project || res.Result["parentProjectUuid"] != project {
		return "", "", fmt.Errorf("document.current project identity is unavailable or changed")
	}
	document, tab = strField(res.Result, "uuid"), strField(res.Result, "tabId")
	if document == "" || tab == "" || document != res.Context.DocumentUUID || tab != res.Context.TabID ||
		strField(res.Result, "documentType") != res.Context.DocumentType {
		return "", "", fmt.Errorf("document.current result/context identity is inconsistent")
	}
	return document, tab, nil
}

func webReloadInventory(res *actionResult, project, document, docType, tab string) ([32]byte, []string, error) {
	var zero [32]byte
	if res == nil || res.Context == nil || res.Context.ProjectUUID != project || res.Context.DocumentUUID != document ||
		res.Context.DocumentType != docType || res.Context.TabID != tab {
		return zero, nil, fmt.Errorf("object read context drifted from the requested document")
	}
	components, ok := res.Result["components"].([]any)
	if !ok {
		return zero, nil, fmt.Errorf("object read lacked a component inventory; readiness is unproven")
	}
	count, ok := res.Result["count"].(float64)
	if !ok || count != float64(len(components)) {
		return zero, nil, fmt.Errorf("object read count does not match its component inventory")
	}
	ids := make([]string, 0, len(components))
	for _, item := range components {
		component, ok := item.(map[string]any)
		if !ok || strField(component, "primitiveId") == "" {
			return zero, nil, fmt.Errorf("object read contains an unidentified component")
		}
		ids = append(ids, strField(component, "primitiveId"))
	}
	sort.Strings(ids)
	for i := 1; i < len(ids); i++ {
		if ids[i] == ids[i-1] {
			return zero, nil, fmt.Errorf("object read contains a duplicate component ID")
		}
	}
	ordered := append([]any(nil), components...)
	sort.Slice(ordered, func(i, j int) bool {
		left := ordered[i].(map[string]any)
		right := ordered[j].(map[string]any)
		return strField(left, "primitiveId") < strField(right, "primitiveId")
	})
	encoded, err := json.Marshal(ordered)
	if err != nil {
		return zero, nil, fmt.Errorf("encode object inventory: %w", err)
	}
	return sha256.Sum256(encoded), ids, nil
}

func webReloadSameIDs(actual, baseline []string) bool {
	if len(actual) != len(baseline) {
		return false
	}
	for i := range actual {
		if actual[i] != baseline[i] {
			return false
		}
	}
	return true
}

func webReloadPause(deadline time.Time) {
	if remaining := time.Until(deadline); remaining > 0 {
		if remaining > 250*time.Millisecond {
			remaining = 250 * time.Millisecond
		}
		time.Sleep(remaining)
	}
}
