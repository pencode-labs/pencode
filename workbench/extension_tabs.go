package workbench

import (
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/peb"
)

func (model *workspaceModel) syncExtensionProject(previous string) {
	if previous == model.projectPath {
		return
	}
	for _, surface := range model.actions.ui.surfaces {
		if surface.ui.Kind != peb.EditorTab {
			continue
		}
		surface.invocation.context = peb.ActionContext{ID: surface.invocation.context.ID, ProjectPath: model.projectPath}
		surface.send(peb.EditorEvent{Kind: "projectchange"}, "")
	}
}

func (model *workspaceModel) selectExtensionTab(surface *pencodeExtensionSurface) {
	if model.closing.action != pencodeCloseNone {
		return
	}
	model.cancelFileOpens()
	model.actions.ui.editor = surface
	model.sourceState().Active = ""
	model.screen = pencodeWorkspace
}

func (model *workspaceModel) selectFirstExtensionTab() {
	for _, surface := range model.actions.ui.surfaces {
		if surface.ui.Kind == peb.EditorTab {
			model.selectExtensionTab(surface)
			return
		}
	}
}

func (model *workspaceModel) selectExtensionTabNeighbor(closed *pencodeExtensionSurface) {
	var previous, next pencodeEditorTab
	found := false
	for tab := range model.editorTabs() {
		if found {
			next = tab
			break
		}
		if tab.surface == closed {
			found = true
		} else {
			previous = tab
		}
	}
	if next == (pencodeEditorTab{}) {
		next = previous
	}
	model.actions.ui.editor = nil
	switch {
	case next.surface != nil:
		model.selectExtensionTab(next.surface)
	case next.source != nil:
		s := model.sourceState()
		s.Active, s.RestoreScroll = next.source.Key, true
		s.Selection++
	case next.path != "":
		model.selectTab(next.path)
	case len(model.tabs) > 0:
		model.selectTab(model.tabs[len(model.tabs)-1])
	}
}

func (model *workspaceModel) renderExtensionTabs(view *htmlui.View, definition *htmlui.Definition[tabProps, struct{}]) {
	for _, surface := range model.actions.ui.surfaces {
		if surface.ui.Kind != peb.EditorTab {
			continue
		}
		icon := ""
		for _, action := range model.actions.rows {
			if action.Ref == surface.invocation.anchor {
				icon = action.Action.Icon
				break
			}
		}
		view.AppendComponentChild("file-tabs", componentKey(surface.rootID), definition,
			tabProps{Model: model, Surface: surface, Theme: model.workbenchTheme, Icons: model.fileIcons,
				Path: surface.rootID, Name: surface.ui.Title, Icon: icon, Active: surface.live(), Busy: surface.ui.Busy, Unread: surface.ui.Unread}, struct{}{})
	}
}
