package protocol

import "strconv"

// AckPayload carries server acknowledgment and selected protocol version.
type AckPayload struct {
	SelectedVersion uint8 // protocol version chosen by server
}

// EncodeAck encodes an AckPayload (1 byte: selectedVersion).
func EncodeAck(a AckPayload) ([]byte, error) {
	return []byte{a.SelectedVersion}, nil
}

// DecodeAck parses a binary ACK payload.
func DecodeAck(payload []byte) (AckPayload, error) {
	if len(payload) < 1 {
		return AckPayload{}, NewParseError(ErrHeaderTooShort, ParseValidation{
			Field:       "ACK payload",
			Expected:    ">= 1 byte",
			Actual:      strconv.Itoa(len(payload)),
			PacketType:  PacketTypeAck,
			HeaderBytes: len(payload),
		})
	}
	return AckPayload{SelectedVersion: payload[0]}, nil
}
