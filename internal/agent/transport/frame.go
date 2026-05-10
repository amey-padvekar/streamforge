package transport

import (
	"encoding/binary"
	"fmt"
	"time"

	"streamforge/internal/protocol"
)

const (
	framePayloadMetadataSize  = 5
	framePayloadWidthOffset   = 0
	framePayloadHeightOffset  = 2
	framePayloadQualityOffset = 4
	framePayloadJPEGOffset    = 5
)

// FrameHeader is parsed from the common protocol header + frame payload metadata.
type FrameHeader struct {
	Version     uint8
	PacketType  uint8
	FrameID     uint32
	Width       uint16
	Height      uint16
	JPEGQuality uint8
	PayloadLen  uint32
}

// EncodeFrame encodes a frame packet as common protocol header + frame payload.
func EncodeFrame(frameID uint32, width, height uint16, quality uint8, jpeg []byte) []byte {
	payload := make([]byte, framePayloadMetadataSize+len(jpeg))
	binary.BigEndian.PutUint16(payload[framePayloadWidthOffset:framePayloadWidthOffset+2], width)
	binary.BigEndian.PutUint16(payload[framePayloadHeightOffset:framePayloadHeightOffset+2], height)
	payload[framePayloadQualityOffset] = quality
	copy(payload[framePayloadJPEGOffset:], jpeg)

	header := protocol.Header{
		Version:     protocol.ProtocolVersion,
		PacketType:  protocol.PacketTypeFrame,
		SequenceID:  frameID,
		TimestampNs: uint64(time.Now().UnixNano()),
		PayloadLen:  uint32(len(payload)),
	}

	packet, err := protocol.BuildPacket(header, payload)
	if err != nil {
		return nil
	}

	return packet
}

// DecodeFrameHeader decodes and validates frame metadata from a protocol packet.
func DecodeFrameHeader(data []byte) (FrameHeader, error) {
	h, payload, err := protocol.ParsePacket(data)
	if err != nil {
		return FrameHeader{}, err
	}

	if h.PacketType != protocol.PacketTypeFrame {
		return FrameHeader{}, fmt.Errorf("invalid packet type: got 0x%02x, want 0x%02x", uint8(h.PacketType), uint8(protocol.PacketTypeFrame))
	}

	if len(payload) < framePayloadMetadataSize {
		return FrameHeader{}, fmt.Errorf("frame payload metadata too short: got %d bytes, need %d", len(payload), framePayloadMetadataSize)
	}

	jpegPayloadLen := len(payload) - framePayloadMetadataSize
	return FrameHeader{
		Version:     h.Version,
		PacketType:  uint8(h.PacketType),
		FrameID:     h.SequenceID,
		Width:       binary.BigEndian.Uint16(payload[framePayloadWidthOffset : framePayloadWidthOffset+2]),
		Height:      binary.BigEndian.Uint16(payload[framePayloadHeightOffset : framePayloadHeightOffset+2]),
		JPEGQuality: payload[framePayloadQualityOffset],
		PayloadLen:  uint32(jpegPayloadLen),
	}, nil
}
