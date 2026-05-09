# Streamforge Design Document

## 1. Vision

Streamforge is a modular, low-latency remote display streaming platform built in Go for real-time desktop streaming and remote interaction over a custom binary protocol on WebSockets.

It is intended to be reusable infrastructure, not a one-off screen sharing app.

### Core outcomes
- Real-time screen streaming
- Remote device interaction
- Browser-based viewing
- Third-party integrations
- Embeddable SDKs
- Scalable session management

### Engineering intent
- Systems engineering depth
- Real-time networking design
- Protocol engineering
- Performance optimization
- Concurrency and memory discipline
- Infrastructure-level architecture

---

## 2. Scope and Objectives

### 2.1 Functional objectives
- Capture desktop frames from agent host
- Compress and stream frames in real time
- Render stream in browser viewer
- Support remote mouse and keyboard input
- Maintain low end-to-end latency
- Support multiple concurrent sessions
- Provide clean integration points for external systems

### 2.2 Technical objectives
- Pure implementation over WebSockets (no WebRTC in MVP)
- Versioned binary protocol
- Modular architecture with strict boundaries
- Efficient memory and allocation behavior
- Backpressure handling and frame pacing
- Adaptive quality and transport strategy
- Extensible protocol and SDK surface

### 2.3 Portfolio objectives
- Demonstrate networking and protocol expertise
- Demonstrate concurrency and performance tradeoffs
- Demonstrate clean architecture and operational readiness

---

## 3. Non-Goals (MVP / Early Versions)

Out of scope for initial phases:
- Kubernetes/microservices/distributed clusters
- P2P NAT traversal
- H264/H265 and advanced video codecs
- Audio streaming
- Linux/macOS agent support
- Full RDP replacement
- Enterprise-scale deployment features
- Mobile clients

Guiding rule: optimize for engineering depth over feature breadth.

---

## 4. System Context

Streamforge has three runtime roles:
1. **Agent Service** (runs on host machine): capture + encode + stream + input injection
2. **Streaming Server** (Go backend): auth + session routing + protocol handling + telemetry
3. **Browser Viewer** (TypeScript frontend): receive + decode + render + input capture

### 4.1 High-level architecture

```text
+------------------------------------------------+
|                Browser Viewer                  |
|   Canvas Rendering + Input Capture + SDK       |
+------------------------▲-----------------------+
                         |
                    WebSocket
                         |
+------------------------▼-----------------------+
|              Go Streaming Server               |
|------------------------------------------------|
| Session Management                             |
| Binary Protocol Handling                       |
| Frame Routing                                  |
| Backpressure Management                        |
| Metrics & Telemetry                            |
| Authentication                                 |
+------------------------▲-----------------------+
                         |
                    WebSocket
                         |
+------------------------▼-----------------------+
|                 Agent Service                  |
|------------------------------------------------|
| Screen Capture                                 |
| JPEG Encoding                                  |
| Frame Differencing (future)                    |
| Input Injection                                |
| Frame Scheduler                                |
+------------------------------------------------+
```

---

## 5. Architecture Principles

### 5.1 Separation of concerns
Required layers:
- Capture layer
- Encoding layer
- Transport layer
- Protocol layer
- Session layer
- Viewer layer
- Control layer

Each layer must have:
- Clear responsibilities
- Testable interfaces
- Low coupling
- Replaceable implementations

### 5.2 Performance-first design
Prioritize:
- Low latency
- Stable FPS
- Minimal allocations
- Bandwidth efficiency
- Predictable memory behavior

### 5.3 Protocol stability
Protocol requirements:
- Versioned
- Binary
- Extensible
- Language agnostic

---

## 6. Technology Stack

### 6.1 Core server
| Component | Technology |
|---|---|
| Language | Go |
| HTTP | net/http |
| WebSockets | gorilla/websocket |
| Serialization | Custom binary protocol |
| Logging | slog or zap |
| Metrics | Prometheus-compatible |

### 6.2 Browser viewer
| Component | Technology |
|---|---|
| Frontend | TypeScript |
| Rendering | HTML5 Canvas |
| Transport | Native WebSocket |
| Packaging | Vite |

### 6.3 Agent
Phase 1:
- Go
- Windows GDI capture
- JPEG encoding

Future:
- DXGI Desktop Duplication
- GPU acceleration

---

## 7. Component Design

## 7.1 Agent Service

### Responsibilities
- Capture frames
- Encode/compress frames
- Detect frame changes (future optimized mode)
- Stream frames to server
- Receive and apply remote input
- Emit telemetry

### Internal modules
```text
capture/
encoder/
scheduler/
transport/
input/
telemetry/
```

### Capture pipeline
**Phase 1**
- Full-screen capture
- Fixed capture interval
- Full-frame JPEG

**Future optimization**
- Dirty rectangle detection
- Multi-monitor selection/switching
- DXGI/GPU path
- Hardware-assisted encoding

### Frame scheduler
- Maintain target FPS window (10–30 initial)
- Prevent queue buildup
- Drop stale frames under pressure
- Coordinate adaptive quality/FPS loops

## 7.2 Streaming Server

### Responsibilities
- Accept and authenticate WebSocket clients
- Maintain sessions and permissions
- Route frames agent → viewer(s)
- Route input events viewer → agent
- Track latency, loss, drops, queue pressure
- Apply rate limits and guardrails

### Internal modules
```text
server/
protocol/
session/
auth/
metrics/
router/
transport/
```

### Session model
A session represents:
```text
Agent ↔ Viewer relationship
```

Session state includes:
- `sessionId`
- agent connection state
- connected viewers
- target/actual FPS
- quality profile
- latency percentile snapshot
- active monitor + resolution
- permission scope (view/control)

## 7.3 Browser Viewer

### Responsibilities
- Receive binary frame stream
- Decode JPEG payloads efficiently
- Render to canvas at paced cadence
- Capture pointer/keyboard input
- Send control/input packets

### Rendering pipeline
```text
Binary Frame
    ↓
Blob
    ↓
ImageBitmap
    ↓
Canvas Draw
```

Avoid:
- Base64 frame transport
- unnecessary DOM churn
- synchronous decode paths

---

## 8. Protocol Design

## 8.1 Protocol goals
- Low overhead and parse simplicity
- Backward/forward compatibility via versioning
- Extensibility through typed packets and flags

## 8.2 Common packet header

```text
Version     uint8
Type        uint8
Flags       uint16
Length      uint32
Timestamp   uint64
```

Notes:
- `Length` = payload length in bytes
- `Timestamp` = sender monotonic or epoch-millis (fixed per protocol version)
- Network byte order fixed and documented

## 8.3 Packet types
| Type | Purpose |
|---|---|
| FRAME | Screen frame payload |
| INPUT | Mouse/keyboard events |
| HEARTBEAT | Liveness and RTT sampling |
| AUTH | Authentication handshake |
| METRICS | Telemetry exchange |
| QUALITY | Quality/FPS update signals |
| RESIZE | Resolution/monitor changes |
| CONTROL | Session control commands |
| ERROR | Structured error reporting |

## 8.4 Frame packet schema
```text
FrameID
Width
Height
JPEGQuality
PayloadLength
JPEGData
```

### 8.5 Protocol versioning strategy
- Negotiate protocol version during `AUTH`
- Additive evolution first (new packet types/flags)
- Maintain parser compatibility where possible
- Reserve ranges for experimental packet extensions

---

## 9. Streaming Strategy by Phase

### Phase 1: Full JPEG frames
Pros:
- Fast implementation
- Browser compatibility
- Lower complexity/risk

Cons:
- Higher bandwidth
- Re-sends unchanged regions

Decision: acceptable for MVP.

### Phase 2: Dirty rectangle updates
- Transmit only changed regions
- Reduce bandwidth and encode cost
- Improve scalability with many sessions

### Phase 3: Delta compression
- Transmit frame deltas against reference frames
- Better efficiency at complexity cost

---

## 10. Performance Targets (MVP)

| Metric | Target |
|---|---|
| FPS | 15–30 |
| End-to-end latency | <150ms typical |
| Memory growth | Stable over sustained run |
| Concurrent sessions | 5–10 |
| CPU usage | Acceptable on mid-range systems |

Target validation will use percentile reporting (e.g., p50/p95 latency).

---

## 11. Backpressure and Flow Control

### Problem
If frame production exceeds processing/transmission capacity:
- queues grow
- latency increases
- memory grows
- user experience degrades

### Strategy
1. **Bounded queues** between pipeline stages
2. **Drop stale frames** to preserve freshness
3. **Adaptive FPS** when congestion is sustained
4. **Adaptive JPEG quality** under pressure
5. **Writer pacing** to prevent transport bursts

Policy rule: freshness over completeness in real-time mode.

---

## 12. Memory Management

### Rules
- Reuse buffers aggressively
- Minimize cross-stage copying
- Avoid per-frame large allocations
- Use pooling (`sync.Pool`) where beneficial

### Guardrails
- Track allocation rate and GC pause trends
- Cap queue sizes at each stage
- Reject pathological frame sizes with protocol limits

---

## 13. Concurrency Model

Use goroutines to isolate stages:
```text
Capture Goroutine
    ↓
Encode Worker(s)
    ↓
Frame Queue
    ↓
Transport Writer
```

Concurrency principles:
- Communicate via channels
- Avoid broad shared mutable state
- Keep locks narrow and data-local
- Use cancellation contexts for lifecycle control

---

## 14. Security Model

### MVP security
- Session token authentication
- Viewer authorization checks
- Origin validation for browser clients
- Per-IP / per-session connection limits

### Future security
- JWT / OIDC support
- RBAC permission model
- Audit logs
- Encryption hardening and key rotation strategy
- Fine-grained permission scopes

---

## 15. Observability

### Metrics
- FPS (target vs actual)
- Encode duration
- End-to-end latency
- Queue depths
- Dropped frames
- Connected viewers
- Memory usage / GC stats
- Bandwidth per session

### Logging
Structured logs only with categories:
- `connection`
- `session`
- `protocol`
- `streaming`
- `auth`
- `errors`

### Tracing (optional phase)
- Packet/operation correlation IDs
- Cross-component timeline for latency decomposition

---

## 16. API and Endpoint Design

### WebSocket endpoint
```text
/ws/session/{sessionId}
```

### REST endpoints
- `POST /api/sessions` (create session)
- `GET /api/sessions` (list sessions)
- `GET /api/sessions/{id}/metrics` (session metrics)

### API design notes
- Stable response schema with explicit versioning
- Strong auth checks for control operations
- Rate limiting and abuse protection

---

## 17. SDK Strategy

### Browser SDK goals
- Simple embed and attach API
- Connection lifecycle management
- Input forwarding and reconnection handling
- Telemetry hooks for host applications

### Example integration shape (conceptual)
- Construct client with endpoint + token
- Attach to canvas/container
- Subscribe to connection/quality events

SDK responsibilities:
- transport handling
- rendering pipeline orchestration
- reconnection policy
- input mapping
- metrics exposure

---

## 18. Testing Strategy

### Unit tests
- Protocol encoding/decoding
- Header parsing and bounds checks
- Session lifecycle logic
- Scheduler and queue behavior
- Backpressure policy correctness

### Integration tests
- Agent ↔ Server stream path
- Server ↔ Viewer render path
- Input roundtrip viewer → agent
- Multi-session and disconnect/reconnect behavior

### Load / soak tests
- Session concurrency scaling
- Latency and drop behavior under load
- Memory stability over long runs

---

## 19. Delivery Phases

### Phase 1 — MVP
Deliverables:
- Screen capture
- JPEG encoding
- WebSocket streaming
- Browser rendering
- Basic sessions
- FPS telemetry

Goal: end-to-end demo.

### Phase 2 — Stability
Deliverables:
- Versioned binary protocol
- Backpressure handling
- Frame pacing
- Adaptive quality
- Structured logging
- Prometheus metrics

Goal: reliable low-latency streaming.

### Phase 3 — Remote Interaction
Deliverables:
- Mouse + keyboard control
- Session permissions
- Multi-monitor support

Goal: interactive remote system.

### Phase 4 — Platformization
Deliverables:
- Browser SDK package
- Public APIs
- Plugin/event extension points
- Integration documentation

Goal: embeddable platform infrastructure.

---

## 20. Risks and Tradeoffs

### Key risks
- JPEG full-frame bandwidth overhead in MVP
- Capture performance variance across hardware
- Tail-latency spikes during GC/CPU contention
- Input event ordering and idempotency edge cases

### Tradeoffs
- Simplicity (JPEG/WebSocket) vs bandwidth efficiency
- Freshness (frame dropping) vs visual completeness
- Faster delivery vs protocol feature richness

### Mitigations
- Early instrumentation and profiling
- Strict queue caps and adaptive controls
- Progressive protocol evolution with compatibility gates

---

## 21. Future Extensions (Post-MVP)

Potential extensions:
- H264/H265 encoding paths
- Audio channel
- Session recording/replay
- Linux/macOS agents
- Mobile viewer
- Optional WebRTC transport
- P2P transport mode
- File transfer and clipboard sync

These are explicitly out of MVP scope.

---

## 22. Success Criteria

The project is successful when:
- Browser viewer can observe a live remote desktop stream
- Latency remains consistently low under sustained operation
- Architecture remains modular and maintainable
- Protocol supports extension without breaking baseline clients
- System remains stable with target concurrent sessions
- External systems can integrate through API/SDK cleanly
- Implementation demonstrates strong systems and networking depth

---

## 23. Guiding Philosophy

```text
Engineering quality > feature quantity
Architecture clarity > premature complexity
Performance understanding > framework dependence
Real systems thinking > demo gimmicks
```

Streamforge is designed as clean, extensible, infrastructure-grade real-time streaming technology with deliberate, measurable engineering decisions.

---

## 24. Implementation Companion

The detailed phase-by-phase implementation roadmap lives in [docs/design/streamforge-implementation-plan.md](docs/design/streamforge-implementation-plan.md).

Use this document as the execution guide for translating the architecture in this design into concrete milestones, workstreams, validation checkpoints, and delivery order.
