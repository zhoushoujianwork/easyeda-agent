package app

import (
	"bytes"
	"encoding/json"
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
			if payload["dryRun"] != dryRun || payload["projectUuid"] != "p" || payload["documentUuid"] != "d" || payload["snapshot"].(map[string]any)["source"] != probeSource {
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
