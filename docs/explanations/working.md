## High-level flow of Streamforge

Streamforge has **three main parts**:

1. **Agent** on the host machine  
2. **Streaming server** in the middle  
3. **Browser viewer** on the client side  

## End-to-end flow

### 1. Session is created
A session is created through the server.

The server:
- generates a **session ID**
- issues connection details/tokens
- prepares in-memory session state

This session becomes the logical link between:
- one **agent**
- one or more **viewers**

---

### 2. Agent connects to the server
The agent runs on the machine being streamed.

It:
- connects to the server over **WebSocket**
- authenticates into the session
- starts the screen capture pipeline

Its job is to:
- capture frames
- encode them
- send them continuously

---

### 3. Viewer connects to the same session
The browser viewer also connects to the server over **WebSocket**.

It:
- joins the session as a viewer
- waits for incoming frame packets
- prepares to render them onto a canvas

So the server becomes the broker between producer and consumers.

---

### 4. Agent captures the desktop
The agent captures the desktop on a loop.

In MVP form:
- full-screen capture
- fixed capture interval
- Windows GDI capture
- JPEG encoding

Pipeline:

```text
Screen Capture
  -> Raw Image Buffer
  -> JPEG Encode
  -> Binary Frame Packet
  -> WebSocket Send
```

---

### 5. Server receives and routes frames
The server does not do heavy rendering work.

Its main role is to:
- receive frame packets from the agent
- associate them with the correct session
- fan them out to connected viewers
- enforce session rules
- apply backpressure logic

This is the center of session management.

---

### 6. Viewer decodes and renders frames
The browser viewer receives binary frame packets.

It then:
- extracts the JPEG payload
- converts it into a `Blob`
- decodes it with `createImageBitmap`
- draws it onto an HTML canvas

Pipeline:

```text
Binary Packet
  -> JPEG Bytes
  -> Blob
  -> ImageBitmap
  -> Canvas Draw
```

This keeps rendering simple and browser-compatible.

---

## Remote interaction flow

Once control is added:

### 7. Viewer captures user input
The viewer captures:
- mouse movement
- clicks
- wheel events
- keyboard events

It sends these as **INPUT packets** to the server.

---

### 8. Server validates and forwards input
The server checks:
- session membership
- permissions
- packet validity

If allowed, it forwards the input event to the agent.

---

### 9. Agent injects input on the host
The agent receives the input packet and injects it into the host OS.

That completes the interaction loop:

```text
Viewer Input
  -> Server Validation
  -> Agent Injection
  -> Host Changes
  -> New Frame Captured
  -> Viewer Sees Update
```

---

## Control and reliability loop

Streamforge is not just frame forwarding. It also manages runtime pressure.

### Backpressure handling
If rendering/networking slows down:
- queues stay bounded
- stale frames are dropped
- FPS may be reduced
- JPEG quality may be reduced

The goal is:
- **freshness over completeness**
- low latency over perfect delivery

---

## Observability loop

Across the pipeline, the system tracks:
- capture FPS
- encode time
- send time
- render FPS
- dropped frames
- queue depth
- latency
- bandwidth

This makes performance bottlenecks visible.

---

## In one sentence

Streamforge works as a **real-time capture -> encode -> route -> render -> interact** pipeline, with the server managing sessions and transport while the agent handles screen/input and the browser handles viewing/control.

---

## Related documents

- Sequence diagram source: [docs/explanations/sequenceDiagram.txt](docs/explanations/sequenceDiagram.txt)
- Phase 1 lifecycle walkthrough: [docs/explanations/phase1-lifecycle.md](docs/explanations/phase1-lifecycle.md)