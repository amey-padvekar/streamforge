package router

import "streamforge/internal/server/session"

// FanoutFrame forwards one agent frame to all viewers of the session.
// It uses non-blocking sends and records drop count for telemetry.
func FanoutFrame(s *session.Session, frame []byte) (forwarded int, dropped int) {
	if s == nil {
		return 0, 0
	}

	forwarded, dropped = s.EnqueueFrameForViewers(frame)
	s.AddDroppedFrames(dropped)

	return forwarded, dropped
}
