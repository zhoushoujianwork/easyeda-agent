package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const documentSourceLimit = 2 * 1024 * 1024

const (
	documentSourceComparisonExact     = "exact"
	documentSourceComparisonDOCHEADV1 = "dochead-volatile-v1"
)

type documentSourceVolatile struct {
	Client     string `json:"client"`
	UpdateTime int64  `json:"updateTime"`
	Version    string `json:"version"`
}

// This is a deliberately narrow, byte-level grammar for the first line observed
// on Web 4.1.60. Do not replace it with JSON parsing/normalization: field order,
// whitespace, all other header fields, the newline and the body are evidence.
var documentSourceDOCHEADV1 = regexp.MustCompile(`^(\{"type":"DOCHEAD"\}\|\|\{"docType":"SCH_PAGE","client":")([0-9a-f]{16})(","uuid":")([0-9a-f]{16})(","updateTime":)([0-9]{13})(,"version":")([0-9]{13})(","editVersion":"[0-9]+\.[0-9]+\.[0-9]+"\}\|)(\r?\n)`)

func documentSourceComparison(source string, target documentSourceTarget, comparison string) (string, *documentSourceVolatile, error) {
	switch comparison {
	case documentSourceComparisonExact:
		return source, nil, nil
	case documentSourceComparisonDOCHEADV1:
		if target.DocumentType != "schematic" {
			return "", nil, fmt.Errorf("dochead-volatile-v1 only supports schematic documents")
		}
		if len(source) > documentSourceLimit {
			return "", nil, fmt.Errorf("document source exceeds 2 MiB")
		}
		indices := documentSourceDOCHEADV1.FindStringSubmatchIndex(source)
		if indices == nil {
			return "", nil, fmt.Errorf("source does not match the fixed dochead-volatile-v1 first-line grammar")
		}
		group := func(n int) string { return source[indices[n*2]:indices[n*2+1]] }
		if group(4) != target.DocumentUUID {
			return "", nil, fmt.Errorf("DOCHEAD UUID differs from the exact target document")
		}
		if group(6) != group(8) {
			return "", nil, fmt.Errorf("DOCHEAD version must equal the decimal updateTime")
		}
		if strings.Contains(source[indices[1]:], "DOCHEAD") {
			return "", nil, fmt.Errorf("dochead-volatile-v1 refuses DOCHEAD anywhere in the body")
		}
		updateTime, err := strconv.ParseInt(group(6), 10, 64)
		if err != nil {
			return "", nil, fmt.Errorf("invalid DOCHEAD updateTime: %w", err)
		}
		if strconv.FormatInt(updateTime, 10) != group(8) {
			return "", nil, fmt.Errorf("DOCHEAD version must equal String(updateTime) without leading zeroes")
		}
		// Replace only the three captured value spans. The projected string is a
		// comparison artifact and must never be submitted to the source setter.
		projection := group(1) + "<client>" + group(3) + group(4) + group(5) + "<updateTime>" + group(7) + "<version>" + group(9) + group(10) + source[indices[1]:]
		return projection, &documentSourceVolatile{Client: group(2), UpdateTime: updateTime, Version: group(8)}, nil
	default:
		return "", nil, fmt.Errorf("unsupported document source comparison %q; use exact or dochead-volatile-v1", comparison)
	}
}

func sourceVolatileEvidenceMatches(value any, expected *documentSourceVolatile) bool {
	if expected == nil || value == nil {
		return false
	}
	data, err := json.Marshal(value)
	if err != nil {
		return false
	}
	var evidence documentSourceVolatile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&evidence) != nil {
		return false
	}
	return evidence == *expected
}

func sourceSHA256(source string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(source)))
}

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
func sourceRoundtripErrors(report map[string]any, snapshot *documentSourceSnapshot, dryRun bool, comparison string) []string {
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
	for _, key := range []string{"writeError", "readbackError"} {
		if value, exists := report[key]; exists && value != nil {
			message, isString := value.(string)
			if !isString || message != "" {
				failures = append(failures, "connector returned "+key+" despite claimed verification")
			}
		}
	}
	if report["comparison"] != comparison {
		failures = append(failures, "connector comparison evidence is missing or differs from the requested mode")
	}
	before, beforeOK := report["beforeSource"].(string)
	after, afterOK := report["afterSource"].(string)
	if !beforeOK || !afterOK {
		failures = append(failures, "connector returned missing or non-string source evidence")
	}
	rawBeforeEqual := beforeOK && before == snapshot.Source
	rawAfterEqual := beforeOK && afterOK && after == before
	if report["rawBeforeEqualsSnapshot"] != rawBeforeEqual {
		failures = append(failures, "connector rawBeforeEqualsSnapshot evidence is missing or inconsistent")
	}
	if report["rawAfterEqualsBefore"] != rawAfterEqual {
		failures = append(failures, "connector rawAfterEqualsBefore evidence is missing or inconsistent")
	}
	if dryRun && !rawAfterEqual {
		failures = append(failures, "dry-run afterSource must be the exact fresh beforeSource")
	}
	snapshotProjection, snapshotVolatile, snapshotErr := documentSourceComparison(snapshot.Source, snapshot.Target, comparison)
	beforeProjection, beforeVolatile, beforeErr := documentSourceComparison(before, snapshot.Target, comparison)
	afterProjection, afterVolatile, afterErr := documentSourceComparison(after, snapshot.Target, comparison)
	for _, check := range []struct {
		name string
		err  error
	}{{"snapshot", snapshotErr}, {"beforeSource", beforeErr}, {"afterSource", afterErr}} {
		if check.err != nil {
			failures = append(failures, check.name+" comparison failed: "+check.err.Error())
		}
	}
	if snapshotErr == nil && beforeErr == nil && beforeOK && snapshotProjection != beforeProjection {
		failures = append(failures, "fresh beforeSource differs from the snapshot under the requested comparison")
	}
	if beforeErr == nil && afterErr == nil && beforeOK && afterOK && beforeProjection != afterProjection {
		failures = append(failures, "afterSource differs from fresh beforeSource under the requested comparison")
	}
	if comparison == documentSourceComparisonDOCHEADV1 {
		for _, check := range []struct {
			key      string
			expected *documentSourceVolatile
		}{{"snapshotVolatile", snapshotVolatile}, {"beforeVolatile", beforeVolatile}, {"afterVolatile", afterVolatile}} {
			if !sourceVolatileEvidenceMatches(report[check.key], check.expected) {
				failures = append(failures, "connector "+check.key+" evidence is missing or inconsistent with the original source")
			}
		}
	}
	// Hashes are derived here from original strings, independently of any values
	// claimed by the connector. Null comparison hashes mark unparseable evidence.
	report["beforeSourceSha256"], report["afterSourceSha256"] = nil, nil
	report["beforeComparisonSha256"], report["afterComparisonSha256"] = nil, nil
	report["snapshotComparisonSha256"] = nil
	if snapshotErr == nil {
		report["snapshotComparisonSha256"] = sourceSHA256(snapshotProjection)
	}
	if beforeOK {
		report["beforeSourceSha256"] = sourceSHA256(before)
		if beforeErr == nil {
			report["beforeComparisonSha256"] = sourceSHA256(beforeProjection)
		}
	}
	if afterOK {
		report["afterSourceSha256"] = sourceSHA256(after)
		if afterErr == nil {
			report["afterComparisonSha256"] = sourceSHA256(afterProjection)
		}
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
	var from, reportOut, comparison string
	var dryRun bool
	roundtrip := &cobra.Command{
		Use: "roundtrip", Short: "Write only unchanged fresh source once; inspect immediate equality",
		Long:    "Probe the official Beta source setter using only the unchanged fresh source bound to a saved snapshot.\nDefault exact comparison checks every byte. Experimental dochead-volatile-v1 ignores only the three observed SCH_PAGE DOCHEAD value spans; every other byte remains exact.\nOnly complete fresh source reaches the setter: no new design content, automatic retry, rollback, save or reload. Use a dedicated Web test document.\nverified only means immediate equality under the stated comparison; save/reload and object/connectivity checks remain separate.",
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
			if _, _, err = documentSourceComparison(snapshot.Source, snapshot.Target, comparison); err != nil {
				return err
			}
			f, err := reserveSourceOutput(reportOut)
			if err != nil {
				return err
			}
			defer f.Close()
			payload := map[string]any{
				"projectUuid": snapshot.Target.ProjectUUID, "documentUuid": snapshot.Target.DocumentUUID, "dryRun": dryRun, "comparison": comparison,
				"snapshot": map[string]any{"schemaVersion": 1, "projectUuid": snapshot.Target.ProjectUUID, "documentUuid": snapshot.Target.DocumentUUID, "documentType": snapshot.Target.DocumentType, "source": snapshot.Source},
			}
			// Even QUEUE_BLOCKED is returned directly: this experimental setter is
			// deliberately a single dispatch with no retries at any layer here.
			res, callErr := requestActionOnce(cfg, "document.source.roundtrip", *window, payload, 60*time.Second)
			if callErr != nil {
				_ = writeSourceEvidence(f, map[string]any{"error": callErr.Error(), "response": res, "comparison": comparison, "snapshotSha256": snapshot.SourceSHA256, "persistenceVerified": false})
				return callErr
			}
			report := res.Result
			failures := sourceRoundtripErrors(report, snapshot, dryRun, comparison)
			if report == nil {
				report = map[string]any{}
			}
			report["snapshotSha256"] = snapshot.SourceSHA256
			report["requestedComparison"] = comparison
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
			summary := map[string]any{"path": absolute, "target": report["target"], "comparison": comparison, "rawBeforeEqualsSnapshot": report["rawBeforeEqualsSnapshot"], "rawAfterEqualsBefore": report["rawAfterEqualsBefore"], "dryRun": dryRun, "writeAttempted": report["writeAttempted"], "written": report["written"], "verified": report["verified"], "partial": report["partial"], "incomplete": report["incomplete"], "persistenceVerified": false}
			for _, key := range []string{"snapshotComparisonSha256", "beforeSourceSha256", "afterSourceSha256", "beforeComparisonSha256", "afterComparisonSha256", "snapshotVolatile", "beforeVolatile", "afterVolatile"} {
				if value, exists := report[key]; exists {
					summary[key] = value
				}
			}
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
	roundtrip.Flags().StringVar(&comparison, "comparison", documentSourceComparisonExact, "source comparison: exact (default) or experimental schematic-only dochead-volatile-v1")
	group.AddCommand(get, roundtrip)
	return group
}
