package router

import (
	"time"

	"streamforge/internal/server/metrics"
	"streamforge/internal/server/session"
)

// FanoutFrame forwards one agent frame to all viewers of the session.
// It uses non-blocking sends and records drop count for telemetry.
func FanoutFrame(s *session.Session, frame []byte) (forwarded int, dropped int) {
	if s == nil {
		return 0, 0
	}

	start := time.Now()

	forwarded, dropped = s.EnqueueFrameForViewers(frame)
	s.AddForwardedFrames(forwarded)
	s.AddDroppedFrames(dropped)
	metrics.IncFramesForwarded(s.ID, forwarded)
	metrics.IncFramesDropped(s.ID, "viewer_queue_full", dropped)
	metrics.ObserveServerRoutingLatency(s.ID, float64(time.Since(start))/float64(time.Millisecond))

	return forwarded, dropped
}
