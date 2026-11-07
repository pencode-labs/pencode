package workbench

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/onix-engine/onix"
	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/scene"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func tabContextRightClick(t *testing.T, app *onix.App, m *workspaceModel, state *input.State, ref string) {
	t.Helper()
	r := editorContextBounds(t, m, ref)
	scroll := m.fontResources.Ref[scene.UIScroll](m.tabScroll.area)
	scale := float64(m.settings.Zoom) / 100
	x, y := float64(r.X+scroll.Position.X()+r.Width/2)*scale, float64(r.Y+r.Height/2)*scale
	state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseRight, X: x, Y: y})
	state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseRight, X: x, Y: y})
	languageInteractionStep(t, app)
	languageInteractionStep(t, app)
}

func TestTabContextAllEditorKinds(t *testing.T) {
	app, m, _, state := languageInteractionFixture(t)
	p, s := m.activeProjectState(), m.sourceState()
	s.Snapshot.Repository.Root = filepath.Join(p.Path, "nested")
	for _, kind := range []string{"changes", "staged", "history", "merge"} {
		s.Tabs = append(s.Tabs, &sourceTab{Key: kind, Path: "file.go", Kind: kind, Revision: "0123456789abcdef"})
	}
	if err := m.applyExtensionUI("test", &extensions.ActionSession{}, peb.UI{ID: "tool", Title: "Tool", Kind: peb.EditorTab, View: &peb.EditorView{HTML: `<p>tool</p>`}}); err != nil {
		t.Fatal(err)
	}
	surface := m.actions.ui.editor
	clipboard := ""
	state.SetClipboard(func(value string) bool { clipboard = value; return true }, func() (string, bool) { return clipboard, true })
	for _, zoom := range []int{100, 150, 70} {
		m.settings.Zoom = zoom
		for _, tab := range s.Tabs {
			m.actions.ui.editor = nil
			s.Active = tab.Key
			markPencodeRootDirtyByModel(m.fontResources, m)
			languageInteractionStep(t, app)
			tabContextRightClick(t, app, m, state, "sc-tab-group:"+tab.Key)
			if !m.tabContext.open || m.tabContext.source != tab || s.Active != tab.Key {
				t.Fatalf("%s tab not targeted at zoom %d: %+v", tab.Kind, zoom, m.tabContext)
			}
			local, _ := m.fontResources.Resource[*input.State]()
			if n := testing.AllocsPerRun(100, func() { m.updateTabContext(m.fontResources, local) }); n != 0 {
				t.Fatalf("idle comparison context allocated %g times", n)
			}
			editorContextClick(t, app, m, state, "tab-context-copy-path")
			if clipboard != filepath.Join(p.Path, "nested", "file.go") {
				t.Fatalf("comparison absolute path: %q", clipboard)
			}
			tabContextRightClick(t, app, m, state, "sc-tab-group:"+tab.Key)
			editorContextClick(t, app, m, state, "tab-context-copy-relative")
			if clipboard != "nested/file.go" {
				t.Fatalf("comparison relative path: %q", clipboard)
			}
		}
		m.selectExtensionTab(surface)
		markPencodeRootDirtyByModel(m.fontResources, m)
		languageInteractionStep(t, app)
		tabContextRightClick(t, app, m, state, "active-tab")
		if !m.tabContext.open || m.tabContext.surface != surface || m.tabContextEnabled(5) || m.tabContextEnabled(6) || !m.tabContextEnabled(2) {
			t.Fatalf("extension context has wrong target/actions: %+v", m.tabContext)
		}
		m.tabContext.open = false
	}
	// A background extension tab must not change the selected file.
	m.selectProjectTab(p, p.ActivePath)
	markPencodeRootDirtyByModel(m.fontResources, m)
	languageInteractionStep(t, app)
	tabContextRightClick(t, app, m, state, "file-tab:"+surface.rootID)
	if !m.tabContext.open || m.tabContext.surface != surface || m.actions.ui.editor != nil {
		t.Fatal("right-click selected the background extension")
	}
	editorContextClick(t, app, m, state, "tab-context-close")
	if slices.Contains(m.actions.ui.surfaces, surface) || len(s.Tabs) != 4 {
		t.Fatal("extension close affected comparisons")
	}
}

func mixedEditorTabs(t *testing.T) (*workspaceModel, *pencodeProject, *pencodeExtensionSurface) {
	t.Helper()
	p := &pencodeProject{Path: t.TempDir(), Tabs: []string{"a.go", "b.go"}, ActivePath: "a.go", Files: map[string]*openFile{
		"a.go": {Path: "a.go", Name: "a.go"}, "b.go": {Path: "b.go", Name: "b.go"},
	}}
	m := &workspaceModel{projects: []*pencodeProject{p}}
	m.syncActiveProjectFields()
	tool := &pencodeExtensionSurface{model: m, session: &extensions.ActionSession{}, ui: peb.UI{ID: "tool", Kind: peb.EditorTab}}
	panel := &pencodeExtensionSurface{model: m, ui: peb.UI{Kind: peb.Panel}}
	m.actions.ui.surfaces = []*pencodeExtensionSurface{panel, tool}
	p.sourceControl.Tabs = []*sourceTab{{Key: "diff", Kind: "changes"}, {Key: "merge", Kind: "merge"}}
	return m, p, tool
}

func TestMixedEditorTabCloseActions(t *testing.T) {
	for _, tc := range []struct {
		want   []string
		action pencodeCloseAction
	}{
		{action: pencodeCloseTab, want: []string{"a.go", "b.go", "diff", "merge"}},
		{action: pencodeCloseOtherTabs, want: []string{"tool"}},
		{action: pencodeCloseRightTabs, want: []string{"a.go", "b.go", "tool"}},
		{action: pencodeCloseSavedTabs, want: nil},
		{action: pencodeCloseAllTabs, want: nil},
	} {
		t.Run(pencodeTabContextActions[int(tc.action)-1].ref, func(t *testing.T) {
			m, _, tool := mixedEditorTabs(t)
			m.closeEditorTabs(tc.action, pencodeEditorTab{surface: tool})
			var got []string
			for tab := range m.editorTabs() {
				switch {
				case tab.surface != nil:
					got = append(got, tab.surface.ui.ID)
				case tab.source != nil:
					got = append(got, tab.source.Key)
				default:
					got = append(got, tab.path)
				}
			}
			if !slices.Equal(got, tc.want) || len(m.actions.ui.surfaces) == 0 || m.actions.ui.surfaces[0].ui.Kind != peb.Panel {
				t.Fatalf("remaining tabs=%v, want=%v; panel must remain", got, tc.want)
			}
		})
	}
}

func TestMixedEditorCloseWaitsForDirtyFileAndComparisonDecisions(t *testing.T) {
	m, p, _ := mixedEditorTabs(t)
	p.Files["a.go"].Dirty = true
	p.sourceControl.Tabs[0].Result = "unsaved"
	closeAll := func() { m.closeEditorTabs(pencodeCloseAllTabs, pencodeEditorTab{project: p, path: "a.go"}) }
	unchanged := func() {
		t.Helper()
		if len(p.Tabs) != 2 || len(p.sourceControl.Tabs) != 2 || len(m.actions.ui.surfaces) != 2 || !p.Files["a.go"].Dirty {
			t.Fatal("cancelled close changed tabs or dirty contents")
		}
	}
	closeAll()
	if m.closing.action != pencodeCloseAllTabs || len(m.closing.targets) != 1 {
		t.Fatal("dirty file was not prompted")
	}
	m.clearCloseRequest()
	unchanged()
	closeAll()
	m.resolveClose(false)
	if p.sourceControl.Prompt != "discard-tabs" {
		t.Fatal("dirty comparison was not prompted")
	}
	m.sourceAction("sc-prompt-cancel")
	unchanged()
	closeAll()
	m.resolveClose(false)
	m.sourceAction("sc-prompt-confirm")
	if len(p.Tabs) != 0 || len(p.sourceControl.Tabs) != 0 || len(m.actions.ui.surfaces) != 1 {
		t.Fatal("confirmed close did not remove all editor tabs")
	}
}

func TestExtensionTabCloseInterceptionForEveryUserAction(t *testing.T) {
	for _, action := range []string{"button", "shortcut", "context", "all"} {
		t.Run(action, func(t *testing.T) {
			app, m, _, state := languageInteractionFixture(t)
			if err := m.applyExtensionUI("test", &extensions.ActionSession{}, peb.UI{ID: "task", Title: "Task", Kind: peb.EditorTab, InterceptClose: true, Busy: true, View: &peb.EditorView{HTML: `<p>running</p>`}}); err != nil {
				t.Fatal(err)
			}
			s := m.actions.ui.editor
			markPencodeRootDirtyByModel(m.fontResources, m)
			languageInteractionStep(t, app)
			switch action {
			case "button":
				editorContextClick(t, app, m, state, "file-tab-close:"+s.rootID)
			case "shortcut":
				m.runShortcut(m.fontResources, pencodeShortcutClose)
			case "context":
				tabContextRightClick(t, app, m, state, "active-tab")
				editorContextClick(t, app, m, state, "tab-context-close")
			case "all":
				m.closeEditorTabs(pencodeCloseAllTabs, pencodeEditorTab{surface: s})
			}
			if !slices.Contains(m.actions.ui.surfaces, s) || m.actions.ui.editor != s {
				t.Fatal("tab closed before the extension accepted")
			}
			if err := m.applyExtensionUI("test", s.session, peb.UI{ID: "task", Close: true}); err != nil {
				t.Fatal(err)
			}
			if slices.Contains(m.actions.ui.surfaces, s) {
				t.Fatal("extension approval did not close the tab")
			}
		})
	}
}
