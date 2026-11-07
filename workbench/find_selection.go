package workbench

import (
	"github.com/onix-engine/onix/module"
	"github.com/onix-engine/onix/text/piecetree"
	engineui "github.com/onix-engine/onix/ui"
	"github.com/onix-engine/onix/ui/html"
	"github.com/pencode-labs/pencode/textsearch"
)

type pencodeFindScope struct {
	file       *openFile
	start, end int
}

func (m *workspaceModel) captureFindScope(world *module.Content) {
	if world == nil || m.activeFile() == nil {
		return
	}
	if field, ok := world.Get[engineui.TextField](m.editor.control); ok && field.Caret != field.Anchor {
		buffer := m.activeFile().snapshotBuffer()
		m.findScope = pencodeFindScope{m.activeFile(), buffer.ByteOffset(min(field.Anchor, field.Caret)), buffer.ByteOffset(max(field.Anchor, field.Caret))}
	}
}

func (m *workspaceModel) scopedFindMatches[V piecetree.Value](value V, dst []textsearch.Match, limit int) []textsearch.Match {
	content := piecetree.Text(value)
	if !m.findSelection {
		return m.findPattern.FindSnapshot(content, dst, 0, content.Len(), limit)
	}
	if m.findScope.file != m.activeFile() {
		return dst[:0]
	}
	return m.findPattern.FindSnapshot(content, dst, m.findScope.start, m.findScope.end, limit)
}

func (m *workspaceModel) rebaseFindScope(file *openFile, start, oldEnd, newEnd int) {
	if m.findScope.file != file {
		return
	}
	rebase := func(offset int, end bool) int {
		if offset < start || offset == start && !end {
			return offset
		}
		if offset >= oldEnd {
			return offset + newEnd - oldEnd
		}
		return newEnd
	}
	m.findScope.start = max(0, min(file.snapshot().Len(), rebase(m.findScope.start, false)))
	m.findScope.end = max(m.findScope.start, min(file.snapshot().Len(), rebase(m.findScope.end, true)))
}

func renderReplaceToggle(view *html.View, ref string, expanded bool) {
	class, label := "pencode-find-toggle", "展开替换"
	if expanded {
		class, label = "pencode-find-toggle expanded", "收起替换"
	}
	view.Attr(ref, "class", class)
	view.Attr(ref, "aria-expanded", boolAttribute(expanded))
	view.Attr(ref, "title", label)
	view.Attr(ref, "aria-label", label)
}

func renderFindToggle(view *html.View, ref string, active bool) {
	class := "pencode-find-action pencode-find-option"
	if active {
		class = "pencode-find-action pencode-find-option active"
	}
	view.Attr(ref, "class", class)
	view.Attr(ref, "aria-pressed", boolAttribute(active))
}
