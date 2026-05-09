# Phase 1 lifecycle: end-to-end streaming MVP

## A. Session bootstrap
1. A client or operator creates a session through the server.
2. The server creates:
   - `sessionId`
   - agent auth token
   - viewer auth token or join token
   - in-memory session record
3. The session starts in a waiting state until the agent connects.

## B. Agent join
1. The agent starts on the host machine.
2. It opens a WebSocket connection to the server.
3. It sends an auth packet with:
   - protocol version
   - role = agent
   - session ID
   - token
4. The server validates the token and binds the agent to the session.
5. The server marks the session as agent-ready.

## C. Viewer join
1. The browser opens the viewer page.
2. The viewer SDK connects to the same session over WebSocket.
3. It authenticates as a viewer.
4. The server validates access and adds the viewer to the session viewer list.
5. The viewer waits for incoming frame packets.

## D. Frame production on the agent
1. The capture loop runs on a fixed interval.
2. The agent captures the full desktop using Windows GDI.
3. The raw frame is passed to the encoder.
4. The encoder compresses the frame as JPEG.
5. The agent wraps the JPEG bytes in a binary `FRAME` packet with metadata:
   - frame ID
   - width
   - height
   - timestamp
   - quality
   - payload length

## E. Frame transport to the server
1. The agent sends the frame packet over WebSocket.
2. The server reads the packet from the agent connection.
3. The server validates:
   - session is active
   - packet type is valid
   - payload size is acceptable
4. The server records basic metrics:
   - receive time
   - frame size
   - per-session throughput

## F. Frame routing to viewers
1. The server looks up the session.
2. It fans the frame out to each connected viewer.
3. Each viewer has a bounded outbound queue.
4. If a viewer is slow:
   - stale frames may be dropped
   - queue growth is limited
   - viewer latency is prevented from poisoning the session

## G. Frame consumption in the browser
1. The browser receives the binary frame packet.
2. The viewer extracts the JPEG payload.
3. It creates a `Blob`.
4. It decodes the blob using `createImageBitmap`.
5. It draws the bitmap to a canvas.
6. The browser shows the latest frame.

## H. Continuous loop behavior
This repeats continuously:

```text
Capture -> Encode -> Send -> Route -> Decode -> Render
```

The loop target for Phase 1 is roughly:
- 15 to 30 FPS
- low latency
- stable memory usage
- no unbounded queues

## I. Input path in Phase 1
Phase 1 may keep input disabled or stubbed, but the lifecycle is:

1. Viewer captures mouse or keyboard event.
2. Viewer sends `INPUT` packet to server.
3. Server validates permission.
4. Server forwards event to agent.
5. Agent injects input into the host OS.
6. Resulting screen change appears in later frames.

## J. Health and recovery
During the stream:
1. Agent and viewer send heartbeats.
2. The server tracks disconnects and liveness.
3. If the viewer disconnects:
   - session remains active if agent is still connected
4. If the agent disconnects:
   - session becomes unavailable
   - viewers can be notified with a control or error packet

## K. Phase 1 success path
A successful Phase 1 lifecycle looks like this:

1. Session created
2. Agent authenticates
3. Viewer authenticates
4. Agent continuously sends JPEG frames
5. Server routes frames with bounded buffering
6. Viewer renders frames in near real time
7. Metrics confirm stable FPS and acceptable latency
