package workbench

import (
	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/module"
	engineui "github.com/onix-engine/onix/ui"
)

const pencodeNavigationLimit = 128

type pencodeNavigationLocation struct {
	project *pencodeProject
	path    string
	view    pencodeEditorView
}

type pencodeNavigationHistory struct {
	entries []pencodeNavigationLocation
	index   int
}

func (m *workspaceModel) navigationLocation() pencodeNavigationLocation {
	if !m.textEditorActive() {
		return pencodeNavigationLocation{}
	}
	f, p := m.activeFile(), m.activeProjectState()
	// A caller may have activated the destination project already. The shared
	// editor still holds the source document until the next reconciliation.
	if m.editor.file != nil && m.fontResources != nil {
		f = m.editor.file
		p = nil
		for _, candidate := range m.projects {
			if candidate.Files[f.Path] == f {
				p = candidate
				break
			}
		}
		field, ok := m.fontResources.Get[engineui.TextField](m.editor.control)
		area, scrollOK := m.fontResources.Get[engineui.Textarea](m.editor.control)
		if ok && scrollOK {
			f.editorView = pencodeEditorView{field.Caret, field.Anchor, area.ScrollX, area.ScrollY}
		}
	}
	if f == nil {
		return pencodeNavigationLocation{}
	}
	return pencodeNavigationLocation{p, f.Path, f.editorView}
}

func (h *pencodeNavigationHistory) record(from, to pencodeNavigationLocation) {
	if from.path == "" || to.path == "" {
		return
	}
	same := func(a, b pencodeNavigationLocation) bool {
		return a.project == b.project && a.path == b.path && a.view.caret == b.view.caret && a.view.anchor == b.view.anchor
	}
	if same(from, to) {
		return
	}
	if h.entries == nil {
		h.entries = make([]pencodeNavigationLocation, 0, pencodeNavigationLimit)
	}
	if len(h.entries) > 0 {
		clear(h.entries[h.index+1:])
		h.entries = h.entries[:h.index+1]
	}
	for _, location := range [...]pencodeNavigationLocation{from, to} {
		if last := len(h.entries) - 1; last >= 0 && same(h.entries[last], location) {
			h.entries[last] = location
			continue
		}
		if len(h.entries) == pencodeNavigationLimit {
			copy(h.entries, h.entries[1:])
			h.entries = h.entries[:pencodeNavigationLimit-1]
		}
		h.entries = append(h.entries, location)
	}
	h.index = len(h.entries) - 1
}

func (m *workspaceModel) navigateEditorHistory(step int) {
	h := &m.navigation
	for index := h.index + step; index >= 0 && index < len(h.entries); index += step {
		target := h.entries[index]
		if target.project != nil && !m.projectLive(target.project) {
			continue
		}
		n := fileNavigation{origin: m.navigationLocation(), view: target.view, navigationIndex: index, restore: true}
		files := m.files
		if target.project != nil {
			files = target.project.Files
		}
		m.activateProject(target.project)
		var err error
		if f := files[target.path]; f != nil {
			// Keep navigation on the stack when no asynchronous open is needed.
			err = m.requestFileOpen(target.project, target.path, false, true, nil)
			if err == nil {
				m.finishFileNavigation(target.project, f, &n)
			}
		} else {
			pending := n
			err = m.requestFileOpen(target.project, target.path, false, true, &pending)
		}
		if err != nil {
			m.setActiveStatus(err.Error())
		}
		return
	}
}

func (m *workspaceModel) editorNavigationShortcut(world *module.Content, state *input.State, goos string) bool {
	if world == nil || !m.textEditorActive() || m.settingsOpen || m.quickOpen || state.Composing() {
		return false
	}
	field, ok := world.Get[engineui.TextField](m.editor.control)
	if !ok || !field.Focused {
		return false
	}
	step := 0
	if m.commandMatches("editor-go-back", state, goos) {
		step = -1
	} else if m.commandMatches("editor-go-forward", state, goos) {
		step = 1
	}
	if step == 0 {
		return false
	}
	m.dismissLanguageContext()
	m.cancelLanguageHover()
	m.cancelLanguageAutomatic()
	m.dismissLanguagePanel()
	m.navigateEditorHistory(step)
	m.languageUI.keysHandled = true
	return true
}
