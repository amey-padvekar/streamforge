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
	PacketTypeInput     PacketType = 0x08
)

// InputEventType identifies the semantic meaning of an INPUT packet payload.
type InputEventType uint8

const (
	InputEventMouseMove  InputEventType = 0x10
	InputEventMouseDown  InputEventType = 0x11
	InputEventMouseUp    InputEventType = 0x12
	InputEventMouseWheel InputEventType = 0x13

	// 0x14-0x1F reserved for future mouse/pointer events.

	InputEventKeyDown InputEventType = 0x20
	InputEventKeyUp   InputEventType = 0x21

	// 0x22-0x2F reserved for future keyboard events.

	InputEventResizeHint    InputEventType = 0x30
	InputEventMonitorSelect InputEventType = 0x31

	// 0x32-0x3F reserved for future display/session control events.
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
		PacketTypeError,
		PacketTypeInput:
		return true
	default:
		return false
	}
}

// IsKnownInputEventType reports whether t is a defined INPUT event type.
func IsKnownInputEventType(t InputEventType) bool {
	switch t {
	case InputEventMouseMove,
		InputEventMouseDown,
		InputEventMouseUp,
		InputEventMouseWheel,
		InputEventKeyDown,
		InputEventKeyUp,
		InputEventResizeHint,
		InputEventMonitorSelect:
		return true
	default:
		return false
	}
}
