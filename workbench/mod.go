package workbench

import (
	"time"

	"github.com/diiyw/onix/graphics"
	"github.com/diiyw/onix/module"
	html "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/peb"
)

type Mod struct {
	model                 *workspaceModel
	session               *Session
	screen                pencodeScreen
	activating, switching bool
	icons                 *pencodeIconProvider
	installed             bool
}

func (m *Mod) Install(c *module.Context) error {
	content := c.Content()
	slot, err := c.Slot("workspace")
	if err != nil {
		return err
	}
	isWorkspace, err := module.ReadSlot[bool](slot)
	if err != nil {
		return err
	}
	pageName := "launcher.html"
	if isWorkspace {
		m.screen, pageName = pencodeWorkspace, "workspace.html"
	}
	page, err := content.ReadFile("assets/ui/" + pageName)
	if err != nil {
		return err
	}
	css, err := content.ReadFile("assets/ui/pencode.css")
	if err != nil {
		return err
	}
	model := &workspaceModel{app: c.Host(), fontResources: content, lastDirectory: defaultDirectory(), screen: m.screen, files: make(map[string]*openFile), activeProjectIndex: -1}
	model.session, model.market = m.session, m.session.market
	model.backend = m.session.backend
	model.output = &m.session.Output
	model.output.Channel("pencode")
	model.fonts = m.session.fonts
	m.model = model
	model.settings, model.settingsPath = loadPencodeSettings()
	model.themeSourceCSS = string(css)
	if err := model.loadSyntaxSettings(); err != nil {
		return err
	}
	if err := model.initializeFonts(); err != nil {
		return err
	}
	model.rebuildFontOptions()
	model.refreshSettingsLabels()
	definitions, err := newPencodeDefinitions(content, string(page))
	if err != nil {
		return err
	}
	uiFont, uiBold := model.uiFont()
	model.surface = spawnPencodeSurface(content, uiFont, uiBold, model.settings.UIFontSize)
	model.windowBorder = spawnPencodeWindowBorder(content, model.workbenchTheme.border)
	m.icons = &pencodeIconProvider{content: content, model: model, textures: make(map[string]*graphics.Texture)}
	if surface := module.ContentMut[html.Surface](content, model.surface); surface != nil {
		surface.Images = &html.Images{Provider: m.icons}
	}
	model.root, err = module.ContentMount(content, model.surface, definitions.root, appProps{Model: model, Screen: m.screen}, appState{}, 1)
	if err != nil {
		return err
	}
	seedPencodeViewport(content, c.Host())
	model.syncZoom(content)
	if err := content.SyncHTML(); err != nil {
		return err
	}
	setPencodeEditorValue(content, model.surface, model.activeContent())
	if err := content.SyncUI(); err != nil {
		return err
	}
	m.session.views[m] = struct{}{}
	m.installed = true
	return nil
}

func (m *Mod) Enter(c *module.Context, _ module.SceneStartup) error {
	if previous := m.session.active; previous != nil && previous != m {
		// Domain data follows the foreground. Entities and rendering caches stay
		// with their owning view, including the retained workspace editor.
		previous.model.closeCustomEditors()
		previous.model.releaseSyntaxWorker(true)
		local := *m.model
		*m.model = *previous.model
		// Surface callbacks must mutate the foreground list, not a stale copy
		// whose backing array is shared with the active model.
		previous.model.actions.ui = pencodeExtensionUIState{}
		for _, surface := range m.model.actions.ui.surfaces {
			surface.model = m.model
			surface.needsFocus = surface.ui.Kind != peb.Notification && surface.ui.Kind != peb.EditorTab
		}
		m.model.root, m.model.surface, m.model.fontResources = local.root, local.surface, c.Content()
		m.model.windowBorder = local.windowBorder
		m.model.editor = local.editor
		m.model.terminalView = local.terminalView
		m.model.customDocuments = local.customDocuments
		m.model.highlightScratch, m.model.highlightRebaseScratch = local.highlightScratch, local.highlightRebaseScratch
		m.model.highlightWorker = local.highlightWorker
		m.model.highlightRevision = local.highlightRevision
		m.model.lastFileClick = 0
		m.model.inputContext = pencodeInputContext{}
		m.model.tooltip = pencodeTooltip{}
		m.model.resetHighlight()
	}
	m.model.screen = m.screen
	m.model.applySettingsToView()
	m.model.pollProjectScans()
	m.activating, m.switching = true, false
	markPencodeRootDirtyByModel(c.Content(), m.model)
	m.model.syncZoom(c.Content())
	if err := c.Content().SyncHTML(); err != nil {
		return err
	}
	setPencodeEditorValue(c.Content(), m.model.surface, m.model.activeContent())
	if err := c.Content().SyncUI(); err != nil {
		return err
	}
	m.session.active = m
	m.session.Actions.SetWake(m.model.backgroundWake())
	m.session.Output.SetWake(m.model.backgroundWake())
	m.session.startExtensions(m.model)
	m.session.watchConnection(m.model.backgroundWake())
	return nil
}
func (m *Mod) Resume(c *module.Context, v module.SceneStartup) error { return m.Enter(c, v) }

func (m *Mod) Update(c *module.Context, _ module.Tick) error {
	if m.model.pollSettingsWrites() {
		markPencodeRootDirtyByModel(c.Content(), m.model)
	}
	if m.model.pollConnection() {
		markPencodeRootDirtyByModel(c.Content(), m.model)
	}
	if m.model.pollPorts() {
		markPencodeRootDirtyByModel(c.Content(), m.model)
	}
	previousFile := m.model.activeFile()
	previousContent := ""
	if previousFile != nil {
		previousContent = previousFile.Content
	}
	if m.model.pollFileIO() {
		if previousFile != m.model.activeFile() || previousContent != m.model.activeContent() {
			setPencodeEditorValue(c.Content(), m.model.surface, m.model.activeContent())
		}
		markPencodeRootDirtyByModel(c.Content(), m.model)
	}
	m.model.releaseIdleSyntaxMemory(time.Now())
	terminalCount, terminalActive := 0, (*pencodeTerminal)(nil)
	panel, status := m.model.bottom.active, m.model.status
	if m.model.terminals != nil {
		terminalCount, terminalActive = len(m.model.terminals.items), m.model.terminals.active
	}
	if m.model.pollTerminals() {
		if m.model.terminals == nil || len(m.model.terminals.items) != terminalCount || m.model.terminals.active != terminalActive || m.model.bottom.active != panel || m.model.status != status {
			markPencodeRootDirtyByModel(c.Content(), m.model)
		} else {
			m.model.refreshRegion(c.Content(), pencodeRefreshTerminal)
		}
	}
	if m.model.pollOutput() {
		m.model.refreshRegion(c.Content(), pencodeRefreshOutput)
	}
	if m.model.pollActions() {
		markPencodeRootDirtyByModel(c.Content(), m.model)
	}
	if m.model.expireExtensionNotifications(time.Now()) {
		markPencodeRootDirtyByModel(c.Content(), m.model)
	}
	if m.model.pollMarketplace() {
		markPencodeRootDirtyByModel(c.Content(), m.model)
	}
	if m.model.pollExtensions(m.session.extensions) {
		markPencodeRootDirtyByModel(c.Content(), m.model)
	}
	if m.model.pollLanguageServers(c.Content()) {
		markPencodeRootDirtyByModel(c.Content(), m.model)
	}
	if m.model.pollDebugger(c.Content()) {
		markPencodeRootDirtyByModel(c.Content(), m.model)
	}
	if m.model.pollCustomEditors() {
		markPencodeRootDirtyByModel(c.Content(), m.model)
	}
	if m.activating {
		m.activating = false
		configurePencodeWindow(c.Host(), m.model)
		if host := c.Host(); host != nil && host.OnCloseRequested != nil {
			host.OnCloseRequested(func() bool {
				if m.model.requestWindowClose() {
					return true
				}
				markPencodeRootDirtyByModel(c.Content(), m.model)
				return false
			})
		}
	}
	if m.model.screen != m.screen {
		if m.switching {
			return nil
		}
		m.switching = true
		return module.Send(c, module.SceneTarget, "pencode.navigate", m.model.screen == pencodeWorkspace)
	}
	activePath := m.model.activePath
	treeChanged := m.model.pollProjectScans()
	treeChanged = m.model.pollProjectWatches() || treeChanged
	quickChanged := m.model.pollQuickMatches()
	folderChanged := m.model.pollFolderSearch()
	hoverChanged := m.model.pollHoverMarkup()
	referenceChanged := m.model.pollReferencePreviews()
	if treeChanged || quickChanged || folderChanged || hoverChanged || referenceChanged {
		if activePath != m.model.activePath {
			setPencodeEditorValue(c.Content(), m.model.surface, m.model.activeContent())
		}
		markPencodeRootDirtyByModel(c.Content(), m.model)
	}
	ctx := module.ContentSystemContext{World: c.Content()}
	if err := updatePencodeShortcuts(&ctx, m.model); err != nil {
		return err
	}
	if m.screen == pencodeLauncher {
		return nil
	}
	if err := updatePencodeHighlight(&ctx, m.model); err != nil {
		return err
	}
	m.model.editor.syncText(c.Content(), m.model.surface, m.model)
	m.model.bindSourceDiffControls(c.Content())
	return nil
}
func (m *Mod) OnOpenProject(c *module.Context, value string) error {
	if value == "" {
		return nil
	}
	if err := m.model.loadProject(value); err != nil {
		return err
	}
	markPencodeRootDirtyByModel(c.Content(), m.model)
	return nil
}
func (m *Mod) OnMessage(c *module.Context, message module.Message) error {
	if message.Topic == "openProject" {
		value, ok := module.Value[string](message)
		if !ok {
			return module.ErrType
		}
		return m.OnOpenProject(c, value)
	}
	if message.Topic == "pencode.error" {
		value, ok := module.Value[string](message)
		if !ok {
			return module.ErrType
		}
		m.switching, m.model.screen, m.model.status = false, m.screen, value
		markPencodeRootDirtyByModel(c.Content(), m.model)
	}
	return nil
}
func (m *Mod) Destroy(c *module.Context) error {
	if m.installed {
		m.installed = false
		delete(m.session.views, m)
		if len(m.session.views) == 0 {
			m.model.closeDebugger()
			m.model.closePorts()
			m.session.closeSettingsWriters()
			m.model.stopFileIO()
			if m.session.connection != nil {
				m.session.Close()
			}
			if m.model.highlightCache != nil {
				*m.model.highlightCache = pencodeHighlightCache{}
			}
			for _, project := range m.model.projects {
				project.stopWatch()
				project.sourceControl.stop()
				for _, file := range project.Files {
					file.closeDocument()
				}
			}
			m.model.sourceControl.stop()
			m.model.closeTerminal("")
			m.model.closeActions()
			m.model.closeLanguageServers()
			m.session.market.stop()
			m.session.extensions.Close()
			m.session.extensions = nil
			defer m.model.disposeFonts()
		}
	}
	if m.session.active == m {
		for _, project := range m.model.projects {
			project.stopScan()
		}
	}
	if m.model != nil {
		m.model.closeCustomEditors()
		c.Content().Unmount(m.model.root)
		m.model.editor.dispose(c.Content())
		m.model.closeHighlightWorker()
		m.model.closeQuickWorker()
		m.model.closeFolderSearchWorker()
		m.model.closeHoverWorker()
		m.model.closeReferenceWorker()
	}
	if m.icons != nil {
		for _, texture := range m.icons.textures {
			if texture != nil {
				_ = texture.Dispose()
			}
		}
		clear(m.icons.textures)
	}
	if m.session.active == m {
		m.session.active = nil
	}
	return nil
}
