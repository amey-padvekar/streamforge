# Streamforge Detailed Implementation Plan

## 1. Purpose

This document translates the Streamforge design into a practical phase-by-phase implementation plan.

It is optimized for:
- incremental delivery
- measurable milestones
- engineering clarity
- portfolio-quality execution
- low-risk expansion from MVP to platform

This plan assumes the current architecture in [streamforge-design.md](streamforge-design.md).

---

## 2. Delivery Strategy

Implementation proceeds in vertical slices:
1. establish project foundations
2. deliver an end-to-end streaming path
3. improve protocol and runtime stability
4. add remote interaction
5. platformize for reuse and integration

Each phase includes:
- objective
- outputs
- workstreams
- ordered implementation steps
- validation checkpoints
- exit criteria

---

## 3. Phase 0 — Foundations and Repository Setup

### Objective
Create a repo structure, development workflow, and baseline architecture skeleton that supports fast iteration without premature complexity.

### Outputs
- initialized repository structure
- top-level documentation set
- Go module and package layout
- frontend workspace scaffold
- shared protocol specification folder
- local development workflow
- baseline CI plan

### Workstreams
- repository structure
- backend bootstrap
- frontend bootstrap
- documentation and conventions
- build/test workflow

### Detailed steps
1. Create the top-level folder structure:
   - `cmd/agent`
   - `cmd/server`
   - `internal/server`
   - `internal/agent`
   - `internal/protocol`
   - `internal/session`
   - `internal/transport`
   - `internal/telemetry`
   - `web/viewer`
   - `docs/design`
   - `docs/adr`
2. Initialize the Go module and define package boundaries early.
3. Set coding conventions for package naming, logging, error handling, and configuration.
4. Decide how configuration is loaded:
   - environment variables
   - config file
   - command flags
5. Add a minimal frontend scaffold using Vite + TypeScript.
6. Add a root README that explains repository purpose and planned components.
7. Define ADR usage and create `docs/adr/README.md`.
8. Add baseline developer tooling:
   - formatting
   - linting
   - test commands
   - basic CI placeholders
9. Decide initial branch strategy and commit conventions.
10. Record initial architectural assumptions as ADRs.

### Validation checkpoints
- repo structure is stable and understandable
- Go packages compile as empty shells
- frontend scaffold runs locally
- docs are sufficient for new contributors

### Exit criteria
- repository supports parallel development of agent, server, and viewer
- conventions are documented
- local dev startup steps are reproducible

---

## 4. Phase 1 — End-to-End Streaming MVP

> **Detailed plan**: see [streamforge-phase1-plan.md](streamforge-phase1-plan.md) for workstream-level step-by-step implementation with file targets, struct definitions, and a completion checklist.

### Objective
Deliver the first working slice where an agent captures the screen, sends JPEG frames through the Go server, and a browser viewer renders them.

### Outputs
- live desktop capture from Windows host
- JPEG frame encoding path
- WebSocket streaming pipeline
- session creation/join flow
- browser viewer rendering frames
- basic FPS/latency telemetry

### Workstreams
- agent capture
- agent encoding
- server session routing
- binary transport framing
- viewer rendering
- minimal API surface

### Detailed steps

#### 4.1 Server skeleton
1. Create the HTTP server bootstrap in `cmd/server`.
2. Add health and readiness endpoints.
3. Add WebSocket upgrade endpoint for session traffic.
4. Implement a minimal in-memory session registry.
5. Support two roles in a session:
   - agent
   - viewer
6. Add session creation endpoint:
   - create session ID
   - create token(s)
   - return connection details
7. Add basic structured logging around connection lifecycle.

#### 4.2 Agent capture pipeline
1. Build a Windows-only capture module using GDI.
2. Implement full-screen capture into an in-memory image buffer.
3. Measure capture latency and frame size.
4. Add a simple capture loop at a fixed interval.
5. Add configuration for:
   - capture interval
   - JPEG quality
   - monitor selection placeholder
6. Confirm capture works under normal desktop activity.

#### 4.3 Agent encoding and sending
1. Encode captured frames as JPEG.
2. Add timestamps and frame sequence IDs.
3. Create an initial binary frame envelope.
4. Open a WebSocket connection from agent to server.
5. Send encoded frames continuously.
6. Handle disconnect/reconnect with simple retry logic.
7. Track encode duration and send duration.

#### 4.4 Server routing path
1. Accept agent frames into session-scoped pipelines.
2. Forward frames from one agent to one or more viewers.
3. Maintain basic fan-out logic for viewer connections.
4. Drop frames if no viewer is connected or if viewer channel is full.
5. Log session lifecycle events and frame rate counters.

#### 4.5 Viewer rendering path
1. Scaffold the browser viewer app in `web/viewer`.
2. Connect to the session WebSocket.
3. Parse the incoming binary frame envelope.
4. Convert frame payload to Blob.
5. Decode using `createImageBitmap`.
6. Render frames onto canvas.
7. Display basic connection state and FPS.

#### 4.6 Minimal telemetry
1. Track capture FPS at the agent.
2. Track frame receive/render FPS at the viewer.
3. Track basic server-side session metrics.
4. Expose one debug metrics endpoint on the server.

### Validation checkpoints
- screen changes appear in the browser in near real time
- end-to-end latency is observable and measurable
- multiple reconnect cycles do not crash the pipeline
- system survives several minutes of sustained streaming

### Exit criteria
- end-to-end demo works reliably on one machine/network path
- architecture is separated enough to iterate without rewrite
- known bottlenecks are documented

---

## 5. Phase 2 — Protocol and Runtime Stability

> Detailed plan: see [streamforge-phase2-plan.md](streamforge-phase2-plan.md) for workstream-level implementation steps, packet model details, validation checkpoints, and a completion checklist.

### Objective
Replace the MVP transport shortcuts with a stable binary protocol and improve runtime behavior under pressure.

### Outputs
- formalized protocol messages
- versioned packet header
- bounded queues
- backpressure handling
- adaptive FPS and quality controls
- stronger session lifecycle handling
- structured metrics/logging

### Workstreams
- protocol hardening
- queue and scheduler control
- observability
- stability testing

### Detailed steps

#### 5.1 Protocol formalization
1. Finalize the common packet header.
2. Define packet type constants and ownership rules.
3. Specify binary encoding details:
   - byte order
   - timestamp semantics
   - maximum frame size
   - reserved flags
4. Implement packet reader/writer helpers in `internal/protocol`.
5. Add parser validation and malformed packet rejection.
6. Add protocol negotiation during connection/auth handshake.
7. Document protocol evolution rules.

#### 5.2 Session lifecycle hardening
1. Add explicit connection state transitions.
2. Define behavior for:
   - agent disconnect
   - viewer disconnect
   - duplicate agent joins
   - expired session tokens
3. Add heartbeat packets for liveness and RTT measurement.
4. Add session expiration and cleanup rules.
5. Add idle timeout handling.

#### 5.3 Backpressure and pacing
1. Introduce bounded channels between capture, encode, and send stages.
2. Implement frame dropping policy favoring freshness.
3. Add scheduler logic to lower FPS under pressure.
4. Add quality reduction rules under congestion.
5. Instrument queue depth and drop counters.
6. Validate that latency stays bounded when the viewer slows down.

#### 5.4 Observability improvements
1. Add Prometheus-style metrics endpoint.
2. Add structured log fields for:
   - session ID
   - connection role
   - frame ID
   - queue depth
   - dropped frame count
3. Add latency histograms.
4. Add error categorization for transport, auth, and protocol faults.

#### 5.5 Stability testing
1. Write unit tests for protocol parsing and serialization.
2. Write integration tests for agent-server-viewer flows.
3. Add load scenarios with multiple viewers.
4. Record p50/p95 latency and memory growth.

### Validation checkpoints
- protocol is documented and versioned
- queue growth is bounded under slow consumers
- system recovers cleanly from reconnects and invalid packets
- metrics can explain performance bottlenecks

### Exit criteria
- runtime remains stable under repeated long-running sessions
- latency and drop behavior are understandable and controlled
- protocol changes can be made without ad hoc breakage

---

## 6. Phase 3 — Remote Interaction

### Objective
Make sessions interactive by allowing authorized viewers to control the host machine safely.

### Outputs
- mouse input forwarding
- keyboard input forwarding
- permission-aware control model
- input event validation
- monitor selection and resize support

### Workstreams
- input protocol
- input capture in viewer
- input injection in agent
- authorization and permissions
- session UX behaviors

### Detailed steps

#### 6.1 Input protocol
1. Define `INPUT` packet variants:
   - mouse move
   - mouse down/up
   - wheel
   - key down/up
2. Decide coordinate normalization strategy.
3. Define how modifier keys are represented.
4. Add validation and bounds checks.

#### 6.2 Viewer input capture
1. Capture pointer movement relative to canvas.
2. Normalize viewer coordinates to host display coordinates.
3. Capture click, drag, wheel, and keyboard events.
4. Prevent conflicting browser-default shortcuts where appropriate.
5. Send input packets only when the viewer has control permission.

#### 6.3 Agent input injection
1. Implement Windows mouse injection.
2. Implement Windows keyboard injection.
3. Protect against invalid or dangerous event sequences.
4. Add focus and safety constraints where needed.
5. Log control actions at an operational level.

#### 6.4 Permission model
1. Introduce session roles:
   - view-only
   - control-enabled
   - owner/admin
2. Enforce permission checks server-side.
3. Expose session metadata to viewers.
4. Allow control to be granted/revoked during a session.

#### 6.5 Display management
1. Add monitor enumeration in the agent.
2. Add active monitor switch packets.
3. Add resolution change handling.
4. Handle viewer canvas resize gracefully.

### Validation checkpoints
- viewer input causes expected host behavior
- unauthorized viewers cannot send control events
- multi-monitor switching works without session restart
- input latency remains acceptable

### Exit criteria
- Streamforge supports reliable remote interaction
- control permissions are enforced and observable
- display changes are reflected cleanly in the viewer

---

## 7. Phase 4 — Platformization and SDKs

### Objective
Turn the implementation into reusable infrastructure that other applications can embed and integrate.

### Outputs
- browser SDK package
- stable public API contracts
- event subscription hooks
- integration documentation
- extension/plugin direction

### Workstreams
- SDK packaging
- public API stabilization
- integration ergonomics
- platform documentation

### Detailed steps

#### 7.1 Browser SDK extraction
1. Extract viewer connection logic into a reusable client package.
2. Extract rendering lifecycle into composable APIs.
3. Expose methods for:
   - connect
   - disconnect
   - attach canvas
   - enable/disable control
   - subscribe to metrics/events
4. Add reconnection behavior and error callbacks.
5. Publish typed event definitions.

#### 7.2 Public API stabilization
1. Review REST and WebSocket contracts for external consumers.
2. Freeze naming conventions and response formats.
3. Add versioning strategy for public APIs.
4. Document supported integration patterns.

#### 7.3 Extensibility hooks
1. Define internal event bus or callback extension points.
2. Add telemetry/event subscription hooks.
3. Design plugin boundaries without overbuilding a plugin system.
4. Reserve extension points for recording, analytics, and custom auth.

#### 7.4 Integration documentation
1. Add quick-start integration docs.
2. Add session lifecycle diagrams.
3. Add deployment guidance for local and small hosted setups.
4. Add troubleshooting guidance for latency, bandwidth, and disconnects.

### Validation checkpoints
- external app can embed the browser SDK with minimal setup
- public APIs are documented and stable enough for third-party use
- future extension points are clear but not over-engineered

### Exit criteria
- Streamforge is credibly reusable as infrastructure
- integration surface is understandable and documented

---

## 8. Phase 5 — Advanced Efficiency Enhancements

### Objective
Improve bandwidth efficiency and scalability after the core system is reliable.

### Outputs
- dirty rectangle transport mode
- improved frame differencing options
- better scheduling heuristics
- optional transport and encoding experiments

### Workstreams
- selective update strategies
- performance profiling
- advanced transport experiments

### Detailed steps
1. Implement dirty rectangle detection at the agent.
2. Extend frame packet format to support region-based updates.
3. Add viewer-side composition for partial updates.
4. Compare bandwidth and CPU cost against full-frame JPEG mode.
5. Prototype delta compression strategy.
6. Re-profile memory, CPU, and latency across realistic workloads.
7. Decide whether to keep JPEG-only, hybrid, or multi-mode transport.

### Validation checkpoints
- bandwidth decreases meaningfully on low-motion screens
- complexity increase is justified by measurable gains
- fallback to full-frame mode remains reliable

### Exit criteria
- at least one advanced efficiency strategy proves production value
- the system retains compatibility and maintainability

---

## 9. Cross-Phase Technical Tracks

These tracks progress continuously across phases rather than belonging to a single milestone.

### 9.1 Testing track
- Unit tests for protocol, sessions, and scheduler logic
- Integration tests for agent/server/viewer flow
- Regression tests for disconnect/reconnect behavior
- Soak tests for long-running memory stability

### 9.2 Performance track
- Benchmark capture, encode, network send, and render stages
- Track p50/p95 end-to-end latency
- Track GC impact and allocation rate
- Compare FPS stability under stress

### 9.3 Security track
- Validate session tokens and origin rules
- Add rate limits and abuse controls
- Audit input control permissions
- Prepare future auth model decisions

### 9.4 Documentation track
- Keep design and implementation docs aligned
- Add ADRs for major decisions
- Maintain protocol spec revisions
- Record known limitations and deferred work

---

## 10. Suggested Repository Evolution

### Initial structure
```text
cmd/
  agent/
  server/
internal/
  agent/
    capture/
    encoder/
    input/
    scheduler/
    telemetry/
    transport/
  protocol/
  server/
    auth/
    metrics/
    router/
    session/
    transport/
web/
  viewer/
docs/
  design/
  adr/
```

### Later additions
```text
test/
examples/
sdk/
  browser/
benchmarks/
```

---

## 11. Milestone Sequence

### Milestone A
Repository and scaffolding complete.

### Milestone B
First frame visible in browser.

### Milestone C
Stable session routing and bounded queues.

### Milestone D
Protocol versioning and telemetry complete.

### Milestone E
Remote input works with permissions.

### Milestone F
Browser SDK and public integration docs exist.

### Milestone G
Advanced efficiency features validated.

---

## 12. Recommended Immediate Next Steps

Execute these next in order:
1. scaffold repo directories and Go module
2. initialize `cmd/server` and `cmd/agent`
3. scaffold `web/viewer` with Vite + TypeScript
4. implement minimal session creation and WebSocket join path
5. implement Windows GDI screen capture proof of concept
6. render the first JPEG frame in browser
7. measure baseline latency/FPS before optimizing

These steps produce the fastest path to a visible, testable MVP while preserving the architecture needed for later phases.
