package router

import (
	"errors"
	"time"

	"streamforge/internal/server/metrics"
	"streamforge/internal/server/session"
)

var (
	ErrNoActiveAgent    = errors.New("no active agent available for input routing")
	ErrInputBackpressure = errors.New("agent input queue is full")
)

// RouteInput forwards one viewer INPUT packet toward the active agent queue.
// The forwarding path is non-blocking to keep frame fanout independent from input pressure.
func RouteInput(s *session.Session, inputPacket []byte) error {
	if s == nil {
		return ErrNoActiveAgent
	}

	start := time.Now()
	enqueued, dropped := s.EnqueueInputForAgent(inputPacket)
	metrics.ObserveServerRoutingLatency(s.ID, float64(time.Since(start))/float64(time.Millisecond))

	if dropped {
		metrics.IncTransportErrors("server", metrics.ErrorCategoryBackpressure)
		return ErrInputBackpressure
	}
	if !enqueued {
		metrics.IncTransportErrors("server", metrics.ErrorCategoryTransport)
		return ErrNoActiveAgent
	}

	return nil
}
