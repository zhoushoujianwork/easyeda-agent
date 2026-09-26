package daemon

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
	"github.com/zhoushoujianwork/easyeda-agent/internal/schguard"
)

const (
	// A successful SDK create can precede publication by getAll. Only reads
	// may repeat, under the same serialized window slot and caller deadline.
	wirePublicationMaxReads = 5
	wirePublicationDelay    = 250 * time.Millisecond
)

// No request flag or forceReason bypasses this guard. Run at the HTTP dispatch
// boundary, not just in Cobra, so Apply and raw typed /action share the rule.
func schematicGeometryGuarded(action string) bool {
	return protocol.SchematicGeometryGuarded(action)
}

// Hold the same slot across read→write→read. Other writes and document switches
// must not sneak between those reads, including debug scripts. This does not
// claim to lock out a human editing the Web page; context/freshness is rechecked.
func schematicGeometrySerializes(req *protocol.Request) bool {
	tagPages, _ := req.Payload["tagPages"].(bool)
	return mutatesAction[req.Action] || strings.HasPrefix(req.Action, "document.") || req.Action == "schematic.page.open" || req.Action == "debug.exec_js" || (req.Action == "schematic.components.list" && tagPages)
}

// geometryFindingDigest renders the first few finding messages so the rejection
// reason travels with error.detail, not only inside result.geometryGuard.
// A caller that only surfaces the error line (`sch autoconnect` prints one row
// per pin) otherwise reports the generic "geometry guard failed (preflight)"
// and the operator cannot tell a wrong exit direction from a wire crossing a
// body without re-running the same connection through `sch connect`.
func geometryFindingDigest(findings any) string {
	list, ok := findings.([]schguard.Finding)
	if !ok || len(list) == 0 {
		return ""
	}
	const maxShown = 3
	parts := make([]string, 0, maxShown)
	for _, f := range list {
		if len(parts) == maxShown {
			break
		}
		if f.Message == "" {
			continue
		}
		where := f.Designator
		if where != "" && len(f.Pins) > 0 {
			where += ":" + f.Pins[0]
		}
		if where != "" {
			where = " (" + where + ")"
		}
		parts = append(parts, f.Type+where+": "+f.Message)
	}
	if len(parts) == 0 {
		return ""
	}
	digest := strings.Join(parts, "; ")
	if len(list) > len(parts) {
		digest += fmt.Sprintf("; +%d more", len(list)-len(parts))
	}
	return digest
}

func geometryFailure(req protocol.Request, phase string, applied bool, findings any, detail string) *protocol.Response {
	if digest := geometryFindingDigest(findings); digest != "" {
		detail = strings.TrimSpace(detail)
		if detail != "" {
			detail += " "
		}
		detail += digest
	}
	r := errorResponse(req.ID, "SCHEMATIC_GEOMETRY_INVALID", "schematic geometry guard failed ("+phase+")", detail)
	r.Result = map[string]any{"geometryGuard": map[string]any{"phase": phase, "passed": false, "findings": findings, "mutationApplied": applied}}
	if applied {
		r.Result["partial"] = true
		r.Warnings = []string{"Mutation may have landed. No automatic rollback/retry; read the current data and regenerate the repair."}
	}
	return &r
}

func (s *Server) readSchematicGeometry(ctx context.Context, req protocol.Request, dispatch dispatchFn, suffix string) (*protocol.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	read := req
	read.ID = req.ID + "-geometry-" + suffix
	read.Action = "schematic.components.list"
	readBudget := protocol.SchematicGeometryReadTimeout(time.Duration(req.TimeoutMs) * time.Millisecond)
	read.TimeoutMs = int((readBudget + protocol.DispatchResponseGrace) / time.Millisecond)
	read.Payload = map[string]any{"includeBBox": true, "includePins": true, "includePinNets": false, "includeWires": true}
	ctx, cancel := context.WithTimeout(ctx, readBudget)
	defer cancel()
	started := time.Now().UTC()
	res, err := dispatch(ctx, read)
	if err != nil {
		return nil, err
	}
	if res == nil || !res.OK {
		return nil, fmt.Errorf("geometry read failed: %+v", res)
	}
	s.audit.Append(fromResponse(started, &read, res))
	// A late response may contain valid geometry, but cannot turn an expired
	// observation into a successful verification. Keep its raw audit evidence.
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("geometry response arrived after read deadline: %w", err)
	}
	if res.Context == nil || res.Context.DocumentUUID == "" || res.Context.ProjectUUID == "" || res.Context.DocumentType != "schematic" {
		return nil, fmt.Errorf("geometry read lacks an identified active schematic")
	}
	if available, _ := res.Result["wiresAvailable"].(bool); !available {
		return nil, fmt.Errorf("wire inventory unavailable; matching connector with wiresAvailable evidence is required")
	}
	return res, nil
}

func (s *Server) forwardSchematicGeometry(ctx context.Context, req protocol.Request, dispatch dispatchFn) (*protocol.Response, error) {
	if req.Action == "schematic.pin.repair_marker" {
		return s.forwardSchematicPinMarkerRepair(ctx, req, dispatch)
	}
	if !schematicGeometryGuarded(req.Action) {
		return dispatch(ctx, req)
	}
	before, err := s.readSchematicGeometry(ctx, req, dispatch, "before")
	if err != nil {
		return geometryFailure(req, "preflight", false, nil, err.Error()), nil
	}
	baseline := schguard.AnalyzeWireGeometry(before.Result)
	addingWire := req.Action == "schematic.wire.create" || req.Action == "schematic.power.connect_pin"
	for _, f := range baseline {
		if strings.Contains(f.Type, "unverified") || strings.Contains(f.Type, "unavailable") {
			return geometryFailure(req, "preflight", false, baseline, "Required geometry is missing; no mutation was dispatched."), nil
		}
	}
	if addingWire {
		wire, e := proposedSchematicWire(req)
		if e != nil {
			return geometryFailure(req, "preflight", false, nil, e.Error()), nil
		}
		candidate := map[string]any{"components": before.Result["components"], "wires": []any{wire}, "wiresAvailable": true}
		if findings := schguard.AnalyzeWireGeometry(candidate); len(findings) != 0 {
			return geometryFailure(req, "preflight", false, findings, "Rejected before write: fix source pin exit/wire geometry and replan."), nil
		}
	}
	writeCtx, cancelWrite := context.WithTimeout(ctx, requestTimeout(&req))
	res, err := dispatch(writeCtx, req)
	cancelWrite()
	if err != nil || res == nil || !res.OK {
		return res, err // Unknown/failed mutations are never retried by this guard.
	}
	mutation := req
	mutation.Payload = nil // an invented dryRun on wire.create cannot mute effects
	verified, hasVerified := res.Result["verified"].(bool)
	if effectFromResponse(&mutation, res) == effectNotLanded || (hasVerified && !verified) {
		failed := geometryFailure(req, "readback", true, nil, "Connector reported partial/notApplied/unverified mutation; success is refused.")
		failed.Result["actionResult"] = res.Result
		return failed, nil
	}
	observations := []map[string]any{}
	failReadback := func(findings any, detail string) (*protocol.Response, error) {
		failed := geometryFailure(req, "readback", true, findings, detail)
		failed.Result["actionResult"] = res.Result
		guard := failed.Result["geometryGuard"].(map[string]any)
		guard["readbackAttempts"], guard["observations"] = len(observations), observations
		return failed, nil
	}
	readCtx := ctx
	var cancelPublication context.CancelFunc
	defer func() {
		if cancelPublication != nil {
			cancelPublication()
		}
	}()
	previousSeq := res.Seq
	for attempt := 0; ; attempt++ {
		suffix := "after"
		if attempt > 0 {
			suffix = fmt.Sprintf("after-%d", attempt+1)
		}
		after, err := s.readSchematicGeometry(readCtx, req, dispatch, suffix)
		observation := map[string]any{"readId": req.ID + "-geometry-" + suffix}
		observations = append(observations, observation)
		if err != nil {
			observation["error"] = err.Error()
			return failReadback(nil, err.Error())
		}
		observation["seq"], observation["seqAbandoned"] = after.Seq, after.SeqAbandoned
		if wires, ok := after.Result["wires"].([]any); ok {
			observation["wireSegments"] = len(wires)
		}
		if res.Context == nil || res.Context.ProjectUUID != before.Context.ProjectUUID || res.Context.DocumentUUID != before.Context.DocumentUUID || res.Context.DocumentType != "schematic" ||
			after.Context.ProjectUUID != before.Context.ProjectUUID || after.Context.DocumentUUID != before.Context.DocumentUUID ||
			before.Seq == nil || after.Seq == nil || res.Seq == nil || previousSeq == nil ||
			*res.Seq <= *before.Seq || *after.Seq <= *previousSeq ||
			before.SeqAbandoned == nil || after.SeqAbandoned == nil || res.SeqAbandoned == nil ||
			*before.SeqAbandoned != *res.SeqAbandoned || *before.SeqAbandoned != *after.SeqAbandoned {
			return failReadback(nil, "Document or FIFO freshness changed; geometry verification is incomplete.")
		}
		previousSeq = after.Seq
		findings := schguard.AnalyzeWireGeometry(after.Result)
		if addingWire {
			findings = schguard.NewGeometryFindings(baseline, findings)
		}
		if len(findings) != 0 {
			return failReadback(findings, "New invalid geometry observed after write. Repair the measured state; API success is not validation success.")
		}
		if addingWire {
			proposed, _ := proposedSchematicWire(req)
			if err := schguard.VerifyWirePresent(after.Result, proposed); err != nil {
				observation["coverageError"] = err.Error()
				var incomplete *schguard.WireCoverageError
				if !errors.As(err, &incomplete) || attempt+1 >= wirePublicationMaxReads {
					return failReadback(nil, err.Error())
				}
				if cancelPublication == nil {
					readCtx, cancelPublication = context.WithTimeout(ctx, protocol.SchematicWirePublicationBudget)
				}
				timer := time.NewTimer(wirePublicationDelay)
				select {
				case <-readCtx.Done():
					timer.Stop()
					return failReadback(nil, fmt.Sprintf("%s; wire publication read deadline: %v", err, readCtx.Err()))
				case <-timer.C:
				}
				continue // Never dispatch the mutation again.
			}
			if err := schguard.VerifyWireTopology(before.Result, after.Result, proposed); err != nil {
				return failReadback(nil, err.Error())
			}
		}
		if err := readCtx.Err(); err != nil {
			return failReadback(nil, fmt.Sprintf("geometry verification deadline: %v", err))
		}
		break
	}
	if res.Result == nil {
		res.Result = map[string]any{}
	}
	res.Result["geometryGuard"] = map[string]any{"passed": true, "phase": "readback", "preexistingFindings": len(baseline), "scope": "pin-exit-direction/wire-through-body"}
	res.Result["geometryGuard"].(map[string]any)["readbackAttempts"] = len(observations)
	if len(observations) > 1 {
		res.Result["geometryGuard"].(map[string]any)["observations"] = observations
	}
	if req.Action == "schematic.wire.create" || req.Action == "schematic.power.connect_pin" {
		res.Result["geometryGuard"].(map[string]any)["scope"] = "pin-exit-direction/wire-through-body/wire-coverage/wire-contact-topology"
		res.Result["geometryGuard"].(map[string]any)["baselineFindings"] = baseline
		if len(baseline) > 0 {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%d pre-existing geometry finding(s) retained as diagnostics; this validates the added wire, not the whole page.", len(baseline)))
		}
	}
	return res, nil
}

func proposedSchematicWire(req protocol.Request) (map[string]any, error) {
	if req.Action == "schematic.wire.create" {
		// Preserve nested/flat payload form; the common checker validates it.
		return map[string]any{"primitiveId": "proposed-wire", "points": req.Payload["points"]}, nil
	}
	number := func(key string) (float64, bool) {
		v, ok := req.Payload[key].(float64)
		return v, ok && !math.IsNaN(v) && !math.IsInf(v, 0)
	}
	x, xok := number("pinX")
	y, yok := number("pinY")
	if !xok || !yok {
		return nil, fmt.Errorf("connect_pin requires finite pinX/pinY")
	}
	offset := 30.0
	if _, exists := req.Payload["offset"]; exists {
		var ok bool
		offset, ok = number("offset")
		if !ok || offset <= 0 {
			return nil, fmt.Errorf("connect_pin requires positive finite offset")
		}
	}
	direction, _ := req.Payload["direction"].(string)
	if direction == "" {
		kind, _ := req.Payload["kind"].(string)
		switch kind {
		case "ground", "analog_ground", "protective_ground", "protect_ground":
			direction = "down"
		case "power":
			direction = "up"
		case "net_port_in":
			direction = "left"
		default:
			direction = "right"
		}
	}
	ex, ey := x, y
	switch direction {
	case "up":
		ey += offset
	case "down":
		ey -= offset
	case "left":
		ex -= offset
	case "right":
		ex += offset
	default:
		return nil, fmt.Errorf("invalid connect_pin direction %q", direction)
	}
	// Match JavaScript Math.round, including negative half-grid values.
	snap := func(v float64) float64 { return math.Floor(v/5+0.5) * 5 }
	return map[string]any{"primitiveId": "proposed-wire", "x0": snap(x), "y0": snap(y), "x1": snap(ex), "y1": snap(ey)}, nil
}
