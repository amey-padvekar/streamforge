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
