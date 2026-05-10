package transport

import (
	"log/slog"

	"github.com/gorilla/websocket"

	"streamforge/internal/server/session"
)

const viewerOutboundQueueSize = 32

func (h *WSHandler) handleViewerConnection(s *session.Session, conn *websocket.Conn) {
	viewerID := h.nextViewerID()
	outbound := make(chan []byte, viewerOutboundQueueSize)
	disconnect := make(chan struct{})

	if !s.AddViewer(viewerID, conn, outbound) {
		closeWithProtocolError(conn, websocket.CloseInternalServerErr, "failed to register viewer")
		return
	}

	go func() {
		defer close(disconnect)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
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

			if err := conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					slog.Warn("viewer write failed", "sessionId", s.ID, "viewerId", viewerID, "error", err)
				}
				return
			}
		}
	}
}
