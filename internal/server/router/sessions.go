package router

import (
	"encoding/json"
	"net/http"
	"strings"

	"streamforge/internal/server/session"
)

type SessionHandler struct {
	Registry *session.Registry
}

func NewSessionHandler(registry *session.Registry) *SessionHandler {
	return &SessionHandler{Registry: registry}
}

func (h *SessionHandler) HandleSessions(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Registry == nil {
		http.Error(w, "session registry is not configured", http.StatusInternalServerError)
		return
	}

	switch r.Method {
	case http.MethodPost:
		h.handleCreateSession(w, r)
	case http.MethodGet:
		h.handleListSessions(w)
	default:
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *SessionHandler) HandleSessionMetrics(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Registry == nil {
		http.Error(w, "session registry is not configured", http.StatusInternalServerError)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	const prefix = "/api/sessions/"
	const suffix = "/metrics"

	if !strings.HasPrefix(r.URL.Path, prefix) || !strings.HasSuffix(r.URL.Path, suffix) {
		http.NotFound(w, r)
		return
	}

	sessionID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix), suffix)
	sessionID = strings.Trim(sessionID, "/")
	if sessionID == "" {
		http.NotFound(w, r)
		return
	}

	if _, ok := h.Registry.Get(sessionID); !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"sessionId": sessionID,
		"metrics":   map[string]any{},
	})
}

func (h *SessionHandler) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	s := h.Registry.Create()

	writeJSON(w, http.StatusCreated, map[string]string{
		"sessionId":   s.ID,
		"agentToken":  s.AgentToken,
		"viewerToken": s.ViewerToken,
		"wsUrl":       websocketURL(r, s.ID),
	})
}

func (h *SessionHandler) handleListSessions(w http.ResponseWriter) {
	type sessionView struct {
		SessionID   string `json:"sessionId"`
		HasAgent    bool   `json:"hasAgent"`
		ViewerCount int    `json:"viewerCount"`
	}

	sessions := h.Registry.List()
	response := make([]sessionView, 0, len(sessions))
	for _, s := range sessions {
		hasAgent, viewerCount := s.ConnectionState()
		response = append(response, sessionView{
			SessionID:   s.ID,
			HasAgent:    hasAgent,
			ViewerCount: viewerCount,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"sessions": response})
}

func websocketURL(r *http.Request, sessionID string) string {
	scheme := "ws"
	if r.TLS != nil {
		scheme = "wss"
	}

	host := r.Host
	if host == "" {
		host = "localhost:8080"
	}

	return scheme + "://" + host + "/ws/session/" + sessionID
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
