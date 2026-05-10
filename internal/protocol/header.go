package protocol

import (
	"encoding/binary"
	"strconv"
)

// EncodeHeader writes a validated protocol header into dst.
func EncodeHeader(dst []byte, h Header) error {
	if len(dst) < HeaderSize {
		return NewParseError(ErrHeaderTooShort, ParseValidation{
			Field:       "header",
			Expected:    "at least " + strconv.Itoa(HeaderSize) + " bytes",
			Actual:      strconv.Itoa(len(dst)) + " bytes",
			PacketType:  h.PacketType,
			HeaderBytes: len(dst),
		})
	}

	if h.Version != ProtocolVersion {
		return NewParseError(ErrUnsupportedVersion, ParseValidation{
			Field:       "version",
			Expected:    strconv.Itoa(int(ProtocolVersion)),
			Actual:      strconv.Itoa(int(h.Version)),
			PacketType:  h.PacketType,
			HeaderBytes: len(dst),
		})
	}

	if !IsKnownPacketType(h.PacketType) {
		return NewParseError(ErrUnknownPacketType, ParseValidation{
			Field:       "packetType",
			Expected:    "known packet type",
			Actual:      "0x" + strconv.FormatUint(uint64(h.PacketType), 16),
			PacketType:  h.PacketType,
			HeaderBytes: len(dst),
		})
	}

	if h.PayloadLen > uint32(MaxPayloadBytes) {
		return NewParseError(ErrPayloadTooLarge, ParseValidation{
			Field:       "payloadLen",
			Expected:    "<= " + strconv.Itoa(MaxPayloadBytes),
			Actual:      strconv.FormatUint(uint64(h.PayloadLen), 10),
			PacketType:  h.PacketType,
			HeaderBytes: len(dst),
		})
	}

	if h.Reserved != 0 {
		return NewParseError(ErrReservedNotZero, ParseValidation{
			Field:       "reserved",
			Expected:    "0",
			Actual:      strconv.Itoa(int(h.Reserved)),
			PacketType:  h.PacketType,
			HeaderBytes: len(dst),
		})
	}

	dst[VersionOffset] = h.Version
	dst[TypeOffset] = uint8(h.PacketType)
	dst[FlagsOffset] = h.Flags
	dst[ReservedOffset] = h.Reserved
	binary.BigEndian.PutUint32(dst[SequenceOffset:SequenceOffset+4], h.SequenceID)
	binary.BigEndian.PutUint64(dst[TimestampOffset:TimestampOffset+8], h.TimestampNs)
	binary.BigEndian.PutUint32(dst[PayloadLenOffset:PayloadLenOffset+4], h.PayloadLen)

	return nil
}

// DecodeHeader parses and validates a protocol header from src.
func DecodeHeader(src []byte) (Header, error) {
	if len(src) < HeaderSize {
		return Header{}, NewParseError(ErrHeaderTooShort, ParseValidation{
			Field:       "header",
			Expected:    "at least " + strconv.Itoa(HeaderSize) + " bytes",
			Actual:      strconv.Itoa(len(src)) + " bytes",
			HeaderBytes: len(src),
		})
	}

	h := Header{
		Version:     src[VersionOffset],
		PacketType:  PacketType(src[TypeOffset]),
		Flags:       src[FlagsOffset],
		Reserved:    src[ReservedOffset],
		SequenceID:  binary.BigEndian.Uint32(src[SequenceOffset : SequenceOffset+4]),
		TimestampNs: binary.BigEndian.Uint64(src[TimestampOffset : TimestampOffset+8]),
		PayloadLen:  binary.BigEndian.Uint32(src[PayloadLenOffset : PayloadLenOffset+4]),
	}

	if h.Version != ProtocolVersion {
		return Header{}, NewParseError(ErrUnsupportedVersion, ParseValidation{
			Field:       "version",
			Expected:    strconv.Itoa(int(ProtocolVersion)),
			Actual:      strconv.Itoa(int(h.Version)),
			PacketType:  h.PacketType,
			HeaderBytes: len(src),
		})
	}

	if !IsKnownPacketType(h.PacketType) {
		return Header{}, NewParseError(ErrUnknownPacketType, ParseValidation{
			Field:       "packetType",
			Expected:    "known packet type",
			Actual:      "0x" + strconv.FormatUint(uint64(h.PacketType), 16),
			PacketType:  h.PacketType,
			HeaderBytes: len(src),
		})
	}

	if h.PayloadLen > uint32(MaxPayloadBytes) {
		return Header{}, NewParseError(ErrPayloadTooLarge, ParseValidation{
			Field:       "payloadLen",
			Expected:    "<= " + strconv.Itoa(MaxPayloadBytes),
			Actual:      strconv.FormatUint(uint64(h.PayloadLen), 10),
			PacketType:  h.PacketType,
			HeaderBytes: len(src),
		})
	}

	if h.Reserved != 0 {
		return Header{}, NewParseError(ErrReservedNotZero, ParseValidation{
			Field:       "reserved",
			Expected:    "0",
			Actual:      strconv.Itoa(int(h.Reserved)),
			PacketType:  h.PacketType,
			HeaderBytes: len(src),
		})
	}

	return h, nil
}
