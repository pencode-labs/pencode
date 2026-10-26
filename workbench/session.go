package workbench

import (
	"os"
	"path/filepath"

	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
	engineui "github.com/diiyw/onix/ui"
	"github.com/pencode-labs/pencode/actions"
	"github.com/pencode-labs/pencode/client"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/output"
)

// Session keeps the foreground model available while preloaded views own their
// HTML, editor entities and resource readers independently.
type Session struct {
	backend      *client.Client
	localBackend *client.Client
	connection   *pencodeConnection
	// Actions accepts native registrations before or after the session starts.
	Actions         actions.Registry
	Output          output.Store
	active          *Mod
	extensions      *extensions.Host
	views           map[*Mod]struct{}
	market          *pencodeMarketplace
	quickHistory    *pencodeQuickHistory
	fonts           map[string]pencodeFont
	settingsWriters map[*client.Client]*pencodeSettingsWriter
}

func NewSession(backend *client.Client) *Session {
	cacheDirectory := ""
	if home, err := os.UserHomeDir(); err == nil {
		cacheDirectory = filepath.Join(home, ".pencode", "cache")
	}
	return &Session{backend: backend, localBackend: backend, views: make(map[*Mod]struct{}), fonts: make(map[string]pencodeFont), market: &pencodeMarketplace{}, quickHistory: loadPencodeQuickHistory(cacheDirectory)}
}

func (s *Session) Close() {
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
				model.syncTreeScroll(model.fontResources)
			}
			return nil
		},
	}
}

// LayoutSystem aligns decorations with the final native textarea scroll and
// HTML bounds in the same frame, including when the next frame is far away.
func (s *Session) LayoutSystem() ecs.SystemConfig {
	return ecs.SystemConfig{
		Name: "sync_pencode_editor_layout", After: []string{"sync_ui_global_transform_after_layout"},
		System: func(*ecs.SystemContext) error { s.syncLayout(); return nil },
	}
}

func (s *Session) syncLayout() {
	if s.active == nil {
		return
	}
	model := s.active.model
	model.focusQuickControls(model.fontResources, model.surface)
	model.focusConnectionPassword(model.fontResources)
	defer model.syncInputContext(model.fontResources)
	if s.active.screen != pencodeWorkspace {
		return
	}
	model.bindSourceControls(model.fontResources)
	model.bindTerminal(model.fontResources)
	model.focusPorts(model.fontResources)
	model.syncTabScroll(model.fontResources)
	model.centerRevealedTreeRow(model.fontResources)
	model.syncTreeScroll(model.fontResources)
	// HTML/native reconciliation may replace text spans after FrameUpdate.
	// Publish paint against the final field value, including asynchronous results.
	if field, ok := module.ContentGet[engineui.TextField](model.fontResources, model.editor.control); ok {
		if value := module.ContentMut[scene.Text2D](model.fontResources, model.editor.text); value != nil {
			model.editor.syncHighlight(value, field, model)
		}
	}
	model.syncExtensionUI(model.fontResources)
	model.focusFileContext(model.fontResources)
	model.focusFindInput(model.fontResources, model.surface)
	model.focusFolderSearch(model.fontResources)
	model.constrainFileContext(model.fontResources)
	model.focusLanguageControls(model.fontResources)
	model.focusPanelEditor(model.fontResources)
	model.editor.syncCaretStatus(model.fontResources, model.surface, model)
	model.editor.syncGutter(model.fontResources, model.surface, model)
	model.editor.debugPaint.sync(model.fontResources, model)
	model.editor.diagnostics.sync(model.fontResources, model)
	model.editor.findPaint.sync(model.fontResources, model)
	model.constrainLanguagePopups(model.fontResources)
	model.editor.saveView(model.fontResources, model)
}

func (s *Session) Register(registry *module.Registry) error {
	return module.RegisterMod(registry, "resources/workbench", func() Mod { return Mod{session: s} }, module.Lifecycle[Mod]{
		Install:     func(c *module.Context, m *Mod) error { return m.Install(c) },
		Enter:       func(c *module.Context, m *Mod, v module.SceneStartup) error { return m.Enter(c, v) },
		Resume:      func(c *module.Context, m *Mod, v module.SceneStartup) error { return m.Resume(c, v) },
		FrameUpdate: func(c *module.Context, m *Mod, v module.Tick) error { return m.Update(c, v) },
		OnMessage:   func(c *module.Context, m *Mod, v module.Message) error { return m.OnMessage(c, v) },
		Destroy:     func(c *module.Context, m *Mod) error { return m.Destroy(c) },
	})
}
