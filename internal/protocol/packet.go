package protocol

import "strconv"

// BuildPacket validates header/payload consistency and returns a full packet buffer.
func BuildPacket(h Header, payload []byte) ([]byte, error) {
	payloadLen := len(payload)
	if payloadLen > MaxPayloadBytes {
		return nil, NewParseError(ErrPayloadTooLarge, ParseValidation{
			Field:      "payloadLen",
			Expected:   "<= " + strconv.Itoa(MaxPayloadBytes),
			Actual:     strconv.Itoa(payloadLen),
			PacketType: h.PacketType,
		})
	}

	if h.PayloadLen != uint32(payloadLen) {
		return nil, NewParseError(ErrLengthMismatch, ParseValidation{
			Field:      "payloadLen",
			Expected:   strconv.Itoa(payloadLen),
			Actual:     strconv.FormatUint(uint64(h.PayloadLen), 10),
			PacketType: h.PacketType,
		})
	}

	packet := make([]byte, HeaderSize+payloadLen)
	if err := EncodeHeader(packet[:HeaderSize], h); err != nil {
		return nil, err
	}
	copy(packet[HeaderSize:], payload)

	return packet, nil
}

// ParsePacket validates packet framing and returns parsed header plus payload bytes.
func ParsePacket(data []byte) (Header, []byte, error) {
	h, err := DecodeHeader(data)
	if err != nil {
		return Header{}, nil, err
	}

	expectedLen := HeaderSize + int(h.PayloadLen)
	if len(data) != expectedLen {
		return Header{}, nil, NewParseError(ErrLengthMismatch, ParseValidation{
			Field:       "packetLength",
			Expected:    strconv.Itoa(expectedLen),
			Actual:      strconv.Itoa(len(data)),
			PacketType:  h.PacketType,
			HeaderBytes: HeaderSize,
		})
	}

	return h, data[HeaderSize:expectedLen], nil
}
