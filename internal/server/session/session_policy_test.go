package session

import (
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestCreateSessionSetsTokenMetadataWithDefaultTTL(t *testing.T) {
	r := NewRegistry()
	s := r.Create()

	issuedAt, expiresAt := s.TokenMetadata()
	if issuedAt.IsZero() {
		t.Fatalf("TokenIssuedAt must be set")
	}
	if expiresAt.IsZero() {
		t.Fatalf("TokenExpiresAt must be set")
	}

	ttl := expiresAt.Sub(issuedAt)
	if ttl != DefaultTokenTTL {
		t.Fatalf("token TTL: got %s want %s", ttl, DefaultTokenTTL)
	}
}

func TestIsTokenExpired(t *testing.T) {
	r := NewRegistry()
	s := r.Create()

	issuedAt, _ := s.TokenMetadata()
	s.TokenExpiresAt = issuedAt.Add(2 * time.Minute)

	if s.IsTokenExpired(issuedAt.Add(90 * time.Second)) {
		t.Fatalf("token should not be expired before expiry time")
	}
	if !s.IsTokenExpired(issuedAt.Add(2 * time.Minute)) {
		t.Fatalf("token should be expired at expiry timestamp")
	}
}

func TestAgentDisconnectMovesSessionToDegradedAndKeepsViewers(t *testing.T) {
	r := NewRegistry()
	s := r.Create()

	agentConn := &websocket.Conn{}
	viewerConn := &websocket.Conn{}

	if ok := s.TryAttachAgent(agentConn); !ok {
		t.Fatalf("attach agent: expected success")
	}

	outbound := make(chan []byte, 1)
	if ok := s.AddViewer("viewer-1", viewerConn, outbound); !ok {
		t.Fatalf("add viewer: expected success")
	}

	if got := s.State(); got != SessionStateActive {
		t.Fatalf("state before detach: got %q want %q", got, SessionStateActive)
	}

	s.DetachAgent(agentConn)

	hasAgent, viewers := s.ConnectionState()
	if hasAgent {
		t.Fatalf("agent connection should be detached")
	}
	if viewers != 1 {
		t.Fatalf("viewer count after agent disconnect: got %d want 1", viewers)
	}
	if got := s.State(); got != SessionStateDegraded {
		t.Fatalf("state after detach: got %q want %q", got, SessionStateDegraded)
	}
}

func TestViewerDisconnectRemovesOnlyThatViewer(t *testing.T) {
	r := NewRegistry()
	s := r.Create()

	viewer1Conn := &websocket.Conn{}
	viewer2Conn := &websocket.Conn{}

	if ok := s.AddViewer("viewer-1", viewer1Conn, make(chan []byte, 1)); !ok {
		t.Fatalf("add viewer-1: expected success")
	}
	if ok := s.AddViewer("viewer-2", viewer2Conn, make(chan []byte, 1)); !ok {
		t.Fatalf("add viewer-2: expected success")
	}

	_, viewersBefore := s.ConnectionState()
	if viewersBefore != 2 {
		t.Fatalf("viewer count before remove: got %d want 2", viewersBefore)
	}

	s.RemoveViewer("viewer-1")

	hasAgent, viewersAfter := s.ConnectionState()
	if hasAgent {
		t.Fatalf("expected no agent attached")
	}
	if viewersAfter != 1 {
		t.Fatalf("viewer count after remove: got %d want 1", viewersAfter)
	}
	if got := s.State(); got != SessionStateDegraded {
		t.Fatalf("state after removing one viewer: got %q want %q", got, SessionStateDegraded)
	}
}

func TestCleanupIdleSessionsDeletesExpiredIdleSession(t *testing.T) {
	r := NewRegistry()
	s := r.Create()

	s.idleSince = time.Now().Add(-DefaultIdleTTL - 5*time.Second)

	deleted := r.CleanupIdleSessions(time.Now(), DefaultIdleTTL, nil)
	if deleted != 1 {
		t.Fatalf("deleted sessions: got %d want 1", deleted)
	}

	if _, ok := r.Get(s.ID); ok {
		t.Fatalf("session should be deleted from registry")
	}
}

func TestCleanupIdleSessionsSkipsActiveSession(t *testing.T) {
	r := NewRegistry()
	s := r.Create()

	if ok := s.TryAttachAgent(&websocket.Conn{}); !ok {
		t.Fatalf("attach agent: expected success")
	}

	s.idleSince = time.Now().Add(-DefaultIdleTTL - 5*time.Second)

	deleted := r.CleanupIdleSessions(time.Now(), DefaultIdleTTL, nil)
	if deleted != 0 {
		t.Fatalf("deleted sessions: got %d want 0", deleted)
	}

	if _, ok := r.Get(s.ID); !ok {
		t.Fatalf("active session should not be deleted")
	}
}

func TestExpireAndCloseClearsResidualViewerResources(t *testing.T) {
	r := NewRegistry()
	s := r.Create()

	outbound := make(chan []byte, 1)
	if ok := s.AddViewer("viewer-1", nil, outbound); !ok {
		t.Fatalf("add viewer: expected success")
	}

	s.ExpireAndClose("idle cleanup")

	if got := s.State(); got != SessionStateExpired {
		t.Fatalf("session state: got %q want %q", got, SessionStateExpired)
	}

	if len(s.Viewers) != 0 {
		t.Fatalf("viewers should be cleared: got %d", len(s.Viewers))
	}
	if len(s.viewerOutbound) != 0 {
		t.Fatalf("viewer outbound channels should be cleared: got %d", len(s.viewerOutbound))
	}
	if len(s.viewerStates) != 0 {
		t.Fatalf("viewer states should be cleared: got %d", len(s.viewerStates))
	}
	if len(s.viewerLastSeen) != 0 {
		t.Fatalf("viewer last-seen map should be cleared: got %d", len(s.viewerLastSeen))
	}

	_, ok := <-outbound
	if ok {
		t.Fatalf("outbound channel should be closed")
	}
}

func TestMetricsSnapshotIncludesViewerDropCounters(t *testing.T) {
	r := NewRegistry()
	s := r.Create()

	slow := make(chan []byte, 1)
	fast := make(chan []byte, 1)

	if ok := s.AddViewer("viewer-slow", nil, slow); !ok {
		t.Fatalf("add slow viewer: expected success")
	}
	if ok := s.AddViewer("viewer-fast", nil, fast); !ok {
		t.Fatalf("add fast viewer: expected success")
	}

	// Fill slow viewer queue so a new fanout enqueue drops for this viewer.
	slow <- []byte("queued")

	forwarded, dropped := s.EnqueueFrameForViewers([]byte("frame"))
	if forwarded != 1 || dropped != 1 {
		t.Fatalf("fanout result: got forwarded=%d dropped=%d want forwarded=1 dropped=1", forwarded, dropped)
	}
	s.AddDroppedFrames(dropped)

	metrics := s.MetricsSnapshot()
	if metrics.FramesDropped != 1 {
		t.Fatalf("session dropped frames: got %d want 1", metrics.FramesDropped)
	}
	if got := metrics.ViewerDrops["viewer-slow"]; got != 1 {
		t.Fatalf("slow viewer drops in metrics: got %d want 1", got)
	}
	if got := metrics.ViewerDrops["viewer-fast"]; got != 0 {
		t.Fatalf("fast viewer drops in metrics: got %d want 0", got)
	}
}

func TestSessionStateTransitionsAcrossLifecycle(t *testing.T) {
	r := NewRegistry()
	s := r.Create()

	if got := s.State(); got != SessionStatePending {
		t.Fatalf("initial state: got %q want %q", got, SessionStatePending)
	}

	agentConn := &websocket.Conn{}
	if ok := s.TryAttachAgent(agentConn); !ok {
		t.Fatalf("attach agent: expected success")
	}
	if got := s.State(); got != SessionStateActive {
		t.Fatalf("state after agent attach: got %q want %q", got, SessionStateActive)
	}

	if ok := s.AddViewer("viewer-1", &websocket.Conn{}, make(chan []byte, 1)); !ok {
		t.Fatalf("add viewer: expected success")
	}
	if got := s.State(); got != SessionStateActive {
		t.Fatalf("state with agent+viewer: got %q want %q", got, SessionStateActive)
	}

	s.DetachAgent(agentConn)
	if got := s.State(); got != SessionStateDegraded {
		t.Fatalf("state after agent detach: got %q want %q", got, SessionStateDegraded)
	}

	s.RemoveViewer("viewer-1")
	if got := s.State(); got != SessionStateClosed {
		t.Fatalf("state after last viewer removed: got %q want %q", got, SessionStateClosed)
	}

	if ok := s.MarkExpired("lifecycle complete"); !ok {
		t.Fatalf("mark expired: expected success")
	}
	if got := s.State(); got != SessionStateExpired {
		t.Fatalf("state after mark expired: got %q want %q", got, SessionStateExpired)
	}
}

func TestAgentHeartbeatStaleTransitionAndIdleDuration(t *testing.T) {
	r := NewRegistry()
	s := r.Create()

	if ok := s.SetAgentConnectionState(ConnectionStateConnecting, "connect start"); !ok {
		t.Fatalf("set agent state to connecting: expected success")
	}
	if ok := s.SetAgentConnectionState(ConnectionStateAuthenticated, "auth ok"); !ok {
		t.Fatalf("set agent state to authenticated: expected success")
	}
	if ok := s.SetAgentConnectionState(ConnectionStateStreaming, "stream start"); !ok {
		t.Fatalf("set agent state to streaming: expected success")
	}

	now := time.Now()
	lastSeen := now.Add(-16 * time.Second)
	s.TouchAgentLastSeen(lastSeen)

	idle := s.AgentIdleDuration(now)
	if idle < 16*time.Second {
		t.Fatalf("agent idle duration: got %s want at least %s", idle, 16*time.Second)
	}

	if ok := s.SetAgentConnectionState(ConnectionStateStale, "heartbeat timeout"); !ok {
		t.Fatalf("set agent state to stale: expected success")
	}
	if got := s.AgentConnectionState(); got != ConnectionStateStale {
		t.Fatalf("agent state after timeout: got %q want %q", got, ConnectionStateStale)
	}
}

func TestViewerHeartbeatStaleTransitionAndIdleDuration(t *testing.T) {
	r := NewRegistry()
	s := r.Create()

	if ok := s.AddViewer("viewer-1", &websocket.Conn{}, make(chan []byte, 1)); !ok {
		t.Fatalf("add viewer: expected success")
	}
	if ok := s.SetViewerConnectionState("viewer-1", ConnectionStateAuthenticated, "auth ok"); !ok {
		t.Fatalf("set viewer state to authenticated: expected success")
	}
	if ok := s.SetViewerConnectionState("viewer-1", ConnectionStateStreaming, "stream start"); !ok {
		t.Fatalf("set viewer state to streaming: expected success")
	}

	now := time.Now()
	lastSeen := now.Add(-20 * time.Second)
	s.TouchViewerLastSeen("viewer-1", lastSeen)

	idle := s.ViewerIdleDuration("viewer-1", now)
	if idle < 20*time.Second {
		t.Fatalf("viewer idle duration: got %s want at least %s", idle, 20*time.Second)
	}

	if ok := s.SetViewerConnectionState("viewer-1", ConnectionStateStale, "heartbeat timeout"); !ok {
		t.Fatalf("set viewer state to stale: expected success")
	}
}

func TestTokenWithoutExpiryIsNotExpired(t *testing.T) {
	r := NewRegistry()
	s := r.Create()

	s.TokenExpiresAt = time.Time{}
	if s.IsTokenExpired(time.Now().Add(24 * time.Hour)) {
		t.Fatalf("token with zero expiry should never be considered expired")
	}
}

func TestDuplicateJoinBehavior(t *testing.T) {
	r := NewRegistry()
	s := r.Create()

	if ok := s.TryAttachAgent(&websocket.Conn{}); !ok {
		t.Fatalf("first agent attach: expected success")
	}
	if ok := s.TryAttachAgent(&websocket.Conn{}); ok {
		t.Fatalf("second agent attach should be rejected")
	}

	if ok := s.AddViewer("viewer-dup", &websocket.Conn{}, make(chan []byte, 1)); !ok {
		t.Fatalf("first viewer add: expected success")
	}
	if ok := s.AddViewer("viewer-dup", &websocket.Conn{}, make(chan []byte, 1)); ok {
		t.Fatalf("duplicate viewer add should be rejected")
	}
}
