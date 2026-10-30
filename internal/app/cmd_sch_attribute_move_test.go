package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func attributeMoveFixture() (map[string]any, map[string]any) {
	before := map[string]any{"primitiveId": "a1", "X": float64(10), "Y": float64(20), "Rotation": float64(0), "Color": nil, "FontName": nil, "FontSize": nil, "Bold": nil, "Italic": nil, "UnderLine": nil, "AlignMode": nil, "FillColor": nil, "Key": "Name", "Value": "={Manufacturer Part}", "KeyVisible": false, "ValueVisible": nil, "ParentPrimitiveId": "part1"}
	after := map[string]any{}
	for k, v := range before {
		after[k] = v
	}
	after["X"], after["Y"] = float64(30), float64(40)
	payload := map[string]any{"projectUuid": "project1", "documentUuid": "page1", "primitiveId": "a1", "expected": before, "x": float64(30), "y": float64(40)}
	result := map[string]any{"scope": "attribute-position-only", "primitiveId": "a1", "verified": true, "partial": false, "preserved": true, "attempted": true, "moved": true, "before": before, "after": after, "requested": map[string]any{"x": float64(30), "y": float64(40)}}
	return payload, result
}

func TestSchSheetAttributeMoveRequiresExplicitOwnerAndPreservesState(t *testing.T) {
	for _, key := range []string{"Name", "Description", "Drawed"} {
		payload, result := attributeMoveFixture()
		payload["ownerType"], result["ownerType"] = "sheet", "sheet"
		payload["expected"].(map[string]any)["Key"] = key
		result["after"].(map[string]any)["Key"] = key
		if err := validateSchAttributeMovePayload(payload); err != nil {
			t.Fatal(key, err)
		}
		if err := schAttributeMoveResultError("schematic.attribute.move", result); err != nil {
			t.Fatal(key, err)
		}
		delete(payload, "ownerType")
		if key != "Name" && validateSchAttributeMovePayload(payload) == nil {
			t.Fatal("sheet-only key accepted without explicit owner")
		}
		result["after"].(map[string]any)["ValueVisible"] = true
		if schAttributeMoveResultError("schematic.attribute.move", result) == nil {
			t.Fatal("sheet visibility change accepted")
		}
	}
	for _, key := range []string{"Designator", "Device", "@Page Name", "Color"} {
		payload, _ := attributeMoveFixture()
		payload["ownerType"] = "sheet"
		payload["expected"].(map[string]any)["Key"] = key
		if validateSchAttributeMovePayload(payload) == nil {
			t.Fatal("unsupported sheet key accepted", key)
		}
	}
}

func TestSchAttributeMovePayloadAndResultValidation(t *testing.T) {
	for _, mode := range []string{"valid", "missing-state", "extra-state", "wrong-key", "wrong-id", "extra-payload", "null-x", "changed-text", "changed-visibility", "ignored-x", "partial", "missing-verified", "wrong-scope"} {
		t.Run(mode, func(t *testing.T) {
			payload, result := attributeMoveFixture()
			before := payload["expected"].(map[string]any)
			after := result["after"].(map[string]any)
			badPayload := false
			switch mode {
			case "missing-state":
				delete(before, "FontSize")
				badPayload = true
			case "extra-state":
				before["wrong"] = nil
				badPayload = true
			case "wrong-key":
				before["Key"] = "Pin Name"
				badPayload = true
			case "wrong-id":
				payload["primitiveId"] = "other"
				badPayload = true
			case "extra-payload":
				payload["valueVisible"] = false
				badPayload = true
			case "null-x":
				payload["x"] = nil
				badPayload = true
			case "changed-text":
				after["Value"] = "rendered text"
			case "changed-visibility":
				after["ValueVisible"] = false
			case "ignored-x":
				after["X"] = float64(10)
			case "partial":
				result["partial"] = true
			case "missing-verified":
				delete(result, "verified")
			case "wrong-scope":
				result["scope"] = "other"
			}
			if err := validateSchAttributeMovePayload(payload); (err != nil) != badPayload {
				t.Fatalf("payload %s: %v", mode, err)
			}
			if !badPayload {
				if err := schAttributeMoveResultError("schematic.attribute.move", result); (err != nil) != (mode != "valid") {
					t.Fatalf("result %s: %v", mode, err)
				}
			}
		})
	}
}

func TestSchAttributeMoveCLIHelpAndDispatch(t *testing.T) {
	var help bytes.Buffer
	cmd := newSchCmd(nil, &help, &help)
	cmd.SetOut(&help)
	cmd.SetArgs([]string{"attribute-move", "--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"--from", "expected", "formula", "x/y", "never retried", "--doc"} {
		if !strings.Contains(help.String(), v) {
			t.Fatalf("help missing %s", v)
		}
	}
	payload, result := attributeMoveFixture()
	response, _ := json.Marshal(map[string]any{"ok": true, "result": result})
	cfg, server, cleanup := newBlockApplyTestDaemon(t, func(call blockApplyTestCall) string {
		if call.Action != "schematic.attribute.move" {
			t.Errorf("unexpected action %s", call.Action)
		}
		return string(response)
	})
	defer cleanup()
	file := filepath.Join(t.TempDir(), "move.json")
	data, _ := json.Marshal(payload)
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cmd = newSchCmd(cfg, &out, &out)
	cmd.SetArgs([]string{"attribute-move", "--from", file, "--window", "w1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	calls := server.snapshot()
	if len(calls) != 1 || !reflect.DeepEqual(calls[0].Payload, payload) {
		t.Fatalf("unexpected calls %+v", calls)
	}
}

func TestSchAttributeMoveAllEntrypointsRejectPartial(t *testing.T) {
	for _, entry := range []string{"dispatch", "capture", "request", "apply"} {
		t.Run(entry, func(t *testing.T) {
			_, result := attributeMoveFixture()
			result["partial"], result["verified"] = true, false
			encoded, _ := json.Marshal(map[string]any{"ok": true, "result": result})
			cfg, server, cleanup := newBlockApplyTestDaemon(t, func(call blockApplyTestCall) string { return string(encoded) })
			defer cleanup()
			var out bytes.Buffer
			var err error
			switch entry {
			case "dispatch":
				err = dispatch(cfg, "schematic.attribute.move", "w1", nil, &out, &out)
			case "capture":
				_, err = dispatchCapture(cfg, "schematic.attribute.move", "w1", nil, &out)
			case "request":
				_, err = requestAction(cfg, "schematic.attribute.move", "w1", nil)
			case "apply":
				r := applyRunner{cfg: cfg, stdout: &out, stderr: &out, window: "w1"}
				_, err = r.runAction("schematic.attribute.move", nil, time.Second)
			}
			var typed *unverifiedSchAttributeMoveError
			if !errors.As(err, &typed) || len(server.snapshot()) != 1 {
				t.Fatalf("unverified write accepted: %v %+v", err, server.snapshot())
			}
		})
	}
}

func TestSchAttributeMoveApplyStopsAllRecoveryPolicies(t *testing.T) {
	for _, mode := range []string{"retry", "verify", "continue", "default-continue", "prompt"} {
		t.Run(mode, func(t *testing.T) {
			_, result := attributeMoveFixture()
			result["partial"], result["verified"] = true, false
			encoded, _ := json.Marshal(map[string]any{"ok": true, "result": result})
			cfg, server, cleanup := newBlockApplyTestDaemon(t, func(call blockApplyTestCall) string { return string(encoded) })
			defer cleanup()
			step := playbookStep{ID: "move", Action: "schematic.attribute.move", Payload: map[string]any{}}
			pb := &playbook{Version: 1}
			switch mode {
			case "retry":
				n := 1
				step.Retry = &n
			case "verify":
				step.Verify = &verifyBlock{Action: "document.current"}
			case "continue", "prompt":
				step.OnFail = mode
			case "default-continue":
				v := true
				pb.Defaults.ContinueOnError = &v
			}
			pb.Steps = []playbookStep{step, {ID: "dependent", Action: "document.current"}}
			var out bytes.Buffer
			journal := filepath.Join(t.TempDir(), "journal.jsonl")
			r := applyRunner{cfg: cfg, stdout: &out, stderr: &out, pb: pb, vars: map[string]string{}, window: "w1", yes: true, journalPath: journal, toIdx: 1, quiet: true}
			err := r.execute()
			var typed *unverifiedSchAttributeMoveError
			if !errors.As(err, &typed) || len(server.snapshot()) != 1 {
				t.Fatalf("failure bypassed: %v %+v", err, server.snapshot())
			}
			b, e := os.ReadFile(journal)
			if e != nil {
				t.Fatal(e)
			}
			if !strings.Contains(string(b), "rawResponse") {
				t.Fatalf("raw evidence not journaled: %s", b)
			}
		})
	}
}

func TestSchAttributeMoveApplyCommandDoesNotContinueOrRetry(t *testing.T) {
	payload, result := attributeMoveFixture()
	result["partial"], result["verified"] = true, false
	encoded, _ := json.Marshal(map[string]any{"ok": true, "result": result})
	cfg, server, cleanup := newBlockApplyTestDaemon(t, func(call blockApplyTestCall) string { return string(encoded) })
	defer cleanup()
	file := filepath.Join(t.TempDir(), "move.json")
	data, _ := json.Marshal(payload)
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	retry := 1
	step := playbookStep{ID: "move", Run: "sch attribute-move", Flags: map[string]any{"from": file}, OnFail: "continue"}
	step.Retry = &retry
	pb := &playbook{Version: 1, Steps: []playbookStep{step, {ID: "dependent", Action: "document.current"}}}
	var out bytes.Buffer
	r := applyRunner{cfg: cfg, stdout: &out, stderr: &out, pb: pb, vars: map[string]string{}, window: "w1", yes: true, journalPath: filepath.Join(t.TempDir(), "journal.jsonl"), toIdx: 1, quiet: true}
	err := r.execute()
	var typed *unverifiedSchAttributeMoveError
	if !errors.As(err, &typed) || len(server.snapshot()) != 1 {
		t.Fatalf("command failure bypassed: %v %+v", err, server.snapshot())
	}
}

func TestSchAttributeMoveQueueBlockedHasNoRetry(t *testing.T) {
	cfg, server, cleanup := newBlockApplyTestDaemon(t, func(call blockApplyTestCall) string {
		return `{"ok":false,"error":{"code":"QUEUE_BLOCKED","message":"blocked"}}`
	})
	defer cleanup()
	started := time.Now()
	if _, err := requestAction(cfg, "schematic.attribute.move", "w1", nil); err == nil {
		t.Fatal("blocked action accepted")
	}
	if len(server.snapshot()) != 1 || time.Since(started) > time.Second {
		t.Fatal("attribute move retried or waited")
	}
}

func TestSchAttributeMoveApplyPostWriteGateNeverReplays(t *testing.T) {
	for _, entry := range []string{"action", "run"} {
		for _, gate := range []string{"assert", "capture"} {
			t.Run(entry+"-"+gate, func(t *testing.T) {
				payload, result := attributeMoveFixture()
				encoded, _ := json.Marshal(map[string]any{"ok": true, "result": result})
				cfg, server, cleanup := newBlockApplyTestDaemon(t, func(call blockApplyTestCall) string { return string(encoded) })
				defer cleanup()
				step := playbookStep{ID: "move", Action: "schematic.attribute.move", Payload: payload, OnFail: "continue", Verify: &verifyBlock{Action: "document.current"}}
				retry := 1
				step.Retry = &retry
				if entry == "run" {
					file := filepath.Join(t.TempDir(), "move.json")
					data, _ := json.Marshal(payload)
					if err := os.WriteFile(file, data, 0600); err != nil {
						t.Fatal(err)
					}
					step.Action, step.Payload = "", nil
					step.Run, step.Flags = "sch attribute-move", map[string]any{"from": file}
				}
				if gate == "assert" {
					step.Assert = map[string]string{"$.verified": "false"}
				} else {
					step.Capture = map[string]string{"missing": "$.missing"}
				}
				pb := &playbook{Version: 1, Steps: []playbookStep{step, {ID: "dependent", Action: "document.current"}}}
				var out bytes.Buffer
				journal := filepath.Join(t.TempDir(), "journal.jsonl")
				r := applyRunner{cfg: cfg, stdout: &out, stderr: &out, pb: pb, vars: map[string]string{}, window: "w1", yes: true, journalPath: journal, toIdx: 1, quiet: true}
				err := r.execute()
				var typed *unverifiedSchAttributeMoveError
				if !errors.As(err, &typed) || len(server.snapshot()) != 1 {
					t.Fatalf("post-write gate was retried or bypassed: %v %+v", err, server.snapshot())
				}
				if !strings.Contains(typed.rawResponse, "attribute-position-only") {
					t.Fatalf("post-write result not preserved: %+v", typed)
				}
			})
		}
	}
}
