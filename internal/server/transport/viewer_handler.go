package transport

import (
	"errors"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"streamforge/internal/protocol"
	"streamforge/internal/server/auth"
	"streamforge/internal/server/metrics"
	"streamforge/internal/server/router"
	"streamforge/internal/server/session"
)

const viewerOutboundQueueSize = 32
const viewerAbuseDemotionThreshold = 5

func (h *WSHandler) handleViewerConnection(s *session.Session, conn *websocket.Conn) {
	viewerID := h.nextViewerID()
	outbound := make(chan []byte, viewerOutboundQueueSize)
	disconnect := make(chan struct{})
	var writeMu sync.Mutex
	writeProtocolCloseLocked := func(code int, reason, errorReason, errorDetail string) {
		writeMu.Lock()
		defer writeMu.Unlock()
		if errorReason != "" {
			sendErrorResponse(conn, errorReason, errorDetail)
		}
		closeWithProtocolError(conn, code, reason)
	}
	writeErrorOnlyLocked := func(errorReason, errorDetail string) {
		writeMu.Lock()
		defer writeMu.Unlock()
		sendErrorResponse(conn, errorReason, errorDetail)
	}

	if !s.AddViewer(viewerID, conn, outbound) {
		metrics.IncTransportErrors(string(session.RoleViewer), "internal")
		slog.Warn("viewer registration failed", "sessionId", s.ID, "role", session.RoleViewer, "frameId", 0, "packetType", protocol.PacketTypeAuth, "queueDepth", len(outbound), "framesDropped", s.DroppedFrames(), "errorCategory", "internal", "reason", "viewer_registration_failed")
		writeProtocolCloseLocked(websocket.CloseInternalServerErr, "failed to register viewer", "", "")
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
					writeProtocolCloseLocked(websocket.ClosePolicyViolation, "viewer heartbeat timeout", "timeout", "viewer heartbeat timeout")
				}
				return
			}

			if messageType != websocket.BinaryMessage {
				writeProtocolCloseLocked(websocket.CloseUnsupportedData, "viewer control messages must be binary", "", "")
				return
			}

			header, payload, err := protocol.ParsePacket(packet)
			if err != nil {
				metrics.IncTransportErrors(string(session.RoleViewer), "protocol")
				slog.Warn("viewer control packet rejected", "sessionId", s.ID, "role", session.RoleViewer, "viewerId", viewerID, "frameId", 0, "packetType", 0, "queueDepth", len(outbound), "framesDropped", s.DroppedFrames(), "errorCategory", "protocol", "reason", err.Error())
				writeProtocolCloseLocked(websocket.CloseUnsupportedData, "invalid control packet", "parse_error", err.Error())
				return
			}

			s.TouchViewerLastSeen(viewerID, time.Now())

			switch header.PacketType {
			case protocol.PacketTypeHeartbeat:
				writeMu.Lock()
				err = sendHeartbeatResponse(conn, header.SequenceID)
				writeMu.Unlock()
				if err != nil {
					metrics.IncTransportErrors(string(session.RoleViewer), "transport")
					slog.Warn("viewer heartbeat echo failed", "sessionId", s.ID, "role", session.RoleViewer, "viewerId", viewerID, "frameId", int64(header.SequenceID), "packetType", header.PacketType, "queueDepth", len(outbound), "framesDropped", s.DroppedFrames(), "errorCategory", "transport", "error", err)
					return
				}
			case protocol.PacketTypeInput:
				if !auth.CanSendInput(s, viewerID) {
					metrics.IncTransportErrors(string(session.RoleViewer), "auth")
					s.RecordInputDrop(viewerID, "control_permission_denied")
					metrics.IncInputDropped(s.ID, "control_permission_denied", 1)
					metrics.IncControlPermissionDenied(s.ID, "control_permission_denied", 1)
					slog.Warn("viewer input rejected", "sessionId", s.ID, "role", session.RoleViewer, "viewerId", viewerID, "frameId", int64(header.SequenceID), "packetType", header.PacketType, "queueDepth", len(outbound), "framesDropped", s.DroppedFrames(), "errorCategory", "auth", "reason", "control_permission_denied")
					writeErrorOnlyLocked("control_permission_denied", "viewer is not allowed to send input")
					continue
				}

				if err := auth.ValidateInputRate(s, viewerID, time.Now()); err != nil {
					metrics.IncTransportErrors(string(session.RoleViewer), "backpressure")
					s.RecordInputDrop(viewerID, "input_rate_limited")
					metrics.IncInputDropped(s.ID, "input_rate_limited", 1)
					abuseCount := s.RecordViewerAbuse(viewerID)
					slog.Warn("viewer input rejected", "sessionId", s.ID, "role", session.RoleViewer, "viewerId", viewerID, "frameId", int64(header.SequenceID), "packetType", header.PacketType, "queueDepth", len(outbound), "framesDropped", s.DroppedFrames(), "errorCategory", "backpressure", "reason", "input_rate_limited", "error", err)
					if abuseCount >= viewerAbuseDemotionThreshold {
						_ = s.SetViewerControlRole(viewerID, session.ViewerRoleViewOnly, "server-abuse-policy", time.Now())
						s.ClearViewerAbuse(viewerID)
						s.RecordInputDrop(viewerID, "control_revoked_abuse")
						metrics.IncInputDropped(s.ID, "control_revoked_abuse", 1)
						metrics.IncControlPermissionDenied(s.ID, "control_revoked_abuse", 1)
						writeErrorOnlyLocked("control_revoked_abuse", "control permission revoked due to repeated input rate violations")
						continue
					}
					writeErrorOnlyLocked("input_rate_limited", "viewer exceeded input rate limit")
					continue
				}
				s.ClearViewerAbuse(viewerID)

				inputEnvelope, err := protocol.DecodeInput(payload)
				if err != nil {
					metrics.IncTransportErrors(string(session.RoleViewer), "protocol")
					metrics.IncInputDropped(s.ID, "invalid_input_payload", 1)
					slog.Warn("viewer input rejected", "sessionId", s.ID, "role", session.RoleViewer, "viewerId", viewerID, "frameId", int64(header.SequenceID), "packetType", header.PacketType, "queueDepth", len(outbound), "framesDropped", s.DroppedFrames(), "errorCategory", "protocol", "reason", "invalid_input_payload", "error", err)
					writeProtocolCloseLocked(websocket.CloseUnsupportedData, "invalid input payload", "invalid_input_payload", err.Error())
					return
				}
				metrics.IncInputReceived(s.ID, viewerID, strconv.Itoa(int(inputEnvelope.EventType)), 1)

				if inputEnvelope.ViewerID != viewerID {
					metrics.IncTransportErrors(string(session.RoleViewer), "auth")
					s.RecordInputDrop(viewerID, "viewer_id_mismatch")
					metrics.IncInputDropped(s.ID, "viewer_id_mismatch", 1)
					metrics.IncControlPermissionDenied(s.ID, "viewer_id_mismatch", 1)
					slog.Warn("viewer input rejected", "sessionId", s.ID, "role", session.RoleViewer, "viewerId", viewerID, "frameId", int64(header.SequenceID), "packetType", header.PacketType, "queueDepth", len(outbound), "framesDropped", s.DroppedFrames(), "errorCategory", "auth", "reason", "viewer_id_mismatch", "payloadViewerId", inputEnvelope.ViewerID)
					writeProtocolCloseLocked(websocket.ClosePolicyViolation, "viewer input identity mismatch", "viewer_id_mismatch", "input viewerId does not match authenticated viewer")
					return
				}

				if h.ViewerInputRouter != nil {
					if err := h.ViewerInputRouter(s, packet); err != nil {
						if errors.Is(err, router.ErrInputBackpressure) {
							metrics.IncTransportErrors(string(session.RoleViewer), "backpressure")
							s.RecordInputDrop(viewerID, "input_queue_full")
							metrics.IncInputDropped(s.ID, "input_queue_full", 1)
							slog.Warn("viewer input dropped", "sessionId", s.ID, "role", session.RoleViewer, "viewerId", viewerID, "frameId", int64(header.SequenceID), "packetType", header.PacketType, "queueDepth", len(outbound), "framesDropped", s.DroppedFrames(), "errorCategory", "backpressure", "reason", "input_queue_full")
							continue
						}
						if errors.Is(err, router.ErrNoActiveAgent) {
							metrics.IncTransportErrors(string(session.RoleViewer), "transport")
							metrics.IncInputDropped(s.ID, "no_active_agent", 1)
							slog.Warn("viewer input rejected", "sessionId", s.ID, "role", session.RoleViewer, "viewerId", viewerID, "frameId", int64(header.SequenceID), "packetType", header.PacketType, "queueDepth", len(outbound), "framesDropped", s.DroppedFrames(), "errorCategory", "transport", "reason", "no_active_agent")
							writeProtocolCloseLocked(websocket.ClosePolicyViolation, "no active agent", "no_active_agent", "no active agent available for input routing")
							return
						}
						metrics.IncTransportErrors(string(session.RoleViewer), "transport")
						metrics.IncInputDropped(s.ID, "input_route_failed", 1)
						slog.Warn("viewer input routing failed", "sessionId", s.ID, "role", session.RoleViewer, "viewerId", viewerID, "frameId", int64(header.SequenceID), "packetType", header.PacketType, "queueDepth", len(outbound), "framesDropped", s.DroppedFrames(), "errorCategory", "transport", "reason", "input_route_failed", "error", err)
						writeProtocolCloseLocked(websocket.CloseInternalServerErr, "failed to route input", "input_route_failed", err.Error())
						return
					}
					metrics.IncInputForwarded(s.ID, strconv.Itoa(int(inputEnvelope.EventType)), 1)
				}
			default:
				metrics.IncTransportErrors(string(session.RoleViewer), "protocol")
				slog.Warn("viewer control packet rejected", "sessionId", s.ID, "role", session.RoleViewer, "viewerId", viewerID, "frameId", int64(header.SequenceID), "packetType", header.PacketType, "queueDepth", len(outbound), "framesDropped", s.DroppedFrames(), "errorCategory", "protocol", "reason", "unsupported_viewer_packet")
				writeProtocolCloseLocked(websocket.CloseUnsupportedData, "unsupported packet type", "unsupported_viewer_packet", "expected HEARTBEAT or INPUT packet")
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
