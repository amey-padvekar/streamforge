export const PROTOCOL_HEADER_SIZE = 20;

export const VERSION_OFFSET = 0;
export const PACKET_TYPE_OFFSET = 1;
export const FLAGS_OFFSET = 2;
export const RESERVED_OFFSET = 3;
export const SEQUENCE_ID_OFFSET = 4;
export const TIMESTAMP_NS_OFFSET = 8;
export const PAYLOAD_LEN_OFFSET = 16;

export const PACKET_TYPE_HELLO = 0x01;
export const PACKET_TYPE_AUTH = 0x02;
export const PACKET_TYPE_FRAME = 0x03;
export const PACKET_TYPE_HEARTBEAT = 0x04;
export const PACKET_TYPE_ACK = 0x05;
export const PACKET_TYPE_CONTROL = 0x06;
export const PACKET_TYPE_ERROR = 0x07;
export const PACKET_TYPE_INPUT = 0x08;

export const INPUT_EVENT_MOUSE_MOVE = 0x10;
export const INPUT_EVENT_MOUSE_DOWN = 0x11;
export const INPUT_EVENT_MOUSE_UP = 0x12;
export const INPUT_EVENT_MOUSE_WHEEL = 0x13;

export const INPUT_EVENT_KEY_DOWN = 0x20;
export const INPUT_EVENT_KEY_UP = 0x21;

export const INPUT_EVENT_RESIZE_HINT = 0x30;
export const INPUT_EVENT_MONITOR_SELECT = 0x31;

export const MOUSE_BUTTON_NONE = 0;
export const MOUSE_BUTTON_LEFT = 1;
export const MOUSE_BUTTON_RIGHT = 2;
export const MOUSE_BUTTON_MIDDLE = 3;

export const MOUSE_BUTTON_MASK_LEFT = 1 << 0;
export const MOUSE_BUTTON_MASK_RIGHT = 1 << 1;
export const MOUSE_BUTTON_MASK_MIDDLE = 1 << 2;

export const KEY_MODIFIER_CTRL = 1 << 0;
export const KEY_MODIFIER_ALT = 1 << 1;
export const KEY_MODIFIER_SHIFT = 1 << 2;
export const KEY_MODIFIER_META = 1 << 3;

export const PROTOCOL_VERSION = 1;

export const FRAME_METADATA_SIZE = 5;
export const FRAME_WIDTH_OFFSET = 0;
export const FRAME_HEIGHT_OFFSET = 2;
export const FRAME_QUALITY_OFFSET = 4;

export interface FrameHeader {
  version: number;
  packetType: number;
  frameId: number;
  timestampNs: bigint;
  width: number;
  height: number;
  jpegQuality: number;
  payloadLen: number;
  jpegPayloadLen: number;
}

export interface Header {
  version: number;
  packetType: number;
  flags: number;
  reserved: number;
  sequenceId: number;
  timestampNs: bigint;
  payloadLen: number;
}

export interface MousePayload {
  xNorm: number;
  yNorm: number;
  button: number;
  buttonsMask: number;
  viewportWidth?: number;
  viewportHeight?: number;
  frameWidth?: number;
  frameHeight?: number;
}

export interface WheelPayload {
  deltaX: number;
  deltaY: number;
  viewportWidth?: number;
  viewportHeight?: number;
  frameWidth?: number;
  frameHeight?: number;
}

export interface KeyPayload {
  code: string;
  key: string;
  modifiers: number;
}

export interface DisplayPayload {
  targetMonitorId: string;
  viewportWidth: number;
  viewportHeight: number;
}

export interface InputEnvelope {
  eventType: number;
  eventId: number;
  timestampNs: number;
  viewerId: string;
  mouse?: MousePayload;
  wheel?: WheelPayload;
  key?: KeyPayload;
  display?: DisplayPayload;
}

const textEncoder = new TextEncoder();
const textDecoder = new TextDecoder();

export function encodeHeader(h: Header): Uint8Array {
  const buf = new Uint8Array(PROTOCOL_HEADER_SIZE);
  const view = new DataView(buf.buffer);

  view.setUint8(VERSION_OFFSET, h.version);
  view.setUint8(PACKET_TYPE_OFFSET, h.packetType);
  view.setUint8(FLAGS_OFFSET, h.flags);
  view.setUint8(RESERVED_OFFSET, h.reserved);
  view.setUint32(SEQUENCE_ID_OFFSET, h.sequenceId, false);
  view.setBigUint64(TIMESTAMP_NS_OFFSET, h.timestampNs, false);
  view.setUint32(PAYLOAD_LEN_OFFSET, h.payloadLen, false);

  return buf;
}

export function decodeHeader(buffer: ArrayBuffer): Header | null {
  if (buffer.byteLength < PROTOCOL_HEADER_SIZE) {
    return null;
  }

  const view = new DataView(buffer);

  return {
    version: view.getUint8(VERSION_OFFSET),
    packetType: view.getUint8(PACKET_TYPE_OFFSET),
    flags: view.getUint8(FLAGS_OFFSET),
    reserved: view.getUint8(RESERVED_OFFSET),
    sequenceId: view.getUint32(SEQUENCE_ID_OFFSET, false),
    timestampNs: view.getBigUint64(TIMESTAMP_NS_OFFSET, false),
    payloadLen: view.getUint32(PAYLOAD_LEN_OFFSET, false),
  };
}

export function encodeHello(agentId: string, supportedVersion: number, capabilityFlags: number): Uint8Array {
  const id = textEncoder.encode(agentId);
  if (id.length > 255) {
    throw new Error("agentId too long");
  }

  const payload = new Uint8Array(1 + id.length + 1 + 1);
  payload[0] = id.length;
  payload.set(id, 1);
  payload[1 + id.length] = supportedVersion;
  payload[1 + id.length + 1] = capabilityFlags;

  const header: Header = {
    version: PROTOCOL_VERSION,
    packetType: PACKET_TYPE_HELLO,
    flags: 0,
    reserved: 0,
    sequenceId: 0,
    timestampNs: 0n,
    payloadLen: payload.length,
  };

  const headerBytes = encodeHeader(header);
  const packet = new Uint8Array(PROTOCOL_HEADER_SIZE + payload.length);
  packet.set(headerBytes);
  packet.set(payload, PROTOCOL_HEADER_SIZE);
  return packet;
}

export function encodeAuth(role: string, token: string): Uint8Array {
  const roleBytes = textEncoder.encode(role.toLowerCase());
  const tokenBytes = textEncoder.encode(token);

  if (roleBytes.length > 255) {
    throw new Error("role too long");
  }
  if (tokenBytes.length > 65535) {
    throw new Error("token too long");
  }

  const payloadLen = 1 + roleBytes.length + 2 + tokenBytes.length;
  const payload = new Uint8Array(payloadLen);

  payload[0] = roleBytes.length;
  payload.set(roleBytes, 1);

  const tokenLenOffset = 1 + roleBytes.length;
  payload[tokenLenOffset] = (tokenBytes.length >> 8) & 0xFF;
  payload[tokenLenOffset + 1] = tokenBytes.length & 0xFF;
  payload.set(tokenBytes, tokenLenOffset + 2);

  const header: Header = {
    version: PROTOCOL_VERSION,
    packetType: PACKET_TYPE_AUTH,
    flags: 0,
    reserved: 0,
    sequenceId: 1,
    timestampNs: 0n,
    payloadLen: payload.length,
  };

  const headerBytes = encodeHeader(header);
  const packet = new Uint8Array(PROTOCOL_HEADER_SIZE + payload.length);
  packet.set(headerBytes);
  packet.set(payload, PROTOCOL_HEADER_SIZE);
  return packet;
}

export function encodeHeartbeat(sequenceId: number): Uint8Array {
  const header: Header = {
    version: PROTOCOL_VERSION,
    packetType: PACKET_TYPE_HEARTBEAT,
    flags: 0,
    reserved: 0,
    sequenceId,
    timestampNs: BigInt(Date.now()) * 1000000n,
    payloadLen: 0,
  };

  return encodeHeader(header);
}

export function decodeErrorPayload(payload: Uint8Array): { reason: string; detail: string } | null {
  if (payload.length < 3) {
    return null;
  }

  const reasonLen = payload[0];
  if (payload.length < 1 + reasonLen + 2) {
    return null;
  }

  const reason = textDecoder.decode(payload.slice(1, 1 + reasonLen));
  const detailLenOffset = 1 + reasonLen;
  const detailLen = ((payload[detailLenOffset] << 8) | payload[detailLenOffset + 1]) >>> 0;

  if (payload.length < detailLenOffset + 2 + detailLen) {
    return null;
  }

  const detail = textDecoder.decode(payload.slice(detailLenOffset + 2, detailLenOffset + 2 + detailLen));

  return { reason, detail };
}

export function mapPointerButton(button: number): number {
  switch (button) {
    case 0:
      return MOUSE_BUTTON_LEFT;
    case 1:
      return MOUSE_BUTTON_MIDDLE;
    case 2:
      return MOUSE_BUTTON_RIGHT;
    default:
      return MOUSE_BUTTON_NONE;
  }
}

export function mapPointerButtonsMask(buttons: number): number {
  let mask = 0;
  if ((buttons & 1) !== 0) {
    mask |= MOUSE_BUTTON_MASK_LEFT;
  }
  if ((buttons & 2) !== 0) {
    mask |= MOUSE_BUTTON_MASK_RIGHT;
  }
  if ((buttons & 4) !== 0) {
    mask |= MOUSE_BUTTON_MASK_MIDDLE;
  }
  return mask;
}

export function mapKeyboardModifiers(event: KeyboardEvent): number {
  let modifiers = 0;
  if (event.ctrlKey) {
    modifiers |= KEY_MODIFIER_CTRL;
  }
  if (event.altKey) {
    modifiers |= KEY_MODIFIER_ALT;
  }
  if (event.shiftKey) {
    modifiers |= KEY_MODIFIER_SHIFT;
  }
  if (event.metaKey) {
    modifiers |= KEY_MODIFIER_META;
  }
  return modifiers;
}

export function encodeInputPayload(input: InputEnvelope): Uint8Array {
  validateInputEnvelope(input);
  const payloadText = JSON.stringify(input);
  const payload = textEncoder.encode(payloadText);

  if (payload.length > 8 * 1024 * 1024) {
    throw new Error("input payload too large");
  }

  return payload;
}

export function decodeInputPayload(payload: Uint8Array): InputEnvelope {
  const text = textDecoder.decode(payload);
  const parsed = JSON.parse(text) as InputEnvelope;
  validateInputEnvelope(parsed);
  return parsed;
}

export function encodeInput(input: InputEnvelope, sequenceId: number): Uint8Array {
  const payload = encodeInputPayload(input);

  const header: Header = {
    version: PROTOCOL_VERSION,
    packetType: PACKET_TYPE_INPUT,
    flags: 0,
    reserved: 0,
    sequenceId,
    timestampNs: BigInt(input.timestampNs),
    payloadLen: payload.length,
  };

  const headerBytes = encodeHeader(header);
  const packet = new Uint8Array(PROTOCOL_HEADER_SIZE + payload.length);
  packet.set(headerBytes);
  packet.set(payload, PROTOCOL_HEADER_SIZE);
  return packet;
}

function validateInputEnvelope(input: InputEnvelope): void {
  if (!isKnownInputEventType(input.eventType)) {
    throw new Error(`unknown input event type: ${input.eventType}`);
  }
  if (!Number.isFinite(input.eventId) || input.eventId <= 0) {
    throw new Error("eventId must be > 0");
  }
  if (!Number.isFinite(input.timestampNs) || input.timestampNs <= 0) {
    throw new Error("timestampNs must be > 0");
  }
  if (!input.viewerId || input.viewerId.trim().length === 0) {
    throw new Error("viewerId must be non-empty");
  }

  const payloadCount =
    (input.mouse ? 1 : 0) +
    (input.wheel ? 1 : 0) +
    (input.key ? 1 : 0) +
    (input.display ? 1 : 0);
  if (payloadCount !== 1) {
    throw new Error("exactly one payload block must be set");
  }

  if (isMouseEvent(input.eventType)) {
    if (!input.mouse) {
      throw new Error("mouse payload required for mouse event");
    }
    validateMousePayload(input.mouse);
  }

  if (input.eventType === INPUT_EVENT_MOUSE_WHEEL) {
    if (!input.wheel) {
      throw new Error("wheel payload required for mouse wheel event");
    }
    validateWheelPayload(input.wheel);
  }

  if (isKeyEvent(input.eventType)) {
    if (!input.key) {
      throw new Error("key payload required for key event");
    }
    validateKeyPayload(input.key);
  }

  if (isDisplayEvent(input.eventType)) {
    if (!input.display) {
      throw new Error("display payload required for display event");
    }
    validateDisplayPayload(input.display);
  }
}

function validateMousePayload(mouse: MousePayload): void {
  if (!Number.isFinite(mouse.xNorm) || !Number.isFinite(mouse.yNorm)) {
    throw new Error("xNorm and yNorm must be finite numbers");
  }
  if (mouse.xNorm < 0 || mouse.xNorm > 1 || mouse.yNorm < 0 || mouse.yNorm > 1) {
    throw new Error("xNorm and yNorm must be in [0,1]");
  }

  const knownButtons = new Set<number>([
    MOUSE_BUTTON_NONE,
    MOUSE_BUTTON_LEFT,
    MOUSE_BUTTON_RIGHT,
    MOUSE_BUTTON_MIDDLE,
  ]);
  if (!knownButtons.has(mouse.button)) {
    throw new Error(`invalid mouse button: ${mouse.button}`);
  }

  const allowedMask = MOUSE_BUTTON_MASK_LEFT | MOUSE_BUTTON_MASK_RIGHT | MOUSE_BUTTON_MASK_MIDDLE;
  if ((mouse.buttonsMask & ~allowedMask) !== 0) {
    throw new Error(`invalid mouse buttons mask: ${mouse.buttonsMask}`);
  }

  if (mouse.viewportWidth !== undefined && (!Number.isFinite(mouse.viewportWidth) || mouse.viewportWidth <= 0)) {
    throw new Error("viewportWidth must be > 0 when provided");
  }
  if (mouse.viewportHeight !== undefined && (!Number.isFinite(mouse.viewportHeight) || mouse.viewportHeight <= 0)) {
    throw new Error("viewportHeight must be > 0 when provided");
  }
  if (mouse.frameWidth !== undefined && (!Number.isFinite(mouse.frameWidth) || mouse.frameWidth <= 0)) {
    throw new Error("frameWidth must be > 0 when provided");
  }
  if (mouse.frameHeight !== undefined && (!Number.isFinite(mouse.frameHeight) || mouse.frameHeight <= 0)) {
    throw new Error("frameHeight must be > 0 when provided");
  }
}

function validateWheelPayload(wheel: WheelPayload): void {
  if (!Number.isFinite(wheel.deltaX) || !Number.isFinite(wheel.deltaY)) {
    throw new Error("deltaX and deltaY must be finite numbers");
  }

  if (wheel.viewportWidth !== undefined && (!Number.isFinite(wheel.viewportWidth) || wheel.viewportWidth <= 0)) {
    throw new Error("viewportWidth must be > 0 when provided");
  }
  if (wheel.viewportHeight !== undefined && (!Number.isFinite(wheel.viewportHeight) || wheel.viewportHeight <= 0)) {
    throw new Error("viewportHeight must be > 0 when provided");
  }
  if (wheel.frameWidth !== undefined && (!Number.isFinite(wheel.frameWidth) || wheel.frameWidth <= 0)) {
    throw new Error("frameWidth must be > 0 when provided");
  }
  if (wheel.frameHeight !== undefined && (!Number.isFinite(wheel.frameHeight) || wheel.frameHeight <= 0)) {
    throw new Error("frameHeight must be > 0 when provided");
  }
}

function validateKeyPayload(key: KeyPayload): void {
  if (key.code.trim().length === 0 && key.key.trim().length === 0) {
    throw new Error("key payload must include non-empty code or key");
  }

  const allowedMask = KEY_MODIFIER_CTRL | KEY_MODIFIER_ALT | KEY_MODIFIER_SHIFT | KEY_MODIFIER_META;
  if ((key.modifiers & ~allowedMask) !== 0) {
    throw new Error(`invalid key modifiers bitmask: ${key.modifiers}`);
  }
}

function validateDisplayPayload(display: DisplayPayload): void {
  if (!display.targetMonitorId || display.targetMonitorId.trim().length === 0) {
    throw new Error("targetMonitorId must be non-empty");
  }
  if (!Number.isFinite(display.viewportWidth) || display.viewportWidth <= 0) {
    throw new Error("viewportWidth must be > 0");
  }
  if (!Number.isFinite(display.viewportHeight) || display.viewportHeight <= 0) {
    throw new Error("viewportHeight must be > 0");
  }
}

function isKnownInputEventType(eventType: number): boolean {
  return (
    eventType === INPUT_EVENT_MOUSE_MOVE ||
    eventType === INPUT_EVENT_MOUSE_DOWN ||
    eventType === INPUT_EVENT_MOUSE_UP ||
    eventType === INPUT_EVENT_MOUSE_WHEEL ||
    eventType === INPUT_EVENT_KEY_DOWN ||
    eventType === INPUT_EVENT_KEY_UP ||
    eventType === INPUT_EVENT_RESIZE_HINT ||
    eventType === INPUT_EVENT_MONITOR_SELECT
  );
}

function isMouseEvent(eventType: number): boolean {
  return (
    eventType === INPUT_EVENT_MOUSE_MOVE ||
    eventType === INPUT_EVENT_MOUSE_DOWN ||
    eventType === INPUT_EVENT_MOUSE_UP
  );
}

function isKeyEvent(eventType: number): boolean {
  return eventType === INPUT_EVENT_KEY_DOWN || eventType === INPUT_EVENT_KEY_UP;
}

function isDisplayEvent(eventType: number): boolean {
  return eventType === INPUT_EVENT_RESIZE_HINT || eventType === INPUT_EVENT_MONITOR_SELECT;
}

export function parseFrameHeader(buffer: ArrayBuffer): FrameHeader | null {
  if (buffer.byteLength < PROTOCOL_HEADER_SIZE) {
    return null;
  }

  const view = new DataView(buffer);

  const version = view.getUint8(VERSION_OFFSET);
  if (version !== PROTOCOL_VERSION) {
    return null;
  }

  if (view.getUint8(RESERVED_OFFSET) !== 0) {
    return null;
  }

  const packetType = view.getUint8(PACKET_TYPE_OFFSET);
  if (packetType !== PACKET_TYPE_FRAME) {
    return null;
  }

  const payloadLen = view.getUint32(PAYLOAD_LEN_OFFSET, false);
  const packetSize = PROTOCOL_HEADER_SIZE + payloadLen;
  if (packetSize !== buffer.byteLength) {
    return null;
  }

  if (payloadLen < FRAME_METADATA_SIZE) {
    return null;
  }

  const payloadStart = PROTOCOL_HEADER_SIZE;
  const width = view.getUint16(payloadStart + FRAME_WIDTH_OFFSET, false);
  const height = view.getUint16(payloadStart + FRAME_HEIGHT_OFFSET, false);
  const jpegQuality = view.getUint8(payloadStart + FRAME_QUALITY_OFFSET);

  return {
    version,
    packetType,
    frameId: view.getUint32(SEQUENCE_ID_OFFSET, false),
    timestampNs: view.getBigUint64(TIMESTAMP_NS_OFFSET, false),
    width,
    height,
    jpegQuality,
    payloadLen,
    jpegPayloadLen: payloadLen - FRAME_METADATA_SIZE,
  };
}

export function extractJpegPayload(
  buffer: ArrayBuffer,
  header: FrameHeader,
): Uint8Array {
  const payloadStart = PROTOCOL_HEADER_SIZE + FRAME_METADATA_SIZE;
  const payloadEnd = payloadStart + header.jpegPayloadLen;

  return new Uint8Array(buffer, payloadStart, payloadEnd - payloadStart);
}
