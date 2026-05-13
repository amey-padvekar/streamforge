# Structured Log Field Schema

This document defines canonical log field names for Streamforge runtime components.

## Required canonical fields

Every structured log entry that represents packet, frame, connection, queue, or error behavior should include these keys when applicable:

- `sessionId`: Session identifier. Use `"unknown"` when not available.
- `role`: One of `agent`, `viewer`, `server`, or `unknown`.
- `frameId`: Protocol sequence/frame identifier. Use `0` when not frame-specific.
- `packetType`: Protocol packet type or packet semantic (`FRAME`, `AUTH`, `HEARTBEAT`, etc.).
- `queueDepth`: Queue depth at the point of the event. Use `0` if not queued.
- `framesDropped`: Cumulative or event-local dropped frame count at the point of the event.
- `errorCategory`: One of `auth`, `protocol`, `transport`, `timeout`, `backpressure`, or `internal`.

## Naming rules

- Use lower camelCase keys exactly as listed above.
- Avoid aliases such as `frameID`, `viewer_count`, or `errCategory`.
- Keep canonical keys stable across Go (`slog`) and viewer TypeScript (`console`) logs.

## Component notes

- Agent scheduler and transport logs include role `agent`.
- Server transport/router logs include role `server`, `agent`, or `viewer` based on event owner.
- Viewer logs include role `viewer`; `sessionId` is the active session input or `unknown`.

## Validation

- Run `./scripts/validate-workstream4.ps1` from repository root to verify:
	- error categories are policy-compliant
	- canonical structured keys are present across agent/server/viewer sources
