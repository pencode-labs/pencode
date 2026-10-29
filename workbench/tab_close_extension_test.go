package workbench

import (
	"testing"

	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/scene"
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func TestClosingFileRevealsExtensionEditorTab(t *testing.T) {
	app, session := newPencodeTestApp(t)
	m := session.active.model
	world := m.fontResources
	m.app.GetViewport = func() (int, int) { return 1440, 900 }
	app.World.InsertResource(scene.ViewportSize{Width: 1440, Height: 900, ScaleFactor: 1})
	for _, camera := range app.World.Query[scene.Camera]().All {
		camera.Mut().UpdateViewport(1440, 900)
	}
	project, path := m.activeProjectState(), m.activePath
	if err := m.applyExtensionUI("codex-test", &extensions.ActionSession{}, peb.UI{
		ID: "chat", Title: "Codex", Kind: peb.EditorTab,
		View: &peb.EditorView{HTML: `<p id="pencode-extension-reply">Retained Codex reply</p>`},
	}); err != nil {
		t.Fatal(err)
	}
	chat := m.actions.ui.editor
	m.selectProjectTab(project, path)
	markPencodeRootDirtyByModel(world, m)
	languageInteractionStep(t, app)
	state, _ := app.World.GetResource[*input.State]()
	editorContextClick(t, app, m, state, "file-tab-close:"+project.Path+":"+path)
	languageInteractionStep(t, app)
	if m.actions.ui.editor != chat || !chat.live() || m.activeFile() != nil {
		t.Fatalf("closing the file did not activate the remaining Codex tab: editor=%p chat=%p tabs=%v active=%q closing=%+v", m.actions.ui.editor, chat, project.Tabs, project.ActivePath, m.closing)
	}
	if len(project.Tabs) != 0 || len(project.Files) != 0 {
		t.Fatal("closed file was retained")
	}
	for _, id := range []string{chat.rootID, "pencode-extension-reply"} {
		editorContextBounds(t, m, id)
	}
	var active int
	for _, access := range world.Query[htmlui.Component[tabProps, struct{}]]() {
		props := access.Get().Props
		if props.Active {
			active++
			if props.Surface != chat {
				t.Fatal("wrong tab is highlighted")
			}
		}
	}
	if active != 1 {
		t.Fatalf("got %d active tabs, want 1", active)
	}
}

func TestClosingFileSelectsAdjacentTab(t *testing.T) {
	for _, mode := range []string{"project", "standalone"} {
		for _, tc := range []struct {
			name, active, close, wantFile string
			selected, wantChat            int
			comparison                    bool
		}{
			{name: "right extension", active: "b.go", close: "b.go", wantChat: 1},
			{name: "right file", active: "a.go", close: "a.go", wantFile: "b.go"},
			{name: "background file", active: "a.go", close: "b.go", wantFile: "a.go"},
			{name: "selected extension", active: "b.go", close: "b.go", selected: 2, wantChat: 2},
			{name: "selected comparison", active: "b.go", close: "b.go", comparison: true, wantFile: "a.go"},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				p := &pencodeProject{Path: t.TempDir(), Tabs: []string{"a.go", "b.go"}, ActivePath: tc.active, Files: map[string]*openFile{
					"a.go": {Path: "a.go", Name: "a.go"},
					"b.go": {Path: "b.go", Name: "b.go"},
				}}
				m := &workspaceModel{files: p.Files, tabs: p.Tabs, activePath: p.ActivePath}
				if mode == "project" {
					m.projects = []*pencodeProject{p}
					m.syncActiveProjectFields()
				}
				panel := &pencodeExtensionSurface{model: m, ui: peb.UI{Kind: peb.Panel}}
				one := &pencodeExtensionSurface{model: m, ui: peb.UI{Kind: peb.EditorTab}}
				two := &pencodeExtensionSurface{model: m, ui: peb.UI{Kind: peb.EditorTab}}
				m.actions.ui.surfaces = []*pencodeExtensionSurface{panel, one, two}
				if tc.selected == 2 {
					m.selectExtensionTab(two)
				}
				if tc.comparison {
					m.sourceState().Active = "comparison"
				}
				m.closeTab(tc.close)
				want := []*pencodeExtensionSurface{nil, one, two}[tc.wantChat]
				if m.actions.ui.editor != want {
					t.Fatalf("selected extension=%p, want %p", m.actions.ui.editor, want)
				}
				if tc.wantFile != "" && m.activePath != tc.wantFile {
					t.Fatalf("selected file=%q, want %q", m.activePath, tc.wantFile)
				}
				if tc.comparison && m.sourceState().Active != "comparison" {
					t.Fatal("closing a background file displaced the comparison")
				}
			})
		}
	}
}

func TestClosingDirtyFileRevealsExtensionAfterDiscard(t *testing.T) {
	p := &pencodeProject{Path: t.TempDir(), Tabs: []string{"a.go"}, ActivePath: "a.go", Files: map[string]*openFile{
		"a.go": {Path: "a.go", Name: "a.go", Content: "unsaved", Dirty: true},
	}}
	m := &workspaceModel{projects: []*pencodeProject{p}}
	m.syncActiveProjectFields()
	chat := &pencodeExtensionSurface{model: m, ui: peb.UI{Kind: peb.EditorTab}}
	m.actions.ui.surfaces = []*pencodeExtensionSurface{chat}
	m.closeTab("a.go")
	if m.closing.action != pencodeCloseTab || m.actions.ui.editor != nil {
		t.Fatal("dirty file did not wait for the save decision")
	}
	m.clearCloseRequest()
	if m.activeFile() != p.Files["a.go"] || m.actions.ui.editor != nil {
		t.Fatal("cancelling close changed the active tab")
	}
	m.closeTab("a.go")
	m.resolveClose(false)
	if m.actions.ui.editor != chat || len(p.Files) != 0 || m.closing.action != pencodeCloseNone {
		t.Fatal("discarding changes did not activate the remaining extension tab")
	}
}
