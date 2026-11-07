import { Buffer } from 'node:buffer';
import { Readable, Writable } from 'node:stream';
import { HeaderSize, MaxPayload, ProtocolError, Version } from './codec.js';
import { Kind } from './messages.js';
const empty = Buffer.alloc(0);
export interface Frame { kind: Kind; id: number; payload: Buffer }
export function validFrame(kind: Kind, id: number, size: number): boolean {
  if (!Number.isInteger(id) || id < 0 || id > 0xffffffff || size < 0 || size > MaxPayload) return false;
  if (kind === Kind.Shutdown || kind === Kind.ActionActivate) return id === 0 && size === 0;
  if (kind === Kind.ActionUpdate || kind === Kind.UIUpdate) return id === 0;
  if (kind === Kind.ActionResponse || kind === Kind.UIResponse || kind === Kind.SettingsResponse || kind === Kind.FileChangesResponse) return id !== 0 && size === 0;
  return kind >= Kind.Initialize && kind <= Kind.LifecycleResponse && id !== 0;
}
export class Reader {
  private readonly source: AsyncIterator<Buffer>;
  private chunk: Buffer = Buffer.alloc(0);
  private offset = 0;
  private readonly header = Buffer.allocUnsafe(HeaderSize);
  private payload = Buffer.alloc(0);
  constructor(input: Readable) { this.source = input[Symbol.asyncIterator](); }
  async close(): Promise<void> { await this.source.return?.(); }
  private async fill(target: Buffer, boundary = false): Promise<boolean> {
    let position = 0;
    while (position < target.length) {
      if (this.offset === this.chunk.length) {
        const next = await this.source.next();
        if (next.done) { if (boundary && position === 0) return false; throw new ProtocolError(); }
        this.chunk = next.value; this.offset = 0;
      }
      const length = Math.min(target.length - position, this.chunk.length - this.offset);
      this.chunk.copy(target, position, this.offset, this.offset + length);
      position += length; this.offset += length;
    }
    return true;
  }
  // Payload is borrowed until the next read. Handlers run sequentially.
  async read(): Promise<Frame | null> {
    if (!await this.fill(this.header, true)) return null;
    const h = this.header, kind = h.readUInt16LE(6), id = h.readUInt32LE(8), size = h.readUInt32LE(12);
    if (h.readUInt32LE(0) !== 0x00626570 || h.readUInt16LE(4) !== Version || !validFrame(kind, id, size)) throw new ProtocolError();
    if (size > this.payload.length) this.payload = Buffer.allocUnsafe(size);
    const payload = this.payload.subarray(0, size);
    await this.fill(payload);
    return { kind, id, payload };
  }
}
export class Writer {
  private readonly header = Buffer.allocUnsafe(HeaderSize);
  constructor(private readonly output: Writable) {}
  private async write(data: Buffer): Promise<void> {
    if (!data.length) return;
    await new Promise<void>((resolve, reject) => this.output.write(data, error => error ? reject(error) : resolve()));
  }
  // One owner must serialize calls and await completion before reusing payload.
  async send(kind: Kind, id: number, payload: Buffer = empty): Promise<void> {
    if (!validFrame(kind, id, payload.length)) throw new ProtocolError();
    const h = this.header;
    h.writeUInt32LE(0x00626570, 0); h.writeUInt16LE(Version, 4); h.writeUInt16LE(kind, 6);
    h.writeUInt32LE(id, 8); h.writeUInt32LE(payload.length, 12);
    await this.write(h); await this.write(payload);
  }
}
