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
export const PACKET_TYPE_ACK = 0x05;
export const PACKET_TYPE_ERROR = 0x07;

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
  const id = new TextEncoder().encode(agentId);
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
  const roleBytes = new TextEncoder().encode(role.toLowerCase());
  const tokenBytes = new TextEncoder().encode(token);

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

export function decodeErrorPayload(payload: Uint8Array): { reason: string; detail: string } | null {
  if (payload.length < 3) {
    return null;
  }

  const reasonLen = payload[0];
  if (payload.length < 1 + reasonLen + 2) {
    return null;
  }

  const reason = new TextDecoder().decode(payload.slice(1, 1 + reasonLen));
  const detailLenOffset = 1 + reasonLen;
  const detailLen = ((payload[detailLenOffset] << 8) | payload[detailLenOffset + 1]) >>> 0;

  if (payload.length < detailLenOffset + 2 + detailLen) {
    return null;
  }

  const detail = new TextDecoder().decode(payload.slice(detailLenOffset + 2, detailLenOffset + 2 + detailLen));

  return { reason, detail };
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
