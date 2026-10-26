package workbench

import (
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/peb"
)

func (model *workspaceModel) selectExtensionTab(surface *pencodeExtensionSurface) {
	if model.closing.action != pencodeCloseNone {
		return
	}
	model.cancelFileOpens()
	model.actions.ui.editor = surface
	model.sourceState().Active = ""
	model.screen = pencodeWorkspace
}

func (model *workspaceModel) renderExtensionTabs(view *htmlui.View, definition *htmlui.Definition[tabProps, struct{}]) {
	for _, surface := range model.actions.ui.surfaces {
		if surface.ui.Kind != peb.EditorTab {
			continue
		}
		htmlui.AppendComponentChild(view, "file-tabs", componentKey(surface.rootID), definition,
			tabProps{Model: model, Surface: surface, Theme: model.workbenchTheme, Icons: model.fileIcons,
				Path: surface.rootID, Name: surface.ui.Title, Active: surface.live()}, struct{}{})
	}
}
