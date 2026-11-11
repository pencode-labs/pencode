package workbench

import (
	"path/filepath"
	"runtime"

	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/module"
	"github.com/onix-engine/onix/scene"
	htmlui "github.com/onix-engine/onix/ui/html"
)

type pencodeTabContext struct {
	project *pencodeProject
	surface *pencodeExtensionSurface
	source  *sourceTab
	path    string
	index   int
	x, y    float32
	open    bool
}

var pencodeTabContextActions = [...]struct {
	ref    string
	action pencodeCloseAction
}{
	{"tab-context-close", pencodeCloseTab},
	{"tab-context-others", pencodeCloseOtherTabs},
	{"tab-context-right", pencodeCloseRightTabs},
	{"tab-context-saved", pencodeCloseSavedTabs},
	{"tab-context-all", pencodeCloseAllTabs},
	{"tab-context-copy-path", pencodeCloseNone},
	{"tab-context-copy-relative", pencodeCloseNone},
}

func (c *pencodeTabContext) target() pencodeEditorTab {
	return pencodeEditorTab{project: c.project, path: c.path, surface: c.surface, source: c.source}
}

func (m *workspaceModel) tabContextValid() bool {
	for tab := range m.editorTabs() {
		if tab == m.tabContext.target() {
			return true
		}
	}
	return false
}

func (m *workspaceModel) tabContextEnabled(index int) bool {
	anchor := m.tabContext.target()
	if index >= 5 {
		return anchor.surface == nil && (anchor.source != nil || anchor.project != nil && anchor.path != "")
	}
	right := false
	enabled := false
	for tab := range m.editorTabs() {
		selected := editorTabCloseIncludes(pencodeTabContextActions[index].action, tab, anchor, right)
		if selected {
			if m.sourceState().busy() && tab.source != nil {
				return false
			}
			enabled = true
		}
		right = right || tab == anchor
	}
	return enabled
}

func (m *workspaceModel) renderTabContext(view *htmlui.View) {
	c := &m.tabContext
	setPanelVisible(view, "tab-context", c.open)
	if !c.open {
		return
	}
	view.Attr("tab-context", "style", languagePopupStyle(c.x, c.y))
	for i, item := range pencodeTabContextActions {
		view.Event(item.ref, htmlui.EventClick)
		class := ""
		if i == c.index {
			class = "active"
		}
		view.Attr(item.ref, "class", class)
		setContextMenuEnabled(view, item.ref, m.tabContextEnabled(i))
	}
}

func (m *workspaceModel) updateTabContext(world *module.Content, state *input.State) bool {
	c := &m.tabContext
	if m.screen != pencodeWorkspace || m.settingsOpen || m.quickOpen || m.folderSearch.open || m.closing.action != pencodeCloseNone || m.fileContext.dialog != "" || m.market != nil && m.market.open || m.sourceState().modal() || c.open && !m.tabContextValid() {
		changed := c.open
		*c = pencodeTabContext{}
		return changed
	}
	p := state.Pointer()
	zoom := max(float32(m.settings.Zoom)/100, .01)
	x, y := p.X/zoom, p.Y/zoom
	if c.open && m.pointerInsideRefs(world, x, y, []string{"tab-context"}) {
		if p.DeltaX != 0 || p.DeltaY != 0 {
			for i, item := range pencodeTabContextActions {
				if i != c.index && m.tabContextEnabled(i) && m.pointerInsideRefs(world, x, y, []string{item.ref}) {
					c.index = i
					return true
				}
			}
		}
		return false
	}
	changed := false
	if c.open && (state.PointerPressed() || state.MousePressed(input.MouseRight) || state.MousePressed(input.MouseMiddle)) {
		c.open, changed = false, true
	}
	if !state.MousePressed(input.MouseRight) || !m.pointerInsideRefs(world, x, y, []string{"tabs"}) {
		return changed
	}
	var offset float32
	if scroll, ok := world.Get[scene.UIScroll](m.tabScroll.area); ok {
		offset = scroll.Position.X()
	}
	for tab := range m.editorTabs() {
		id := "active-tab"
		switch {
		case tab.source != nil:
			id = "sc-tab-group:" + tab.source.Key
		case tab.surface != nil:
			if m.actions.ui.editor != tab.surface {
				id = "file-tab:" + tab.surface.rootID
			}
		default:
			if m.actions.ui.editor != nil || m.sourceState().visible() || tab.path != tab.project.ActivePath {
				id = "file-tab:" + tab.path
			}
		}
		node, ok := world.HTMLNode(m.surface, id)
		if !ok {
			continue
		}
		r, ok := node.Bounds()
		r.X += offset
		if !ok || !languageRectContains(r, x, y) {
			continue
		}
		root, ok := world.HTMLNode(m.surface, "pencode-root")
		if !ok {
			return changed
		}
		bounds, _ := root.Bounds()
		cx, cy := clampLanguagePopup(x-bounds.X, y-bounds.Y, bounds, 240, 212)
		*c = pencodeTabContext{project: tab.project, path: tab.path, surface: tab.surface, source: tab.source, x: cx, y: cy, open: true}
		m.fileContext.open, m.terminalView.context.open = false, false
		s := m.sourceState()
		s.MenuOpen, s.BranchMenuOpen, s.RepositoryMenuOpen = false, false, false
		m.dismissLanguageContext()
		m.lastFileClick = 0
		return true
	}
	return changed
}

func (m *workspaceModel) handleTabContextEvent(world *module.Content, ref string) bool {
	for i, item := range pencodeTabContextActions {
		if item.ref != ref {
			continue
		}
		c := &m.tabContext
		if !c.open || !m.tabContextEnabled(i) {
			return true
		}
		c.open = false
		if item.action != pencodeCloseNone {
			m.closeEditorTabs(item.action, c.target())
			setPencodeEditorValue(world, m.surface, m.activeContent())
		} else if state, ok := world.Resource[*input.State](); ok {
			value := m.editorTabPath(c.target())
			if i == 6 {
				if relative, err := filepath.Rel(m.projectPath, value); err == nil {
					value = filepath.ToSlash(relative)
				}
			}
			if !state.SetClipboardText(value) {
				m.setProjectStatus(c.project, m.tr("无法写入剪贴板"))
			}
		}
		return true
	}
	return false
}

func (m *workspaceModel) handleTabContextKeys(world *module.Content, state *input.State) bool {
	c := &m.tabContext
	switch {
	case m.uiKey(state, input.KeyEscape):
		c.open = false
	case m.uiKey(state, input.KeyDown), m.uiKey(state, input.KeyUp), m.uiKey(state, input.KeyTab):
		step := 1
		if m.uiKey(state, input.KeyUp) || m.commandMatches("ui-focus-previous", state, runtime.GOOS) {
			step = -1
		}
		for range len(pencodeTabContextActions) {
			c.index = (c.index + step + len(pencodeTabContextActions)) % len(pencodeTabContextActions)
			if m.tabContextEnabled(c.index) {
				break
			}
		}
	case m.uiKey(state, input.KeyEnter), m.uiKey(state, input.KeySpace):
		m.handleTabContextEvent(world, pencodeTabContextActions[c.index].ref)
	default:
		return false
	}
	return true
}
