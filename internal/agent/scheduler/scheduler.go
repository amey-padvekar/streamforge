package scheduler

import (
	"context"
	"image"
	"log/slog"
	"math"
	"strconv"
	"sync"
	"time"

	"streamforge/internal/agent/capture"
	"streamforge/internal/agent/encoder"
	agenttransport "streamforge/internal/agent/transport"
)

const (
	defaultFPS             = 20
	defaultQuality         = 75
	ewmaAlpha              = 0.2
	stageChannelCapacity   = 2     // Bounded queue capacity per stage (favor freshness)
	minFPS                 = 5     // Minimum FPS under extreme pressure
	minQuality             = 45    // Minimum JPEG quality under extreme pressure
	pressureQueueRatioHigh = 0.75  // Queue depth > 75% of capacity
	pressureDropRateHigh   = 2.0   // More than 2 drops per second
	pressureSendLatencyMs  = 100.0 // Send latency above 100ms
	fpsRecoveryFactor      = 1.02  // Slow recovery: 2% per window
	fpsPressureFactor      = 0.85  // Reduce by 15% when sustained pressure
	qualityPressureStep    = 5     // Reduce quality by 5 points under sustained pressure
	qualityRecoveryStep    = 1     // Recover quality by 1 point per healthy window
)

var latencyBucketsMs = []float64{1, 2, 5, 10, 20, 50, 100, 200, 500}

// captureFrame represents a frame at the capture stage.
type captureFrame struct {
	frameID    uint32
	img        *image.RGBA
	capturedAt time.Time
}

// encodeFrame represents a frame at the encode stage.
type encodeFrame struct {
	frameID    uint32
	jpegBytes  []byte
	width      uint16
	height     uint16
	quality    uint8
	capturedAt time.Time
	encodedAt  time.Time
}

// sendFrame represents a frame at the send stage.
type sendFrame struct {
	frameID    uint32
	data       []byte
	capturedAt time.Time
	encodedAt  time.Time
	sentAt     time.Time
}

// Scheduler drives periodic frame capture/encode/send at a target FPS.
// It uses three bounded channels to implement backpressure:
// - captureOut: bounded queue of captured frames
// - encodeOut: bounded queue of encoded frames
// - sendOut: bounded queue of frames ready to send
//
// Adaptive FPS control:
// - Monitors queue depth, send latency, and drop rate
// - Reduces FPS when sustained pressure detected
// - Slowly recovers after stabilization
type Scheduler struct {
	targetFPS      int // Configured maximum FPS target
	currentFPS     int // Current dynamic FPS (can be reduced by adapter)
	targetQuality  int // Configured maximum JPEG quality target
	currentQuality int // Current dynamic JPEG quality
	tickInterval   time.Duration
	transport      *agenttransport.WSTransport
	logger         *slog.Logger

	mu      sync.Mutex
	running bool
	stopCh  chan struct{}

	// Pipeline stage channels (bounded for backpressure)
	captureOut chan *captureFrame
	encodeOut  chan *encodeFrame
	sendOut    chan *sendFrame

	// Pipeline statistics
	captureDropped uint64
	encodeDropped  uint64
	sendDropped    uint64

	// Drop tracking for controlled telemetry
	lastCaptureDropped uint64
	lastEncodeDropped  uint64
	lastSendDropped    uint64
	lastTelemetryTime  time.Time

	// Adaptive FPS control
	sendLatencyAvgMs     float64 // EWMA of send latency
	dropRatePerSec       float64 // Current drop rate (drops per second)
	pressureSustainCount int     // How many windows with sustained pressure
	maxPressureFrames    int     // Max frames to average for drop rate window
	pressureDetected     bool    // Current pressure state

	// Adaptive quality control
	qualityPressureSustainCount int  // How many windows quality pressure is sustained
	qualityPressureDetected     bool // Current quality-pressure state

	encodeLatencyHist *latencyHistogram
	sendLatencyHist   *latencyHistogram
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
		targetFPS:         fps,
		currentFPS:        fps,
		targetQuality:     quality,
		currentQuality:    quality,
		tickInterval:      time.Second / time.Duration(fps),
		transport:         transport,
		logger:            slog.Default(),
		maxPressureFrames: (fps * 5) / 5, // Sample 5 seconds of frames
		encodeLatencyHist: newLatencyHistogram(latencyBucketsMs),
		sendLatencyHist:   newLatencyHistogram(latencyBucketsMs),
	}
}

// Start runs the capture loop until context cancellation or Stop() is called.
// It spawns three concurrent stages:
// 1. Capture stage: periodically captures frames and enqueues to captureOut
// 2. Encode stage: reads from captureOut, encodes, enqueues to encodeOut
// 3. Send stage: reads from encodeOut, sends, enqueues to sendOut
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

	// Initialize bounded channels
	s.captureOut = make(chan *captureFrame, stageChannelCapacity)
	s.encodeOut = make(chan *encodeFrame, stageChannelCapacity)
	s.sendOut = make(chan *sendFrame, stageChannelCapacity)

	// Initialize telemetry tracking
	s.lastTelemetryTime = time.Now()
	s.lastCaptureDropped = 0
	s.lastEncodeDropped = 0
	s.lastSendDropped = 0

	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		if s.stopCh == stopCh {
			s.stopCh = nil
		}
		s.running = false

		// Close channels
		close(s.captureOut)
		close(s.encodeOut)
		close(s.sendOut)

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

	// Create a done channel to coordinate stage goroutines
	stageDone := make(chan struct{})
	var wg sync.WaitGroup

	// Stage 1: Capture
	wg.Add(1)
	go s.captureStage(ctx, stopCh, &wg)

	// Stage 2: Encode
	wg.Add(1)
	go s.encodeStage(ctx, stopCh, &wg)

	// Stage 3: Send
	wg.Add(1)
	go s.sendStage(ctx, stopCh, &wg)

	// Telemetry reporter
	wg.Add(1)
	go s.telemetryReporter(ctx, stopCh, &wg)

	// Wait for all stages to complete
	wg.Wait()
	close(stageDone)
}

// captureStage periodically captures frames and enqueues them to captureOut.
// Implements drop-oldest policy when captureOut is full.
// Supports dynamic FPS adjustment by skipping frames based on targetFPS vs currentFPS.
func (s *Scheduler) captureStage(ctx context.Context, stopCh chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()

	// Ticker runs at targetFPS; we skip frames based on currentFPS
	tick := time.NewTicker(s.tickInterval)
	defer tick.Stop()

	windowStart := time.Now()
	capturedFramesInWindow := 0
	frameID := uint32(0)
	skipNextTick := false
	frameCounter := 0 // Counter for frame skipping

	for {
		select {
		case <-ctx.Done():
			s.logger.Debug("capture stage stopped by context", "reason", ctx.Err())
			return
		case <-stopCh:
			s.logger.Debug("capture stage stopped by Stop")
			return
		case <-tick.C:
			if skipNextTick {
				skipNextTick = false
				continue
			}

			s.mu.Lock()
			currentFPS := s.currentFPS
			targetFPS := s.targetFPS
			s.mu.Unlock()

			// Skip frames based on FPS ratio if currentFPS < targetFPS
			// E.g., if target=20 and current=10, skip every other frame
			if currentFPS < targetFPS {
				frameCounter++
				framesPerCapture := targetFPS / currentFPS
				if frameCounter%framesPerCapture != 0 {
					continue
				}
			}

			cycleStart := time.Now()

			img, err := capture.Capture()
			if err != nil {
				s.logger.Warn("capture failed", "error", err)
				continue
			}
			capturedFramesInWindow++

			frame := &captureFrame{
				frameID:    frameID,
				img:        img,
				capturedAt: time.Now(),
			}

			// Try to enqueue; if channel full, drop oldest and enqueue new
			select {
			case s.captureOut <- frame:
				// Successfully enqueued
			default:
				// Channel is full; drop oldest frame
				select {
				case <-s.captureOut:
					// Drop oldest
					s.captureDropped++
				default:
					// Channel is empty (shouldn't happen with capacity > 0)
				}
				// Enqueue new frame
				select {
				case s.captureOut <- frame:
					// Enqueued after drop
				default:
					// Still full (shouldn't happen after drop), skip this frame
					s.logger.Warn("capture stage: could not enqueue after drop")
				}
			}

			frameID++

			windowElapsed := time.Since(windowStart)
			if windowElapsed >= time.Second {
				actualCaptureFPS := float64(capturedFramesInWindow) / windowElapsed.Seconds()
				s.logger.Debug("capture stage FPS", "actualFPS", roundTo2(actualCaptureFPS), "targetFPS", currentFPS)
				capturedFramesInWindow = 0
				windowStart = time.Now()
			}

			if time.Since(cycleStart) > s.tickInterval {
				skipNextTick = true
			}
		}
	}
}

// encodeStage reads from captureOut, encodes JPEG, and enqueues to encodeOut.
// Implements drop-oldest policy when encodeOut is full.
func (s *Scheduler) encodeStage(ctx context.Context, stopCh chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()

	for {
		select {
		case <-ctx.Done():
			s.logger.Debug("encode stage stopped by context", "role", "agent", "reason", ctx.Err())
			return
		case <-stopCh:
			s.logger.Debug("encode stage stopped by Stop", "role", "agent")
			return
		case frame, ok := <-s.captureOut:
			if !ok {
				s.logger.Debug("encode stage: captureOut closed")
				return
			}

			s.mu.Lock()
			quality := s.currentQuality
			s.mu.Unlock()

			encodeStart := time.Now()
			jpegBytes, err := encoder.EncodeJPEG(frame.img, quality)
			if err != nil {
				s.logger.Warn("jpeg encode failed", "role", "agent", "frameId", frame.frameID, "errorCategory", "internal", "error", err)
				continue
			}
			encodedAt := time.Now()

			bounds := frame.img.Bounds()
			width := bounds.Dx()
			height := bounds.Dy()
			if width <= 0 || height <= 0 || width > 0xFFFF || height > 0xFFFF {
				s.logger.Warn("invalid frame dimensions", "width", width, "height", height)
				continue
			}

			encodedFrame := &encodeFrame{
				frameID:    frame.frameID,
				jpegBytes:  jpegBytes,
				width:      uint16(width),
				height:     uint16(height),
				quality:    uint8(quality),
				capturedAt: frame.capturedAt,
				encodedAt:  encodedAt,
			}

			// Try to enqueue; if channel full, drop oldest and enqueue new
			select {
			case s.encodeOut <- encodedFrame:
				// Successfully enqueued
			default:
				// Channel is full; drop oldest frame
				select {
				case <-s.encodeOut:
					// Drop oldest
					s.encodeDropped++
				default:
					// Channel is empty (shouldn't happen with capacity > 0)
				}
				// Enqueue new frame
				select {
				case s.encodeOut <- encodedFrame:
					// Enqueued after drop
				default:
					// Still full (shouldn't happen after drop), skip this frame
					s.logger.Warn("encode stage: could not enqueue after drop", "role", "agent", "frameId", frame.frameID, "queueDepth", len(s.encodeOut), "framesDropped", s.encodeDropped, "errorCategory", "backpressure")
				}
			}

			encodeDurationMs := float64(time.Since(encodeStart)) / float64(time.Millisecond)
			s.encodeLatencyHist.Observe(encodeDurationMs)
			s.logger.Debug("encode stage", "role", "agent", "frameId", frame.frameID, "packetType", "frame", "encodeMs", roundTo2(encodeDurationMs), "quality", quality)
		}
	}
}

// sendStage reads from encodeOut, constructs transport frame, and sends.
// Implements drop-oldest policy when sendOut is full.
// Tracks send latency for adaptive FPS control.
func (s *Scheduler) sendStage(ctx context.Context, stopCh chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()

	for {
		select {
		case <-ctx.Done():
			s.logger.Debug("send stage stopped by context", "role", "agent", "reason", ctx.Err())
			return
		case <-stopCh:
			s.logger.Debug("send stage stopped by Stop", "role", "agent")
			return
		case encodedFrame, ok := <-s.encodeOut:
			if !ok {
				s.logger.Debug("send stage: encodeOut closed")
				return
			}

			sendStart := time.Now()
			frame := agenttransport.EncodeFrame(
				encodedFrame.frameID,
				encodedFrame.width,
				encodedFrame.height,
				encodedFrame.quality,
				encodedFrame.jpegBytes,
			)
			if err := s.transport.Send(frame); err != nil {
				s.logger.Warn("frame send failed", "role", "agent", "frameId", encodedFrame.frameID, "packetType", "frame", "queueDepth", len(s.sendOut), "framesDropped", s.sendDropped, "errorCategory", "transport", "error", err)
				continue
			}
			sentAt := time.Now()
			sendLatencyMs := float64(time.Since(sendStart)) / float64(time.Millisecond)
			s.sendLatencyHist.Observe(sendLatencyMs)

			// Update EWMA of send latency
			s.mu.Lock()
			s.sendLatencyAvgMs = updateEWMA(s.sendLatencyAvgMs, sendLatencyMs)
			s.mu.Unlock()

			sentFrame := &sendFrame{
				frameID:    encodedFrame.frameID,
				data:       frame,
				capturedAt: encodedFrame.capturedAt,
				encodedAt:  encodedFrame.encodedAt,
				sentAt:     sentAt,
			}

			// Try to enqueue; if channel full, drop oldest and enqueue new
			select {
			case s.sendOut <- sentFrame:
				// Successfully enqueued
			default:
				// Channel is full; drop oldest frame
				select {
				case <-s.sendOut:
					// Drop oldest
					s.sendDropped++
				default:
					// Channel is empty (shouldn't happen with capacity > 0)
				}
				// Enqueue new frame
				select {
				case s.sendOut <- sentFrame:
					// Enqueued after drop
				default:
					// Still full (shouldn't happen after drop), skip tracking
					s.logger.Warn("send stage: could not enqueue after drop", "role", "agent", "frameId", encodedFrame.frameID, "queueDepth", len(s.sendOut), "framesDropped", s.sendDropped, "errorCategory", "backpressure")
				}
			}

			s.logger.Debug("send stage", "role", "agent", "frameId", encodedFrame.frameID, "packetType", "frame", "sendMs", roundTo2(sendLatencyMs))
		}
	}
}

// telemetryReporter periodically logs pipeline statistics and drop metrics.
// Logs at controlled intervals (every 5 seconds) to avoid per-frame spam.
// Reports drop counts and rates when pressure is detected.
func (s *Scheduler) telemetryReporter(ctx context.Context, stopCh chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()

	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Debug("telemetry reporter stopped by context", "reason", ctx.Err())
			return
		case <-stopCh:
			s.logger.Debug("telemetry reporter stopped by Stop")
			return
		case <-tick.C:
			sessionID := "unknown"
			if s.transport != nil {
				sessionID = s.transport.SessionID()
			}

			now := time.Now()
			elapsedSec := now.Sub(s.lastTelemetryTime).Seconds()
			if elapsedSec <= 0 {
				elapsedSec = 1
			}

			// Calculate drop deltas and rates
			captureDeltaDrops := s.captureDropped - s.lastCaptureDropped
			encodeDeltaDrops := s.encodeDropped - s.lastEncodeDropped
			sendDeltaDrops := s.sendDropped - s.lastSendDropped

			captureDropRate := float64(captureDeltaDrops) / elapsedSec
			encodeDropRate := float64(encodeDeltaDrops) / elapsedSec
			sendDropRate := float64(sendDeltaDrops) / elapsedSec
			totalDropRate := captureDropRate + encodeDropRate + sendDropRate
			s.dropRatePerSec = totalDropRate

			// Update last seen values
			s.lastCaptureDropped = s.captureDropped
			s.lastEncodeDropped = s.encodeDropped
			s.lastSendDropped = s.sendDropped
			s.lastTelemetryTime = now

			// Build telemetry log with queue depths
			captureQueueDepth := len(s.captureOut)
			encodeQueueDepth := len(s.encodeOut)
			sendQueueDepth := len(s.sendOut)
			queueRatio := float64(captureQueueDepth+encodeQueueDepth+sendQueueDepth) / float64(stageChannelCapacity*3)

			s.adjustAdaptiveFPS(queueRatio, s.sendLatencyAvgMs, totalDropRate)
			s.adjustAdaptiveQuality(s.sendLatencyAvgMs, totalDropRate)

			// Log main telemetry with all metrics
			s.logger.Info(
				"scheduler pipeline telemetry",
				"sessionId", sessionID,
				"role", "agent",
				"targetFPS", s.targetFPS,
				"currentFPS", s.currentFPS,
				"targetQuality", s.targetQuality,
				"currentQuality", s.currentQuality,
				"captureQueueDepth", captureQueueDepth,
				"encodeQueueDepth", encodeQueueDepth,
				"sendQueueDepth", sendQueueDepth,
				"queueDepth", captureQueueDepth+encodeQueueDepth+sendQueueDepth,
				"queueDepthRatio", roundTo2(queueRatio),
				"captureDropped", s.captureDropped,
				"encodeDropped", s.encodeDropped,
				"sendDropped", s.sendDropped,
				"framesDropped", s.captureDropped+s.encodeDropped+s.sendDropped,
				"captureDropRate", roundTo2(captureDropRate),
				"encodeDropRate", roundTo2(encodeDropRate),
				"sendDropRate", roundTo2(sendDropRate),
				"sendLatencyAvgMs", roundTo2(s.sendLatencyAvgMs),
				"encodeLatencyHistogram", s.encodeLatencyHist.Snapshot(),
				"sendLatencyHistogram", s.sendLatencyHist.Snapshot(),
				"pressureDetected", s.pressureDetected,
				"pressureSustainCount", s.pressureSustainCount,
				"qualityPressureDetected", s.qualityPressureDetected,
				"qualityPressureSustainCount", s.qualityPressureSustainCount,
			)

			// Log backpressure warning if drops detected in any stage
			if captureDeltaDrops > 0 {
				s.logger.Warn(
					"capture stage backpressure detected",
					"sessionId", sessionID,
					"role", "agent",
					"dropsInWindow", captureDeltaDrops,
					"framesDropped", s.captureDropped+s.encodeDropped+s.sendDropped,
					"dropRate", roundTo2(captureDropRate),
					"queueDepth", captureQueueDepth,
					"reason", "encode stage may be slow",
					"errorCategory", "backpressure",
				)
			}
			if encodeDeltaDrops > 0 {
				s.logger.Warn(
					"encode stage backpressure detected",
					"sessionId", sessionID,
					"role", "agent",
					"dropsInWindow", encodeDeltaDrops,
					"framesDropped", s.captureDropped+s.encodeDropped+s.sendDropped,
					"dropRate", roundTo2(encodeDropRate),
					"queueDepth", encodeQueueDepth,
					"reason", "send stage may be slow",
					"errorCategory", "backpressure",
				)
			}
			if sendDeltaDrops > 0 {
				s.logger.Warn(
					"send stage backpressure detected",
					"sessionId", sessionID,
					"role", "agent",
					"dropsInWindow", sendDeltaDrops,
					"framesDropped", s.captureDropped+s.encodeDropped+s.sendDropped,
					"dropRate", roundTo2(sendDropRate),
					"queueDepth", sendQueueDepth,
					"reason", "transport or network congestion",
					"errorCategory", "backpressure",
				)
			}
		}
	}
}

type latencyHistogram struct {
	buckets []float64
	counts  []uint64
	mu      sync.Mutex
}

func newLatencyHistogram(buckets []float64) *latencyHistogram {
	b := make([]float64, len(buckets))
	copy(b, buckets)

	return &latencyHistogram{
		buckets: b,
		counts:  make([]uint64, len(b)+1),
	}
}

func (h *latencyHistogram) Observe(valueMs float64) {
	if h == nil {
		return
	}
	if valueMs < 0 {
		valueMs = 0
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	for idx, upperBound := range h.buckets {
		if valueMs <= upperBound {
			h.counts[idx]++
			return
		}
	}

	h.counts[len(h.counts)-1]++
}

func (h *latencyHistogram) Snapshot() map[string]uint64 {
	if h == nil {
		return map[string]uint64{}
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	out := make(map[string]uint64, len(h.counts))
	for idx, upperBound := range h.buckets {
		out["le_"+formatBucket(upperBound)+"ms"] = h.counts[idx]
	}
	out["gt_"+formatBucket(h.buckets[len(h.buckets)-1])+"ms"] = h.counts[len(h.counts)-1]

	return out
}

func formatBucket(value float64) string {
	if value == math.Trunc(value) {
		return strconv.FormatInt(int64(value), 10)
	}

	return strconv.FormatFloat(value, 'f', -1, 64)
}

func updateEWMA(current, sample float64) float64 {
	if current <= 0 {
		return sample
	}
	return ewmaAlpha*sample + (1-ewmaAlpha)*current
}

func roundTo2(v float64) float64 {
	return math.Round(v*100) / 100
}

// adjustAdaptiveFPS modifies currentFPS based on sustained pressure signals.
// Pressure inputs:
// - queueRatio: average queue fill ratio across pipeline stages
// - sendLatencyMs: EWMA send latency
// - dropRatePerSec: total drops/sec across stages
func (s *Scheduler) adjustAdaptiveFPS(queueRatio, sendLatencyMs, dropRatePerSec float64) {
	pressureNow := queueRatio >= pressureQueueRatioHigh ||
		sendLatencyMs >= pressureSendLatencyMs ||
		dropRatePerSec >= pressureDropRateHigh

	if pressureNow {
		s.pressureSustainCount++
		s.pressureDetected = true
		if s.pressureSustainCount >= 2 {
			newFPS := int(float64(s.currentFPS) * fpsPressureFactor)
			if newFPS < minFPS {
				newFPS = minFPS
			}
			if newFPS < s.currentFPS {
				s.logger.Info(
					"adaptive FPS reduced",
					"previousFPS", s.currentFPS,
					"newFPS", newFPS,
					"queueDepthRatio", roundTo2(queueRatio),
					"sendLatencyAvgMs", roundTo2(sendLatencyMs),
					"dropRatePerSec", roundTo2(dropRatePerSec),
				)
				s.currentFPS = newFPS
			}
			// Keep sustain counter from growing unbounded.
			s.pressureSustainCount = 2
		}
		return
	}

	// Recovery path when pressure has stabilized.
	s.pressureDetected = false
	if s.pressureSustainCount > 0 {
		s.pressureSustainCount--
	}

	if s.currentFPS < s.targetFPS {
		recovered := int(math.Ceil(float64(s.currentFPS) * fpsRecoveryFactor))
		if recovered > s.targetFPS {
			recovered = s.targetFPS
		}
		if recovered > s.currentFPS {
			s.logger.Info(
				"adaptive FPS recovered",
				"previousFPS", s.currentFPS,
				"newFPS", recovered,
			)
			s.currentFPS = recovered
		}
	}
}

// adjustAdaptiveQuality modifies currentQuality based on send congestion signals.
// Inputs:
// - sendLatencyMs: EWMA send latency
// - dropRatePerSec: total drops/sec across stages
// Behavior:
// - reduces quality in steps under sustained congestion
// - recovers quality gradually when healthy
func (s *Scheduler) adjustAdaptiveQuality(sendLatencyMs, dropRatePerSec float64) {
	qualityPressureNow := sendLatencyMs >= pressureSendLatencyMs || dropRatePerSec >= pressureDropRateHigh

	if qualityPressureNow {
		s.qualityPressureSustainCount++
		s.qualityPressureDetected = true
		if s.qualityPressureSustainCount >= 2 {
			newQuality := s.currentQuality - qualityPressureStep
			if newQuality < minQuality {
				newQuality = minQuality
			}
			if newQuality < s.currentQuality {
				s.logger.Info(
					"adaptive quality reduced",
					"previousQuality", s.currentQuality,
					"newQuality", newQuality,
					"sendLatencyAvgMs", roundTo2(sendLatencyMs),
					"dropRatePerSec", roundTo2(dropRatePerSec),
				)
				s.currentQuality = newQuality
			}
			// Keep sustain counter bounded.
			s.qualityPressureSustainCount = 2
		}
		return
	}

	// Recovery path when congestion has stabilized.
	s.qualityPressureDetected = false
	if s.qualityPressureSustainCount > 0 {
		s.qualityPressureSustainCount--
	}

	if s.currentQuality < s.targetQuality {
		recovered := s.currentQuality + qualityRecoveryStep
		if recovered > s.targetQuality {
			recovered = s.targetQuality
		}
		if recovered > s.currentQuality {
			s.logger.Info(
				"adaptive quality recovered",
				"previousQuality", s.currentQuality,
				"newQuality", recovered,
			)
			s.currentQuality = recovered
		}
	}
}

// CurrentFPS returns the current adaptive FPS.
func (s *Scheduler) CurrentFPS() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentFPS
}

// CurrentQuality returns the current adaptive JPEG quality.
func (s *Scheduler) CurrentQuality() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentQuality
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

// DropStats returns the current drop counters and queue depths for monitoring.
// Safe to call from any goroutine.
type DropStats struct {
	CaptureDropped    uint64
	EncodeDropped     uint64
	SendDropped       uint64
	TotalDropped      uint64
	CaptureQueueDepth int
	EncodeQueueDepth  int
	SendQueueDepth    int
}

// GetDropStats returns current pipeline drop statistics and queue depths.
func (s *Scheduler) GetDropStats() DropStats {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return DropStats{}
	}

	total := s.captureDropped + s.encodeDropped + s.sendDropped

	return DropStats{
		CaptureDropped:    s.captureDropped,
		EncodeDropped:     s.encodeDropped,
		SendDropped:       s.sendDropped,
		TotalDropped:      total,
		CaptureQueueDepth: len(s.captureOut),
		EncodeQueueDepth:  len(s.encodeOut),
		SendQueueDepth:    len(s.sendOut),
	}
}
