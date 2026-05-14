package protocol

import (
	"errors"
	"testing"
)

func makeValidHeaderBuffer() []byte {
	buf := make([]byte, HeaderSize)
	buf[VersionOffset] = ProtocolVersion
	buf[TypeOffset] = uint8(PacketTypeFrame)
	buf[FlagsOffset] = 0
	buf[ReservedOffset] = 0

	return buf
}

func TestBuildParsePacket_RoundTrip(t *testing.T) {
	payload := []byte{1, 2, 3, 4, 5}
	h := Header{
		Version:     ProtocolVersion,
		PacketType:  PacketTypeControl,
		Flags:       0,
		Reserved:    0,
		SequenceID:  7,
		TimestampNs: 99,
		PayloadLen:  uint32(len(payload)),
	}

	packet, err := BuildPacket(h, payload)
	if err != nil {
		t.Fatalf("BuildPacket failed: %v", err)
	}

	gotHeader, gotPayload, err := ParsePacket(packet)
	if err != nil {
		t.Fatalf("ParsePacket failed: %v", err)
	}

	if gotHeader != h {
		t.Fatalf("header mismatch: got %+v want %+v", gotHeader, h)
	}
	if len(gotPayload) != len(payload) {
		t.Fatalf("payload length mismatch: got %d want %d", len(gotPayload), len(payload))
	}
	for i := range payload {
		if gotPayload[i] != payload[i] {
			t.Fatalf("payload byte mismatch at %d: got %d want %d", i, gotPayload[i], payload[i])
		}
	}
}

func TestBuildPacket_LengthMismatch(t *testing.T) {
	_, err := BuildPacket(Header{
		Version:    ProtocolVersion,
		PacketType: PacketTypeFrame,
		PayloadLen: 10,
	}, []byte{1, 2, 3})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errors.Is(err, ErrLengthMismatch) {
		t.Fatalf("expected ErrLengthMismatch, got %v", err)
	}
}

func TestBuildPacket_PayloadTooLarge(t *testing.T) {
	payload := make([]byte, MaxPayloadBytes+1)
	_, err := BuildPacket(Header{
		Version:    ProtocolVersion,
		PacketType: PacketTypeFrame,
		PayloadLen: uint32(len(payload)),
	}, payload)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("expected ErrPayloadTooLarge, got %v", err)
	}
}

func TestParsePacket_ShortHeader(t *testing.T) {
	_, _, err := ParsePacket(make([]byte, HeaderSize-1))
	if err == nil {
		t.Fatalf("expected error for short header")
	}
	if !errors.Is(err, ErrHeaderTooShort) {
		t.Fatalf("expected ErrHeaderTooShort, got %v", err)
	}
}

func TestParsePacket_InvalidVersion(t *testing.T) {
	buf := makeValidHeaderBuffer()
	buf[VersionOffset] = ProtocolVersion + 1

	_, _, err := ParsePacket(buf)
	if err == nil {
		t.Fatalf("expected error for invalid version")
	}
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("expected ErrUnsupportedVersion, got %v", err)
	}
}

func TestParsePacket_UnknownPacketType(t *testing.T) {
	buf := makeValidHeaderBuffer()
	buf[TypeOffset] = 0xFE

	_, _, err := ParsePacket(buf)
	if err == nil {
		t.Fatalf("expected error for unknown packet type")
	}
	if !errors.Is(err, ErrUnknownPacketType) {
		t.Fatalf("expected ErrUnknownPacketType, got %v", err)
	}
}

func TestParsePacket_PayloadTooLarge(t *testing.T) {
	buf := makeValidHeaderBuffer()
	oversized := uint32(MaxPayloadBytes + 1)
	buf[PayloadLenOffset] = byte((oversized >> 24) & 0xFF)
	buf[PayloadLenOffset+1] = byte((oversized >> 16) & 0xFF)
	buf[PayloadLenOffset+2] = byte((oversized >> 8) & 0xFF)
	buf[PayloadLenOffset+3] = byte(oversized & 0xFF)

	_, _, err := ParsePacket(buf)
	if err == nil {
		t.Fatalf("expected error for oversized payload")
	}
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("expected ErrPayloadTooLarge, got %v", err)
	}
}

func TestParsePacket_LengthMismatch(t *testing.T) {
	payload := []byte{1, 2}
	h := Header{
		Version:    ProtocolVersion,
		PacketType: PacketTypeFrame,
		PayloadLen: uint32(len(payload)),
	}
	packet, err := BuildPacket(h, payload)
	if err != nil {
		t.Fatalf("BuildPacket failed: %v", err)
	}

	truncated := packet[:len(packet)-1]
	_, _, err = ParsePacket(truncated)
	if err == nil {
		t.Fatalf("expected error for mismatched packet length")
	}
	if !errors.Is(err, ErrLengthMismatch) {
		t.Fatalf("expected ErrLengthMismatch, got %v", err)
	}
}
