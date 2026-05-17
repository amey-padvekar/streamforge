package transport

import (
	"errors"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"streamforge/internal/protocol"
	"streamforge/internal/server/metrics"
	"streamforge/internal/server/session"
)

const agentInputQueueSize = 256

func (h *WSHandler) handleAgentConnection(s *session.Session, conn *websocket.Conn) {
	if !s.TryAttachAgent(conn) {
		metrics.IncTransportErrors(string(session.RoleAgent), "auth")
		slog.Warn(
			"agent join rejected",
			"sessionId", s.ID,
			"role", session.RoleAgent,
			"frameId", 0,
			"packetType", protocol.PacketTypeAuth,
			"queueDepth", 0,
			"framesDropped", s.DroppedFrames(),
			"errorCategory", "auth",
			"reason", "duplicate_agent_join",
		)
		sendErrorResponse(conn, "duplicate_agent_join", "agent already connected for session")
		closeWithProtocolError(conn, websocket.ClosePolicyViolation, "agent already connected")
		return
	}

	_ = s.SetAgentConnectionState(session.ConnectionStateConnecting, "agent websocket accepted")
	_ = s.SetAgentConnectionState(session.ConnectionStateAuthenticated, "agent auth handshake complete")
	s.TouchAgentLastSeen(time.Now())

	inputQueue := make(chan []byte, agentInputQueueSize)
	s.SetAgentInputQueue(inputQueue)
	var writeMu sync.Mutex

	go func() {
		for packet := range inputQueue {
			writeMu.Lock()
			err := conn.WriteMessage(websocket.BinaryMessage, packet)
			writeMu.Unlock()
			if err != nil {
				metrics.IncTransportErrors(string(session.RoleAgent), "transport")
				slog.Warn("agent input write failed", "sessionId", s.ID, "role", session.RoleAgent, "errorCategory", "transport", "reason", "agent_input_write_failed", "error", err)
				return
			}
		}
	}()

	slog.Info("agent connected", "sessionId", s.ID, "role", session.RoleAgent)
	framesReceived := 0
	defer func() {
		s.ClearAgentInputQueue(inputQueue)
		s.DetachAgent(conn)
		_ = s.SetAgentConnectionState(session.ConnectionStateDisconnected, "agent websocket closed")
		slog.Info("agent disconnected", "sessionId", s.ID, "role", session.RoleAgent)
		_ = conn.Close()
	}()

	for {
		_ = conn.SetReadDeadline(time.Now().Add(StaleThreshold))
		messageType, packet, err := conn.ReadMessage()
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				metrics.IncTransportErrors(string(session.RoleAgent), "timeout")
				idleFor := s.AgentIdleDuration(time.Now())
				_ = s.SetAgentConnectionState(session.ConnectionStateStale, "agent heartbeat timeout")
				slog.Warn("agent stale timeout", "sessionId", s.ID, "role", session.RoleAgent, "frameId", 0, "packetType", protocol.PacketTypeHeartbeat, "queueDepth", 0, "framesDropped", s.DroppedFrames(), "errorCategory", "timeout", "reason", "agent_stale_timeout", "idleFor", idleFor.String(), "threshold", StaleThreshold.String())
				writeMu.Lock()
				sendErrorResponse(conn, "timeout", "agent heartbeat timeout")
				writeMu.Unlock()
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
			metrics.IncTransportErrors(string(session.RoleAgent), "protocol")
			slog.Warn("agent packet rejected", "sessionId", s.ID, "role", session.RoleAgent, "frameId", 0, "packetType", 0, "queueDepth", 0, "framesDropped", s.DroppedFrames(), "errorCategory", "protocol", "reason", err.Error())
			sendErrorResponse(conn, "parse_error", err.Error())
			closeWithProtocolError(conn, websocket.CloseUnsupportedData, "invalid packet")
			return
		}

		s.TouchAgentLastSeen(time.Now())

		if header.PacketType == protocol.PacketTypeHeartbeat {
			writeMu.Lock()
			if err := sendHeartbeatResponse(conn, header.SequenceID); err != nil {
				writeMu.Unlock()
				metrics.IncTransportErrors(string(session.RoleAgent), "transport")
				slog.Warn("agent heartbeat echo failed", "sessionId", s.ID, "role", session.RoleAgent, "frameId", int64(header.SequenceID), "packetType", header.PacketType, "queueDepth", 0, "framesDropped", s.DroppedFrames(), "errorCategory", "transport", "error", err)
				return
			}
			writeMu.Unlock()
			continue
		}

		if header.PacketType != protocol.PacketTypeFrame {
			metrics.IncTransportErrors(string(session.RoleAgent), "protocol")
			slog.Warn("agent packet rejected", "sessionId", s.ID, "role", session.RoleAgent, "frameId", int64(header.SequenceID), "packetType", header.PacketType, "queueDepth", 0, "framesDropped", s.DroppedFrames(), "errorCategory", "protocol", "reason", "unsupported_agent_packet")
			sendErrorResponse(conn, "unsupported_agent_packet", "expected FRAME or HEARTBEAT packet")
			closeWithProtocolError(conn, websocket.CloseUnsupportedData, "unsupported packet type")
			return
		}

		s.AddReceivedFrames(1)
		metrics.IncFramesReceived(s.ID, 1)
		framesReceived++
		_ = s.SetAgentConnectionState(session.ConnectionStateStreaming, "agent frame stream active")
		slog.Info("agent frame received", "sessionId", s.ID, "role", session.RoleAgent, "frameId", int64(header.SequenceID), "packetType", header.PacketType, "queueDepth", 0, "framesDropped", s.DroppedFrames(), "frameBytes", len(packet), "framesReceived", framesReceived)

		if err := h.AgentFrameRouter(s, packet); err != nil {
			metrics.IncTransportErrors(string(session.RoleAgent), "transport")
			slog.Warn("agent frame routing failed", "sessionId", s.ID, "role", session.RoleAgent, "frameId", int64(header.SequenceID), "packetType", header.PacketType, "queueDepth", 0, "framesDropped", s.DroppedFrames(), "errorCategory", "transport", "error", err)
			closeWithProtocolError(conn, websocket.CloseInternalServerErr, "failed to route frame")
			return
		}
	}
}
