import "./style.css";
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

if (
  !canvas ||
  !connectForm ||
  !serverUrlInput ||
  !sessionIdInput ||
  !viewerTokenInput ||
  !connectButton ||
  !statusValue ||
  !fpsValue
) {
  throw new Error("viewer ui elements not found");
}

const disconnectButton = document.createElement("button");
disconnectButton.type = "button";
disconnectButton.id = "disconnect-button";
disconnectButton.textContent = "Disconnect";
disconnectButton.disabled = true;
connectForm.appendChild(disconnectButton);

const renderer = new Renderer(canvas);
let viewer: Viewer | null = null;

const setConnectionState = (state: ViewerConnectionState): void => {
  statusValue.textContent = state;
  statusValue.dataset.state = state;

  const active = state !== "disconnected";
  connectButton.disabled = active;
  disconnectButton.disabled = !active;
};

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
});

const fpsInterval = window.setInterval(() => {
  fpsValue.textContent = String(renderer.getFps());
}, 1000);

window.addEventListener("beforeunload", () => {
  window.clearInterval(fpsInterval);
  viewer?.disconnect();
});

setConnectionState("disconnected");
