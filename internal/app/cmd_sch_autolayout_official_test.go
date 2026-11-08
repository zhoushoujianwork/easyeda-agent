package app

import (
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"testing"
)

const officialTestDocContext = `"context":{"documentUuid":"doc-1","documentType":"schematic"}`

func officialTestOK(result string) string {
	return fmt.Sprintf(`{"ok":true,"result":%s,%s}`, result, officialTestDocContext)
}

func officialSnapshotResponse(wires int, parts string) string {
	return officialTestOK(fmt.Sprintf(`{"value":{
		"wireCount":%d,"busCount":0,"netflagCount":0,"netportCount":0,
		"netlabelCount":0,"shortSymbolCount":0,"sheetCount":1,"parts":%s
	}}`, wires, parts))
}

func officialComponentsResponse(parts string) string {
	return officialTestOK(fmt.Sprintf(`{"components":[
		{"componentType":"sheet","primitiveId":"sheet-1","bbox":{"minX":0,"minY":0,"maxX":1000,"maxY":800}},
		%s
	],"connectivitySummary":{
		"scope":"activePage","wires":0,"buses":0,"netflags":0,"netports":0,
		"netlabels":0,"shortSymbols":0
	}}`, parts))
}

func officialCleanComponentsResponse() string {
	return officialComponentsResponse(`
		{"componentType":"part","primitiveId":"id-U1","designator":"U1","x":100,"y":100,
		 "rotation":0,
		 "bbox":{"minX":90,"minY":90,"maxX":110,"maxY":110},
		 "pinsAvailable":true,"pins":[{"pinNumber":"1","x":90,"y":100}]}
	`)
}

func officialCheckResponse(dangling int) string {
	findings := make([]string, 0, dangling)
	for i := 0; i < dangling; i++ {
		findings = append(findings, `{"type":"dangling-wire","level":"warn"}`)
	}
	return officialTestOK(fmt.Sprintf(`{"passed":%t,"summary":{
		"floatingPins":0,"componentsWithFloating":0,"geomNetMismatches":0,
		"netMarkerMismatches":0,"multiNetWires":0,"wireContacts":0,"wireCrossings":0,
		"wireOverPins":0,"zeroLengthWires":0,"danglingWires":%d,"total":%d
	},"findings":[%s]}`,
		dangling == 0, dangling, dangling, strings.Join(findings, ",")))
}

func officialDefaultResponder(snapshot func(read int) string, postComponents func() string, check func() string) func(int, autolayoutTestCall) string {
	snapshotReads := 0
	return func(_ int, call autolayoutTestCall) string {
		switch call.Action {
		case "document.current":
			return officialTestOK(`{}`)
		case "schematic.pages.list":
			return officialTestOK(`{"pages":[{"uuid":"doc-1","name":"P1"}]}`)
		case "pcb.documents.list":
			return officialTestOK(`{"pcbs":[]}`)
		case "schematic.read":
			return officialTestOK(`{"nets":[],"components":[]}`)
		case "schematic.save":
			return officialTestOK(`{"saved":true}`)
		case "schematic.components.list":
			if postComponents != nil {
				return postComponents()
			}
			return officialCleanComponentsResponse()
		case "schematic.check":
			if check != nil {
				return check()
			}
			return officialCheckResponse(0)
		case "debug.exec_js":
			code, _ := call.Payload["code"].(string)
			switch {
			case strings.Contains(code, "official-autolayout-input-snapshot"):
				snapshotReads++
				return snapshot(snapshotReads)
			case strings.Contains(code, "sch_Document.autoLayout"):
				return officialTestOK(`{"value":{"done":true}}`)
			case strings.Contains(code, "const snap=v=>"):
				return officialTestOK(`{"value":{"n":0}}`)
			default:
				return officialTestOK(`{"value":{}}`)
			}
		default:
			return officialTestOK(`{}`)
		}
	}
}

func officialCallRanAutoLayout(calls []autolayoutTestCall) bool {
	return officialAutoLayoutCode(calls) != ""
}

func officialAutoLayoutCode(calls []autolayoutTestCall) string {
	for _, call := range calls {
		if call.Action != "debug.exec_js" {
			continue
		}
		code, _ := call.Payload["code"].(string)
		if strings.Contains(code, "sch_Document.autoLayout") {
			return code
		}
	}
	return ""
}

// TestConnsFromLiveNets: a captured netlist flattens to one autoconnect
// connection per pin, kind inferred from the net name, "D.N" → "D:N", and
// single-pin nets are skipped (nothing to tie).
func TestConnsFromLiveNets(t *testing.T) {
	live := map[string]map[string]bool{
		"GND":   {"U1.57": true, "C1.2": true},
		"3V3":   {"U1.2": true, "C1.1": true},
		"SOLO":  {"R9.1": true}, // single pin → skipped
		"C1_N3": {"U1.29": true, "U2.8": true},
	}
	got := connsFromLiveNets(live)

	// Expect 6 connections (2+2+2), SOLO dropped.
	if len(got) != 6 {
		t.Fatalf("got %d connections, want 6 (single-pin net dropped): %+v", len(got), got)
	}
	// Kind inference + pin-ref rewrite, checked per net.
	byNet := map[string][]acConnSpec{}
	for _, c := range got {
		byNet[c.Net] = append(byNet[c.Net], c)
	}
	if _, ok := byNet["SOLO"]; ok {
		t.Error("single-pin net SOLO must be skipped")
	}
	for _, c := range byNet["GND"] {
		if c.Kind != "gnd" {
			t.Errorf("GND pin kind = %q, want gnd", c.Kind)
		}
	}
	for _, c := range byNet["3V3"] {
		if c.Kind != "power" {
			t.Errorf("3V3 pin kind = %q, want power", c.Kind)
		}
	}
	for _, c := range byNet["C1_N3"] {
		if c.Kind != "netport" {
			t.Errorf("C1_N3 pin kind = %q, want netport", c.Kind)
		}
	}
	// "U1.57" → "U1:57"
	var gndRefs []string
	for _, c := range byNet["GND"] {
		gndRefs = append(gndRefs, c.PinRef)
	}
	sort.Strings(gndRefs)
	if !reflect.DeepEqual(gndRefs, []string{"C1:2", "U1:57"}) {
		t.Errorf("GND pin refs = %v, want [C1:2 U1:57]", gndRefs)
	}
}

// TestDeviceTypeForDesignator (issue #143): prefix → platform device-type bucket,
// unknown prefixes fall back to otherDevice (never an invalid enum value).
func TestDeviceTypeForDesignator(t *testing.T) {
	cases := map[string]string{
		"R1": "resistor", "RN2": "resistor", "RV1": "resistor",
		"C10": "capacitor", "CN1": "capacitor",
		"L1": "inductive", "FB3": "inductive",
		"D2": "diode", "LED1": "diode", "ZD1": "diode",
		"Q1": "triode",
		"Y1": "oscillator", "X1": "oscillator",
		"U7": "chip", "IC3": "chip",
		"J1": "otherDevice", "SW2": "otherDevice", "TP1": "otherDevice",
		"r1":  "resistor", // case-insensitive
		"FL1": "otherDevice", "?": "otherDevice",
	}
	for desig, want := range cases {
		if got := deviceTypeForDesignator(desig); got != want {
			t.Errorf("deviceTypeForDesignator(%q) = %q, want %q", desig, got, want)
		}
	}
}

// TestBuildDeviceTypeMap: empty designators are skipped; every value is a valid
// bucket keyed by designator.
func TestBuildDeviceTypeMap(t *testing.T) {
	m := buildDeviceTypeMap([]string{"R1", "U1", "", "C2"})
	if len(m) != 3 {
		t.Fatalf("map size = %d, want 3 (empty skipped): %+v", len(m), m)
	}
	if m["R1"] != "resistor" || m["U1"] != "chip" || m["C2"] != "capacitor" {
		t.Errorf("unexpected map: %+v", m)
	}
}

func TestCountNets(t *testing.T) {
	live := map[string]map[string]bool{
		"A": {"U1.1": true, "U2.1": true},
		"B": {"U3.1": true}, // single pin
		"C": {"U4.1": true, "U5.1": true},
	}
	if n := countNets(live); n != 2 {
		t.Errorf("countNets = %d, want 2 (single-pin net not counted)", n)
	}
}

func TestOfficialAutolayoutSecondGuardRejectsWireDrift(t *testing.T) {
	parts := `[{"primitiveId":"id-U1","designator":"U1","x":100,"y":100,"rotation":0}]`
	cfg, daemon, cleanup := newAutolayoutTestDaemon(t,
		officialDefaultResponder(func(read int) string {
			if read == 1 {
				return officialSnapshotResponse(0, parts)
			}
			return officialSnapshotResponse(1, parts)
		}, nil, nil))
	defer cleanup()

	err := runOfficialAutolayout(cfg, "", true, false, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "wire count changed from 0 to 1") {
		t.Fatalf("got %v, want final wire-drift refusal", err)
	}
	if officialCallRanAutoLayout(daemon.snapshot()) {
		t.Fatal("autoLayout ran after the second guard detected wire drift")
	}
}

func TestOfficialAutolayoutSecondGuardRejectsPartPoseDrift(t *testing.T) {
	cfg, daemon, cleanup := newAutolayoutTestDaemon(t,
		officialDefaultResponder(func(read int) string {
			x := 100
			if read == 2 {
				x = 105
			}
			parts := fmt.Sprintf(`[{"primitiveId":"id-U1","designator":"U1","x":%d,"y":100,"rotation":0}]`, x)
			return officialSnapshotResponse(0, parts)
		}, nil, nil))
	defer cleanup()

	err := runOfficialAutolayout(cfg, "", true, false, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "part input changed") {
		t.Fatalf("got %v, want final part-input drift refusal", err)
	}
	if officialCallRanAutoLayout(daemon.snapshot()) {
		t.Fatal("autoLayout ran after the second guard detected part drift")
	}
}

func TestOfficialAutolayoutSecondGuardRejectsSameCountNetlistDrift(t *testing.T) {
	netReads := 0
	parts := `[
		{"primitiveId":"id-U1","designator":"U1","x":100,"y":100,"rotation":0},
		{"primitiveId":"id-U2","designator":"U2","x":200,"y":100,"rotation":0},
		{"primitiveId":"id-U3","designator":"U3","x":300,"y":100,"rotation":0}
	]`
	responder := officialDefaultResponder(func(_ int) string {
		return officialSnapshotResponse(1, parts)
	}, nil, nil)
	cfg, daemon, cleanup := newAutolayoutTestDaemon(t, func(idx int, call autolayoutTestCall) string {
		if call.Action == "schematic.read" {
			netReads++
			pins := `["U1.1","U2.1"]`
			if netReads == 2 {
				pins = `["U1.1","U3.1"]`
			}
			return officialTestOK(fmt.Sprintf(`{"nets":[{"net":"N1","pins":%s}],"components":[]}`, pins))
		}
		return responder(idx, call)
	})
	defer cleanup()

	err := runOfficialAutolayout(cfg, "", true, true, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "net topology changed") {
		t.Fatalf("got %v, want same-wire-count topology-drift refusal", err)
	}
	if officialCallRanAutoLayout(daemon.snapshot()) {
		t.Fatal("autoLayout ran after the netlist drift guard failed")
	}
}

func TestOfficialAutolayoutCleanPostCheckSucceeds(t *testing.T) {
	parts := `[{"primitiveId":"id-U1","designator":"U1","x":100,"y":100,"rotation":0}]`
	cfg, daemon, cleanup := newAutolayoutTestDaemon(t,
		officialDefaultResponder(func(_ int) string {
			return officialSnapshotResponse(0, parts)
		}, nil, nil))
	defer cleanup()

	var stdout strings.Builder
	if err := runOfficialAutolayout(cfg, "", true, false, &stdout, io.Discard); err != nil {
		t.Fatalf("clean official autolayout returned error: %v", err)
	}
	if !officialCallRanAutoLayout(daemon.snapshot()) {
		t.Fatal("clean run never called autoLayout")
	}
	code := officialAutoLayoutCode(daemon.snapshot())
	if !strings.Contains(code, "official-autolayout-guarded-mutate") ||
		!strings.Contains(code, `const expectedDoc="doc-1"`) {
		t.Fatalf("autoLayout did not carry the in-action document/input guard: %q", code)
	}
	if !strings.Contains(stdout.String(), "✓ official autolayout applied") {
		t.Fatalf("clean run did not print success: %q", stdout.String())
	}
}

func TestOfficialAutolayoutRefusesBusEvenWithRewire(t *testing.T) {
	parts := `[{"primitiveId":"id-U1","designator":"U1","x":100,"y":100,"rotation":0}]`
	cfg, daemon, cleanup := newAutolayoutTestDaemon(t,
		officialDefaultResponder(func(_ int) string {
			return officialTestOK(fmt.Sprintf(`{"value":{
				"wireCount":0,"busCount":1,"netflagCount":0,"netportCount":0,
				"netlabelCount":0,"shortSymbolCount":0,"sheetCount":1,"parts":%s
			}}`, parts))
		}, nil, nil))
	defer cleanup()

	err := runOfficialAutolayout(cfg, "", true, true, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "cannot capture/rebuild bus topology") {
		t.Fatalf("bus refusal error=%v", err)
	}
	if officialCallRanAutoLayout(daemon.snapshot()) {
		t.Fatal("autoLayout ran on an unsupported bus page")
	}
}

func TestOfficialAutolayoutRefusesMarkerWithoutRewire(t *testing.T) {
	parts := `[{"primitiveId":"id-U1","designator":"U1","x":100,"y":100,"rotation":0}]`
	cfg, daemon, cleanup := newAutolayoutTestDaemon(t,
		officialDefaultResponder(func(_ int) string {
			return officialTestOK(fmt.Sprintf(`{"value":{
				"wireCount":0,"busCount":0,"netflagCount":0,"netportCount":0,
				"netlabelCount":0,"shortSymbolCount":1,"sheetCount":1,"parts":%s
			}}`, parts))
		}, nil, nil))
	defer cleanup()

	err := runOfficialAutolayout(cfg, "", true, false, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "shortSymbols=1") {
		t.Fatalf("marker refusal error=%v", err)
	}
	if officialCallRanAutoLayout(daemon.snapshot()) {
		t.Fatal("autoLayout ran with an unprotected short_symbol")
	}
}

func TestOfficialAutolayoutRequiresSheetBeforeMutation(t *testing.T) {
	parts := `[{"primitiveId":"id-U1","designator":"U1","x":100,"y":100,"rotation":0}]`
	cfg, daemon, cleanup := newAutolayoutTestDaemon(t,
		officialDefaultResponder(func(_ int) string {
			return officialTestOK(fmt.Sprintf(`{"value":{
				"wireCount":0,"busCount":0,"netflagCount":0,"netportCount":0,
				"netlabelCount":0,"shortSymbolCount":0,"sheetCount":0,"parts":%s
			}}`, parts))
		}, nil, nil))
	defer cleanup()

	err := runOfficialAutolayout(cfg, "", true, false, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "exactly one readable sheet") {
		t.Fatalf("sheet refusal error=%v", err)
	}
	if officialCallRanAutoLayout(daemon.snapshot()) {
		t.Fatal("autoLayout ran without a sheet")
	}
}

func TestOfficialAutolayoutSavedFalseFails(t *testing.T) {
	parts := `[{"primitiveId":"id-U1","designator":"U1","x":100,"y":100,"rotation":0}]`
	base := officialDefaultResponder(func(_ int) string {
		return officialSnapshotResponse(0, parts)
	}, nil, nil)
	cfg, daemon, cleanup := newAutolayoutTestDaemon(t, func(idx int, call autolayoutTestCall) string {
		if call.Action == "schematic.save" {
			return officialTestOK(`{"saved":false}`)
		}
		return base(idx, call)
	})
	defer cleanup()

	err := runOfficialAutolayout(cfg, "", true, false, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "save was not proven") {
		t.Fatalf("saved:false error=%v", err)
	}
	if !officialCallRanAutoLayout(daemon.snapshot()) {
		t.Fatal("save-failure test did not reach autoLayout")
	}
}

func TestOfficialAutolayoutPostCheckFailsClosed(t *testing.T) {
	parts := `[{"primitiveId":"id-U1","designator":"U1","x":100,"y":100,"rotation":0}]`
	tests := []struct {
		name           string
		postComponents func() string
		check          func() string
		want           string
	}{
		{
			name: "geometry read unavailable",
			postComponents: func() string {
				return `{"ok":false,"error":{"message":"bbox API unavailable"}}`
			},
			want: "could not read back geometry",
		},
		{
			name: "check unavailable",
			check: func() string {
				return `{"ok":false,"error":{"message":"check API unavailable"}}`
			},
			want: "could not run/parse schematic.check",
		},
		{
			name: "malformed check summary",
			check: func() string {
				return officialTestOK(`{"passed":true,"findings":[]}`)
			},
			want: "no summary object",
		},
		{
			name: "incomplete check summary",
			check: func() string {
				return officialTestOK(`{"passed":true,"summary":{"danglingWires":0,"total":0},"findings":[]}`)
			},
			want: "invalid or missing floatingPins",
		},
		{
			name: "rendered overlap",
			postComponents: func() string {
				return officialComponentsResponse(`
					{"componentType":"part","primitiveId":"id-U1","designator":"U1","x":100,"y":100,"rotation":0,
					 "bbox":{"minX":90,"minY":90,"maxX":110,"maxY":110},"pinsAvailable":true,"pins":[]},
					{"componentType":"part","primitiveId":"id-U2","designator":"U2","x":105,"y":100,"rotation":0,
					 "bbox":{"minX":95,"minY":90,"maxX":115,"maxY":110},"pinsAvailable":true,"pins":[]}
				`)
			},
			want: "1 part overlap(s)",
		},
		{
			name: "unproven pin read",
			postComponents: func() string {
				return officialComponentsResponse(`
					{"componentType":"part","primitiveId":"id-U1","designator":"U1","x":100,"y":100,"rotation":0,
					 "bbox":{"minX":90,"minY":90,"maxX":110,"maxY":110},"pins":[]}
				`)
			},
			want: "no readable/proven pin set",
		},
		{
			name:  "dangling wire",
			check: func() string { return officialCheckResponse(1) },
			want:  "1 blocking schematic.check finding(s)",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, daemon, cleanup := newAutolayoutTestDaemon(t,
				officialDefaultResponder(func(_ int) string {
					return officialSnapshotResponse(0, parts)
				}, tc.postComponents, tc.check))
			defer cleanup()

			err := runOfficialAutolayout(cfg, "", true, false, io.Discard, io.Discard)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want post-check failure containing %q", err, tc.want)
			}
			if !officialCallRanAutoLayout(daemon.snapshot()) {
				t.Fatal("test did not reach the post-mutation check")
			}
		})
	}
}
