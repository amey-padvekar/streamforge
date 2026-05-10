package scheduler

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"streamforge/internal/agent/capture"
	"streamforge/internal/agent/encoder"
	agenttransport "streamforge/internal/agent/transport"
)

const (
	defaultFPS     = 20
	defaultQuality = 75
)

// Scheduler drives periodic frame capture/encode/send at a target FPS.
type Scheduler struct {
	fps          int
	quality      int
	tickInterval time.Duration
	transport    *agenttransport.WSTransport
	logger       *slog.Logger

	mu      sync.Mutex
	running bool
	stopCh  chan struct{}
}

// NewScheduler constructs a scheduler for the capture pipeline.
func NewScheduler(fps int, quality int, transport *agenttransport.WSTransport) *Scheduler {
	if fps <= 0 {
		fps = defaultFPS
	}
	if quality < 1 || quality > 100 {
		quality = defaultQuality
	}

	return &Scheduler{
		fps:          fps,
		quality:      quality,
		tickInterval: time.Second / time.Duration(fps),
		transport:    transport,
		logger:       slog.Default(),
	}
}

// Start runs the capture loop until context cancellation or Stop() is called.
func (s *Scheduler) Start(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}

	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		s.logger.Warn("scheduler already running")
		return
	}
	s.running = true
	stopCh := make(chan struct{})
	s.stopCh = stopCh
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		if s.stopCh == stopCh {
			s.stopCh = nil
		}
		s.running = false
		s.mu.Unlock()
	}()

	if s.transport == nil {
		s.logger.Error("scheduler transport is nil")
		return
	}

	if err := s.transport.Connect(); err != nil {
		s.logger.Error("scheduler failed to connect transport", "error", err)
		return
	}
	defer func() {
		if err := s.transport.Close(); err != nil {
			s.logger.Warn("scheduler transport close failed", "error", err)
		}
	}()

	tick := time.NewTicker(s.tickInterval)
	defer tick.Stop()

	fpsLogTick := time.NewTicker(5 * time.Second)
	defer fpsLogTick.Stop()

	windowStart := time.Now()
	framesInWindow := 0
	actualFPS := 0.0
	frameID := uint32(0)
	skipNextTick := false

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("scheduler stopped by context", "reason", ctx.Err())
			return
		case <-stopCh:
			s.logger.Info("scheduler stopped by Stop")
			return
		case <-fpsLogTick.C:
			s.logger.Info("scheduler fps", "targetFPS", s.fps, "actualFPS", actualFPS)
		case <-tick.C:
			if skipNextTick {
				skipNextTick = false
				continue
			}

			cycleStart := time.Now()

			img, err := capture.Capture()
			if err != nil {
				s.logger.Warn("capture failed", "error", err)
				continue
			}

			jpegBytes, err := encoder.EncodeJPEG(img, s.quality)
			if err != nil {
				s.logger.Warn("jpeg encode failed", "error", err)
				continue
			}

			bounds := img.Bounds()
			width := bounds.Dx()
			height := bounds.Dy()
			if width <= 0 || height <= 0 || width > 0xFFFF || height > 0xFFFF {
				s.logger.Warn("invalid frame dimensions", "width", width, "height", height)
				continue
			}

			frame := agenttransport.EncodeFrame(
				frameID,
				uint16(width),
				uint16(height),
				uint8(s.quality),
				jpegBytes,
			)
			if err := s.transport.Send(frame); err != nil {
				s.logger.Warn("frame send failed", "error", err, "frameID", frameID)
				continue
			}

			frameID++
			framesInWindow++

			windowElapsed := time.Since(windowStart)
			if windowElapsed >= time.Second {
				actualFPS = float64(framesInWindow) / windowElapsed.Seconds()
				framesInWindow = 0
				windowStart = time.Now()
			}

			if time.Since(cycleStart) > s.tickInterval {
				skipNextTick = true
			}
		}
	}
}

// Stop requests scheduler shutdown.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	stopCh := s.stopCh
	s.stopCh = nil
	s.mu.Unlock()

	if stopCh != nil {
		close(stopCh)
	}
}
