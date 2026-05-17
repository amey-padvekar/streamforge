package protocol

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"strconv"
	"strings"
)

// MouseButton identifies a mouse button for mouse down/up events.
type MouseButton uint8

const (
	MouseButtonNone   MouseButton = 0
	MouseButtonLeft   MouseButton = 1
	MouseButtonRight  MouseButton = 2
	MouseButtonMiddle MouseButton = 3
)

// MouseButtonsMask is a bitmask of currently pressed mouse buttons.
type MouseButtonsMask uint8

const (
	MouseButtonsMaskLeft MouseButtonsMask = 1 << iota
	MouseButtonsMaskRight
	MouseButtonsMaskMiddle
)

// KeyModifiers is a bitmask of active keyboard modifiers.
type KeyModifiers uint8

const (
	KeyModifierCtrl KeyModifiers = 1 << iota
	KeyModifierAlt
	KeyModifierShift
	KeyModifierMeta
)

// InputEnvelope is the canonical INPUT payload wrapper shared across all input events.
//
// EventID is monotonic per viewer session and is used for ordering diagnostics.
type InputEnvelope struct {
	EventType   InputEventType `json:"eventType"`
	EventID     uint64         `json:"eventId"`
	TimestampNs uint64         `json:"timestampNs"`
	ViewerID    string         `json:"viewerId"`

	Mouse   *MousePayload   `json:"mouse,omitempty"`
	Wheel   *WheelPayload   `json:"wheel,omitempty"`
	Key     *KeyPayload     `json:"key,omitempty"`
	Display *DisplayPayload `json:"display,omitempty"`
}

// MousePayload carries normalized pointer coordinates and button state.
type MousePayload struct {
	XNorm       float64          `json:"xNorm"`
	YNorm       float64          `json:"yNorm"`
	Button      MouseButton      `json:"button"`
	ButtonsMask MouseButtonsMask `json:"buttonsMask"`
}

// WheelPayload carries horizontal and vertical wheel deltas.
type WheelPayload struct {
	DeltaX float64 `json:"deltaX"`
	DeltaY float64 `json:"deltaY"`
}

// KeyPayload carries logical and human-readable key data with modifier state.
type KeyPayload struct {
	Code      string       `json:"code"`
	Key       string       `json:"key"`
	Modifiers KeyModifiers `json:"modifiers"`
}

// DisplayPayload carries monitor selection and viewport metadata.
type DisplayPayload struct {
	TargetMonitorID string `json:"targetMonitorId"`
	ViewportWidth   uint32 `json:"viewportWidth"`
	ViewportHeight  uint32 `json:"viewportHeight"`
}

// EncodeInput encodes an InputEnvelope into binary payload bytes.
func EncodeInput(in InputEnvelope) ([]byte, error) {
	if err := validateInputEnvelope(in); err != nil {
		return nil, err
	}

	payload, err := json.Marshal(in)
	if err != nil {
		return nil, NewParseError(ErrLengthMismatch, ParseValidation{
			Field:      "INPUT payload",
			Expected:   "valid JSON",
			Actual:     err.Error(),
			PacketType: PacketTypeInput,
		})
	}

	if len(payload) > MaxPayloadBytes {
		return nil, NewParseError(ErrPayloadTooLarge, ParseValidation{
			Field:      "INPUT payload length",
			Expected:   "<= " + strconv.Itoa(MaxPayloadBytes),
			Actual:     strconv.Itoa(len(payload)),
			PacketType: PacketTypeInput,
		})
	}

	return payload, nil
}

// DecodeInput parses binary payload bytes into an InputEnvelope.
func DecodeInput(payload []byte) (InputEnvelope, error) {
	if len(payload) > MaxPayloadBytes {
		return InputEnvelope{}, NewParseError(ErrPayloadTooLarge, ParseValidation{
			Field:      "INPUT payload length",
			Expected:   "<= " + strconv.Itoa(MaxPayloadBytes),
			Actual:     strconv.Itoa(len(payload)),
			PacketType: PacketTypeInput,
		})
	}

	var in InputEnvelope
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return InputEnvelope{}, NewParseError(ErrLengthMismatch, ParseValidation{
			Field:       "INPUT payload",
			Expected:    "valid JSON envelope",
			Actual:      err.Error(),
			PacketType:  PacketTypeInput,
			HeaderBytes: len(payload),
		})
	}

	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return InputEnvelope{}, NewParseError(ErrLengthMismatch, ParseValidation{
			Field:       "INPUT payload",
			Expected:    "single JSON object with no trailing content",
			Actual:      "trailing content",
			PacketType:  PacketTypeInput,
			HeaderBytes: len(payload),
		})
	}

	if err := validateInputEnvelope(in); err != nil {
		return InputEnvelope{}, err
	}

	return in, nil
}

func validateInputEnvelope(in InputEnvelope) error {
	if !IsKnownInputEventType(in.EventType) {
		return NewParseError(ErrUnknownPacketType, ParseValidation{
			Field:      "eventType",
			Expected:   "known InputEventType",
			Actual:     strconv.Itoa(int(in.EventType)),
			PacketType: PacketTypeInput,
		})
	}

	if strings.TrimSpace(in.ViewerID) == "" {
		return NewParseError(ErrLengthMismatch, ParseValidation{
			Field:      "viewerId",
			Expected:   "non-empty",
			Actual:     "empty",
			PacketType: PacketTypeInput,
		})
	}

	if in.EventID == 0 {
		return NewParseError(ErrLengthMismatch, ParseValidation{
			Field:      "eventId",
			Expected:   "> 0",
			Actual:     "0",
			PacketType: PacketTypeInput,
		})
	}

	if in.TimestampNs == 0 {
		return NewParseError(ErrLengthMismatch, ParseValidation{
			Field:      "timestampNs",
			Expected:   "> 0",
			Actual:     "0",
			PacketType: PacketTypeInput,
		})
	}

	payloadCount := 0
	if in.Mouse != nil {
		payloadCount++
	}
	if in.Wheel != nil {
		payloadCount++
	}
	if in.Key != nil {
		payloadCount++
	}
	if in.Display != nil {
		payloadCount++
	}
	if payloadCount != 1 {
		return NewParseError(ErrLengthMismatch, ParseValidation{
			Field:      "payload",
			Expected:   "exactly one of mouse,wheel,key,display",
			Actual:     strconv.Itoa(payloadCount),
			PacketType: PacketTypeInput,
		})
	}

	switch in.EventType {
	case InputEventMouseMove, InputEventMouseDown, InputEventMouseUp:
		if in.Mouse == nil {
			return NewParseError(ErrLengthMismatch, ParseValidation{
				Field:      "mouse",
				Expected:   "present for mouse event",
				Actual:     "nil",
				PacketType: PacketTypeInput,
			})
		}
		if err := validateMousePayload(*in.Mouse); err != nil {
			return err
		}
	case InputEventMouseWheel:
		if in.Wheel == nil {
			return NewParseError(ErrLengthMismatch, ParseValidation{
				Field:      "wheel",
				Expected:   "present for wheel event",
				Actual:     "nil",
				PacketType: PacketTypeInput,
			})
		}
		if err := validateWheelPayload(*in.Wheel); err != nil {
			return err
		}
	case InputEventKeyDown, InputEventKeyUp:
		if in.Key == nil {
			return NewParseError(ErrLengthMismatch, ParseValidation{
				Field:      "key",
				Expected:   "present for key event",
				Actual:     "nil",
				PacketType: PacketTypeInput,
			})
		}
		if err := validateKeyPayload(*in.Key); err != nil {
			return err
		}
	case InputEventResizeHint, InputEventMonitorSelect:
		if in.Display == nil {
			return NewParseError(ErrLengthMismatch, ParseValidation{
				Field:      "display",
				Expected:   "present for display event",
				Actual:     "nil",
				PacketType: PacketTypeInput,
			})
		}
		if err := validateDisplayPayload(*in.Display); err != nil {
			return err
		}
	default:
		// Guarded above by IsKnownInputEventType.
	}

	return nil
}

func validateMousePayload(m MousePayload) error {
	if !isFinite(m.XNorm) || !isFinite(m.YNorm) {
		return NewParseError(ErrLengthMismatch, ParseValidation{
			Field:      "xNorm/yNorm",
			Expected:   "finite numbers",
			Actual:     "NaN/Inf",
			PacketType: PacketTypeInput,
		})
	}

	if m.XNorm < 0.0 || m.XNorm > 1.0 || m.YNorm < 0.0 || m.YNorm > 1.0 {
		return NewParseError(ErrLengthMismatch, ParseValidation{
			Field:      "xNorm/yNorm",
			Expected:   "in range [0.0, 1.0]",
			Actual:     "out of range",
			PacketType: PacketTypeInput,
		})
	}

	switch m.Button {
	case MouseButtonNone, MouseButtonLeft, MouseButtonRight, MouseButtonMiddle:
	default:
		return NewParseError(ErrLengthMismatch, ParseValidation{
			Field:      "button",
			Expected:   "none/left/right/middle",
			Actual:     strconv.Itoa(int(m.Button)),
			PacketType: PacketTypeInput,
		})
	}

	if m.ButtonsMask&^allowedMouseButtonsMask() != 0 {
		return NewParseError(ErrLengthMismatch, ParseValidation{
			Field:      "buttonsMask",
			Expected:   "known button bits only",
			Actual:     strconv.Itoa(int(m.ButtonsMask)),
			PacketType: PacketTypeInput,
		})
	}

	return nil
}

func validateWheelPayload(w WheelPayload) error {
	if !isFinite(w.DeltaX) || !isFinite(w.DeltaY) {
		return NewParseError(ErrLengthMismatch, ParseValidation{
			Field:      "deltaX/deltaY",
			Expected:   "finite numbers",
			Actual:     "NaN/Inf",
			PacketType: PacketTypeInput,
		})
	}

	return nil
}

func validateKeyPayload(k KeyPayload) error {
	if strings.TrimSpace(k.Code) == "" && strings.TrimSpace(k.Key) == "" {
		return NewParseError(ErrLengthMismatch, ParseValidation{
			Field:      "code/key",
			Expected:   "at least one non-empty key identifier",
			Actual:     "both empty",
			PacketType: PacketTypeInput,
		})
	}

	if k.Modifiers&^allowedKeyModifiersMask() != 0 {
		return NewParseError(ErrLengthMismatch, ParseValidation{
			Field:      "modifiers",
			Expected:   "known modifier bits only",
			Actual:     strconv.Itoa(int(k.Modifiers)),
			PacketType: PacketTypeInput,
		})
	}

	return nil
}

func validateDisplayPayload(d DisplayPayload) error {
	if d.ViewportWidth == 0 || d.ViewportHeight == 0 {
		return NewParseError(ErrLengthMismatch, ParseValidation{
			Field:      "viewportWidth/viewportHeight",
			Expected:   "> 0",
			Actual:     "0",
			PacketType: PacketTypeInput,
		})
	}

	if strings.TrimSpace(d.TargetMonitorID) == "" {
		return NewParseError(ErrLengthMismatch, ParseValidation{
			Field:      "targetMonitorId",
			Expected:   "non-empty",
			Actual:     "empty",
			PacketType: PacketTypeInput,
		})
	}

	return nil
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

func allowedMouseButtonsMask() MouseButtonsMask {
	return MouseButtonsMaskLeft | MouseButtonsMaskRight | MouseButtonsMaskMiddle
}

func allowedKeyModifiersMask() KeyModifiers {
	return KeyModifierCtrl | KeyModifierAlt | KeyModifierShift | KeyModifierMeta
}
