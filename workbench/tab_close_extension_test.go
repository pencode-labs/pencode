package workbench

import (
	"testing"

	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/scene"
	htmlui "github.com/onix-engine/onix/ui/html"
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
	if err := m.applyExtensionUI("codex-test", &extensions.ActionSession{}, peb.UI{
		ID: "new-chat", Title: "Codex 新会话", Kind: peb.EditorTab,
		View: &peb.EditorView{HTML: `<p>New conversation</p>`},
	}); err != nil {
		t.Fatal(err)
	}
	second := m.actions.ui.editor
	markPencodeRootDirtyByModel(world, m)
	languageInteractionStep(t, app)
	editorContextClick(t, app, m, state, "file-tab-close:"+second.rootID)
	languageInteractionStep(t, app)
	if m.actions.ui.editor != chat || !chat.live() {
		t.Fatal("closing the selected extension did not reveal the remaining chat")
	}
	for _, id := range []string{chat.rootID, "pencode-extension-reply", "active-tab"} {
		editorContextBounds(t, m, id)
	}
}

func TestClosingExtensionRevealsAdjacentEditorTab(t *testing.T) {
	for _, mode := range []string{"project", "standalone"} {
		for _, tc := range []struct {
			name                    string
			active, close, wantChat int
			files, comparison       bool
		}{
			{name: "right extension", active: 0, close: 0, wantChat: 1},
			{name: "left extension", active: 1, close: 1, wantChat: 0},
			{name: "extension beside files", active: 1, close: 1, wantChat: 0, files: true},
			{name: "background extension", active: 1, close: 0, wantChat: 1},
			{name: "right comparison", active: 1, close: 1, wantChat: -1, comparison: true},
			{name: "remaining file", active: 0, close: 0, wantChat: -1, files: true},
			{name: "last tab", active: 0, close: 0, wantChat: -1},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				m := &workspaceModel{}
				if tc.files {
					m.tabs, m.activePath = []string{"a.go", "b.go"}, "a.go"
					m.files = map[string]*openFile{"a.go": {Path: "a.go"}, "b.go": {Path: "b.go"}}
				}
				if mode == "project" {
					m.projects = []*pencodeProject{{Tabs: m.tabs, ActivePath: m.activePath, Files: m.files}}
					m.syncActiveProjectFields()
				}
				one := &pencodeExtensionSurface{model: m, session: &extensions.ActionSession{}, ui: peb.UI{Kind: peb.EditorTab}}
				two := &pencodeExtensionSurface{model: m, session: &extensions.ActionSession{}, ui: peb.UI{Kind: peb.EditorTab}}
				panel := &pencodeExtensionSurface{model: m, ui: peb.UI{Kind: peb.Panel}}
				chats := []*pencodeExtensionSurface{one, two}
				m.actions.ui.surfaces = []*pencodeExtensionSurface{one, panel}
				if tc.wantChat >= 0 || tc.comparison {
					m.actions.ui.surfaces = append(m.actions.ui.surfaces, two)
				}
				if tc.comparison {
					m.sourceState().Tabs = []*sourceTab{{Key: "diff", Kind: "changes"}}
				}
				m.selectExtensionTab(chats[tc.active])
				m.closeExtensionSurface(chats[tc.close], "button", true)
				if tc.wantChat >= 0 {
					if m.actions.ui.editor != chats[tc.wantChat] {
						t.Fatal("wrong extension selected after closing")
					}
				} else if m.actions.ui.editor != nil {
					t.Fatal("closed extension remains selected")
				}
				if tc.comparison && (m.sourceState().Active != "diff" || !m.sourceState().RestoreScroll) {
					t.Fatal("adjacent comparison was not selected")
				}
				if tc.files && tc.wantChat < 0 && m.activePath != "b.go" {
					t.Fatal("adjacent file was not selected")
				}
			})
		}
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
		"a.go": {Path: "a.go", Name: "a.go", contentSnapshot: snapshotText("unsaved"), Dirty: true},
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

func TestClosingComparisonSelectsAdjacentEditorTab(t *testing.T) {
	for _, mode := range []string{"project", "standalone"} {
		for _, tc := range []struct {
			name, active, close, wantSource, wantFile string
			comparisons                               int
			files, extension, selected, wantExtension bool
		}{
			{name: "right comparison", active: "one", close: "one", comparisons: 3, wantSource: "two"},
			{name: "middle comparison", active: "two", close: "two", comparisons: 3, wantSource: "three"},
			{name: "left comparison", active: "three", close: "three", comparisons: 3, wantSource: "two"},
			{name: "left extension", active: "one", close: "one", comparisons: 1, files: true, extension: true, wantExtension: true},
			{name: "left file", active: "one", close: "one", comparisons: 1, files: true, wantFile: "b.go"},
			{name: "background comparison", active: "two", close: "one", comparisons: 2, wantSource: "two"},
			{name: "selected extension", close: "one", comparisons: 1, extension: true, selected: true, wantExtension: true},
			{name: "selected file", close: "one", comparisons: 1, files: true, wantFile: "a.go"},
			{name: "last tab", active: "one", close: "one", comparisons: 1},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				m := &workspaceModel{}
				if tc.files {
					m.tabs, m.activePath = []string{"a.go", "b.go"}, "a.go"
					m.files = map[string]*openFile{"a.go": {Path: "a.go"}, "b.go": {Path: "b.go"}}
				}
				if mode == "project" {
					m.projects = []*pencodeProject{{Tabs: m.tabs, ActivePath: m.activePath, Files: m.files}}
					m.syncActiveProjectFields()
				}
				panel := &pencodeExtensionSurface{model: m, ui: peb.UI{Kind: peb.Panel}}
				chat := &pencodeExtensionSurface{model: m, ui: peb.UI{Kind: peb.EditorTab}}
				m.actions.ui.surfaces = []*pencodeExtensionSurface{panel}
				if tc.extension {
					m.actions.ui.surfaces = append(m.actions.ui.surfaces, chat)
				}
				if tc.selected {
					m.selectExtensionTab(chat)
				}
				s := m.sourceState()
				s.Tabs = []*sourceTab{{Key: "one", Kind: "changes"}, {Key: "two", Kind: "staged"}, {Key: "three", Kind: "history"}}
				s.Tabs, s.Active = s.Tabs[:tc.comparisons], tc.active
				m.sourceAction("sc-close-tab:" + tc.close)
				if len(s.Tabs) != tc.comparisons-1 || s.Active != tc.wantSource {
					t.Fatalf("remaining comparisons=%d, active=%q; want %d, %q", len(s.Tabs), s.Active, tc.comparisons-1, tc.wantSource)
				}
				if tc.wantSource != "" && tc.active == tc.close && !s.RestoreScroll {
					t.Fatal("adjacent comparison scroll was not restored")
				}
				if tc.wantExtension != (m.actions.ui.editor == chat) || !tc.wantExtension && m.actions.ui.editor != nil {
					t.Fatal("wrong extension selected after closing comparison")
				}
				if tc.wantFile != "" && m.activePath != tc.wantFile {
					t.Fatalf("selected file=%q, want %q", m.activePath, tc.wantFile)
				}
			})
		}
	}
}

func TestClosingDirtyComparisonRevealsExtensionAfterDiscard(t *testing.T) {
	for _, action := range []string{"button", "context"} {
		t.Run(action, func(t *testing.T) {
			m, p, chat := mixedEditorTabs(t)
			s := m.sourceState()
			s.Tabs = s.Tabs[:1]
			tab := s.Tabs[0]
			tab.Result, s.Active = "unsaved", tab.Key
			closeTab := func() {
				if action == "button" {
					m.sourceAction("sc-close-tab:" + tab.Key)
				} else {
					m.closeEditorTabs(pencodeCloseTab, pencodeEditorTab{project: p, source: tab})
				}
			}
			closeTab()
			if s.Prompt == "" || s.Active != tab.Key || m.actions.ui.editor != nil {
				t.Fatal("dirty comparison did not wait for the discard decision")
			}
			m.sourceAction("sc-prompt-cancel")
			if len(s.Tabs) != 1 || s.Active != tab.Key || m.actions.ui.editor != nil {
				t.Fatal("cancelling close changed the active tab")
			}
			closeTab()
			m.sourceAction("sc-prompt-confirm")
			if len(s.Tabs) != 0 || s.Active != "" || s.Prompt != "" || m.actions.ui.editor != chat {
				t.Fatal("discarding comparison did not activate the adjacent extension tab")
			}
		})
	}
}
