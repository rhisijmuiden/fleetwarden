// The remote desktop protocol between this viewer and the agent's desktop
// helper (internal/remote/proto.go). The server relays it unchanged.

export const Msg = {
  Info: 0x01,
  Frame: 0x02,
  Clipboard: 0x03,
  Event: 0x04,
  RTC: 0x05,
  Mouse: 0x81,
  Key: 0x82,
  Ack: 0x83,
  SetClip: 0x84,
  Control: 0x85,
  RTCAnswer: 0x86,
} as const;

export const Mouse = { Move: 0, Down: 1, Up: 2, Wheel: 3, HWheel: 4 } as const;

export type Monitor = { index: number; name: string; x: number; y: number; width: number; height: number; primary: boolean };

export type Info = {
  monitors: Monitor[];
  monitor: number;
  width: number;
  height: number;
  desktop: string;
  hostname: string;
  user?: string;
  quality: Quality;
  features: string[] | null;
};

export type Quality = "low" | "medium" | "high";

export type RemoteEvent = { type: "desktop" | "error" | "notice" | "path"; desktop?: string; code?: string; message?: string };

export type Tile = { x: number; y: number; w: number; h: number; enc: number; data: Uint8Array<ArrayBuffer> };

export function decodeFrame(buf: ArrayBuffer): { seq: number; tiles: Tile[] } {
  const v = new DataView(buf);
  const seq = v.getUint32(1);
  const n = v.getUint16(5);
  const tiles: Tile[] = [];
  let o = 7;
  for (let i = 0; i < n; i++) {
    const len = v.getUint32(o + 9);
    tiles.push({
      x: v.getUint16(o),
      y: v.getUint16(o + 2),
      w: v.getUint16(o + 4),
      h: v.getUint16(o + 6),
      enc: v.getUint8(o + 8),
      data: new Uint8Array(buf, o + 13, len),
    });
    o += 13 + len;
  }
  return { seq, tiles };
}

const text = new TextDecoder();
const utf8 = new TextEncoder();

export function decodeJSON<T>(buf: ArrayBuffer): T {
  return JSON.parse(text.decode(new Uint8Array(buf, 1))) as T;
}

export function decodeText(buf: ArrayBuffer): string {
  return text.decode(new Uint8Array(buf, 1));
}

export function mouse(action: number, button: number, x: number, y: number, wheel = 0): ArrayBuffer {
  const b = new ArrayBuffer(9);
  const v = new DataView(b);
  v.setUint8(0, Msg.Mouse);
  v.setUint8(1, action);
  v.setUint8(2, button);
  v.setInt16(3, wheel);
  v.setUint16(5, Math.max(0, Math.min(65535, Math.round(x))));
  v.setUint16(7, Math.max(0, Math.min(65535, Math.round(y))));
  return b;
}

export function key(down: boolean, scan: number): ArrayBuffer {
  const b = new ArrayBuffer(4);
  const v = new DataView(b);
  v.setUint8(0, Msg.Key);
  v.setUint8(1, down ? 1 : 0);
  v.setUint16(2, scan);
  return b;
}

export function ack(seq: number): ArrayBuffer {
  const b = new ArrayBuffer(5);
  const v = new DataView(b);
  v.setUint8(0, Msg.Ack);
  v.setUint32(1, seq);
  return b;
}

function withType(type: number, body: Uint8Array): ArrayBuffer {
  const out = new Uint8Array(body.length + 1);
  out[0] = type;
  out.set(body, 1);
  return out.buffer;
}

export function setClipboard(value: string): ArrayBuffer {
  return withType(Msg.SetClip, utf8.encode(value));
}

export type Control =
  | { op: "monitor"; monitor: number }
  | { op: "quality"; quality: Quality }
  | { op: "refresh" }
  | { op: "cad" }
  | { op: "type"; text: string };

export function control(c: Control): ArrayBuffer {
  return withType(Msg.Control, utf8.encode(JSON.stringify(c)));
}

// The direct path (WebRTC data channel), set up over the relay.
export type RTCSignal = { type: "request" | "offer" | "answer" | "failed"; sdp?: string };

export function rtcSignal(s: RTCSignal): ArrayBuffer {
  return withType(Msg.RTCAnswer, utf8.encode(JSON.stringify(s)));
}

// Data channel messages carry a session message in chunks: the first byte
// says whether more follow (0) or this is the last one (1).
export const ChunkSize = 64 * 1024 - 1;

export function* chunks(m: ArrayBuffer): Generator<ArrayBuffer> {
  const src = new Uint8Array(m);
  for (let off = 0; ; ) {
    const end = Math.min(off + ChunkSize, src.length);
    const c = new Uint8Array(1 + end - off);
    c[0] = end === src.length ? 1 : 0;
    c.set(src.subarray(off, end), 1);
    yield c.buffer;
    off = end;
    if (end === src.length) return;
  }
}

// PC/AT set 1 scan codes by KeyboardEvent.code; 0xE0xx are extended keys.
// Scan codes keep the remote keyboard layout, as on a real keyboard.
export const scanCodes: Record<string, number> = {
  Escape: 0x01,
  Digit1: 0x02,
  Digit2: 0x03,
  Digit3: 0x04,
  Digit4: 0x05,
  Digit5: 0x06,
  Digit6: 0x07,
  Digit7: 0x08,
  Digit8: 0x09,
  Digit9: 0x0a,
  Digit0: 0x0b,
  Minus: 0x0c,
  Equal: 0x0d,
  Backspace: 0x0e,
  Tab: 0x0f,
  KeyQ: 0x10,
  KeyW: 0x11,
  KeyE: 0x12,
  KeyR: 0x13,
  KeyT: 0x14,
  KeyY: 0x15,
  KeyU: 0x16,
  KeyI: 0x17,
  KeyO: 0x18,
  KeyP: 0x19,
  BracketLeft: 0x1a,
  BracketRight: 0x1b,
  Enter: 0x1c,
  ControlLeft: 0x1d,
  KeyA: 0x1e,
  KeyS: 0x1f,
  KeyD: 0x20,
  KeyF: 0x21,
  KeyG: 0x22,
  KeyH: 0x23,
  KeyJ: 0x24,
  KeyK: 0x25,
  KeyL: 0x26,
  Semicolon: 0x27,
  Quote: 0x28,
  Backquote: 0x29,
  ShiftLeft: 0x2a,
  Backslash: 0x2b,
  KeyZ: 0x2c,
  KeyX: 0x2d,
  KeyC: 0x2e,
  KeyV: 0x2f,
  KeyB: 0x30,
  KeyN: 0x31,
  KeyM: 0x32,
  Comma: 0x33,
  Period: 0x34,
  Slash: 0x35,
  ShiftRight: 0x36,
  NumpadMultiply: 0x37,
  AltLeft: 0x38,
  Space: 0x39,
  CapsLock: 0x3a,
  F1: 0x3b,
  F2: 0x3c,
  F3: 0x3d,
  F4: 0x3e,
  F5: 0x3f,
  F6: 0x40,
  F7: 0x41,
  F8: 0x42,
  F9: 0x43,
  F10: 0x44,
  NumLock: 0x45,
  ScrollLock: 0x46,
  Numpad7: 0x47,
  Numpad8: 0x48,
  Numpad9: 0x49,
  NumpadSubtract: 0x4a,
  Numpad4: 0x4b,
  Numpad5: 0x4c,
  Numpad6: 0x4d,
  NumpadAdd: 0x4e,
  Numpad1: 0x4f,
  Numpad2: 0x50,
  Numpad3: 0x51,
  Numpad0: 0x52,
  NumpadDecimal: 0x53,
  IntlBackslash: 0x56,
  F11: 0x57,
  F12: 0x58,
  IntlRo: 0x73,
  Convert: 0x79,
  NonConvert: 0x7b,
  IntlYen: 0x7d,
  NumpadEnter: 0xe01c,
  ControlRight: 0xe01d,
  NumpadDivide: 0xe035,
  PrintScreen: 0xe037,
  AltRight: 0xe038,
  Home: 0xe047,
  ArrowUp: 0xe048,
  PageUp: 0xe049,
  ArrowLeft: 0xe04b,
  ArrowRight: 0xe04d,
  End: 0xe04f,
  ArrowDown: 0xe050,
  PageDown: 0xe051,
  Insert: 0xe052,
  Delete: 0xe053,
  MetaLeft: 0xe05b,
  MetaRight: 0xe05c,
  ContextMenu: 0xe05d,
  AudioVolumeMute: 0xe020,
  AudioVolumeDown: 0xe02e,
  AudioVolumeUp: 0xe030,
  MediaPlayPause: 0xe022,
  MediaStop: 0xe024,
  MediaTrackNext: 0xe019,
  MediaTrackPrevious: 0xe010,
};

// Key combinations from the toolbar, pressed in order and released in
// reverse.
export const combos: Record<string, string[]> = {
  altTab: ["AltLeft", "Tab"],
  win: ["MetaLeft"],
  winR: ["MetaLeft", "KeyR"],
  winL: ["MetaLeft", "KeyL"],
  ctrlEsc: ["ControlLeft", "Escape"],
  ctrlShiftEsc: ["ControlLeft", "ShiftLeft", "Escape"],
  altF4: ["AltLeft", "F4"],
  printScreen: ["PrintScreen"],
};
