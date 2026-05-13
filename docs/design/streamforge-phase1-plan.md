# Phase 1 — End-to-End Streaming MVP: Detailed Implementation Plan

## Overview

Phase 1 delivers the first working end-to-end streaming slice:

```text
Agent screen capture
  → JPEG encode
  → WebSocket send
  → Server session routing
  → WebSocket forward
  → Browser decode
  → Canvas render
```

**Goal**: a live desktop visible in a browser tab with observable FPS and latency.

**Exit criteria**:
- browser renders a live remote desktop stream
- end-to-end latency is observable and measurable
- reconnect cycles do not crash the pipeline
- system sustains streaming for several minutes without degradation

---

## Dependencies and Prerequisites

Before starting Phase 1, ensure Phase 0 is complete:
- `go.mod` is initialized with module name `streamforge`
- `cmd/agent/main.go` and `cmd/server/main.go` compile
- `web/viewer` has a working Vite + TypeScript scaffold
- all `internal/` packages exist as placeholders

External dependencies needed in Phase 1:
- `github.com/gorilla/websocket` — WebSocket server and client
- `golang.org/x/sys/windows` — Windows API access for GDI capture
- `image/jpeg` — standard library JPEG encoding (no extra dep)

---

## Workstream 1 — Server: Session Bootstrap

### Goal
A running HTTP server that can create sessions, issue tokens, and accept WebSocket connections from both agents and viewers.

### Files involved
- `cmd/server/main.go`
- `internal/server/session/`
- `internal/server/auth/`
- `internal/server/router/`
- `internal/server/transport/`

---

### Step 1.1 — Add gorilla/websocket dependency

Run in the project root:

```bash
go get github.com/gorilla/websocket
```

Commit dependency update before proceeding.

---

### Step 1.2 — Define the session model

Create `internal/server/session/session.go`.

The session must represent:
- `ID` — unique string identifier
- `AgentToken` — token issued to the agent
- `ViewerToken` — token issued to viewers
- `AgentConn` — pointer to the active agent WebSocket connection
- `Viewers` — map of viewer ID to viewer WebSocket connection
- `CreatedAt` — creation timestamp
- `mu` — mutex for concurrent viewer access

Define connection roles as a typed constant:
- `RoleAgent`
- `RoleViewer`

---

### Step 1.3 — Implement the session registry

Create `internal/server/session/registry.go`.

The registry must:
- hold sessions in an in-memory map keyed by session ID
- expose `Create() *Session` — generates a new session with random ID and tokens
- expose `Get(id string) (*Session, bool)`
- expose `Delete(id string)`
- protect concurrent access with a `sync.RWMutex`

Token generation:
- use `crypto/rand` to generate a random 128-bit token
- encode as hex string

---

### Step 1.4 — Implement REST session creation endpoint

Create `internal/server/router/sessions.go`.

`POST /api/sessions`:
- calls `registry.Create()`
- returns JSON:
  ```json
  {
    "sessionId": "...",
    "agentToken": "...",
    "viewerToken": "...",
    "wsUrl": "ws://host:port/ws/session/{sessionId}"
  }
  ```

`GET /api/sessions`:
- returns list of active session IDs and connection state only

`GET /api/sessions/{id}/metrics`:
- stub in Phase 1 — return an empty metrics object with `sessionId`

---

### Step 1.5 — Implement WebSocket upgrade and role routing

Create `internal/server/transport/ws.go`.

`GET /ws/session/{sessionId}`:
- upgrade the HTTP connection to WebSocket using `gorilla/websocket`
- read the first message as an auth handshake:
  - `role` — agent or viewer
  - `token`
- validate token against the session registry
- branch to agent handler or viewer handler

Auth rejection must close the connection with a protocol-level error message before disconnecting.

---

### Step 1.6 — Implement agent connection handler

Create `internal/server/transport/agent_handler.go`.

Responsibilities:
- bind the agent WebSocket connection to the session
- start a read loop for incoming frames
- pass each received frame message to the session's routing layer
- handle disconnect cleanly:
  - remove agent reference from session
  - log disconnect with session ID
- reject duplicate agent connections per session

---

### Step 1.7 — Implement viewer connection handler

Create `internal/server/transport/viewer_handler.go`.

Responsibilities:
- add the viewer WebSocket connection to the session's viewer map
- assign a viewer ID (random or counter-based)
- maintain a write loop with a bounded outbound channel per viewer
- handle viewer disconnect cleanly:
  - remove from session viewer map
  - drain outbound channel
- implement non-blocking send:
  - if the viewer channel is full, drop the frame (log the drop)

---

### Step 1.8 — Implement frame fan-out

Create `internal/server/router/fanout.go`.

Responsibilities:
- receive a raw frame byte slice from the agent handler
- iterate over all connected viewers in the session
- push the frame to each viewer's outbound channel
- skip or drop if a viewer channel is at capacity
- track a drop counter per session for telemetry

This function must be safe to call from concurrent goroutines.

---

### Step 1.9 — Wire server entrypoint

Update `cmd/server/main.go`:
- create the session registry
- register REST routes
- register WebSocket route
- configure the HTTP server with read/write timeouts
- start listening on a configurable port (default `:8080`)
- add graceful shutdown on interrupt signal

---

### Step 1.10 — Server validation

Manual checks:
- `POST /api/sessions` returns valid JSON with session ID and tokens
- `GET /healthz` returns 200
- Two WebSocket clients can connect as agent and viewer to the same session
- Log output reflects connection lifecycle events

---

## Workstream 2 — Agent: Screen Capture

### Goal
The agent captures the full Windows desktop, encodes frames as JPEG, wraps them in a binary envelope, and sends them over WebSocket to the server continuously.

### Files involved
- `cmd/agent/main.go`
- `internal/agent/capture/`
- `internal/agent/encoder/`
- `internal/agent/scheduler/`
- `internal/agent/transport/`

---

### Step 2.1 — Add Windows dependency

Add `golang.org/x/sys/windows` to `go.mod`:

```bash
go get golang.org/x/sys/windows
```

Capture code must be in a file named `capture_windows.go` with build tag:

```go
//go:build windows
```

This ensures the package compiles only on Windows.

---

### Step 2.2 — Implement GDI screen capture

Create `internal/agent/capture/capture_windows.go`.

Implementation approach:
1. Call `GetDesktopWindow()` to get the desktop handle.
2. Call `GetDC(hwnd)` to get the desktop device context.
3. Create a compatible in-memory DC with `CreateCompatibleDC`.
4. Create a compatible bitmap with `CreateCompatibleBitmap` at the display resolution.
5. Select the bitmap into the memory DC with `SelectObject`.
6. Call `BitBlt` to copy the screen into the bitmap.
7. Call `GetDIBits` to extract raw pixel bytes in BGRA format.
8. Release DCs and delete objects after each capture.
9. Return a `*image.RGBA` or raw BGRA bytes for encoding.

Expose:
- `func Capture() (*image.RGBA, error)` — single frame capture
- `func ScreenBounds() (width, height int, err error)` — current display resolution

Add a stub `doc.go` with notes for DXGI upgrade path.

---

### Step 2.3 — Implement JPEG encoder

Create `internal/agent/encoder/jpeg.go`.

Expose:
- `func EncodeJPEG(img *image.RGBA, quality int) ([]byte, error)`
  - wraps `image/jpeg.Encode` into a `bytes.Buffer`
  - returns the JPEG bytes and byte count

Add a benchmarking note: measure encode time per frame and log it for later profiling.

---

### Step 2.4 — Define the binary frame envelope

Create `internal/agent/transport/frame.go`.

The frame envelope must contain a fixed-size header followed by JPEG payload:

```text
Offset  Size  Field
0       1     Version      uint8
1       1     PacketType   uint8   (0x01 = FRAME)
2       4     FrameID      uint32  big-endian
6       2     Width        uint16  big-endian
8       2     Height       uint16  big-endian
10      1     JPEGQuality  uint8
11      4     PayloadLen   uint32  big-endian
15      N     JPEGData     []byte
```

Expose:
- `func EncodeFrame(frameID uint32, width, height uint16, quality uint8, jpeg []byte) []byte`
  - writes header fields into a pre-allocated slice
  - appends jpeg bytes
  - returns complete frame bytes
- `func DecodeFrameHeader(data []byte) (FrameHeader, error)`
  - reads the fixed-size header for validation

Use `encoding/binary` with `binary.BigEndian`.

---

### Step 2.5 — Implement WebSocket transport for agent

Create `internal/agent/transport/ws.go`.

Responsibilities:
- dial the server WebSocket endpoint with `gorilla/websocket`
- send an auth handshake message as the first message after connect:
  ```json
  {"role":"agent","token":"<agentToken>"}
  ```
- expose a `Send(data []byte) error` method for binary frame messages
- implement a reconnect loop:
  - on disconnect, wait with exponential backoff
  - attempt reconnect up to a configurable max retries
  - log reconnect events with session ID and attempt number

---

### Step 2.6 — Implement the capture scheduler

Create `internal/agent/scheduler/scheduler.go`.

The scheduler drives the capture loop at a target FPS:
- accept a target FPS (default 20)
- compute tick interval as `time.Second / fps`
- on each tick:
  1. capture frame
  2. encode to JPEG
  3. wrap in binary envelope
  4. send over WebSocket transport
- track actual FPS using a rolling counter (frame count per second window)
- if encoding or sending takes longer than the tick interval, skip the next tick rather than queue work
- log actual FPS every 5 seconds

Expose:
- `type Scheduler struct`
- `func NewScheduler(fps int, quality int, transport *WSTransport) *Scheduler`
- `func (s *Scheduler) Start(ctx context.Context)`
- `func (s *Scheduler) Stop()`

---

### Step 2.7 — Wire agent entrypoint

Update `cmd/agent/main.go`:
- read config from environment variables or flags:
  - `SERVER_URL` — WebSocket server address
  - `SESSION_ID` — session to join
  - `AGENT_TOKEN` — auth token
  - `TARGET_FPS` — default 20
  - `JPEG_QUALITY` — default 75
- initialize the WebSocket transport
- perform auth handshake
- initialize the scheduler
- start the capture loop
- handle context cancellation and shutdown on interrupt signal

---

### Step 2.8 — Agent validation

Manual checks:
- agent connects to the server and authenticates successfully
- frames appear in server logs as received
- actual FPS is logged and close to target
- agent reconnects after a deliberate server restart

---

## Workstream 3 — Browser Viewer: Rendering Pipeline

### Goal
The browser viewer connects to the server session, receives binary frame packets, decodes JPEG payloads, and renders them to a canvas element at a stable frame rate.

### Files involved
- `web/viewer/src/main.ts`
- `web/viewer/src/viewer.ts`
- `web/viewer/src/renderer.ts`
- `web/viewer/src/protocol.ts`
- `web/viewer/src/style.css`
- `web/viewer/index.html`

---

### Step 3.1 — Update index.html

Replace the current scaffold body with a viewer layout:
- a `<canvas>` element sized to fill the viewport
- an overlay `<div>` for connection status and FPS display
- a session join form with fields for:
  - server URL
  - session ID
  - viewer token
  - a connect button

---

### Step 3.2 — Implement protocol parser

Create `web/viewer/src/protocol.ts`.

Define the frame header layout as constants matching the Go binary format:

```text
HEADER_SIZE = 15 bytes
Offsets for Version, PacketType, FrameID, Width, Height, JPEGQuality, PayloadLen
```

Expose:
- `function parseFrameHeader(buffer: ArrayBuffer): FrameHeader | null`
  - uses `DataView` with `getUint8`, `getUint16`, `getUint32` (big-endian)
  - validates packet type is `0x01` (FRAME)
  - returns null on invalid or undersized buffer
- `function extractJpegPayload(buffer: ArrayBuffer, header: FrameHeader): Uint8Array`

---

### Step 3.3 — Implement the renderer

Create `web/viewer/src/renderer.ts`.

Responsibilities:
- hold a reference to the canvas and `CanvasRenderingContext2D`
- expose `render(jpeg: Uint8Array): Promise<void>`:
  1. create a `Blob` from the bytes with `type: 'image/jpeg'`
  2. call `createImageBitmap(blob)`
  3. call `ctx.drawImage(bitmap, 0, 0, canvas.width, canvas.height)`
  4. call `bitmap.close()` to release GPU memory
- track render FPS using a rolling counter and expose `getFps(): number`
- resize the canvas to match incoming frame dimensions when they change

Avoid:
- `URL.createObjectURL` / `revokeObjectURL` on every frame
- drawing to DOM `<img>` elements
- synchronous decode paths

---

### Step 3.4 — Implement the WebSocket viewer client

Create `web/viewer/src/viewer.ts`.

Responsibilities:
- open a `WebSocket` to the session endpoint:
  ```
  ws://<serverUrl>/ws/session/<sessionId>
  ```
- set `binaryType = 'arraybuffer'`
- send auth handshake as JSON string on open:
  ```json
  {"role":"viewer","token":"<viewerToken>"}
  ```
- in `onmessage`:
  1. parse the frame header
  2. extract the JPEG payload
  3. call `renderer.render(jpeg)`
- implement reconnect with backoff on `onclose`
- expose a `connect()` and `disconnect()` method
- emit connection state changes to the UI overlay

---

### Step 3.5 — Wire the viewer entrypoint

Update `web/viewer/src/main.ts`:
- wire the connect form to create a `Viewer` instance and call `connect()`
- wire the canvas to the `Renderer`
- bind FPS display to a 1-second interval reading `renderer.getFps()`
- show connection state (disconnected / connecting / connected / error) in the overlay
- handle disconnect button

---

### Step 3.6 — Viewer validation

Manual checks in the browser:
- viewer connects and shows `connected` status
- canvas renders live desktop frames
- FPS counter reflects actual render rate
- viewer reconnects automatically after server restart
- no memory growth visible in DevTools Memory tab after sustained streaming

---

## Workstream 4 — Minimal Telemetry

### Goal
Enough observability to confirm the pipeline works and identify obvious bottlenecks before optimization.

---

### Step 4.1 — Agent-side telemetry

In the scheduler:
- log actual capture FPS every 5 seconds
- log JPEG encode time as moving average
- log frame send latency (time from encode end to write completion)
- log reconnect events

Log format: structured key-value using `log/slog`.

---

### Step 4.2 — Server-side telemetry

Track per-session in the session struct:
- frames received from agent (counter)
- frames forwarded to viewers (counter)
- frames dropped due to full viewer channels (counter)
- viewer count

Expose `GET /api/sessions/{id}/metrics` returning JSON:
```json
{
  "sessionId": "...",
  "framesReceived": 0,
  "framesForwarded": 0,
  "framesDropped": 0,
  "viewerCount": 0
}
```

Log session-level FPS computed server-side every 5 seconds.

---

### Step 4.3 — Viewer-side telemetry

In the renderer:
- display render FPS in the overlay
- display frame dimensions from the parsed header
- log frame drops if `render()` is called before the previous `createImageBitmap` resolves

---

## Integration Test Sequence

Once all workstreams are complete, run through this sequence in order:

1. Start the server: `go run ./cmd/server`
2. Create a session: `POST /api/sessions` → capture `sessionId`, `agentToken`, `viewerToken`
3. Start the agent: `go run ./cmd/agent` with the session credentials
4. Open the browser viewer: `npm run dev` in `web/viewer`, connect with the session credentials
5. Observe:
   - frames render in the browser
   - agent logs show stable FPS
   - server metrics show frames received and forwarded
   - no crashes on sustained streaming for 2+ minutes
6. Disconnect and reconnect the agent → viewer should recover
7. Disconnect and reconnect the browser tab → stream should resume

---

## Known Limitations Accepted in Phase 1

These are intentional deferred items, not bugs:

| Area | Limitation | Phase fix |
|---|---|---|
| Protocol | No formal binary versioning | Phase 2 |
| Queuing | No bounded queues yet | Phase 2 |
| Backpressure | No frame dropping policy | Phase 2 |
| Capture | GDI only, no dirty rects | Phase 5 |
| Auth | Simple token, no expiry | Phase 2 |
| Concurrency | Simple mutex, not pool | Phase 2 |
| Multi-monitor | Not supported | Phase 3 |
| Input | View-only, no control | Phase 3 |

---

## Phase 1 Completion Checklist

Status: Completed (2026-05-10)

- [x] `go get gorilla/websocket` and `golang.org/x/sys/windows` added
- [x] Session registry implemented with create/get/delete
- [x] `POST /api/sessions` returns valid credentials
- [x] WebSocket endpoint accepts agent and viewer connections
- [x] Agent auth handshake validated server-side
- [x] GDI capture produces valid `image.RGBA` frames
- [x] JPEG encoding produces valid output files
- [x] Binary frame envelope encodes and decodes correctly
- [x] Agent scheduler runs at target FPS
- [x] Server fan-out delivers frames to all viewers
- [x] Browser viewer parses frame header correctly
- [x] Browser canvas renders live JPEG frames
- [x] FPS visible in browser overlay
- [x] Session metrics endpoint returns live counters
- [x] Full integration test sequence passes
- [x] No memory growth observed over 2+ minutes of streaming
