package integration

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"streamforge/internal/protocol"
)

type stabilitySummary struct {
	Scenario         string  `json:"scenario"`
	Viewers          int     `json:"viewers,omitempty"`
	FramesSent       int     `json:"framesSent"`
	FramesReceived   int     `json:"framesReceived"`
	FramesForwarded  uint64  `json:"framesForwarded"`
	FramesDropped    uint64  `json:"framesDropped"`
	DropRate         float64 `json:"dropRate"`
	LatencyP50Ms     float64 `json:"latencyP50Ms"`
	LatencyP95Ms     float64 `json:"latencyP95Ms"`
	MemoryGrowthMiB  float64 `json:"memoryGrowthMiB,omitempty"`
	ReconnectsTried  int     `json:"reconnectsTried,omitempty"`
	ReconnectsPassed int     `json:"reconnectsPassed,omitempty"`
	DurationSeconds  float64 `json:"durationSeconds,omitempty"`
}

type latencyCollector struct {
	conn      *websocket.Conn
	stop      chan struct{}
	received  atomic.Int64
	latMu     sync.Mutex
	latencies []float64
}

func startLatencyCollector(conn *websocket.Conn) *latencyCollector {
	c := &latencyCollector{
		conn:      conn,
		stop:      make(chan struct{}),
		latencies: make([]float64, 0, 1024),
	}

	go func() {
		for {
			select {
			case <-c.stop:
				return
			default:
			}

			header, payload, err := readPacket(conn, 300*time.Millisecond)
			if err != nil {
				var netErr net.Error
				if errors.As(err, &netErr) && netErr.Timeout() {
					continue
				}
				return
			}
			if header.PacketType != protocol.PacketTypeFrame {
				continue
			}
			if len(payload) < 8 {
				continue
			}

			sentNs := int64(binary.BigEndian.Uint64(payload[:8]))
			latencyMs := float64(time.Now().UnixNano()-sentNs) / 1_000_000.0
			if latencyMs < 0 {
				continue
			}

			c.received.Add(1)
			c.latMu.Lock()
			c.latencies = append(c.latencies, latencyMs)
			c.latMu.Unlock()
		}
	}()

	return c
}

func (c *latencyCollector) Stop() {
	select {
	case <-c.stop:
		return
	default:
		close(c.stop)
	}
}

func (c *latencyCollector) Received() int {
	return int(c.received.Load())
}

func (c *latencyCollector) Latencies() []float64 {
	c.latMu.Lock()
	defer c.latMu.Unlock()

	out := make([]float64, len(c.latencies))
	copy(out, c.latencies)
	return out
}

func latencyPayload(size int) []byte {
	if size < 8 {
		size = 8
	}
	payload := make([]byte, size)
	binary.BigEndian.PutUint64(payload[:8], uint64(time.Now().UnixNano()))
	for i := 8; i < len(payload); i++ {
		payload[i] = byte(i % 251)
	}
	return payload
}

func aggregateCollectorData(collectors []*latencyCollector) (received int, latencies []float64) {
	for _, collector := range collectors {
		received += collector.Received()
		latencies = append(latencies, collector.Latencies()...)
	}
	return received, latencies
}

func percentile(latencies []float64, p float64) float64 {
	if len(latencies) == 0 {
		return 0
	}
	if p <= 0 {
		p = 0
	}
	if p >= 100 {
		p = 100
	}

	sorted := make([]float64, len(latencies))
	copy(sorted, latencies)
	sort.Float64s(sorted)

	index := int((p / 100) * float64(len(sorted)-1))
	return sorted[index]
}

func dropRate(forwarded uint64, dropped uint64) float64 {
	total := forwarded + dropped
	if total == 0 {
		return 0
	}
	return float64(dropped) / float64(total)
}

func emitStabilitySummary(t *testing.T, summary stabilitySummary) {
	t.Helper()

	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("encode stability summary: %v", err)
	}
	t.Logf("stability_summary=%s", string(encoded))

	resultsPath := os.Getenv("STREAMFORGE_STABILITY_RESULTS")
	if resultsPath == "" {
		return
	}

	file, err := os.OpenFile(resultsPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open results file %s: %v", resultsPath, err)
	}
	defer file.Close()

	if _, err := file.Write(append(encoded, '\n')); err != nil {
		t.Fatalf("append results file %s: %v", resultsPath, err)
	}
}

func multiViewerCounts() ([]int, error) {
	raw := os.Getenv("STREAMFORGE_MULTI_VIEWER_COUNTS")
	if raw == "" {
		return []int{2, 5, 10}, nil
	}

	parts := strings.Split(raw, ",")
	out := make([]int, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		value, err := strconv.Atoi(trimmed)
		if err != nil {
			return nil, err
		}
		if value <= 0 {
			return nil, errors.New("viewer count must be positive")
		}
		out = append(out, value)
	}

	if len(out) == 0 {
		return nil, errors.New("no viewer counts provided")
	}

	return out, nil
}

func TestStability_MultiViewerMatrix(t *testing.T) {
	viewerCounts, err := multiViewerCounts()
	if err != nil {
		t.Fatalf("parse STREAMFORGE_MULTI_VIEWER_COUNTS: %v", err)
	}

	for _, viewerCount := range viewerCounts {
		t.Run("viewers_"+strconv.Itoa(viewerCount), func(t *testing.T) {
			h := newIntegrationHarness(t)
			created := h.createSession(t)
			wsURL := h.wsBaseURL() + "/ws/session/" + created.SessionID

			agent := dialWS(t, wsURL)
			defer agent.Close()
			performHandshake(t, agent, "agent", created.AgentToken)

			viewers := make([]*websocket.Conn, 0, viewerCount)
			collectors := make([]*latencyCollector, 0, viewerCount)
			for i := 0; i < viewerCount; i++ {
				viewer := dialWS(t, wsURL)
				viewers = append(viewers, viewer)
				defer viewer.Close()
				performHandshake(t, viewer, "viewer", created.ViewerToken)
				collectors = append(collectors, startLatencyCollector(viewer))
			}
			defer func() {
				for _, collector := range collectors {
					collector.Stop()
				}
			}()

			framesSent := 80
			for i := 0; i < framesSent; i++ {
				packet := buildPacket(t, protocol.PacketTypeFrame, uint32(i+1), latencyPayload(512))
				if err := agent.WriteMessage(websocket.BinaryMessage, packet); err != nil {
					t.Fatalf("write frame %d: %v", i+1, err)
				}
				time.Sleep(10 * time.Millisecond)
			}

			expectedMinReceives := framesSent * viewerCount / 2
			waitFor(t, 4*time.Second, func() bool {
				received, _ := aggregateCollectorData(collectors)
				return received >= expectedMinReceives
			}, "timed out waiting for viewer receipts in multi-viewer matrix")

			s, ok := h.registry.Get(created.SessionID)
			if !ok {
				t.Fatalf("session %s not found", created.SessionID)
			}
			metrics := s.MetricsSnapshot()
			received, latencies := aggregateCollectorData(collectors)

			summary := stabilitySummary{
				Scenario:        "multiple_viewers",
				Viewers:         viewerCount,
				FramesSent:      framesSent,
				FramesReceived:  received,
				FramesForwarded: metrics.FramesForwarded,
				FramesDropped:   metrics.FramesDropped,
				DropRate:        dropRate(metrics.FramesForwarded, metrics.FramesDropped),
				LatencyP50Ms:    percentile(latencies, 50),
				LatencyP95Ms:    percentile(latencies, 95),
			}
			emitStabilitySummary(t, summary)
		})
	}
}

func TestStability_SlowViewerSimulation(t *testing.T) {
	h := newIntegrationHarness(t)
	created := h.createSession(t)
	wsURL := h.wsBaseURL() + "/ws/session/" + created.SessionID

	agent := dialWS(t, wsURL)
	defer agent.Close()
	performHandshake(t, agent, "agent", created.AgentToken)

	// Intentionally never read this connection to force outbound queue pressure.
	slowViewer := dialWS(t, wsURL)
	defer slowViewer.Close()
	performHandshake(t, slowViewer, "viewer", created.ViewerToken)

	fastViewers := make([]*websocket.Conn, 0, 2)
	collectors := make([]*latencyCollector, 0, 2)
	for i := 0; i < 2; i++ {
		viewer := dialWS(t, wsURL)
		fastViewers = append(fastViewers, viewer)
		defer viewer.Close()
		performHandshake(t, viewer, "viewer", created.ViewerToken)
		collectors = append(collectors, startLatencyCollector(viewer))
	}
	defer func() {
		for _, collector := range collectors {
			collector.Stop()
		}
	}()

	framesSent := 250
	for i := 0; i < framesSent; i++ {
		packet := buildPacket(t, protocol.PacketTypeFrame, uint32(i+1), latencyPayload(128*1024))
		if err := agent.WriteMessage(websocket.BinaryMessage, packet); err != nil {
			t.Fatalf("write pressure frame %d: %v", i+1, err)
		}
		time.Sleep(2 * time.Millisecond)
	}

	s, ok := h.registry.Get(created.SessionID)
	if !ok {
		t.Fatalf("session %s not found", created.SessionID)
	}

	waitFor(t, 5*time.Second, func() bool {
		m := s.MetricsSnapshot()
		return m.FramesDropped > 0
	}, "expected frame drops under slow viewer simulation")

	metrics := s.MetricsSnapshot()
	received, latencies := aggregateCollectorData(collectors)
	if metrics.FramesDropped == 0 {
		t.Fatalf("expected dropped frames > 0 with slow viewer pressure")
	}

	summary := stabilitySummary{
		Scenario:        "slow_viewer",
		Viewers:         3,
		FramesSent:      framesSent,
		FramesReceived:  received,
		FramesForwarded: metrics.FramesForwarded,
		FramesDropped:   metrics.FramesDropped,
		DropRate:        dropRate(metrics.FramesForwarded, metrics.FramesDropped),
		LatencyP50Ms:    percentile(latencies, 50),
		LatencyP95Ms:    percentile(latencies, 95),
	}
	emitStabilitySummary(t, summary)
}

func TestStability_ReconnectStormSimulation(t *testing.T) {
	h := newIntegrationHarness(t)
	created := h.createSession(t)
	wsURL := h.wsBaseURL() + "/ws/session/" + created.SessionID

	viewer := dialWS(t, wsURL)
	defer viewer.Close()
	performHandshake(t, viewer, "viewer", created.ViewerToken)
	collector := startLatencyCollector(viewer)
	defer collector.Stop()

	reconnects := 25
	reconnectsPassed := 0

	for i := 0; i < reconnects; i++ {
		agent := dialWS(t, wsURL)
		performHandshake(t, agent, "agent", created.AgentToken)

		packet := buildPacket(t, protocol.PacketTypeFrame, uint32(1000+i), latencyPayload(1024))
		if err := agent.WriteMessage(websocket.BinaryMessage, packet); err != nil {
			agent.Close()
			t.Fatalf("write reconnect storm frame %d: %v", i+1, err)
		}

		reconnectsPassed++
		_ = agent.Close()
		time.Sleep(20 * time.Millisecond)
	}

	waitFor(t, 5*time.Second, func() bool {
		return collector.Received() >= reconnects/2
	}, "expected reconnect storm to deliver at least half of verification frames")

	s, ok := h.registry.Get(created.SessionID)
	if !ok {
		t.Fatalf("session %s not found", created.SessionID)
	}
	metrics := s.MetricsSnapshot()
	latencies := collector.Latencies()

	summary := stabilitySummary{
		Scenario:         "reconnect_storm",
		Viewers:          1,
		FramesSent:       reconnects,
		FramesReceived:   collector.Received(),
		FramesForwarded:  metrics.FramesForwarded,
		FramesDropped:    metrics.FramesDropped,
		DropRate:         dropRate(metrics.FramesForwarded, metrics.FramesDropped),
		LatencyP50Ms:     percentile(latencies, 50),
		LatencyP95Ms:     percentile(latencies, 95),
		ReconnectsTried:  reconnects,
		ReconnectsPassed: reconnectsPassed,
	}
	emitStabilitySummary(t, summary)
}

func TestStability_Soak30To60Minutes(t *testing.T) {
	if os.Getenv("STREAMFORGE_ENABLE_SOAK") != "1" {
		t.Skip("set STREAMFORGE_ENABLE_SOAK=1 to run long soak scenario")
	}

	duration := 30 * time.Minute
	if raw := os.Getenv("STREAMFORGE_SOAK_DURATION"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			t.Fatalf("invalid STREAMFORGE_SOAK_DURATION=%q: %v", raw, err)
		}
		duration = parsed
	}
	if duration < 30*time.Minute || duration > 60*time.Minute {
		t.Fatalf("soak duration must be between 30m and 60m, got %s", duration)
	}

	h := newIntegrationHarness(t)
	created := h.createSession(t)
	wsURL := h.wsBaseURL() + "/ws/session/" + created.SessionID

	agent := dialWS(t, wsURL)
	defer agent.Close()
	performHandshake(t, agent, "agent", created.AgentToken)

	// One intentionally slow viewer + four fast viewers.
	slowViewer := dialWS(t, wsURL)
	defer slowViewer.Close()
	performHandshake(t, slowViewer, "viewer", created.ViewerToken)

	collectors := make([]*latencyCollector, 0, 4)
	for i := 0; i < 4; i++ {
		viewer := dialWS(t, wsURL)
		defer viewer.Close()
		performHandshake(t, viewer, "viewer", created.ViewerToken)
		collectors = append(collectors, startLatencyCollector(viewer))
	}
	defer func() {
		for _, collector := range collectors {
			collector.Stop()
		}
	}()

	var memStart runtime.MemStats
	runtime.ReadMemStats(&memStart)

	deadline := time.Now().Add(duration)
	framesSent := 0
	for time.Now().Before(deadline) {
		framesSent++
		packet := buildPacket(t, protocol.PacketTypeFrame, uint32(framesSent), latencyPayload(64*1024))
		if err := agent.WriteMessage(websocket.BinaryMessage, packet); err != nil {
			t.Fatalf("soak frame write failed at frame %d: %v", framesSent, err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	waitFor(t, 10*time.Second, func() bool {
		received, _ := aggregateCollectorData(collectors)
		return received > 0
	}, "expected at least one frame receipt during soak")

	var memEnd runtime.MemStats
	runtime.ReadMemStats(&memEnd)

	s, ok := h.registry.Get(created.SessionID)
	if !ok {
		t.Fatalf("session %s not found", created.SessionID)
	}
	metrics := s.MetricsSnapshot()
	received, latencies := aggregateCollectorData(collectors)

	memoryGrowthBytes := int64(memEnd.HeapAlloc) - int64(memStart.HeapAlloc)
	memoryGrowthMiB := float64(memoryGrowthBytes) / (1024.0 * 1024.0)

	summary := stabilitySummary{
		Scenario:        "soak",
		Viewers:         5,
		FramesSent:      framesSent,
		FramesReceived:  received,
		FramesForwarded: metrics.FramesForwarded,
		FramesDropped:   metrics.FramesDropped,
		DropRate:        dropRate(metrics.FramesForwarded, metrics.FramesDropped),
		LatencyP50Ms:    percentile(latencies, 50),
		LatencyP95Ms:    percentile(latencies, 95),
		MemoryGrowthMiB: memoryGrowthMiB,
		DurationSeconds: duration.Seconds(),
	}
	emitStabilitySummary(t, summary)
}
