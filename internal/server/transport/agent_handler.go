package transport

import (
	"errors"
	"log/slog"
	"net"
	"time"

	"github.com/gorilla/websocket"

	"streamforge/internal/protocol"
	"streamforge/internal/server/session"
)

func (h *WSHandler) handleAgentConnection(s *session.Session, conn *websocket.Conn) {
	if !s.TryAttachAgent(conn) {
		slog.Warn(
			"agent join rejected",
			"sessionId", s.ID,
			"role", session.RoleAgent,
			"errorCategory", "session",
			"reason", "duplicate_agent_join",
		)
		sendErrorResponse(conn, "duplicate_agent_join", "agent already connected for session")
		closeWithProtocolError(conn, websocket.ClosePolicyViolation, "agent already connected")
		return
	}

	_ = s.SetAgentConnectionState(session.ConnectionStateConnecting, "agent websocket accepted")
	_ = s.SetAgentConnectionState(session.ConnectionStateAuthenticated, "agent auth handshake complete")
	s.TouchAgentLastSeen(time.Now())

	slog.Info("agent connected", "sessionId", s.ID)
	framesReceived := 0
	defer func() {
		s.DetachAgent(conn)
		_ = s.SetAgentConnectionState(session.ConnectionStateDisconnected, "agent websocket closed")
		slog.Info("agent disconnected", "sessionId", s.ID)
		_ = conn.Close()
	}()

	for {
		_ = conn.SetReadDeadline(time.Now().Add(StaleThreshold))
		messageType, packet, err := conn.ReadMessage()
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				idleFor := s.AgentIdleDuration(time.Now())
				_ = s.SetAgentConnectionState(session.ConnectionStateStale, "agent heartbeat timeout")
				slog.Warn("agent stale timeout", "sessionId", s.ID, "errorCategory", "timeout", "reason", "agent_stale_timeout", "idleFor", idleFor.String(), "threshold", StaleThreshold.String())
				sendErrorResponse(conn, "timeout", "agent heartbeat timeout")
				closeWithProtocolError(conn, websocket.ClosePolicyViolation, "agent heartbeat timeout")
				return
			}

			if websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				slog.Warn("agent read failed", "sessionId", s.ID, "error", err)
			}
			return
		}

		if messageType != websocket.BinaryMessage {
			closeWithProtocolError(conn, websocket.CloseUnsupportedData, "agent frames must be binary")
			return
		}

		header, _, err := protocol.ParsePacket(packet)
		if err != nil {
			slog.Warn("agent packet rejected", "sessionId", s.ID, "errorCategory", "protocol", "reason", err.Error())
			sendErrorResponse(conn, "parse_error", err.Error())
			closeWithProtocolError(conn, websocket.CloseUnsupportedData, "invalid packet")
			return
		}

		s.TouchAgentLastSeen(time.Now())

		if header.PacketType == protocol.PacketTypeHeartbeat {
			if err := sendHeartbeatResponse(conn, header.SequenceID); err != nil {
				slog.Warn("agent heartbeat echo failed", "sessionId", s.ID, "errorCategory", "transport", "error", err)
				return
			}
			continue
		}

		if header.PacketType != protocol.PacketTypeFrame {
			slog.Warn("agent packet rejected", "sessionId", s.ID, "errorCategory", "protocol", "reason", "unsupported_agent_packet", "packetType", header.PacketType)
			sendErrorResponse(conn, "unsupported_agent_packet", "expected FRAME or HEARTBEAT packet")
			closeWithProtocolError(conn, websocket.CloseUnsupportedData, "unsupported packet type")
			return
		}

		s.AddReceivedFrames(1)
		framesReceived++
		_ = s.SetAgentConnectionState(session.ConnectionStateStreaming, "agent frame stream active")
		slog.Info("agent frame received", "sessionId", s.ID, "frameBytes", len(packet), "framesReceived", framesReceived)

		if err := h.AgentFrameRouter(s, packet); err != nil {
			slog.Warn("agent frame routing failed", "sessionId", s.ID, "error", err)
			closeWithProtocolError(conn, websocket.CloseInternalServerErr, "failed to route frame")
			return
		}
	}
}
