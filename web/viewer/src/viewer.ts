import {
  extractJpegPayload,
  parseFrameHeader,
  encodeHello,
  encodeAuth,
  encodeHeartbeat,
  decodeErrorPayload,
  PACKET_TYPE_ACK,
  PACKET_TYPE_ERROR,
  PACKET_TYPE_HEARTBEAT,
  PROTOCOL_HEADER_SIZE,
} from "./protocol";
import { Renderer } from "./renderer";

const BASE_RECONNECT_DELAY_MS = 500;
const MAX_RECONNECT_DELAY_MS = 8000;
const HEARTBEAT_INTERVAL_MS = 5000;
const HEARTBEAT_TIMEOUT_MS = 15000;

const VALID_TRANSITIONS: Record<ViewerConnectionState, ViewerConnectionState[]> = {
  disconnected: ["connecting"],
  connecting: ["authenticated", "stale", "disconnected"],
  authenticated: ["streaming", "stale", "disconnected"],
  streaming: ["stale", "disconnected"],
  stale: ["connecting", "disconnected"],
};

export type ViewerConnectionState =
  | "disconnected"
  | "connecting"
  | "authenticated"
  | "streaming"
  | "stale";

export interface ViewerOptions {
  serverUrl: string;
  sessionId: string;
  viewerToken: string;
  renderer: Renderer;
  onStateChange?: (state: ViewerConnectionState) => void;
}

export class Viewer {
  private readonly serverUrl: string;
  private readonly sessionId: string;
  private readonly viewerToken: string;
  private readonly renderer: Renderer;

  private socket: WebSocket | null = null;
  private reconnectTimer: number | null = null;
  private reconnectAttempt = 0;
  private heartbeatSeq = 0;
  private heartbeatIntervalTimer: number | null = null;
  private heartbeatTimeoutTimer: number | null = null;
  private shouldReconnect = true;
  private stateChangeHandler?: (state: ViewerConnectionState) => void;
  private state: ViewerConnectionState = "disconnected";
  private authed = false;
  private ready = false;

  private logFields(overrides: Partial<Record<string, unknown>> = {}): Record<string, unknown> {
    return {
      sessionId: this.sessionId,
      role: "viewer",
      frameId: 0,
      packetType: 0,
      queueDepth: 0,
      framesDropped: this.renderer.getDroppedFrames(),
      errorCategory: "internal",
      ...overrides,
    };
  }

  constructor(options: ViewerOptions) {
    this.serverUrl = options.serverUrl;
    this.sessionId = options.sessionId;
    this.viewerToken = options.viewerToken;
    this.renderer = options.renderer;
    this.stateChangeHandler = options.onStateChange;
  }

  connect(): void {
    this.shouldReconnect = true;
    this.clearReconnectTimer();
    this.openSocket(this.reconnectAttempt > 0);
  }

  disconnect(): void {
    this.shouldReconnect = false;
    this.clearReconnectTimer();
    this.clearHeartbeatTimers();

    if (this.socket) {
      this.socket.close();
      this.socket = null;
    }

    this.reconnectAttempt = 0;
    this.authed = false;
    this.ready = false;
    this.setState("disconnected");
  }

  setStateChangeHandler(handler: (state: ViewerConnectionState) => void): void {
    this.stateChangeHandler = handler;
  }

  getState(): ViewerConnectionState {
    return this.state;
  }

  private openSocket(isReconnect: boolean): void {
    if (this.socket) {
      this.socket.close();
      this.socket = null;
    }

    this.authed = false;
    this.ready = false;
    this.setState("connecting");

    const socket = new WebSocket(this.buildSessionUrl());
    socket.binaryType = "arraybuffer";
    this.socket = socket;

    socket.onopen = () => {
      if (socket !== this.socket) {
        return;
      }

      this.reconnectAttempt = 0;

      // Send HELLO packet
      try {
        const helloPacket = encodeHello("viewer", 1, 0);
        socket.send(helloPacket.buffer as ArrayBuffer);
      } catch (err) {
        console.error("failed to encode HELLO", this.logFields({ packetType: "hello", errorCategory: "protocol", error: err }));
        this.setState("stale");
        socket.close();
      }
    };

    socket.onerror = () => {
      if (socket !== this.socket) {
        return;
      }

      this.setState("stale");
    };

    socket.onclose = () => {
      if (socket !== this.socket) {
        return;
      }

      this.socket = null;

      if (!this.shouldReconnect) {
        this.clearHeartbeatTimers();
        this.setState("disconnected");
        return;
      }

      this.scheduleReconnect();
    };

    socket.onmessage = (event) => {
      if (typeof event.data === "string") {
        return;
      }

      if (event.data instanceof ArrayBuffer) {
        this.bumpHeartbeatTimeout();
        this.handleBinaryMessage(event.data);
      }
    };
  }

  private handleBinaryMessage(buffer: ArrayBuffer): void {
    if (buffer.byteLength < PROTOCOL_HEADER_SIZE) {
      console.error("message too short for header", this.logFields({ errorCategory: "protocol" }));
      return;
    }

    const view = new DataView(buffer);
    const packetType = view.getUint8(1);

    // If we haven't sent AUTH yet, expect ACK to HELLO
    if (this.state === "connecting" && !this.authed) {
      if (packetType === PACKET_TYPE_ACK) {
        // ACK to HELLO received, now send AUTH
        this.authed = true;
        try {
          const authPacket = encodeAuth("viewer", this.viewerToken);
          if (this.socket) {
            this.socket.send(authPacket.buffer as ArrayBuffer);
          }
        } catch (err) {
          console.error("failed to encode AUTH", this.logFields({ packetType: "auth", errorCategory: "protocol", error: err }));
          this.setState("stale");
          if (this.socket) {
            this.socket.close();
          }
        }
      } else if (packetType === PACKET_TYPE_ERROR) {
        const payload = new Uint8Array(buffer, PROTOCOL_HEADER_SIZE);
        const error = decodeErrorPayload(payload);
        console.error("server rejected HELLO", this.logFields({ packetType, errorCategory: "auth", reason: error?.reason, detail: error?.detail }));
        this.setState("stale");
        if (this.socket) {
          this.socket.close();
        }
      } else {
        console.warn("ignoring unsupported handshake packet before AUTH", this.logFields({ packetType, errorCategory: "protocol" }));
      }
      return;
    }

    // If we sent AUTH, expect ACK or ERROR response
    if (this.authed && !this.ready) {
      if (packetType === PACKET_TYPE_ACK) {
        this.setState("authenticated");
        this.ready = true;
        this.startHeartbeatLoop();
        return;
      } else if (packetType === PACKET_TYPE_ERROR) {
        const payload = new Uint8Array(buffer, PROTOCOL_HEADER_SIZE);
        const error = decodeErrorPayload(payload);
        console.error("server rejected AUTH", this.logFields({ packetType, errorCategory: "auth", reason: error?.reason, detail: error?.detail }));
        this.setState("stale");
        if (this.socket) {
          this.socket.close();
        }
      } else {
        console.warn("ignoring unsupported handshake packet after AUTH", this.logFields({ packetType, errorCategory: "protocol" }));
      }
      return;
    }

    // After handshake, expect FRAME packets
    if (this.ready) {
      if (packetType === PACKET_TYPE_HEARTBEAT) {
        return;
      }

      this.setState("streaming");
      this.enqueueRender(buffer);
    }
  }

  private enqueueRender(buffer: ArrayBuffer): void {
    const receivedAt = performance.now();
    const header = parseFrameHeader(buffer);
    if (!header) {
      console.warn("failed to parse frame header", this.logFields({ packetType: "frame", errorCategory: "protocol" }));
      return;
    }

    const jpeg = extractJpegPayload(buffer, header);

    void this.renderer.render(jpeg, header.width, header.height, receivedAt).catch((error: unknown) => {
      console.warn("viewer render failed", this.logFields({ frameId: header.frameId, packetType: "frame", errorCategory: "internal", error }));
      this.setState("stale");
    });
  }

  private scheduleReconnect(): void {
    this.reconnectAttempt += 1;

    const delay = Math.min(
      BASE_RECONNECT_DELAY_MS * Math.pow(2, this.reconnectAttempt - 1),
      MAX_RECONNECT_DELAY_MS,
    );

    this.setState("stale");
    this.clearHeartbeatTimers();
    this.clearReconnectTimer();

    this.reconnectTimer = window.setTimeout(() => {
      this.openSocket(true);
    }, delay);
  }

  private buildSessionUrl(): string {
    const trimmed = this.serverUrl.trim().replace(/\/+$/, "");

    if (trimmed.startsWith("ws://") || trimmed.startsWith("wss://")) {
      return `${trimmed}/ws/session/${encodeURIComponent(this.sessionId)}`;
    }

    if (trimmed.startsWith("http://")) {
      return `ws://${trimmed.slice("http://".length)}/ws/session/${encodeURIComponent(this.sessionId)}`;
    }

    if (trimmed.startsWith("https://")) {
      return `wss://${trimmed.slice("https://".length)}/ws/session/${encodeURIComponent(this.sessionId)}`;
    }

    return `ws://${trimmed}/ws/session/${encodeURIComponent(this.sessionId)}`;
  }

  private clearReconnectTimer(): void {
    if (this.reconnectTimer !== null) {
      window.clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
  }

  private startHeartbeatLoop(): void {
    this.clearHeartbeatTimers();
    this.bumpHeartbeatTimeout();

    this.heartbeatIntervalTimer = window.setInterval(() => {
      if (!this.socket || this.socket.readyState !== WebSocket.OPEN || !this.ready) {
        return;
      }

      this.heartbeatSeq += 1;
      try {
        const heartbeatPacket = encodeHeartbeat(this.heartbeatSeq);
        this.socket.send(heartbeatPacket.buffer as ArrayBuffer);
      } catch (err) {
        console.error("failed to send heartbeat", this.logFields({ frameId: this.heartbeatSeq, packetType: "heartbeat", errorCategory: "transport", error: err }));
        this.setState("stale");
        this.socket.close();
      }
    }, HEARTBEAT_INTERVAL_MS);
  }

  private bumpHeartbeatTimeout(): void {
    if (!this.ready) {
      return;
    }

    if (this.heartbeatTimeoutTimer !== null) {
      window.clearTimeout(this.heartbeatTimeoutTimer);
    }

    this.heartbeatTimeoutTimer = window.setTimeout(() => {
      if (!this.socket || this.socket.readyState !== WebSocket.OPEN) {
        return;
      }

      console.warn("viewer heartbeat timeout; reconnecting", this.logFields({ packetType: "heartbeat", errorCategory: "timeout" }));
      this.setState("stale");
      this.socket.close();
    }, HEARTBEAT_TIMEOUT_MS);
  }

  private clearHeartbeatTimers(): void {
    if (this.heartbeatIntervalTimer !== null) {
      window.clearInterval(this.heartbeatIntervalTimer);
      this.heartbeatIntervalTimer = null;
    }

    if (this.heartbeatTimeoutTimer !== null) {
      window.clearTimeout(this.heartbeatTimeoutTimer);
      this.heartbeatTimeoutTimer = null;
    }
  }

  private setState(state: ViewerConnectionState): void {
    if (this.state !== state) {
      const allowed = VALID_TRANSITIONS[this.state] ?? [];
      if (!allowed.includes(state)) {
        console.warn("viewer state transition rejected", this.logFields({
          errorCategory: "internal",
          from: this.state,
          to: state,
        }));
        return;
      }

      console.info("viewer state transitioned", this.logFields({
        errorCategory: "internal",
        from: this.state,
        to: state,
      }));
    }

    this.state = state;
    this.stateChangeHandler?.(state);
  }
}
