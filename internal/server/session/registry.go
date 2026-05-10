package session

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type Registry struct {
	sessions map[string]*Session

	mu sync.RWMutex
}

func NewRegistry() *Registry {
	return &Registry{
		sessions: make(map[string]*Session),
	}
}

func (r *Registry) Create() *Session {
	sessionID := generateToken(16)
	agentToken := generateToken(16)
	viewerToken := generateToken(16)

	session := &Session{
		ID:          sessionID,
		AgentToken:  agentToken,
		ViewerToken: viewerToken,
		Viewers:     make(map[string]*websocket.Conn),
		CreatedAt:   time.Now(),
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

func generateToken(size int) string {
	bytes := make([]byte, size)

	_, err := rand.Read(bytes)
	if err != nil {
		panic(err)
	}

	return hex.EncodeToString(bytes)
}
