package transport

import (
	"errors"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"streamforge/internal/protocol"
	"streamforge/internal/server/session"
)

const viewerOutboundQueueSize = 32

func (h *WSHandler) handleViewerConnection(s *session.Session, conn *websocket.Conn) {
	viewerID := h.nextViewerID()
	outbound := make(chan []byte, viewerOutboundQueueSize)
	disconnect := make(chan struct{})
	var writeMu sync.Mutex

	if !s.AddViewer(viewerID, conn, outbound) {
		closeWithProtocolError(conn, websocket.CloseInternalServerErr, "failed to register viewer")
		return
	}
	_ = s.SetViewerConnectionState(viewerID, session.ConnectionStateAuthenticated, "viewer auth handshake complete")
	s.TouchViewerLastSeen(viewerID, time.Now())

	go func() {
		defer close(disconnect)
		for {
			_ = conn.SetReadDeadline(time.Now().Add(StaleThreshold))
			messageType, packet, err := conn.ReadMessage()
			if err != nil {
				var netErr net.Error
				if errors.As(err, &netErr) && netErr.Timeout() {
					idleFor := s.ViewerIdleDuration(viewerID, time.Now())
					_ = s.SetViewerConnectionState(viewerID, session.ConnectionStateStale, "viewer heartbeat timeout")
					slog.Warn("viewer stale timeout", "sessionId", s.ID, "viewerId", viewerID, "errorCategory", "timeout", "reason", "viewer_stale_timeout", "idleFor", idleFor.String(), "threshold", StaleThreshold.String())
					sendErrorResponse(conn, "timeout", "viewer heartbeat timeout")
					closeWithProtocolError(conn, websocket.ClosePolicyViolation, "viewer heartbeat timeout")
				}
				return
			}

			if messageType != websocket.BinaryMessage {
				closeWithProtocolError(conn, websocket.CloseUnsupportedData, "viewer control messages must be binary")
				return
			}

			header, _, err := protocol.ParsePacket(packet)
			if err != nil {
				slog.Warn("viewer control packet rejected", "sessionId", s.ID, "viewerId", viewerID, "errorCategory", "protocol", "reason", err.Error())
				sendErrorResponse(conn, "parse_error", err.Error())
				closeWithProtocolError(conn, websocket.CloseUnsupportedData, "invalid control packet")
				return
			}

			s.TouchViewerLastSeen(viewerID, time.Now())

			if header.PacketType != protocol.PacketTypeHeartbeat {
				slog.Warn("viewer control packet rejected", "sessionId", s.ID, "viewerId", viewerID, "errorCategory", "protocol", "reason", "unsupported_viewer_packet", "packetType", header.PacketType)
				sendErrorResponse(conn, "unsupported_viewer_packet", "expected HEARTBEAT packet")
				closeWithProtocolError(conn, websocket.CloseUnsupportedData, "unsupported packet type")
				return
			}

			writeMu.Lock()
			err = sendHeartbeatResponse(conn, header.SequenceID)
			writeMu.Unlock()
			if err != nil {
				slog.Warn("viewer heartbeat echo failed", "sessionId", s.ID, "viewerId", viewerID, "errorCategory", "transport", "error", err)
				return
			}
		}
	}()

	slog.Info("viewer connected", "sessionId", s.ID, "viewerId", viewerID)
	defer func() {
		s.RemoveViewer(viewerID)
		for range outbound {
		}
		slog.Info("viewer disconnected", "sessionId", s.ID, "viewerId", viewerID)
		_ = conn.Close()
	}()

	for {
		select {
		case <-disconnect:
			return
		case frame, ok := <-outbound:
			if !ok {
				return
			}

			_ = s.SetViewerConnectionState(viewerID, session.ConnectionStateStreaming, "viewer frame stream active")

			writeMu.Lock()
			if err := conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
				writeMu.Unlock()
				if websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					slog.Warn("viewer write failed", "sessionId", s.ID, "viewerId", viewerID, "error", err)
				}
				return
			}
			writeMu.Unlock()
		}
	}
}
