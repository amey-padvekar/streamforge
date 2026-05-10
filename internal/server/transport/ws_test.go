package transport

import (
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"streamforge/internal/protocol"
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
