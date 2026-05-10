package transport

import (
	"log/slog"

	"github.com/gorilla/websocket"

	"streamforge/internal/server/session"
)

func (h *WSHandler) handleAgentConnection(s *session.Session, conn *websocket.Conn) {
	if !s.TryAttachAgent(conn) {
		closeWithProtocolError(conn, websocket.ClosePolicyViolation, "agent already connected")
		return
	}

	slog.Info("agent connected", "sessionId", s.ID)
	framesReceived := 0
	defer func() {
		s.DetachAgent(conn)
		slog.Info("agent disconnected", "sessionId", s.ID)
		_ = conn.Close()
	}()

	for {
		messageType, frame, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				slog.Warn("agent read failed", "sessionId", s.ID, "error", err)
			}
			return
		}

		if messageType != websocket.BinaryMessage {
			closeWithProtocolError(conn, websocket.CloseUnsupportedData, "agent frames must be binary")
			return
		}

		s.AddReceivedFrames(1)
		framesReceived++
		slog.Info("agent frame received", "sessionId", s.ID, "frameBytes", len(frame), "framesReceived", framesReceived)

		if err := h.AgentFrameRouter(s, frame); err != nil {
			slog.Warn("agent frame routing failed", "sessionId", s.ID, "error", err)
			closeWithProtocolError(conn, websocket.CloseInternalServerErr, "failed to route frame")
			return
		}
	}
}
