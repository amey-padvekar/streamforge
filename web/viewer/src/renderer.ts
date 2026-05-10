const FPS_WINDOW_MS = 1000;

export class Renderer {
  private readonly canvas: HTMLCanvasElement;
  private readonly ctx: CanvasRenderingContext2D;
  private readonly frameTimestamps: number[] = [];

  constructor(canvas: HTMLCanvasElement) {
    this.canvas = canvas;

    const context = this.canvas.getContext("2d");
    if (!context) {
      throw new Error("2d canvas context is unavailable");
    }

    this.ctx = context;
  }

  async render(jpeg: Uint8Array): Promise<void> {
    const jpegCopy = new Uint8Array(jpeg.byteLength);
    jpegCopy.set(jpeg);

    const blob = new Blob([jpegCopy.buffer], { type: "image/jpeg" });
    const bitmap = await createImageBitmap(blob);

    try {
      this.resizeCanvasIfNeeded(bitmap.width, bitmap.height);
      this.ctx.drawImage(bitmap, 0, 0, this.canvas.width, this.canvas.height);
      this.recordFrameRendered();
    } finally {
      bitmap.close();
    }
  }

  getFps(): number {
    this.pruneOldFrameTimestamps();
    return this.frameTimestamps.length;
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
