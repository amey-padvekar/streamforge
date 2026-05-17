package auth

import (
	"errors"
	"math"
	"sync"
	"time"

	"streamforge/internal/server/session"
)

var (
	ErrNilSession              = errors.New("session is nil")
	ErrViewerNotAttached       = errors.New("viewer is not attached")
	ErrViewerNotAuthenticated  = errors.New("viewer is not authenticated")
	ErrViewerControlNotAllowed = errors.New("viewer is not allowed to send control input")
	ErrNoActiveAgent           = errors.New("no active agent for session")
	ErrSessionExpired          = errors.New("session is expired")
	ErrTokenExpired            = errors.New("session token is expired")
	ErrInputRateLimited        = errors.New("input rate limit exceeded")
)

const (
	defaultInputEventsPerSecond = 120.0
	defaultInputBurstTokens     = 240.0
)

type inputTokenBucket struct {
	tokens     float64
	lastRefill time.Time
}

type inputRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*inputTokenBucket
	perSec  float64
	burst   float64
}

var defaultLimiter = &inputRateLimiter{
	buckets: make(map[string]*inputTokenBucket),
	perSec:  defaultInputEventsPerSecond,
	burst:   defaultInputBurstTokens,
}

// SetInputRateLimitsForTesting overrides limiter settings and returns a restore function.
func SetInputRateLimitsForTesting(eventsPerSecond float64, burstTokens float64) func() {
	defaultLimiter.mu.Lock()
	prevPerSec := defaultLimiter.perSec
	prevBurst := defaultLimiter.burst
	defaultLimiter.perSec = normalizeRate(eventsPerSecond, defaultInputEventsPerSecond)
	defaultLimiter.burst = normalizeRate(burstTokens, defaultInputBurstTokens)
	defaultLimiter.buckets = make(map[string]*inputTokenBucket)
	defaultLimiter.mu.Unlock()

	return func() {
		defaultLimiter.mu.Lock()
		defaultLimiter.perSec = prevPerSec
		defaultLimiter.burst = prevBurst
		defaultLimiter.buckets = make(map[string]*inputTokenBucket)
		defaultLimiter.mu.Unlock()
	}
}

// CanSendInput returns whether viewer input should be accepted for this session.
func CanSendInput(s *session.Session, viewerID string) bool {
	if err := ValidateSessionControlState(s); err != nil {
		return false
	}

	if err := validateViewerControlState(s, viewerID); err != nil {
		return false
	}

	return true
}

// ValidateInputRate enforces per-viewer input pacing using a token bucket.
func ValidateInputRate(s *session.Session, viewerID string, now time.Time) error {
	if s == nil {
		return ErrNilSession
	}
	if viewerID == "" {
		return ErrViewerNotAttached
	}
	if now.IsZero() {
		now = time.Now()
	}

	key := s.ID + ":" + viewerID
	if !defaultLimiter.allow(key, now) {
		return ErrInputRateLimited
	}

	return nil
}

// ValidateSessionControlState checks session-level control prerequisites.
func ValidateSessionControlState(s *session.Session) error {
	if s == nil {
		return ErrNilSession
	}

	if s.State() == session.SessionStateExpired {
		return ErrSessionExpired
	}

	if s.IsTokenExpired(time.Now()) {
		return ErrTokenExpired
	}

	if !s.HasActiveAgent() {
		return ErrNoActiveAgent
	}

	return nil
}

func validateViewerControlState(s *session.Session, viewerID string) error {
	if viewerID == "" || !s.HasViewer(viewerID) {
		return ErrViewerNotAttached
	}

	viewerState, ok := s.ViewerConnectionState(viewerID)
	if !ok {
		return ErrViewerNotAttached
	}

	if viewerState != session.ConnectionStateAuthenticated && viewerState != session.ConnectionStateStreaming {
		return ErrViewerNotAuthenticated
	}

	metadata, ok := s.ViewerControlMetadata(viewerID)
	if !ok {
		return ErrViewerNotAttached
	}

	if !metadata.ControlEnabled {
		return ErrViewerControlNotAllowed
	}

	if metadata.Role != session.ViewerRoleControlEnabled && metadata.Role != session.ViewerRoleOwner {
		return ErrViewerControlNotAllowed
	}

	return nil
}

func (l *inputRateLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	bucket, exists := l.buckets[key]
	if !exists {
		bucket = &inputTokenBucket{
			tokens:     l.burst,
			lastRefill: now,
		}
		l.buckets[key] = bucket
	}

	elapsedSeconds := now.Sub(bucket.lastRefill).Seconds()
	if elapsedSeconds > 0 {
		bucket.tokens = math.Min(l.burst, bucket.tokens+(elapsedSeconds*l.perSec))
		bucket.lastRefill = now
	}

	if bucket.tokens < 1.0 {
		return false
	}

	bucket.tokens -= 1.0
	return true
}

func normalizeRate(value float64, fallback float64) float64 {
	if value <= 0 {
		return fallback
	}

	return value
}
