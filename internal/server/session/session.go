package session

import (
	"log/slog"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type Role string

const (
	RoleAgent  Role = "agent"
	RoleViewer Role = "viewer"
)

type SessionState string

const (
	SessionStatePending  SessionState = "pending"
	SessionStateActive   SessionState = "active"
	SessionStateDegraded SessionState = "degraded"
	SessionStateClosed   SessionState = "closed"
	SessionStateExpired  SessionState = "expired"
)

type ConnectionState string

const (
	ConnectionStateDisconnected  ConnectionState = "disconnected"
	ConnectionStateConnecting    ConnectionState = "connecting"
	ConnectionStateAuthenticated ConnectionState = "authenticated"
	ConnectionStateStreaming     ConnectionState = "streaming"
	ConnectionStateStale         ConnectionState = "stale"
)

type Session struct {
	ID string

	AgentToken  string
	ViewerToken string

	TokenIssuedAt  time.Time
	TokenExpiresAt time.Time

	AgentConn *websocket.Conn

	Viewers map[string]*websocket.Conn

	CreatedAt time.Time

	state         SessionState
	agentState    ConnectionState
	agentLastSeen time.Time
	idleSince     time.Time

	viewerOutbound  map[string]chan []byte
	viewerStates    map[string]ConnectionState
	viewerLastSeen  map[string]time.Time
	framesReceived  uint64
	framesForwarded uint64
	droppedFrames   uint64

	mu sync.RWMutex
}

type MetricsSnapshot struct {
	SessionID       string
	State           SessionState
	FramesReceived  uint64
	FramesForwarded uint64
	FramesDropped   uint64
	ViewerCount     int
}

var validSessionTransitions = map[SessionState]map[SessionState]struct{}{
	SessionStatePending: {
		SessionStateActive:   {},
		SessionStateDegraded: {},
		SessionStateClosed:   {},
		SessionStateExpired:  {},
	},
	SessionStateActive: {
		SessionStateDegraded: {},
		SessionStateClosed:   {},
		SessionStateExpired:  {},
	},
	SessionStateDegraded: {
		SessionStateActive:  {},
		SessionStateClosed:  {},
		SessionStateExpired: {},
	},
	SessionStateClosed: {
		SessionStateActive:   {},
		SessionStateDegraded: {},
		SessionStateExpired:  {},
	},
	SessionStateExpired: {},
}

var validConnectionTransitions = map[ConnectionState]map[ConnectionState]struct{}{
	ConnectionStateDisconnected: {
		ConnectionStateConnecting: {},
	},
	ConnectionStateConnecting: {
		ConnectionStateAuthenticated: {},
		ConnectionStateDisconnected:  {},
		ConnectionStateStale:         {},
	},
	ConnectionStateAuthenticated: {
		ConnectionStateStreaming:    {},
		ConnectionStateDisconnected: {},
		ConnectionStateStale:        {},
	},
	ConnectionStateStreaming: {
		ConnectionStateStale:        {},
		ConnectionStateDisconnected: {},
	},
	ConnectionStateStale: {
		ConnectionStateConnecting:   {},
		ConnectionStateDisconnected: {},
	},
}

func isValidTransition[T comparable](valid map[T]map[T]struct{}, from T, to T) bool {
	next, ok := valid[from]
	if !ok {
		return false
	}
	_, ok = next[to]
	return ok
}

func (s *Session) ConnectionState() (hasAgent bool, viewerCount int) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.AgentConn != nil, len(s.Viewers)
}

func (s *Session) State() SessionState {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.state
}

func (s *Session) AgentConnectionState() ConnectionState {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.agentState
}

func (s *Session) TokenMetadata() (issuedAt time.Time, expiresAt time.Time) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.TokenIssuedAt, s.TokenExpiresAt
}

func (s *Session) IsTokenExpired(now time.Time) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.TokenExpiresAt.IsZero() {
		return false
	}

	return !now.Before(s.TokenExpiresAt)
}

func (s *Session) TouchAgentLastSeen(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.agentLastSeen = now
}

func (s *Session) TouchViewerLastSeen(viewerID string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.viewerLastSeen == nil {
		s.viewerLastSeen = make(map[string]time.Time)
	}

	s.viewerLastSeen[viewerID] = now
}

func (s *Session) AgentIdleDuration(now time.Time) time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.agentLastSeen.IsZero() {
		return 0
	}

	return now.Sub(s.agentLastSeen)
}

func (s *Session) ViewerIdleDuration(viewerID string, now time.Time) time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()

	lastSeen, ok := s.viewerLastSeen[viewerID]
	if !ok || lastSeen.IsZero() {
		return 0
	}

	return now.Sub(lastSeen)
}

func (s *Session) IsIdleExpired(now time.Time, idleTTL time.Duration) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if idleTTL <= 0 {
		return false
	}
	if s.AgentConn != nil || len(s.Viewers) > 0 {
		return false
	}
	if s.idleSince.IsZero() {
		return false
	}

	return now.Sub(s.idleSince) >= idleTTL
}

func (s *Session) IdleSince() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.idleSince
}

func (s *Session) ExpireAndClose(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.AgentConn != nil {
		_ = s.transitionConnectionStateLocked("agent", s.agentState, ConnectionStateDisconnected, reason, "")
		_ = s.AgentConn.Close()
		s.AgentConn = nil
		s.agentState = ConnectionStateDisconnected
	}

	for viewerID, conn := range s.Viewers {
		if current, ok := s.viewerStates[viewerID]; ok && current != ConnectionStateDisconnected {
			_ = s.transitionConnectionStateLocked("viewer", current, ConnectionStateDisconnected, reason, viewerID)
		}
		if conn != nil {
			_ = conn.Close()
		}
		delete(s.Viewers, viewerID)
		delete(s.viewerStates, viewerID)
		delete(s.viewerLastSeen, viewerID)
	}

	for viewerID, outbound := range s.viewerOutbound {
		delete(s.viewerOutbound, viewerID)
		if outbound != nil {
			close(outbound)
		}
	}

	s.idleSince = nowOrFallback(s.idleSince)
	_ = s.transitionSessionStateLocked(SessionStateExpired, reason)
}

func (s *Session) MarkExpired(reason string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.transitionSessionStateLocked(SessionStateExpired, reason)
}

func (s *Session) SetAgentConnectionState(next ConnectionState, reason string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.transitionConnectionStateLocked("agent", s.agentState, next, reason, "") {
		return false
	}

	s.agentState = next
	return true
}

func (s *Session) SetViewerConnectionState(viewerID string, next ConnectionState, reason string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	current, exists := s.viewerStates[viewerID]
	if !exists {
		slog.Warn("viewer connection state transition rejected", "sessionId", s.ID, "viewerId", viewerID, "errorCategory", "state", "reason", "viewer not registered", "to", next)
		return false
	}

	if !s.transitionConnectionStateLocked("viewer", current, next, reason, viewerID) {
		return false
	}

	s.viewerStates[viewerID] = next
	return true
}

func (s *Session) TryAttachAgent(conn *websocket.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.AgentConn != nil {
		return false
	}

	s.AgentConn = conn
	s.recomputeSessionStateLocked("agent attached")
	return true
}

func (s *Session) DetachAgent(conn *websocket.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if conn == nil || s.AgentConn == conn {
		s.AgentConn = nil
		s.recomputeSessionStateLocked("agent detached")
	}
}

func (s *Session) AddViewer(viewerID string, conn *websocket.Conn, outbound chan []byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.Viewers[viewerID]; exists {
		return false
	}

	if s.viewerOutbound == nil {
		s.viewerOutbound = make(map[string]chan []byte)
	}
	if s.viewerStates == nil {
		s.viewerStates = make(map[string]ConnectionState)
	}
	if s.viewerLastSeen == nil {
		s.viewerLastSeen = make(map[string]time.Time)
	}

	s.Viewers[viewerID] = conn
	s.viewerOutbound[viewerID] = outbound
	s.viewerStates[viewerID] = ConnectionStateConnecting
	s.viewerLastSeen[viewerID] = time.Now()
	slog.Info("viewer connection state transitioned", "sessionId", s.ID, "viewerId", viewerID, "from", ConnectionStateDisconnected, "to", ConnectionStateConnecting, "reason", "viewer registered")
	s.recomputeSessionStateLocked("viewer attached")

	return true
}

func (s *Session) RemoveViewer(viewerID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.Viewers, viewerID)
	if outbound, exists := s.viewerOutbound[viewerID]; exists {
		delete(s.viewerOutbound, viewerID)
		close(outbound)
	}
	if current, exists := s.viewerStates[viewerID]; exists {
		if current != ConnectionStateDisconnected {
			_ = s.transitionConnectionStateLocked("viewer", current, ConnectionStateDisconnected, "viewer removed", viewerID)
		}
		delete(s.viewerStates, viewerID)
	}
	delete(s.viewerLastSeen, viewerID)
	s.recomputeSessionStateLocked("viewer detached")
}

func (s *Session) EnqueueFrameForViewers(frame []byte) (forwarded int, dropped int) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, outbound := range s.viewerOutbound {
		select {
		case outbound <- frame:
			forwarded++
		default:
			dropped++
		}
	}

	return forwarded, dropped
}

func (s *Session) AddReceivedFrames(count int) {
	if count <= 0 {
		return
	}

	s.mu.Lock()
	s.framesReceived += uint64(count)
	s.mu.Unlock()
}

func (s *Session) AddForwardedFrames(count int) {
	if count <= 0 {
		return
	}

	s.mu.Lock()
	s.framesForwarded += uint64(count)
	s.mu.Unlock()
}

func (s *Session) AddDroppedFrames(count int) {
	if count <= 0 {
		return
	}

	s.mu.Lock()
	s.droppedFrames += uint64(count)
	s.mu.Unlock()
}

func (s *Session) DroppedFrames() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.droppedFrames
}

func (s *Session) MetricsSnapshot() MetricsSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return MetricsSnapshot{
		SessionID:       s.ID,
		State:           s.state,
		FramesReceived:  s.framesReceived,
		FramesForwarded: s.framesForwarded,
		FramesDropped:   s.droppedFrames,
		ViewerCount:     len(s.Viewers),
	}
}

func (s *Session) transitionConnectionStateLocked(role string, current ConnectionState, next ConnectionState, reason string, viewerID string) bool {
	if current == next {
		return true
	}

	if !isValidTransition(validConnectionTransitions, current, next) {
		slog.Warn("connection state transition rejected", "sessionId", s.ID, "role", role, "viewerId", viewerID, "from", current, "to", next, "errorCategory", "state", "reason", reason)
		return false
	}

	slog.Info("connection state transitioned", "sessionId", s.ID, "role", role, "viewerId", viewerID, "from", current, "to", next, "reason", reason)
	return true
}

func (s *Session) transitionSessionStateLocked(next SessionState, reason string) bool {
	if s.state == next {
		return true
	}

	if !isValidTransition(validSessionTransitions, s.state, next) {
		slog.Warn("session state transition rejected", "sessionId", s.ID, "from", s.state, "to", next, "errorCategory", "state", "reason", reason)
		return false
	}

	slog.Info("session state transitioned", "sessionId", s.ID, "from", s.state, "to", next, "reason", reason)
	s.state = next
	return true
}

func (s *Session) recomputeSessionStateLocked(reason string) {
	now := time.Now()

	if s.state == SessionStateExpired {
		return
	}

	next := s.state
	if s.AgentConn != nil {
		s.idleSince = time.Time{}
		next = SessionStateActive
	} else if len(s.Viewers) > 0 {
		s.idleSince = time.Time{}
		next = SessionStateDegraded
	} else if s.state == SessionStatePending {
		if s.idleSince.IsZero() {
			s.idleSince = now
		}
		next = SessionStatePending
	} else {
		if s.idleSince.IsZero() {
			s.idleSince = now
		}
		next = SessionStateClosed
	}

	_ = s.transitionSessionStateLocked(next, reason)
}

func nowOrFallback(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now()
	}
	return t
}
