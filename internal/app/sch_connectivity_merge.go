package app

import (
	"errors"
	"fmt"

	"github.com/zhoushoujianwork/easyeda-agent/internal/connectivity"
)

func connectivityPageContext(result *actionResult, projectID, pageID string) error {
	if result == nil || result.Context == nil || result.Context.ProjectUUID != projectID || result.Context.DocumentUUID != pageID || result.Context.DocumentType != "schematic" {
		return fmt.Errorf("connectivity snapshot target mismatch: expected project %s page %s", projectID, pageID)
	}
	return nil
}

func collectSchConnectivityAllPages(cfg *appConfig, window string) (out connectivity.Document, err error) {
	// Per-page reads explicitly choose their own document. A caller's --doc
	// must not silently steer the subsequent pages back to the starting page.
	readCfg := *cfg
	readCfg.doc = ""
	docs, active, win, err := discoverDocs(&readCfg, window)
	if err != nil {
		return out, err
	}
	current, err := requestAction(&readCfg, "document.current", win, nil)
	if err != nil {
		return out, err
	}
	if active == "" || current.Context == nil || current.Context.ProjectUUID == "" || current.Context.DocumentUUID != active {
		return out, fmt.Errorf("cannot establish initial project/document identity for all-pages connectivity")
	}
	projectID := current.Context.ProjectUUID
	changedPage := false
	defer func() {
		if changedPage {
			scope := pageScope{window: win, prevActive: active, switched: true}
			if restoreErr := scope.restore(&readCfg); restoreErr != nil {
				err = errors.Join(err, fmt.Errorf("restore original document %s: %w", active, restoreErr))
			}
		}
	}()
	// All pages are converted together so the units of one multi-unit device
	// placed on different pages fold into one component, while two distinct
	// parts sharing a designator anywhere in the project are still rejected.
	var records []any
	pageCount := 0
	for _, doc := range docs {
		if doc.Type != "schematic" {
			continue
		}
		// Mark before switching: an unsuccessful open may still have changed
		// the foreground document and must also trigger restoration.
		changedPage = changedPage || doc.UUID != active
		scope, switchErr := switchToPage(&readCfg, win, doc.UUID)
		if switchErr != nil {
			return out, switchErr
		}
		if !scope.settled {
			return out, fmt.Errorf("page %s did not settle; no connectivity snapshot emitted", doc.Name)
		}
		raw, readErr := requestAction(&readCfg, "schematic.read", win, map[string]any{"includeCheck": false})
		if readErr != nil {
			return out, readErr
		}
		if err := connectivityPageContext(raw, projectID, doc.UUID); err != nil {
			return out, err
		}
		if raw.Result == nil {
			return out, fmt.Errorf("page %s: missing semantic snapshot", doc.Name)
		}
		if available, declared := raw.Result["pinNetsAvailable"].(bool); declared && !available {
			return out, fmt.Errorf("page %s: pin net evidence unavailable; cannot construct connectivity", doc.Name)
		}
		pinRaw, pinErr := requestAction(&readCfg, "schematic.components.list", win, map[string]any{"includePins": true, "includeBBox": true, "includeDeviceIdentity": true})
		if pinErr != nil {
			return out, pinErr
		}
		if err := connectivityPageContext(pinRaw, projectID, doc.UUID); err != nil {
			return out, err
		}
		parts, ok := pinRaw.Result["components"].([]any)
		if !ok {
			return out, fmt.Errorf("page %s: missing pin inventory", doc.Name)
		}
		for _, entry := range parts {
			record, ok := entry.(map[string]any)
			if !ok {
				return out, fmt.Errorf("page %s: invalid component", doc.Name)
			}
			if page, _ := record["pageUuid"].(string); page != "" && page != doc.UUID {
				return out, fmt.Errorf("page %s: component %v has conflicting page identity", doc.Name, record["designator"])
			}
			tagged := make(map[string]any, len(record)+2)
			for key, value := range record {
				tagged[key] = value
			}
			tagged["pageUuid"], tagged["pageName"] = doc.UUID, doc.Name
			records = append(records, tagged)
		}
		pageCount++
	}
	if pageCount == 0 {
		return out, fmt.Errorf("no schematic pages found")
	}
	out, err = connectivity.FromRead(map[string]any{"components": records})
	if err != nil {
		return out, err
	}
	// A project-wide snapshot has no single active-page identity. Page IDs
	// remain on the components; leaving DocumentID empty prevents accidental
	// single-page Apply of a multi-page electrical inventory.
	out.ProjectID = projectID
	if pageCount == 1 {
		for _, doc := range docs {
			if doc.Type == "schematic" {
				out.DocumentID = doc.UUID
			}
		}
	}
	return out, nil
}
