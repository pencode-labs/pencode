import { Buffer } from 'node:buffer';

export const Version = 19;
export const HeaderSize = 16;
export const MaxPayload = 4 << 20;
export const MaxString = 64 << 10;
export const MaxDocument = 2 << 20;
export const MaxItems = 16384;
export class ProtocolError extends Error {
  constructor() { super('peb: invalid protocol message'); }
}
export type Input<T> = T extends readonly (infer V)[] ? Input<V>[] : T extends object ? { [K in keyof T]?: Input<T[K]> } : T;

// Encoders retain a growing buffer; payloads are borrowed until the next reset.
export class Encoder {
  private buffer = Buffer.allocUnsafe(1024);
  private offset = 0;
  reset(): void { this.offset = 0; }
  private reserve(size: number): void {
    const end = this.offset + size;
    if (end > MaxPayload) throw new ProtocolError();
    if (end > this.buffer.length) {
      const next = Buffer.allocUnsafe(Math.min(MaxPayload, Math.max(end, this.buffer.length * 2)));
      this.buffer.copy(next, 0, 0, this.offset);
      this.buffer = next;
    }
  }
  u32(value: number): void {
    if (!Number.isInteger(value) || value < 0 || value > 0xffffffff) throw new ProtocolError();
    this.reserve(4); this.buffer.writeUInt32LE(value, this.offset); this.offset += 4;
  }
  f32(value: number): void {
    if (!Number.isFinite(value) || !Number.isFinite(Math.fround(value))) throw new ProtocolError();
    this.reserve(4); this.buffer.writeFloatLE(value, this.offset); this.offset += 4;
  }
  string(value: string, limit = MaxString): void {
    if (!value.isWellFormed()) throw new ProtocolError();
    const length = Buffer.byteLength(value);
    if (length > limit) throw new ProtocolError();
    this.u32(length); this.reserve(length);
    this.offset += this.buffer.write(value, this.offset, length, 'utf8');
  }
  bytes(): Buffer { return this.buffer.subarray(0, this.offset); }
}
const utf8 = new TextDecoder('utf-8', { fatal: true, ignoreBOM: true });
export class Decoder {
  private offset = 0;
  constructor(private readonly buffer: Buffer) { if (buffer.length > MaxPayload) throw new ProtocolError(); }
  private take(size: number): number {
    const start = this.offset;
    if (size > this.buffer.length - start) throw new ProtocolError();
    this.offset += size; return start;
  }
  u32(): number { return this.buffer.readUInt32LE(this.take(4)); }
  f32(): number {
    const value = this.buffer.readFloatLE(this.take(4));
    if (!Number.isFinite(value)) throw new ProtocolError();
    return value;
  }
  string(limit = MaxString): string {
    const size = this.u32();
    if (size > limit) throw new ProtocolError();
    const start = this.take(size);
    try { return utf8.decode(this.buffer.subarray(start, start + size)); }
    catch { throw new ProtocolError(); }
  }
  count(): number {
    const count = this.u32();
    if (count > MaxItems || count > (this.buffer.length - this.offset) / 4) throw new ProtocolError();
    return count;
  }
  done(): void { if (this.offset !== this.buffer.length) throw new ProtocolError(); }
}
export interface Codec<T> { write(e: Encoder, value: T | undefined): void; read(d: Decoder): T }
export const u32: Codec<number> = { write: (e, v) => e.u32(v ?? 0), read: d => d.u32() };
export const f32: Codec<number> = { write: (e, v) => e.f32(v ?? 0), read: d => d.f32() };
export const bool: Codec<boolean> = {
  write(e, v) { if (v !== undefined && typeof v !== 'boolean') throw new ProtocolError(); e.u32(v ? 1 : 0); },
  read(d) { const v = d.u32(); if (v > 1) throw new ProtocolError(); return v === 1; },
};
export const str: Codec<string> = { write: (e, v) => e.string(v ?? ''), read: d => d.string() };
export const text: Codec<string> = { write: (e, v) => e.string(v ?? '', MaxDocument), read: d => d.string(MaxDocument) };
export function array<T>(item: Codec<T>): Codec<T[]> {
  return {
    write(e, values = []) { if (values.length > MaxItems) throw new ProtocolError(); e.u32(values.length); for (const v of values) item.write(e, v); },
    read(d) { return Array.from({ length: d.count() }, () => item.read(d)); },
  };
}
export function optional<T>(item: Codec<T>): Codec<T | null> {
  return { write(e, v) { bool.write(e, v != null); if (v != null) item.write(e, v); }, read(d) { return bool.read(d) ? item.read(d) : null; } };
}
export function struct<T extends object>(fields: { [K in keyof T]: Codec<T[K]> }): Codec<T> {
  const keys = Object.keys(fields) as (keyof T)[];
  return {
    write(e, value) { for (const key of keys) fields[key].write(e, value?.[key]); },
    read(d) { const value = {} as T; for (const key of keys) value[key] = fields[key].read(d); return value; },
  };
}
export function encode<T>(codec: Codec<T>, value: Input<T>, e = new Encoder()): Buffer {
  e.reset(); codec.write(e, value as T); return e.bytes();
}
export function decode<T>(codec: Codec<T>, data: Buffer): T {
  const d = new Decoder(data), value = codec.read(d); d.done(); return value;
}
