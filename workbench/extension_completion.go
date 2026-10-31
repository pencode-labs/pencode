package workbench

import (
	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/input"
	engineui "github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/peb"
)

// Completion navigation stays in the text field; the extension owns its list.
func (s *pencodeExtensionSurface) interceptCompletion(state *input.State) bool {
	if state.KeyHeld(input.KeyLeftShift) || state.KeyHeld(input.KeyRightShift) || state.KeyHeld(input.KeyLeftControl) || state.KeyHeld(input.KeyRightControl) || state.KeyHeld(input.KeyLeftAlt) || state.KeyHeld(input.KeyRightAlt) || state.KeyHeld(input.KeyLeftSuper) || state.KeyHeld(input.KeyRightSuper) {
		return false
	}
	data := ""
	switch {
	case state.KeyPressed(input.KeyDown):
		data = "next"
	case state.KeyPressed(input.KeyUp):
		data = "previous"
	case state.KeyPressed(input.KeyEnter) && !state.KeyPressedDuringComposition(input.KeyEnter), state.KeyPressed(input.KeyTab):
		data = "accept"
	case state.KeyPressed(input.KeyEscape):
		data = "dismiss"
	default:
		return false
	}
	w := s.model.fontResources
	for _, f := range s.focus {
		if !f.completion {
			continue
		}
		control, ok := w.HTMLControl(s.model.surface, f.id)
		if !ok || w.FocusedControl() != control {
			continue
		}
		field := w.Mut[engineui.TextField](control)
		native, ok := w.Get[htmlui.Control](control)
		if field == nil || !ok || native.ReadOnly || native.Disabled {
			return false
		}
		event := s.commitInput(state, control, field, f.ref)
		event.Kind, event.Data = "completion", data
		s.send(event, "")
		return true
	}
	return false
}

// Include text queued in the same frame as Enter or completion navigation.
func (s *pencodeExtensionSurface) commitInput(state *input.State, control ecs.Entity, field *engineui.TextField, ref string) peb.EditorEvent {
	s.submitEdits = state.AppendTextEdits(s.submitEdits[:0])
	edits := s.submitEdits[:0]
	for _, edit := range s.submitEdits {
		if edit.Command != input.TextCommandEnter {
			edits = append(edits, edit)
		}
	}
	anchor, caret := field.Anchor, field.Caret
	if selection, ok := state.TextSelection(); ok {
		anchor, caret = selection.Start, selection.End
	}
	value, caret, _ := state.ApplyTextEditsInRange(field.Value, min(anchor, caret), max(anchor, caret), field.MaxRunes, true, edits)
	if value != field.Value {
		s.model.fontResources.SetControlValue(control, value)
		field.Caret, field.Anchor = caret, caret
	}
	clear(s.submitEdits)
	s.submitEdits = s.submitEdits[:0]
	return peb.EditorEvent{Kind: "input", Ref: ref, Value: value, Anchor: uint32(max(0, field.Anchor)), Focus: uint32(max(0, field.Caret))}
}
