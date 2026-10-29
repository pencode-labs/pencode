package workbench

import (
	"encoding/json"
	"runtime"
	"testing"

	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	engineui "github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/actions"
	"github.com/pencode-labs/pencode/peb"
)

func keymapState(binding pencodeKeybinding, goos string) *input.State {
	state := input.NewState()
	if binding.Control || binding.Primary && goos != "darwin" {
		state.PressKey(input.KeyLeftControl)
	}
	if binding.Super || binding.Primary && goos == "darwin" {
		state.PressKey(input.KeyLeftSuper)
	}
	if binding.Shift {
		state.PressKey(input.KeyLeftShift)
	}
	if binding.Alt {
		state.PressKey(input.KeyLeftAlt)
	}
	state.PressKey(binding.Key)
	return state
}
func TestKeymapProfilesPersistIndependentOverrides(t *testing.T) {
	m := &workspaceModel{settings: defaultPencodeSettings()}
	event := func(ref, value string) {
		t.Helper()
		c := module.ContentEventContext[appProps, appState]{Ref: ref, Event: htmlui.Event{Value: value}}
		if !m.handleSettingsEvent(&c) {
			t.Fatalf("unhandled %s", ref)
		}
	}
	event("keymap-name", "我的方案")
	event("keymap-create", "")
	if m.settings.Keymap != "我的方案" {
		t.Fatal("profile was not selected")
	}
	m.settings.Bindings["save"] = pencodeBindingChord("Primary+L")
	m.settings.Bindings["debug-start"] = pencodeKeybinding{}
	m.settings.Bindings["extensions/unloaded/run"] = pencodeBindingChord("Alt+B")
	event("keymap-select", "default")
	if m.settings.Bindings["save"].Key != input.KeyS {
		t.Fatal("custom profile mutated default")
	}
	event("keymap-select", "我的方案")
	if m.settings.Bindings["save"].Key != input.KeyL {
		t.Fatal("profile switch lost edit")
	}
	event("keymap-name", "工作方案")
	event("keymap-rename", "")
	data, err := json.Marshal(m.settings)
	if err != nil {
		t.Fatal(err)
	}
	restored := parsePencodeSettings(data)
	if restored.Keymap != "工作方案" || restored.Bindings["save"].Key != input.KeyL || restored.Bindings["debug-start"].Key != 0 || restored.Bindings["extensions/unloaded/run"].Key != input.KeyB || restored.Keymaps["default"]["save"].Key != input.KeyS {
		t.Fatalf("lost profiles: %s", data)
	}
	m.settings = restored
	event("reset-shortcuts", "")
	if m.settings.Bindings["save"].Key != input.KeyS || m.settings.Keymap != "工作方案" {
		t.Fatal("reset changed wrong profile")
	}
	event("keymap-delete", "")
	if m.settings.Keymap != "default" || m.settings.Keymaps["工作方案"] != nil {
		t.Fatal("delete did not restore default")
	}
}
func TestActionShortcutsDispatchUpdateRemoveAndOverride(t *testing.T) {
	m := &workspaceModel{settings: defaultPencodeSettings()}
	calls := 0
	a := peb.Action{ID: "extensions/test/run", Text: "Run", Location: peb.Command, Shortcut: "Primary+Shift+B"}
	if err := m.actionRegistry().Register(actions.Definition{Action: a, Run: func(peb.ActionContext) error { calls++; return nil }}); err != nil {
		t.Fatal(err)
	}
	for _, goos := range []string{"linux", "darwin"} {
		state := keymapState(pencodeBindingChord(a.Shortcut), goos)
		if !m.handleShortcutKeys(nil, state, goos) {
			t.Fatalf("not dispatched on %s", goos)
		}
	}
	if calls != 2 || len(m.actions.rows) != 0 {
		t.Fatal("command rendered a button or did not run")
	}
	m.settings.Bindings[a.ID] = pencodeBindingChord("Alt+M")
	if m.handleActionShortcuts(nil, keymapState(pencodeBindingChord(a.Shortcut), "linux"), "linux") {
		t.Fatal("old chord still triggers")
	}
	if !m.handleActionShortcuts(nil, keymapState(pencodeBindingChord("Alt+M"), "linux"), "linux") {
		t.Fatal("user override did not trigger")
	}
	a.Disabled = true
	m.actionRegistry().Update(a)
	if m.handleActionShortcuts(nil, keymapState(pencodeBindingChord("Alt+M"), "linux"), "linux") {
		t.Fatal("disabled command triggered")
	}
	a.Disabled = false
	a.Shortcut = "F6"
	m.actionRegistry().Update(a)
	delete(m.settings.Bindings, a.ID)
	if !m.handleActionShortcuts(nil, keymapState(pencodeBindingChord("F6"), "linux"), "linux") {
		t.Fatal("live shortcut replacement ignored")
	}
	m.actionRegistry().Remove(a.ID)
	if m.handleActionShortcuts(nil, keymapState(pencodeBindingChord("F6"), "linux"), "linux") {
		t.Fatal("removed command triggered")
	}
	idle := input.NewState()
	if allocations := testing.AllocsPerRun(100, func() { m.handleActionShortcuts(nil, idle, "linux") }); allocations != 0 {
		t.Fatalf("idle shortcut handling allocated %g", allocations)
	}
}
func TestKeymapContextConflictsAndNavigation(t *testing.T) {
	m := &workspaceModel{settings: defaultPencodeSettings()}
	if m.shortcutConflict("editor-copy", pencodeBindingChord("Primary+S"), "linux") == "" {
		t.Fatal("missed global conflict")
	}
	if m.shortcutConflict("terminal-copy", pencodeBindingChord("Primary+C"), "linux") != "" {
		t.Fatal("exclusive editor context conflicts with terminal")
	}
	m.settings.Bindings["ui-next"] = pencodeBindingChord("Alt+J")
	if m.uiKey(keymapState(pencodeBindingChord("Down"), runtime.GOOS), input.KeyDown) || !m.uiKey(keymapState(pencodeBindingChord("Alt+J"), runtime.GOOS), input.KeyDown) {
		t.Fatal("list navigation not remapped")
	}
	m.settings.Bindings["ui-focus-previous"] = pencodeBindingChord("Alt+K")
	m.tabContext = pencodeTabContext{open: true, index: 1, path: "main.go", project: &pencodeProject{Files: map[string]*openFile{"main.go": {}}}}
	m.handleTabContextKeys(nil, keymapState(pencodeBindingChord("Alt+K"), runtime.GOOS))
	if m.tabContext.index != 0 {
		t.Fatal("remapped previous-focus moved in wrong direction")
	}
	for _, d := range pencodeExtraShortcuts {
		if d.chord != "" && d.defaultBinding().Key == 0 {
			t.Fatalf("invalid default for %s: %s", d.id, d.chord)
		}
	}
}
func TestShortcutSettingsUIRecordsAndSwitchesProfiles(t *testing.T) {
	app, m, _, state := languageInteractionFixture(t)
	m.settingsOpen, m.settingsShortcuts = true, true
	markPencodeRootDirtyByModel(m.fontResources, m)
	languageInteractionStep(t, app)
	languageInteractionStep(t, app)
	editorContextBounds(t, m, "keymap-select")
	editorContextClick(t, app, m, state, "shortcut-save")
	if !m.capturingShortcut || m.captureAction != "save" {
		t.Fatal("dynamic shortcut row did not start recording")
	}
	primary := input.KeyLeftControl
	if runtime.GOOS == "darwin" {
		primary = input.KeyLeftSuper
	}
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: primary})
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyL})
	languageInteractionStep(t, app)
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyL})
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: primary})
	languageInteractionStep(t, app)
	if m.capturingShortcut || m.settings.Bindings["save"].Key != input.KeyL {
		t.Fatalf("failed to record: %s", m.settingsFeedback)
	}
	c := module.ContentEventContext[appProps, appState]{Ref: "keymap-name", Event: htmlui.Event{Value: "测试"}}
	m.handleSettingsEvent(&c)
	c.Ref = "keymap-create"
	m.handleSettingsEvent(&c)
	m.settings.Bindings["save"] = pencodeBindingChord("Alt+L")
	c.Ref = "keymap-select"
	c.Event.Value = "default"
	m.handleSettingsEvent(&c)
	if m.settings.Bindings["save"].Key != input.KeyL || m.settings.Bindings["save"].Alt {
		t.Fatal("selector did not restore selected mapping")
	}
	// Clipboard uses the selected binding and suppresses the native old chord.
	m.settingsOpen = false
	m.fontResources.Focus(m.editor.control)
	field := m.fontResources.Mut[engineui.TextField](m.editor.control)
	field.Anchor, field.Caret = 0, 2
	clipboard := ""
	state.SetClipboard(func(value string) bool { clipboard = value; return true }, func() (string, bool) { return clipboard, true })
	m.settings.Bindings["editor-copy"] = pencodeBindingChord("Alt+C")
	custom := keymapState(m.settings.Bindings["editor-copy"], runtime.GOOS)
	if !m.editorClipboardShortcut(m.fontResources, custom, runtime.GOOS) || clipboard == "" {
		t.Fatal("custom clipboard shortcut failed")
	}
	m.languageUI.keysHandled = false
	if !m.editorKeyHandled(keymapState(pencodeBindingChord("Primary+C"), runtime.GOOS)) {
		t.Fatal("native old shortcut is still active")
	}
}

func TestKeymapDebugAndFileCommandsUseReplacedBindings(t *testing.T) {
	_, m, _, _ := languageInteractionFixture(t)
	project := m.activeProjectState()
	m.settings.Bindings["debug-breakpoint"] = pencodeBindingChord("F8")
	if m.handleShortcutKeys(m.fontResources, keymapState(pencodeBindingChord("F9"), runtime.GOOS), runtime.GOOS) {
		t.Fatal("old breakpoint shortcut still triggers")
	}
	if !m.handleShortcutKeys(m.fontResources, keymapState(pencodeBindingChord("F8"), runtime.GOOS), runtime.GOOS) || len(project.debugBreakpoints[m.activePath]) != 1 {
		t.Fatal("remapped breakpoint shortcut did not toggle")
	}
	m.settings.Bindings["file-context-rename"] = pencodeBindingChord("Alt+R")
	m.fileContext = pencodeFileContext{open: true, project: project, path: m.activePath}
	if m.handleShortcutKeys(m.fontResources, keymapState(pencodeBindingChord("F2"), runtime.GOOS), runtime.GOOS) || m.fileContext.dialog != "" {
		t.Fatal("old rename shortcut still triggers")
	}
	if !m.handleShortcutKeys(m.fontResources, keymapState(pencodeBindingChord("Alt+R"), runtime.GOOS), runtime.GOOS) || m.fileContext.dialog != "rename" {
		t.Fatal("remapped file rename shortcut did not open dialog")
	}
}
