package workbench

import (
	"runtime"

	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	engineui "github.com/diiyw/onix/ui"
)

var pencodeEditorClipboardCommands = [...]struct{ id, ref string }{
	{"editor-cut", "editor-context-cut"}, {"editor-copy", "editor-context-copy"},
	{"editor-paste", "editor-context-paste"}, {"editor-select-all", "editor-context-select-all"},
}

func (m *workspaceModel) editorClipboardShortcut(world *module.Content, state *input.State, goos string) bool {
	if world == nil || !m.textEditorActive() || m.settingsOpen || m.quickOpen || m.findOpen {
		return false
	}
	field, ok := world.Get[engineui.TextField](m.editor.control)
	if !ok || !field.Focused {
		return false
	}
	for _, command := range pencodeEditorClipboardCommands {
		if !m.commandMatches(command.id, state, goos) {
			continue
		}
		ui := &m.languageUI
		if !ui.contextOpen {
			ui.contextOpen, ui.contextFile, ui.contextContent = true, m.activeFile(), m.activeContent()
			ui.contextCaret, ui.contextAnchor = field.Caret, field.Anchor
		}
		m.runEditorContextEdit(world, command.ref)
		ui.keysHandled = true
		return true
	}
	return false
}

// The editor's native input layer must not execute the old hardcoded clipboard
// and indentation chords after the user replaces or clears their mappings.
func (m *workspaceModel) editorKeyHandled(state *input.State) bool {
	if m.languageUI.keysHandled {
		return true
	}
	if state.Composing() {
		return false
	}
	if state.KeyHeld(input.KeyLeftControl) || state.KeyHeld(input.KeyRightControl) || state.KeyHeld(input.KeyLeftSuper) || state.KeyHeld(input.KeyRightSuper) {
		for _, key := range [...]input.KeyCode{input.KeyA, input.KeyC, input.KeyV, input.KeyX} {
			if state.KeyPressed(key) {
				return true
			}
		}
	}
	if state.KeyPressed(input.KeyTab) && !state.KeyHeld(input.KeyLeftControl) && !state.KeyHeld(input.KeyRightControl) && !state.KeyHeld(input.KeyLeftSuper) && !state.KeyHeld(input.KeyRightSuper) && !state.KeyHeld(input.KeyLeftAlt) && !state.KeyHeld(input.KeyRightAlt) {
		return !m.commandMatches("editor-indent", state, runtime.GOOS) && !m.commandMatches("editor-outdent", state, runtime.GOOS)
	}
	return false
}
