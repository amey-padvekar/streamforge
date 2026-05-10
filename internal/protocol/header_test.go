package protocol

import (
	"errors"
	"testing"
)

func TestEncodeDecodeHeader_RoundTrip(t *testing.T) {
	h := Header{
		Version:     ProtocolVersion,
		PacketType:  PacketTypeFrame,
		Flags:       0,
		Reserved:    0,
		SequenceID:  42,
		TimestampNs: 123456789,
		PayloadLen:  16,
	}

	buf := make([]byte, HeaderSize)
	if err := EncodeHeader(buf, h); err != nil {
		t.Fatalf("EncodeHeader failed: %v", err)
	}

	got, err := DecodeHeader(buf)
	if err != nil {
		t.Fatalf("DecodeHeader failed: %v", err)
	}

	if got != h {
		t.Fatalf("header mismatch: got %+v want %+v", got, h)
	}
}

func TestDecodeHeader_ShortHeader(t *testing.T) {
	_, err := DecodeHeader(make([]byte, HeaderSize-1))
	if err == nil {
		t.Fatalf("expected error for short header")
	}
	if !errors.Is(err, ErrHeaderTooShort) {
		t.Fatalf("expected ErrHeaderTooShort, got %v", err)
	}
}

func TestDecodeHeader_InvalidVersion(t *testing.T) {
	buf := make([]byte, HeaderSize)
	buf[VersionOffset] = ProtocolVersion + 1
	buf[TypeOffset] = uint8(PacketTypeFrame)
	buf[ReservedOffset] = 0

	_, err := DecodeHeader(buf)
	if err == nil {
		t.Fatalf("expected error for invalid version")
	}
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("expected ErrUnsupportedVersion, got %v", err)
	}
}

func TestDecodeHeader_UnknownPacketType(t *testing.T) {
	buf := make([]byte, HeaderSize)
	buf[VersionOffset] = ProtocolVersion
	buf[TypeOffset] = 0xFE
	buf[ReservedOffset] = 0

	_, err := DecodeHeader(buf)
	if err == nil {
		t.Fatalf("expected error for unknown packet type")
	}
	if !errors.Is(err, ErrUnknownPacketType) {
		t.Fatalf("expected ErrUnknownPacketType, got %v", err)
	}
}

func TestDecodeHeader_PayloadTooLarge(t *testing.T) {
	buf := make([]byte, HeaderSize)
	buf[VersionOffset] = ProtocolVersion
	buf[TypeOffset] = uint8(PacketTypeFrame)
	buf[ReservedOffset] = 0
	buf[PayloadLenOffset] = byte((uint32(MaxPayloadBytes+1) >> 24) & 0xFF)
	buf[PayloadLenOffset+1] = byte((uint32(MaxPayloadBytes+1) >> 16) & 0xFF)
	buf[PayloadLenOffset+2] = byte((uint32(MaxPayloadBytes+1) >> 8) & 0xFF)
	buf[PayloadLenOffset+3] = byte(uint32(MaxPayloadBytes+1) & 0xFF)

	_, err := DecodeHeader(buf)
	if err == nil {
		t.Fatalf("expected error for payload too large")
	}
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("expected ErrPayloadTooLarge, got %v", err)
	}
}

func TestDecodeHeader_ReservedNotZero(t *testing.T) {
	buf := make([]byte, HeaderSize)
	buf[VersionOffset] = ProtocolVersion
	buf[TypeOffset] = uint8(PacketTypeFrame)
	buf[ReservedOffset] = 1

	_, err := DecodeHeader(buf)
	if err == nil {
		t.Fatalf("expected error for reserved byte")
	}
	if !errors.Is(err, ErrReservedNotZero) {
		t.Fatalf("expected ErrReservedNotZero, got %v", err)
	}
}
