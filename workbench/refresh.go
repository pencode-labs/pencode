package workbench

import (
	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/module"
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/peb"
)

type pencodeRefresh uint8

const (
	pencodeRefreshEditor pencodeRefresh = 1 << iota
	pencodeRefreshTerminal
	pencodeRefreshOutput
	pencodeRefreshSelection
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
	previous := m.selectionPrevious
	m.selectionPrevious = ""
	if full || regions == 0 || c.Props.Screen != pencodeWorkspace {
		return false
	}
	if regions&pencodeRefreshEditor != 0 {
		if !m.textEditorActive() || m.findOpen || m.quickOpen || m.settingsOpen || m.languageUI.hover.open || m.languageUI.contextOpen || m.languages != nil && m.languages.panel.open {
			return false
		}
		renderPencodeHighlight(m)
		if regions&pencodeRefreshSelection != 0 {
			m.renderEditorHeader(view, m.activeFile())
			for entity, access := range module.ContentQuery[htmlui.Component[treeProps, struct{}]](c.World) {
				props := access.Ref().Props
				if props.Model == m && props.Project == m.activeProjectState() && (props.Path == previous || props.Path == m.activePath) {
					module.ContentUpdateState[treeProps, struct{}](c.World, entity, func(*struct{}) {})
				}
			}
		}
		view.Text("line-info", m.lineInfo)
		view.Text("launcher-status", m.status)
		// A first edit pins the preview and changes the dirty indicator, without
		// reconciling the explorer or the other workbench regions.
		for entity, access := range module.ContentQuery[htmlui.Component[tabProps, struct{}]](c.World) {
			props := access.Get().Props
			if props.Model != m || props.Project == nil {
				continue
			}
			active := props.Project == m.activeProjectState() && props.Path == m.activePath
			if file := props.Project.Files[props.Path]; file != nil && (props.Dirty != file.Dirty || props.Preview != file.Preview || regions&pencodeRefreshSelection != 0 && props.Active != active) {
				if regions&pencodeRefreshSelection != 0 {
					props.Active = active
				}
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

// A cached ordinary-file selection changes bindings, not workbench structure.
type pencodeSelectionSnapshot struct {
	project  *pencodeProject
	path     string
	name     string
	revision uint64
	tabs     int
	valid    bool
}

func (m *workspaceModel) selectionSnapshot() pencodeSelectionSnapshot {
	p := m.activeProjectState()
	valid := p != nil && !p.Collapsed && m.textEditorActive() && m.activeFile() != nil && !m.activeFile().editorSplit && m.actions.ui.editor == nil && !m.findOpen && !m.quickOpen && !m.settingsOpen && !m.languageUI.hover.open && !m.languageUI.contextOpen && !m.fileContext.open && m.fileContext.dialog == "" && !m.tabContext.open
	if !valid {
		return pencodeSelectionSnapshot{}
	}
	return pencodeSelectionSnapshot{project: p, path: m.activePath, name: m.activeFile().Name, revision: p.tree.revision, tabs: len(p.Tabs), valid: true}
}
func (m *workspaceModel) refreshFileSelection(before pencodeSelectionSnapshot) {
	after := m.selectionSnapshot()
	if !before.valid || !after.valid || before.project != after.project || before.revision != after.revision || before.tabs != after.tabs {
		markPencodeRootDirtyByModel(m.fontResources, m)
		return
	}
	for _, action := range m.actions.snapshot {
		if action.Location == peb.OpenWith && (editorMatch(&action, before.name) > 0) != (editorMatch(&action, after.name) > 0) {
			markPencodeRootDirtyByModel(m.fontResources, m)
			return
		}
	}
	// Consume the file-dependent action refresh now; its bindings are unchanged.
	revision := m.actions.revision
	m.syncActions()
	if revision != m.actions.revision {
		markPencodeRootDirtyByModel(m.fontResources, m)
		return
	}
	if m.refresh&pencodeRefreshSelection == 0 {
		m.selectionPrevious = before.path
	}
	m.refreshRegion(m.fontResources, pencodeRefreshEditor|pencodeRefreshSelection)
}
func (m *workspaceModel) finishSelectionRefresh(world *module.Content) {
	if m.refresh&pencodeRefreshSelection != 0 {
		markPencodeComponentDirty(world, m)
	} else {
		markPencodeRootDirtyByModel(world, m)
	}
}
