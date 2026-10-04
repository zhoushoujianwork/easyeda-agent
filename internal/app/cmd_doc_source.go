package app

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const documentSourceLimit = 2 * 1024 * 1024

type documentSourceTarget struct {
	ProjectUUID  string `json:"projectUuid"`
	DocumentUUID string `json:"documentUuid"`
	DocumentType string `json:"documentType"`
}

type documentSourceSnapshot struct {
	SchemaVersion int                  `json:"schemaVersion"`
	Target        documentSourceTarget `json:"target"`
	Availability  map[string]bool      `json:"availability"`
	Source        string               `json:"source"`
	SourceSHA256  string               `json:"sourceSha256"`
	Bytes         int                  `json:"bytes"`
}

func sourceSnapshotFromResult(value map[string]any, project, doc string) (*documentSourceSnapshot, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var snapshot documentSourceSnapshot
	if err = json.Unmarshal(data, &snapshot); err != nil {
		return nil, err
	}
	if snapshot.SchemaVersion != 1 || snapshot.Target.ProjectUUID != project || snapshot.Target.DocumentUUID != doc {
		return nil, fmt.Errorf("document source returned missing or mismatched target/schema")
	}
	if snapshot.Target.DocumentType != "schematic" && snapshot.Target.DocumentType != "pcb" {
		return nil, fmt.Errorf("document source requires a schematic or PCB document")
	}
	if strings.TrimSpace(snapshot.Source) == "" || len(snapshot.Source) > documentSourceLimit {
		return nil, fmt.Errorf("document source is empty or exceeds 2 MiB")
	}
	if !snapshot.Availability["getDocumentSource"] {
		return nil, fmt.Errorf("document source getter was not confirmed available")
	}
	if value["verified"] != true || value["partial"] != false || value["incomplete"] != false {
		return nil, fmt.Errorf("document source read was not fully verified")
	}
	snapshot.Bytes = len(snapshot.Source)
	snapshot.SourceSHA256 = fmt.Sprintf("%x", sha256.Sum256([]byte(snapshot.Source)))
	return &snapshot, nil
}

func readDocumentSourceSnapshot(path string) (*documentSourceSnapshot, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// JSON escaping can expand a 2 MiB source. Bound the envelope as well.
	data, err := io.ReadAll(io.LimitReader(f, 16*1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 16*1024*1024 {
		return nil, fmt.Errorf("document source snapshot envelope exceeds 16 MiB")
	}
	var snapshot documentSourceSnapshot
	if err = json.Unmarshal(data, &snapshot); err != nil {
		return nil, fmt.Errorf("invalid document source snapshot: %w", err)
	}
	if snapshot.Target.ProjectUUID == "" || snapshot.Target.DocumentUUID == "" {
		return nil, fmt.Errorf("snapshot is missing exact target UUIDs")
	}
	want := fmt.Sprintf("%x", sha256.Sum256([]byte(snapshot.Source)))
	if snapshot.SourceSHA256 != want || snapshot.Bytes != len(snapshot.Source) {
		return nil, fmt.Errorf("snapshot source SHA-256 or byte count does not match; do not edit the snapshot")
	}
	// Stored snapshots are comparison inputs, not proof of host persistence.
	value := map[string]any{"schemaVersion": snapshot.SchemaVersion, "target": snapshot.Target, "source": snapshot.Source, "availability": snapshot.Availability, "verified": true, "partial": false, "incomplete": false}
	return sourceSnapshotFromResult(value, snapshot.Target.ProjectUUID, snapshot.Target.DocumentUUID)
}

func sourceTargetArgs(cfg *appConfig, window, project, doc string) error {
	if strings.TrimSpace(window) == "" || strings.TrimSpace(project) == "" || strings.TrimSpace(doc) == "" {
		return fmt.Errorf("explicit --window and exact project/document UUIDs are required")
	}
	if cfg.project != "" || cfg.doc != "" {
		return fmt.Errorf("doc source uses exact UUIDs; omit global --project/--doc routing")
	}
	return nil
}

func reserveSourceOutput(path string) (*os.File, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("--out must name a new JSON evidence file")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
}

func writeSourceEvidence(f *os.File, value any) error {
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(value); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Validate connector evidence independently. A successful HTTP envelope is not
// enough to certify the source probe, and missing fields are not false values.
func sourceRoundtripErrors(report map[string]any, snapshot *documentSourceSnapshot, dryRun bool) []string {
	var failures []string
	if report == nil {
		return []string{"connector returned no roundtrip evidence"}
	}
	var identity struct {
		SchemaVersion int                  `json:"schemaVersion"`
		Target        documentSourceTarget `json:"target"`
		Availability  map[string]bool      `json:"availability"`
	}
	data, err := json.Marshal(report)
	if err != nil || json.Unmarshal(data, &identity) != nil || identity.SchemaVersion != 1 || identity.Target != snapshot.Target {
		failures = append(failures, "connector returned missing or mismatched target/schema")
	}
	if !identity.Availability["getDocumentSource"] || !identity.Availability["setDocumentSource"] {
		failures = append(failures, "connector did not confirm both source methods available")
	}
	if report["beforeSource"] != snapshot.Source || report["afterSource"] != snapshot.Source {
		failures = append(failures, "connector source evidence differs from the snapshot")
	}
	if !dryRun {
		var after documentSourceTarget
		data, err := json.Marshal(report["identityAfter"])
		if err != nil || json.Unmarshal(data, &after) != nil || after != snapshot.Target {
			failures = append(failures, "connector returned missing or mismatched post-write identity")
		}
	}
	for key, expected := range map[string]bool{"dryRun": dryRun, "writeAttempted": !dryRun, "written": !dryRun, "verified": true, "partial": false, "incomplete": false} {
		if report[key] != expected {
			failures = append(failures, fmt.Sprintf("connector %s evidence is missing or inconsistent", key))
		}
	}
	return failures
}

func newDocSourceCmd(cfg *appConfig, window *string, stdout io.Writer) *cobra.Command {
	group := &cobra.Command{Use: "source", Short: "Inspect and probe the Beta native document source (no new design content)"}
	var project, doc, out string
	get := &cobra.Command{
		Use: "get", Short: "Read exact-target raw source to a new SHA-256-bound snapshot",
		Args:    cobra.NoArgs,
		Example: "  easyeda doc source get --window W --project-uuid P --document-uuid D --out before.json",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := sourceTargetArgs(cfg, *window, project, doc); err != nil {
				return err
			}
			f, err := reserveSourceOutput(out)
			if err != nil {
				return err
			}
			defer f.Close()
			res, err := requestActionTimed(cfg, "document.source.get", *window, map[string]any{"projectUuid": project, "documentUuid": doc}, 45*time.Second)
			if err != nil {
				_ = writeSourceEvidence(f, map[string]any{"error": err.Error(), "response": res})
				return err
			}
			snapshot, err := sourceSnapshotFromResult(res.Result, project, doc)
			if err != nil {
				_ = writeSourceEvidence(f, map[string]any{"error": err.Error(), "response": res})
				return err
			}
			if err = writeSourceEvidence(f, snapshot); err != nil {
				return err
			}
			absolute, err := filepath.Abs(out)
			if err != nil {
				return err
			}
			return encodeResultEnvelope(res, map[string]any{"path": absolute, "target": snapshot.Target, "bytes": snapshot.Bytes, "sourceSha256": snapshot.SourceSHA256, "availability": snapshot.Availability, "designVerified": false, "persistenceVerified": false}, stdout)
		},
	}
	get.Flags().StringVar(&project, "project-uuid", "", "exact active project UUID (required)")
	get.Flags().StringVar(&doc, "document-uuid", "", "exact active schematic/PCB UUID (required)")
	get.Flags().StringVar(&out, "out", "", "new JSON snapshot path, never overwritten (required)")
	var from, reportOut string
	var dryRun bool
	roundtrip := &cobra.Command{
		Use: "roundtrip", Short: "Write only unchanged fresh source once; inspect immediate equality",
		Long:    "Probe the official Beta source setter using only the byte-identical fresh source bound to a saved snapshot.\nNo new design content, automatic retry, rollback, save or reload. Use a dedicated Web test document.\nverified only means immediate source equality; save/reload and object/connectivity checks remain separate.",
		Args:    cobra.NoArgs,
		Example: "  easyeda doc source roundtrip --window W --from before.json --out dry-run.json --dry-run\n  easyeda doc source roundtrip --window W --from before.json --out roundtrip.json",
		RunE: func(cmd *cobra.Command, args []string) error {
			snapshot, err := readDocumentSourceSnapshot(from)
			if err != nil {
				return err
			}
			if err = sourceTargetArgs(cfg, *window, snapshot.Target.ProjectUUID, snapshot.Target.DocumentUUID); err != nil {
				return err
			}
			f, err := reserveSourceOutput(reportOut)
			if err != nil {
				return err
			}
			defer f.Close()
			payload := map[string]any{
				"projectUuid": snapshot.Target.ProjectUUID, "documentUuid": snapshot.Target.DocumentUUID, "dryRun": dryRun,
				"snapshot": map[string]any{"schemaVersion": 1, "projectUuid": snapshot.Target.ProjectUUID, "documentUuid": snapshot.Target.DocumentUUID, "documentType": snapshot.Target.DocumentType, "source": snapshot.Source},
			}
			// Even QUEUE_BLOCKED is returned directly: this experimental setter is
			// deliberately a single dispatch with no retries at any layer here.
			res, callErr := requestActionOnce(cfg, "document.source.roundtrip", *window, payload, 60*time.Second)
			if callErr != nil {
				_ = writeSourceEvidence(f, map[string]any{"error": callErr.Error(), "response": res, "snapshotSha256": snapshot.SourceSHA256, "persistenceVerified": false})
				return callErr
			}
			report := res.Result
			failures := sourceRoundtripErrors(report, snapshot, dryRun)
			if report == nil {
				report = map[string]any{}
			}
			report["snapshotSha256"] = snapshot.SourceSHA256
			report["persistenceVerified"] = false
			if len(failures) > 0 {
				report["validationErrors"] = failures
				report["verified"] = false
				report["incomplete"] = true
			}
			if err = writeSourceEvidence(f, report); err != nil {
				return err
			}
			absolute, err := filepath.Abs(reportOut)
			if err != nil {
				return err
			}
			summary := map[string]any{"path": absolute, "target": report["target"], "dryRun": dryRun, "writeAttempted": report["writeAttempted"], "written": report["written"], "verified": report["verified"], "partial": report["partial"], "incomplete": report["incomplete"], "persistenceVerified": false}
			if err = encodeResultEnvelope(res, summary, stdout); err != nil {
				return err
			}
			if report["verified"] != true || report["partial"] == true || report["incomplete"] == true {
				return fmt.Errorf("document source roundtrip was not verified; inspect %s before any further writes", absolute)
			}
			return nil
		},
	}
	roundtrip.Flags().StringVar(&from, "from", "", "unaltered source snapshot from 'doc source get' (required)")
	roundtrip.Flags().StringVar(&reportOut, "out", "", "new JSON evidence report path, reserved before invoking the setter (required)")
	roundtrip.Flags().BoolVar(&dryRun, "dry-run", false, "only compare the fresh source and identity; never invoke the setter")
	group.AddCommand(get, roundtrip)
	return group
}
