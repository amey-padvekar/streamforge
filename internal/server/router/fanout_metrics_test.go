package router

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"streamforge/internal/server/metrics"
	"streamforge/internal/server/session"
)

func TestFanoutFrame_RecordsRoutingHistogramUnderLoad(t *testing.T) {
	prometheus := metrics.NewPrometheus()
	metrics.SetDefault(prometheus)
	defer metrics.SetDefault(nil)

	registry := session.NewRegistry()
	s := registry.Create()

	fast := make(chan []byte, 4096)
	if ok := s.AddViewer("viewer-fast", nil, fast); !ok {
		t.Fatalf("add fast viewer: expected success")
	}

	for i := 0; i < 250; i++ {
		FanoutFrame(s, []byte("frame-"+strconv.Itoa(i)))
	}

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	prometheus.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("metrics status: got %d want %d", rr.Code, http.StatusOK)
	}

	body := rr.Body.String()
	if !strings.Contains(body, "streamforge_server_routing_latency_ms_bucket") {
		t.Fatalf("routing histogram buckets missing from scrape output")
	}

	expectedCountLine := regexp.MustCompile(`streamforge_server_routing_latency_ms_count\{session_id="` + regexp.QuoteMeta(s.ID) + `"\}\s+([0-9]+)`) //nolint:lll
	matches := expectedCountLine.FindStringSubmatch(body)
	if len(matches) != 2 {
		t.Fatalf("routing histogram count for session not found in scrape output")
	}

	count, err := strconv.Atoi(matches[1])
	if err != nil {
		t.Fatalf("parse routing histogram count: %v", err)
	}
	if count <= 0 {
		t.Fatalf("routing histogram count should be > 0 after induced fanout load, got %d", count)
	}
}
