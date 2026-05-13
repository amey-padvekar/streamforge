package transport

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"streamforge/internal/protocol"
)

const (
	defaultMaxRetries  = 5
	defaultBaseBackoff = 500 * time.Millisecond
	defaultMaxBackoff  = 8 * time.Second
	heartbeatInterval  = 5 * time.Second
	heartbeatTimeout   = 15 * time.Second
)

type ConnectionState string

const (
	ConnectionStateDisconnected  ConnectionState = "disconnected"
	ConnectionStateConnecting    ConnectionState = "connecting"
	ConnectionStateAuthenticated ConnectionState = "authenticated"
	ConnectionStateStreaming     ConnectionState = "streaming"
	ConnectionStateStale         ConnectionState = "stale"
)

var validConnectionTransitions = map[ConnectionState]map[ConnectionState]struct{}{
	ConnectionStateDisconnected: {
		ConnectionStateConnecting: {},
	},
	ConnectionStateConnecting: {
		ConnectionStateAuthenticated: {},
		ConnectionStateDisconnected:  {},
		ConnectionStateStale:         {},
	},
	ConnectionStateAuthenticated: {
		ConnectionStateStreaming:    {},
		ConnectionStateDisconnected: {},
		ConnectionStateStale:        {},
	},
	ConnectionStateStreaming: {
		ConnectionStateStale:        {},
		ConnectionStateDisconnected: {},
	},
	ConnectionStateStale: {
		ConnectionStateConnecting:   {},
		ConnectionStateDisconnected: {},
	},
}

// WSTransport sends frame packets from the agent to the server over WebSocket.
type WSTransport struct {
	wsURL     string
	sessionID string
	token     string

	maxRetries  int
	baseBackoff time.Duration
	maxBackoff  time.Duration

	logger *slog.Logger
	dialer websocket.Dialer

	mu     sync.Mutex
	writeM sync.Mutex
	conn   *websocket.Conn
	closed bool
	state  ConnectionState

	everConnected  bool
	reconnectCount uint64
	heartbeatSeq   uint32
	connDone       chan struct{}
}

// NewWSTransport creates an agent WebSocket transport.
func NewWSTransport(serverURL, sessionID, agentToken string, maxRetries int, logger *slog.Logger) (*WSTransport, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("session ID is required")
	}
	if strings.TrimSpace(agentToken) == "" {
		return nil, fmt.Errorf("agent token is required")
	}

	wsURL, err := buildSessionWSURL(serverURL, sessionID)
	if err != nil {
		return nil, err
	}

	if maxRetries < 0 {
		maxRetries = defaultMaxRetries
	}

	if logger == nil {
		logger = slog.Default()
	}

	return &WSTransport{
		wsURL:       wsURL,
		sessionID:   sessionID,
		token:       agentToken,
		maxRetries:  maxRetries,
		baseBackoff: defaultBaseBackoff,
		maxBackoff:  defaultMaxBackoff,
		logger:      logger,
		dialer:      websocket.Dialer{HandshakeTimeout: 5 * time.Second},
		state:       ConnectionStateDisconnected,
	}, nil
}

// Connect establishes the initial WebSocket connection and sends auth handshake.
func (t *WSTransport) Connect() error {
	_ = t.setState(ConnectionStateConnecting, "connect requested")
	_, err := t.connectWithRetry()
	return err
}

// Send writes a binary frame message, reconnecting with backoff if needed.
func (t *WSTransport) Send(data []byte) error {
	if len(data) == 0 {
		return nil
	}

	for attempt := 0; attempt <= t.maxRetries; attempt++ {
		conn, err := t.ensureConnected()
		if err != nil {
			return err
		}

		t.writeM.Lock()
		err = conn.WriteMessage(websocket.BinaryMessage, data)
		t.writeM.Unlock()
		if err == nil {
			_ = t.setState(ConnectionStateStreaming, "frame send succeeded")
			return nil
		}

		t.logger.Warn("agent transport write failed; reconnecting", "sessionId", t.sessionID, "role", "agent", "frameId", 0, "packetType", protocol.PacketTypeFrame, "queueDepth", 0, "framesDropped", 0, "errorCategory", "transport", "attempt", attempt+1, "error", err)
		_ = t.setState(ConnectionStateStale, "frame send failed")
		t.dropConn(conn)

		if attempt == t.maxRetries {
			return fmt.Errorf("send failed after %d attempts: %w", attempt+1, err)
		}

		t.sleepBackoff(attempt + 1)
	}

	return fmt.Errorf("send failed")
}

// Close shuts down the underlying WebSocket connection.
func (t *WSTransport) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	conn := t.conn
	t.conn = nil
	if t.connDone != nil {
		close(t.connDone)
		t.connDone = nil
	}
	t.mu.Unlock()
	_ = t.setState(ConnectionStateDisconnected, "transport closed")

	if conn != nil {
		return conn.Close()
	}
	return nil
}

func (t *WSTransport) ensureConnected() (*websocket.Conn, error) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil, errors.New("transport is closed")
	}
	if t.conn != nil {
		conn := t.conn
		t.mu.Unlock()
		return conn, nil
	}
	t.mu.Unlock()

	return t.connectWithRetry()
}

func (t *WSTransport) connectWithRetry() (*websocket.Conn, error) {
	var lastErr error
	wasReconnect := t.wasConnectedBefore()
	_ = t.setState(ConnectionStateConnecting, "connecting to websocket")
	if wasReconnect {
		t.logger.Info("agent reconnect started", "sessionId", t.sessionID, "role", "agent")
	}

	for attempt := 0; attempt <= t.maxRetries; attempt++ {
		conn, err := t.connectOnce()
		if err == nil {
			return conn, nil
		}
		lastErr = err

		if attempt == t.maxRetries {
			break
		}

		t.logger.Warn("agent reconnect attempt failed", "sessionId", t.sessionID, "role", "agent", "frameId", 0, "packetType", 0, "queueDepth", 0, "framesDropped", 0, "errorCategory", "transport", "attempt", attempt+1, "error", err)
		t.sleepBackoff(attempt + 1)
	}

	if wasReconnect {
		t.logger.Error("agent reconnect failed", "sessionId", t.sessionID, "role", "agent", "frameId", 0, "packetType", 0, "queueDepth", 0, "framesDropped", 0, "errorCategory", "transport", "maxRetries", t.maxRetries+1, "error", lastErr)
	}
	_ = t.setState(ConnectionStateStale, "connect retries exhausted")

	return nil, fmt.Errorf("failed to connect after %d attempts: %w", t.maxRetries+1, lastErr)
}

func (t *WSTransport) connectOnce() (*websocket.Conn, error) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil, errors.New("transport is closed")
	}
	if t.conn != nil {
		conn := t.conn
		t.mu.Unlock()
		return conn, nil
	}
	t.mu.Unlock()

	conn, _, err := t.dialer.Dial(t.wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("dial failed: %w", err)
	}

	// Send HELLO packet
	helloPayload, err := protocol.EncodeHello(protocol.HelloPayload{
		AgentID:          "agent",
		SupportedVersion: protocol.ProtocolVersion,
		CapabilityFlags:  0,
	})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("encode HELLO failed: %w", err)
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
		_ = conn.Close()
		return nil, fmt.Errorf("build HELLO packet failed: %w", err)
	}

	if err := conn.WriteMessage(websocket.BinaryMessage, helloPacket); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("HELLO handshake failed: %w", err)
	}

	// Read ACK or ERROR response
	_, respData, err := conn.ReadMessage()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("read HELLO response failed: %w", err)
	}

	respHeader, respPayload, err := protocol.ParsePacket(respData)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("parse HELLO response failed: %w", err)
	}

	if respHeader.PacketType == protocol.PacketTypeError {
		errPayload, parseErr := protocol.DecodeError(respPayload)
		if parseErr == nil {
			_ = conn.Close()
			return nil, fmt.Errorf("server rejected HELLO: %s (%s)", errPayload.Reason, errPayload.Detail)
		}
		_ = conn.Close()
		_ = t.setState(ConnectionStateStale, "server rejected HELLO")
		return nil, fmt.Errorf("server rejected HELLO with unparseable error")
	}

	if respHeader.PacketType != protocol.PacketTypeAck {
		_ = conn.Close()
		_ = t.setState(ConnectionStateStale, "invalid HELLO response packet type")
		return nil, fmt.Errorf("invalid HELLO response type: expected ACK, got %d", respHeader.PacketType)
	}
	_ = t.setState(ConnectionStateAuthenticated, "HELLO acknowledged")

	// Send AUTH packet
	authPayload, err := protocol.EncodeAuth(protocol.AuthPayload{
		Role:  "agent",
		Token: t.token,
	})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("encode AUTH failed: %w", err)
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
		_ = conn.Close()
		return nil, fmt.Errorf("build AUTH packet failed: %w", err)
	}

	if err := conn.WriteMessage(websocket.BinaryMessage, authPacket); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("AUTH handshake failed: %w", err)
	}

	// Read AUTH response
	_, authRespData, err := conn.ReadMessage()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("read AUTH response failed: %w", err)
	}

	authRespHeader, authRespPayload, err := protocol.ParsePacket(authRespData)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("parse AUTH response failed: %w", err)
	}

	if authRespHeader.PacketType == protocol.PacketTypeError {
		errPayload, parseErr := protocol.DecodeError(authRespPayload)
		if parseErr == nil {
			_ = conn.Close()
			return nil, fmt.Errorf("server rejected AUTH: %s (%s)", errPayload.Reason, errPayload.Detail)
		}
		_ = conn.Close()
		_ = t.setState(ConnectionStateStale, "server rejected AUTH")
		return nil, fmt.Errorf("server rejected AUTH with unparseable error")
	}

	if authRespHeader.PacketType != protocol.PacketTypeAck {
		_ = conn.Close()
		_ = t.setState(ConnectionStateStale, "invalid AUTH response packet type")
		return nil, fmt.Errorf("invalid AUTH response type: expected ACK, got %d", authRespHeader.PacketType)
	}
	_ = t.setState(ConnectionStateAuthenticated, "AUTH acknowledged")

	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		_ = conn.Close()
		return nil, errors.New("transport is closed")
	}
	if t.conn != nil {
		t.mu.Unlock()
		_ = conn.Close()
		return t.conn, nil
	}

	isReconnect := t.everConnected
	reconnectCount := t.reconnectCount
	if isReconnect {
		t.reconnectCount++
		reconnectCount = t.reconnectCount
	}
	t.everConnected = true
	t.conn = conn
	t.connDone = make(chan struct{})
	t.mu.Unlock()

	if isReconnect {
		t.logger.Info("agent websocket reconnected", "sessionId", t.sessionID, "role", "agent", "url", t.wsURL, "reconnectCount", reconnectCount)
	} else {
		t.logger.Info("agent websocket connected", "sessionId", t.sessionID, "role", "agent", "url", t.wsURL)
	}
	_ = t.setState(ConnectionStateStreaming, "connection ready for frame streaming")
	t.startConnectionMonitors(conn)
	return conn, nil
}

func (t *WSTransport) wasConnectedBefore() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.everConnected
}

func (t *WSTransport) dropConn(candidate *websocket.Conn) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.conn == candidate {
		if t.connDone != nil {
			close(t.connDone)
			t.connDone = nil
		}
		_ = t.conn.Close()
		t.conn = nil
	}
}

func (t *WSTransport) startConnectionMonitors(conn *websocket.Conn) {
	t.mu.Lock()
	done := t.connDone
	t.mu.Unlock()

	go t.runHeartbeatSender(conn, done)
	go t.runHeartbeatReceiver(conn, done)
}

func (t *WSTransport) runHeartbeatSender(conn *websocket.Conn, done <-chan struct{}) {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			if err := t.sendHeartbeat(conn); err != nil {
				t.logger.Warn("agent heartbeat send failed", "sessionId", t.sessionID, "role", "agent", "frameId", 0, "packetType", protocol.PacketTypeHeartbeat, "queueDepth", 0, "framesDropped", 0, "errorCategory", "timeout", "reason", "heartbeat_send_failed", "error", err)
				_ = t.setState(ConnectionStateStale, "heartbeat send failed")
				t.dropConn(conn)
				return
			}
		}
	}
}

func (t *WSTransport) runHeartbeatReceiver(conn *websocket.Conn, done <-chan struct{}) {
	for {
		select {
		case <-done:
			return
		default:
		}

		_ = conn.SetReadDeadline(time.Now().Add(heartbeatTimeout))
		messageType, packet, err := conn.ReadMessage()
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				t.logger.Warn("agent heartbeat timeout", "sessionId", t.sessionID, "role", "agent", "frameId", 0, "packetType", protocol.PacketTypeHeartbeat, "queueDepth", 0, "framesDropped", 0, "errorCategory", "timeout", "reason", "server_heartbeat_timeout", "threshold", heartbeatTimeout.String())
				_ = t.setState(ConnectionStateStale, "server heartbeat timeout")
				t.dropConn(conn)
				return
			}

			t.dropConn(conn)
			return
		}

		if messageType != websocket.BinaryMessage {
			continue
		}

		header, payload, err := protocol.ParsePacket(packet)
		if err != nil {
			continue
		}

		switch header.PacketType {
		case protocol.PacketTypeHeartbeat:
			continue
		case protocol.PacketTypeError:
			errPayload, parseErr := protocol.DecodeError(payload)
			if parseErr == nil {
				t.logger.Warn("agent received protocol error", "sessionId", t.sessionID, "role", "agent", "frameId", int64(header.SequenceID), "packetType", header.PacketType, "queueDepth", 0, "framesDropped", 0, "errorCategory", "protocol", "reason", errPayload.Reason, "detail", errPayload.Detail)
			} else {
				t.logger.Warn("agent received malformed protocol error", "sessionId", t.sessionID, "role", "agent", "frameId", int64(header.SequenceID), "packetType", header.PacketType, "queueDepth", 0, "framesDropped", 0, "errorCategory", "protocol")
			}
			_ = t.setState(ConnectionStateStale, "server protocol error")
			t.dropConn(conn)
			return
		default:
			continue
		}
	}
}

func (t *WSTransport) sendHeartbeat(conn *websocket.Conn) error {
	sequenceID := atomic.AddUint32(&t.heartbeatSeq, 1)
	header := protocol.Header{
		Version:     protocol.ProtocolVersion,
		PacketType:  protocol.PacketTypeHeartbeat,
		Flags:       0,
		Reserved:    0,
		SequenceID:  sequenceID,
		TimestampNs: uint64(time.Now().UnixNano()),
		PayloadLen:  0,
	}

	packet, err := protocol.BuildPacket(header, nil)
	if err != nil {
		return err
	}

	t.writeM.Lock()
	err = conn.WriteMessage(websocket.BinaryMessage, packet)
	t.writeM.Unlock()

	return err
}

func (t *WSTransport) setState(next ConnectionState, reason string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.state == next {
		return true
	}

	if !isValidConnectionTransition(t.state, next) {
		t.logger.Warn("agent connection state transition rejected", "sessionId", t.sessionID, "role", "agent", "frameId", 0, "packetType", 0, "queueDepth", 0, "framesDropped", 0, "errorCategory", "internal", "from", t.state, "to", next, "reason", reason)
		return false
	}

	t.logger.Info("agent connection state transitioned", "sessionId", t.sessionID, "role", "agent", "from", t.state, "to", next, "reason", reason)
	t.state = next
	return true
}

func isValidConnectionTransition(from ConnectionState, to ConnectionState) bool {
	next, ok := validConnectionTransitions[from]
	if !ok {
		return false
	}
	_, ok = next[to]
	return ok
}

func (t *WSTransport) sleepBackoff(attempt int) {
	d := t.baseBackoff
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= t.maxBackoff {
			d = t.maxBackoff
			break
		}
	}
	t.logger.Info("agent reconnect backoff", "sessionId", t.sessionID, "role", "agent", "attempt", attempt, "wait", d.String())
	time.Sleep(d)
}

func (t *WSTransport) SessionID() string {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.sessionID
}

func buildSessionWSURL(serverURL, sessionID string) (string, error) {
	raw := strings.TrimSpace(serverURL)
	if raw == "" {
		return "", fmt.Errorf("server URL is required")
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid server URL %q: %w", raw, err)
	}

	if u.Scheme == "" {
		u = &url.URL{Scheme: "ws", Host: raw}
	}

	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported server URL scheme %q", u.Scheme)
	}

	if strings.TrimSpace(u.Host) == "" {
		return "", fmt.Errorf("server URL host is required")
	}

	basePath := strings.TrimRight(u.Path, "/")
	u.Path = basePath + "/ws/session/" + url.PathEscape(sessionID)
	u.RawQuery = ""
	u.Fragment = ""

	return u.String(), nil
}
