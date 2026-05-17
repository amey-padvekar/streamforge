import {
  INPUT_EVENT_KEY_DOWN,
  INPUT_EVENT_KEY_UP,
  INPUT_EVENT_MOUSE_DOWN,
  INPUT_EVENT_MOUSE_MOVE,
  INPUT_EVENT_MOUSE_UP,
  INPUT_EVENT_MOUSE_WHEEL,
  type InputEnvelope,
  mapKeyboardModifiers,
  mapPointerButton,
  mapPointerButtonsMask,
} from "./protocol";

export interface InputCaptureOptions {
  canvas: HTMLCanvasElement;
  viewerId: string;
  getRenderedFrameSize?: () => { width: number; height: number } | null;
  onInput: (input: InputEnvelope) => void;
  onControlEnabledChange?: (enabled: boolean) => void;
}

export interface InputCaptureHandle {
  stop: () => void;
  setControlEnabled: (enabled: boolean) => void;
  isControlEnabled: () => boolean;
}

interface NormalizedPoint {
  xNorm: number;
  yNorm: number;
}

interface ViewportMetadata {
  viewportWidth: number;
  viewportHeight: number;
  frameWidth?: number;
  frameHeight?: number;
}

interface DisplayedFrameRect {
  left: number;
  top: number;
  width: number;
  height: number;
}

export function startInputCapture(options: InputCaptureOptions): InputCaptureHandle {
  const { canvas, viewerId, getRenderedFrameSize, onInput, onControlEnabledChange } = options;

  let controlEnabled = false;
  let stopped = false;
  let eventId = 0;
  let hasFocus = false;

  if (canvas.tabIndex < 0) {
    canvas.tabIndex = 0;
  }

  const emitInput = (input: Omit<InputEnvelope, "eventId" | "timestampNs" | "viewerId">): void => {
    if (stopped) {
      return;
    }

    eventId += 1;
    onInput({
      ...input,
      eventId,
      timestampNs: Math.round(performance.now() * 1_000_000),
      viewerId,
    });
  };

  const getViewportMetadata = (): ViewportMetadata | null => {
    const rect = canvas.getBoundingClientRect();
    if (rect.width <= 0 || rect.height <= 0) {
      return null;
    }

    const frame = getRenderedFrameSize?.() ?? null;
    return {
      viewportWidth: Math.round(rect.width),
      viewportHeight: Math.round(rect.height),
      frameWidth: frame?.width,
      frameHeight: frame?.height,
    };
  };

  const getDisplayedFrameRect = (): DisplayedFrameRect | null => {
    const rect = canvas.getBoundingClientRect();
    if (rect.width <= 0 || rect.height <= 0) {
      return null;
    }

    const frame = getRenderedFrameSize?.() ?? null;
    if (!frame || frame.width <= 0 || frame.height <= 0) {
      return {
        left: rect.left,
        top: rect.top,
        width: rect.width,
        height: rect.height,
      };
    }

    const canvasAspect = rect.width / rect.height;
    const frameAspect = frame.width / frame.height;

    if (frameAspect > canvasAspect) {
      const displayedWidth = rect.width;
      const displayedHeight = displayedWidth / frameAspect;
      return {
        left: rect.left,
        top: rect.top + (rect.height - displayedHeight) / 2,
        width: displayedWidth,
        height: displayedHeight,
      };
    }

    const displayedHeight = rect.height;
    const displayedWidth = displayedHeight * frameAspect;
    return {
      left: rect.left + (rect.width - displayedWidth) / 2,
      top: rect.top,
      width: displayedWidth,
      height: displayedHeight,
    };
  };

  const normalizePointer = (event: PointerEvent): NormalizedPoint | null => {
    const frameRect = getDisplayedFrameRect();
    if (!frameRect || frameRect.width <= 0 || frameRect.height <= 0) {
      return null;
    }

    const x = (event.clientX - frameRect.left) / frameRect.width;
    const y = (event.clientY - frameRect.top) / frameRect.height;

    return {
      xNorm: clamp(x, 0, 1),
      yNorm: clamp(y, 0, 1),
    };
  };

  const shouldCapturePointer = (): boolean => controlEnabled;
  const shouldCaptureKeyboard = (): boolean => controlEnabled && hasFocus;

  const onPointerMove = (event: PointerEvent): void => {
    if (!shouldCapturePointer()) {
      return;
    }

    const point = normalizePointer(event);
    const metadata = getViewportMetadata();
    if (!point) {
      return;
    }

    emitInput({
      eventType: INPUT_EVENT_MOUSE_MOVE,
      mouse: {
        xNorm: point.xNorm,
        yNorm: point.yNorm,
        button: mapPointerButton(event.button),
        buttonsMask: mapPointerButtonsMask(event.buttons),
        ...metadata,
      },
    });
  };

  const onPointerDown = (event: PointerEvent): void => {
    if (!shouldCapturePointer()) {
      return;
    }

    if (document.activeElement !== canvas) {
      canvas.focus({ preventScroll: true });
    }

    const point = normalizePointer(event);
    const metadata = getViewportMetadata();
    if (!point) {
      return;
    }

    emitInput({
      eventType: INPUT_EVENT_MOUSE_DOWN,
      mouse: {
        xNorm: point.xNorm,
        yNorm: point.yNorm,
        button: mapPointerButton(event.button),
        buttonsMask: mapPointerButtonsMask(event.buttons),
        ...metadata,
      },
    });
  };

  const onPointerUp = (event: PointerEvent): void => {
    if (!shouldCapturePointer()) {
      return;
    }

    const point = normalizePointer(event);
    const metadata = getViewportMetadata();
    if (!point) {
      return;
    }

    emitInput({
      eventType: INPUT_EVENT_MOUSE_UP,
      mouse: {
        xNorm: point.xNorm,
        yNorm: point.yNorm,
        button: mapPointerButton(event.button),
        buttonsMask: mapPointerButtonsMask(event.buttons),
        ...metadata,
      },
    });
  };

  const onWheel = (event: WheelEvent): void => {
    if (!shouldCapturePointer()) {
      return;
    }

    event.preventDefault();

    const metadata = getViewportMetadata();

    emitInput({
      eventType: INPUT_EVENT_MOUSE_WHEEL,
      wheel: {
        deltaX: event.deltaX,
        deltaY: event.deltaY,
        ...metadata,
      },
    });
  };

  const onKeyDown = (event: KeyboardEvent): void => {
    if (!shouldCaptureKeyboard()) {
      return;
    }

    if (event.key === "Escape") {
      event.preventDefault();
      controlEnabled = false;
      onControlEnabledChange?.(false);
      return;
    }

    if (isBlockedShortcut(event)) {
      event.preventDefault();
    }

    emitInput({
      eventType: INPUT_EVENT_KEY_DOWN,
      key: {
        code: event.code,
        key: event.key,
        modifiers: mapKeyboardModifiers(event),
      },
    });
  };

  const onKeyUp = (event: KeyboardEvent): void => {
    if (!shouldCaptureKeyboard()) {
      return;
    }

    emitInput({
      eventType: INPUT_EVENT_KEY_UP,
      key: {
        code: event.code,
        key: event.key,
        modifiers: mapKeyboardModifiers(event),
      },
    });
  };

  const onCanvasFocus = (): void => {
    hasFocus = true;
  };

  const onCanvasBlur = (): void => {
    hasFocus = false;
  };

  const onWindowBlur = (): void => {
    hasFocus = false;
  };

  const wheelOptions: AddEventListenerOptions = { passive: false };

  canvas.addEventListener("pointermove", onPointerMove);
  canvas.addEventListener("pointerdown", onPointerDown);
  canvas.addEventListener("pointerup", onPointerUp);
  canvas.addEventListener("wheel", onWheel, wheelOptions);
  canvas.addEventListener("focus", onCanvasFocus);
  canvas.addEventListener("blur", onCanvasBlur);
  window.addEventListener("keydown", onKeyDown);
  window.addEventListener("keyup", onKeyUp);
  window.addEventListener("blur", onWindowBlur);

  const stop = (): void => {
    if (stopped) {
      return;
    }

    stopped = true;
    canvas.removeEventListener("pointermove", onPointerMove);
    canvas.removeEventListener("pointerdown", onPointerDown);
    canvas.removeEventListener("pointerup", onPointerUp);
    canvas.removeEventListener("wheel", onWheel, wheelOptions);
    canvas.removeEventListener("focus", onCanvasFocus);
    canvas.removeEventListener("blur", onCanvasBlur);
    window.removeEventListener("keydown", onKeyDown);
    window.removeEventListener("keyup", onKeyUp);
    window.removeEventListener("blur", onWindowBlur);
  };

  const setEnabled = (enabled: boolean): void => {
    if (controlEnabled === enabled) {
      return;
    }

    controlEnabled = enabled;
    onControlEnabledChange?.(enabled);
  };

  return {
    stop,
    setControlEnabled: setEnabled,
    isControlEnabled: () => controlEnabled,
  };
}

export function stopInputCapture(handle: InputCaptureHandle | null | undefined): void {
  handle?.stop();
}

export function setControlEnabled(
  handle: InputCaptureHandle | null | undefined,
  enabled: boolean,
): void {
  handle?.setControlEnabled(enabled);
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value));
}

function isBlockedShortcut(event: KeyboardEvent): boolean {
  const key = event.key.toLowerCase();
  if (key === "f5") {
    return true;
  }

  if (event.ctrlKey) {
    return key === "r" || key === "t" || key === "w" || key === "n";
  }

  if (event.altKey) {
    return key === "arrowleft" || key === "arrowright";
  }

  return false;
}
