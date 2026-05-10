package session

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type Role string

const (
	RoleAgent  Role = "agent"
	RoleViewer Role = "viewer"
)

type Session struct {
	ID string

	AgentToken  string
	ViewerToken string

	AgentConn *websocket.Conn

	Viewers map[string]*websocket.Conn

	CreatedAt time.Time

	viewerOutbound map[string]chan []byte
	droppedFrames  uint64

	mu sync.RWMutex
}

func (s *Session) ConnectionState() (hasAgent bool, viewerCount int) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.AgentConn != nil, len(s.Viewers)
}

func (s *Session) TryAttachAgent(conn *websocket.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.AgentConn != nil {
		return false
	}

	s.AgentConn = conn
	return true
}

func (s *Session) DetachAgent(conn *websocket.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if conn == nil || s.AgentConn == conn {
		s.AgentConn = nil
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

	s.Viewers[viewerID] = conn
	s.viewerOutbound[viewerID] = outbound

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
