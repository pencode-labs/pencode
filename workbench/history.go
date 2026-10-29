package workbench

import (
	"strings"

	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	engineui "github.com/diiyw/onix/ui"
)

const (
	pencodeHistoryLimit     = 128
	pencodeHistoryByteLimit = 4 << 20
)

func (edit pencodeEdit) bytes() int { return len(edit.removed) + len(edit.inserted) }

// Both stacks share a byte budget. Undo/redo move ownership without copying
// strings or charging the same edit twice.
type pencodeEditHistory struct {
	buffer       input.TextBuffer
	undo, redo   []pencodeEdit
	historyBytes int
}

func (file *pencodeEditHistory) recordEdit(edit pencodeEdit) {
	for i := range file.redo {
		file.historyBytes -= file.redo[i].bytes()
		file.redo[i] = pencodeEdit{}
	}
	file.redo = file.redo[:0]
	size := edit.bytes()
	if size > pencodeHistoryByteLimit {
		// Keeping earlier edits would create an undo gap across this change.
		clear(file.undo)
		file.undo = file.undo[:0]
		file.historyBytes = 0
		return
	}
	removed := 0
	for removed < len(file.undo) && (len(file.undo)-removed >= pencodeHistoryLimit || file.historyBytes+size > pencodeHistoryByteLimit) {
		file.historyBytes -= file.undo[removed].bytes()
		removed++
	}
	if removed > 0 {
		remaining := copy(file.undo, file.undo[removed:])
		clear(file.undo[remaining:])
		file.undo = file.undo[:remaining]
	}
	// Clone only accepted deltas; substrings must not pin old file versions.
	edit.removed, edit.inserted = strings.Clone(edit.removed), strings.Clone(edit.inserted)
	file.undo = append(file.undo, edit)
	file.historyBytes += size
}

// textBuffer binds the native field to its owning file, including when a
// snapshot arrives from disk, an extension, or a language server.
func (history *pencodeEditHistory) textBuffer(value string) *input.TextBuffer {
	history.buffer.Sync(value)
	return &history.buffer
}

func (history *pencodeEditHistory) replaceContent(current, value string, start, oldEnd, newEnd int) {
	// Native input has already committed this version to the shared tree.
	if history.buffer.Matches(value) {
		return
	}
	history.buffer.Sync(current)
	history.buffer.ApplySnapshot(value, start, oldEnd, newEnd)
}

// restoreEdit transfers the same delta between stacks and edits only the
// affected tree range. Carets are stored as Unicode rune indices.
func (history *pencodeEditHistory) restoreEdit(current string, redo bool) (string, int, bool) {
	from, to := &history.undo, &history.redo
	if redo {
		from, to = to, from
	}
	if len(*from) == 0 {
		return current, 0, false
	}
	i := len(*from) - 1
	edit := (*from)[i]
	removed, inserted, caret := edit.inserted, edit.removed, edit.oldCaret
	if redo {
		removed, inserted, caret = edit.removed, edit.inserted, edit.newCaret
	}
	end := edit.start + len(removed)
	if edit.start < 0 || end > len(current) || current[edit.start:end] != removed {
		return current, 0, false
	}
	(*from)[i] = pencodeEdit{}
	*from = (*from)[:i]
	*to = append(*to, edit)
	buffer := history.textBuffer(current)
	buffer.ReplaceBytes(edit.start, end, inserted)
	return buffer.String(), caret, true
}

func (model *workspaceModel) undoActiveContent(world *module.Content) bool {
	return model.restoreActiveContent(world, false)
}
func (model *workspaceModel) redoActiveContent(world *module.Content) bool {
	return model.restoreActiveContent(world, true)
}
func (model *workspaceModel) restoreActiveContent(world *module.Content, redo bool) bool {
	file := model.activeFile()
	if file == nil {
		return false
	}
	value, caret, ok := file.restoreEdit(file.Content, redo)
	if !ok {
		return false
	}
	model.applyActiveContent(value, false)
	model.setEditorValueAndCaret(world, value, caret)
	return true
}

func (model *workspaceModel) setEditorValueAndCaret(world *module.Content, value string, caret int) {
	if world == nil {
		return
	}
	setPencodeEditorValue(world, model.surface, value)
	if field := world.Mut[engineui.TextField](model.editor.control); field != nil {
		field.Caret, field.Anchor = caret, caret
	}
}
