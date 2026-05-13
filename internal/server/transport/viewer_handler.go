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

const viewerOutboundQueueSize = 32

func (h *WSHandler) handleViewerConnection(s *session.Session, conn *websocket.Conn) {
	viewerID := h.nextViewerID()
	outbound := make(chan []byte, viewerOutboundQueueSize)
	disconnect := make(chan struct{})
	var writeMu sync.Mutex

	if !s.AddViewer(viewerID, conn, outbound) {
		metrics.IncTransportErrors(string(session.RoleViewer), "internal")
		slog.Warn("viewer registration failed", "sessionId", s.ID, "role", session.RoleViewer, "frameId", 0, "packetType", protocol.PacketTypeAuth, "queueDepth", len(outbound), "framesDropped", s.DroppedFrames(), "errorCategory", "internal", "reason", "viewer_registration_failed")
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
					metrics.IncTransportErrors(string(session.RoleViewer), "timeout")
					idleFor := s.ViewerIdleDuration(viewerID, time.Now())
					_ = s.SetViewerConnectionState(viewerID, session.ConnectionStateStale, "viewer heartbeat timeout")
					slog.Warn("viewer stale timeout", "sessionId", s.ID, "role", session.RoleViewer, "viewerId", viewerID, "frameId", 0, "packetType", protocol.PacketTypeHeartbeat, "queueDepth", len(outbound), "framesDropped", s.DroppedFrames(), "errorCategory", "timeout", "reason", "viewer_stale_timeout", "idleFor", idleFor.String(), "threshold", StaleThreshold.String())
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
				metrics.IncTransportErrors(string(session.RoleViewer), "protocol")
				slog.Warn("viewer control packet rejected", "sessionId", s.ID, "role", session.RoleViewer, "viewerId", viewerID, "frameId", 0, "packetType", 0, "queueDepth", len(outbound), "framesDropped", s.DroppedFrames(), "errorCategory", "protocol", "reason", err.Error())
				sendErrorResponse(conn, "parse_error", err.Error())
				closeWithProtocolError(conn, websocket.CloseUnsupportedData, "invalid control packet")
				return
			}

			s.TouchViewerLastSeen(viewerID, time.Now())

			if header.PacketType != protocol.PacketTypeHeartbeat {
				metrics.IncTransportErrors(string(session.RoleViewer), "protocol")
				slog.Warn("viewer control packet rejected", "sessionId", s.ID, "role", session.RoleViewer, "viewerId", viewerID, "frameId", int64(header.SequenceID), "packetType", header.PacketType, "queueDepth", len(outbound), "framesDropped", s.DroppedFrames(), "errorCategory", "protocol", "reason", "unsupported_viewer_packet")
				sendErrorResponse(conn, "unsupported_viewer_packet", "expected HEARTBEAT packet")
				closeWithProtocolError(conn, websocket.CloseUnsupportedData, "unsupported packet type")
				return
			}

			writeMu.Lock()
			err = sendHeartbeatResponse(conn, header.SequenceID)
			writeMu.Unlock()
			if err != nil {
				metrics.IncTransportErrors(string(session.RoleViewer), "transport")
				slog.Warn("viewer heartbeat echo failed", "sessionId", s.ID, "role", session.RoleViewer, "viewerId", viewerID, "frameId", int64(header.SequenceID), "packetType", header.PacketType, "queueDepth", len(outbound), "framesDropped", s.DroppedFrames(), "errorCategory", "transport", "error", err)
				return
			}
		}
	}()

	slog.Info("viewer connected", "sessionId", s.ID, "role", session.RoleViewer, "viewerId", viewerID, "queueDepth", len(outbound), "framesDropped", s.DroppedFrames())
	defer func() {
		s.RemoveViewer(viewerID)
		for range outbound {
		}
		slog.Info("viewer disconnected", "sessionId", s.ID, "role", session.RoleViewer, "viewerId", viewerID, "queueDepth", 0, "framesDropped", s.DroppedFrames())
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
				metrics.IncTransportErrors(string(session.RoleViewer), "transport")
				if websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					slog.Warn("viewer write failed", "sessionId", s.ID, "role", session.RoleViewer, "viewerId", viewerID, "frameId", 0, "packetType", protocol.PacketTypeFrame, "queueDepth", len(outbound), "framesDropped", s.DroppedFrames(), "errorCategory", "transport", "error", err)
				}
				return
			}
			writeMu.Unlock()
		}
	}
}
