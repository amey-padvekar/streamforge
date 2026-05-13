package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type Registry struct {
	sessions map[string]*Session

	mu sync.RWMutex
}

const DefaultTokenTTL = 30 * time.Minute

const (
	DefaultCleanupInterval = 60 * time.Second
	DefaultIdleTTL         = 10 * time.Minute
)

func NewRegistry() *Registry {
	return &Registry{
		sessions: make(map[string]*Session),
	}
}

func (r *Registry) Create() *Session {
	sessionID := generateToken(16)
	agentToken := generateToken(16)
	viewerToken := generateToken(16)
	now := time.Now()

	session := &Session{
		ID:             sessionID,
		AgentToken:     agentToken,
		ViewerToken:    viewerToken,
		TokenIssuedAt:  now,
		TokenExpiresAt: now.Add(DefaultTokenTTL),
		Viewers:        make(map[string]*websocket.Conn),
		state:          SessionStatePending,
		agentState:     ConnectionStateDisconnected,
		agentLastSeen:  now,
		viewerDropped:  make(map[string]uint64),
		viewerStates:   make(map[string]ConnectionState),
		viewerLastSeen: make(map[string]time.Time),
		CreatedAt:      now,
	}

	r.mu.Lock()
	r.sessions[sessionID] = session
	r.mu.Unlock()

	return session

}

func (r *Registry) Get(id string) (*Session, bool) {
	r.mu.RLock()
	session, exists := r.sessions[id]
	r.mu.RUnlock()

	return session, exists
}

func (r *Registry) Delete(id string) {
	r.mu.Lock()
	delete(r.sessions, id)
	r.mu.Unlock()
}

func (r *Registry) List() []*Session {
	r.mu.RLock()
	defer r.mu.RUnlock()

	list := make([]*Session, 0, len(r.sessions))
	for _, s := range r.sessions {
		list = append(list, s)
	}

	return list
}

func (r *Registry) StartCleanupLoop(ctx context.Context, cleanupInterval time.Duration, idleTTL time.Duration, logger *slog.Logger) {
	if r == nil {
		return
	}
	if cleanupInterval <= 0 {
		cleanupInterval = DefaultCleanupInterval
	}
	if idleTTL <= 0 {
		idleTTL = DefaultIdleTTL
	}
	if logger == nil {
		logger = slog.Default()
	}

	ticker := time.NewTicker(cleanupInterval)

	go func() {
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				r.CleanupIdleSessions(now, idleTTL, logger)
			}
		}
	}()
}

func (r *Registry) CleanupIdleSessions(now time.Time, idleTTL time.Duration, logger *slog.Logger) int {
	if r == nil || idleTTL <= 0 {
		return 0
	}
	if logger == nil {
		logger = slog.Default()
	}

	deleted := 0

	r.mu.Lock()
	for sessionID, s := range r.sessions {
		if !s.IsIdleExpired(now, idleTTL) {
			continue
		}

		idleSince := s.IdleSince()
		s.ExpireAndClose("session idle ttl exceeded")
		delete(r.sessions, sessionID)
		deleted++

		logger.Info(
			"session cleaned up",
			"sessionId", sessionID,
			"errorCategory", "timeout",
			"reason", "session_idle_timeout",
			"idleSince", idleSince,
			"idleTTL", idleTTL.String(),
		)
	}
	r.mu.Unlock()

	return deleted
}

func generateToken(size int) string {
	bytes := make([]byte, size)

	_, err := rand.Read(bytes)
	if err != nil {
		panic(err)
	}

	return hex.EncodeToString(bytes)
}
