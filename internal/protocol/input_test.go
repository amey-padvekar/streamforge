package protocol

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestEncodeDecodeInput_RoundTripPerEventType(t *testing.T) {
	tests := []struct {
		name string
		in   InputEnvelope
	}{
		{
			name: "mouse move",
			in: InputEnvelope{
				EventType:   InputEventMouseMove,
				EventID:     1,
				TimestampNs: 100,
				ViewerID:    "viewer-1",
				Mouse: &MousePayload{
					XNorm:       0.25,
					YNorm:       0.75,
					Button:      MouseButtonNone,
					ButtonsMask: 0,
				},
			},
		},
		{
			name: "mouse down",
			in: InputEnvelope{
				EventType:   InputEventMouseDown,
				EventID:     2,
				TimestampNs: 101,
				ViewerID:    "viewer-1",
				Mouse: &MousePayload{
					XNorm:       0.5,
					YNorm:       0.5,
					Button:      MouseButtonLeft,
					ButtonsMask: MouseButtonsMaskLeft,
				},
			},
		},
		{
			name: "mouse up",
			in: InputEnvelope{
				EventType:   InputEventMouseUp,
				EventID:     3,
				TimestampNs: 102,
				ViewerID:    "viewer-1",
				Mouse: &MousePayload{
					XNorm:       0.5,
					YNorm:       0.5,
					Button:      MouseButtonLeft,
					ButtonsMask: 0,
				},
			},
		},
		{
			name: "mouse wheel",
			in: InputEnvelope{
				EventType:   InputEventMouseWheel,
				EventID:     4,
				TimestampNs: 103,
				ViewerID:    "viewer-1",
				Wheel: &WheelPayload{
					DeltaX: 2.5,
					DeltaY: -1.5,
				},
			},
		},
		{
			name: "key down",
			in: InputEnvelope{
				EventType:   InputEventKeyDown,
				EventID:     5,
				TimestampNs: 104,
				ViewerID:    "viewer-1",
				Key: &KeyPayload{
					Code:      "KeyA",
					Key:       "a",
					Modifiers: KeyModifierCtrl | KeyModifierShift,
				},
			},
		},
		{
			name: "key up",
			in: InputEnvelope{
				EventType:   InputEventKeyUp,
				EventID:     6,
				TimestampNs: 105,
				ViewerID:    "viewer-1",
				Key: &KeyPayload{
					Code:      "Enter",
					Key:       "Enter",
					Modifiers: 0,
				},
			},
		},
		{
			name: "resize hint",
			in: InputEnvelope{
				EventType:   InputEventResizeHint,
				EventID:     7,
				TimestampNs: 106,
				ViewerID:    "viewer-1",
				Display: &DisplayPayload{
					TargetMonitorID: "monitor-1",
					ViewportWidth:   1280,
					ViewportHeight:  720,
				},
			},
		},
		{
			name: "monitor select",
			in: InputEnvelope{
				EventType:   InputEventMonitorSelect,
				EventID:     8,
				TimestampNs: 107,
				ViewerID:    "viewer-1",
				Display: &DisplayPayload{
					TargetMonitorID: "monitor-2",
					ViewportWidth:   1920,
					ViewportHeight:  1080,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload, err := EncodeInput(tt.in)
			if err != nil {
				t.Fatalf("EncodeInput failed: %v", err)
			}

			got, err := DecodeInput(payload)
			if err != nil {
				t.Fatalf("DecodeInput failed: %v", err)
			}

			if !reflect.DeepEqual(got, tt.in) {
				t.Fatalf("input mismatch: got %+v want %+v", got, tt.in)
			}
		})
	}
}

func TestEncodeInput_UnknownEventRejected(t *testing.T) {
	in := InputEnvelope{
		EventType:   InputEventType(0xFF),
		EventID:     10,
		TimestampNs: 200,
		ViewerID:    "viewer-unknown",
		Mouse: &MousePayload{
			XNorm:       0.5,
			YNorm:       0.5,
			Button:      MouseButtonNone,
			ButtonsMask: 0,
		},
	}

	_, err := EncodeInput(in)
	if err == nil {
		t.Fatalf("expected error for unknown event type")
	}
	if !errors.Is(err, ErrUnknownPacketType) {
		t.Fatalf("expected ErrUnknownPacketType, got %v", err)
	}
}

func TestDecodeInput_UnknownEventRejected(t *testing.T) {
	raw := map[string]any{
		"eventType":   255,
		"eventId":     11,
		"timestampNs": 201,
		"viewerId":    "viewer-unknown",
		"mouse": map[string]any{
			"xNorm":       0.5,
			"yNorm":       0.5,
			"button":      int(MouseButtonNone),
			"buttonsMask": 0,
		},
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	_, err = DecodeInput(payload)
	if err == nil {
		t.Fatalf("expected error for unknown event type")
	}
	if !errors.Is(err, ErrUnknownPacketType) {
		t.Fatalf("expected ErrUnknownPacketType, got %v", err)
	}
}

func TestEncodeInput_MalformedCoordinatesRejected(t *testing.T) {
	base := InputEnvelope{
		EventType:   InputEventMouseMove,
		EventID:     12,
		TimestampNs: 202,
		ViewerID:    "viewer-coords",
		Mouse: &MousePayload{
			XNorm:       0.4,
			YNorm:       0.6,
			Button:      MouseButtonNone,
			ButtonsMask: 0,
		},
	}

	tests := []struct {
		name  string
		xNorm float64
		yNorm float64
	}{
		{name: "x NaN", xNorm: math.NaN(), yNorm: 0.4},
		{name: "y +Inf", xNorm: 0.4, yNorm: math.Inf(1)},
		{name: "x below range", xNorm: -0.01, yNorm: 0.4},
		{name: "y above range", xNorm: 0.4, yNorm: 1.01},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := base
			in.Mouse = &MousePayload{
				XNorm:       tt.xNorm,
				YNorm:       tt.yNorm,
				Button:      MouseButtonNone,
				ButtonsMask: 0,
			}

			_, err := EncodeInput(in)
			if err == nil {
				t.Fatalf("expected coordinate validation error")
			}
			if !errors.Is(err, ErrLengthMismatch) {
				t.Fatalf("expected ErrLengthMismatch, got %v", err)
			}
		})
	}
}

func TestEncodeInput_KeyPayloadValidation(t *testing.T) {
	in := InputEnvelope{
		EventType:   InputEventKeyDown,
		EventID:     13,
		TimestampNs: 203,
		ViewerID:    "viewer-keys",
		Key: &KeyPayload{
			Code:      "   ",
			Key:       "",
			Modifiers: 0,
		},
	}

	_, err := EncodeInput(in)
	if err == nil {
		t.Fatalf("expected key validation error")
	}
	if !errors.Is(err, ErrLengthMismatch) {
		t.Fatalf("expected ErrLengthMismatch, got %v", err)
	}
}

func TestInputPayloadLengthBounds(t *testing.T) {
	t.Run("decode rejects oversized payload", func(t *testing.T) {
		oversized := make([]byte, MaxPayloadBytes+1)
		_, err := DecodeInput(oversized)
		if err == nil {
			t.Fatalf("expected oversized payload error")
		}
		if !errors.Is(err, ErrPayloadTooLarge) {
			t.Fatalf("expected ErrPayloadTooLarge, got %v", err)
		}
	})

	t.Run("encode rejects oversized payload", func(t *testing.T) {
		in := InputEnvelope{
			EventType:   InputEventKeyDown,
			EventID:     14,
			TimestampNs: 204,
			ViewerID:    "viewer-large",
			Key: &KeyPayload{
				Code:      "KeyA",
				Key:       strings.Repeat("x", MaxPayloadBytes),
				Modifiers: 0,
			},
		}

		_, err := EncodeInput(in)
		if err == nil {
			t.Fatalf("expected oversized payload error")
		}
		if !errors.Is(err, ErrPayloadTooLarge) {
			t.Fatalf("expected ErrPayloadTooLarge, got %v", err)
		}
	})
}
