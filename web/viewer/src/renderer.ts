const FPS_WINDOW_MS = 1000;

export class Renderer {
  private readonly canvas: HTMLCanvasElement;
  private readonly ctx: CanvasRenderingContext2D;
  private readonly frameTimestamps: number[] = [];
  private isRendering = false;
  private droppedFrames = 0;
  private lastFrameWidth = 0;
  private lastFrameHeight = 0;

  constructor(canvas: HTMLCanvasElement) {
    this.canvas = canvas;

    const context = this.canvas.getContext("2d");
    if (!context) {
      throw new Error("2d canvas context is unavailable");
    }

    this.ctx = context;
  }

  async render(jpeg: Uint8Array, frameWidth: number, frameHeight: number): Promise<boolean> {
    if (this.isRendering) {
      this.droppedFrames += 1;
      console.warn("render dropped because previous frame is still decoding", {
        droppedFrames: this.droppedFrames,
        frameWidth,
        frameHeight,
      });
      return false;
    }

    this.isRendering = true;
    this.lastFrameWidth = frameWidth;
    this.lastFrameHeight = frameHeight;

    const jpegCopy = new Uint8Array(jpeg.byteLength);
    jpegCopy.set(jpeg);

    const blob = new Blob([jpegCopy.buffer], { type: "image/jpeg" });
    try {
      const bitmap = await createImageBitmap(blob);

      try {
        this.resizeCanvasIfNeeded(frameWidth || bitmap.width, frameHeight || bitmap.height);
        this.ctx.drawImage(bitmap, 0, 0, this.canvas.width, this.canvas.height);
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
