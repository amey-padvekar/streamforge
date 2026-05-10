import { extractJpegPayload, parseFrameHeader } from "./protocol";
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

  // Serialize decode/draw work so frames are rendered in receive order.
  private renderQueue: Promise<void> = Promise.resolve();

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

      const handshake = JSON.stringify({ role: "viewer", token: this.viewerToken });
      socket.send(handshake);
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
        this.enqueueRender(event.data);
      }
    };
  }

  private enqueueRender(buffer: ArrayBuffer): void {
    const header = parseFrameHeader(buffer);
    if (!header) {
      return;
    }

    const jpeg = extractJpegPayload(buffer, header);

    this.renderQueue = this.renderQueue
      .then(() => this.renderer.render(jpeg))
      .catch(() => {
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
