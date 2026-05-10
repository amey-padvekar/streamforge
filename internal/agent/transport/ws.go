package transport

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"streamforge/internal/protocol"
)

const (
	defaultMaxRetries  = 5
	defaultBaseBackoff = 500 * time.Millisecond
	defaultMaxBackoff  = 8 * time.Second
)

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

	everConnected  bool
	reconnectCount uint64
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
	}, nil
}

// Connect establishes the initial WebSocket connection and sends auth handshake.
func (t *WSTransport) Connect() error {
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
			return nil
		}

		t.logger.Warn("agent transport write failed; reconnecting", "sessionId", t.sessionID, "attempt", attempt+1, "error", err)
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
	t.mu.Unlock()

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
	if wasReconnect {
		t.logger.Info("agent reconnect started", "sessionId", t.sessionID)
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

		t.logger.Warn("agent reconnect attempt failed", "sessionId", t.sessionID, "attempt", attempt+1, "error", err)
		t.sleepBackoff(attempt + 1)
	}

	if wasReconnect {
		t.logger.Error("agent reconnect failed", "sessionId", t.sessionID, "maxRetries", t.maxRetries+1, "error", lastErr)
	}

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
		return nil, fmt.Errorf("server rejected HELLO with unparseable error")
	}

	if respHeader.PacketType != protocol.PacketTypeAck {
		_ = conn.Close()
		return nil, fmt.Errorf("invalid HELLO response type: expected ACK, got %d", respHeader.PacketType)
	}

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
		return nil, fmt.Errorf("server rejected AUTH with unparseable error")
	}

	if authRespHeader.PacketType != protocol.PacketTypeAck {
		_ = conn.Close()
		return nil, fmt.Errorf("invalid AUTH response type: expected ACK, got %d", authRespHeader.PacketType)
	}

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
	t.mu.Unlock()

	if isReconnect {
		t.logger.Info("agent websocket reconnected", "sessionId", t.sessionID, "url", t.wsURL, "reconnectCount", reconnectCount)
	} else {
		t.logger.Info("agent websocket connected", "sessionId", t.sessionID, "url", t.wsURL)
	}
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
		_ = t.conn.Close()
		t.conn = nil
	}
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
	t.logger.Info("agent reconnect backoff", "sessionId", t.sessionID, "attempt", attempt, "wait", d.String())
	time.Sleep(d)
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
