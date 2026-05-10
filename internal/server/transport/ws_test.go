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

type unexpectedPacketTypeError struct {
	got  protocol.PacketType
	want protocol.PacketType
}

func (e *unexpectedPacketTypeError) Error() string {
	out, _ := json.Marshal(map[string]any{"got": e.got, "want": e.want})
	return "unexpected packet type: " + string(out)
}
