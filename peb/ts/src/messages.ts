import { array, bool, decode, encode, f32, optional, ProtocolError, str, struct, text, u32 } from './codec.js';
import type { Codec } from './codec.js';
export { decode, encode };
export enum Kind {
  Initialize = 1, Register, Shutdown, Failure, EditorRequest, EditorResponse,
  ActionActivate, ActionInvoke, ActionResponse, ActionUpdate, UIUpdate, UIRequest,
  UIResponse, SettingsRequest, SettingsResponse, FileChangesRequest,
  FileChangesResponse, LifecycleRequest, LifecycleResponse,
}
export enum Capability {
  FileIcons = 1, EditorThemes = 2, CustomEditors = 4, Fonts = 8,
  LanguageServers = 16, Actions = 32, ExtensionUI = 64, DebugAdapters = 128,
  ExtensionSettings = 256, All = 511,
}
export enum Match { DefaultFile, FileName, FileSuffix, DefaultFolder, FolderName }
export enum ActionLocation { StatusBar, LeftContextMenu, RightContextMenu, TitleBarRight, OpenWith, Command }
export enum UIKind { Dialog, ContextMenu, Panel, Popup, Notification, EditorTab }
export enum UIPosition { Default, Absolute, Anchor, AnchorAbove }
export enum Severity { Info, Warning, Error }
export enum SettingType { String, Boolean, Number, Integer }
export enum FileOperation { Created = 1, Written = 2, Removed = 4, Renamed = 8, MetadataChanged = 16 }
export enum LifecycleKind { WindowClosing, ProjectClosing, ProjectClosed }
export type Value<C> = C extends Codec<infer T> ? T : never;
function range(max: number): Codec<number> {
  return {
    write(e, v = 0) { if (v > max) throw new ProtocolError(); u32.write(e, v); },
    read(d) { const v = u32.read(d); if (v > max) throw new ProtocolError(); return v; },
  };
}
export const HelloCodec = struct({ host: str, capabilities: u32 });
export const FailureCodec = struct({ message: str });
export const SelectorCodec = struct({ match: u32, pattern: str, languageID: str });
export const ActionCodec = struct({
  id: str, text: str, tooltip: str, icon: str, color: str, iconColor: str, iconStyle: str,
  shortcut: str, location: range(ActionLocation.Command), disabled: bool, hidden: bool,
  default: bool, selectors: array(SelectorCodec),
});
export const ActionContextCodec = struct({ id: str, projectPath: str, path: str, directory: bool, caret: u32, anchor: u32 });
export const ActionChangeCodec = struct({ action: ActionCodec, remove: bool });
export const EventCodec = struct({
  kind: str, ref: str, value: text, inputType: str, data: text, checked: bool,
  selected: u32, anchor: u32, focus: u32,
});
export const ViewCodec = struct({ html: text, css: text, events: array(struct({ ref: str, kind: str })) });
export const EditorMessageCodec = struct({ editor: str, path: str, content: text, binary: bool, event: EventCodec });
export const EditorReplyCodec = struct({ view: optional(ViewCodec), content: optional(text) });
export const SettingCodec = struct({
  id: str, label: str, description: str, type: range(SettingType.Integer), default: str,
  options: array(struct({ value: str, label: str })),
});
export const ContributionsCodec = struct({
  themes: array(struct({ id: str, label: str, path: str })),
  iconThemes: array(struct({ id: str, label: str, rules: array(struct({ match: range(Match.FolderName), pattern: str, path: str })) })),
  fonts: array(struct({ id: str, label: str, path: str, boldPath: str })),
  languageServers: array(struct({
    id: str, label: str, languageID: str, command: str, args: array(str),
    selectors: array(SelectorCodec), initializationOptions: text, configuration: text,
  })),
  actions: array(ActionCodec), actionService: bool,
  debugAdapters: array(struct({ id: str, label: str, command: str, args: array(str), templates: text })),
  settings: array(SettingCodec),
});
export const SettingsCodec: Codec<Record<string, string>> = {
  write(e, values = {}) {
    const entries = Object.entries(values);
    if (entries.length > 16384) throw new ProtocolError();
    e.u32(entries.length);
    for (const [id, value] of entries) { if (!id) throw new ProtocolError(); try { JSON.parse(value); } catch { throw new ProtocolError(); } e.string(id); e.string(value); }
  },
  read(d) {
    const values: Record<string, string> = Object.create(null);
    for (let n = d.count(); n > 0; n--) {
      const id = d.string(), value = d.string();
      if (!id || Object.hasOwn(values, id)) throw new ProtocolError();
      try { JSON.parse(value); } catch { throw new ProtocolError(); } values[id] = value;
    }
    return values;
  },
};
export const FileChangesCodec = struct({ root: str, reset: bool, files: array(struct({ path: str, op: u32, directory: bool })) });
export const LifecycleEventCodec = struct({ kind: range(LifecycleKind.ProjectClosed), projectPath: str });
export const LifecycleReplyCodec = struct({ message: str });
export const UICodec = struct({
  id: str, title: str, anchor: str, kind: range(UIKind.EditorTab), close: bool,
  placement: struct({ mode: range(UIPosition.AnchorAbove), x: f32, y: f32, width: f32, height: f32 }),
  closeOnOutsideClick: bool, autoCloseMS: u32, severity: range(Severity.Error), busy: bool,
  unread: bool, interceptClose: bool, context: ActionContextCodec, view: optional(ViewCodec),
  viewVersion: u32, patch: optional(struct({ base: u32, regions: array(struct({ ref: str, html: text })) })),
});
export const UIEventCodec = struct({ id: str, reason: str, context: ActionContextCodec, event: EventCodec });
export type Contributions = Value<typeof ContributionsCodec>;
export type Action = Value<typeof ActionCodec>;
export type ActionContext = Value<typeof ActionContextCodec>;
export type EditorMessage = Value<typeof EditorMessageCodec>;
export type EditorReply = Value<typeof EditorReplyCodec>;
export type EditorView = Value<typeof ViewCodec>;
export type UI = Value<typeof UICodec>;
export type UIEvent = Value<typeof UIEventCodec>;
export type FileChanges = Value<typeof FileChangesCodec>;
export type LifecycleEvent = Value<typeof LifecycleEventCodec>;
export type Setting = Value<typeof SettingCodec>;

export function validateSetting(setting: Setting, raw: string): void {
  let value: unknown;
  try { value = JSON.parse(raw); } catch { throw new ProtocolError(); }
  const types = ['string', 'boolean', 'number', 'number'];
  if (typeof value !== types[setting.type]) throw new ProtocolError();
  if (typeof value === 'number' && !Number.isFinite(value)) throw new ProtocolError();
  // Validate the decimal token directly so signed int64 settings retain precision.
  if (setting.type === SettingType.Integer && (!/^-?(0|[1-9]\d*)$/.test(raw.trim()) || BigInt(raw) < -(1n << 63n) || BigInt(raw) >= (1n << 63n))) throw new ProtocolError();
  if (setting.options.length && !setting.options.some(o => o.value === value)) throw new ProtocolError();
}
export function validateSettings(settings: Setting[]): void {
  if (settings.length > 256) throw new ProtocolError();
  const ids = new Set<string>();
  for (const s of settings) {
    if (!s.id || /[/\\\0]/.test(s.id) || !s.label.trim() || ids.has(s.id) || s.options.length > 256 || (s.options.length && s.type !== SettingType.String)) throw new ProtocolError();
    ids.add(s.id);
    const options = new Set<string>();
    for (const o of s.options) {
      if (!o.label || options.has(o.value)) throw new ProtocolError();
      options.add(o.value);
    }
    validateSetting(s, s.default);
  }
}
