import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { Readable, PassThrough } from 'node:stream';
import test from 'node:test';
import * as peb from '../src/index.js';
import type { Codec } from '../src/index.js';

test('all TS codecs preserve Go-generated PEB v18 wire bytes', () => {
  const fixtures = JSON.parse(execFileSync('go', ['run', './testdata/interop.go'], { encoding: 'utf8' })) as { Name: string; Data: string }[];
  const codecs: Record<string, Codec<unknown>> = {
    Hello: peb.HelloCodec, Contributions: peb.ContributionsCodec, EditorMessage: peb.EditorMessageCodec,
    EditorReply: peb.EditorReplyCodec, EmptyReply: peb.EditorReplyCodec, ActionContext: peb.ActionContextCodec,
    ActionChange: peb.ActionChangeCodec, UI: peb.UICodec, UIPatch: peb.UICodec, UIEvent: peb.UIEventCodec,
    Settings: peb.SettingsCodec, FileChanges: peb.FileChangesCodec, LifecycleEvent: peb.LifecycleEventCodec,
    LifecycleReply: peb.LifecycleReplyCodec, Failure: peb.FailureCodec,
  };
  for (const sample of fixtures) {
    const codec = codecs[sample.Name]!, wire = Buffer.from(sample.Data, 'base64');
    assert.deepEqual(peb.encode(codec, peb.decode(codec, wire)), wire, sample.Name);
    assert.throws(() => peb.decode(codec, Buffer.concat([wire, Buffer.from([0])])), sample.Name);
  }
});
test('reader accepts arbitrary splits, coalesced frames, and clean EOF', async () => {
  const stream = new PassThrough(), writer = new peb.Writer(stream);
  const chunks: Buffer[] = [];
  stream.on('data', chunk => chunks.push(Buffer.from(chunk)));
  await writer.send(peb.Kind.Initialize, 7, peb.encode(peb.HelloCodec, { host: '中文', capabilities: 511 }));
  await writer.send(peb.Kind.Shutdown, 0);
  const wire = Buffer.concat(chunks);
  for (const size of [1, 3, 16, wire.length]) {
    const fragments: Buffer[] = [];
    for (let i = 0; i < wire.length; i += size) fragments.push(wire.subarray(i, i + size));
    const reader = new peb.Reader(Readable.from(fragments));
    const first = await reader.read();
    assert.equal(first?.id, 7); assert.equal(peb.decode(peb.HelloCodec, first!.payload).host, '中文');
    assert.equal((await reader.read())?.kind, peb.Kind.Shutdown); assert.equal(await reader.read(), null);
  }
  for (const length of [1, 15, 17, wire.length - 1]) {
    const reader = new peb.Reader(Readable.from([wire.subarray(0, length)]));
    await assert.rejects(async () => { while (await reader.read()) {} }, peb.ProtocolError);
  }
  for (const mutate of [(b: Buffer) => b.writeUInt16LE(17, 4), (b: Buffer) => b.writeUInt32LE(peb.MaxPayload + 1, 12), (b: Buffer) => b.writeUInt32LE(0, 8), (b: Buffer) => b.writeUInt16LE(99, 6)]) {
    const invalid = Buffer.from(wire); mutate(invalid);
    await assert.rejects(new peb.Reader(Readable.from([invalid])).read(), peb.ProtocolError);
  }
});
test('invalid booleans, UTF-8, counts, strings and frame IDs are rejected', () => {
  assert.throws(() => peb.decode(peb.EditorReplyCodec, Buffer.from([2, 0, 0, 0])), peb.ProtocolError);
  assert.throws(() => peb.decode(peb.FailureCodec, Buffer.from([1, 0, 0, 0, 255])), peb.ProtocolError);
  assert.throws(() => peb.encode(peb.FailureCodec, { message: '\ud800' }), peb.ProtocolError);
  assert.throws(() => peb.encode(peb.FailureCodec, { message: 'x'.repeat(peb.MaxString + 1) }), peb.ProtocolError);
  assert.throws(() => peb.decode(peb.ContributionsCodec, Buffer.from([255, 255, 255, 255])), peb.ProtocolError);
  assert.equal(peb.validFrame(peb.Kind.SettingsResponse, 1, 1), false);
  assert.equal(peb.validFrame(peb.Kind.UIUpdate, 1, 0), false);
});
test('serve keeps read-only replies distinct from clearing text and responds to errors', async () => {
  const input = new PassThrough(), output = new PassThrough(), reader = new peb.Reader(output), writer = new peb.Writer(input);
  const serving = peb.serveExtension({ actions: [{ id: 'office', location: peb.ActionLocation.OpenWith }] }, {
    editor(message) { if (message.event.kind === 'error') throw new Error('test error'); return { view: { html: '<p>你好</p>' } }; },
  }, input, output);
  await writer.send(peb.Kind.Initialize, 1, peb.encode(peb.HelloCodec, { capabilities: 511 }));
  const registration = await reader.read(); assert.equal(registration?.kind, peb.Kind.Register);
  assert.equal(peb.decode(peb.ContributionsCodec, registration!.payload).actionService, false);
  await writer.send(peb.Kind.EditorRequest, 2, peb.encode(peb.EditorMessageCodec, { event: { kind: 'open' } }));
  const response = await reader.read(), reply = peb.decode(peb.EditorReplyCodec, response!.payload);
  assert.equal(reply.view?.html, '<p>你好</p>'); assert.equal(reply.content, null);
  await writer.send(peb.Kind.EditorRequest, 3, peb.encode(peb.EditorMessageCodec, { event: { kind: 'error' } }));
  const error = await reader.read(); assert.equal(error?.kind, peb.Kind.Failure);
  assert.equal(peb.decode(peb.FailureCodec, error!.payload).message, 'test error');
  await writer.send(peb.Kind.Shutdown, 0); await serving;
});
