import { Readable, Writable } from 'node:stream';
import { Encoder, ProtocolError, decode, encode } from './codec.js';
import type { Codec, Input } from './codec.js';
import {
  ActionChangeCodec, ActionContextCodec, ActionLocation, Capability, ContributionsCodec,
  EditorMessageCodec, EditorReplyCodec, FailureCodec, FileChangesCodec, HelloCodec,
  Kind, LifecycleEventCodec, LifecycleReplyCodec, SettingsCodec, UICodec, UIEventCodec,
  validateSetting, validateSettings,
} from './messages.js';
import type { Action, ActionContext, Contributions, EditorMessage, EditorReply, FileChanges, LifecycleEvent, UI, UIEvent } from './messages.js';
import { Reader, Writer } from './stream.js';
type Awaitable<T> = T | Promise<T>;
export interface Handlers {
  // Stateful views create a fresh handler and release it when signal is aborted.
  newEditor?(signal: AbortSignal): NonNullable<Handlers['editor']>;
  editor?(message: EditorMessage): Awaitable<Input<EditorReply>>;
  settings?(values: Record<string, string>): Awaitable<void>;
  fileChanges?(changes: FileChanges): Awaitable<void>;
  lifecycle?(event: LifecycleEvent): Awaitable<{ message?: string }>;
  action?(context: ActionContext): Awaitable<void>;
  uiEvent?(event: UIEvent): Awaitable<void>;
  activate?(publisher: Publisher, signal: AbortSignal): Awaitable<void>;
}
export class Publisher {
  private tail: Promise<void> = Promise.resolve();
  private readonly encoder = new Encoder();
  context: ActionContext = decode(ActionContextCodec, encode(ActionContextCodec, {}));
  constructor(private readonly writer: Writer, private readonly signal: AbortSignal, private readonly ui: boolean, private readonly session = 0) {}
  send<T>(kind: Kind, id: number, codec?: Codec<T>, value?: Input<T>): Promise<void> {
    const task = this.tail.then(async () => {
      this.signal.throwIfAborted();
      await this.writer.send(kind, id, codec ? encode(codec, value!, this.encoder) : undefined, this.session);
    });
    this.tail = task.catch(() => {});
    return task;
  }
  update(action: Input<Action>): Promise<void> { return this.send(Kind.ActionUpdate, 0, ActionChangeCodec, { action }); }
  remove(id: string): Promise<void> { return this.send(Kind.ActionUpdate, 0, ActionChangeCodec, { action: { id }, remove: true }); }
  show(ui: Input<UI>): Promise<void> {
    if (!this.ui) return Promise.reject(new ProtocolError());
    return this.send(Kind.UIUpdate, 0, UICodec, { ...ui, context: ui.context?.id ? ui.context : this.context });
  }
  closeUI(id: string): Promise<void> { return this.show({ id, close: true }); }
}
export async function serveExtension(
  contributions: Input<Contributions>, handlers: Handlers = {},
  input: Readable = process.stdin, output: Writable = process.stdout,
): Promise<void> {
  const reader = new Reader(input), writer = new Writer(output);
  const sessions = new Map<number, { abort: AbortController; publisher: Publisher; active: boolean; editor?: Handlers['editor'] }>();
  const c = decode(ContributionsCodec, encode(ContributionsCodec, {
    ...contributions, actionService: !!(handlers.action || handlers.uiEvent || handlers.activate || handlers.fileChanges || handlers.lifecycle),
  }));
  validateSettings(c.settings);
  try {
    for (;;) {
      const frame = await reader.read();
      if (!frame) return;
      if (frame.kind === Kind.Shutdown) {
        if (frame.session === 0) return;
        sessions.get(frame.session)?.abort.abort(); sessions.delete(frame.session); continue;
      }
      if (frame.kind === Kind.Initialize) {
        if (sessions.has(frame.session) || sessions.size >= 256) throw new ProtocolError();
        const abort = new AbortController(), publisher = new Publisher(writer, abort.signal, !!handlers.uiEvent, frame.session);
        sessions.set(frame.session, { abort, publisher, active: false });
        const first = frame;
        const hello = decode(HelloCodec, first.payload);
        let required = c.actions.length || c.actionService ? Capability.Actions : 0;
        if (c.languagePacks.length) required |= Capability.LanguagePacks;
        if (c.themes.length) required |= Capability.EditorThemes;
        if (c.iconThemes.length) required |= Capability.FileIcons;
        if (c.fonts.length) required |= Capability.Fonts;
        if (c.languageServers.length) required |= Capability.LanguageServers;
        if (c.debugAdapters.length) required |= Capability.DebugAdapters;
        if (c.settings.length) required |= Capability.ExtensionSettings;
        if (handlers.uiEvent) required |= Capability.ExtensionUI;
        const views = c.actions.some(a => a.location === ActionLocation.OpenWith);
        if (views) required |= Capability.CustomEditors;
        if ((hello.capabilities & required) !== required || (views && !handlers.editor && !handlers.newEditor) || (c.actions.some(a => a.location !== ActionLocation.OpenWith) && !handlers.action)) {
          await publisher.send(Kind.Failure, first.id, FailureCodec, { message: 'unsupported extension capabilities' }); return;
        }
        await publisher.send(Kind.Register, first.id, ContributionsCodec, c);
        continue;
      }
      const session = sessions.get(frame.session);
      if (!session) throw new ProtocolError();
      const { publisher, abort } = session;
      if (frame.kind === Kind.ActionActivate) {
        if (session.active || !c.actionService) throw new ProtocolError();
        session.active = true; await handlers.activate?.(publisher, abort.signal); continue;
      }
      try {
        switch (frame.kind) {
          case Kind.EditorRequest: {
            session.editor ??= handlers.newEditor ? handlers.newEditor(abort.signal) : handlers.editor;
            if (!session.editor) throw new ProtocolError();
            const message = decode(EditorMessageCodec, frame.payload);
            await publisher.send(Kind.EditorResponse, frame.id, EditorReplyCodec, await session.editor(message)); break;
          }
          case Kind.SettingsRequest: {
            const values = decode(SettingsCodec, frame.payload);
            if (Object.keys(values).length !== c.settings.length) throw new ProtocolError();
            for (const s of c.settings) {
              if (!Object.hasOwn(values, s.id)) throw new ProtocolError();
              validateSetting(s, values[s.id]!);
            }
            await handlers.settings?.(values);
            await publisher.send(Kind.SettingsResponse, frame.id); break;
          }
          case Kind.FileChangesRequest: {
            const changes = decode(FileChangesCodec, frame.payload);
            await handlers.fileChanges?.(changes);
            await publisher.send(Kind.FileChangesResponse, frame.id); break;
          }
          case Kind.LifecycleRequest: {
            if (!session.active) throw new ProtocolError();
            const event = decode(LifecycleEventCodec, frame.payload);
            const reply = await handlers.lifecycle?.(event) ?? {};
            await publisher.send(Kind.LifecycleResponse, frame.id, LifecycleReplyCodec, reply); break;
          }
          case Kind.ActionInvoke: {
            if (!session.active || !handlers.action) throw new ProtocolError();
            const context = decode(ActionContextCodec, frame.payload); publisher.context = context;
            await handlers.action(context); await publisher.send(Kind.ActionResponse, frame.id); break;
          }
          case Kind.UIRequest: {
            if (!session.active || !handlers.uiEvent) throw new ProtocolError();
            const event = decode(UIEventCodec, frame.payload); publisher.context = event.context;
            await handlers.uiEvent(event); await publisher.send(Kind.UIResponse, frame.id); break;
          }
          default: throw new ProtocolError();
        }
      } catch (error) {
        if (error instanceof ProtocolError) throw error;
        const message = (error instanceof Error ? error.message : String(error)).slice(0, 8192);
        await publisher.send(Kind.Failure, frame.id, FailureCodec, { message });
      }
    }
  } finally { for (const session of sessions.values()) session.abort.abort(); await reader.close(); }
}
