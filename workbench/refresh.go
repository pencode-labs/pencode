package workbench

import (
	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/module"
	htmlui "github.com/diiyw/onix/ui/html"
)

type pencodeRefresh uint8

const (
	pencodeRefreshEditor pencodeRefresh = 1 << iota
	pencodeRefreshTerminal
	pencodeRefreshOutput
)

func (m *workspaceModel) refreshRegion(world *module.Content, region pencodeRefresh) {
	if !m.fullRefresh {
		m.refresh |= region
	}
	markPencodeComponentDirty(world, m)
}

// Structural changes take precedence over all queued binding patches.
func (m *workspaceModel) patchWorkbench(c module.ContentRenderContext[appProps, appState], view *htmlui.View) bool {
	regions, full := m.refresh, m.fullRefresh
	m.refresh, m.fullRefresh = 0, false
	if full || regions == 0 || c.Props.Screen != pencodeWorkspace {
		return false
	}
	if regions&pencodeRefreshEditor != 0 {
		if !m.textEditorActive() || m.findOpen || m.quickOpen || m.settingsOpen || m.languageUI.hover.open || m.languageUI.contextOpen || m.languages != nil && m.languages.panel.open {
			return false
		}
		renderPencodeHighlight(m)
		view.Text("line-info", m.lineInfo)
		view.Text("launcher-status", m.status)
		// A first edit pins the preview and changes the dirty indicator, without
		// reconciling the explorer or the other workbench regions.
		for entity, access := range module.ContentQuery[htmlui.Component[tabProps, struct{}]](c.World) {
			props := access.Get().Props
			if props.Model != m || props.Project == nil {
				continue
			}
			if file := props.Project.Files[props.Path]; file != nil && (props.Dirty != file.Dirty || props.Preview != file.Preview) {
				props.Dirty, props.Preview = file.Dirty, file.Preview
				module.ContentMut[htmlui.Component[tabProps, struct{}]](c.World, entity).Props = props
				module.ContentUpdateState[tabProps, struct{}](c.World, entity, func(*struct{}) {})
			}
		}
	}
	if regions&pencodeRefreshTerminal != 0 && m.bottom.active == pencodePanelTerminal {
		m.prepareTerminalView()
		view.Text("terminal-content", m.terminalView.value)
	}
	if regions&pencodeRefreshOutput != 0 && m.bottom.active == pencodePanelOutput {
		count := len(m.bottom.options)
		m.syncOutput()
		if count != len(m.bottom.options) {
			return false
		}
		view.Text("output-content", m.bottom.outputText)
	}
	view.PatchBindings()
	return true
}

// Keep the generic query out of the high-frequency callers.
func markPencodeComponentDirty(world *module.Content, m *workspaceModel) {
	if world == nil || m == nil {
		return
	}
	if m.root == 0 || !world.IsAlive(m.root) {
		for entity, access := range module.ContentQuery[htmlui.Component[appProps, appState]](world) {
			if access.Get().Props.Model == m {
				m.root = entity
				break
			}
		}
	}
	if m.root != ecs.Entity(0) {
		_ = module.ContentUpdateState[appProps, appState](world, m.root, func(s *appState) { s.Revision++ })
	}
	if m.app != nil {
		m.app.RequestRender()
	}
}
