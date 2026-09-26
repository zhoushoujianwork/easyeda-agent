package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestReadBoundedResponse(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		wantError  bool
	}{
		{"below", "1234567", false}, {"exact", "12345678", false}, {"above", "123456789", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readBoundedResponse(strings.NewReader(tc.body), 8)
			if tc.wantError {
				if err == nil || got != nil {
					t.Fatalf("partial response returned: %q, %v", got, err)
				}
			} else if err != nil || string(got) != tc.body {
				t.Fatalf("response changed: %q, %v", got, err)
			}
		})
	}
	readErr := errors.New("interrupted response")
	got, err := readBoundedResponse(io.MultiReader(strings.NewReader("abc"), responseErrorReader{readErr}), 8)
	if !errors.Is(err, readErr) || got != nil {
		t.Fatalf("read error lost or partial bytes exposed: %q, %v", got, err)
	}
}

type responseErrorReader struct{ err error }

func (r responseErrorReader) Read([]byte) (int, error) { return 0, r.err }

// A whole-page response includes native attributes, pin geometry and wire
// evidence. Checking the final part/pin ensures downstream verification sees
// the complete page, rather than an apparently successful truncated prefix.
func TestPostActionWholePageResponseAboveOneMiB(t *testing.T) {
	parts := make([]map[string]any, 200)
	for i := range parts {
		attrs := make(map[string]string, 128)
		for j := 0; j < 128; j++ {
			attrs[fmt.Sprintf("property-%03d", j)] = strings.Repeat("native-attribute-", 5)
		}
		parts[i] = map[string]any{
			"primitiveId": fmt.Sprintf("id-U%d", i+1), "designator": fmt.Sprintf("U%d", i+1),
			"componentType": "part", "otherProperty": attrs, "pinsAvailable": true,
			"pins": []any{map[string]any{"pinNumber": "1", "x": i * 10, "y": 20, "net": "3V3", "noConnected": false}},
		}
	}
	response, err := json.Marshal(map[string]any{
		"ok": true, "result": map[string]any{
			"components": parts, "count": len(parts),
			"wires": []any{map[string]any{"primitiveId": "last-wire", "net": "3V3", "points": []any{[]int{1990, 20}, []int{1990, 30}}}},
		}, "context": map[string]any{"documentUuid": "page-1", "projectUuid": "project-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(response) <= 1<<20 {
		t.Fatalf("fixture is too small: %d", len(response))
	}
	cfg, daemon, closeFn := newAutolayoutTestDaemon(t, func(_ int, call autolayoutTestCall) string {
		if call.Action != "schematic.components.list" {
			t.Errorf("unexpected action: %s", call.Action)
		}
		return string(response)
	})
	defer closeFn()
	got, err := postAction(cfg, "schematic.components.list", "w1", map[string]any{"includePins": true, "includeWires": true}, defaultActionTimeout)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, response) {
		t.Fatalf("whole-page response changed: got %d, want %d bytes", len(got), len(response))
	}
	var decoded struct {
		OK     bool `json:"ok"`
		Result struct {
			Components []struct {
				Designator string `json:"designator"`
				Pins       []struct {
					Net         string `json:"net"`
					X           int    `json:"x"`
					NoConnected bool   `json:"noConnected"`
				} `json:"pins"`
			} `json:"components"`
			Wires []struct {
				PrimitiveID string `json:"primitiveId"`
			} `json:"wires"`
		} `json:"result"`
		Context struct {
			DocumentUUID string `json:"documentUuid"`
		} `json:"context"`
	}
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.OK || decoded.Context.DocumentUUID != "page-1" || len(decoded.Result.Components) != 200 {
		t.Fatalf("incomplete page: %+v", decoded.Context)
	}
	last := decoded.Result.Components[199]
	if last.Designator != "U200" || len(last.Pins) != 1 || last.Pins[0].Net != "3V3" || last.Pins[0].X != 1990 || last.Pins[0].NoConnected || len(decoded.Result.Wires) != 1 || decoded.Result.Wires[0].PrimitiveID != "last-wire" {
		t.Fatalf("tail evidence lost: %+v", last)
	}
	if len(daemon.snapshot()) != 1 {
		t.Fatal("response reading must not retry dispatch")
	}
}

func TestPostActionRejectsOversizedResponseWithoutPartialBody(t *testing.T) {
	// Even a valid JSON prefix must not be returned as success when the complete
	// HTTP response exceeds the cap. Whitespace is deliberately legal JSON.
	response := `{"ok":true,"result":{}}` + strings.Repeat(" ", (32<<20)+1)
	cfg, daemon, closeFn := newAutolayoutTestDaemon(t, func(_ int, _ autolayoutTestCall) string { return response })
	defer closeFn()
	got, err := postAction(cfg, "schematic.components.list", "w1", nil, defaultActionTimeout)
	if err == nil || !strings.Contains(err.Error(), "response exceeds 33554432-byte limit") {
		t.Fatalf("got body length %d, error %v", len(got), err)
	}
	if got != nil {
		t.Fatalf("returned truncated response: %d bytes", len(got))
	}
	if len(daemon.snapshot()) != 1 {
		t.Fatal("oversized response must not trigger a retry")
	}
}

func TestScanHealthRejectsOversizedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"service":"easyeda-agent"}`+strings.Repeat(" ", 1<<20))
	}))
	defer srv.Close()
	host, portText, _ := strings.Cut(strings.TrimPrefix(srv.URL, "http://"), ":")
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	result := scanHealth(context.Background(), hostPortOptions{host: host, portStart: port, portEnd: port})
	if result.Found != nil || len(result.Checked) != 1 || result.Checked[0].Status != "read_error" || !strings.Contains(result.Checked[0].Error, "response exceeds 1048576-byte limit") {
		t.Fatalf("oversized health accepted: %+v", result)
	}
}
