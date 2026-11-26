package workbench

import (
	htmlui "github.com/onix-engine/onix/ui/html"
)

func (model *workspaceModel) selectExtensionTab(surface *pencodeExtensionSurface) {
	if model.closing.action != pencodeCloseNone {
		return
	}
	if current := model.actions.ui.editor; current != nil && current != surface {
		current.saveScrollPositions()
	}
	model.cancelFileOpens()
	model.actions.ui.editor = surface
	model.sourceState().Active = ""
	model.screen = pencodeWorkspace
}

func (model *workspaceModel) renderEditorTabs(view *htmlui.View, definition *htmlui.Definition[tabProps, struct{}]) {
	for tab := range model.editorTabs() {
		if tab.source != nil {
			break // Comparisons are rendered in the adjacent source-tabs container.
		}
		props := tabProps{Model: model, Theme: model.workbenchTheme, Icons: model.fileIcons}
		var key uint64
		if surface := tab.surface; surface != nil {
			props.Surface, props.Path, props.Name = surface, surface.rootID, surface.ui.Title
			props.Active, props.Busy, props.Unread = surface.live(), surface.ui.Busy, surface.ui.Unread
			for _, action := range model.actions.rows {
				if action.Ref == surface.invocation.anchor {
					props.Icon = action.Action.Icon
					break
				}
			}
			key = componentKey(surface.rootID)
		} else {
			file := tab.file(model)
			props.Project, props.Path, props.Name = tab.project, file.Path, file.Name
			props.Active = tab.project == model.activeProjectState() && model.actions.ui.editor == nil && !model.sourceState().visible() && model.activePath == tab.path
			props.Preview, props.Dirty = file.Preview, file.Dirty
			key = componentKey(file.Path)
			if project := tab.project; project != nil {
				props.Decoration = model.fileNameDecoration(project, file.treeEntry(project.Entries))
				key ^= componentKey(project.Path) * 1099511628211
			}
		}
		view.AppendComponentChild("file-tabs", key, definition, props, struct{}{})
	}
}
