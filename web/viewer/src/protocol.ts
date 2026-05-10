export const HEADER_SIZE = 15;

export const VERSION_OFFSET = 0;
export const PACKET_TYPE_OFFSET = 1;
export const FRAME_ID_OFFSET = 2;
export const WIDTH_OFFSET = 6;
export const HEIGHT_OFFSET = 8;
export const JPEG_QUALITY_OFFSET = 10;
export const PAYLOAD_LEN_OFFSET = 11;

export const FRAME_PACKET_TYPE = 0x01;

export interface FrameHeader {
  version: number;
  packetType: number;
  frameId: number;
  width: number;
  height: number;
  jpegQuality: number;
  payloadLen: number;
}

export function parseFrameHeader(buffer: ArrayBuffer): FrameHeader | null {
  if (buffer.byteLength < HEADER_SIZE) {
    return null;
  }

  const view = new DataView(buffer);

  const packetType = view.getUint8(PACKET_TYPE_OFFSET);
  if (packetType !== FRAME_PACKET_TYPE) {
    return null;
  }

  const payloadLen = view.getUint32(PAYLOAD_LEN_OFFSET, false);
  const frameSize = HEADER_SIZE + payloadLen;

  if (frameSize > buffer.byteLength) {
    return null;
  }

  return {
    version: view.getUint8(VERSION_OFFSET),
    packetType,
    frameId: view.getUint32(FRAME_ID_OFFSET, false),
    width: view.getUint16(WIDTH_OFFSET, false),
    height: view.getUint16(HEIGHT_OFFSET, false),
    jpegQuality: view.getUint8(JPEG_QUALITY_OFFSET),
    payloadLen,
  };
}

export function extractJpegPayload(
  buffer: ArrayBuffer,
  header: FrameHeader,
): Uint8Array {
  const payloadStart = HEADER_SIZE;
  const payloadEnd = payloadStart + header.payloadLen;

  return new Uint8Array(buffer, payloadStart, payloadEnd - payloadStart);
}
