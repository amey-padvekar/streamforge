import { describe, expect, it } from "vitest";
import { startInputCapture } from "./input";
import {
  INPUT_EVENT_KEY_DOWN,
  INPUT_EVENT_MOUSE_MOVE,
  INPUT_EVENT_MOUSE_WHEEL,
  type InputEnvelope,
} from "./protocol";

type RectLike = {
  left: number;
  top: number;
  width: number;
  height: number;
};

function withMockRect(canvas: HTMLCanvasElement, rect: RectLike): void {
  Object.defineProperty(canvas, "getBoundingClientRect", {
    configurable: true,
    value: () => ({
      left: rect.left,
      top: rect.top,
      width: rect.width,
      height: rect.height,
      right: rect.left + rect.width,
      bottom: rect.top + rect.height,
      x: rect.left,
      y: rect.top,
      toJSON: () => ({}),
    }),
  });
}

function dispatchPointer(
  canvas: HTMLCanvasElement,
  type: string,
  init: { clientX: number; clientY: number; button?: number; buttons?: number },
): void {
  const event = new MouseEvent(type, {
    bubbles: true,
    cancelable: true,
    clientX: init.clientX,
    clientY: init.clientY,
    button: init.button ?? 0,
  });

  Object.defineProperty(event, "buttons", {
    configurable: true,
    value: init.buttons ?? 0,
  });

  canvas.dispatchEvent(event);
}

describe("input capture validation", () => {
  it("captures pointer and key events only when control mode is enabled", () => {
    const canvas = document.createElement("canvas");
    document.body.appendChild(canvas);
    withMockRect(canvas, { left: 0, top: 0, width: 800, height: 600 });

    const captured: InputEnvelope[] = [];
    const handle = startInputCapture({
      canvas,
      viewerId: "viewer-a",
      onInput: (input) => captured.push(input),
    });

    dispatchPointer(canvas, "pointermove", { clientX: 100, clientY: 120, buttons: 0 });
    window.dispatchEvent(new KeyboardEvent("keydown", { bubbles: true, code: "KeyA", key: "a" }));
    expect(captured).toHaveLength(0);

    handle.setControlEnabled(true);
    canvas.focus();

    dispatchPointer(canvas, "pointermove", { clientX: 100, clientY: 120, buttons: 0 });
    window.dispatchEvent(new KeyboardEvent("keydown", { bubbles: true, code: "KeyA", key: "a" }));

    expect(captured.some((event) => event.eventType === INPUT_EVENT_MOUSE_MOVE)).toBe(true);
    expect(captured.some((event) => event.eventType === INPUT_EVENT_KEY_DOWN)).toBe(true);

    handle.stop();
    document.body.removeChild(canvas);
  });

  it("keeps normalized coordinates correct after canvas resize", () => {
    const canvas = document.createElement("canvas");
    document.body.appendChild(canvas);

    let rect: RectLike = { left: 0, top: 0, width: 1000, height: 1000 };
    withMockRect(canvas, rect);

    const captured: InputEnvelope[] = [];
    const handle = startInputCapture({
      canvas,
      viewerId: "viewer-a",
      getRenderedFrameSize: () => ({ width: 1920, height: 1080 }),
      onInput: (input) => captured.push(input),
    });

    handle.setControlEnabled(true);

    dispatchPointer(canvas, "pointermove", { clientX: 500, clientY: 500, buttons: 0 });

    rect = { left: 0, top: 0, width: 600, height: 400 };
    withMockRect(canvas, rect);
    dispatchPointer(canvas, "pointermove", { clientX: 300, clientY: 200, buttons: 0 });

    const mouseEvents = captured.filter((event) => event.eventType === INPUT_EVENT_MOUSE_MOVE);
    expect(mouseEvents).toHaveLength(2);

    const first = mouseEvents[0].mouse;
    expect(first?.xNorm).toBeCloseTo(0.5, 6);
    expect(first?.yNorm).toBeCloseTo(0.5, 6);

    const second = mouseEvents[1].mouse;
    expect(second?.xNorm).toBeCloseTo(0.5, 6);
    expect(second?.yNorm).toBeCloseTo(0.5, 6);

    handle.stop();
    document.body.removeChild(canvas);
  });

  it("keeps browser wheel default behavior outside control mode", () => {
    const canvas = document.createElement("canvas");
    document.body.appendChild(canvas);
    withMockRect(canvas, { left: 0, top: 0, width: 800, height: 600 });

    const captured: InputEnvelope[] = [];
    const handle = startInputCapture({
      canvas,
      viewerId: "viewer-a",
      onInput: (input) => captured.push(input),
    });

    const disabledWheel = new WheelEvent("wheel", { bubbles: true, cancelable: true, deltaY: 120 });
    canvas.dispatchEvent(disabledWheel);
    expect(disabledWheel.defaultPrevented).toBe(false);
    expect(captured).toHaveLength(0);

    handle.setControlEnabled(true);
    const enabledWheel = new WheelEvent("wheel", { bubbles: true, cancelable: true, deltaY: 120 });
    canvas.dispatchEvent(enabledWheel);

    expect(enabledWheel.defaultPrevented).toBe(true);
    expect(captured.some((event) => event.eventType === INPUT_EVENT_MOUSE_WHEEL)).toBe(true);

    handle.stop();
    document.body.removeChild(canvas);
  });
});
