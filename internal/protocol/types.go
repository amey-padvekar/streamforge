package protocol

// ProtocolVersion is the current supported binary protocol version.
const ProtocolVersion uint8 = 1

const (
	// HeaderSize is the fixed byte size of the common packet header.
	HeaderSize = 20
	// MaxPayloadBytes bounds packet payload size for parser safety.
	MaxPayloadBytes = 8 * 1024 * 1024 // 8 MiB
)

const (
	VersionOffset    uint8 = 0
	TypeOffset       uint8 = 1
	FlagsOffset      uint8 = 2
	ReservedOffset   uint8 = 3
	SequenceOffset   uint8 = 4
	TimestampOffset  uint8 = 8
	PayloadLenOffset uint8 = 16
)

// PacketType identifies the semantic meaning of a packet payload.
type PacketType uint8

const (
	PacketTypeHello     PacketType = 0x01
	PacketTypeAuth      PacketType = 0x02
	PacketTypeFrame     PacketType = 0x03
	PacketTypeHeartbeat PacketType = 0x04
	PacketTypeAck       PacketType = 0x05
	PacketTypeControl   PacketType = 0x06
	PacketTypeError     PacketType = 0x07
)

// Header carries the common metadata prefix for all binary protocol packets.
type Header struct {
	Version     uint8
	PacketType  PacketType
	Flags       uint8
	Reserved    uint8
	SequenceID  uint32
	TimestampNs uint64
	PayloadLen  uint32
}

// ParseValidation captures structured context about a parser validation failure.
type ParseValidation struct {
	Field       string
	Expected    string
	Actual      string
	PacketType  PacketType
	HeaderBytes int
}

// IsKnownPacketType reports whether t is a defined protocol packet type.
func IsKnownPacketType(t PacketType) bool {
	switch t {
	case PacketTypeHello,
		PacketTypeAuth,
		PacketTypeFrame,
		PacketTypeHeartbeat,
		PacketTypeAck,
		PacketTypeControl,
		PacketTypeError:
		return true
	default:
		return false
	}
}
