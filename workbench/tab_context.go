package workbench

import (
	"path/filepath"
	"slices"

	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
	htmlui "github.com/diiyw/onix/ui/html"
)

type pencodeTabContext struct {
	project *pencodeProject
	path    string
	x, y    float32
	index   int
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

func (m *workspaceModel) tabContextEnabled(index int) bool {
	c := &m.tabContext
	if c.project == nil || c.project.Files[c.path] == nil {
		return false
	}
	if index == 1 {
		return len(c.project.Tabs) > 1
	}
	if index == 2 {
		i := slices.Index(c.project.Tabs, c.path)
		return i >= 0 && i+1 < len(c.project.Tabs)
	}
	if index == 3 {
		for _, path := range c.project.Tabs {
			if file := c.project.Files[path]; file != nil && !file.Dirty {
				return true
			}
		}
		return false
	}
	return true
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
	if m.screen != pencodeWorkspace || m.settingsOpen || m.quickOpen || m.folderSearch.open || m.closing.action != pencodeCloseNone || m.fileContext.dialog != "" || m.market != nil && m.market.open || c.open && (m.activeProjectState() != c.project || c.project.Files[c.path] == nil) {
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
	if scroll, ok := module.ContentGet[scene.UIScroll](world, m.tabScroll.area); ok {
		offset = scroll.Position.X()
	}
	for _, access := range module.ContentQuery[htmlui.Component[tabProps, struct{}]](world) {
		props := access.Get().Props
		if props.Model != m || props.Project != m.activeProjectState() {
			continue
		}
		id := "active-tab"
		if !props.Active {
			id = "file-tab:" + props.Path
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
		*c = pencodeTabContext{project: props.Project, path: props.Path, x: cx, y: cy, open: true}
		m.fileContext.open, m.terminalView.context.open = false, false
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
			m.closeProjectTabs(item.action, c.project, c.path)
			setPencodeEditorValue(world, m.surface, m.activeContent())
		} else if state, ok := module.ContentResource[*input.State](world); ok {
			value := c.path
			if i == 5 {
				value = filepath.Join(c.project.Path, filepath.FromSlash(value))
			}
			if !state.SetClipboardText(value) {
				m.setProjectStatus(c.project, "无法写入剪贴板")
			}
		}
		return true
	}
	return false
}

func (m *workspaceModel) handleTabContextKeys(world *module.Content, state *input.State) bool {
	c := &m.tabContext
	switch {
	case state.KeyPressed(input.KeyEscape):
		c.open = false
	case state.KeyPressed(input.KeyDown), state.KeyPressed(input.KeyUp), state.KeyPressed(input.KeyTab):
		step := 1
		if state.KeyPressed(input.KeyUp) || state.KeyPressed(input.KeyTab) && (state.KeyHeld(input.KeyLeftShift) || state.KeyHeld(input.KeyRightShift)) {
			step = -1
		}
		for range len(pencodeTabContextActions) {
			c.index = (c.index + step + len(pencodeTabContextActions)) % len(pencodeTabContextActions)
			if m.tabContextEnabled(c.index) {
				break
			}
		}
	case state.KeyPressed(input.KeyEnter), state.KeyPressed(input.KeySpace):
		m.handleTabContextEvent(world, pencodeTabContextActions[c.index].ref)
	default:
		return false
	}
	return true
}
