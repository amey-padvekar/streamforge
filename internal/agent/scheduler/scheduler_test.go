package scheduler

import (
	"log/slog"
	"testing"
)

func TestAdaptiveFPS_ReducesAndRecoversWithinBounds(t *testing.T) {
	s := &Scheduler{
		targetFPS:      20,
		currentFPS:     20,
		targetQuality:  75,
		currentQuality: 75,
		logger:         slog.Default(),
	}

	// Sustained pressure should reduce FPS after the second pressure window.
	s.adjustAdaptiveFPS(0.9, 150, 4)
	if s.currentFPS != 20 {
		t.Fatalf("fps should not reduce on first pressure window: got %d want 20", s.currentFPS)
	}
	s.adjustAdaptiveFPS(0.9, 150, 4)
	if s.currentFPS >= 20 {
		t.Fatalf("fps should reduce under sustained pressure: got %d", s.currentFPS)
	}

	// Keep applying pressure to ensure we never go below minFPS.
	for i := 0; i < 20; i++ {
		s.adjustAdaptiveFPS(0.9, 150, 4)
	}
	if s.currentFPS < minFPS {
		t.Fatalf("fps below min bound: got %d want >= %d", s.currentFPS, minFPS)
	}

	// Recovery should be gradual and never overshoot target.
	prev := s.currentFPS
	for i := 0; i < 40; i++ {
		s.adjustAdaptiveFPS(0.1, 10, 0)
		if s.currentFPS < prev {
			t.Fatalf("fps recovery should be non-decreasing: prev=%d now=%d", prev, s.currentFPS)
		}
		if s.currentFPS > s.targetFPS {
			t.Fatalf("fps recovery overshot target: got %d want <= %d", s.currentFPS, s.targetFPS)
		}
		prev = s.currentFPS
	}
	if s.currentFPS != s.targetFPS {
		t.Fatalf("fps should converge to target: got %d want %d", s.currentFPS, s.targetFPS)
	}
}

func TestAdaptiveQuality_ReducesAndRecoversWithinBounds(t *testing.T) {
	s := &Scheduler{
		targetFPS:      20,
		currentFPS:     20,
		targetQuality:  75,
		currentQuality: 75,
		logger:         slog.Default(),
	}

	// Sustained congestion should reduce quality after second window.
	s.adjustAdaptiveQuality(150, 4)
	if s.currentQuality != 75 {
		t.Fatalf("quality should not reduce on first pressure window: got %d want 75", s.currentQuality)
	}
	s.adjustAdaptiveQuality(150, 4)
	if s.currentQuality >= 75 {
		t.Fatalf("quality should reduce under sustained congestion: got %d", s.currentQuality)
	}

	// Keep applying pressure to ensure we never go below minQuality.
	for i := 0; i < 20; i++ {
		s.adjustAdaptiveQuality(150, 4)
	}
	if s.currentQuality < minQuality {
		t.Fatalf("quality below min bound: got %d want >= %d", s.currentQuality, minQuality)
	}

	// Recovery should be gradual and without overshoot.
	prev := s.currentQuality
	for i := 0; i < 80; i++ {
		s.adjustAdaptiveQuality(10, 0)
		if s.currentQuality < prev {
			t.Fatalf("quality recovery should be non-decreasing: prev=%d now=%d", prev, s.currentQuality)
		}
		if s.currentQuality > s.targetQuality {
			t.Fatalf("quality recovery overshot target: got %d want <= %d", s.currentQuality, s.targetQuality)
		}
		prev = s.currentQuality
	}
	if s.currentQuality != s.targetQuality {
		t.Fatalf("quality should converge to target: got %d want %d", s.currentQuality, s.targetQuality)
	}
}

func TestAdaptiveControls_NoOscillationSpikes(t *testing.T) {
	s := &Scheduler{
		targetFPS:      20,
		currentFPS:     20,
		targetQuality:  75,
		currentQuality: 75,
		logger:         slog.Default(),
	}

	// Pressure phase: values should be monotonic down (or stable at bounds).
	prevFPS := s.currentFPS
	prevQuality := s.currentQuality
	for i := 0; i < 10; i++ {
		s.adjustAdaptiveFPS(0.95, 200, 5)
		s.adjustAdaptiveQuality(200, 5)
		if s.currentFPS > prevFPS {
			t.Fatalf("fps spike during pressure: prev=%d now=%d", prevFPS, s.currentFPS)
		}
		if s.currentQuality > prevQuality {
			t.Fatalf("quality spike during pressure: prev=%d now=%d", prevQuality, s.currentQuality)
		}
		prevFPS = s.currentFPS
		prevQuality = s.currentQuality
	}

	// Recovery phase: values should be monotonic up (or stable at targets).
	for i := 0; i < 50; i++ {
		s.adjustAdaptiveFPS(0.05, 5, 0)
		s.adjustAdaptiveQuality(5, 0)
		if i > 0 {
			if s.currentFPS < prevFPS {
				t.Fatalf("fps dip during recovery: prev=%d now=%d", prevFPS, s.currentFPS)
			}
			if s.currentQuality < prevQuality {
				t.Fatalf("quality dip during recovery: prev=%d now=%d", prevQuality, s.currentQuality)
			}
		}
		prevFPS = s.currentFPS
		prevQuality = s.currentQuality
	}
}
