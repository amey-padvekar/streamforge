import "./style.css";
import { setControlEnabled, startInputCapture, stopInputCapture, type InputCaptureHandle } from "./input";
import { type InputEnvelope } from "./protocol";
import { Renderer } from "./renderer";
import { Viewer, type ViewerConnectionState } from "./viewer";

const canvas = document.querySelector<HTMLCanvasElement>("#viewer-canvas");
const connectForm = document.querySelector<HTMLFormElement>("#connect-form");
const serverUrlInput = document.querySelector<HTMLInputElement>("#server-url");
const sessionIdInput = document.querySelector<HTMLInputElement>("#session-id");
const viewerTokenInput = document.querySelector<HTMLInputElement>("#viewer-token");
const connectButton = document.querySelector<HTMLButtonElement>("#connect-button");
const statusValue = document.querySelector<HTMLSpanElement>("#connection-status");
const fpsValue = document.querySelector<HTMLSpanElement>("#fps-value");
const dimensionsValue = document.querySelector<HTMLSpanElement>("#dimensions-value");

if (
  !canvas ||
  !connectForm ||
  !serverUrlInput ||
  !sessionIdInput ||
  !viewerTokenInput ||
  !connectButton ||
  !statusValue ||
  !fpsValue ||
  !dimensionsValue
) {
  throw new Error("viewer ui elements not found");
}

const disconnectButton = document.createElement("button");
disconnectButton.type = "button";
disconnectButton.id = "disconnect-button";
disconnectButton.textContent = "Disconnect";
disconnectButton.disabled = true;
connectForm.appendChild(disconnectButton);

const controlPanel = document.createElement("div");
controlPanel.className = "control-panel";

const controlToggleButton = document.createElement("button");
controlToggleButton.type = "button";
controlToggleButton.id = "control-toggle-button";
controlToggleButton.className = "control-toggle";

const controlBadge = document.createElement("span");
controlBadge.id = "control-state-badge";
controlBadge.className = "control-state-badge";

controlPanel.appendChild(controlToggleButton);
controlPanel.appendChild(controlBadge);
connectForm.appendChild(controlPanel);

const renderer = new Renderer(canvas);
let viewer: Viewer | null = null;
let inputCapture: InputCaptureHandle | null = null;
let isControlEnabled = false;
let hasCanvasFocus = false;

const setConnectionState = (state: ViewerConnectionState): void => {
  statusValue.textContent = state;
  statusValue.dataset.state = state;

  const active = state !== "disconnected";
  connectButton.disabled = active;
  disconnectButton.disabled = !active;
};

const renderControlState = (): void => {
  if (isControlEnabled) {
    controlToggleButton.textContent = "Switch to View-only";

    if (hasCanvasFocus) {
      controlBadge.textContent = "Control enabled: keyboard capture active";
      controlBadge.dataset.mode = "control-focused";
      return;
    }

    controlBadge.textContent = "Control enabled: click stream to focus keyboard";
    controlBadge.dataset.mode = "control-await-focus";
    return;
  }

  controlToggleButton.textContent = "Enable Control";
  controlBadge.textContent = "View-only mode";
  controlBadge.dataset.mode = "view-only";
};

const setControlMode = (enabled: boolean): void => {
  const previous = isControlEnabled;
  isControlEnabled = enabled;
  setControlEnabled(inputCapture, enabled);

  if (!enabled) {
    canvas.blur();
    hasCanvasFocus = false;
  }

  if (previous !== enabled) {
    console.info("viewer control toggled", {
      sessionId: sessionIdInput.value.trim() || "unknown",
      role: "viewer",
      frameId: 0,
      packetType: "input",
      queueDepth: 0,
      framesDropped: renderer.getDroppedFrames(),
      errorCategory: "internal",
      from: previous ? "control-enabled" : "view-only",
      to: enabled ? "control-enabled" : "view-only",
    });
  }

  renderControlState();
};

const emitLocalInputDrop = (input: InputEnvelope, reason: string): void => {
  console.warn("viewer input dropped", {
    sessionId: sessionIdInput.value.trim() || "unknown",
    role: "viewer",
    frameId: input.eventId,
    packetType: "input",
    queueDepth: 0,
    framesDropped: renderer.getDroppedFrames(),
    errorCategory: "transport",
    reason,
    eventType: input.eventType,
    eventId: input.eventId,
  });
};

inputCapture = startInputCapture({
  canvas,
  viewerId: "local-viewer",
  getRenderedFrameSize: () => renderer.getLastFrameSize(),
  onInput: (input) => {
    if (!viewer) {
      emitLocalInputDrop(input, "viewer-not-initialized");
      return;
    }

    if (viewer.getState() === "disconnected") {
      emitLocalInputDrop(input, "viewer-disconnected");
      return;
    }

    viewer.enqueueInput(input);
  },
  onControlEnabledChange: (enabled) => {
    const previous = isControlEnabled;
    isControlEnabled = enabled;

    if (previous !== enabled) {
      console.info("viewer control toggled", {
        sessionId: sessionIdInput.value.trim() || "unknown",
        role: "viewer",
        frameId: 0,
        packetType: "input",
        queueDepth: 0,
        framesDropped: renderer.getDroppedFrames(),
        errorCategory: "internal",
        from: previous ? "control-enabled" : "view-only",
        to: enabled ? "control-enabled" : "view-only",
        reason: "capture-module",
      });
    }

    renderControlState();
  },
});

controlToggleButton.addEventListener("click", () => {
  setControlMode(!isControlEnabled);
});

canvas.addEventListener("focus", () => {
  hasCanvasFocus = true;
  renderControlState();
});

canvas.addEventListener("blur", () => {
  hasCanvasFocus = false;
  renderControlState();
});

const startViewer = (): void => {
  viewer?.disconnect();

  const nextViewer = new Viewer({
    serverUrl: serverUrlInput.value.trim(),
    sessionId: sessionIdInput.value.trim(),
    viewerToken: viewerTokenInput.value.trim(),
    renderer,
    onStateChange: setConnectionState,
  });

  viewer = nextViewer;
  viewer.connect();
};

connectForm.addEventListener("submit", (event) => {
  event.preventDefault();

  const serverUrl = serverUrlInput.value.trim();
  const sessionId = sessionIdInput.value.trim();
  const viewerToken = viewerTokenInput.value.trim();

  if (!serverUrl || !sessionId || !viewerToken) {
    return;
  }

  startViewer();
});

disconnectButton.addEventListener("click", () => {
  viewer?.disconnect();
  setControlMode(false);
});

const fpsInterval = window.setInterval(() => {
  fpsValue.textContent = String(renderer.getFps());
  dimensionsValue.textContent = renderer.getFrameDimensions();
}, 1000);

const viewerLatencyInterval = window.setInterval(() => {
  console.info("viewer latency telemetry", {
    sessionId: sessionIdInput.value.trim() || "unknown",
    role: "viewer",
    frameId: 0,
    packetType: "frame",
    queueDepth: 0,
    framesDropped: renderer.getDroppedFrames(),
    errorCategory: "internal",
    latency: renderer.getLatencySnapshot(),
  });
}, 5000);

window.addEventListener("beforeunload", () => {
  window.clearInterval(fpsInterval);
  window.clearInterval(viewerLatencyInterval);
  stopInputCapture(inputCapture);
  viewer?.disconnect();
});

setConnectionState("disconnected");
renderControlState();
