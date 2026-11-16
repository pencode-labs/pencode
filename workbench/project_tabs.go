package workbench

import (
	"github.com/onix-engine/onix/module"
	htmlui "github.com/onix-engine/onix/ui/html"
)

type pencodeProjectTabProps struct {
	Model   *workspaceModel
	Project *pencodeProject
	Theme   *pencodeTheme
	Active  bool
}

func newProjectTabDefinition(content *module.Content) (*htmlui.Definition[pencodeProjectTabProps, struct{}], error) {
	return content.Define[pencodeProjectTabProps, struct{}](
		htmlui.NewTemplate(`<div data-ref="tab" class="pencode-project-tab"><button data-ref="select" class="pencode-project-tab-select" type="button" role="tab"><span data-ref="name"></span></button><button data-ref="close" class="pencode-project-tab-close" type="button"><img data-ref="close-icon" class="pencode-close-icon" src="icons/close.svg" alt=""></button></div>`), "",
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
