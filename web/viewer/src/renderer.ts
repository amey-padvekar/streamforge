const FPS_WINDOW_MS = 1000;
const LATENCY_BUCKETS_MS = [1, 2, 5, 10, 20, 50, 100, 200, 500] as const;

type HistogramSnapshot = Record<string, number>;

class LatencyHistogram {
  private readonly buckets: readonly number[];
  private readonly counts: number[];

  constructor(buckets: readonly number[]) {
    this.buckets = buckets;
    this.counts = Array.from({ length: buckets.length + 1 }, () => 0);
  }

  observe(valueMs: number): void {
    const normalized = Number.isFinite(valueMs) && valueMs > 0 ? valueMs : 0;

    for (let i = 0; i < this.buckets.length; i += 1) {
      if (normalized <= this.buckets[i]) {
        this.counts[i] += 1;
        return;
      }
    }

    this.counts[this.counts.length - 1] += 1;
  }

  snapshot(): HistogramSnapshot {
    const out: HistogramSnapshot = {};

    for (let i = 0; i < this.buckets.length; i += 1) {
      out[`le_${this.buckets[i]}ms`] = this.counts[i];
    }
    out[`gt_${this.buckets[this.buckets.length - 1]}ms`] = this.counts[this.counts.length - 1];

    return out;
  }
}

export interface RendererLatencySnapshot {
  decodeMs: HistogramSnapshot;
  renderMs: HistogramSnapshot;
  decodeRenderMs: HistogramSnapshot;
}

export interface RendererFrameSize {
  width: number;
  height: number;
}

export class Renderer {
  private readonly canvas: HTMLCanvasElement;
  private readonly ctx: CanvasRenderingContext2D;
  private readonly frameTimestamps: number[] = [];
  private isRendering = false;
  private droppedFrames = 0;
  private lastFrameWidth = 0;
  private lastFrameHeight = 0;
  private readonly decodeLatency = new LatencyHistogram(LATENCY_BUCKETS_MS);
  private readonly renderLatency = new LatencyHistogram(LATENCY_BUCKETS_MS);
  private readonly decodeRenderLatency = new LatencyHistogram(LATENCY_BUCKETS_MS);

  private logFields(overrides: Partial<Record<string, unknown>> = {}): Record<string, unknown> {
    return {
      sessionId: "unknown",
      role: "viewer",
      frameId: 0,
      packetType: "frame",
      queueDepth: this.isRendering ? 1 : 0,
      framesDropped: this.droppedFrames,
      errorCategory: "internal",
      ...overrides,
    };
  }

  constructor(canvas: HTMLCanvasElement) {
    this.canvas = canvas;

    const context = this.canvas.getContext("2d");
    if (!context) {
      throw new Error("2d canvas context is unavailable");
    }

    this.ctx = context;
  }

  async render(
    jpeg: Uint8Array,
    frameWidth: number,
    frameHeight: number,
    receivedAtMs: number = performance.now(),
  ): Promise<boolean> {
    if (this.isRendering) {
      this.droppedFrames += 1;
      console.warn("render dropped because previous frame is still decoding", this.logFields({
        errorCategory: "backpressure",
        framesDropped: this.droppedFrames,
        frameWidth,
        frameHeight,
      }));
      return false;
    }

    this.isRendering = true;
    this.lastFrameWidth = frameWidth;
    this.lastFrameHeight = frameHeight;

    const jpegCopy = new Uint8Array(jpeg.byteLength);
    jpegCopy.set(jpeg);

    const blob = new Blob([jpegCopy.buffer], { type: "image/jpeg" });
    try {
      const decodeStart = performance.now();
      const bitmap = await createImageBitmap(blob);
      const decodedAt = performance.now();
      this.decodeLatency.observe(decodedAt - decodeStart);

      try {
        const renderStart = performance.now();
        this.resizeCanvasIfNeeded(frameWidth || bitmap.width, frameHeight || bitmap.height);
        this.ctx.drawImage(bitmap, 0, 0, this.canvas.width, this.canvas.height);
        const renderedAt = performance.now();
        this.renderLatency.observe(renderedAt - renderStart);
        this.decodeRenderLatency.observe(renderedAt - receivedAtMs);
        this.recordFrameRendered();
        return true;
      } finally {
        bitmap.close();
      }
    } finally {
      this.isRendering = false;
    }
  }

  getFps(): number {
    this.pruneOldFrameTimestamps();
    return this.frameTimestamps.length;
  }

  getFrameDimensions(): string {
    if (this.lastFrameWidth <= 0 || this.lastFrameHeight <= 0) {
      return "0 x 0";
    }

    return `${this.lastFrameWidth} x ${this.lastFrameHeight}`;
  }

  getLastFrameSize(): RendererFrameSize | null {
    if (this.lastFrameWidth <= 0 || this.lastFrameHeight <= 0) {
      return null;
    }

    return {
      width: this.lastFrameWidth,
      height: this.lastFrameHeight,
    };
  }

  getLatencySnapshot(): RendererLatencySnapshot {
    return {
      decodeMs: this.decodeLatency.snapshot(),
      renderMs: this.renderLatency.snapshot(),
      decodeRenderMs: this.decodeRenderLatency.snapshot(),
    };
  }

  getDroppedFrames(): number {
    return this.droppedFrames;
  }

  private resizeCanvasIfNeeded(width: number, height: number): void {
    if (this.canvas.width === width && this.canvas.height === height) {
      return;
    }

    this.canvas.width = width;
    this.canvas.height = height;
  }

  private recordFrameRendered(): void {
    this.frameTimestamps.push(performance.now());
    this.pruneOldFrameTimestamps();
  }

  private pruneOldFrameTimestamps(): void {
    const cutoff = performance.now() - FPS_WINDOW_MS;

    while (this.frameTimestamps.length > 0 && this.frameTimestamps[0] < cutoff) {
      this.frameTimestamps.shift();
    }
  }
}
