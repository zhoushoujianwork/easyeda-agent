package app

import (
	"bytes"
	"strings"
	"testing"
)

func TestBasicWriteDispatchRejectsUnverifiedPartial(t *testing.T) {
	for _, action := range []string{"schematic.netflag.create", "schematic.power.connect_pin", "pcb.region.create", "pcb.add_component", "schematic.primitives.delete", "schematic.component.delete", "schematic.pin.disconnect"} {
		for _, tc := range []struct {
			result  string
			failure bool
		}{
			{`{"primitiveId":"kept-id","partial":true}`, true},
			{`{"primitiveId":"kept-id","verified":false}`, true},
			{`{"primitiveId":"kept-id","verified":true}`, false},
			{`{"primitiveId":"kept-id","bindingVerified":true}`, false},
			{`{"primitiveId":"kept-id","partial":true,"verified":true}`, true},
		} {
			result := tc.result
			t.Run(action+result, func(t *testing.T) {
				response := `{"ok":true,"result":` + result + `}`
				cfg, calls, cleanup := newAutolayoutTestDaemon(t, func(_ int, _ autolayoutTestCall) string { return response })
				defer cleanup()
				var out, stderr bytes.Buffer
				err := dispatch(cfg, action, "", map[string]any{}, &out, &stderr)
				wantFailure := tc.failure
				if (err != nil) != wantFailure {
					t.Fatalf("err=%v; want failure=%v", err, wantFailure)
				}
				if strings.TrimSpace(out.String()) != response {
					t.Fatalf("lost partial evidence: %s", out.String())
				}
				if len(calls.snapshot()) != 1 {
					t.Fatalf("unexpected retry: %+v", calls.snapshot())
				}
			})
		}
	}
}

func TestPageRenameDispatchRequiresHostSuccessAndVerifiedReadback(t *testing.T) {
	for _, tc := range []struct {
		result  string
		failure bool
	}{
		{`{"ok":false,"verified":false,"warning":"old cache warning"}`, true},
		{`{"ok":false,"verified":true}`, true},
		{`{"ok":true,"verified":false}`, true},
		{`{"ok":true}`, true},
		{`{"verified":true}`, true},
		{`{"ok":true,"verified":true,"partial":true}`, true},
		{`{"ok":true,"verified":true}`, false},
	} {
		t.Run(tc.result, func(t *testing.T) {
			response := `{"ok":true,"result":` + tc.result + `}`
			cfg, calls, cleanup := newAutolayoutTestDaemon(t, func(_ int, _ autolayoutTestCall) string { return response })
			defer cleanup()
			var out, stderr bytes.Buffer
			err := dispatch(cfg, "schematic.page.rename", "", map[string]any{"pageUuid": "page", "name": "new"}, &out, &stderr)
			if (err != nil) != tc.failure {
				t.Fatalf("err=%v; want failure=%v", err, tc.failure)
			}
			if strings.TrimSpace(out.String()) != response {
				t.Fatalf("lost original evidence: %s", out.String())
			}
			if len(calls.snapshot()) != 1 {
				t.Fatalf("unexpected retry: %+v", calls.snapshot())
			}
		})
	}
}
