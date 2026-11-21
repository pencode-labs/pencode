package workbench

import (
	"path"

	"github.com/onix-engine/onix/module"
	htmlui "github.com/onix-engine/onix/ui/html"
)

var diffInputs = [...]string{"sc-code-left-input", "sc-code-right-input"}
var diffViewports = [...]string{"sc-code-left", "sc-code-right"}

func (m *workspaceModel) languageDiffTab() *sourceTab {
	s := m.sourceState()
	if s.visible() {
		if tab := s.activeTab(); tab != nil && !tab.Binary && tab.SubmoduleSummary == "" && tab.Kind != "merge" {
			return tab
		}
	}
	return nil
}

func (tab *sourceTab) languageFile(side int) *openFile {
	f := tab.languageFiles[side]
	if f == nil {
		name := tab.Path
		if side == 0 && tab.PreviousPath != "" {
			name = tab.PreviousPath
		}
		f = &openFile{Path: name, Name: path.Base(name), forcedText: true}
		tab.languageFiles[side] = f
	}
	f.contentSnapshot = snapshotText(tab.sourceText(side))
	return f
}

func (m *workspaceModel) languageFile() *openFile {
	if tab := m.languageDiffTab(); tab != nil {
		return tab.languageFile(m.sourceState().languageSide)
	}
	return m.activeFile()
}

func (m *workspaceModel) languageEditorActive() bool {
	return m.textEditorActive() || m.languageDiffTab() != nil
}

func (m *workspaceModel) languageEditor() *pencodeEditor {
	if m.languageDiffTab() != nil {
		s := m.sourceState()
		return &s.diffEditors[s.languageSide]
	}
	return &m.editor
}

// The scroll container clips the pointer. The larger, scrolling textarea
// supplies the origin for Onix's aligned line layout.
func (m *workspaceModel) languagePointerEditor(world *module.Content, x, y float32) (*pencodeEditor, *openFile, htmlui.Rect, int) {
	if tab := m.languageDiffTab(); tab != nil {
		s := m.sourceState()
		if s.diffPaneTab != tab {
			return nil, nil, htmlui.Rect{}, 0
		}
		for side, ref := range diffViewports {
			node, ok := world.HTMLNode(m.surface, ref)
			if !ok {
				continue
			}
			bounds, _ := node.Bounds()
			if !languageRectContains(bounds, x, y) {
				continue
			}
			input, ok := world.HTMLNode(m.surface, diffInputs[side])
			if !ok {
				continue
			}
			rect, _ := input.Bounds()
			// HTML bounds are in layout coordinates, before ancestor scrolling.
			rect.X += tab.scrollApplied[side][0]
			rect.Y += tab.scrollApplied[side][1]
			if languageRectContains(rect, x, y) {
				return &s.diffEditors[side], tab.languageFile(side), rect, side
			}
		}
		return nil, nil, htmlui.Rect{}, 0
	}
	node, ok := world.HTMLNode(m.surface, "editor")
	if ok {
		rect, _ := node.Bounds()
		if languageRectContains(rect, x, y) {
			return &m.editor, m.activeFile(), rect, 0
		}
	}
	return nil, nil, htmlui.Rect{}, 0
}

func (doc *pencodeLanguageDocument) writable() bool {
	return doc != nil && (doc.tab == nil || doc.pane == 2 && doc.tab.editable())
}

func (m *workspaceModel) languageEditable() bool {
	if tab := m.languageDiffTab(); tab != nil {
		s := m.sourceState()
		return s.languageSide == 1 && tab.editable() && !s.busy()
	}
	f := m.activeFile()
	return f != nil && !f.pageLoading
}

// Each pane uses the real URI in its own server session so servers that require
// file URIs resolve relative imports without overwriting the workspace buffer.
func (m *workspaceModel) comparisonDocument(session *pencodeLanguageSession) *pencodeLanguageDocument {
	if session == nil || session.key.pane == 0 || session.key.root != m.projectPath {
		return nil
	}
	if tab := m.languageDiffTab(); tab != nil {
		doc := m.languages.documents[tab.languageFile(session.key.pane-1)]
		if doc != nil && doc.seen {
			return doc
		}
	}
	return nil
}
