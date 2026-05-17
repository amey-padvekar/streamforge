package metrics

import (
	"net/http"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var defaultMetrics atomic.Pointer[Prometheus]

const (
	ErrorCategoryAuth         = "auth"
	ErrorCategoryProtocol     = "protocol"
	ErrorCategoryTransport    = "transport"
	ErrorCategoryTimeout      = "timeout"
	ErrorCategoryBackpressure = "backpressure"
	ErrorCategoryInternal     = "internal"
)

var validTransportErrorCategories = map[string]struct{}{
	ErrorCategoryAuth:         {},
	ErrorCategoryProtocol:     {},
	ErrorCategoryTransport:    {},
	ErrorCategoryTimeout:      {},
	ErrorCategoryBackpressure: {},
	ErrorCategoryInternal:     {},
}

// Prometheus stores metric vectors and a dedicated registry.
type Prometheus struct {
	registry *prometheus.Registry

	framesReceived   *prometheus.CounterVec
	framesForwarded  *prometheus.CounterVec
	framesDropped    *prometheus.CounterVec
	inputReceived    *prometheus.CounterVec
	inputForwarded   *prometheus.CounterVec
	inputDropped     *prometheus.CounterVec
	controlDenied    *prometheus.CounterVec
	viewersConnected *prometheus.GaugeVec
	sessionFPS       *prometheus.GaugeVec
	transportErrors  *prometheus.CounterVec
	routingLatencyMs *prometheus.HistogramVec
}

var latencyBucketsMs = []float64{1, 2, 5, 10, 20, 50, 100, 200, 500}

func NewPrometheus() *Prometheus {
	registry := prometheus.NewRegistry()

	p := &Prometheus{
		registry: registry,
		framesReceived: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "streamforge_frames_received_total",
				Help: "Total number of frames received from agent per session.",
			},
			[]string{"session_id"},
		),
		framesForwarded: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "streamforge_frames_forwarded_total",
				Help: "Total number of frames forwarded to viewers per session.",
			},
			[]string{"session_id"},
		),
		framesDropped: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "streamforge_frames_dropped_total",
				Help: "Total number of dropped frames per session by reason.",
			},
			[]string{"session_id", "reason"},
		),
		inputReceived: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "streamforge_input_received_total",
				Help: "Total number of INPUT packets received from viewers by event type.",
			},
			[]string{"session_id", "viewer_id", "event_type"},
		),
		inputForwarded: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "streamforge_input_forwarded_total",
				Help: "Total number of INPUT packets forwarded to active agent by event type.",
			},
			[]string{"session_id", "event_type"},
		),
		inputDropped: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "streamforge_input_dropped_total",
				Help: "Total number of dropped INPUT packets by reason.",
			},
			[]string{"session_id", "reason"},
		),
		controlDenied: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "streamforge_control_permission_denied_total",
				Help: "Total number of control permission denials by reason.",
			},
			[]string{"session_id", "reason"},
		),
		viewersConnected: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "streamforge_viewers_connected",
				Help: "Current number of connected viewers per session.",
			},
			[]string{"session_id"},
		),
		sessionFPS: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "streamforge_session_fps",
				Help: "Observed frame ingress FPS per session.",
			},
			[]string{"session_id"},
		),
		transportErrors: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "streamforge_transport_errors_total",
				Help: "Total number of categorized transport and protocol errors by role.",
			},
			[]string{"role", "category"},
		),
		routingLatencyMs: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "streamforge_server_routing_latency_ms",
				Help:    "Latency of server frame routing fanout in milliseconds.",
				Buckets: latencyBucketsMs,
			},
			[]string{"session_id"},
		),
	}

	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		p.framesReceived,
		p.framesForwarded,
		p.framesDropped,
		p.inputReceived,
		p.inputForwarded,
		p.inputDropped,
		p.controlDenied,
		p.viewersConnected,
		p.sessionFPS,
		p.transportErrors,
		p.routingLatencyMs,
	)

	return p
}

func (p *Prometheus) Handler() http.Handler {
	if p == nil {
		return promhttp.Handler()
	}

	return promhttp.HandlerFor(p.registry, promhttp.HandlerOpts{})
}

func SetDefault(p *Prometheus) {
	defaultMetrics.Store(p)
}

func IncFramesReceived(sessionID string, count int) {
	p := defaultMetrics.Load()
	if p == nil || count <= 0 {
		return
	}

	p.framesReceived.WithLabelValues(sessionLabelValue(sessionID)).Add(float64(count))
}

func IncFramesForwarded(sessionID string, count int) {
	p := defaultMetrics.Load()
	if p == nil || count <= 0 {
		return
	}

	p.framesForwarded.WithLabelValues(sessionLabelValue(sessionID)).Add(float64(count))
}

func IncFramesDropped(sessionID string, reason string, count int) {
	p := defaultMetrics.Load()
	if p == nil || count <= 0 {
		return
	}

	p.framesDropped.WithLabelValues(sessionLabelValue(sessionID), stringLabelValue(reason)).Add(float64(count))
}

func IncInputReceived(sessionID string, viewerID string, eventType string, count int) {
	p := defaultMetrics.Load()
	if p == nil || count <= 0 {
		return
	}

	p.inputReceived.WithLabelValues(sessionLabelValue(sessionID), stringLabelValue(viewerID), stringLabelValue(eventType)).Add(float64(count))
}

func IncInputForwarded(sessionID string, eventType string, count int) {
	p := defaultMetrics.Load()
	if p == nil || count <= 0 {
		return
	}

	p.inputForwarded.WithLabelValues(sessionLabelValue(sessionID), stringLabelValue(eventType)).Add(float64(count))
}

func IncInputDropped(sessionID string, reason string, count int) {
	p := defaultMetrics.Load()
	if p == nil || count <= 0 {
		return
	}

	p.inputDropped.WithLabelValues(sessionLabelValue(sessionID), stringLabelValue(reason)).Add(float64(count))
}

func IncControlPermissionDenied(sessionID string, reason string, count int) {
	p := defaultMetrics.Load()
	if p == nil || count <= 0 {
		return
	}

	p.controlDenied.WithLabelValues(sessionLabelValue(sessionID), stringLabelValue(reason)).Add(float64(count))
}

func SetViewersConnected(sessionID string, count int) {
	p := defaultMetrics.Load()
	if p == nil {
		return
	}

	if count < 0 {
		count = 0
	}

	p.viewersConnected.WithLabelValues(sessionLabelValue(sessionID)).Set(float64(count))
}

func SetSessionFPS(sessionID string, fps float64) {
	p := defaultMetrics.Load()
	if p == nil {
		return
	}
	if fps < 0 {
		fps = 0
	}

	p.sessionFPS.WithLabelValues(sessionLabelValue(sessionID)).Set(fps)
}

func IncTransportErrors(role string, category string) {
	p := defaultMetrics.Load()
	if p == nil {
		return
	}

	p.transportErrors.WithLabelValues(stringLabelValue(role), normalizeTransportErrorCategory(category)).Inc()
}

func ObserveServerRoutingLatency(sessionID string, durationMs float64) {
	p := defaultMetrics.Load()
	if p == nil {
		return
	}
	if durationMs < 0 {
		durationMs = 0
	}

	p.routingLatencyMs.WithLabelValues(sessionLabelValue(sessionID)).Observe(durationMs)
}

func sessionLabelValue(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}

func stringLabelValue(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}

func normalizeTransportErrorCategory(category string) string {
	value := stringLabelValue(category)
	if _, ok := validTransportErrorCategories[value]; ok {
		return value
	}

	return ErrorCategoryInternal
}
