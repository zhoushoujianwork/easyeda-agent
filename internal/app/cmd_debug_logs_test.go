package app

import "testing"

func TestConnectorLogReadbackRejectsUnknownUnscopedAndUnboundedData(t *testing.T) {
	line := map[string]any{"timestamp": float64(1), "type": "info", "message": "[easyeda-agent] liveness lost"}
	for _, result := range []map[string]any{{"connectorLogs": []any{}}, {"connectorLogs": []any{line}}} {
		if err := validateConnectorLogs(result, 1); err != nil {
			t.Fatal(err)
		}
	}
	for _, result := range []map[string]any{{}, {"connectorLogs": nil}, {"connectorLogs": []any{line, line}}, {"connectorLogs": []any{map[string]any{"message": "unrelated"}}}} {
		if validateConnectorLogs(result, 1) == nil {
			t.Fatal("incomplete/unscoped log accepted", result)
		}
	}
}
