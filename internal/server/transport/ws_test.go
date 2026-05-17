package transport

import (
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"streamforge/internal/protocol"
	"streamforge/internal/server/auth"
	"streamforge/internal/server/session"
)

func TestReadBinaryHandshake_RejectsInvalidVersion(t *testing.T) {
	conn, cleanup := startHandshakeServer(t)
	defer cleanup()

	pkt := make([]byte, protocol.HeaderSize)
	pkt[protocol.VersionOffset] = protocol.ProtocolVersion + 1
	pkt[protocol.TypeOffset] = uint8(protocol.PacketTypeHello)
	pkt[protocol.ReservedOffset] = 0
	binary.BigEndian.PutUint32(pkt[protocol.PayloadLenOffset:protocol.PayloadLenOffset+4], 0)

	if err := conn.WriteMessage(websocket.BinaryMessage, pkt); err != nil {
		t.Fatalf("write invalid version packet: %v", err)
	}

	reason, detail := readErrorPacket(t, conn)
	if reason != "parse_error" {
		t.Fatalf("unexpected reason: got %q want %q", reason, "parse_error")
	}
	if !strings.Contains(detail, "unsupported protocol version") {
		t.Fatalf("unexpected detail: %q", detail)
	}
}

func TestReadBinaryHandshake_RejectsOversizedPayload(t *testing.T) {
	conn, cleanup := startHandshakeServer(t)
	defer cleanup()

	pkt := make([]byte, protocol.HeaderSize)
	pkt[protocol.VersionOffset] = protocol.ProtocolVersion
	pkt[protocol.TypeOffset] = uint8(protocol.PacketTypeHello)
	pkt[protocol.ReservedOffset] = 0
	binary.BigEndian.PutUint32(pkt[protocol.PayloadLenOffset:protocol.PayloadLenOffset+4], uint32(protocol.MaxPayloadBytes+1))

	if err := conn.WriteMessage(websocket.BinaryMessage, pkt); err != nil {
		t.Fatalf("write oversized payload packet: %v", err)
	}

	reason, detail := readErrorPacket(t, conn)
	if reason != "parse_error" {
		t.Fatalf("unexpected reason: got %q want %q", reason, "parse_error")
	}
	if !strings.Contains(detail, "payload too large") {
		t.Fatalf("unexpected detail: %q", detail)
	}
}

func startHandshakeServer(t *testing.T) (*websocket.Conn, func()) {
	t.Helper()

	upgrader := websocket.Upgrader{CheckOrigin: func(_ *http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		_, _, _ = readBinaryHandshake(c)
	}))

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		srv.Close()
		t.Fatalf("dial websocket: %v", err)
	}

	cleanup := func() {
		_ = conn.Close()
		srv.Close()
	}
	return conn, cleanup
}

func readErrorPacket(t *testing.T, conn *websocket.Conn) (string, string) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read response packet: %v", err)
	}

	h, payload, err := protocol.ParsePacket(msg)
	if err != nil {
		t.Fatalf("parse response packet: %v", err)
	}
	if h.PacketType != protocol.PacketTypeError {
		t.Fatalf("expected ERROR packet, got %d", h.PacketType)
	}
	ep, err := protocol.DecodeError(payload)
	if err != nil {
		t.Fatalf("decode ERROR payload: %v", err)
	}
	return ep.Reason, ep.Detail
}

func TestHandleSessionWS_RejectsDuplicateAgentJoinWithStructuredReason(t *testing.T) {
	registry := session.NewRegistry()
	s := registry.Create()
	h := NewWSHandler(registry)

	srv := httptest.NewServer(http.HandlerFunc(h.HandleSessionWS))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/session/" + s.ID

	primary, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial primary agent: %v", err)
	}
	defer primary.Close()

	if err := performHandshake(t, primary, "agent", s.AgentToken); err != nil {
		t.Fatalf("primary handshake failed: %v", err)
	}

	duplicate, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial duplicate agent: %v", err)
	}
	defer duplicate.Close()

	if err := performHandshake(t, duplicate, "agent", s.AgentToken); err != nil {
		t.Fatalf("duplicate handshake failed unexpectedly: %v", err)
	}

	reason, detail := readErrorPacket(t, duplicate)
	if reason != "duplicate_agent_join" {
		t.Fatalf("unexpected error reason: got %q want %q", reason, "duplicate_agent_join")
	}
	if !strings.Contains(detail, "agent already connected") {
		t.Fatalf("unexpected error detail: %q", detail)
	}
}

func TestHandleSessionWS_RejectsExpiredTokenAtAuth(t *testing.T) {
	registry := session.NewRegistry()
	s := registry.Create()
	s.TokenExpiresAt = time.Now().Add(-1 * time.Minute)

	h := NewWSHandler(registry)
	srv := httptest.NewServer(http.HandlerFunc(h.HandleSessionWS))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/session/" + s.ID
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()

	helloPayload, err := protocol.EncodeHello(protocol.HelloPayload{
		AgentID:          "agent",
		SupportedVersion: protocol.ProtocolVersion,
		CapabilityFlags:  0,
	})
	if err != nil {
		t.Fatalf("encode HELLO: %v", err)
	}

	helloHeader := protocol.Header{
		Version:     protocol.ProtocolVersion,
		PacketType:  protocol.PacketTypeHello,
		PayloadLen:  uint32(len(helloPayload)),
		Flags:       0,
		Reserved:    0,
		SequenceID:  0,
		TimestampNs: 0,
	}

	helloPacket, err := protocol.BuildPacket(helloHeader, helloPayload)
	if err != nil {
		t.Fatalf("build HELLO: %v", err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, helloPacket); err != nil {
		t.Fatalf("write HELLO: %v", err)
	}

	if _, _, err := readPacket(t, conn, protocol.PacketTypeAck); err != nil {
		t.Fatalf("read HELLO ACK: %v", err)
	}

	authPayload, err := protocol.EncodeAuth(protocol.AuthPayload{Role: "agent", Token: s.AgentToken})
	if err != nil {
		t.Fatalf("encode AUTH: %v", err)
	}
	authHeader := protocol.Header{
		Version:     protocol.ProtocolVersion,
		PacketType:  protocol.PacketTypeAuth,
		PayloadLen:  uint32(len(authPayload)),
		Flags:       0,
		Reserved:    0,
		SequenceID:  1,
		TimestampNs: 0,
	}
	authPacket, err := protocol.BuildPacket(authHeader, authPayload)
	if err != nil {
		t.Fatalf("build AUTH: %v", err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, authPacket); err != nil {
		t.Fatalf("write AUTH: %v", err)
	}

	reason, detail := readErrorPacket(t, conn)
	if reason != "token_expired" {
		t.Fatalf("unexpected error reason: got %q want %q", reason, "token_expired")
	}
	if !strings.Contains(detail, "expired") {
		t.Fatalf("unexpected error detail: %q", detail)
	}

	if got := s.State(); got != session.SessionStateExpired {
		t.Fatalf("session state after expired auth: got %q want %q", got, session.SessionStateExpired)
	}
}

func TestHandleSessionWS_EchoesViewerHeartbeat(t *testing.T) {
	registry := session.NewRegistry()
	s := registry.Create()
	h := NewWSHandler(registry)

	srv := httptest.NewServer(http.HandlerFunc(h.HandleSessionWS))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/session/" + s.ID
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()

	if err := performHandshake(t, conn, "viewer", s.ViewerToken); err != nil {
		t.Fatalf("viewer handshake failed: %v", err)
	}

	heartbeatHeader := protocol.Header{
		Version:     protocol.ProtocolVersion,
		PacketType:  protocol.PacketTypeHeartbeat,
		Flags:       0,
		Reserved:    0,
		SequenceID:  42,
		TimestampNs: uint64(time.Now().UnixNano()),
		PayloadLen:  0,
	}
	heartbeatPacket, err := protocol.BuildPacket(heartbeatHeader, nil)
	if err != nil {
		t.Fatalf("build heartbeat packet: %v", err)
	}

	if err := conn.WriteMessage(websocket.BinaryMessage, heartbeatPacket); err != nil {
		t.Fatalf("write heartbeat packet: %v", err)
	}

	header, _, err := readPacket(t, conn, protocol.PacketTypeHeartbeat)
	if err != nil {
		t.Fatalf("read heartbeat response: %v", err)
	}
	if header.SequenceID != 42 {
		t.Fatalf("heartbeat sequence mismatch: got %d want %d", header.SequenceID, 42)
	}
}

func TestHandleSessionWS_ForwardsAuthorizedViewerInput(t *testing.T) {
	registry := session.NewRegistry()
	s := registry.Create()
	h := NewWSHandler(registry)

	if ok := s.TryAttachAgent(&websocket.Conn{}); !ok {
		t.Fatalf("attach agent: expected success")
	}
	if ok := s.SetAgentConnectionState(session.ConnectionStateConnecting, "agent websocket accepted"); !ok {
		t.Fatalf("set agent connecting state: expected success")
	}
	if ok := s.SetAgentConnectionState(session.ConnectionStateAuthenticated, "agent auth complete"); !ok {
		t.Fatalf("set agent authenticated state: expected success")
	}

	routed := make(chan []byte, 1)
	h.ViewerInputRouter = func(_ *session.Session, packet []byte) error {
		routed <- append([]byte(nil), packet...)
		return nil
	}

	srv := httptest.NewServer(http.HandlerFunc(h.HandleSessionWS))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/session/" + s.ID
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()

	if err := performHandshake(t, conn, "viewer", s.ViewerToken); err != nil {
		t.Fatalf("viewer handshake failed: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for !s.HasViewer("viewer-1") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !s.HasViewer("viewer-1") {
		t.Fatalf("viewer-1 should be attached")
	}

	if ok := s.SetViewerControlRole("viewer-1", session.ViewerRoleControlEnabled, "owner-1", time.Now()); !ok {
		t.Fatalf("set viewer control role: expected success")
	}

	inputPayload, err := protocol.EncodeInput(protocol.InputEnvelope{
		EventType:   protocol.InputEventMouseMove,
		EventID:     1,
		TimestampNs: uint64(time.Now().UnixNano()),
		ViewerID:    "viewer-1",
		Mouse: &protocol.MousePayload{
			XNorm:       0.5,
			YNorm:       0.5,
			Button:      protocol.MouseButtonNone,
			ButtonsMask: 0,
		},
	})
	if err != nil {
		t.Fatalf("encode input payload: %v", err)
	}

	inputHeader := protocol.Header{
		Version:     protocol.ProtocolVersion,
		PacketType:  protocol.PacketTypeInput,
		Flags:       0,
		Reserved:    0,
		SequenceID:  77,
		TimestampNs: uint64(time.Now().UnixNano()),
		PayloadLen:  uint32(len(inputPayload)),
	}
	inputPacket, err := protocol.BuildPacket(inputHeader, inputPayload)
	if err != nil {
		t.Fatalf("build input packet: %v", err)
	}

	if err := conn.WriteMessage(websocket.BinaryMessage, inputPacket); err != nil {
		t.Fatalf("write input packet: %v", err)
	}

	select {
	case got := <-routed:
		if len(got) == 0 {
			t.Fatalf("routed packet should not be empty")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("expected authorized input to be routed")
	}
}

func TestHandleSessionWS_RejectsUnauthorizedViewerInput(t *testing.T) {
	registry := session.NewRegistry()
	s := registry.Create()
	h := NewWSHandler(registry)

	if ok := s.TryAttachAgent(&websocket.Conn{}); !ok {
		t.Fatalf("attach agent: expected success")
	}
	if ok := s.SetAgentConnectionState(session.ConnectionStateConnecting, "agent websocket accepted"); !ok {
		t.Fatalf("set agent connecting state: expected success")
	}
	if ok := s.SetAgentConnectionState(session.ConnectionStateAuthenticated, "agent auth complete"); !ok {
		t.Fatalf("set agent authenticated state: expected success")
	}

	srv := httptest.NewServer(http.HandlerFunc(h.HandleSessionWS))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/session/" + s.ID
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()

	if err := performHandshake(t, conn, "viewer", s.ViewerToken); err != nil {
		t.Fatalf("viewer handshake failed: %v", err)
	}

	inputPayload, err := protocol.EncodeInput(protocol.InputEnvelope{
		EventType:   protocol.InputEventMouseMove,
		EventID:     1,
		TimestampNs: uint64(time.Now().UnixNano()),
		ViewerID:    "viewer-1",
		Mouse: &protocol.MousePayload{
			XNorm:       0.5,
			YNorm:       0.5,
			Button:      protocol.MouseButtonNone,
			ButtonsMask: 0,
		},
	})
	if err != nil {
		t.Fatalf("encode input payload: %v", err)
	}

	inputHeader := protocol.Header{
		Version:     protocol.ProtocolVersion,
		PacketType:  protocol.PacketTypeInput,
		Flags:       0,
		Reserved:    0,
		SequenceID:  88,
		TimestampNs: uint64(time.Now().UnixNano()),
		PayloadLen:  uint32(len(inputPayload)),
	}
	inputPacket, err := protocol.BuildPacket(inputHeader, inputPayload)
	if err != nil {
		t.Fatalf("build input packet: %v", err)
	}

	if err := conn.WriteMessage(websocket.BinaryMessage, inputPacket); err != nil {
		t.Fatalf("write input packet: %v", err)
	}

	reason, detail := readErrorPacket(t, conn)
	if reason != "control_permission_denied" {
		t.Fatalf("unexpected error reason: got %q want %q", reason, "control_permission_denied")
	}
	if !strings.Contains(detail, "not allowed") {
		t.Fatalf("unexpected error detail: %q", detail)
	}
}

func TestHandleSessionWS_RejectsMalformedViewerInputPayload(t *testing.T) {
	registry := session.NewRegistry()
	s := registry.Create()
	h := NewWSHandler(registry)

	if ok := s.TryAttachAgent(&websocket.Conn{}); !ok {
		t.Fatalf("attach agent: expected success")
	}
	if ok := s.SetAgentConnectionState(session.ConnectionStateConnecting, "agent websocket accepted"); !ok {
		t.Fatalf("set agent connecting state: expected success")
	}
	if ok := s.SetAgentConnectionState(session.ConnectionStateAuthenticated, "agent auth complete"); !ok {
		t.Fatalf("set agent authenticated state: expected success")
	}

	srv := httptest.NewServer(http.HandlerFunc(h.HandleSessionWS))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/session/" + s.ID
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()

	if err := performHandshake(t, conn, "viewer", s.ViewerToken); err != nil {
		t.Fatalf("viewer handshake failed: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for !s.HasViewer("viewer-1") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !s.HasViewer("viewer-1") {
		t.Fatalf("viewer-1 should be attached")
	}

	if ok := s.SetViewerControlRole("viewer-1", session.ViewerRoleControlEnabled, "owner-1", time.Now()); !ok {
		t.Fatalf("set viewer control role: expected success")
	}

	badPayload := []byte("{not-json")
	inputHeader := protocol.Header{
		Version:     protocol.ProtocolVersion,
		PacketType:  protocol.PacketTypeInput,
		Flags:       0,
		Reserved:    0,
		SequenceID:  99,
		TimestampNs: uint64(time.Now().UnixNano()),
		PayloadLen:  uint32(len(badPayload)),
	}
	inputPacket, err := protocol.BuildPacket(inputHeader, badPayload)
	if err != nil {
		t.Fatalf("build input packet: %v", err)
	}

	if err := conn.WriteMessage(websocket.BinaryMessage, inputPacket); err != nil {
		t.Fatalf("write input packet: %v", err)
	}

	reason, _ := readErrorPacket(t, conn)
	if reason != "invalid_input_payload" {
		t.Fatalf("unexpected error reason: got %q want %q", reason, "invalid_input_payload")
	}
}

func TestHandleSessionWS_ForwardsViewerInputToActiveAgent(t *testing.T) {
	registry := session.NewRegistry()
	s := registry.Create()
	h := NewWSHandler(registry)

	srv := httptest.NewServer(http.HandlerFunc(h.HandleSessionWS))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/session/" + s.ID
	agentConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial agent websocket: %v", err)
	}
	defer agentConn.Close()

	if err := performHandshake(t, agentConn, "agent", s.AgentToken); err != nil {
		t.Fatalf("agent handshake failed: %v", err)
	}

	viewerConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial viewer websocket: %v", err)
	}
	defer viewerConn.Close()

	if err := performHandshake(t, viewerConn, "viewer", s.ViewerToken); err != nil {
		t.Fatalf("viewer handshake failed: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for !s.HasViewer("viewer-1") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !s.HasViewer("viewer-1") {
		t.Fatalf("viewer-1 should be attached")
	}

	if ok := s.SetViewerControlRole("viewer-1", session.ViewerRoleControlEnabled, "owner-1", time.Now()); !ok {
		t.Fatalf("set viewer control role: expected success")
	}

	inputPayload, err := protocol.EncodeInput(protocol.InputEnvelope{
		EventType:   protocol.InputEventMouseMove,
		EventID:     1,
		TimestampNs: uint64(time.Now().UnixNano()),
		ViewerID:    "viewer-1",
		Mouse: &protocol.MousePayload{
			XNorm:       0.5,
			YNorm:       0.5,
			Button:      protocol.MouseButtonNone,
			ButtonsMask: 0,
		},
	})
	if err != nil {
		t.Fatalf("encode input payload: %v", err)
	}

	inputHeader := protocol.Header{
		Version:     protocol.ProtocolVersion,
		PacketType:  protocol.PacketTypeInput,
		Flags:       0,
		Reserved:    0,
		SequenceID:  123,
		TimestampNs: uint64(time.Now().UnixNano()),
		PayloadLen:  uint32(len(inputPayload)),
	}
	inputPacket, err := protocol.BuildPacket(inputHeader, inputPayload)
	if err != nil {
		t.Fatalf("build input packet: %v", err)
	}

	if err := viewerConn.WriteMessage(websocket.BinaryMessage, inputPacket); err != nil {
		t.Fatalf("write viewer input packet: %v", err)
	}

	gotHeader, gotPayload, err := readPacket(t, agentConn, protocol.PacketTypeInput)
	if err != nil {
		t.Fatalf("read forwarded input packet at agent: %v", err)
	}
	if gotHeader.SequenceID != inputHeader.SequenceID {
		t.Fatalf("forwarded input sequence mismatch: got %d want %d", gotHeader.SequenceID, inputHeader.SequenceID)
	}

	gotEnvelope, err := protocol.DecodeInput(gotPayload)
	if err != nil {
		t.Fatalf("decode forwarded input payload: %v", err)
	}
	if gotEnvelope.ViewerID != "viewer-1" {
		t.Fatalf("forwarded input viewerId: got %q want %q", gotEnvelope.ViewerID, "viewer-1")
	}
}

func TestHandleSessionWS_DropsRateLimitedInputWithoutDisconnect(t *testing.T) {
	restore := auth.SetInputRateLimitsForTesting(1, 1)
	defer restore()

	registry := session.NewRegistry()
	s := registry.Create()
	h := NewWSHandler(registry)
	h.ViewerInputRouter = func(_ *session.Session, _ []byte) error { return nil }

	if ok := s.TryAttachAgent(&websocket.Conn{}); !ok {
		t.Fatalf("attach agent: expected success")
	}
	if ok := s.SetAgentConnectionState(session.ConnectionStateConnecting, "agent websocket accepted"); !ok {
		t.Fatalf("set agent connecting state: expected success")
	}
	if ok := s.SetAgentConnectionState(session.ConnectionStateAuthenticated, "agent auth complete"); !ok {
		t.Fatalf("set agent authenticated state: expected success")
	}

	srv := httptest.NewServer(http.HandlerFunc(h.HandleSessionWS))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/session/" + s.ID
	viewerConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial viewer websocket: %v", err)
	}
	defer viewerConn.Close()

	if err := performHandshake(t, viewerConn, "viewer", s.ViewerToken); err != nil {
		t.Fatalf("viewer handshake failed: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for !s.HasViewer("viewer-1") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !s.HasViewer("viewer-1") {
		t.Fatalf("viewer-1 should be attached")
	}

	if ok := s.SetViewerControlRole("viewer-1", session.ViewerRoleControlEnabled, "owner-1", time.Now()); !ok {
		t.Fatalf("set viewer control role: expected success")
	}

	allowedPacket := mustBuildInputPacket(t, 1, 101, "viewer-1")
	if err := viewerConn.WriteMessage(websocket.BinaryMessage, allowedPacket); err != nil {
		t.Fatalf("write first input packet: %v", err)
	}

	rateLimitedPacket := mustBuildInputPacket(t, 2, 102, "viewer-1")
	if err := viewerConn.WriteMessage(websocket.BinaryMessage, rateLimitedPacket); err != nil {
		t.Fatalf("write rate-limited input packet: %v", err)
	}

	reason, _ := readErrorPacket(t, viewerConn)
	if reason != "input_rate_limited" {
		t.Fatalf("unexpected rate-limit reason: got %q want %q", reason, "input_rate_limited")
	}

	heartbeatHeader := protocol.Header{
		Version:     protocol.ProtocolVersion,
		PacketType:  protocol.PacketTypeHeartbeat,
		SequenceID:  500,
		TimestampNs: uint64(time.Now().UnixNano()),
		PayloadLen:  0,
	}
	heartbeatPacket, err := protocol.BuildPacket(heartbeatHeader, nil)
	if err != nil {
		t.Fatalf("build heartbeat packet: %v", err)
	}

	if err := viewerConn.WriteMessage(websocket.BinaryMessage, heartbeatPacket); err != nil {
		t.Fatalf("write heartbeat packet after rate-limit: %v", err)
	}

	if _, _, err := readPacket(t, viewerConn, protocol.PacketTypeHeartbeat); err != nil {
		t.Fatalf("viewer should stay connected after rate-limit drop: %v", err)
	}
}

func TestHandleSessionWS_DemotesViewerAfterRepeatedRateLimitAbuse(t *testing.T) {
	restore := auth.SetInputRateLimitsForTesting(1, 1)
	defer restore()

	registry := session.NewRegistry()
	s := registry.Create()
	h := NewWSHandler(registry)
	h.ViewerInputRouter = func(_ *session.Session, _ []byte) error { return nil }

	if ok := s.TryAttachAgent(&websocket.Conn{}); !ok {
		t.Fatalf("attach agent: expected success")
	}
	if ok := s.SetAgentConnectionState(session.ConnectionStateConnecting, "agent websocket accepted"); !ok {
		t.Fatalf("set agent connecting state: expected success")
	}
	if ok := s.SetAgentConnectionState(session.ConnectionStateAuthenticated, "agent auth complete"); !ok {
		t.Fatalf("set agent authenticated state: expected success")
	}

	srv := httptest.NewServer(http.HandlerFunc(h.HandleSessionWS))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/session/" + s.ID
	viewerConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial viewer websocket: %v", err)
	}
	defer viewerConn.Close()

	if err := performHandshake(t, viewerConn, "viewer", s.ViewerToken); err != nil {
		t.Fatalf("viewer handshake failed: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for !s.HasViewer("viewer-1") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !s.HasViewer("viewer-1") {
		t.Fatalf("viewer-1 should be attached")
	}

	if ok := s.SetViewerControlRole("viewer-1", session.ViewerRoleControlEnabled, "owner-1", time.Now()); !ok {
		t.Fatalf("set viewer control role: expected success")
	}

	firstAllowed := mustBuildInputPacket(t, 1, 201, "viewer-1")
	if err := viewerConn.WriteMessage(websocket.BinaryMessage, firstAllowed); err != nil {
		t.Fatalf("write first input packet: %v", err)
	}

	for i := 0; i < viewerAbuseDemotionThreshold; i++ {
		packet := mustBuildInputPacket(t, uint64(i+2), uint32(202+i), "viewer-1")
		if err := viewerConn.WriteMessage(websocket.BinaryMessage, packet); err != nil {
			t.Fatalf("write abusive input packet %d: %v", i+1, err)
		}

		reason, _ := readErrorPacket(t, viewerConn)
		if i < viewerAbuseDemotionThreshold-1 && reason != "input_rate_limited" {
			t.Fatalf("abuse strike %d reason: got %q want %q", i+1, reason, "input_rate_limited")
		}
		if i == viewerAbuseDemotionThreshold-1 && reason != "control_revoked_abuse" {
			t.Fatalf("final abuse reason: got %q want %q", reason, "control_revoked_abuse")
		}
	}

	postDemotionPacket := mustBuildInputPacket(t, 100, 999, "viewer-1")
	if err := viewerConn.WriteMessage(websocket.BinaryMessage, postDemotionPacket); err != nil {
		t.Fatalf("write post-demotion input packet: %v", err)
	}

	reason, _ := readErrorPacket(t, viewerConn)
	if reason != "control_permission_denied" {
		t.Fatalf("post-demotion reason: got %q want %q", reason, "control_permission_denied")
	}
}

func TestHandleSessionWS_DisconnectsStaleAgentAndAllowsReconnect(t *testing.T) {
	previous := StaleThreshold
	StaleThreshold = 200 * time.Millisecond
	defer func() { StaleThreshold = previous }()

	registry := session.NewRegistry()
	s := registry.Create()
	h := NewWSHandler(registry)

	srv := httptest.NewServer(http.HandlerFunc(h.HandleSessionWS))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/session/" + s.ID
	first, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial first agent: %v", err)
	}
	defer first.Close()

	if err := performHandshake(t, first, "agent", s.AgentToken); err != nil {
		t.Fatalf("first agent handshake failed: %v", err)
	}

	reason, detail := readErrorPacket(t, first)
	if reason != "timeout" {
		t.Fatalf("unexpected timeout reason: got %q want %q", reason, "timeout")
	}
	if !strings.Contains(detail, "heartbeat timeout") {
		t.Fatalf("unexpected timeout detail: %q", detail)
	}

	second, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial second agent: %v", err)
	}
	defer second.Close()

	if err := performHandshake(t, second, "agent", s.AgentToken); err != nil {
		t.Fatalf("second agent handshake should recover after stale disconnect: %v", err)
	}
}

func TestHandleSessionWS_RejectsDuplicateAgentJoinConsistently(t *testing.T) {
	registry := session.NewRegistry()
	s := registry.Create()
	h := NewWSHandler(registry)

	srv := httptest.NewServer(http.HandlerFunc(h.HandleSessionWS))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/session/" + s.ID
	primary, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial primary agent: %v", err)
	}
	defer primary.Close()

	if err := performHandshake(t, primary, "agent", s.AgentToken); err != nil {
		t.Fatalf("primary handshake failed: %v", err)
	}

	for i := 0; i < 3; i++ {
		duplicate, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			t.Fatalf("dial duplicate agent %d: %v", i+1, err)
		}

		if err := performHandshake(t, duplicate, "agent", s.AgentToken); err != nil {
			_ = duplicate.Close()
			t.Fatalf("duplicate handshake %d failed unexpectedly: %v", i+1, err)
		}

		reason, detail := readErrorPacket(t, duplicate)
		_ = duplicate.Close()

		if reason != "duplicate_agent_join" {
			t.Fatalf("duplicate %d reason: got %q want %q", i+1, reason, "duplicate_agent_join")
		}
		if !strings.Contains(detail, "agent already connected") {
			t.Fatalf("duplicate %d detail: %q", i+1, detail)
		}
	}
}

func performHandshake(t *testing.T, conn *websocket.Conn, role, token string) error {
	t.Helper()

	helloPayload, err := protocol.EncodeHello(protocol.HelloPayload{
		AgentID:          role,
		SupportedVersion: protocol.ProtocolVersion,
		CapabilityFlags:  0,
	})
	if err != nil {
		return err
	}

	helloHeader := protocol.Header{
		Version:     protocol.ProtocolVersion,
		PacketType:  protocol.PacketTypeHello,
		Flags:       0,
		Reserved:    0,
		SequenceID:  0,
		TimestampNs: 0,
		PayloadLen:  uint32(len(helloPayload)),
	}

	helloPacket, err := protocol.BuildPacket(helloHeader, helloPayload)
	if err != nil {
		return err
	}

	if err := conn.WriteMessage(websocket.BinaryMessage, helloPacket); err != nil {
		return err
	}

	if _, _, err := readPacket(t, conn, protocol.PacketTypeAck); err != nil {
		return err
	}

	authPayload, err := protocol.EncodeAuth(protocol.AuthPayload{Role: role, Token: token})
	if err != nil {
		return err
	}

	authHeader := protocol.Header{
		Version:     protocol.ProtocolVersion,
		PacketType:  protocol.PacketTypeAuth,
		Flags:       0,
		Reserved:    0,
		SequenceID:  1,
		TimestampNs: 0,
		PayloadLen:  uint32(len(authPayload)),
	}

	authPacket, err := protocol.BuildPacket(authHeader, authPayload)
	if err != nil {
		return err
	}

	if err := conn.WriteMessage(websocket.BinaryMessage, authPacket); err != nil {
		return err
	}

	if _, _, err := readPacket(t, conn, protocol.PacketTypeAck); err != nil {
		return err
	}

	return nil
}

func readPacket(t *testing.T, conn *websocket.Conn, packetType protocol.PacketType) (protocol.Header, []byte, error) {
	t.Helper()

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		return protocol.Header{}, nil, err
	}

	h, payload, err := protocol.ParsePacket(msg)
	if err != nil {
		return protocol.Header{}, nil, err
	}
	if h.PacketType != packetType {
		return protocol.Header{}, nil, &unexpectedPacketTypeError{got: h.PacketType, want: packetType}
	}

	return h, payload, nil
}

func mustBuildInputPacket(t *testing.T, eventID uint64, sequenceID uint32, viewerID string) []byte {
	t.Helper()

	payload, err := protocol.EncodeInput(protocol.InputEnvelope{
		EventType:   protocol.InputEventMouseMove,
		EventID:     eventID,
		TimestampNs: uint64(time.Now().UnixNano()),
		ViewerID:    viewerID,
		Mouse: &protocol.MousePayload{
			XNorm:       0.5,
			YNorm:       0.5,
			Button:      protocol.MouseButtonNone,
			ButtonsMask: 0,
		},
	})
	if err != nil {
		t.Fatalf("encode input payload: %v", err)
	}

	header := protocol.Header{
		Version:     protocol.ProtocolVersion,
		PacketType:  protocol.PacketTypeInput,
		Flags:       0,
		Reserved:    0,
		SequenceID:  sequenceID,
		TimestampNs: uint64(time.Now().UnixNano()),
		PayloadLen:  uint32(len(payload)),
	}

	packet, err := protocol.BuildPacket(header, payload)
	if err != nil {
		t.Fatalf("build input packet: %v", err)
	}

	return packet
}

type unexpectedPacketTypeError struct {
	got  protocol.PacketType
	want protocol.PacketType
}

func (e *unexpectedPacketTypeError) Error() string {
	out, _ := json.Marshal(map[string]any{"got": e.got, "want": e.want})
	return "unexpected packet type: " + string(out)
}
