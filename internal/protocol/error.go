package protocol

import "strconv"

// ErrorPayload carries error reason and optional detail message.
type ErrorPayload struct {
	Reason string // error category/reason (max 255 bytes)
	Detail string // optional detail message (max 65535 bytes)
}

// EncodeError encodes an ErrorPayload (reasonLen + reason + detailLen + detail).
func EncodeError(e ErrorPayload) ([]byte, error) {
	reason := e.Reason
	detail := e.Detail

	if len(reason) > 255 {
		return nil, NewParseError(ErrPayloadTooLarge, ParseValidation{
			Field:      "error reason length",
			Expected:   "<= 255",
			Actual:     strconv.Itoa(len(reason)),
			PacketType: PacketTypeError,
		})
	}

	if len(detail) > 65535 {
		return nil, NewParseError(ErrPayloadTooLarge, ParseValidation{
			Field:      "error detail length",
			Expected:   "<= 65535",
			Actual:     strconv.Itoa(len(detail)),
			PacketType: PacketTypeError,
		})
	}

	// Layout: 1 byte (reasonLen) + reason + 2 bytes (detailLen) + detail
	payloadLen := 1 + len(reason) + 2 + len(detail)
	payload := make([]byte, payloadLen)

	payload[0] = uint8(len(reason))
	copy(payload[1:], reason)

	detailLenOffset := 1 + len(reason)
	detailLen := uint16(len(detail))
	payload[detailLenOffset] = byte(detailLen >> 8)
	payload[detailLenOffset+1] = byte(detailLen & 0xFF)

	copy(payload[detailLenOffset+2:], detail)

	return payload, nil
}

// DecodeError parses a binary ERROR payload.
func DecodeError(payload []byte) (ErrorPayload, error) {
	if len(payload) < 3 {
		return ErrorPayload{}, NewParseError(ErrHeaderTooShort, ParseValidation{
			Field:       "ERROR payload",
			Expected:    ">= 3 bytes",
			Actual:      strconv.Itoa(len(payload)),
			PacketType:  PacketTypeError,
			HeaderBytes: len(payload),
		})
	}

	reasonLen := int(payload[0])
	if len(payload) < 1+reasonLen+2 {
		return ErrorPayload{}, NewParseError(ErrLengthMismatch, ParseValidation{
			Field:       "ERROR payload length (reason section)",
			Expected:    strconv.Itoa(1 + reasonLen + 2),
			Actual:      strconv.Itoa(len(payload)),
			PacketType:  PacketTypeError,
			HeaderBytes: len(payload),
		})
	}

	reason := string(payload[1 : 1+reasonLen])
	detailLenOffset := 1 + reasonLen
	detailLen := (uint16(payload[detailLenOffset]) << 8) | uint16(payload[detailLenOffset+1])

	if len(payload) < detailLenOffset+2+int(detailLen) {
		return ErrorPayload{}, NewParseError(ErrLengthMismatch, ParseValidation{
			Field:       "ERROR payload length (detail section)",
			Expected:    strconv.Itoa(detailLenOffset + 2 + int(detailLen)),
			Actual:      strconv.Itoa(len(payload)),
			PacketType:  PacketTypeError,
			HeaderBytes: len(payload),
		})
	}

	detail := string(payload[detailLenOffset+2 : detailLenOffset+2+int(detailLen)])

	return ErrorPayload{
		Reason: reason,
		Detail: detail,
	}, nil
}
