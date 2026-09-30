package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPcbImageDryRunAndValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "照片 test.png")
	if err := os.WriteFile(path, encodeTestPNG(t, 200, 100), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		flags []string
		fail  bool
	}{
		{[]string{"--width", "600"}, false}, {nil, true}, {[]string{"--width", "0"}, true}, {[]string{"--width", "NaN"}, true}, {[]string{"--height", "100", "--layer", "3"}, true}, {[]string{"--width", "600", "--rotation", "90"}, false},
	} {
		var out bytes.Buffer
		window := "w1"
		cfg, cap, cleanup := newCapturingDaemon(t)
		cmd := newPcbImageCmd(cfg, &window, &out, &out)
		cmd.SetArgs(append([]string{"create", "--file", path, "--dry-run"}, tc.flags...))
		err := cmd.Execute()
		if (err != nil) != tc.fail {
			t.Fatalf("flags=%v err=%v", tc.flags, err)
		}
		if !tc.fail {
			var result map[string]any
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result["width"] != float64(600) || result["height"] != float64(300) || result["layer"] != float64(13) {
				t.Fatal(result)
			}
		}
		cap.mu.Lock()
		if cap.action != "" {
			t.Fatal("dry run dispatched")
		}
		cap.mu.Unlock()
		cleanup()
	}
	// Explicit dimensions must still reject corrupt files.
	if err := os.WriteFile(path, []byte("broken PNG"), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	window := "w1"
	cmd := newPcbImageCmd(&appConfig{}, &window, &out, &out)
	cmd.SetArgs([]string{"create", "--file", path, "--width", "100", "--height", "50", "--dry-run"})
	if cmd.Execute() == nil {
		t.Fatal("accepted corrupt file")
	}
}

func TestPcbImageMutationVerificationAndPayload(t *testing.T) {
	for _, verified := range []bool{true, false} {
		var got struct {
			Action  string
			Payload map[string]any
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/health" {
				fmt.Fprint(w, `{"service":"easyeda-agent","windows":[{"windowId":"w1"}]}`)
				return
			}
			json.NewDecoder(r.Body).Decode(&got)
			fmt.Fprintf(w, `{"ok":true,"result":{"primitiveId":"kept-id","verified":%t}}`, verified)
		}))
		host, port, _ := strings.Cut(strings.TrimPrefix(srv.URL, "http://"), ":")
		cfg := &appConfig{host: host, ports: port + "-" + port}
		window := "w1"
		var out, errout bytes.Buffer
		cmd := newPcbImageCmd(cfg, &window, &out, &errout)
		cmd.SetArgs([]string{"modify", "--id", "kept-id", "--mirror=false", "--x", "30"})
		err := cmd.Execute()
		if (err == nil) != verified {
			t.Fatalf("verified=%v err=%v", verified, err)
		}
		if got.Action != "pcb.image.modify" || got.Payload["mirror"] != false || got.Payload["x"] != float64(30) {
			t.Fatalf("request=%+v", got)
		}
		if !strings.Contains(out.String(), "kept-id") {
			t.Fatal("lost ID on partial write")
		}
		srv.Close()
	}
}

func TestPcbImageCreateWiresDocumentPayload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "照片 logo.png")
	if err := os.WriteFile(path, encodeTestPNG(t, 200, 100), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, cap, cleanup := newCapturingDaemon(t)
	defer cleanup()
	window := "w1"
	var out bytes.Buffer
	cmd := newPcbImageCmd(cfg, &window, &out, &out)
	cmd.SetArgs([]string{"create", "--file", path, "--height", "300"})
	// The capture daemon returns no verification. The CLI must reject that result
	// while still dispatching exactly one correctly dimensioned create.
	if cmd.Execute() == nil {
		t.Fatal("accepted missing verification")
	}
	cap.mu.Lock()
	defer cap.mu.Unlock()
	if cap.action != "pcb.image.create" || cap.payload["layer"] != float64(13) || cap.payload["width"] != float64(600) || cap.payload["height"] != float64(300) || cap.payload["fileName"] != "照片 logo.png" || cap.payload["dataBase64"] == "" {
		t.Fatalf("payload=%+v action=%s", cap.payload, cap.action)
	}
}

func TestPcbImageRotatedBBox(t *testing.T) {
	bbox := pcbImageBBox(10, -20, 200, 100, 90, true)
	for key, want := range map[string]float64{"minX": 10, "maxX": 110, "minY": -20, "maxY": 180} {
		if math.Abs(bbox[key]-want) > 1e-6 {
			t.Fatalf("bbox=%v", bbox)
		}
	}
}
