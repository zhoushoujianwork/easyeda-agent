package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const probeSource = "{\"type\":\"DOCHEAD\",\"ticket\":7}||{\"docType\":\"SCH_PAGE\"}|\n原始文本\n"

func probeSnapshot(t *testing.T) *documentSourceSnapshot {
	t.Helper()
	snapshot, err := sourceSnapshotFromResult(map[string]any{
		"verified": true, "partial": false, "incomplete": false, "schemaVersion": 1, "target": documentSourceTarget{"p", "d", "schematic"},
		"availability": map[string]bool{"getDocumentSource": true, "setDocumentSource": true}, "source": probeSource,
	}, "p", "d")
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func writeProbeSnapshot(t *testing.T, snapshot *documentSourceSnapshot) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "before.json")
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDocumentSourceSnapshotPreservesBytesAndRejectsTampering(t *testing.T) {
	snapshot := probeSnapshot(t)
	path := writeProbeSnapshot(t, snapshot)
	got, err := readDocumentSourceSnapshot(path)
	if err != nil || got.Source != probeSource || got.Bytes != len(probeSource) {
		t.Fatalf("snapshot=%+v err=%v", got, err)
	}
	snapshot.Source += "changed"
	if _, err = readDocumentSourceSnapshot(writeProbeSnapshot(t, snapshot)); err == nil {
		t.Fatal("accepted altered source with the original hash")
	}
	snapshot = probeSnapshot(t)
	snapshot.Bytes++
	if _, err = readDocumentSourceSnapshot(writeProbeSnapshot(t, snapshot)); err == nil {
		t.Fatal("accepted altered byte count")
	}
}

func TestDocumentSourceGetRejectsWrongOrUnavailableEvidence(t *testing.T) {
	for _, change := range []func(map[string]any){
		func(m map[string]any) { m["target"] = documentSourceTarget{"wrong", "d", "schematic"} },
		func(m map[string]any) { m["target"] = documentSourceTarget{"p", "wrong", "schematic"} },
		func(m map[string]any) { m["target"] = documentSourceTarget{"p", "d", "symbol"} },
		func(m map[string]any) { delete(m, "schemaVersion") },
		func(m map[string]any) { m["verified"] = false },
		func(m map[string]any) { delete(m, "partial") },
		func(m map[string]any) { m["availability"] = map[string]bool{} },
		func(m map[string]any) { m["source"] = "" },
		func(m map[string]any) { m["source"] = strings.Repeat("字", documentSourceLimit/3+1) },
	} {
		value := map[string]any{"verified": true, "partial": false, "incomplete": false, "schemaVersion": 1, "target": documentSourceTarget{"p", "d", "schematic"}, "availability": map[string]bool{"getDocumentSource": true}, "source": probeSource}
		change(value)
		if _, err := sourceSnapshotFromResult(value, "p", "d"); err == nil {
			t.Fatal("accepted incomplete or wrong source evidence")
		}
	}
}

func TestDocumentSourceGetCLIStoresOriginalSnapshot(t *testing.T) {
	out := filepath.Join(t.TempDir(), "before.json")
	value := map[string]any{"schemaVersion": 1, "target": documentSourceTarget{"p", "d", "schematic"},
		"availability": map[string]bool{"getDocumentSource": true, "setDocumentSource": false}, "source": probeSource,
		"verified": true, "partial": false, "incomplete": false}
	code, stdout, calls := probeCLI(t, []string{"doc", "source", "get", "--window", "w", "--project-uuid", "p", "--document-uuid", "d", "--out", out}, value, false, func(req map[string]any) {
		if req["action"] != "document.source.get" {
			t.Error(req)
		}
	})
	if code != 0 || calls != 1 {
		t.Fatalf("code=%d calls=%d", code, calls)
	}
	snapshot, err := readDocumentSourceSnapshot(out)
	if err != nil || snapshot.Source != probeSource {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
	if strings.Contains(stdout, "DOCHEAD") || !strings.Contains(stdout, `"designVerified": false`) {
		t.Fatal(stdout)
	}
}

func probeCLI(t *testing.T, args []string, result map[string]any, failed bool, inspect func(map[string]any)) (int, string, int) {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			_, _ = w.Write([]byte(`{"service":"easyeda-agent","windows":[{"windowId":"w"}]}`))
			return
		}
		calls++
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if inspect != nil {
			inspect(request)
		}
		if failed {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": map[string]any{"code": "QUEUE_BLOCKED", "message": "pending setter"}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
	}))
	defer srv.Close()
	host, port, _ := strings.Cut(strings.TrimPrefix(srv.URL, "http://"), ":")
	var out, stderr bytes.Buffer
	code := Run(append([]string{"--host", host, "--ports", port + "-" + port}, args...), &out, &stderr)
	return code, out.String(), calls
}

func probeReport(dryRun bool) map[string]any {
	return map[string]any{"schemaVersion": 1, "target": documentSourceTarget{"p", "d", "schematic"},
		"availability": map[string]bool{"getDocumentSource": true, "setDocumentSource": true}, "identityAfter": documentSourceTarget{"p", "d", "schematic"},
		"beforeSource": probeSource, "afterSource": probeSource, "dryRun": dryRun,
		"comparison": "exact", "rawBeforeEqualsSnapshot": true, "rawAfterEqualsBefore": true,
		"writeAttempted": !dryRun, "written": !dryRun, "verified": true, "partial": false, "incomplete": false}
}

func TestDocumentSourceRoundtripCLISuccessAndEvidence(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		from := writeProbeSnapshot(t, probeSnapshot(t))
		out := filepath.Join(t.TempDir(), "nested", "report.json")
		args := []string{"doc", "source", "roundtrip", "--window", "w", "--from", from, "--out", out}
		if dryRun {
			args = append(args, "--dry-run")
		}
		code, stdout, calls := probeCLI(t, args, probeReport(dryRun), false, func(req map[string]any) {
			if req["action"] != "document.source.roundtrip" || req["windowId"] != "w" {
				t.Errorf("wrong request: %v", req)
			}
			payload := req["payload"].(map[string]any)
			if payload["dryRun"] != dryRun || payload["comparison"] != "exact" || payload["projectUuid"] != "p" || payload["documentUuid"] != "d" || payload["snapshot"].(map[string]any)["source"] != probeSource {
				t.Errorf("wrong payload: %v", payload)
			}
			if _, err := os.Stat(out); err != nil {
				t.Error("output was not reserved before dispatch")
			}
		})
		if code != 0 || calls != 1 {
			t.Fatalf("code=%d calls=%d stdout=%s", code, calls, stdout)
		}
		data, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		var report map[string]any
		if err = json.Unmarshal(data, &report); err != nil {
			t.Fatal(err)
		}
		if report["persistenceVerified"] != false || report["beforeSource"] != probeSource || report["afterSource"] != probeSource {
			t.Fatal(report)
		}
		for _, key := range []string{"snapshotComparisonSha256", "beforeSourceSha256", "afterSourceSha256", "beforeComparisonSha256", "afterComparisonSha256"} {
			if report[key] != probeSnapshot(t).SourceSHA256 {
				t.Fatalf("%s=%v", key, report[key])
			}
		}
		if strings.Contains(stdout, "DOCHEAD") || !strings.Contains(stdout, `"persistenceVerified": false`) {
			t.Fatal(stdout)
		}
	}
}

func TestDocumentSourceRoundtripCLIRejectsIncompleteEvidenceWithoutRetry(t *testing.T) {
	for _, change := range []func(map[string]any) map[string]any{
		func(m map[string]any) map[string]any { return nil },
		func(m map[string]any) map[string]any {
			m["target"] = documentSourceTarget{"p", "other", "schematic"}
			return m
		},
		func(m map[string]any) map[string]any { m["afterSource"] = "changed"; return m },
		func(m map[string]any) map[string]any { m["partial"] = true; return m },
		func(m map[string]any) map[string]any { delete(m, "writeAttempted"); return m },
		func(m map[string]any) map[string]any { delete(m, "incomplete"); return m },
		func(m map[string]any) map[string]any { m["written"] = false; return m },
		func(m map[string]any) map[string]any { delete(m, "identityAfter"); return m },
		func(m map[string]any) map[string]any { delete(m, "comparison"); return m },
		func(m map[string]any) map[string]any { m["comparison"] = "dochead-volatile-v1"; return m },
		func(m map[string]any) map[string]any { delete(m, "rawBeforeEqualsSnapshot"); return m },
		func(m map[string]any) map[string]any { delete(m, "rawAfterEqualsBefore"); return m },
		func(m map[string]any) map[string]any { m["rawBeforeEqualsSnapshot"] = false; return m },
		func(m map[string]any) map[string]any { m["rawAfterEqualsBefore"] = false; return m },
		func(m map[string]any) map[string]any { m["writeError"] = "setter failed"; return m },
		func(m map[string]any) map[string]any { m["readbackError"] = "getter failed"; return m },
		func(m map[string]any) map[string]any { m["writeError"] = false; return m },
		func(m map[string]any) map[string]any { m["readbackError"] = map[string]any{}; return m },
	} {
		out := filepath.Join(t.TempDir(), "report.json")
		code, _, calls := probeCLI(t, []string{"doc", "source", "roundtrip", "--window", "w", "--from", writeProbeSnapshot(t, probeSnapshot(t)), "--out", out}, change(probeReport(false)), false, nil)
		if code == 0 || calls != 1 {
			t.Fatalf("code=%d calls=%d", code, calls)
		}
		data, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		var report map[string]any
		_ = json.Unmarshal(data, &report)
		if report["verified"] != false || report["incomplete"] != true {
			t.Fatal(report)
		}
	}
	out := filepath.Join(t.TempDir(), "blocked.json")
	code, _, calls := probeCLI(t, []string{"doc", "source", "roundtrip", "--window", "w", "--from", writeProbeSnapshot(t, probeSnapshot(t)), "--out", out}, nil, true, nil)
	if code == 0 || calls != 1 {
		t.Fatalf("blocked action retried: code=%d calls=%d", code, calls)
	}
}

func TestDocumentSourceCLIRefusesBeforeDispatch(t *testing.T) {
	out := filepath.Join(t.TempDir(), "existing.json")
	if err := os.WriteFile(out, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	from := writeProbeSnapshot(t, probeSnapshot(t))
	for _, args := range [][]string{
		{"doc", "source", "roundtrip", "--window", "w", "--from", from, "--out", out},
		{"doc", "source", "get", "--window", "w", "--project-uuid", "p", "--document-uuid", "d", "--out", out},
		{"doc", "source", "roundtrip", "--from", from, "--out", out + "2"},
		{"--doc", "d", "doc", "source", "roundtrip", "--window", "w", "--from", from, "--out", out + "2"},
		{"--project", "p", "doc", "source", "get", "--window", "w", "--project-uuid", "p", "--document-uuid", "d", "--out", out + "2"},
	} {
		code, _, calls := probeCLI(t, args, nil, false, nil)
		if code == 0 || calls != 0 {
			t.Fatalf("dispatched invalid request %v: code=%d calls=%d", args, code, calls)
		}
	}
	data, _ := os.ReadFile(out)
	if string(data) != "keep" {
		t.Fatal("overwrote original evidence")
	}
}

const volatileProbeDocument = "0123456789abcdef"

var volatileProbeTarget = documentSourceTarget{"p", volatileProbeDocument, "schematic"}

func volatileProbeSource(client string, updateTime int64) string {
	return fmt.Sprintf(`{"type":"DOCHEAD"}||{"docType":"SCH_PAGE","client":"%s","uuid":"%s","updateTime":%d,"version":"%d","editVersion":"4.1.60"}|`+"\n"+`{"type":"COMPONENT","text":"原始文本"}|`+"\n", client, volatileProbeDocument, updateTime, updateTime)
}

func volatileProbeSnapshot(t *testing.T) *documentSourceSnapshot {
	t.Helper()
	snapshot, err := sourceSnapshotFromResult(map[string]any{
		"verified": true, "partial": false, "incomplete": false, "schemaVersion": 1, "target": volatileProbeTarget,
		"availability": map[string]bool{"getDocumentSource": true, "setDocumentSource": true}, "source": volatileProbeSource("1111111111111111", 1791100000000),
	}, volatileProbeTarget.ProjectUUID, volatileProbeTarget.DocumentUUID)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func volatileProbeReport(dryRun bool) map[string]any {
	report := probeReport(dryRun)
	report["target"], report["identityAfter"] = volatileProbeTarget, volatileProbeTarget
	report["comparison"] = "dochead-volatile-v1"
	report["beforeSource"] = volatileProbeSource("2222222222222222", 1791100000001)
	report["afterSource"] = volatileProbeSource("3333333333333333", 1791100000002)
	report["rawBeforeEqualsSnapshot"], report["rawAfterEqualsBefore"] = false, false
	report["snapshotVolatile"] = documentSourceVolatile{"1111111111111111", 1791100000000, "1791100000000"}
	report["beforeVolatile"] = documentSourceVolatile{"2222222222222222", 1791100000001, "1791100000001"}
	report["afterVolatile"] = documentSourceVolatile{"3333333333333333", 1791100000002, "1791100000002"}
	if dryRun {
		report["afterSource"] = report["beforeSource"]
		report["afterVolatile"] = report["beforeVolatile"]
		report["rawAfterEqualsBefore"] = true
	}
	return report
}

func TestDocumentSourceDOCHEADProjectionPreservesAllOtherBytes(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		source := strings.ReplaceAll(volatileProbeSource("1111111111111111", 1791100000000), "\n", newline)
		projected, volatile, err := documentSourceComparison(source, volatileProbeTarget, documentSourceComparisonDOCHEADV1)
		if err != nil {
			t.Fatal(err)
		}
		expected := strings.Replace(source, "1111111111111111", "<client>", 1)
		expected = strings.Replace(expected, `"updateTime":1791100000000`, `"updateTime":<updateTime>`, 1)
		expected = strings.Replace(expected, `"version":"1791100000000"`, `"version":"<version>"`, 1)
		if projected != expected || *volatile != (documentSourceVolatile{"1111111111111111", 1791100000000, "1791100000000"}) {
			t.Fatalf("projection=%q volatile=%+v", projected, volatile)
		}
	}
}

func TestDocumentSourceDOCHEADProjectionRefusesUnobservedGrammar(t *testing.T) {
	source := volatileProbeSnapshot(t).Source
	for name, invalid := range map[string]string{
		"uppercase client":        strings.Replace(source, "1111111111111111", "ABCDEF0123456789", 1),
		"short client":            strings.Replace(source, "1111111111111111", "111111111111111", 1),
		"nonhex client":           strings.Replace(source, "1111111111111111", "zzzzzzzzzzzzzzzz", 1),
		"wrong uuid":              strings.Replace(source, volatileProbeDocument, "ffffffffffffffff", 1),
		"short time":              strings.ReplaceAll(source, "1791100000000", "179110000000"),
		"long time":               strings.ReplaceAll(source, "1791100000000", "17911000000000"),
		"leading zero time":       strings.ReplaceAll(source, "1791100000000", "0791100000000"),
		"negative time":           strings.Replace(source, `"updateTime":1791100000000`, `"updateTime":-1791100000000`, 1),
		"decimal time":            strings.Replace(source, `"updateTime":1791100000000`, `"updateTime":1791100000000.0`, 1),
		"quoted time":             strings.Replace(source, `"updateTime":1791100000000`, `"updateTime":"1791100000000"`, 1),
		"unequal version":         strings.Replace(source, `"version":"1791100000000"`, `"version":"1791100000001"`, 1),
		"numeric version":         strings.Replace(source, `"version":"1791100000000"`, `"version":1791100000000`, 1),
		"wrong editVersion":       strings.Replace(source, "4.1.60", "4.1", 1),
		"extra head field":        strings.Replace(source, `{"type":"DOCHEAD"}`, `{"type":"DOCHEAD","ticket":7}`, 1),
		"extra payload field":     strings.Replace(source, `"editVersion":"4.1.60"`, `"editVersion":"4.1.60","extra":7`, 1),
		"duplicate payload field": strings.Replace(source, `"editVersion":"4.1.60"`, `"editVersion":"4.1.60","client":"1111111111111111"`, 1),
		"reordered payload":       strings.Replace(source, `"docType":"SCH_PAGE","client":"1111111111111111"`, `"client":"1111111111111111","docType":"SCH_PAGE"`, 1),
		"whitespace":              strings.Replace(source, `"client":`, `"client": `, 1),
		"wrong docType":           strings.Replace(source, "SCH_PAGE", "PCB", 1),
		"no first newline":        strings.Replace(source, "\n", "", 1),
		"bare CR":                 strings.Replace(source, "\n", "\r", 1),
		"extra separator":         strings.Replace(source, "||", "|||", 1),
		"prefixed newline":        "\n" + source,
		"second DOCHEAD":          source + `{"type":"DOCHEAD"}|` + "\n",
		"DOCHEAD body string":     source + `{"text":"DOCHEAD is data too"}|` + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := documentSourceComparison(invalid, volatileProbeTarget, documentSourceComparisonDOCHEADV1); err == nil {
				t.Fatalf("accepted unobserved grammar %q", invalid)
			}
		})
	}
	pcb := volatileProbeTarget
	pcb.DocumentType = "pcb"
	if _, _, err := documentSourceComparison(source, pcb, documentSourceComparisonDOCHEADV1); err == nil {
		t.Fatal("accepted PCB in the schematic-only comparison")
	}
}

func TestDocumentSourceRoundtripCLIVolatileModeSuccessAndEvidence(t *testing.T) {
	for _, dryRun := range []bool{true, false} {
		snapshot := volatileProbeSnapshot(t)
		out := filepath.Join(t.TempDir(), "report.json")
		args := []string{"doc", "source", "roundtrip", "--window", "w", "--from", writeProbeSnapshot(t, snapshot), "--out", out, "--comparison", "dochead-volatile-v1"}
		if dryRun {
			args = append(args, "--dry-run")
		}
		code, stdout, calls := probeCLI(t, args, volatileProbeReport(dryRun), false, func(req map[string]any) {
			payload := req["payload"].(map[string]any)
			if payload["comparison"] != "dochead-volatile-v1" || payload["snapshot"].(map[string]any)["source"] != snapshot.Source {
				t.Fatal(payload)
			}
		})
		if code != 0 || calls != 1 {
			t.Fatalf("code=%d calls=%d stdout=%s", code, calls, stdout)
		}
		data, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		var report map[string]any
		if err = json.Unmarshal(data, &report); err != nil {
			t.Fatal(err)
		}
		if report["verified"] != true || report["persistenceVerified"] != false || report["snapshotSha256"] != snapshot.SourceSHA256 || report["rawBeforeEqualsSnapshot"] != false || report["rawAfterEqualsBefore"] != dryRun {
			t.Fatal(report)
		}
		if report["snapshotComparisonSha256"] != report["beforeComparisonSha256"] || report["beforeComparisonSha256"] != report["afterComparisonSha256"] || report["beforeSourceSha256"] != sourceSHA256(report["beforeSource"].(string)) || report["afterSourceSha256"] != sourceSHA256(report["afterSource"].(string)) {
			t.Fatal("hashes were not independently derived from raw/comparison sources")
		}
		if !dryRun && report["beforeSourceSha256"] == report["afterSourceSha256"] {
			t.Fatal("volatile raw differences were incorrectly hidden")
		}
		if strings.Contains(stdout, "DOCHEAD") || !strings.Contains(stdout, `"comparison": "dochead-volatile-v1"`) || !strings.Contains(stdout, `"rawBeforeEqualsSnapshot": false`) {
			t.Fatal(stdout)
		}
	}
}

func TestDocumentSourceRoundtripCLIDefaultExactRejectsVolatileChanges(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		report := volatileProbeReport(false)
		report["comparison"] = "exact"
		out := filepath.Join(t.TempDir(), "report.json")
		args := []string{"doc", "source", "roundtrip", "--window", "w", "--from", writeProbeSnapshot(t, volatileProbeSnapshot(t)), "--out", out}
		if explicit {
			args = append(args, "--comparison", "exact")
		}
		code, _, calls := probeCLI(t, args, report, false, func(req map[string]any) {
			if req["payload"].(map[string]any)["comparison"] != "exact" {
				t.Fatal("default mode was not exact")
			}
		})
		if code == 0 || calls != 1 {
			t.Fatalf("accepted or retried volatile changes under exact: code=%d calls=%d", code, calls)
		}
		assertFailedSourceEvidence(t, out)
	}
}

func assertFailedSourceEvidence(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report map[string]any
	if err = json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if report["verified"] != false || report["incomplete"] != true || report["persistenceVerified"] != false || len(report["validationErrors"].([]any)) == 0 {
		t.Fatal(report)
	}
	return report
}

func TestDocumentSourceRoundtripCLIVolatileRefusesBeforeDispatch(t *testing.T) {
	for name, comparison := range map[string]string{"unknown": "ignore-metadata", "empty": "", "wrong case": "DOCHEAD-VOLATILE-V1"} {
		t.Run(name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "report.json")
			code, _, calls := probeCLI(t, []string{"doc", "source", "roundtrip", "--window", "w", "--from", writeProbeSnapshot(t, volatileProbeSnapshot(t)), "--out", out, "--comparison", comparison}, nil, false, nil)
			if code == 0 || calls != 0 {
				t.Fatalf("dispatched invalid mode: code=%d calls=%d", code, calls)
			}
		})
	}
	for name, change := range map[string]func(*documentSourceSnapshot){
		"unsupported format": func(s *documentSourceSnapshot) { s.Source = probeSource },
		"wrong target":       func(s *documentSourceSnapshot) { s.Target.DocumentUUID = "ffffffffffffffff" },
		"PCB":                func(s *documentSourceSnapshot) { s.Target.DocumentType = "pcb" },
		"DOCHEAD body":       func(s *documentSourceSnapshot) { s.Source += `{"text":"DOCHEAD"}|` },
		"extra head field": func(s *documentSourceSnapshot) {
			s.Source = strings.Replace(s.Source, `{"type":"DOCHEAD"}`, `{"type":"DOCHEAD","extra":1}`, 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			snapshot := volatileProbeSnapshot(t)
			change(snapshot)
			snapshot.Bytes, snapshot.SourceSHA256 = len(snapshot.Source), sourceSHA256(snapshot.Source)
			out := filepath.Join(t.TempDir(), "report.json")
			code, _, calls := probeCLI(t, []string{"doc", "source", "roundtrip", "--window", "w", "--from", writeProbeSnapshot(t, snapshot), "--out", out, "--comparison", "dochead-volatile-v1"}, nil, false, nil)
			if code == 0 || calls != 0 {
				t.Fatalf("dispatched invalid snapshot: code=%d calls=%d", code, calls)
			}
		})
	}
}

func TestDocumentSourceRoundtripCLIVolatileRejectsNonvolatileChangesWithoutRetry(t *testing.T) {
	for name, change := range map[string]func(string) string{
		"body value":            func(s string) string { return strings.Replace(s, "原始文本", "changed", 1) },
		"body trailing newline": func(s string) string { return strings.TrimSuffix(s, "\n") },
		"extra body whitespace": func(s string) string { return s + " " },
		"header editVersion":    func(s string) string { return strings.Replace(s, "4.1.60", "4.1.61", 1) },
		"header target":         func(s string) string { return strings.Replace(s, volatileProbeDocument, "ffffffffffffffff", 1) },
		"header newline":        func(s string) string { return strings.Replace(s, "\n", "\r\n", 1) },
		"header extra field": func(s string) string {
			return strings.Replace(s, `"editVersion":"4.1.60"`, `"editVersion":"4.1.60","extra":7`, 1)
		},
		"header bad client": func(s string) string {
			return strings.NewReplacer(`"client":"2222222222222222"`, `"client":"222222222222222"`, `"client":"3333333333333333"`, `"client":"333333333333333"`).Replace(s)
		},
		"header time/version mismatch": func(s string) string {
			return strings.NewReplacer(`"version":"1791100000001"`, `"version":"1791100000003"`, `"version":"1791100000002"`, `"version":"1791100000003"`).Replace(s)
		},
		"DOCHEAD body": func(s string) string { return s + `{"text":"DOCHEAD"}|` + "\n" },
	} {
		for _, field := range []string{"beforeSource", "afterSource"} {
			t.Run(name+"/"+field, func(t *testing.T) {
				report := volatileProbeReport(false)
				report[field] = change(report[field].(string))
				out := filepath.Join(t.TempDir(), "report.json")
				code, _, calls := probeCLI(t, []string{"doc", "source", "roundtrip", "--window", "w", "--from", writeProbeSnapshot(t, volatileProbeSnapshot(t)), "--out", out, "--comparison", "dochead-volatile-v1"}, report, false, nil)
				if code == 0 || calls != 1 {
					t.Fatalf("accepted or retried changed source: code=%d calls=%d", code, calls)
				}
				saved := assertFailedSourceEvidence(t, out)
				if saved[field] != report[field] {
					t.Fatal("failed original source evidence was not retained")
				}
			})
		}
	}
}

func TestDocumentSourceRoundtripCLIVolatileRejectsForgedOrMissingEvidence(t *testing.T) {
	changes := map[string]func(map[string]any){
		"missing comparison":       func(m map[string]any) { delete(m, "comparison") },
		"wrong comparison":         func(m map[string]any) { m["comparison"] = "exact" },
		"missing before source":    func(m map[string]any) { delete(m, "beforeSource") },
		"null after source":        func(m map[string]any) { m["afterSource"] = nil; m["afterVolatile"] = nil },
		"forged raw before":        func(m map[string]any) { m["rawBeforeEqualsSnapshot"] = true },
		"forged raw after":         func(m map[string]any) { m["rawAfterEqualsBefore"] = true },
		"raw before wrong type":    func(m map[string]any) { m["rawBeforeEqualsSnapshot"] = "false" },
		"raw after wrong type":     func(m map[string]any) { m["rawAfterEqualsBefore"] = 0 },
		"writeError":               func(m map[string]any) { m["writeError"] = "setter failed" },
		"readbackError":            func(m map[string]any) { m["readbackError"] = "getter failed" },
		"wrong type writeError":    func(m map[string]any) { m["writeError"] = 0 },
		"wrong type readbackError": func(m map[string]any) { m["readbackError"] = map[string]any{} },
	}
	for _, field := range []string{"snapshotVolatile", "beforeVolatile", "afterVolatile", "rawBeforeEqualsSnapshot", "rawAfterEqualsBefore"} {
		field := field
		changes["missing "+field] = func(m map[string]any) { delete(m, field) }
	}
	for _, field := range []string{"snapshotVolatile", "beforeVolatile", "afterVolatile"} {
		field := field
		for _, part := range []string{"client", "updateTime", "version"} {
			part := part
			changes["forged "+field+" "+part] = func(m map[string]any) {
				e := m[field].(documentSourceVolatile)
				values := map[string]any{"client": e.Client, "updateTime": e.UpdateTime, "version": e.Version}
				if part == "updateTime" {
					values[part] = e.UpdateTime + 1
				} else {
					values[part] = "forged"
				}
				m[field] = values
			}
			changes["missing "+field+" "+part] = func(m map[string]any) {
				e := m[field].(documentSourceVolatile)
				values := map[string]any{"client": e.Client, "updateTime": e.UpdateTime, "version": e.Version}
				delete(values, part)
				m[field] = values
			}
		}
		changes["wrong type "+field+" updateTime"] = func(m map[string]any) {
			e := m[field].(documentSourceVolatile)
			m[field] = map[string]any{"client": e.Client, "updateTime": e.Version, "version": e.Version}
		}
		changes["extra field "+field] = func(m map[string]any) {
			e := m[field].(documentSourceVolatile)
			m[field] = map[string]any{"client": e.Client, "updateTime": e.UpdateTime, "version": e.Version, "extra": true}
		}
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			report := volatileProbeReport(false)
			change(report)
			out := filepath.Join(t.TempDir(), "report.json")
			code, _, calls := probeCLI(t, []string{"doc", "source", "roundtrip", "--window", "w", "--from", writeProbeSnapshot(t, volatileProbeSnapshot(t)), "--out", out, "--comparison", "dochead-volatile-v1"}, report, false, nil)
			if code == 0 || calls != 1 {
				t.Fatalf("accepted or retried forged evidence: code=%d calls=%d", code, calls)
			}
			assertFailedSourceEvidence(t, out)
		})
	}
}

func TestDocumentSourceRoundtripCLIDryRunRequiresExactAfterBefore(t *testing.T) {
	report := volatileProbeReport(false)
	report["dryRun"], report["writeAttempted"], report["written"] = true, false, false
	out := filepath.Join(t.TempDir(), "report.json")
	code, _, calls := probeCLI(t, []string{"doc", "source", "roundtrip", "--window", "w", "--from", writeProbeSnapshot(t, volatileProbeSnapshot(t)), "--out", out, "--comparison", "dochead-volatile-v1", "--dry-run"}, report, false, nil)
	if code == 0 || calls != 1 {
		t.Fatalf("accepted or retried dry-run with changed afterSource: code=%d calls=%d", code, calls)
	}
	assertFailedSourceEvidence(t, out)
}
