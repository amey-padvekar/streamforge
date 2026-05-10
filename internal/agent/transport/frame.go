package transport

import (
	"encoding/binary"
	"fmt"
)

const (
	FrameVersion    = uint8(1)
	PacketTypeFrame = uint8(0x01)

	headerSize = 15
)

const (
	offsetVersion     = 0
	offsetPacketType  = 1
	offsetFrameID     = 2
	offsetWidth       = 6
	offsetHeight      = 8
	offsetJPEGQuality = 10
	offsetPayloadLen  = 11
	offsetPayload     = 15
)

// FrameHeader is the fixed-size metadata section for a streamed frame packet.
type FrameHeader struct {
	Version     uint8
	PacketType  uint8
	FrameID     uint32
	Width       uint16
	Height      uint16
	JPEGQuality uint8
	PayloadLen  uint32
}

// EncodeFrame encodes a frame packet as fixed-size header + JPEG payload.
func EncodeFrame(frameID uint32, width, height uint16, quality uint8, jpeg []byte) []byte {
	payloadLen := len(jpeg)
	packet := make([]byte, offsetPayload+payloadLen)

	packet[offsetVersion] = FrameVersion
	packet[offsetPacketType] = PacketTypeFrame
	binary.BigEndian.PutUint32(packet[offsetFrameID:offsetFrameID+4], frameID)
	binary.BigEndian.PutUint16(packet[offsetWidth:offsetWidth+2], width)
	binary.BigEndian.PutUint16(packet[offsetHeight:offsetHeight+2], height)
	packet[offsetJPEGQuality] = quality
	binary.BigEndian.PutUint32(packet[offsetPayloadLen:offsetPayloadLen+4], uint32(payloadLen))
	copy(packet[offsetPayload:], jpeg)

	return packet
}

// DecodeFrameHeader decodes and validates the fixed-size frame header.
func DecodeFrameHeader(data []byte) (FrameHeader, error) {
	if len(data) < headerSize {
		return FrameHeader{}, fmt.Errorf("frame header too short: got %d bytes, need %d", len(data), headerSize)
	}

	header := FrameHeader{
		Version:     data[offsetVersion],
		PacketType:  data[offsetPacketType],
		FrameID:     binary.BigEndian.Uint32(data[offsetFrameID : offsetFrameID+4]),
		Width:       binary.BigEndian.Uint16(data[offsetWidth : offsetWidth+2]),
		Height:      binary.BigEndian.Uint16(data[offsetHeight : offsetHeight+2]),
		JPEGQuality: data[offsetJPEGQuality],
		PayloadLen:  binary.BigEndian.Uint32(data[offsetPayloadLen : offsetPayloadLen+4]),
	}

	if header.PacketType != PacketTypeFrame {
		return FrameHeader{}, fmt.Errorf("invalid packet type: got 0x%02x, want 0x%02x", header.PacketType, PacketTypeFrame)
	}

	return header, nil
}
