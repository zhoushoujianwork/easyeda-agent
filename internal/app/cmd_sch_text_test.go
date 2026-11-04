package app

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestSchTextCreateRejectsInvalidBeforeDispatch(t *testing.T) {
	cases := [][]string{
		{}, {"--content", "说明", "--y", "0"}, {"--content", "说明", "--x", "0"}, {"--x", "0", "--y", "0"},
		{"--content", ""}, {"--content", " \n　"}, {"--content", "a\x00b"}, {"--content", strings.Repeat("😀", 16001)},
		{"--x", "NaN"}, {"--y", "+Inf"}, {"--font-size", "0"}, {"--font-size", "-1"}, {"--font-size", "NaN"}, {"--font-size", "Inf"},
		{"--rotation", "45"}, {"--rotation", "360"}, {"--color", "red"},
	}
	for i, args := range cases {
		cfg, server, cleanup := newBlockApplyTestDaemon(t, func(call blockApplyTestCall) string {
			t.Errorf("invalid input dispatched: %+v", call)
			return `{"ok":true}`
		})
		var out bytes.Buffer
		cmd := newSchCmd(cfg, &out, &out)
		if i >= 4 {
			args = append([]string{"--content", "说明", "--x", "0", "--y", "0"}, args...)
		}
		cmd.SetArgs(append([]string{"text", "create"}, args...))
		if err := cmd.Execute(); err == nil {
			t.Errorf("case %d accepted", i)
		}
		if len(server.snapshot()) != 0 {
			t.Errorf("case %d wrote", i)
		}
		cleanup()
	}
}

func TestSchTextCreatePayloadAndPartialExit(t *testing.T) {
	for _, mode := range []string{"valid", "defaults", "unicode-limit", "partial", "missing-verified", "missing-partial", "missing-id", "wrong-id", "ok-false", "bad-json"} {
		t.Run(mode, func(t *testing.T) {
			result := map[string]any{"primitiveId": "text-1", "knownID": "text-1", "verified": true, "partial": false}
			switch mode {
			case "partial":
				result["partial"] = true
				result["verified"] = false
			case "missing-verified":
				delete(result, "verified")
			case "missing-partial":
				delete(result, "partial")
			case "missing-id":
				delete(result, "primitiveId")
			case "wrong-id":
				result["knownID"] = "other"
			}
			envelope := map[string]any{"ok": mode != "ok-false", "result": result}
			encoded, _ := json.Marshal(envelope)
			raw := string(encoded)
			if mode == "bad-json" {
				raw = "{"
			}
			cfg, server, cleanup := newBlockApplyTestDaemon(t, func(call blockApplyTestCall) string { return raw })
			defer cleanup()
			var out, stderr bytes.Buffer
			cmd := newSchCmd(cfg, &out, &stderr)
			content := "中文说明\n第二行😀"
			if mode == "unicode-limit" {
				content = strings.Repeat("😀", 16000)
			}
			args := []string{"text", "create", "--content", content, "--x", "0", "--y", "-20", "--window", "w1"}
			if mode != "defaults" {
				args = append(args, "--rotation", "270", "--font-size", "10")
			}
			cmd.SetArgs(args)
			err := cmd.Execute()
			good := mode == "valid" || mode == "defaults" || mode == "unicode-limit"
			if (err == nil) != good {
				t.Fatalf("unexpected exit: %v", err)
			}
			calls := server.snapshot()
			if len(calls) != 1 || calls[0].Action != "schematic.text.create" {
				t.Fatalf("must dispatch exactly once: %+v", calls)
			}
			payload := calls[0].Payload
			if payload["content"] != content || payload["x"] != float64(0) || payload["y"] != float64(-20) {
				t.Fatalf("content/coordinates changed: %+v", payload)
			}
			if mode == "defaults" {
				if payload["rotation"] != float64(0) {
					t.Fatal(payload)
				}
				if _, present := payload["fontSize"]; present {
					t.Fatal("implicit font size sent")
				}
			} else if payload["rotation"] != float64(270) || payload["fontSize"] != float64(10) {
				t.Fatal(payload)
			}
			if !strings.Contains(out.String(), raw) {
				t.Fatalf("original response lost: %s", out.String())
			}
		})
	}
}

func TestSchTextCreateHelp(t *testing.T) {
	var out bytes.Buffer
	cmd := newSchTextCmd(&appConfig{}, new(string), &out, &out)
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"create", "--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--content", "--x", "--y", "--font-size", "--rotation", "16000", "easyeda sch text create"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help missing %s", want)
		}
	}
}
