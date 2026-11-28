package workbench

import (
	"fmt"
	"hash/crc32"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onix-engine/onix/ecs"
	"github.com/onix-engine/onix/html/markup"
	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/module"
	"github.com/onix-engine/onix/scene"
	htmlui "github.com/onix-engine/onix/ui/html"
)

var projectTabIconStyles = [...]string{
	"background-color:#315e9e", "background-color:#85602b", "background-color:#237269", "background-color:#873e63",
	"background-color:#4e6c38", "background-color:#624ba0", "background-color:#975032", "background-color:#346979",
	"background-color:#934552", "background-color:#495d91", "background-color:#6f682b", "background-color:#743e85",
	"background-color:#35704d", "background-color:#8e4d72", "background-color:#87543f", "background-color:#366d93",
}

var nativeProjectSwitcherStyles = func() [pencodeDefaultZoom - pencodeMinZoom]string {
	var styles [pencodeDefaultZoom - pencodeMinZoom]string
	for i := range styles {
		zoom := pencodeMinZoom + i
		// Keep six window points after AppKit's buttons, accounting for the
		// root border and six pixels of titlebar padding.
		left := (8800+zoom-1)/zoom - 7
		styles[i] = "left:" + strconv.Itoa(left) + "px"
	}
	return styles
}()

func projectTabIconStyle(path string, projects []*pencodeProject) string {
	hash := crc32.ChecksumIEEE([]byte(path))
	styleInUse := func(style string) bool {
		return slices.ContainsFunc(projects, func(project *pencodeProject) bool {
			return project.tabIconStyle == style
		})
	}
	count := len(projectTabIconStyles)
	start := int(hash % uint32(count))
	for i := range count {
		style := projectTabIconStyles[(start+i)%count]
		if !styleInUse(style) {
			return style
		}
	}
	// Generate extra shades with sufficiently dark RGB channels for white text.
	for color := hash; ; color += 0x9e3779b9 {
		rgb := (color & 0x5f5f5f) + 0x141414
		style := fmt.Sprintf("background-color:#%06x", rgb)
		if !styleInUse(style) {
			return style
		}
	}
}

func projectTabInitial(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "?"
	}
	_, size := utf8.DecodeRuneInString(name)
	return name[:size]
}

type pencodeProjectTabProps struct {
	Model   *workspaceModel
	Project *pencodeProject
	Theme   *pencodeTheme
	Drag    string
	Active  bool
}

func (p *pencodeProject) titlebarTabID(active bool) string {
	if active {
		return "active-project-tab"
	}
	return p.tabID
}

func newProjectTabDefinition(content *module.Content) (*htmlui.Definition[pencodeProjectTabProps, struct{}], error) {
	template, err := htmlui.BuildTemplate(func(b *markup.Builder) {
		b.Open("div", "data-ref", "tab", "class", "pencode-project-tab")
		b.Open("button", "data-ref", "select", "class", "pencode-project-tab-select", "type", "button", "role", "tab")
		b.Open("span", "data-ref", "icon", "class", "pencode-project-tab-icon", "aria-hidden", "true")
		b.Element("span", "data-ref", "initial")
		b.End()
		b.Element("span", "data-ref", "name", "class", "pencode-project-tab-name")
		b.End()
		b.Open("button", "data-ref", "close", "class", "pencode-project-tab-close", "type", "button")
		b.Element("img", "data-ref", "close-icon", "class", "pencode-close-icon", "src", "icons/close.svg", "alt", "")
		b.End()
		b.End()
	})
	if err != nil {
		return nil, err
	}
	return content.Define[pencodeProjectTabProps, struct{}](
		template, "",
		module.ContentBehavior[pencodeProjectTabProps, struct{}]{
			Render: func(c module.ContentRenderContext[pencodeProjectTabProps, struct{}], view *htmlui.View) {
				p := c.Props
				className, foreground := "pencode-project-tab", "descriptionForeground"
				if p.Active {
					className, foreground = className+" active", "titleBar.activeForeground"
				}
				view.Attr("tab", "id", p.Project.titlebarTabID(p.Active))
				view.Attr("tab", "class", className)
				if p.Drag != "" {
					view.Attr("tab", "data-drag", p.Drag)
				}
				view.Attr("select", "id", p.Project.tabSelectID)
				view.Attr("select", "title", p.Project.Path)
				view.Attr("select", "aria-selected", boolAttribute(p.Active))
				view.Attr("close", "id", "project-tab-close:"+p.Project.Path)
				view.Attr("close", "title", p.Model.tr("关闭项目"))
				view.Attr("close", "aria-label", p.Model.tr("关闭项目")+" "+p.Project.Name)
				view.Attr("close-icon", "src", p.Model.themedIcon("icons/close.svg", foreground))
				view.Attr("icon", "style", p.Project.tabIconStyle)
				view.Text("initial", projectTabInitial(p.Project.Name))
				view.Text("name", p.Project.Name)
				view.Event("select", htmlui.EventClick)
				view.Event("close", htmlui.EventClick)
			},
			HandleEvent: func(c *module.ContentEventContext[pencodeProjectTabProps, struct{}]) {
				p := c.Props
				m := p.Model
				if m.closing.action != pencodeCloseNone || m.fileDrag.suppressClick || m.projectTabDrag.suppressClick {
					return
				}
				switch c.Ref {
				case "select":
					m.activateProject(p.Project)
				case "close":
					m.closeProject(p.Project)
				default:
					return
				}
				setPencodeEditorValue(c.World, m.surface, m.activeContent())
				markPencodeRootDirtyByModel(c.World, m)
			},
		},
	)
}

func (m *workspaceModel) renderProjectTabs(view *htmlui.View, definition *htmlui.Definition[pencodeProjectTabProps, struct{}]) {
	active, drag := m.activeProjectState(), &m.projectTabDrag
	setPanelVisible(view, "project-tab-preview", drag.active)
	if drag.active {
		view.Attr("root", "data-project-tab-drag", "")
		view.Attr("project-tab-preview-icon", "style", drag.source.tabIconStyle)
		view.Text("project-tab-preview-initial", projectTabInitial(drag.source.Name))
		view.Text("project-tab-preview-name", drag.source.Name)
	}
	for _, project := range m.projects {
		if project.tabID == "" {
			project.tabID = "project-tab:" + project.Path
			project.tabSelectID = "project-tab-select:" + project.Path
		}
		role := ""
		if drag.active && project == drag.source {
			role = "source"
		} else if project == drag.target {
			role = "before"
			if drag.after {
				role = "after"
			}
		}
		view.AppendComponentChild("project-tabs", componentKey(project.Path), definition,
			pencodeProjectTabProps{Model: m, Project: project, Theme: m.workbenchTheme, Drag: role, Active: project == active}, struct{}{})
	}
}

type pencodeProjectTabDrag struct {
	nextScroll                   time.Time
	source, target               *pencodeProject
	grabX, grabY                 float32
	previewX, previewY           float32
	active, after, suppressClick bool
	positioned                   bool
}

func (m *workspaceModel) syncProjectTabPreview(world *module.Content, pointer input.Pointer) {
	drag := &m.projectTabDrag
	if !drag.active {
		return
	}
	preview, _ := world.HTMLNode(m.surface, "project-tab-preview")
	bounds, ok := preview.Bounds()
	if !ok {
		return
	}
	zoom := max(float32(m.settings.Zoom)/100, .01)
	x := min(max(0, pointer.X/zoom-drag.grabX-bounds.X), max(0, m.viewportWidth/zoom-bounds.Width-bounds.X))
	y := min(max(0, pointer.Y/zoom-drag.grabY-bounds.Y), max(0, m.viewportHeight/zoom-bounds.Height-bounds.Y))
	if !drag.positioned || x != drag.previewX || y != drag.previewY {
		drag.positioned = world.HTMLSetOffset(m.surface, "project-tab-preview", x, y)
		drag.previewX, drag.previewY = x, y
	}
}

// ProjectTabDragSystem resolves drags before pointer releases can activate a tab.
func (s *Session) ProjectTabDragSystem() ecs.SystemConfig {
	return ecs.SystemConfig{
		Name: "update_pencode_project_tab_drag", Before: []string{"dispatch_pointer_events_2d"},
		System: func(c *ecs.SystemContext) error {
			if s.active != nil {
				m := s.active.model
				if state, ok := c.World.GetResource[*input.State](); ok && m.updateProjectTabDrag(m.fontResources, state) {
					markPencodeRootDirtyByModel(m.fontResources, m)
				}
			}
			return nil
		},
	}
}

func (m *workspaceModel) updateProjectTabDrag(world *module.Content, state *input.State) bool {
	drag := &m.projectTabDrag
	pointer := state.Pointer()
	if drag.suppressClick && (pointer.Pressed || m.uiKey(state, input.KeyEnter) || m.uiKey(state, input.KeySpace)) {
		drag.suppressClick = false
	}
	if drag.source == nil && !pointer.Pressed {
		return false
	}
	if m.screen != pencodeWorkspace || len(m.projects) < 2 || m.settingsOpen || m.quickOpen || m.folderSearch.open ||
		m.fileContext.dialog != "" || m.closing.action != pencodeCloseNone || m.market != nil && m.market.open ||
		m.uiKey(state, input.KeyEscape) || state.MousePressed(input.MouseRight) ||
		pointer.Device != input.PointerDeviceMouse || !pointer.Down && !pointer.Released {
		return drag.finish()
	}
	node, _ := world.HTMLNode(m.surface, "project-tabs")
	viewport, ok := node.Bounds()
	if !ok {
		return drag.finish()
	}
	zoom := max(float32(m.settings.Zoom)/100, .01)
	x, y := pointer.X/zoom, pointer.Y/zoom
	px, py := pointer.PressX/zoom, pointer.PressY/zoom
	changed := false
	if pointer.Pressed {
		changed = drag.finish()
		// The press location still identifies the tab when press and move arrive together.
		if !languageRectContains(viewport, px, py) {
			return changed
		}
	}
	area, _ := node.ScrollContainer()
	scroll := world.Ref[scene.UIScroll](area)
	var offset float32
	if scroll != nil {
		offset = scroll.Position.X()
	}
	if pointer.Pressed {
		for _, project := range m.projects {
			selectNode, _ := world.HTMLNode(m.surface, project.tabSelectID)
			bounds, ok := selectNode.Bounds()
			if ok && languageRectContains(bounds, px-offset, py) {
				tab, _ := world.HTMLNode(m.surface, project.titlebarTabID(project == m.activeProjectState()))
				if box, ok := tab.Bounds(); ok {
					drag.source = project
					drag.grabX, drag.grabY = px-offset-box.X, py-box.Y
				}
				break
			}
		}
	}
	if drag.source == nil {
		return changed
	}
	dx, dy := x-px, y-py
	if !drag.active && dx*dx+dy*dy < 36 {
		if pointer.Released {
			drag.finish()
		}
		return changed
	}
	changed = changed || !drag.active
	drag.active, drag.suppressClick = true, true
	from, to := -1, -1
	if languageRectContains(viewport, x, y) {
		if !pointer.Released && scroll != nil && scroll.Horizontal && !scroll.DisableInput {
			offset = m.scrollProjectTabDrag(world, area, scroll, viewport, x)
		}
		active := m.activeProjectState()
		slot := 0
		for index, project := range m.projects {
			if project == drag.source {
				from = index
			}
			tab, _ := world.HTMLNode(m.surface, project.titlebarTabID(project == active))
			if bounds, ok := tab.Bounds(); ok && x-offset >= bounds.X+bounds.Width/2 {
				slot = index + 1
			}
		}
		if from < 0 {
			return drag.finish()
		}
		// Convert the gap in the original order to the index after removing the source.
		to = slot
		if from < slot {
			to--
		}
	} else {
		drag.nextScroll = time.Time{}
	}
	if pointer.Released {
		if to >= 0 && from != to {
			m.moveProjectTab(from, to)
		}
		return drag.finish()
	}
	var target *pencodeProject
	if to >= 0 && from != to {
		target = m.projects[to]
	}
	after := to > from
	changed = changed || drag.target != target || drag.after != after
	drag.target, drag.after = target, after
	return changed
}

func (drag *pencodeProjectTabDrag) finish() bool {
	active := drag.active
	*drag = pencodeProjectTabDrag{suppressClick: drag.suppressClick}
	return active
}

func (m *workspaceModel) scrollProjectTabDrag(world *module.Content, area ecs.Entity, scroll *scene.UIScroll, viewport htmlui.Rect, x float32) float32 {
	drag := &m.projectTabDrag
	offset := scroll.Position.X()
	edge := min(20, viewport.Width/4)
	leftLimit := min(0, scroll.ViewportSize.X()-scroll.ContentSize.X())
	next := offset
	switch {
	case x < viewport.X+edge:
		next = min(0, offset+12)
	case x >= viewport.X+viewport.Width-edge:
		next = max(leftLimit, offset-12)
	}
	if next == offset {
		drag.nextScroll = time.Time{}
		return offset
	}
	now := time.Now()
	if !now.Before(drag.nextScroll) {
		scroll := world.Mut[scene.UIScroll](area)
		scroll.Position[0], scroll.Momentum[0] = next, 0
		offset = next
		drag.nextScroll = now.Add(32 * time.Millisecond)
		if m.app != nil {
			m.app.RequestRender()
		}
	}
	if m.app != nil && m.app.WakeAt != nil {
		m.app.WakeAt(drag.nextScroll)
	}
	return offset
}

func (m *workspaceModel) moveProjectTab(from, to int) {
	project, active := m.projects[from], m.activeProjectIndex
	if from < to {
		copy(m.projects[from:to], m.projects[from+1:to+1])
	} else {
		copy(m.projects[to+1:from+1], m.projects[to:from])
	}
	m.projects[to] = project
	switch {
	case active == from:
		m.activeProjectIndex = to
	case from < active && active <= to:
		m.activeProjectIndex--
	case to <= active && active < from:
		m.activeProjectIndex++
	}
}
