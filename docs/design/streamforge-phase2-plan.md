# Phase 2 - Protocol and Runtime Stability: Detailed Implementation Plan

## Overview

Phase 2 hardens Streamforge from MVP behavior into a stable runtime that can tolerate slow consumers, reconnect churn, malformed traffic, and long-running sessions.

Phase 1 proved the happy path. Phase 2 focuses on predictable behavior under pressure.

```text
Protocol hardening
  -> Session lifecycle state machine
  -> Bounded pipelines and backpressure
  -> Strong observability
  -> Repeatable stability tests
```

Goal: stable, measurable streaming sessions with controlled latency and drop behavior.

Exit criteria:
- protocol is versioned and reject-safe
- queue growth is bounded by design
- reconnect and disconnect paths are explicit and recoverable
- metrics and logs explain bottlenecks without guesswork

---

## Prerequisites and Baseline

Before starting Phase 2, ensure:
- Phase 1 checklist is complete in docs/design/streamforge-phase1-plan.md
- agent, server, and viewer build successfully
- basic integration flow is reproducible locally

Baseline assumptions from current implementation:
- agent and server are in Go
- viewer is Vite + TypeScript
- transport currently uses binary frame packet + JSON auth handshake
- telemetry exists but is not yet Prometheus grade

---

## Protocol Targets for Phase 2

### Protocol versioning objective
Define Protocol V1 formally and enforce it consistently across:
- agent packet write path
- server packet read/validation path
- viewer packet read path

### Proposed packet model
Use a single fixed header for every binary packet:

```text
Offset  Size  Field
0       1     Version        uint8
1       1     PacketType     uint8
2       1     Flags          uint8
3       1     Reserved       uint8
4       4     SequenceID     uint32  big-endian
8       8     TimestampNs    uint64  big-endian (unix nanos)
16      4     PayloadLen     uint32  big-endian
20      N     Payload
```

Header size: 20 bytes.

Rules:
- byte order: big-endian
- payloadLen max: 8 MiB
- invalid headers must be rejected with categorized error reason
- unknown packet types must be rejected unless explicitly marked ignorable by flags

### Packet types
- 0x01 HELLO
- 0x02 AUTH
- 0x03 FRAME
- 0x04 HEARTBEAT
- 0x05 ACK
- 0x06 CONTROL
- 0x07 ERROR

Ownership:
- agent: HELLO, AUTH, FRAME, HEARTBEAT
- viewer: HELLO, AUTH, HEARTBEAT
- server: ACK, ERROR, CONTROL (and optional HEARTBEAT echo)

---

## Workstream 1 - Protocol Hardening

### Goal
Move protocol behavior from ad hoc envelope usage to strict parser/encoder helpers and negotiation.

### Files involved
- internal/protocol/doc.go
- internal/protocol/header.go (new)
- internal/protocol/packet.go (new)
- internal/protocol/types.go (new)
- internal/protocol/errors.go (new)
- internal/agent/transport/frame.go
- internal/agent/transport/ws.go
- internal/server/transport/ws.go
- web/viewer/src/protocol.ts
- web/viewer/src/viewer.ts

---

### Step 1.1 - Define core protocol constants and structs
Create internal/protocol/types.go with:
- ProtocolVersion constant
- PacketType enum values
- MaxPayloadBytes
- HeaderSize
- Parse/validation helper types

Add structured parse errors in internal/protocol/errors.go:
- ErrHeaderTooShort
- ErrUnsupportedVersion
- ErrUnknownPacketType
- ErrPayloadTooLarge
- ErrLengthMismatch

---

### Step 1.2 - Implement header encode/decode helpers
Create internal/protocol/header.go:
- type Header struct
- func EncodeHeader(dst []byte, h Header) error
- func DecodeHeader(src []byte) (Header, error)

Validation rules:
- enforce header length
- enforce version
- enforce payload bounds
- enforce reserved byte semantics

---

### Step 1.3 - Implement packet reader/writer helpers
Create internal/protocol/packet.go:
- func BuildPacket(h Header, payload []byte) ([]byte, error)
- func ParsePacket(data []byte) (Header, []byte, error)

Behavior:
- never panic on malformed data
- return categorized validation errors
- keep allocations bounded and predictable

---

### Step 1.4 - Migrate FRAME envelope to common packet helpers
Update internal/agent/transport/frame.go:
- map existing frame metadata into protocol Header + frame payload
- remove duplicated low-level binary handling when helper exists

Update web/viewer/src/protocol.ts accordingly:
- parse common header first
- then parse FRAME payload fields

---

### Step 1.5 - Add HELLO/AUTH negotiation sequence
Update handshake in:
- internal/agent/transport/ws.go
- internal/server/transport/ws.go
- web/viewer/src/viewer.ts

Sequence:
1. client sends HELLO with supportedVersion and capability flags
2. server responds with ACK (selectedVersion) or ERROR (unsupported version)
3. client sends AUTH
4. server responds with ACK success or ERROR rejection

Keep transport close behavior explicit on negotiation failure.

---

### Step 1.6 - Document protocol evolution rules
Add section in internal/protocol/doc.go:
- backward-compatible vs breaking changes
- packet type extension policy
- reserved flag usage policy
- deprecation and removal guidance

---

### Step 1.7 - Validation for Workstream 1
- unit tests for header parse, packet parse, malformed input
- server rejects invalid version and oversize payloads
- viewer ignores unsupported payload safely
- all protocol rejections produce categorized log reason

---

## Workstream 2 - Session Lifecycle Hardening

### Goal
Make connection behavior deterministic through explicit state transitions and liveness detection.

### Files involved
- internal/server/session/session.go
- internal/server/session/registry.go
- internal/server/transport/ws.go
- internal/server/transport/agent_handler.go
- internal/server/transport/viewer_handler.go
- internal/agent/transport/ws.go
- web/viewer/src/viewer.ts

---

### Step 2.1 - Add explicit session and connection states
Introduce state enums:
- SessionState: pending, active, degraded, closed, expired
- ConnectionState: disconnected, connecting, authenticated, streaming, stale

State transitions must be validated and logged.

---

### Step 2.2 - Define disconnect and duplicate-join policy
Behavior matrix:
- agent disconnect: session moves to degraded, viewers remain attached
- viewer disconnect: remove only that viewer
- duplicate agent join: reject with protocol error and structured reason
- duplicate viewer token use: allow by default unless explicit single-viewer mode

---

### Step 2.3 - Add token expiration metadata
Extend session model with:
- tokenIssuedAt
- tokenExpiresAt

Server checks expiration at AUTH time.

Default token TTL recommendation: 30 minutes.

---

### Step 2.4 - Add heartbeat packets and stale detection
Heartbeat cadence:
- client heartbeat interval: 5 seconds
- stale threshold: 15 seconds

Server actions:
- update lastSeen on heartbeat and frame traffic
- disconnect stale clients with categorized timeout reason

Agent/viewer actions:
- reconnect on heartbeat timeout

---

### Step 2.5 - Add session idle timeout and cleanup
Registry cleanup loop:
- run every 60 seconds
- expire sessions with no agent and no viewers for configured idle TTL
- close residual connections and delete registry entry

Recommended idle TTL: 10 minutes.

---

### Step 2.6 - Validation for Workstream 2
- stale clients are disconnected and recover
- expired tokens are rejected
- duplicate agent joins rejected consistently
- idle sessions are cleaned without leaks

---

## Workstream 3 - Backpressure and Pacing Control

### Goal
Bound latency under slow networks/viewers by controlling queue depth, frame drop policy, FPS, and quality.

### Files involved
- internal/agent/scheduler/scheduler.go
- internal/agent/encoder/jpeg.go
- internal/agent/transport/ws.go
- internal/server/router/fanout.go
- internal/server/transport/viewer_handler.go
- internal/server/session/session.go
- web/viewer/src/renderer.ts

---

### Step 3.1 - Split agent pipeline into bounded stages
Create three bounded channels:
- captureOut
- encodeOut
- sendOut

Recommendation:
- capacity 1 to 3 per stage to favor freshness and avoid queue growth

---

### Step 3.2 - Implement explicit drop policy
Policy: newest-frame-wins under pressure.

Rules:
- if stage queue full, drop oldest queued frame and enqueue latest
- increment drop counters by stage
- log drops at controlled interval, not per-frame spam

---

### Step 3.3 - Add adaptive FPS control
Inputs:
- queue depth ratio
- send latency moving average
- drop rate over rolling window

Controller behavior:
- reduce target FPS when pressure sustained
- slowly recover FPS after stabilization

Suggested bounds:
- min FPS 5
- max FPS from configured target

---

### Step 3.4 - Add adaptive JPEG quality control
Inputs:
- send latency and drop rate

Behavior:
- reduce quality in steps under congestion
- recover quality gradually when healthy

Suggested bounds:
- min quality 45
- max quality from configured target

---

### Step 3.5 - Bound server fanout behavior
Per-viewer queue must stay bounded.

Rules:
- never block agent ingress on slow viewer
- drop per-viewer when outbound channel full
- expose per-viewer and per-session drop counters

---

### Step 3.6 - Validation for Workstream 3
- queue depth remains bounded under induced slow viewer
- latency does not grow unbounded
- frame drops are measurable and explainable
- adaptive controls converge without oscillation spikes

---

## Workstream 4 - Observability Improvements

### Goal
Expose machine-readable metrics and structured logs sufficient for root-cause analysis.

### Files involved
- cmd/server/main.go
- internal/server/metrics/doc.go
- internal/server/metrics/prometheus.go (new)
- internal/server/router/sessions.go
- internal/agent/scheduler/scheduler.go
- internal/agent/transport/ws.go
- web/viewer/src/main.ts
- web/viewer/src/viewer.ts
- web/viewer/src/renderer.ts

---

### Step 4.1 - Add Prometheus metrics endpoint
Expose server endpoint:
- GET /metrics

Metric families:
- streamforge_frames_received_total{session_id}
- streamforge_frames_forwarded_total{session_id}
- streamforge_frames_dropped_total{session_id, reason}
- streamforge_viewers_connected{session_id}
- streamforge_session_fps{session_id}
- streamforge_transport_errors_total{role, category}

---

### Step 4.2 - Add histograms and latency buckets
Add histograms for:
- agent encode latency
- agent send latency
- server routing latency
- viewer decode/render latency

Suggested buckets (ms):
- 1, 2, 5, 10, 20, 50, 100, 200, 500

---

### Step 4.3 - Standardize structured log fields
Minimum fields:
- sessionId
- role
- frameId
- packetType
- queueDepth
- framesDropped
- errorCategory

Define canonical field names in docs and keep consistent across components.

---

### Step 4.4 - Error categorization
Define categories:
- auth
- protocol
- transport
- timeout
- backpressure
- internal

Emit category in both logs and counters.

---

### Step 4.5 - Validation for Workstream 4
- metrics endpoint scrapes without errors
- logs from agent/server/viewer share consistent keys
- histogram output reflects induced load scenarios

---

## Workstream 5 - Stability Testing

### Goal
Build confidence with repeatable tests for parsing, lifecycle, and long-running behavior.

### Files involved
- internal/protocol/*_test.go
- internal/server/session/*_test.go
- internal/server/router/*_test.go
- internal/agent/scheduler/*_test.go
- test/integration/ (new)
- scripts/ (new)

---

### Step 5.1 - Unit tests for protocol
Add tests for:
- valid encode/decode roundtrip
- short header rejection
- invalid version rejection
- unknown type rejection
- payload length mismatch rejection
- oversized payload rejection

---

### Step 5.2 - Unit tests for session lifecycle
Add tests for:
- state transitions
- heartbeat stale timeout behavior
- token expiration checks
- duplicate join behavior
- idle cleanup behavior

---

### Step 5.3 - Integration tests for agent-server-viewer flow
Build an in-process integration harness:
- spin up server
- connect mock agent and mock viewers
- send frames at controlled rates
- assert forwarding, drop counters, reconnect handling

---

### Step 5.4 - Load and soak scenarios
Add scripts for:
- multiple viewers (2, 5, 10)
- slow viewer simulation
- reconnect storm simulation
- 30 to 60 minute soak test

Capture:
- p50 and p95 end-to-end latency
- drop rates
- memory growth

---

### Step 5.5 - Validation for Workstream 5
- protocol tests pass consistently
- integration tests pass in CI and local runs
- soak test shows no unbounded memory growth
- p95 latency and drops are within accepted thresholds

---

## Recommended Execution Order

Implement in this order to reduce migration risk:
1. Workstream 1 (protocol helpers and parser safety)
2. Workstream 2 (lifecycle and heartbeat)
3. Workstream 3 (bounded pipelines and adaptation)
4. Workstream 4 (metrics and log normalization)
5. Workstream 5 (test harness and soak)

Commit strategy:
- one commit per step where feasible
- run go test ./... and npm run build between workstreams

---

## Validation Checkpoints

Checkpoint A - Protocol readiness
- all packet parsing centralized in internal/protocol
- malformed packets are rejected safely

Checkpoint B - Lifecycle readiness
- reconnect, stale timeout, and cleanup behavior are deterministic

Checkpoint C - Backpressure readiness
- queue depth and latency remain bounded under slow viewers

Checkpoint D - Observability readiness
- /metrics and structured logs explain pressure and failures

Checkpoint E - Stability readiness
- long runs and load scenarios complete without crashes or runaway memory

---

## Exit Criteria

Phase 2 is complete when all are true:
- protocol is versioned, documented, and negotiation-enforced
- runtime remains stable through repeated reconnect and slow-consumer scenarios
- frame drop and latency behavior are intentional, bounded, and measurable
- metrics and logs support quick diagnosis without code-level tracing

---

## Phase 2 Completion Checklist

- [ ] Common protocol header finalized and versioned
- [ ] Packet reader/writer helpers implemented in internal/protocol
- [ ] Protocol negotiation added to handshake
- [ ] Malformed and oversized packet rejection implemented
- [ ] Session lifecycle states and transitions implemented
- [ ] Heartbeat and stale timeout handling implemented
- [ ] Session token expiration and idle cleanup implemented
- [ ] Bounded stage queues implemented in agent pipeline
- [ ] Frame drop policy favoring freshness implemented
- [ ] Adaptive FPS and quality controls implemented
- [ ] Prometheus /metrics endpoint implemented
- [ ] Latency histograms and error-category counters implemented
- [ ] Structured log field schema normalized across components
- [ ] Unit tests for protocol and lifecycle are passing
- [ ] Integration and soak tests are passing
- [ ] p95 latency, drop rate, and memory growth documented
