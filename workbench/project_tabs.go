package workbench

import (
	"fmt"
	"hash/crc32"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/onix-engine/onix/module"
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
	Active  bool
}

func newProjectTabDefinition(content *module.Content) (*htmlui.Definition[pencodeProjectTabProps, struct{}], error) {
	return content.Define[pencodeProjectTabProps, struct{}](
		htmlui.NewTemplate(`<div data-ref="tab" class="pencode-project-tab">
  <button data-ref="select" class="pencode-project-tab-select" type="button" role="tab">
    <span data-ref="icon" class="pencode-project-tab-icon" aria-hidden="true"><span data-ref="initial"></span></span>
    <span data-ref="name" class="pencode-project-tab-name"></span>
  </button>
  <button data-ref="close" class="pencode-project-tab-close" type="button"><img data-ref="close-icon" class="pencode-close-icon" src="icons/close.svg" alt=""></button>
</div>`), "",
		module.ContentBehavior[pencodeProjectTabProps, struct{}]{
			Render: func(c module.ContentRenderContext[pencodeProjectTabProps, struct{}], view *htmlui.View) {
				p := c.Props
				className, foreground := "pencode-project-tab", "descriptionForeground"
				id := "project-tab:" + p.Project.Path
				if p.Active {
					className, foreground = className+" active", "titleBar.activeForeground"
					id = "active-project-tab"
				}
				view.Attr("tab", "id", id)
				view.Attr("tab", "class", className)
				view.Attr("select", "id", "project-tab-select:"+p.Project.Path)
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
				if m.closing.action != pencodeCloseNone || m.fileDrag.suppressClick {
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
	for _, project := range m.projects {
		view.AppendComponentChild("project-tabs", componentKey(project.Path), definition,
			pencodeProjectTabProps{Model: m, Project: project, Theme: m.workbenchTheme, Active: project == m.activeProjectState()}, struct{}{})
	}
}
