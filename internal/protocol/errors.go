package protocol

import "fmt"

var (
	ErrHeaderTooShort     = fmt.Errorf("protocol header too short")
	ErrUnsupportedVersion = fmt.Errorf("unsupported protocol version")
	ErrUnknownPacketType  = fmt.Errorf("unknown packet type")
	ErrPayloadTooLarge    = fmt.Errorf("payload too large")
	ErrLengthMismatch     = fmt.Errorf("packet length mismatch")
	ErrReservedNotZero    = fmt.Errorf("reserved header byte must be zero")
)

// ParseError wraps a sentinel parse error with structured validation context.
type ParseError struct {
	Err        error
	Validation ParseValidation
}

func (e *ParseError) Error() string {
	if e == nil {
		return "<nil>"
	}

	if e.Validation.Field == "" {
		return e.Err.Error()
	}

	return fmt.Sprintf(
		"%s: field=%s expected=%s actual=%s",
		e.Err,
		e.Validation.Field,
		e.Validation.Expected,
		e.Validation.Actual,
	)
}

func (e *ParseError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// NewParseError creates a categorized parse error with validation context.
func NewParseError(err error, v ParseValidation) error {
	return &ParseError{Err: err, Validation: v}
}
