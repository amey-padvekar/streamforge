package transport

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"streamforge/internal/protocol"
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

	role, token, err := readBinaryHandshake(conn)
	if err != nil {
		slog.Warn("websocket handshake rejected", "errorCategory", "protocol", "reason", err.Error())
		closeWithProtocolError(conn, websocket.ClosePolicyViolation, "invalid auth handshake")
		return
	}

	if !isAuthorized(s, role, token) {
		slog.Warn("websocket auth rejected", "errorCategory", "auth", "reason", "invalid role or token", "role", role)
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

// readBinaryHandshake performs protocol negotiation: reads HELLO, sends ACK/ERROR,
// reads AUTH, sends ACK/ERROR. Returns (role, token, error).
func readBinaryHandshake(conn *websocket.Conn) (session.Role, string, error) {
	// Read HELLO packet
	_, helloData, err := conn.ReadMessage()
	if err != nil {
		return "", "", err
	}

	helloHeader, helloPayload, err := protocol.ParsePacket(helloData)
	if err != nil {
		// Send ERROR response
		logProtocolRejection("parse_error", err.Error())
		sendErrorResponse(conn, "parse_error", err.Error())
		return "", "", err
	}

	if helloHeader.PacketType != protocol.PacketTypeHello {
		logProtocolRejection("invalid_packet_type", "expected HELLO")
		sendErrorResponse(conn, "invalid_packet_type", "expected HELLO")
		return "", "", errors.New("expected HELLO packet type")
	}

	_, err = protocol.DecodeHello(helloPayload)
	if err != nil {
		logProtocolRejection("invalid_hello", err.Error())
		sendErrorResponse(conn, "invalid_hello", err.Error())
		return "", "", err
	}

	// Send ACK response to HELLO
	ackPayload, _ := protocol.EncodeAck(protocol.AckPayload{SelectedVersion: protocol.ProtocolVersion})
	ackHeader := protocol.Header{
		Version:     protocol.ProtocolVersion,
		PacketType:  protocol.PacketTypeAck,
		Flags:       0,
		Reserved:    0,
		SequenceID:  0,
		TimestampNs: 0,
		PayloadLen:  uint32(len(ackPayload)),
	}
	ackPacket, _ := protocol.BuildPacket(ackHeader, ackPayload)
	if err := conn.WriteMessage(websocket.BinaryMessage, ackPacket); err != nil {
		return "", "", fmt.Errorf("send HELLO ACK failed: %w", err)
	}

	// Read AUTH packet
	_, authData, err := conn.ReadMessage()
	if err != nil {
		return "", "", err
	}

	authHeader, authPayload, err := protocol.ParsePacket(authData)
	if err != nil {
		logProtocolRejection("parse_error", err.Error())
		sendErrorResponse(conn, "parse_error", err.Error())
		return "", "", err
	}

	if authHeader.PacketType != protocol.PacketTypeAuth {
		logProtocolRejection("invalid_packet_type", "expected AUTH")
		sendErrorResponse(conn, "invalid_packet_type", "expected AUTH")
		return "", "", errors.New("expected AUTH packet type")
	}

	auth, err := protocol.DecodeAuth(authPayload)
	if err != nil {
		logProtocolRejection("invalid_auth", err.Error())
		sendErrorResponse(conn, "invalid_auth", err.Error())
		return "", "", err
	}

	role := session.Role(auth.Role)
	token := auth.Token

	// Send ACK response to AUTH
	authAckPayload, _ := protocol.EncodeAck(protocol.AckPayload{SelectedVersion: protocol.ProtocolVersion})
	authAckHeader := protocol.Header{
		Version:     protocol.ProtocolVersion,
		PacketType:  protocol.PacketTypeAck,
		Flags:       0,
		Reserved:    0,
		SequenceID:  1,
		TimestampNs: 0,
		PayloadLen:  uint32(len(authAckPayload)),
	}
	authAckPacket, _ := protocol.BuildPacket(authAckHeader, authAckPayload)
	if err := conn.WriteMessage(websocket.BinaryMessage, authAckPacket); err != nil {
		return "", "", fmt.Errorf("send AUTH ACK failed: %w", err)
	}

	return role, token, nil
}

// sendErrorResponse sends an ERROR packet to the peer.
func sendErrorResponse(conn *websocket.Conn, reason, detail string) {
	errPayload, _ := protocol.EncodeError(protocol.ErrorPayload{
		Reason: reason,
		Detail: detail,
	})
	errHeader := protocol.Header{
		Version:     protocol.ProtocolVersion,
		PacketType:  protocol.PacketTypeError,
		Flags:       0,
		Reserved:    0,
		SequenceID:  0,
		TimestampNs: 0,
		PayloadLen:  uint32(len(errPayload)),
	}
	errPacket, _ := protocol.BuildPacket(errHeader, errPayload)
	_ = conn.WriteMessage(websocket.BinaryMessage, errPacket)
}

func logProtocolRejection(reason, detail string) {
	slog.Warn("protocol packet rejected", "errorCategory", "protocol", "reason", reason, "detail", detail)
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
