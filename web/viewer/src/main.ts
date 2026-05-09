import "./style.css";

const app = document.querySelector<HTMLDivElement>("#app");

if (!app) {
  throw new Error("viewer root element not found");
}

app.innerHTML = `
  <main class="app-shell">
    <section class="hero">
      <p class="eyebrow">Streamforge</p>
      <h1>Viewer scaffold ready</h1>
      <p class="copy">
        Phase 0 is complete. Phase 1 will connect this viewer to a live WebSocket session and render remote frames.
      </p>
    </section>
  </main>
`;
