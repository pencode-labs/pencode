package workbench

import (
	"github.com/diiyw/onix/module"
	engineui "github.com/diiyw/onix/ui"
	"github.com/diiyw/onix/ui/html"
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
	if field, ok := module.ContentGet[engineui.TextField](world, m.editor.control); ok && field.Caret != field.Anchor {
		buffer := m.activeFile().textBuffer(m.activeContent())
		m.findScope = pencodeFindScope{m.activeFile(), buffer.ByteOffset(min(field.Anchor, field.Caret)), buffer.ByteOffset(max(field.Anchor, field.Caret))}
	}
}

func (m *workspaceModel) scopedFindMatches(content string, dst []textsearch.Match, limit int) []textsearch.Match {
	if !m.findSelection {
		return m.findPattern.FindAll(content, dst, limit)
	}
	if m.findScope.file != m.activeFile() {
		return dst[:0]
	}
	dst = m.findPattern.FindAll(content, dst, -1)
	n := 0
	for _, match := range dst {
		if match.Start >= m.findScope.start && match.End <= m.findScope.end {
			dst[n] = match
			n++
			if limit > 0 && n == limit {
				break
			}
		}
	}
	clear(dst[n:])
	return dst[:n]
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
	m.findScope.start = max(0, min(len(file.Content), rebase(m.findScope.start, false)))
	m.findScope.end = max(m.findScope.start, min(len(file.Content), rebase(m.findScope.end, true)))
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
