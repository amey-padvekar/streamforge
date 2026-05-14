package integration

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"streamforge/internal/protocol"
	"streamforge/internal/server/metrics"
	"streamforge/internal/server/router"
	"streamforge/internal/server/session"
	"streamforge/internal/server/transport"
)

type integrationHarness struct {
	registry *session.Registry
	server   *httptest.Server
}

type createSessionResponse struct {
	SessionID   string `json:"sessionId"`
	AgentToken  string `json:"agentToken"`
	ViewerToken string `json:"viewerToken"`
	WSURL       string `json:"wsUrl"`
}

func newIntegrationHarness(t *testing.T) *integrationHarness {
	t.Helper()

	registry := session.NewRegistry()
	metrics.SetDefault(metrics.NewPrometheus())

	sessionHandler := router.NewSessionHandler(registry)
	wsHandler := transport.NewWSHandler(registry)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/sessions", sessionHandler.HandleSessions)
	mux.HandleFunc("/api/sessions/", sessionHandler.HandleSessionMetrics)
	mux.HandleFunc("/ws/session/", wsHandler.HandleSessionWS)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return &integrationHarness{
		registry: registry,
		server:   srv,
	}
}

func (h *integrationHarness) wsBaseURL() string {
	return "ws" + strings.TrimPrefix(h.server.URL, "http")
}

func (h *integrationHarness) createSession(t *testing.T) createSessionResponse {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost, h.server.URL+"/api/sessions", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create session request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("create session status: got %d want %d body=%s", resp.StatusCode, http.StatusCreated, string(body))
	}

	var out createSessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode create session response: %v", err)
	}
	if out.SessionID == "" || out.AgentToken == "" || out.ViewerToken == "" {
		t.Fatalf("create session returned empty fields: %+v", out)
	}

	return out
}

func dialWS(t *testing.T, wsURL string) *websocket.Conn {
	t.Helper()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket %s: %v", wsURL, err)
	}

	return conn
}

func performHandshake(t *testing.T, conn *websocket.Conn, role, token string) {
	t.Helper()

	helloPayload, err := protocol.EncodeHello(protocol.HelloPayload{
		AgentID:          role,
		SupportedVersion: protocol.ProtocolVersion,
		CapabilityFlags:  0,
	})
	if err != nil {
		t.Fatalf("encode HELLO: %v", err)
	}

	helloPacket := buildPacket(t, protocol.PacketTypeHello, 0, helloPayload)
	if err := conn.WriteMessage(websocket.BinaryMessage, helloPacket); err != nil {
		t.Fatalf("write HELLO: %v", err)
	}

	h, _, err := readPacket(conn, 2*time.Second)
	if err != nil {
		t.Fatalf("read HELLO ACK: %v", err)
	}
	if h.PacketType != protocol.PacketTypeAck {
		t.Fatalf("HELLO response packet type: got %d want %d", h.PacketType, protocol.PacketTypeAck)
	}

	authPayload, err := protocol.EncodeAuth(protocol.AuthPayload{Role: role, Token: token})
	if err != nil {
		t.Fatalf("encode AUTH: %v", err)
	}

	authPacket := buildPacket(t, protocol.PacketTypeAuth, 1, authPayload)
	if err := conn.WriteMessage(websocket.BinaryMessage, authPacket); err != nil {
		t.Fatalf("write AUTH: %v", err)
	}

	h, payload, err := readPacket(conn, 2*time.Second)
	if err != nil {
		t.Fatalf("read AUTH response: %v", err)
	}
	if h.PacketType == protocol.PacketTypeError {
		ep, decErr := protocol.DecodeError(payload)
		if decErr != nil {
			t.Fatalf("decode AUTH ERROR payload: %v", decErr)
		}
		t.Fatalf("AUTH rejected: reason=%s detail=%s", ep.Reason, ep.Detail)
	}
	if h.PacketType != protocol.PacketTypeAck {
		t.Fatalf("AUTH response packet type: got %d want %d", h.PacketType, protocol.PacketTypeAck)
	}
}

func buildPacket(t *testing.T, packetType protocol.PacketType, seq uint32, payload []byte) []byte {
	t.Helper()

	header := protocol.Header{
		Version:     protocol.ProtocolVersion,
		PacketType:  packetType,
		Flags:       0,
		Reserved:    0,
		SequenceID:  seq,
		TimestampNs: uint64(time.Now().UnixNano()),
		PayloadLen:  uint32(len(payload)),
	}

	packet, err := protocol.BuildPacket(header, payload)
	if err != nil {
		t.Fatalf("build packet type=%d seq=%d: %v", packetType, seq, err)
	}

	return packet
}

func readPacket(conn *websocket.Conn, timeout time.Duration) (protocol.Header, []byte, error) {
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		return protocol.Header{}, nil, err
	}

	return protocol.ParsePacket(msg)
}

func waitFor(t *testing.T, timeout time.Duration, check func() bool, failureMsg string) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatal(failureMsg)
}

func TestAgentViewerFlow_ForwardsFramesAndTracksMetrics(t *testing.T) {
	h := newIntegrationHarness(t)
	created := h.createSession(t)
	wsURL := h.wsBaseURL() + "/ws/session/" + created.SessionID

	agent := dialWS(t, wsURL)
	defer agent.Close()
	performHandshake(t, agent, "agent", created.AgentToken)

	viewerA := dialWS(t, wsURL)
	defer viewerA.Close()
	performHandshake(t, viewerA, "viewer", created.ViewerToken)

	viewerB := dialWS(t, wsURL)
	defer viewerB.Close()
	performHandshake(t, viewerB, "viewer", created.ViewerToken)

	const frameCount = 5
	for i := 1; i <= frameCount; i++ {
		payload := []byte{byte(i), 0xAB, 0xCD}
		packet := buildPacket(t, protocol.PacketTypeFrame, uint32(i), payload)
		if err := agent.WriteMessage(websocket.BinaryMessage, packet); err != nil {
			t.Fatalf("write frame %d from agent: %v", i, err)
		}
		time.Sleep(15 * time.Millisecond)
	}

	for i := 1; i <= frameCount; i++ {
		hdr, _, err := readPacket(viewerA, 2*time.Second)
		if err != nil {
			t.Fatalf("viewer A read frame %d: %v", i, err)
		}
		if hdr.PacketType != protocol.PacketTypeFrame {
			t.Fatalf("viewer A packet type for frame %d: got %d want %d", i, hdr.PacketType, protocol.PacketTypeFrame)
		}

		hdr, _, err = readPacket(viewerB, 2*time.Second)
		if err != nil {
			t.Fatalf("viewer B read frame %d: %v", i, err)
		}
		if hdr.PacketType != protocol.PacketTypeFrame {
			t.Fatalf("viewer B packet type for frame %d: got %d want %d", i, hdr.PacketType, protocol.PacketTypeFrame)
		}
	}

	s, ok := h.registry.Get(created.SessionID)
	if !ok {
		t.Fatalf("session %s not found in registry", created.SessionID)
	}

	waitFor(t, 2*time.Second, func() bool {
		m := s.MetricsSnapshot()
		return m.FramesReceived == frameCount && m.FramesForwarded == frameCount*2
	}, "expected framesReceived and framesForwarded counters to match delivered frames")

	m := s.MetricsSnapshot()
	if m.FramesDropped != 0 {
		t.Fatalf("frames dropped: got %d want 0", m.FramesDropped)
	}
}

func TestAgentViewerFlow_DropCountersAndReconnectHandling(t *testing.T) {
	h := newIntegrationHarness(t)
	created := h.createSession(t)
	wsURL := h.wsBaseURL() + "/ws/session/" + created.SessionID

	agent := dialWS(t, wsURL)
	performHandshake(t, agent, "agent", created.AgentToken)
	defer agent.Close()

	fastViewer := dialWS(t, wsURL)
	performHandshake(t, fastViewer, "viewer", created.ViewerToken)
	defer fastViewer.Close()

	slowViewer := dialWS(t, wsURL)
	performHandshake(t, slowViewer, "viewer", created.ViewerToken)
	defer slowViewer.Close()

	stopFastDrain := make(chan struct{})
	go func() {
		for {
			select {
			case <-stopFastDrain:
				return
			default:
			}
			_, _, err := readPacket(fastViewer, 200*time.Millisecond)
			if err != nil {
				var netErr net.Error
				if errors.As(err, &netErr) && netErr.Timeout() {
					continue
				}
				return
			}
		}
	}()
	defer close(stopFastDrain)

	largePayload := make([]byte, 256*1024)
	for i := 0; i < 120; i++ {
		packet := buildPacket(t, protocol.PacketTypeFrame, uint32(i+1), largePayload)
		if err := agent.WriteMessage(websocket.BinaryMessage, packet); err != nil {
			t.Fatalf("write frame burst %d: %v", i+1, err)
		}
		time.Sleep(2 * time.Millisecond)
	}

	s, ok := h.registry.Get(created.SessionID)
	if !ok {
		t.Fatalf("session %s not found in registry", created.SessionID)
	}

	waitFor(t, 3*time.Second, func() bool {
		m := s.MetricsSnapshot()
		return m.FramesDropped > 0
	}, "expected dropped frame counters to increase for slow viewer scenario")

	if err := agent.Close(); err != nil {
		t.Fatalf("close first agent: %v", err)
	}

	waitFor(t, 2*time.Second, func() bool {
		hasAgent, _ := s.ConnectionState()
		return !hasAgent
	}, "expected first agent disconnect to clear session agent slot")

	reconnectedAgent := dialWS(t, wsURL)
	defer reconnectedAgent.Close()
	performHandshake(t, reconnectedAgent, "agent", created.AgentToken)

	reconnectViewer := dialWS(t, wsURL)
	defer reconnectViewer.Close()
	performHandshake(t, reconnectViewer, "viewer", created.ViewerToken)

	reconnectPacket := buildPacket(t, protocol.PacketTypeFrame, 999, []byte("reconnect-frame"))
	if err := reconnectedAgent.WriteMessage(websocket.BinaryMessage, reconnectPacket); err != nil {
		t.Fatalf("write reconnect verification frame: %v", err)
	}

	hdr, _, err := readPacket(reconnectViewer, 2*time.Second)
	if err != nil {
		t.Fatalf("read reconnect verification frame: %v", err)
	}
	if hdr.PacketType != protocol.PacketTypeFrame {
		t.Fatalf("reconnect viewer packet type: got %d want %d", hdr.PacketType, protocol.PacketTypeFrame)
	}
	if hdr.SequenceID != 999 {
		t.Fatalf("reconnect viewer sequence: got %d want %d", hdr.SequenceID, 999)
	}
}
