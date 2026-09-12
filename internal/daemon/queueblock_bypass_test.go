package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

func TestQueueBypassEvidenceMustBeSuccessfulFreshAndForThisProbe(t *testing.T) {
	now := time.Unix(1000, 0)
	s := &Server{queueBlocks: clockTracker(&now)}
	p, _ := s.queueBlocks.beginProbe("w", "write", "r", now)
	now = now.Add(queueBlockGrace + time.Millisecond)
	req := &protocol.Request{Envelope: protocol.Envelope{ID: "next", WindowID: "w"}, Action: "schematic.save"}
	check := func(want string) {
		t.Helper()
		resp := s.checkQueueBlocked(req)
		if resp == nil || resp.Error == nil || resp.Error.Code != want {
			t.Fatalf("want %s, got %+v", want, resp)
		}
		if want == "CONNECTOR_HEALTH_UNVERIFIED" && strings.Contains(resp.Error.Detail, "still answers") {
			t.Fatal("invented a successful bypass observation")
		}
	}
	check("CONNECTOR_HEALTH_UNVERIFIED")
	s.queueBlocks.recordBypass("w", p, true)
	check("CONNECTOR_QUEUE_BLOCKED")
	now = now.Add(queueBypassMaxAge + time.Millisecond)
	check("CONNECTOR_HEALTH_UNVERIFIED")
	s.queueBlocks.recordBypass("w", p, true)
	s.queueBlocks.recordBypass("w", p, false)
	check("CONNECTOR_HEALTH_UNVERIFIED")
	s.queueBlocks.endProbe("w")
	s.queueBlocks.beginProbe("w", "new-write", "r2", now)
	now = now.Add(queueBlockGrace + time.Millisecond)
	s.queueBlocks.recordBypass("w", p, true) // late result from the retired probe
	check("CONNECTOR_HEALTH_UNVERIFIED")
}

func TestQueueBypassActuallyDispatchesAndRequiresOK(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response *protocol.Response
		err      error
		want     bool
	}{
		{"success", &protocol.Response{OK: true}, nil, true},
		{"application failure", &protocol.Response{OK: false}, nil, false},
		{"no response", nil, nil, false},
		{"timeout", nil, context.DeadlineExceeded, false},
		{"transport", nil, errors.New("closed"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{queueBlocks: newQueueBlockTracker()}
			p, _ := s.queueBlocks.beginProbe("w", "write", "r", time.Now())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			s.observeQueueBypass(ctx, "w", p, "r", func(readCtx context.Context, req protocol.Request) (*protocol.Response, error) {
				calls++
				if req.Action != "document.current" || req.WindowID != "w" || req.ID == "r" {
					t.Fatalf("wrong bypass request: %+v", req)
				}
				if deadline, ok := readCtx.Deadline(); !ok || time.Until(deadline) > queueBypassTimeout {
					t.Fatal("bypass must have a short deadline")
				}
				cancel()
				return tc.response, tc.err
			})
			if calls != 1 || p.bypassOK != tc.want {
				t.Fatalf("calls=%d bypassOK=%v, want 1/%v", calls, p.bypassOK, tc.want)
			}
		})
	}
}
