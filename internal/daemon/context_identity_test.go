package daemon

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

// Exercise the actual receive handler, including registration and context
// ownership, rather than applying a context directly to a connection.
func sendContextIdentityFrame(t *testing.T, s *Server, c *conn, frame any) {
	t.Helper()
	data, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	s.handleFrame(context.Background(), c, data)
}

func TestHandleFrameRejectsContextOutsideRegisteredSession(t *testing.T) {
	for _, tc := range []struct {
		name              string
		registered        bool
		messageID         string
		sharedCachedState bool
	}{
		{"old session after register", true, "w1", false},
		{"empty ID after register", true, "", false},
		{"context before register", false, "w1", false},
		{"empty ID before register", false, "", false},
		{"old session must not change cached state", true, "w1", true},
		{"empty ID must not change cached state", true, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New(Options{Version: "1.7.1"})
			now := time.Now().UTC()
			other := newConn(nil, now.Add(-time.Second))
			sendContextIdentityFrame(t, s, other, protocol.Register{
				Type: protocol.TypeRegister, WindowID: "w1", ConnectorVersion: "1.7.1",
			})
			shared := protocol.ContextMessage{
				Type: protocol.TypeContext, WindowID: "w1", ProjectUUID: "shared-project",
				DocumentUUID: "shared-document", DocumentType: "schematic", TabID: "shared-tab",
			}
			sendContextIdentityFrame(t, s, other, shared)

			incoming := newConn(nil, now)
			if tc.registered {
				sendContextIdentityFrame(t, s, incoming, protocol.Register{
					Type: protocol.TypeRegister, WindowID: "w2", ConnectorVersion: "1.7.1",
				})
				sendContextIdentityFrame(t, s, incoming, protocol.ContextMessage{
					Type: protocol.TypeContext, WindowID: "w2", ProjectUUID: "current-project",
					DocumentUUID: "current-document", DocumentType: "pcb", TabID: "current-tab",
				})
			}
			if tc.sharedCachedState {
				// Response contexts can refresh the cache. A rejected context
				// must preserve it even when it matches another connection.
				cached := shared.Context()
				sendContextIdentityFrame(t, s, incoming, protocol.Response{
					Envelope: protocol.Envelope{Type: protocol.TypeResponse, ID: "completed-read"},
					OK:       true, Context: &cached,
				})
			}
			beforeIncoming, beforeOther := incoming.snapshot(), other.snapshot()
			beforeCount := len(s.hub.windows)
			// The rejected frame reports w1's complete project/document/tab.
			shared.WindowID = tc.messageID
			sendContextIdentityFrame(t, s, incoming, shared)

			if got := incoming.snapshot(); !reflect.DeepEqual(got, beforeIncoming) {
				t.Errorf("invalid context changed incoming identity/context/liveness: before=%+v after=%+v", beforeIncoming, got)
			}
			if got := other.snapshot(); !reflect.DeepEqual(got, beforeOther) {
				t.Errorf("invalid context changed other window: before=%+v after=%+v", beforeOther, got)
			}
			if len(s.hub.windows) != beforeCount || s.hub.windows["w1"] != other {
				t.Errorf("invalid context removed another registration: windows=%v", s.hub.windows)
			}
			if tc.registered && s.hub.windows["w2"] != incoming {
				t.Error("invalid context removed the current registration")
			}
			for id, c := range s.hub.windows {
				if id != c.id() {
					t.Errorf("hub key %q differs from connection identity %q", id, c.id())
				}
			}
			for _, c := range []*conn{incoming, other} {
				select {
				case <-c.done:
					t.Error("invalid context disconnected a window")
				default:
				}
			}
		})
	}
}

func TestHandleFrameRegisteredContextCanSwitchDocument(t *testing.T) {
	s := New(Options{Version: "1.7.1"})
	c := newConn(nil, time.Now().UTC())
	sendContextIdentityFrame(t, s, c, protocol.Register{
		Type: protocol.TypeRegister, WindowID: "w2", ConnectorVersion: "1.7.1",
	})
	for _, msg := range []protocol.ContextMessage{
		{Type: protocol.TypeContext, WindowID: "w2", ProjectUUID: "first-project", ProjectName: "First",
			DocumentUUID: "schematic", DocumentType: "schematic", TabID: "first-tab"},
		{Type: protocol.TypeContext, WindowID: "w2", ProjectUUID: "next-project", ProjectName: "Next",
			DocumentUUID: "pcb", DocumentType: "pcb", TabID: "next-tab", Unit: "mil"},
	} {
		before := c.snapshot()
		sendContextIdentityFrame(t, s, c, msg)
		after := c.snapshot()
		if after.WindowID != "w2" || after.Context != msg.Context() {
			t.Fatalf("same-session context not applied: %+v", after)
		}
		if after.LastSeen.Before(before.LastSeen) || len(s.hub.windows) != 1 || s.hub.windows["w2"] != c {
			t.Fatal("same-session document change corrupted registration/liveness")
		}
	}
}

func TestHandleFrameSameDocumentConnectionsStayIndependent(t *testing.T) {
	for _, versions := range [][2]string{
		{"1.7.1-dev.8", "1.7.1-dev.8"},
		{"1.7.1-dev.8", "1.7.1-dev.9"},
		{"1.7.1-dev.9", "1.7.1-dev.8"},
		{"1.7.1", "1.7.1-dev.9"},
		{"unknown", "1.7.1"},
	} {
		t.Run(versions[0]+"_"+versions[1], func(t *testing.T) {
			s := New(Options{})
			conns := []*conn{newConn(nil, time.Now().Add(-time.Second)), newConn(nil, time.Now())}
			for i, c := range conns {
				id := []string{"first", "second"}[i]
				sendContextIdentityFrame(t, s, c, protocol.Register{Type: protocol.TypeRegister, WindowID: id, ConnectorVersion: versions[i]})
			}
			for round := 0; round < 10; round++ {
				for _, c := range conns {
					sendContextIdentityFrame(t, s, c, protocol.ContextMessage{
						Type: protocol.TypeContext, WindowID: c.id(), ProjectUUID: "p", ProjectName: "same",
						DocumentUUID: "d", DocumentType: "schematic", TabID: "tab",
					})
				}
			}
			if len(s.hub.list()) != 2 {
				t.Fatal("shared document retired a live connection")
			}
			if _, ok := s.hub.target(""); ok {
				t.Fatal("shared document implicitly selected a connection")
			}
			if _, found, ambiguous := s.hub.windowForProject("p", "schematic"); found || !ambiguous {
				t.Fatal("shared project must remain ambiguous")
			}
			for _, c := range conns {
				if target, ok := s.hub.target(c.id()); !ok || target != c {
					t.Fatal("explicit target changed")
				}
				select {
				case <-c.done:
					t.Fatal("shared document disconnected a transport")
				default:
				}
			}
		})
	}
}
