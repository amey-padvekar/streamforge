package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"streamforge/internal/server/session"
)

func TestCanSendInputRejectsViewOnlyViewer(t *testing.T) {
	r := session.NewRegistry()
	s := r.Create()

	if ok := s.TryAttachAgent(&websocket.Conn{}); !ok {
		t.Fatalf("attach agent: expected success")
	}
	if ok := s.SetAgentConnectionState(session.ConnectionStateConnecting, "agent websocket accepted"); !ok {
		t.Fatalf("set agent state connecting: expected success")
	}
	if ok := s.SetAgentConnectionState(session.ConnectionStateAuthenticated, "agent auth complete"); !ok {
		t.Fatalf("set agent state: expected success")
	}

	if ok := s.AddViewer("viewer-1", &websocket.Conn{}, make(chan []byte, 1)); !ok {
		t.Fatalf("add viewer: expected success")
	}
	if ok := s.SetViewerConnectionState("viewer-1", session.ConnectionStateAuthenticated, "viewer auth complete"); !ok {
		t.Fatalf("set viewer state: expected success")
	}

	if CanSendInput(s, "viewer-1") {
		t.Fatalf("view-only viewer must be denied control")
	}
}

func TestCanSendInputAllowsControlEnabledViewer(t *testing.T) {
	r := session.NewRegistry()
	s := r.Create()

	if ok := s.TryAttachAgent(&websocket.Conn{}); !ok {
		t.Fatalf("attach agent: expected success")
	}
	if ok := s.SetAgentConnectionState(session.ConnectionStateConnecting, "agent websocket accepted"); !ok {
		t.Fatalf("set agent state connecting: expected success")
	}
	if ok := s.SetAgentConnectionState(session.ConnectionStateAuthenticated, "agent auth complete"); !ok {
		t.Fatalf("set agent state: expected success")
	}

	if ok := s.AddViewer("viewer-1", &websocket.Conn{}, make(chan []byte, 1)); !ok {
		t.Fatalf("add viewer: expected success")
	}
	if ok := s.SetViewerConnectionState("viewer-1", session.ConnectionStateAuthenticated, "viewer auth complete"); !ok {
		t.Fatalf("set viewer state: expected success")
	}

	now := time.Now().UTC().Round(0)
	if ok := s.SetViewerControlRole("viewer-1", session.ViewerRoleControlEnabled, "owner-1", now); !ok {
		t.Fatalf("set viewer control role: expected success")
	}

	if !CanSendInput(s, "viewer-1") {
		t.Fatalf("control-enabled viewer should be allowed")
	}
}

func TestValidateSessionControlStateRejectsNoActiveAgent(t *testing.T) {
	r := session.NewRegistry()
	s := r.Create()

	err := ValidateSessionControlState(s)
	if !errors.Is(err, ErrNoActiveAgent) {
		t.Fatalf("validate session control state: got %v want %v", err, ErrNoActiveAgent)
	}
}

func TestValidateSessionControlStateRejectsExpiredToken(t *testing.T) {
	r := session.NewRegistry()
	s := r.Create()

	if ok := s.TryAttachAgent(&websocket.Conn{}); !ok {
		t.Fatalf("attach agent: expected success")
	}
	if ok := s.SetAgentConnectionState(session.ConnectionStateConnecting, "agent websocket accepted"); !ok {
		t.Fatalf("set agent state connecting: expected success")
	}
	if ok := s.SetAgentConnectionState(session.ConnectionStateAuthenticated, "agent auth complete"); !ok {
		t.Fatalf("set agent state: expected success")
	}

	s.TokenExpiresAt = time.Now().Add(-1 * time.Second)
	err := ValidateSessionControlState(s)
	if !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("validate session control state: got %v want %v", err, ErrTokenExpired)
	}
}

func TestValidateInputRateRejectsBurstOverrun(t *testing.T) {
	r := session.NewRegistry()
	s := r.Create()

	defaultLimiter.mu.Lock()
	defaultLimiter.buckets = make(map[string]*inputTokenBucket)
	defaultLimiter.mu.Unlock()

	now := time.Now().UTC().Round(0)
	var lastErr error
	for i := 0; i < int(defaultInputBurstTokens)+1; i++ {
		lastErr = ValidateInputRate(s, "viewer-1", now)
	}

	if !errors.Is(lastErr, ErrInputRateLimited) {
		t.Fatalf("validate input rate: got %v want %v", lastErr, ErrInputRateLimited)
	}
}
