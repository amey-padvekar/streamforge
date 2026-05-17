package protocol

/*
Package protocol defines Streamforge's binary wire format and parser helpers.

Protocol Evolution Rules

Backward-compatible changes (minor evolution inside the same protocol version):
	- Add new packet types if older peers can safely reject or ignore them by policy.
	- Add optional payload fields only at the end of an existing payload format.
	- Introduce new flag bits with default value 0 and preserve behavior when unset.
	- Tighten validation only when malformed inputs were already invalid by spec.

Breaking changes (require protocol version bump):
	- Reorder, resize, or reinterpret fields in the fixed 20-byte common header.
	- Change byte order, field offsets, or core semantics of existing packet types.
	- Change required payload fields for existing packet types in a way old peers cannot parse.
	- Reuse an existing packet type value for different semantics.

Packet type extension policy:
	- PacketType values are append-only and must remain stable once released.
	- Unknown packet types are rejected by default unless explicitly marked ignorable by flags.
	- New packet types must define ownership (agent, server, viewer), payload schema,
		and rejection/acknowledgement behavior.
	- Do not repurpose retired type IDs; keep historical mappings for diagnostics.
	- INPUT packet type uses PacketTypeInput (0x08) and must remain wire-stable once released.

INPUT event wire-value policy:
	- InputEventType values are explicitly assigned and append-only.
	- Current values: MouseMove=0x10, MouseDown=0x11, MouseUp=0x12, MouseWheel=0x13,
		KeyDown=0x20, KeyUp=0x21, ResizeHint=0x30, MonitorSelect=0x31.
	- Reserved ranges: 0x14-0x1F (mouse/pointer), 0x22-0x2F (keyboard),
		0x32-0x3F (display/session control).
	- Receivers must reject unknown InputEventType values by default.

INPUT versioning and compatibility policy:
	- InputEnvelope JSON keys are versioned by additive evolution in protocol v1.
	- New optional fields may be added only as backward-compatible additions; existing
		field names, types, and semantics are immutable within v1.
	- Existing required fields (eventType, eventId, timestampNs, viewerId) cannot be removed
		or made optional in v1.
	- If an INPUT change requires field type reinterpretation, required-field removal,
		or non-additive schema changes, a new protocol version is required.

INPUT modifiers and reserved bits policy:
	- Key modifiers currently use bitmask: ctrl(1<<0), alt(1<<1), shift(1<<2), meta(1<<3).
	- Bits 4-7 are reserved for future extension and must be zero in v1 payloads.
	- Receivers must reject payloads that set unknown modifier bits unless a newer
		protocol version explicitly defines them.

Unknown INPUT event handling expectations:
	- Unknown InputEventType values are treated as protocol parse errors and must not
		be forwarded to agent injection.
	- Implementations should emit categorized telemetry for unknown/rejected INPUT events
		to support compatibility diagnostics during rollout.

Reserved flags usage policy:
	- Header.Reserved byte must be zero in v1; non-zero is a protocol error.
	- New flag bits must be documented with explicit sender/receiver behavior.
	- Receivers must mask and evaluate only known bits; unknown bits are handled by version/flags policy.
	- Never assign conflicting meanings to the same bit across packet types.

Deprecation and removal guidance:
	- Deprecate before removal: announce replacement packet/field and migration behavior.
	- Keep compatibility paths for at least one full release cycle after deprecation.
	- Emit categorized logs/counters when deprecated behavior is observed.
	- Remove deprecated behavior only in a new protocol version or after explicit cutoff policy.
*/
