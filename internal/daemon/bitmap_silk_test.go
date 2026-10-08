package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

func TestBitmapSilkDaemonRefusesBeforeConnectorRouting(t *testing.T) {
	p := protocol.BitmapSilkPayload{SchemaVersion: 1, Source: protocol.BitmapSilkSource{FileName: "logo.png", Format: "png", SHA256: strings.Repeat("a", 64), PixelWidth: 2, PixelHeight: 2}, Conversion: protocol.BitmapSilkConversion{Threshold: 128, Background: "white", Simplify: true}, Polygons: [][]any{{float64(0), float64(0), "L", float64(100), float64(0), float64(100), float64(100), float64(0), float64(100), float64(0), float64(0)}}, Width: 100, Height: 100, Layer: 3, Units: "mil", Anchor: "top-left"}
	s := New(Options{AuditDir: t.TempDir(), ArtifactDir: t.TempDir()})
	t.Cleanup(s.autosave.stop)
	for _, valid := range []bool{true, false} {
		if !valid {
			p.Layer = 13
		}
		raw, _ := json.Marshal(map[string]any{"action": protocol.BitmapSilkAction, "project": "missing", "payload": p})
		w := httptest.NewRecorder()
		s.handleAction(w, httptest.NewRequest(http.MethodPost, "/action", bytes.NewReader(raw)))
		var res protocol.Response
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
			t.Fatal(err)
		}
		if res.OK || res.Error == nil || res.Error.Code != "PRECONDITION_REFUSED" || valid && !strings.Contains(res.Error.Message, "unsupported:") {
			t.Fatalf("%s", w.Body.String())
		}
	}
}
