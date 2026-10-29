package workbench

import (
	"strings"

	"github.com/diiyw/onix/module"
	engineui "github.com/diiyw/onix/ui"
)

const pencodeIndent = "    "

func (model *workspaceModel) indentEditorSelection(world *module.Content, unindent bool) bool {
	if model.activeFile() == nil || world == nil {
		return false
	}
	field, ok := world.Get[engineui.TextField](model.editor.control)
	if !ok || !field.Focused {
		return false
	}
	content := model.activeContent()
	start, end := min(field.Anchor, field.Caret), max(field.Anchor, field.Caret)
	buffer := model.activeFile().textBuffer(content)
	startByte, endByte := buffer.ByteOffset(start), buffer.ByteOffset(end)
	if !unindent && start == end {
		value := content[:startByte] + pencodeIndent + content[startByte:]
		if model.updateActiveContent(value) {
			model.setEditorValueAndCaret(world, value, field.Caret+len(pencodeIndent))
		}
		return true
	}

	lastByte := endByte
	if end > start && lastByte > 0 && content[lastByte-1] == '\n' {
		lastByte--
	}
	firstLine := strings.LastIndexByte(content[:startByte], '\n') + 1
	lastLine := strings.LastIndexByte(content[:lastByte], '\n') + 1
	anchorByte, caretByte := startByte, endByte
	if field.Anchor > field.Caret {
		anchorByte, caretByte = endByte, startByte
	}
	anchor, caret := field.Anchor, field.Caret
	var result strings.Builder
	result.Grow(len(content) + len(pencodeIndent))
	cursor := 0
	changed := false
	for line := firstLine; line <= lastLine; {
		result.WriteString(content[cursor:line])
		removed, inserted := 0, 0
		if unindent {
			if line < len(content) && content[line] == '\t' {
				removed = 1
			} else {
				for removed < len(pencodeIndent) && line+removed < len(content) && content[line+removed] == ' ' {
					removed++
				}
			}
		} else {
			result.WriteString(pencodeIndent)
			inserted = len(pencodeIndent)
		}
		changed = changed || removed != 0 || inserted != 0
		anchor += pencodeIndentPositionChange(anchorByte, line, removed, inserted)
		caret += pencodeIndentPositionChange(caretByte, line, removed, inserted)
		cursor = line + removed
		next := strings.IndexByte(content[line:], '\n')
		if next < 0 {
			break
		}
		line += next + 1
	}
	if !changed {
		return true
	}
	result.WriteString(content[cursor:])
	value := result.String()
	if model.updateActiveContent(value) {
		setPencodeEditorValue(world, model.surface, value)
		if updated := world.Mut[engineui.TextField](model.editor.control); updated != nil {
			updated.Anchor, updated.Caret = anchor, caret
		}
	}
	return true
}

func pencodeIndentPositionChange(position, line, removed, inserted int) int {
	if position >= line+removed {
		return inserted - removed
	}
	if position > line {
		return inserted - (position - line)
	}
	return 0
}

func pencodeSelectionBytes(content string, start, end int) (int, int) {
	startByte, endByte := len(content), len(content)
	runeIndex := 0
	for byteIndex := range content {
		if runeIndex == start {
			startByte = byteIndex
		}
		if runeIndex == end {
			endByte = byteIndex
			break
		}
		runeIndex++
	}
	return startByte, endByte
}
