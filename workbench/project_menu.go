package workbench

import (
	"fmt"
	"strconv"

	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/module"
	engineui "github.com/onix-engine/onix/ui"
	htmlui "github.com/onix-engine/onix/ui/html"
	"github.com/pencode-labs/pencode/connection"
)

type pencodeProjectMenu struct {
	style       string
	recentStyle string
	index       int
	recentIndex int
	open        bool
	focus       bool
}

const (
	projectMenuRecent     = "project-menu-recent"
	projectMenuRecentList = "project-menu-recent-list"
	projectMenuWidth      = 280
	projectMenuMargin     = 8
)

var projectMenuRefs = [...]string{"project-menu-local", "project-menu-remote", projectMenuRecent}

var projectMenuRecentRefs = func() (refs [pencodeRecentProjectLimit]string) {
	for i := range refs {
		refs[i] = "project-menu-recent:" + strconv.Itoa(i)
	}
	return refs
}()

type pencodeProjectHostProps struct {
	Model   *workspaceModel
	Theme   *pencodeTheme
	Profile connection.Profile
	Active  bool
	Busy    bool
}

func newProjectHostDefinition(content *module.Content) (*htmlui.Definition[pencodeProjectHostProps, struct{}], error) {
	return content.Define[pencodeProjectHostProps, struct{}](
		htmlui.NewTemplate(`<button data-ref="host" class="pencode-project-menu-item" type="button" role="menuitem"><img data-ref="icon" alt=""><span class="pencode-project-menu-copy"><span data-ref="name"></span><span data-ref="address" class="pencode-project-menu-address"></span></span></button>`), "",
		module.ContentBehavior[pencodeProjectHostProps, struct{}]{
			Render: func(c module.ContentRenderContext[pencodeProjectHostProps, struct{}], view *htmlui.View) {
				p := c.Props
				view.Attr("host", "class", projectMenuItemClass(p.Active))
				view.Attr("host", "id", "project-menu-host:"+p.Profile.Address())
				view.Attr("host", "title", p.Profile.Address())
				view.Attr("icon", "src", p.Model.themedIcon("icons/remote.svg", "icon.foreground"))
				view.Text("name", p.Profile.Label())
				view.Text("address", p.Profile.Address())
				setPanelVisible(view, "address", p.Profile.Label() != p.Profile.Address())
				setContextMenuEnabled(view, "host", !p.Busy)
				view.Event("host", htmlui.EventClick)
			},
			HandleEvent: func(c *module.ContentEventContext[pencodeProjectHostProps, struct{}]) {
				m := c.Props.Model
				m.openProjectHost(c.Props.Profile)
				markPencodeRootDirtyByModel(c.World, m)
			},
		})
}

type pencodeProjectMenuRecentProps struct {
	Model  *workspaceModel
	Theme  *pencodeTheme
	Ref    string
	Entry  pencodeRecentProject
	Active bool
	Busy   bool
}

func newProjectMenuRecentDefinition(content *module.Content) (*htmlui.Definition[pencodeProjectMenuRecentProps, struct{}], error) {
	return content.Define[pencodeProjectMenuRecentProps, struct{}](
		htmlui.NewTemplate(`<button data-ref="project" class="pencode-project-menu-item" type="button" role="menuitem"><img data-ref="icon" alt=""><span class="pencode-project-menu-copy"><span data-ref="name"></span><span data-ref="path" class="pencode-project-menu-address"></span></span></button>`), "",
		module.ContentBehavior[pencodeProjectMenuRecentProps, struct{}]{
			Render: func(c module.ContentRenderContext[pencodeProjectMenuRecentProps, struct{}], view *htmlui.View) {
				p := c.Props
				view.Attr("project", "class", projectMenuItemClass(p.Active))
				view.Attr("project", "id", p.Ref)
				view.Attr("project", "title", p.Entry.description)
				view.Attr("icon", "src", p.Model.themedIcon(p.Entry.icon(), "icon.foreground"))
				view.Text("name", p.Entry.Name)
				view.Text("path", p.Entry.description)
				setContextMenuEnabled(view, "project", !p.Busy)
				view.Event("project", htmlui.EventClick)
			},
			HandleEvent: func(c *module.ContentEventContext[pencodeProjectMenuRecentProps, struct{}]) {
				m := c.Props.Model
				m.projectMenu = pencodeProjectMenu{}
				m.openRecentProject(c.Props.Entry)
				markPencodeRootDirtyByModel(c.World, m)
			},
		})
}

func projectMenuItemClass(active bool) string {
	if active {
		return "pencode-project-menu-item active"
	}
	return "pencode-project-menu-item"
}

func (m *workspaceModel) toggleProjectMenu(world *module.Content) {
	p := &m.projectMenu
	if p.open {
		p.open, p.focus = false, false
		return
	}
	m.closeQuickOpen(world)
	m.settingsOpen = false
	if m.market != nil {
		m.market.open = false
	}
	if m.session != nil && m.session.connection != nil {
		m.connectionEvent("connection-close", "")
	}
	root, rootOK := world.HTMLNode(m.surface, "pencode-root")
	anchor, anchorOK := world.HTMLNode(m.surface, "add-project")
	area, areaOK := root.Bounds()
	box, boxOK := anchor.Bounds()
	if !rootOK || !anchorOK || !areaOK || !boxOK {
		return
	}
	const width, margin, gap = projectMenuWidth, projectMenuMargin, 4
	x := min(max(box.X-area.X, margin), max(margin, area.Width-width-margin))
	y := box.Y + box.Height + gap - area.Y
	*p = pencodeProjectMenu{open: true, focus: true, style: fmt.Sprintf("left:%gpx;top:%gpx;max-height:%gpx", x, y, max(0, area.Height-y-margin))}
}

func (m *workspaceModel) renderProjectMenu(view *htmlui.View, hosts *htmlui.Definition[pencodeProjectHostProps, struct{}], recent *htmlui.Definition[pencodeProjectMenuRecentProps, struct{}]) {
	p := &m.projectMenu
	recentOpen := p.open && p.recentStyle != ""
	view.Attr("add-project", "aria-expanded", boolAttribute(p.open))
	view.Attr(projectMenuRecent, "aria-expanded", boolAttribute(recentOpen))
	setPanelVisible(view, "project-menu", p.open)
	setPanelVisible(view, projectMenuRecentList, recentOpen)
	if !p.open {
		return
	}
	view.Attr("project-menu", "style", p.style)
	for i, ref := range projectMenuRefs {
		view.Attr(ref, "class", projectMenuItemClass(p.index == i))
		view.Event(ref, htmlui.EventClick)
	}
	view.Attr("project-menu-local-icon", "src", m.themedIcon("icons/folder-opened.svg", "icon.foreground"))
	view.Attr("project-menu-remote-icon", "src", m.themedIcon("icons/remote.svg", "icon.foreground"))
	view.Attr("project-menu-recent-icon", "src", m.themedIcon("icons/folder-opened.svg", "icon.foreground"))
	view.Attr("project-menu-recent-arrow", "src", m.themedIcon("icons/chevron-right.svg", "icon.foreground"))
	c := m.session.connection
	busy := c != nil && c.busy
	setContextMenuEnabled(view, "project-menu-local", !busy)
	setContextMenuEnabled(view, "project-menu-remote", c != nil)
	setPanelVisible(view, "project-menu-hosts", c != nil && len(c.profiles) != 0)
	if c != nil {
		for i, profile := range c.profiles {
			view.AppendComponentChild("project-menu-host-list", componentKey(profile.Name+"\x00"+profile.Address()), hosts,
				pencodeProjectHostProps{Model: m, Theme: m.workbenchTheme, Profile: profile, Active: p.index == i+len(projectMenuRefs), Busy: busy}, struct{}{})
		}
	}
	if !recentOpen {
		return
	}
	view.Attr(projectMenuRecentList, "style", p.recentStyle)
	entries := m.projectMenuRecentEntries()
	setPanelVisible(view, "project-menu-recent-empty", len(entries) == 0)
	for i, entry := range entries {
		view.AppendComponentChild("project-menu-recent-items", componentKey(entry.key), recent,
			pencodeProjectMenuRecentProps{Model: m, Theme: m.workbenchTheme, Ref: projectMenuRecentRefs[i], Entry: entry, Active: p.recentIndex == i, Busy: busy}, struct{}{})
	}
}

func (m *workspaceModel) projectMenuRecentEntries() []pencodeRecentProject {
	history := m.session.recentProjects
	if history == nil {
		return nil
	}
	return history.entries[:history.count]
}

func (m *workspaceModel) projectMenuRecentAnchor(world *module.Content) (menu, row htmlui.Rect, visible bool) {
	menuNode, _ := world.HTMLNode(m.surface, "project-menu")
	rowNode, _ := world.HTMLNode(m.surface, projectMenuRecent)
	menu, menuOK := menuNode.Bounds()
	row, rowOK := rowNode.Bounds()
	if scroll, ok := menuNode.ScrollContainer(); ok {
		offset, _ := world.ScrollOffset(scroll)
		row.Y -= offset.Y()
	}
	return menu, row, menuOK && rowOK && row.Y+row.Height > menu.Y && row.Y < menu.Y+menu.Height
}

func (m *workspaceModel) openProjectMenuRecent(world *module.Content, keyboard bool) bool {
	p := &m.projectMenu
	if p.recentStyle == "" {
		root, _ := world.HTMLNode(m.surface, "pencode-root")
		area, ok := root.Bounds()
		menu, row, visible := m.projectMenuRecentAnchor(world)
		if !ok || !visible {
			return false
		}
		const margin = projectMenuMargin
		x := menu.X + menu.Width - area.X
		left, right := menu.X-area.X-margin, area.Width-x-margin
		width := min(float32(projectMenuWidth), max(left, right))
		if width <= 0 {
			return false
		}
		if right < width {
			x = menu.X - area.X - width
		}
		height := float32(10 + 48*max(1, len(m.projectMenuRecentEntries())))
		y := max(float32(margin), min(row.Y-area.Y-5, area.Height-height-margin))
		p.recentStyle = fmt.Sprintf("left:%gpx;top:%gpx;width:%gpx;max-height:%gpx", x, y, width, max(0, area.Height-y-margin))
	}
	p.index, p.recentIndex = len(projectMenuRefs)-1, -1
	if c := m.session.connection; keyboard && len(m.projectMenuRecentEntries()) != 0 && (c == nil || !c.busy) {
		p.recentIndex, p.focus = 0, true
	}
	return true
}

func (m *workspaceModel) hoverProjectMenu(world *module.Content, state *input.State) bool {
	p := &m.projectMenu
	if !p.open {
		return false
	}
	pointer := state.Pointer()
	if pointer.DeltaX == 0 && pointer.DeltaY == 0 && pointer.WheelY == 0 {
		return false
	}
	zoom := max(float32(m.settings.Zoom)/100, .01)
	x, y := pointer.X/zoom, pointer.Y/zoom
	if p.recentStyle != "" && m.pointerInsideRefs(world, x, y, []string{projectMenuRecentList}) {
		return false
	}
	menu, row, visible := m.projectMenuRecentAnchor(world)
	// Include the menu padding beside the row so the pointer can cross it.
	if visible && languageRectContains(menu, x, y) && y >= row.Y && y < row.Y+row.Height && pointer.WheelY == 0 {
		return p.recentStyle == "" && m.openProjectMenuRecent(world, false)
	}
	if p.recentStyle != "" {
		p.focus = p.focus || p.recentIndex >= 0
		p.recentStyle, p.recentIndex = "", -1
		return true
	}
	return false
}

func (m *workspaceModel) projectMenuEvent(ref string) bool {
	switch ref {
	case "project-menu-local":
		m.projectMenu.open, m.projectMenu.focus = false, false
		m.chooseLocalProject()
	case "project-menu-remote":
		m.projectMenu.open, m.projectMenu.focus = false, false
		if m.session != nil && m.session.connection != nil {
			m.connectionEvent("connection-open", "")
			if len(m.session.connection.profiles) == 0 {
				m.connectionEvent("connection-add", "")
			}
		}
	default:
		return false
	}
	return true
}

func (m *workspaceModel) openProjectHost(profile connection.Profile) {
	c := m.session.connection
	if c == nil || c.busy {
		return
	}
	m.projectMenu.open, m.projectMenu.focus = false, false
	if m.connectionConnected(profile) {
		m.openRemoteProject(profile)
		return
	}
	c.open = true
	m.beginConnection(profile, false)
	c.pickProject = c.busy
}

func (m *workspaceModel) projectMenuRef() string {
	p := &m.projectMenu
	if p.recentStyle != "" && p.recentIndex >= 0 && p.recentIndex < len(m.projectMenuRecentEntries()) {
		return projectMenuRecentRefs[p.recentIndex]
	}
	if p.index < len(projectMenuRefs) {
		return projectMenuRefs[p.index]
	}
	return "project-menu-host:" + m.session.connection.profiles[p.index-len(projectMenuRefs)].Address()
}

func (m *workspaceModel) focusProjectMenu(world *module.Content) {
	if !m.projectMenu.focus {
		return
	}
	ref := "add-project"
	if m.projectMenu.open {
		ref = m.projectMenuRef()
		menu := "project-menu"
		if m.projectMenu.recentStyle != "" && m.projectMenu.recentIndex >= 0 {
			menu = projectMenuRecentList
		}
		m.revealProjectMenuItem(world, menu, ref)
	}
	if entity, ok := world.HTMLControl(m.surface, ref); ok && world.Focus(entity) {
		if m.projectMenu.open && !world.Has[engineui.TextFieldKeyHandler](entity) {
			_ = world.Insert(entity, engineui.TextFieldKeyHandler{Handle: m.menuActivationHandled})
		}
		m.projectMenu.focus = false
	}
}

func (m *workspaceModel) revealProjectMenuItem(world *module.Content, menu, ref string) {
	node, _ := world.HTMLNode(m.surface, menu)
	scroll, ok := node.ScrollContainer()
	if !ok {
		return
	}
	item, _ := world.HTMLNode(m.surface, ref)
	row, rowOK := item.Bounds()
	bounds, boundsOK := node.Bounds()
	offset, _ := world.ScrollOffset(scroll)
	y := row.Y - bounds.Y
	if rowOK && boundsOK {
		position := min(y, max(offset.Y(), y+row.Height-bounds.Height+1))
		if position != offset.Y() {
			offset[1] = position
			world.SetScrollOffset(scroll, offset)
		}
	}
}

func (m *workspaceModel) menuActivationHandled(state *input.State) bool {
	// The workbench has already routed this key to an open menu.
	return m.languageUI.keysHandled && (state.KeyPressed(input.KeyEnter) || state.KeyReleased(input.KeySpace))
}

func (m *workspaceModel) projectMenuKeys(world *module.Content, state *input.State) bool {
	p := &m.projectMenu
	if p.recentStyle != "" {
		return m.projectMenuRecentKeys(state)
	}
	switch {
	case m.uiKey(state, input.KeyEscape):
		p.open, p.focus = false, true
	case p.index == len(projectMenuRefs)-1 && m.uiKey(state, input.KeyRight):
		m.openProjectMenuRecent(world, true)
	case m.uiKey(state, input.KeyDown), m.uiKey(state, input.KeyUp):
		count := len(projectMenuRefs)
		if c := m.session.connection; c != nil {
			count += len(c.profiles)
		}
		delta := 1
		if m.uiKey(state, input.KeyUp) {
			delta = -1
		}
		p.index, p.focus = (p.index+delta+count)%count, true
	case m.uiKey(state, input.KeyEnter), m.uiKey(state, input.KeySpace):
		switch {
		case p.index == len(projectMenuRefs)-1:
			m.openProjectMenuRecent(world, true)
		case p.index < len(projectMenuRefs):
			m.projectMenuEvent(m.projectMenuRef())
		default:
			m.openProjectHost(m.session.connection.profiles[p.index-len(projectMenuRefs)])
		}
	default:
		return false
	}
	m.languageUI.keysHandled = true
	return true
}

func (m *workspaceModel) projectMenuRecentKeys(state *input.State) bool {
	p := &m.projectMenu
	entries := m.projectMenuRecentEntries()
	c := m.session.connection
	enabled := len(entries) != 0 && (c == nil || !c.busy)
	switch {
	case m.uiKey(state, input.KeyEscape), m.uiKey(state, input.KeyLeft):
		p.recentStyle, p.recentIndex, p.focus = "", -1, true
	case m.uiKey(state, input.KeyDown):
		if enabled {
			p.recentIndex, p.focus = (p.recentIndex+1)%len(entries), true
		}
	case m.uiKey(state, input.KeyUp):
		if enabled {
			index := p.recentIndex - 1
			if index < 0 {
				index = len(entries) - 1
			}
			p.recentIndex, p.focus = index, true
		}
	case m.uiKey(state, input.KeyEnter), m.uiKey(state, input.KeySpace):
		if enabled {
			if p.recentIndex < 0 {
				p.recentIndex, p.focus = 0, true
			} else if p.recentIndex < len(entries) {
				entry := entries[p.recentIndex]
				*p = pencodeProjectMenu{}
				m.openRecentProject(entry)
			}
		}
	default:
		return false
	}
	m.languageUI.keysHandled = true
	return true
}
