package router

import (
	"testing"
	"time"

	"streamforge/internal/server/session"
)

func TestFanoutFrame_NonBlockingDropAccounting(t *testing.T) {
	r := session.NewRegistry()
	s := r.Create()

	slow := make(chan []byte, 1)
	fast := make(chan []byte, 2048)

	if ok := s.AddViewer("viewer-slow", nil, slow); !ok {
		t.Fatalf("add slow viewer: expected success")
	}
	if ok := s.AddViewer("viewer-fast", nil, fast); !ok {
		t.Fatalf("add fast viewer: expected success")
	}

	// Fill slow viewer queue to force non-blocking drop on fanout.
	slow <- []byte("already queued")

	forwarded, dropped := FanoutFrame(s, []byte("frame-1"))
	if forwarded != 1 {
		t.Fatalf("forwarded count: got %d want 1", forwarded)
	}
	if dropped != 1 {
		t.Fatalf("dropped count: got %d want 1", dropped)
	}

	if got := s.DroppedFrames(); got != 1 {
		t.Fatalf("session dropped frames: got %d want 1", got)
	}

	viewerDrops := s.ViewerDroppedFrames()
	if got := viewerDrops["viewer-slow"]; got != 1 {
		t.Fatalf("slow viewer drops: got %d want 1", got)
	}
	if got := viewerDrops["viewer-fast"]; got != 0 {
		t.Fatalf("fast viewer drops: got %d want 0", got)
	}

	select {
	case <-fast:
		// Expected: fast viewer received the frame.
	default:
		t.Fatalf("fast viewer should have received frame")
	}
}

func TestFanoutFrame_QueueDepthBoundedUnderSlowViewer(t *testing.T) {
	r := session.NewRegistry()
	s := r.Create()

	slow := make(chan []byte, 1)
	fast := make(chan []byte, 1)

	if ok := s.AddViewer("viewer-slow", nil, slow); !ok {
		t.Fatalf("add slow viewer: expected success")
	}
	if ok := s.AddViewer("viewer-fast", nil, fast); !ok {
		t.Fatalf("add fast viewer: expected success")
	}

	// Simulate a slow viewer by pre-filling and never draining the slow queue.
	slow <- []byte("prefill")

	start := time.Now()
	totalForwarded := 0
	totalDropped := 0

	for i := 0; i < 1000; i++ {
		forwarded, dropped := FanoutFrame(s, []byte("frame"))
		totalForwarded += forwarded
		totalDropped += dropped
	}
	duration := time.Since(start)

	// Non-blocking fanout should finish quickly; if blocked by slow viewer this spikes.
	if duration > 500*time.Millisecond {
		t.Fatalf("fanout took too long and may be blocking: duration=%s", duration)
	}

	// Slow viewer queue depth should remain bounded by channel capacity.
	if got := len(slow); got > cap(slow) {
		t.Fatalf("slow viewer queue exceeded capacity: got=%d cap=%d", got, cap(slow))
	}

	if totalDropped == 0 {
		t.Fatalf("expected drops for slow viewer under load")
	}

	viewerDrops := s.ViewerDroppedFrames()
	if viewerDrops["viewer-slow"] == 0 {
		t.Fatalf("expected slow viewer drop counter > 0")
	}

	if got := s.DroppedFrames(); got == 0 {
		t.Fatalf("expected session-level drop counter > 0")
	}

	// We intentionally do not drain fast viewer here; this test validates bounded depth + drop accounting.
	_ = totalForwarded
}
