package workbench

import (
	"github.com/onix-engine/onix/scene"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/lsp"
	"github.com/pencode-labs/pencode/output"
	"github.com/pencode-labs/pencode/peb"
	"github.com/pencode-labs/pencode/textsearch"
)

// The foreground model binds one project's state to the window's controls.
// Inactive projects keep their own sessions and receive their own replies.
type pencodeProjectActions struct {
	sessions map[string]*extensions.ActionSession
	changes  map[string]peb.ActionChange
	ui       pencodeExtensionUIState
}

type pencodeProjectLayout struct {
	source                                          *output.Channel
	sidebarWidth, changesWidth, panelHeight, filter string
	loggedStatus                                    string
	panel                                           pencodePanel
}

type pencodeProjectView struct {
	folder        *pencodeFolderSearch
	problemGroups map[lsp.URI]*pencodeProblemGroup
	market        pencodeMarketplaceView
	layout        pencodeProjectLayout
	portUI        pencodePortUI
	actions       pencodeProjectActions
	pencodeProjectInteraction
	tabPosition              float32
	restoreTabPosition       bool
	problemGroupsInitialized bool
}

// These values move together when the shared controls bind to another project.
type pencodeProjectInteraction struct {
	findPreview                                                           *pencodeCustomDocument
	findQuery, replaceQuery, quickQuery                                   string
	extensionFeedback, settingsFeedback, shortcutQuery, settingsExtension string
	findScope                                                             pencodeFindScope
	navigation                                                            pencodeNavigationHistory
	fileContext                                                           pencodeFileContext
	debug                                                                 pencodeDebugState
	quickIndex, findIndex                                                 int
	findOptions                                                           textsearch.Options
	settingsToolPage                                                      toolSettingsPage
	settingsOpen, settingsAbout, settingsShortcuts                        bool
	settingsExtensions, settingsExtensionsExpanded                        bool
	quickOpen, findOpen, replaceOpen, findSelection                       bool
}

func (m *workspaceModel) storedView(p *pencodeProject) *pencodeProjectView {
	if p == nil {
		return &m.launcherView
	}
	return &p.view
}

func (m *workspaceModel) projectActions(p *pencodeProject) *pencodeProjectActions {
	if p == m.activeProjectState() {
		return &m.actions.pencodeProjectActions
	}
	return &m.storedView(p).actions
}

func (m *workspaceModel) actionGroups(yield func(*pencodeProject, *pencodeProjectActions) bool) {
	active := m.activeProjectState()
	if !yield(active, &m.actions.pencodeProjectActions) {
		return
	}
	if active != nil && !yield(nil, &m.launcherView.actions) {
		return
	}
	for _, p := range m.projects {
		if p != active && !yield(p, &p.view.actions) {
			return
		}
	}
}

func (m *workspaceModel) projectInteraction(p *pencodeProject) *pencodeProjectInteraction {
	if p == m.activeProjectState() {
		return &m.pencodeProjectInteraction
	}
	return &m.storedView(p).pencodeProjectInteraction
}

func (m *workspaceModel) projectPanel(p *pencodeProject) *pencodePanel {
	if p == m.activeProjectState() {
		return &m.bottom.active
	}
	return &m.storedView(p).layout.panel
}

func (m *workspaceModel) currentLayout() pencodeProjectLayout {
	return pencodeProjectLayout{
		source: m.bottom.source, sidebarWidth: m.sidebarWidthStyle, changesWidth: m.sourceChangesWidthStyle,
		panelHeight: m.bottom.heightStyle, filter: m.bottom.filter, loggedStatus: m.bottom.lastStatus, panel: m.bottom.active,
	}
}

// Move a value between the shared controls and their project's backing state.
func switchValue[T any](current, previous, next *T) {
	*previous, *current = *current, *next
	var zero T
	*next = zero
}

func (m *workspaceModel) switchProjectView(p *pencodeProject) {
	old, next := m.storedView(m.viewProject), m.storedView(p)
	m.cancelFileOpens()
	if m.viewProject != nil && m.viewProject.tree.area != 0 {
		m.viewProject.tree.restore = true
	}
	if m.fontResources != nil && m.tabScroll.project == m.viewProject && !old.restoreTabPosition {
		if scroll := m.fontResources.Ref[scene.UIScroll](m.tabScroll.area); scroll != nil {
			old.tabPosition = scroll.Position.X()
		}
	}
	next.restoreTabPosition = true
	for _, s := range m.actions.ui.surfaces {
		s.saveProjectInputs()
		if s.visible {
			s.saveScrollPositions()
			s.setVisible(false)
		}
	}
	// Selection highlights are native entities belonging to the shared window.
	quads := m.actions.ui.selection.quads
	m.actions.ui.selection = pencodeExtensionTextSelection{}
	switchValue(&m.actions.pencodeProjectActions, &old.actions, &next.actions)
	m.actions.ui.selection.quads = quads
	old.layout = m.currentLayout()
	m.switchTerminalProject(p)
	layout := next.layout
	m.sidebarWidthStyle, m.sourceChangesWidthStyle = layout.sidebarWidth, layout.changesWidth
	m.bottom.active, m.bottom.heightStyle, m.bottom.source, m.bottom.filter = layout.panel, layout.panelHeight, layout.source, layout.filter
	m.bottom.lastStatus = layout.loggedStatus
	switchValue(&m.bottom.problemGroupByURI, &old.problemGroups, &next.problemGroups)
	switchValue(&m.bottom.problemGroupsInitialized, &old.problemGroupsInitialized, &next.problemGroupsInitialized)
	m.bottom.problemsDirty = true
	m.bottom.options = m.bottom.options[:0]
	m.capturingShortcut = false
	m.captureAction = ""
	if m.market != nil {
		switchValue(&m.market.pencodeMarketplaceView, &old.market, &next.market)
		m.market.project = p
		m.market.revision++
	}
	m.debug.startPending = false
	switchValue(&m.pencodeProjectInteraction, &old.pencodeProjectInteraction, &next.pencodeProjectInteraction)
	if m.ports != nil {
		m.closePorts()
	}
	switchValue(&m.portUI, &old.portUI, &next.portUI)
	if m.folderSearch.project != nil || m.folderSearch.worker != nil {
		if old.folder == nil {
			old.folder = new(pencodeFolderSearch)
		}
		*old.folder = m.folderSearch
	}
	m.folderSearch = pencodeFolderSearch{}
	if next.folder != nil {
		m.folderSearch = *next.folder
		*next.folder = pencodeFolderSearch{}
		m.folderSearch.dirty = true
		clear(m.folderSearch.bindings[:])
	}
	m.findCountLabel = ""
	m.findNeedsFocus = false
	m.quickCount, m.quickTargetLine = -1, 0
	m.quickFocusEditor, m.quickNeedsFocus = false, m.quickOpen
	m.tabContext, m.inputContext, m.fileDrag = pencodeTabContext{}, pencodeInputContext{}, pencodeFileDrag{}
	m.resize, m.tooltip = pencodeWorkbenchResize{}, pencodeTooltip{}
	m.lastFileClick = 0
	m.dismissLanguageContext()
	m.cancelLanguageHover()
	m.cancelLanguageAutomatic()
	m.dismissLanguagePanel()
	m.viewProject = p
	m.switchActionContributions(&old.actions)
	m.fullRefresh = true
}

func (m *workspaceModel) releaseProjectView(p *pencodeProject) {
	group := m.projectActions(p)
	m.clearProjectExtensionUI(p, &group.ui, "")
	for id, session := range group.sessions {
		session.Close()
		delete(group.sessions, id)
	}
	if p == m.activeProjectState() {
		m.closeDebugger()
		m.closeFolderSearchWorker()
	} else {
		p.view.debug.close()
		if p.view.folder != nil {
			p.view.folder.closeWorker()
		}
	}
}

func (m *workspaceModel) closeProjectWorkers() {
	m.debug.close()
	m.closeFolderSearchWorker()
	for p := range m.actionGroups {
		if p == m.activeProjectState() {
			continue
		}
		view := m.storedView(p)
		view.debug.close()
		if view.folder != nil {
			view.folder.closeWorker()
			view.folder = nil
		}
	}
}
