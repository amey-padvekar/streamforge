package protocol

import (
	"strconv"
	"strings"
)

// HelloPayload carries initial connection metadata for protocol negotiation.
type HelloPayload struct {
	AgentID          string // agent or viewer identifier (max 255 bytes)
	SupportedVersion uint8  // highest protocol version supported by client
	CapabilityFlags  uint8  // bit flags for optional features
}

// EncodeHello encodes a HelloPayload into binary form (agentIDLen + agentID + version + flags).
func EncodeHello(h HelloPayload) ([]byte, error) {
	if len(h.AgentID) > 255 {
		return nil, NewParseError(ErrPayloadTooLarge, ParseValidation{
			Field:      "agentID length",
			Expected:   "<= 255",
			Actual:     strconv.Itoa(len(h.AgentID)),
			PacketType: PacketTypeHello,
		})
	}

	// Layout: 1 byte (agentID length) + agentID + 1 byte (version) + 1 byte (flags)
	payloadLen := 1 + len(h.AgentID) + 1 + 1
	payload := make([]byte, payloadLen)

	payload[0] = uint8(len(h.AgentID))
	copy(payload[1:], h.AgentID)
	payload[1+len(h.AgentID)] = h.SupportedVersion
	payload[1+len(h.AgentID)+1] = h.CapabilityFlags

	return payload, nil
}

// DecodeHello parses a binary HELLO payload.
func DecodeHello(payload []byte) (HelloPayload, error) {
	if len(payload) < 3 {
		return HelloPayload{}, NewParseError(ErrHeaderTooShort, ParseValidation{
			Field:       "HELLO payload",
			Expected:    ">= 3 bytes",
			Actual:      strconv.Itoa(len(payload)),
			PacketType:  PacketTypeHello,
			HeaderBytes: len(payload),
		})
	}

	idLen := int(payload[0])
	if len(payload) < 1+idLen+2 {
		return HelloPayload{}, NewParseError(ErrLengthMismatch, ParseValidation{
			Field:       "HELLO payload length",
			Expected:    strconv.Itoa(1 + idLen + 2),
			Actual:      strconv.Itoa(len(payload)),
			PacketType:  PacketTypeHello,
			HeaderBytes: len(payload),
		})
	}

	agentID := string(payload[1 : 1+idLen])
	supportedVersion := payload[1+idLen]
	capabilityFlags := payload[1+idLen+1]

	// Sanitize agentID
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return HelloPayload{}, NewParseError(ErrLengthMismatch, ParseValidation{
			Field:       "agentID",
			Expected:    "non-empty",
			Actual:      "empty",
			PacketType:  PacketTypeHello,
			HeaderBytes: len(payload),
		})
	}

	return HelloPayload{
		AgentID:          agentID,
		SupportedVersion: supportedVersion,
		CapabilityFlags:  capabilityFlags,
	}, nil
}
