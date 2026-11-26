package workbench

import (
	"os"
	"path/filepath"
	"time"

	"github.com/onix-engine/onix/ecs"
	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/module"
	"github.com/onix-engine/onix/scene"
	engineui "github.com/onix-engine/onix/ui"
	"github.com/pencode-labs/pencode/actions"
	"github.com/pencode-labs/pencode/client"
	"github.com/pencode-labs/pencode/connection"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/output"
)

// Session keeps the foreground model available while preloaded views own their
// HTML, editor entities and resource readers independently.
type Session struct {
	externalJob       *fileIOJob
	updates           *pencodeUpdates
	quickHistory      *pencodeQuickHistory
	localBackend      *client.Client
	connection        *pencodeConnection
	active            *Mod
	extensions        *extensions.Host
	views             map[*Mod]struct{}
	market            *pencodeMarketplace
	backend           *client.Client
	recentProjects    *pencodeRecentProjects
	fonts             map[string]pencodeFont
	settingsWriters   map[*client.Client]*pencodeSettingsWriter
	window            *pencodeWindow
	focusOtherProject func(*Session, connection.Profile, string) bool
	openWindow        func([]string, connection.Profile) error
	remoteProjects    []string
	externalPaths     []string
	stateDirectory    string
	// Actions accepts native registrations before or after the session starts.
	Actions actions.Registry
	Output  output.Store
}

func NewSession(backend *client.Client) *Session {
	cacheDirectory := ""
	if home, err := os.UserHomeDir(); err == nil {
		cacheDirectory = filepath.Join(home, ".pencode", "cache")
	}
	configDirectory, _ := os.UserConfigDir()
	if configDirectory != "" {
		configDirectory = filepath.Join(configDirectory, "pencode")
	}
	return newSession(backend, cacheDirectory, configDirectory)
}

func newSession(backend *client.Client, cacheDirectory, configDirectory string) *Session {
	return &Session{backend: backend, localBackend: backend, views: make(map[*Mod]struct{}), fonts: make(map[string]pencodeFont), market: &pencodeMarketplace{}, quickHistory: loadPencodeQuickHistory(cacheDirectory), recentProjects: loadPencodeRecentProjects(configDirectory), stateDirectory: configDirectory}
}

func (s *Session) Close() {
	if s.window != nil {
		defer s.window.store.Release()
	}
	if s.active != nil {
		if s.window != nil {
			s.window.due = time.Now()
			s.checkpointWindow(s.active.model)
		}
		s.active.model.saveProjectStates()
	}
	s.closeSettingsWriters()
	if s.active != nil {
		s.active.model.closePorts()
		s.active.model.stopFileIO()
	}
	if s.connection != nil {
		s.connection.stop()
	}
	if s.backend != nil {
		_ = s.backend.Close()
	}
	if s.localBackend != nil {
		_ = s.localBackend.Close()
	}
}

// ScrollSystem synchronizes comparison panes after wheel/drag input and before
// HTML culling and native control layout consume their scroll positions.
func (s *Session) ScrollSystem() ecs.SystemConfig {
	return ecs.SystemConfig{
		Name: "sync_pencode_source_scroll", After: []string{"ui_scroll"}, Before: []string{"sync_ui"},
		System: func(*ecs.SystemContext) error {
			if s.active != nil && s.active.screen == pencodeWorkspace {
				model := s.active.model
				model.syncSourceScroll(model.fontResources)
				if model.syncSourceChangesScroll(model.fontResources) {
					// Advance the retained scroll paint window before replacing its
					// rows, so new controls inherit this frame's clipping geometry.
					if !model.fullRefresh {
						if err := model.fontResources.SyncHTML(); err != nil {
							return err
						}
					}
					model.refreshRegion(model.fontResources, pencodeRefreshSourcePanel)
				}
				model.syncTreeScroll(model.fontResources)
				model.syncCustomGrid(model.fontResources)
			}
			return nil
		},
	}
}

// LayoutSystem aligns decorations with the final native textarea scroll and
// HTML bounds in the same frame, including when the next frame is far away.
func (s *Session) LayoutSystem() ecs.SystemConfig {
	return ecs.SystemConfig{
		Name: "sync_pencode_editor_layout", After: []string{"sync_ui_text_input"},
		System: func(context *ecs.SystemContext) error {
			state, _ := context.World.GetResource[*input.State]()
			previousRevision := state.TextInputConfig().Revision
			changed := s.syncLayout(state)
			if s.active != nil && s.active.model.syncImagePreview(s.active.model.fontResources) {
				if err := s.active.model.fontResources.SyncHTML(); err != nil {
					return err
				}
				changed = true
			}
			if changed {
				// Extension scroll restoration and inline text changes run after
				// layout. Update native controls before this frame reaches paint.
				if err := engineui.SyncAfterLayout(context.World); err != nil {
					return err
				}
			}
			if s.active != nil {
				model := s.active.model
				world := model.fontResources
				// A focus change after the regular IME pass resets the candidate
				// rectangle. Publish the final caret before the window reads it.
				if config := state.TextInputConfig(); config.Active && (changed || config.Revision != previousRevision) {
					if changed {
						if err := scene.SyncUIGlobalTransformSystem("").System(context); err != nil {
							return err
						}
					}
					if err := engineui.SyncTextInputSystem("").System(context); err != nil {
						return err
					}
				}
				model.syncInputContextSelection(world)
				model.syncTerminalIME(world, state)
			}
			return nil
		},
	}
}

func (s *Session) syncLayout(state *input.State) (scrollChanged bool) {
	if s.active == nil {
		return
	}
	model := s.active.model
	defer s.checkpointWindow(model)
	defer s.active.icons.trimImages()
	model.focusQuickControls(model.fontResources, model.surface)
	model.focusProjectMenu(model.fontResources)
	model.focusConnectionPassword(model.fontResources)
	defer model.syncInputContext(model.fontResources)
	defer model.syncTooltip(model.fontResources, time.Now())
	model.syncProjectTabScroll(model.fontResources)
	if s.active.screen != pencodeWorkspace {
		return
	}
	model.bindSourceControls(model.fontResources)
	if model.syncSourceChangesScroll(model.fontResources) {
		model.refreshRegion(model.fontResources, pencodeRefreshSourcePanel)
	}
	model.bindTerminal(model.fontResources)
	scrollChanged = model.syncTerminalSelection(model.fontResources) || scrollChanged
	model.focusPorts(model.fontResources)
	model.syncTabScroll(model.fontResources)
	centered := model.centerRevealedTreeRow(model.fontResources)
	if model.syncTreeScroll(model.fontResources) || centered {
		if err := model.fontResources.SyncHTML(); err != nil {
			model.setActiveStatus(err.Error())
		}
		scrollChanged = true
	}
	model.syncFileDragPreview(model.fontResources)
	// HTML/native reconciliation may replace text spans after FrameUpdate.
	// Publish paint against the final field value, including asynchronous results.
	if field, ok := model.fontResources.Get[engineui.TextField](model.editor.control); ok {
		if value := model.fontResources.Mut[scene.Text2D](model.editor.text); value != nil {
			model.editor.syncHighlight(value, field, model)
		}
	}
	if doc := model.customDocuments[model.activeFile()]; doc != nil {
		scrollChanged = doc.draft.restoreFocused(model.fontResources) || scrollChanged
	}
	scrollChanged = model.syncExtensionUI(model.fontResources) || scrollChanged
	scrollChanged = model.actions.ui.selection.sync(model.fontResources, model) || scrollChanged
	scrollChanged = model.syncRenderedFind(model.fontResources) || scrollChanged
	scrollChanged = model.syncCustomGrid(model.fontResources) || scrollChanged
	model.focusFileContext(model.fontResources)
	model.focusFindInput(model.fontResources, model.surface)
	model.focusFolderSearch(model.fontResources)
	model.constrainFileContext(model.fontResources)
	model.focusLanguageControls(model.fontResources)
	model.restoreEditorFocus(model.fontResources, state)
	model.editor.syncCaretStatus(model.fontResources, model.surface, model)
	model.editor.syncGutter(model.fontResources, model.surface, model)
	model.editor.syncWidthGuide(model.fontResources, model.settings.EditorWidth, model.workbenchTheme.guide)
	model.editor.navigationPaint.sync(model.fontResources, model)
	model.editor.debugPaint.sync(model.fontResources, model)
	model.editor.diagnostics.sync(model.fontResources, model)
	model.editor.findPaint.sync(model.fontResources, model)
	model.constrainLanguagePopups(model.fontResources)
	model.editor.saveView(model.fontResources, model)
	return
}

func (s *Session) Register(registry *module.Registry) error {
	return registry.RegisterMod("resources/workbench", func() Mod { return Mod{session: s} }, module.Lifecycle[Mod]{
		Install:     func(c *module.Context, m *Mod) error { return m.Install(c) },
		Enter:       func(c *module.Context, m *Mod, v module.SceneStartup) error { return m.Enter(c, v) },
		Resume:      func(c *module.Context, m *Mod, v module.SceneStartup) error { return m.Resume(c, v) },
		FrameUpdate: func(c *module.Context, m *Mod, v module.Tick) error { return m.Update(c, v) },
		OnMessage:   func(c *module.Context, m *Mod, v module.Message) error { return m.OnMessage(c, v) },
		Destroy:     func(c *module.Context, m *Mod) error { return m.Destroy(c) },
	})
}
