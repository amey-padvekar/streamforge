import {
  extractJpegPayload,
  parseFrameHeader,
  encodeHello,
  encodeAuth,
  decodeErrorPayload,
  PACKET_TYPE_ACK,
  PACKET_TYPE_ERROR,
  PROTOCOL_HEADER_SIZE,
} from "./protocol";
import { Renderer } from "./renderer";

const BASE_RECONNECT_DELAY_MS = 500;
const MAX_RECONNECT_DELAY_MS = 8000;

export type ViewerConnectionState =
  | "disconnected"
  | "connecting"
  | "connected"
  | "reconnecting"
  | "error";

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
  private shouldReconnect = true;
  private stateChangeHandler?: (state: ViewerConnectionState) => void;
  private state: ViewerConnectionState = "disconnected";
  private authed = false;
  private ready = false;

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
    this.setState(isReconnect ? "reconnecting" : "connecting");

    const socket = new WebSocket(this.buildSessionUrl());
    socket.binaryType = "arraybuffer";
    this.socket = socket;

    socket.onopen = () => {
      if (socket !== this.socket) {
        return;
      }

      this.reconnectAttempt = 0;
      this.setState("connected");

      // Send HELLO packet
      try {
        const helloPacket = encodeHello("viewer", 1, 0);
        socket.send(helloPacket.buffer);
      } catch (err) {
        console.error("failed to encode HELLO:", err);
        this.setState("error");
        socket.close();
      }
    };

    socket.onerror = () => {
      if (socket !== this.socket) {
        return;
      }

      this.setState("error");
    };

    socket.onclose = () => {
      if (socket !== this.socket) {
        return;
      }

      this.socket = null;

      if (!this.shouldReconnect) {
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
        this.handleBinaryMessage(event.data);
      }
    };
  }

  private handleBinaryMessage(buffer: ArrayBuffer): void {
    if (buffer.byteLength < PROTOCOL_HEADER_SIZE) {
      console.error("message too short for header");
      return;
    }

    const view = new DataView(buffer);
    const packetType = view.getUint8(1);

    // If we haven't sent AUTH yet, expect ACK to HELLO
    if (this.state === "connected" && !this.authed) {
      if (packetType === PACKET_TYPE_ACK) {
        // ACK to HELLO received, now send AUTH
        this.authed = true;
        try {
          const authPacket = encodeAuth("viewer", this.viewerToken);
          if (this.socket) {
            this.socket.send(authPacket.buffer);
          }
        } catch (err) {
          console.error("failed to encode AUTH:", err);
          this.setState("error");
          if (this.socket) {
            this.socket.close();
          }
        }
      } else if (packetType === PACKET_TYPE_ERROR) {
        const payload = new Uint8Array(buffer, PROTOCOL_HEADER_SIZE);
        const error = decodeErrorPayload(payload);
        console.error("server rejected HELLO:", error?.reason, error?.detail);
        this.setState("error");
        if (this.socket) {
          this.socket.close();
        }
      } else {
        console.warn("ignoring unsupported handshake packet before AUTH", { packetType });
      }
      return;
    }

    // If we sent AUTH, expect ACK or ERROR response
    if (this.authed && !this.ready) {
      if (packetType === PACKET_TYPE_ACK) {
        this.ready = true;
        return;
      } else if (packetType === PACKET_TYPE_ERROR) {
        const payload = new Uint8Array(buffer, PROTOCOL_HEADER_SIZE);
        const error = decodeErrorPayload(payload);
        console.error("server rejected AUTH:", error?.reason, error?.detail);
        this.setState("error");
        if (this.socket) {
          this.socket.close();
        }
      } else {
        console.warn("ignoring unsupported handshake packet after AUTH", { packetType });
      }
      return;
    }

    // After handshake, expect FRAME packets
    if (this.ready) {
      this.enqueueRender(buffer);
    }
  }

  private enqueueRender(buffer: ArrayBuffer): void {
    const header = parseFrameHeader(buffer);
    if (!header) {
      return;
    }

    const jpeg = extractJpegPayload(buffer, header);

    void this.renderer.render(jpeg, header.width, header.height).catch(() => {
      this.setState("error");
    });
  }

  private scheduleReconnect(): void {
    this.reconnectAttempt += 1;

    const delay = Math.min(
      BASE_RECONNECT_DELAY_MS * Math.pow(2, this.reconnectAttempt - 1),
      MAX_RECONNECT_DELAY_MS,
    );

    this.setState("reconnecting");
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

  private setState(state: ViewerConnectionState): void {
    this.state = state;
    this.stateChangeHandler?.(state);
  }
}
