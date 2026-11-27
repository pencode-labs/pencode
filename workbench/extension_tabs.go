package workbench

import (
	"path"

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
	source := model.sourceState()
	for tab := range model.editorTabs() {
		props := tabProps{Model: model, Theme: model.workbenchTheme, Icons: model.fileIcons}
		var key uint64
		switch {
		case tab.source != nil:
			comparison := tab.source
			name := path.Base(comparison.Path)
			props.Source, props.Path = comparison, comparison.Path
			props.Name = name + " · " + comparison.Kind
			if comparison.Kind == "history" {
				props.Name += " · " + comparison.Revision[:8]
			} else if comparison.Kind == "local-history" {
				props.Name = name + " · " + model.tr("本地历史") + " · " + comparison.Revision
			}
			props.Icon = props.Icons.source(name, false)
			if props.Icon == "" {
				props.Icon = "icons/file-text.svg"
			}
			props.Active, props.Preview = source.Active == comparison.Key, comparison.Preview
			props.Dirty, props.CloseDisabled = comparison.Result != comparison.Saved, source.busy()
			key = componentKey(comparison.Key) ^ componentKey("sc-tab-group")
		case tab.surface != nil:
			surface := tab.surface
			props.Surface, props.Path, props.Name = surface, surface.rootID, surface.ui.Title
			props.Active, props.Busy, props.Unread = surface.live(), surface.ui.Busy, surface.ui.Unread
			for _, action := range model.actions.rows {
				if action.Ref == surface.invocation.anchor {
					props.Icon = action.Action.Icon
					break
				}
			}
			key = componentKey(surface.rootID)
		default:
			file := tab.file(model)
			props.Project, props.Path, props.Name = tab.project, file.Path, file.Name
			props.Active = tab.project == model.activeProjectState() && model.actions.ui.editor == nil && !source.visible() && model.activePath == tab.path
			props.Preview, props.Dirty = file.Preview, file.Dirty
			key = componentKey(file.Path)
			if project := tab.project; project != nil {
				props.Decoration = model.fileNameDecoration(project, file.treeEntry(project.Entries))
				key ^= componentKey(project.Path) * 1099511628211
			}
		}
		view.AppendComponentChild("tabs", key, definition, props, struct{}{})
	}
}
