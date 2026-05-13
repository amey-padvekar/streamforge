# Streamforge Usage & Testing Guide

## Overview

Streamforge is an end-to-end desktop streaming MVP that captures your Windows desktop, encodes it as JPEG frames, transmits them over WebSocket through a server, and renders them in a browser viewer. It includes built-in telemetry to observe pipeline performance.

**Phase 1 Deliverables:**
- Live remote desktop visible in browser
- Observable end-to-end latency via structured logging
- Graceful reconnect with exponential backoff
- Sustained streaming without degradation

---

## Architecture

```
┌─────────────┐         ┌────────────┐         ┌─────────────┐
│   Agent     │────────▶│   Server   │◀────────│   Viewer    │
│  (Windows)  │ Binary  │(Go HTTP +  │ Binary  │  (Browser)  │
│ GDI Capture │ Frames  │ WebSocket) │ Frames  │   Canvas    │
└─────────────┘         └────────────┘         └─────────────┘
   │ Capture            │ Session                │ Decode
   │ JPEG Encode        │ Routing                │ Render
   │ WebSocket Send     │ Fan-out                │ FPS Track
```

---

## Prerequisites

### System Requirements
- **Windows 10 or later** (agent requires Windows API)
- **Go 1.21+** (to build agent and server)
- **Node.js 18+ and npm** (to build viewer)
- **Modern web browser** (Chrome, Firefox, Safari, Edge)

### Dependencies

The project uses:
- `github.com/gorilla/websocket` — WebSocket transport
- `golang.org/x/sys/windows` — Windows GDI screen capture
- `image/jpeg` — JPEG encoding (Go standard library)
- Vite + TypeScript — Browser viewer build

All Go dependencies are declared in `go.mod` and installed automatically with `go get`.

---

## Project Structure

```
streamforge/
├── cmd/
│   ├── agent/
│   │   └── main.go              # Agent entrypoint
│   └── server/
│       └── main.go              # Server entrypoint
├── internal/
│   ├── agent/
│   │   ├── capture/             # GDI screen capture
│   │   ├── encoder/             # JPEG encoding
│   │   ├── scheduler/           # Capture loop driver + agent telemetry
│   │   └── transport/           # WebSocket client + frame envelope
│   ├── server/
│   │   ├── session/             # Session state + metrics
│   │   ├── router/              # Session REST + frame fan-out
│   │   └── transport/           # WebSocket server + handlers
│   └── protocol/                # Binary protocol definitions
├── web/viewer/
│   ├── src/
│   │   ├── main.ts              # UI wiring
│   │   ├── viewer.ts            # WebSocket viewer client
│   │   ├── renderer.ts          # Canvas rendering + viewer telemetry
│   │   ├── protocol.ts          # Frame header parser
│   │   └── style.css            # Overlay styling
│   ├── index.html               # Viewer HTML layout
│   ├── package.json
│   └── tsconfig.json
├── go.mod                        # Go module definition
└── docs/
    └── design/
        ├── streamforge-phase1-plan.md   # Detailed implementation plan
        └── explanations/                 # Protocol and lifecycle docs
```

---

## Build Instructions

### 1. Install Go Dependencies

From the project root:

```bash
go mod download
```

This downloads `github.com/gorilla/websocket` and `golang.org/x/sys/windows`.

### 2. Build Server and Agent

```bash
go build -o streamforge-server ./cmd/server
go build -o streamforge-agent ./cmd/agent
```

Or use `go run` to run directly without building:

```bash
go run ./cmd/server
go run ./cmd/agent
```

### 3. Build Viewer

From the `web/viewer` directory:

```bash
cd web/viewer
npm install
npm run build
```

This produces optimized viewer assets in `dist/`.

For development with live reload:

```bash
npm run dev
```

---

## Running the Full Integration Test

Follow this sequence **in order** to test the complete end-to-end pipeline:

### Step 1: Start the Server

Open **Terminal 1** and run:

```bash
go run ./cmd/server
```

**Expected output:**
```
time=2026-05-10T14:00:00.000Z level=INFO msg="streamforge server starting" addr=:8080
```

The server listens on `http://localhost:8080` by default. Override with:

```bash
SERVER_ADDR=:9000 go run ./cmd/server
```

### Step 2: Create a Session

Open **Terminal 2** and create a session via REST API:

**PowerShell:**
```powershell
$session = Invoke-RestMethod -Method Post -Uri http://localhost:8080/api/sessions
$session | ConvertTo-Json
```

**Bash/curl:**
```bash
curl -X POST http://localhost:8080/api/sessions
```

**Expected response:**
```json
{
  "sessionId": "abc123def456...",
  "agentToken": "xyz789uvw012...",
  "viewerToken": "pqr345stu678...",
  "wsUrl": "ws://localhost:8080/ws/session/abc123def456..."
}
```

**Save these values** — you'll need them for the agent and viewer.

### Step 3: Start the Agent

Open **Terminal 3** and start the agent with the credentials from Step 2:

```powershell
$env:SERVER_URL = 'ws://localhost:8080'
$env:SESSION_ID = 'abc123def456...'    # From sessionId above
$env:AGENT_TOKEN = 'xyz789uvw012...'   # From agentToken above
$env:TARGET_FPS = '20'
$env:JPEG_QUALITY = '75'

go run ./cmd/agent
```

**Expected output:**
```
time=2026-05-10T14:00:05.000Z level=INFO msg="streamforge agent starting" serverURL=ws://localhost:8080 sessionID=abc123def456... targetFPS=20 jpegQuality=75
time=2026-05-10T14:00:05.500Z level=INFO msg="agent websocket connected" sessionId=abc123def456... url=ws://localhost:8080/ws/session/abc123def456...
time=2026-05-10T14:00:10.000Z level=INFO msg="scheduler telemetry" targetFPS=20 actualCaptureFPS=5.42 jpegEncodeAvgMs=42.15 sendLatencyAvgMs=2.38
```

### Step 4: Open the Browser Viewer

Open **Terminal 4** and start the viewer dev server:

```bash
cd web/viewer
npm run dev
```

**Expected output:**
```
  VITE v6.4.2  ready in 123 ms

  ➜  Local:   http://localhost:5173/
  ➜  press h to show help
```

### Step 5: Connect Viewer to Session

1. Open http://localhost:5173 in your browser
2. Fill in the form:
   - **Server URL:** `ws://localhost:8080`
   - **Session ID:** `abc123def456...` (from Step 2)
   - **Viewer Token:** `pqr345stu678...` (from Step 2)
3. Click **Connect**

**Expected UI updates:**
- Status changes to `connected` (green)
- Canvas starts rendering live desktop frames
- FPS counter shows render rate (usually 5–10 fps on Phase 1)
- Frame dimensions display (e.g., "1920 x 1080")

---

## Observing Telemetry

### Agent Telemetry (Terminal 3)

Every 5 seconds, the agent logs:

```
msg="scheduler telemetry"
targetFPS=20              # Target capture FPS
actualCaptureFPS=5.42     # Actual captured FPS (may be lower due to GDI overhead)
jpegEncodeAvgMs=42.15     # Moving average encode time in milliseconds
sendLatencyAvgMs=2.38     # Moving average frame send latency
```

**What to observe:**
- `actualCaptureFPS` is usually 5–8 on Phase 1 (GDI is not optimized)
- `jpegEncodeAvgMs` increases with screen resolution and quality
- `sendLatencyAvgMs` reflects network round-trip time (usually <10ms on localhost)

### Server Telemetry (Terminal 1)

Every 5 seconds, the server logs per-session metrics:

```
msg="session telemetry"
sessionId=abc123def456...
fps=5.4                   # Computed FPS from frames received
framesReceived=27         # Total frames received from agent
framesForwarded=27        # Total frames forwarded to viewers
framesDropped=0           # Frames dropped due to full viewer queues
viewerCount=1             # Active viewer connections
```

**What to observe:**
- `fps` tracks actual frame delivery through the server
- `framesDropped` should stay 0 unless viewers are slow
- `viewerCount` increases/decreases as viewers connect/disconnect

### Viewer Telemetry (Browser DevTools Console)

If renders are dropped (frames arriving while previous decode is in flight):

```
WARN: render dropped because previous frame is still decoding
{droppedFrames: 2, frameWidth: 1920, frameHeight: 1080}
```

**UI overlay shows:**
- **Status:** Connection state (disconnected / connecting / connected / reconnecting / error)
- **FPS:** Render frames per second (updated every 1 second)
- **Frame:** Current decoded frame dimensions (e.g., "1920 x 1080")

---

## Testing Reconnect Behavior

### Test 1: Agent Reconnect

1. Ensure agent and viewer are connected and streaming (5+ seconds)
2. Stop the server: press `Ctrl+C` in Terminal 1
3. Observe agent logs (Terminal 3):
   ```
   msg="agent reconnect started" sessionId=abc123def456...
   msg="agent reconnect backoff" sessionId=abc123def456... attempt=1 wait=500ms
   msg="agent reconnect backoff" sessionId=abc123def456... attempt=2 wait=1s
   ...
   ```
4. After 5 reconnect attempts, the agent logs:
   ```
   msg="agent reconnect failed" maxRetries=6 error="dial failed: ..."
   ```
5. Restart the server: `go run ./cmd/server` in Terminal 1
6. Agent automatically reconnects:
   ```
   msg="agent websocket reconnected" sessionId=abc123def456... reconnectCount=1
   ```
7. Viewer resumes receiving frames

### Test 2: Viewer Reconnect

1. Ensure streaming is active
2. Close the browser tab or press Disconnect in the overlay
3. Viewer logs (browser console):
   ```
   onclose event fired, scheduling reconnect
   ```
4. Reopen http://localhost:5173 and reconnect
5. Stream resumes from the same session

### Test 3: Multiple Viewers

1. Keep Terminal 4 open with the first viewer still connected
2. Open http://localhost:5173 in a **new browser tab** or **different browser**
3. Fill in the same session credentials and connect
4. Both viewers render the desktop simultaneously
5. Server logs show:
   ```
   msg="session telemetry"
   viewerCount=2
   framesForwarded=54  # 27 frames × 2 viewers
   ```

---

## Metrics Endpoint

### Query Session Metrics

```bash
curl http://localhost:8080/api/sessions/{sessionId}/metrics
```

**Response:**
```json
{
  "sessionId": "abc123def456...",
  "framesReceived": 27,
  "framesForwarded": 27,
  "framesDropped": 0,
  "viewerCount": 1
}
```

### List Active Sessions

```bash
curl http://localhost:8080/api/sessions
```

**Response:**
```json
{
  "sessions": [
    {
      "sessionId": "abc123def456...",
      "hasAgent": true,
      "viewerCount": 1
    }
  ]
}
```

---

## Configuration

### Agent Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `SERVER_URL` | (required) | WebSocket server URL (e.g., `ws://localhost:8080`) |
| `SESSION_ID` | (required) | Session ID from `POST /api/sessions` |
| `AGENT_TOKEN` | (required) | Agent token from `POST /api/sessions` |
| `TARGET_FPS` | `20` | Target capture FPS (1–60) |
| `JPEG_QUALITY` | `75` | JPEG quality (1–100) |

### Server Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `SERVER_ADDR` | `:8080` | HTTP server listen address |
| `PORT` | (unused) | Alternative to `SERVER_ADDR` |

### Viewer

The viewer reads session credentials from the HTML form. No environment setup required; all configuration is done via the UI.

---

## Performance Expectations (Phase 1)

These are intentional baselines, not bugs:

| Metric | Expected | Notes |
|--------|----------|-------|
| Capture FPS | 5–8 fps | GDI capture has overhead; DXGI upgrade in Phase 2 |
| Encode Time | 30–50 ms | Depends on resolution and JPEG quality |
| Send Latency | <10 ms | Localhost; higher on real networks |
| Frame Size | 200–400 KB | JPEG + 15-byte header; varies with scene complexity |
| Viewer Render | 60+ fps | Browser can render faster than frames arrive |
| Memory | Stable | No leaks observed over 2+ minutes sustained streaming |

---

## Troubleshooting

### "dial failed: connection refused"

**Cause:** Server not running  
**Fix:** Start server first: `go run ./cmd/server`

### Agent connects then disconnects immediately

**Cause:** Invalid credentials  
**Fix:** Verify `SESSION_ID` and `AGENT_TOKEN` match the POST /api/sessions response

### Viewer shows "error" status

**Cause:** WebSocket connection failed  
**Fix:** 
- Check browser console for connection error details
- Verify server URL is correct (e.g., `ws://localhost:8080`)
- Ensure server is running and listening on the expected port

### Canvas is blank

**Cause:** Agent not connected to session  
**Fix:**
- Confirm agent is running: check Terminal 3 logs
- Verify agent is using the same session credentials
- Query `/api/sessions/{id}/metrics` to confirm `framesReceived > 0`

### Choppy or stuttering video

**Cause:** Network latency or high JPEG quality  
**Fix:**
- Lower `JPEG_QUALITY` to 50–60 (uses less bandwidth)
- Run agent and server on same machine (localhost) to minimize latency
- Check `sendLatencyAvgMs` in agent logs; if >50ms, network may be congested

### FPS shows 0 in browser

**Cause:** Renders are still queued or decoding is very slow  
**Fix:**
- Wait 1–2 seconds (FPS updates every 1 second)
- Lower JPEG quality if browser is CPU-bound
- Check browser DevTools Performance tab for decode bottlenecks

### High dropped frames in console

**Cause:** Viewer decoding slower than frames arrive  
**Fix:**
- Lower `JPEG_QUALITY` on agent to reduce decode time
- Reduce `TARGET_FPS` to reduce frame arrival rate
- Close other browser tabs to free CPU

---

## Development Notes

### Modifying Target FPS

To increase frame rate (requires more CPU):

```powershell
$env:TARGET_FPS = '30'  # Requires faster capture + encode
```

On Phase 1 (GDI capture), expect actual FPS to be lower than target due to Windows API overhead.

### Changing JPEG Quality

Higher quality = slower encode + larger frames:

| Quality | Typical Size | Encode Time | Notes |
|---------|--------------|-------------|-------|
| 50 | 150–200 KB | 15–20 ms | Very compressed; visible artifacts |
| 75 | 250–350 KB | 30–40 ms | Good balance (default) |
| 90 | 400–600 KB | 50–80 ms | High quality; slow on Phase 1 |

### Monitoring Memory

To check for memory leaks during extended streaming:

**Browser DevTools:**
1. Open DevTools → Memory tab
2. Take a heap snapshot
3. Stream for 2+ minutes
4. Take another snapshot
5. Compare sizes; should remain ~constant

**Go (agent/server):**
Use `pprof` to profile:
```bash
# Build with pprof support (this is built-in)
go run ./cmd/agent
# In another terminal
curl http://localhost:6060/debug/pprof/heap > heap.prof
go tool pprof heap.prof
```

---

## Next Steps (Future Phases)

- **Phase 2:** Hardware video encoding (H.264/H.265), bounded queues, token expiry
- **Phase 3:** Multi-monitor support, keyboard/mouse input, audio
- **Phase 4:** Audio streaming, latency optimization
- **Phase 5:** DXGI dirty-rect capture, GPU encoding

---

## Support & Debugging

For detailed protocol and lifecycle documentation, see:
- `docs/design/streamforge-phase1-plan.md` — Implementation plan with step-by-step details
- `docs/explanations/phase1-lifecycle.md` — Frame lifecycle walkthrough
- `docs/explanations/sequenceDiagram.txt` — Message sequence diagrams

For implementation details:
- `internal/agent/scheduler/scheduler.go` — Capture loop and telemetry
- `internal/server/session/session.go` — Session state and metrics
- `web/viewer/src/renderer.ts` — Viewer rendering and drop detection

