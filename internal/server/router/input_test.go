package router

import (
	"errors"
	"testing"

	"github.com/gorilla/websocket"

	"streamforge/internal/server/session"
)

func TestRouteInput_RejectsWhenNoActiveAgentQueue(t *testing.T) {
	r := session.NewRegistry()
	s := r.Create()

	err := RouteInput(s, []byte("input"))
	if !errors.Is(err, ErrNoActiveAgent) {
		t.Fatalf("route input error: got %v want %v", err, ErrNoActiveAgent)
	}
}

func TestRouteInput_DropsOnBackpressureWithoutBlocking(t *testing.T) {
	r := session.NewRegistry()
	s := r.Create()

	s.AgentConn = &websocket.Conn{}
	queue := make(chan []byte, 1)
	s.SetAgentInputQueue(queue)
	defer s.ClearAgentInputQueue(queue)

	if err := RouteInput(s, []byte("input-1")); err != nil {
		t.Fatalf("first route input should succeed, got error: %v", err)
	}

	err := RouteInput(s, []byte("input-2"))
	if !errors.Is(err, ErrInputBackpressure) {
		t.Fatalf("second route input error: got %v want %v", err, ErrInputBackpressure)
	}
	if got := s.AgentInputDropCount(); got != 1 {
		t.Fatalf("agent input drop count: got %d want 1", got)
	}
}
