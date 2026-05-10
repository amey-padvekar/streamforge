package protocol

import (
	"strconv"
	"strings"
)

// AuthPayload carries role and token for connection authorization.
type AuthPayload struct {
	Role  string // "agent" or "viewer" (max 255 bytes)
	Token string // session token (max 65535 bytes)
}

// EncodeAuth encodes an AuthPayload into binary form (roleLen + role + tokenLen + token).
func EncodeAuth(a AuthPayload) ([]byte, error) {
	role := strings.ToLower(strings.TrimSpace(a.Role))
	token := strings.TrimSpace(a.Token)

	if len(role) > 255 {
		return nil, NewParseError(ErrPayloadTooLarge, ParseValidation{
			Field:      "role length",
			Expected:   "<= 255",
			Actual:     strconv.Itoa(len(role)),
			PacketType: PacketTypeAuth,
		})
	}

	if len(token) > 65535 {
		return nil, NewParseError(ErrPayloadTooLarge, ParseValidation{
			Field:      "token length",
			Expected:   "<= 65535",
			Actual:     strconv.Itoa(len(token)),
			PacketType: PacketTypeAuth,
		})
	}

	// Layout: 1 byte (roleLen) + role + 2 bytes (tokenLen) + token
	payloadLen := 1 + len(role) + 2 + len(token)
	payload := make([]byte, payloadLen)

	payload[0] = uint8(len(role))
	copy(payload[1:], role)

	tokenLenOffset := 1 + len(role)
	tokenLen := uint16(len(token))
	tokenLenBytes := make([]byte, 2)
	tokenLenBytes[0] = byte(tokenLen >> 8)
	tokenLenBytes[1] = byte(tokenLen & 0xFF)
	copy(payload[tokenLenOffset:tokenLenOffset+2], tokenLenBytes)

	copy(payload[tokenLenOffset+2:], token)

	return payload, nil
}

// DecodeAuth parses a binary AUTH payload.
func DecodeAuth(payload []byte) (AuthPayload, error) {
	if len(payload) < 3 {
		return AuthPayload{}, NewParseError(ErrHeaderTooShort, ParseValidation{
			Field:       "AUTH payload",
			Expected:    ">= 3 bytes",
			Actual:      strconv.Itoa(len(payload)),
			PacketType:  PacketTypeAuth,
			HeaderBytes: len(payload),
		})
	}

	roleLen := int(payload[0])
	if len(payload) < 1+roleLen+2 {
		return AuthPayload{}, NewParseError(ErrLengthMismatch, ParseValidation{
			Field:       "AUTH payload length (role section)",
			Expected:    strconv.Itoa(1 + roleLen + 2),
			Actual:      strconv.Itoa(len(payload)),
			PacketType:  PacketTypeAuth,
			HeaderBytes: len(payload),
		})
	}

	role := string(payload[1 : 1+roleLen])
	tokenLenOffset := 1 + roleLen
	tokenLen := (uint16(payload[tokenLenOffset]) << 8) | uint16(payload[tokenLenOffset+1])

	if len(payload) < tokenLenOffset+2+int(tokenLen) {
		return AuthPayload{}, NewParseError(ErrLengthMismatch, ParseValidation{
			Field:       "AUTH payload length (token section)",
			Expected:    strconv.Itoa(tokenLenOffset + 2 + int(tokenLen)),
			Actual:      strconv.Itoa(len(payload)),
			PacketType:  PacketTypeAuth,
			HeaderBytes: len(payload),
		})
	}

	token := string(payload[tokenLenOffset+2 : tokenLenOffset+2+int(tokenLen)])

	// Sanitize
	role = strings.ToLower(strings.TrimSpace(role))
	token = strings.TrimSpace(token)

	if role == "" {
		return AuthPayload{}, NewParseError(ErrLengthMismatch, ParseValidation{
			Field:       "role",
			Expected:    "non-empty",
			Actual:      "empty",
			PacketType:  PacketTypeAuth,
			HeaderBytes: len(payload),
		})
	}

	if token == "" {
		return AuthPayload{}, NewParseError(ErrLengthMismatch, ParseValidation{
			Field:       "token",
			Expected:    "non-empty",
			Actual:      "empty",
			PacketType:  PacketTypeAuth,
			HeaderBytes: len(payload),
		})
	}

	return AuthPayload{
		Role:  role,
		Token: token,
	}, nil
}
