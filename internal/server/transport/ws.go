package transport

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"streamforge/internal/server/router"
	"streamforge/internal/server/session"
)

const wsSessionPathPrefix = "/ws/session/"

type WSHandler struct {
	Registry *session.Registry

	Upgrader websocket.Upgrader

	AgentHandler     func(*session.Session, *websocket.Conn)
	ViewerHandler    func(*session.Session, *websocket.Conn)
	AgentFrameRouter func(*session.Session, []byte) error

	viewerSeq atomic.Uint64
}

type handshakeMessage struct {
	Role  string `json:"role"`
	Token string `json:"token"`
}

func NewWSHandler(registry *session.Registry) *WSHandler {
	h := &WSHandler{
		Registry: registry,
		Upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(_ *http.Request) bool {
				return true
			},
		},
		AgentFrameRouter: defaultAgentFrameRouter,
	}
	h.AgentHandler = h.handleAgentConnection
	h.ViewerHandler = h.handleViewerConnection
	h.AgentFrameRouter = h.routeAgentFrame

	return h
}

func (h *WSHandler) HandleSessionWS(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Registry == nil {
		http.Error(w, "session registry is not configured", http.StatusInternalServerError)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID, ok := parseSessionID(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}

	s, exists := h.Registry.Get(sessionID)
	if !exists {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	conn, err := h.Upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	role, token, err := readHandshake(conn)
	if err != nil {
		closeWithProtocolError(conn, websocket.ClosePolicyViolation, "invalid auth handshake")
		return
	}

	if !isAuthorized(s, role, token) {
		closeWithProtocolError(conn, websocket.ClosePolicyViolation, "invalid role or token")
		return
	}

	switch role {
	case session.RoleAgent:
		h.AgentHandler(s, conn)
	case session.RoleViewer:
		h.ViewerHandler(s, conn)
	default:
		closeWithProtocolError(conn, websocket.ClosePolicyViolation, "unsupported role")
	}
}

func parseSessionID(path string) (string, bool) {
	if !strings.HasPrefix(path, wsSessionPathPrefix) {
		return "", false
	}

	sessionID := strings.Trim(strings.TrimPrefix(path, wsSessionPathPrefix), "/")
	if sessionID == "" || strings.Contains(sessionID, "/") {
		return "", false
	}

	return sessionID, true
}

func readHandshake(conn *websocket.Conn) (session.Role, string, error) {
	_, message, err := conn.ReadMessage()
	if err != nil {
		return "", "", err
	}

	var payload handshakeMessage
	if err := json.Unmarshal(message, &payload); err != nil {
		return "", "", err
	}

	role := session.Role(strings.ToLower(strings.TrimSpace(payload.Role)))
	token := strings.TrimSpace(payload.Token)
	if token == "" {
		return "", "", errors.New("empty token")
	}

	return role, token, nil
}

func isAuthorized(s *session.Session, role session.Role, token string) bool {
	switch role {
	case session.RoleAgent:
		return token == s.AgentToken
	case session.RoleViewer:
		return token == s.ViewerToken
	default:
		return false
	}
}

func closeWithProtocolError(conn *websocket.Conn, code int, reason string) {
	deadline := time.Now().Add(2 * time.Second)
	payload := websocket.FormatCloseMessage(code, reason)
	_ = conn.WriteControl(websocket.CloseMessage, payload, deadline)
	_ = conn.Close()
}

func defaultViewerHandler(_ *session.Session, conn *websocket.Conn) {
	closeWithProtocolError(conn, websocket.CloseNormalClosure, "viewer handler not implemented")
}

func defaultAgentFrameRouter(_ *session.Session, _ []byte) error {
	return nil
}

func (h *WSHandler) routeAgentFrame(s *session.Session, frame []byte) error {
	forwarded, dropped := router.FanoutFrame(s, frame)
	if dropped > 0 {
		slog.Warn("viewer queue full, dropping frame", "sessionId", s.ID, "dropped", dropped, "forwarded", forwarded)
	}

	return nil
}

func (h *WSHandler) nextViewerID() string {
	sequence := h.viewerSeq.Add(1)
	return "viewer-" + strconv.FormatUint(sequence, 10)
}
