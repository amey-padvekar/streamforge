package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPrometheusHandler_ScrapeExposesMetricFamilies(t *testing.T) {
	p := NewPrometheus()
	SetDefault(p)
	defer SetDefault(nil)

	IncFramesReceived("session-test", 1)
	IncFramesForwarded("session-test", 1)
	IncFramesDropped("session-test", "viewer_queue_full", 1)
	SetViewersConnected("session-test", 1)
	SetSessionFPS("session-test", 12.5)
	IncTransportErrors("server", ErrorCategoryProtocol)
	ObserveServerRoutingLatency("session-test", 3)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	p.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("metrics status: got %d want %d", rr.Code, http.StatusOK)
	}

	body := rr.Body.String()
	requiredFamilies := []string{
		"streamforge_frames_received_total",
		"streamforge_frames_forwarded_total",
		"streamforge_frames_dropped_total",
		"streamforge_viewers_connected",
		"streamforge_session_fps",
		"streamforge_transport_errors_total",
		"streamforge_server_routing_latency_ms",
	}

	for _, metricName := range requiredFamilies {
		if !strings.Contains(body, metricName) {
			t.Fatalf("missing metric family in scrape output: %s", metricName)
		}
	}
}
