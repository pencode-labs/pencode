package workbench

import (
	"fmt"

	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/module"
	htmlui "github.com/onix-engine/onix/ui/html"
	"github.com/pencode-labs/pencode/connection"
)

type pencodeProjectMenu struct {
	style string
	index int
	open  bool
	focus bool
}

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
				class := "pencode-project-menu-item"
				if p.Active {
					class += " active"
				}
				view.Attr("host", "class", class)
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
	const width, margin, gap = float32(280), float32(8), float32(4)
	x := min(max(box.X-area.X, margin), max(margin, area.Width-width-margin))
	y := box.Y + box.Height + gap - area.Y
	*p = pencodeProjectMenu{open: true, focus: true, style: fmt.Sprintf("left:%gpx;top:%gpx;max-height:%gpx", x, y, max(0, area.Height-y-margin))}
}

func (m *workspaceModel) renderProjectMenu(view *htmlui.View, definition *htmlui.Definition[pencodeProjectHostProps, struct{}]) {
	p := &m.projectMenu
	view.Attr("add-project", "aria-expanded", boolAttribute(p.open))
	setPanelVisible(view, "project-menu", p.open)
	if !p.open {
		return
	}
	view.Attr("project-menu", "style", p.style)
	for i, ref := range [...]string{"project-menu-local", "project-menu-remote"} {
		class := "pencode-project-menu-item"
		if p.index == i {
			class += " active"
		}
		view.Attr(ref, "class", class)
		view.Event(ref, htmlui.EventClick)
	}
	view.Attr("project-menu-local-icon", "src", m.themedIcon("icons/folder-opened.svg", "icon.foreground"))
	view.Attr("project-menu-remote-icon", "src", m.themedIcon("icons/remote.svg", "icon.foreground"))
	c := m.session.connection
	setContextMenuEnabled(view, "project-menu-local", c == nil || !c.busy)
	setContextMenuEnabled(view, "project-menu-remote", c != nil)
	setPanelVisible(view, "project-menu-hosts", c != nil && len(c.profiles) != 0)
	if c == nil {
		return
	}
	for i, profile := range c.profiles {
		view.AppendComponentChild("project-menu-host-list", componentKey(profile.Name+"\x00"+profile.Address()), definition,
			pencodeProjectHostProps{Model: m, Theme: m.workbenchTheme, Profile: profile, Active: p.index == i+2, Busy: c.busy}, struct{}{})
	}
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
	switch m.projectMenu.index {
	case 0:
		return "project-menu-local"
	case 1:
		return "project-menu-remote"
	default:
		return "project-menu-host:" + m.session.connection.profiles[m.projectMenu.index-2].Address()
	}
}

func (m *workspaceModel) focusProjectMenu(world *module.Content) {
	if !m.projectMenu.focus {
		return
	}
	ref := "add-project"
	if m.projectMenu.open {
		ref = m.projectMenuRef()
	}
	if entity, ok := world.HTMLControl(m.surface, ref); ok && world.Focus(entity) {
		m.projectMenu.focus = false
	}
}

func (m *workspaceModel) projectMenuKeys(state *input.State) bool {
	p := &m.projectMenu
	switch {
	case m.uiKey(state, input.KeyEscape):
		p.open, p.focus = false, true
	case m.uiKey(state, input.KeyDown), m.uiKey(state, input.KeyUp):
		count := 2
		if c := m.session.connection; c != nil {
			count += len(c.profiles)
		}
		delta := 1
		if m.uiKey(state, input.KeyUp) {
			delta = -1
		}
		p.index, p.focus = (p.index+delta+count)%count, true
	case m.uiKey(state, input.KeyEnter), m.uiKey(state, input.KeySpace):
		if p.index < 2 {
			m.projectMenuEvent(m.projectMenuRef())
		} else {
			m.openProjectHost(m.session.connection.profiles[p.index-2])
		}
	default:
		return false
	}
	m.languageUI.keysHandled = true
	return true
}
