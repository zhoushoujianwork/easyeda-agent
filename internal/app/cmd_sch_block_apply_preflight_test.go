package app

import (
	"bytes"
	"strings"
	"testing"
)

// A parts file resolved against another EasyEDA site makes every deviceUuid
// unresolvable here. Discovering that one placement at a time cost a rollback
// and named one role; the sweep has to refuse with the canvas untouched and
// name all of them, because the remedy re-resolves the whole library at once.
func TestRunBlockApplyRefusesBeforePlacingWhenDevicesAreUnresolvable(t *testing.T) {
	cfg, daemon, cleanup := newBlockApplyTestDaemon(t, func(call blockApplyTestCall) string {
		switch call.Action {
		case "schematic.components.list":
			return `{"ok":true,"result":{"components":[]}}`
		case "library.device.get":
			uuid, _ := call.Payload["uuid"].(string)
			return `{"ok":false,"error":{"code":"EDA_CALL_FAILED","message":"Device \"` + uuid + `\" was not found."}}`
		default:
			t.Errorf("unexpected action %q after an unresolvable device", call.Action)
			return `{"ok":true,"result":{}}`
		}
	})
	defer cleanup()

	var stdout, stderr bytes.Buffer
	err := runBlockApply(cfg, "w1", "led_indicator_gpio", bapInput{},
		blockApplyPartsFixture(t), false, true, 0, &stdout, &stderr)
	if err == nil {
		t.Fatalf("err=nil, want a refusal; stderr=%s", stderr.String())
	}
	msg := err.Error()
	for _, want := range []string{
		"nothing was placed", // the canvas claim the whole design rests on
		"led.yellow_0805",    // both parts named, not just the first
		"res.1k_0402",
		"dev-led", "dev-res", // the uuids, so the parts file can be edited
		"was not found", // the library's own words, not a paraphrase
		"different EasyEDA site",
		"easyeda lib by-lcsc", // the executable next step
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal missing %q:\n%s", want, msg)
		}
	}
	for _, call := range daemon.snapshot() {
		if call.Action == "schematic.component.place" {
			t.Fatalf("a component was placed despite an unresolvable device; calls=%+v", daemon.snapshot())
		}
	}
}

// One failure among several must not read as a whole-site mismatch: that would
// send the user re-resolving a library that is fine apart from one part.
func TestBapUnresolvedDevicesErrorSeparatesOneBadPartFromAWholeSite(t *testing.T) {
	one := bapUnresolvedDevicesError([]bapUnresolvedDevice{{
		bapDeviceRef: bapDeviceRef{DeviceUUID: "dev-led", LCSC: "C1", PartKeys: []string{"led.yellow_0805"}},
		Reason:       `Device "dev-led" was not found.`,
	}}, 4, "parts.json").Error()
	if strings.Contains(one, "different EasyEDA site") {
		t.Errorf("1-of-4 must not be reported as a site mismatch:\n%s", one)
	}
	if !strings.Contains(one, "1 of 4") {
		t.Errorf("refusal must state the ratio:\n%s", one)
	}

	all := bapUnresolvedDevicesError([]bapUnresolvedDevice{
		{bapDeviceRef: bapDeviceRef{DeviceUUID: "a", PartKeys: []string{"p1"}}, Reason: "x"},
		{bapDeviceRef: bapDeviceRef{DeviceUUID: "b", PartKeys: []string{"p2"}}, Reason: "x"},
	}, 2, "parts.json").Error()
	if !strings.Contains(all, "different EasyEDA site") {
		t.Errorf("2-of-2 is the site-mismatch signature:\n%s", all)
	}
	if !strings.Contains(all, "no LCSC in the parts file") {
		t.Errorf("a device with no LCSC must say so rather than print an empty column:\n%s", all)
	}
}

// A block repeats devices across roles (two CC resistors are one device), so
// the sweep must cost one read per DEVICE, not per placement.
func TestBapPlanDevicesCollapsesRepeatedDevices(t *testing.T) {
	got := bapPlanDevices([]bapPlacement{
		{PartKey: "res.5k1_0402", LibraryUUID: "lib", DeviceUUID: "dev-res", LCSC: "C2"},
		{PartKey: "res.5k1_0402", LibraryUUID: "lib", DeviceUUID: "dev-res"},
		{PartKey: "led.yellow_0805", LibraryUUID: "lib", DeviceUUID: "dev-led", LCSC: "C1"},
		{PartKey: "broken", LibraryUUID: "lib", DeviceUUID: ""},
	})
	if len(got) != 2 {
		t.Fatalf("devices=%+v, want 2 distinct", got)
	}
	if got[0].DeviceUUID != "dev-res" || len(got[0].PartKeys) != 1 || got[0].LCSC != "C2" {
		t.Errorf("first device=%+v, want dev-res/one part key/C2 carried from the row that had it", got[0])
	}
	if got[1].DeviceUUID != "dev-led" {
		t.Errorf("second device=%+v, want dev-led in plan order", got[1])
	}
}

// The sweep costs a round-trip per device, so it has to be possible to turn off.
func TestRunBlockApplySkipDevicePreflightIssuesNoDeviceReads(t *testing.T) {
	cfg, daemon, cleanup := newBlockApplyTestDaemon(t, func(call blockApplyTestCall) string {
		switch call.Action {
		case "schematic.components.list":
			return `{"ok":true,"result":{"components":[]}}`
		case "library.device.get":
			t.Errorf("device read issued despite --skip-device-preflight")
			return `{"ok":true,"result":{}}`
		default:
			return ""
		}
	})
	defer cleanup()

	var stdout, stderr bytes.Buffer
	_ = runBlockApply(cfg, "w1", "led_indicator_gpio", bapInput{SkipDevicePreflight: true},
		blockApplyPartsFixture(t), true, true, 0, &stdout, &stderr)
	for _, call := range daemon.snapshot() {
		if call.Action == "library.device.get" {
			t.Fatalf("library.device.get ran with the sweep disabled")
		}
	}
}

// --dry-run must run the sweep: library.device.get is a read, and a dry-run that
// reports a clean plan for a parts file that cannot place anything is the exact
// false green this guard exists to remove.
func TestRunBlockApplyDryRunStillResolvesDevices(t *testing.T) {
	cfg, daemon, cleanup := newBlockApplyTestDaemon(t, func(call blockApplyTestCall) string {
		switch call.Action {
		case "schematic.components.list":
			return `{"ok":true,"result":{"components":[]}}`
		case "library.device.get":
			return `{"ok":false,"error":{"code":"EDA_CALL_FAILED","message":"Device was not found."}}`
		default:
			return ""
		}
	})
	defer cleanup()

	var stdout, stderr bytes.Buffer
	err := runBlockApply(cfg, "w1", "led_indicator_gpio", bapInput{},
		blockApplyPartsFixture(t), true, true, 0, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "nothing was placed") {
		t.Fatalf("dry-run err=%v, want the same refusal as a real run", err)
	}
	saw := false
	for _, call := range daemon.snapshot() {
		if call.Action == "library.device.get" {
			saw = true
		}
	}
	if !saw {
		t.Fatal("dry-run issued no device read")
	}
}
