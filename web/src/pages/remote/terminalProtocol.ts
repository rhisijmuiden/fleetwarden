// The remote terminal protocol (internal/remote/terminal.go). Every message
// is one binary WebSocket message whose first byte is its type.

export const TermMsg = {
  Info: 0x10,
  Output: 0x11,
  Exit: 0x12,
  Event: 0x04,
  Input: 0x91,
  Resize: 0x92,
} as const;

export type TermInfo = { hostname: string; platform: "linux" | "windows"; shell: string; user: string };
export type TermExit = { code: number };

const enc = new TextEncoder();

export function input(text: string): ArrayBuffer {
  const body = enc.encode(text);
  const b = new Uint8Array(1 + body.length);
  b[0] = TermMsg.Input;
  b.set(body, 1);
  return b.buffer;
}

export function resize(cols: number, rows: number): ArrayBuffer {
  const b = new ArrayBuffer(5);
  const v = new DataView(b);
  v.setUint8(0, TermMsg.Resize);
  v.setUint16(1, cols);
  v.setUint16(3, rows);
  return b;
}

export function decodeJSON<T>(buf: ArrayBuffer): T {
  return JSON.parse(new TextDecoder().decode(new Uint8Array(buf, 1))) as T;
}
