package workbench

import (
	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	"github.com/pencode-labs/pencode/peb"
)

type pencodeActionShortcut struct {
	index   int
	binding pencodeKeybinding
}

// Translate once when the action snapshot changes, never on each key event.
func pencodeShortcutBinding(s peb.Shortcut) pencodeKeybinding {
	return pencodeKeybinding{Key: pencodeShortcutKeys[s.Key], Primary: s.Primary, Control: s.Control, Super: s.Super, Shift: s.Shift, Alt: s.Alt}
}

var pencodeShortcutKeys = map[string]input.KeyCode{
	"A": input.KeyA, "B": input.KeyB, "C": input.KeyC, "D": input.KeyD,
	"E": input.KeyE, "F": input.KeyF, "G": input.KeyG, "H": input.KeyH,
	"I": input.KeyI, "J": input.KeyJ, "K": input.KeyK, "L": input.KeyL,
	"M": input.KeyM, "N": input.KeyN, "O": input.KeyO, "P": input.KeyP,
	"Q": input.KeyQ, "R": input.KeyR, "S": input.KeyS, "T": input.KeyT,
	"U": input.KeyU, "V": input.KeyV, "W": input.KeyW, "X": input.KeyX,
	"Y": input.KeyY, "Z": input.KeyZ,
	"0": input.KeyNum0, "1": input.KeyNum1, "2": input.KeyNum2, "3": input.KeyNum3,
	"4": input.KeyNum4, "5": input.KeyNum5, "6": input.KeyNum6, "7": input.KeyNum7,
	"8": input.KeyNum8, "9": input.KeyNum9,
	"F1": input.KeyF1, "F2": input.KeyF2, "F3": input.KeyF3, "F4": input.KeyF4,
	"F5": input.KeyF5, "F6": input.KeyF6, "F7": input.KeyF7, "F8": input.KeyF8,
	"F9": input.KeyF9, "F10": input.KeyF10, "F11": input.KeyF11, "F12": input.KeyF12,
	"=": input.KeyEqual, "-": input.KeyMinus, ",": input.KeyComma, ".": input.KeyPeriod,
	"/": input.KeySlash, ";": input.KeySemicolon, "'": input.KeyApostrophe,
	"[": input.KeyLeftBracket, "]": input.KeyRightBracket, "\\": input.KeyBackslash, "`": input.KeyGraveAccent,
	"Escape": input.KeyEscape, "Tab": input.KeyTab, "Enter": input.KeyEnter, "Space": input.KeySpace,
	"Backspace": input.KeyBackspace, "Delete": input.KeyDelete, "Insert": input.KeyInsert,
	"Home": input.KeyHome, "End": input.KeyEnd, "PageUp": input.KeyPageUp, "PageDown": input.KeyPageDown,
	"Left": input.KeyLeft, "Right": input.KeyRight, "Up": input.KeyUp, "Down": input.KeyDown,
}

func (model *workspaceModel) handleActionShortcuts(world *module.Content, state *input.State, goos string) bool {
	if state == nil || state.Composing() || model.settingsOpen || model.quickOpen || model.capturingShortcut || model.closing.action != pencodeCloseNone || model.fileContext.dialog != "" {
		return false
	}
	if overlay := model.actions.ui.overlay; overlay != nil && overlay.ui.Kind == peb.Dialog {
		return false
	}
	model.syncActions()
	for _, shortcut := range model.actions.shortcuts {
		a := &model.actions.snapshot[shortcut.index]
		if (a.Location == peb.LeftContextMenu) != model.fileContext.open ||
			(a.Location == peb.RightContextMenu) != model.languageUI.contextOpen {
			continue
		}
		binding := shortcut.binding
		if override, ok := model.settings.Bindings[a.ID]; ok {
			binding = override
		}
		if binding.matches(state, goos) {
			model.runAction(world, a.ID)
			return true
		}
	}
	return false
}
