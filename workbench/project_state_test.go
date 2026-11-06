package workbench

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/diiyw/onix/scene"
	engineui "github.com/diiyw/onix/ui"
)

func TestProjectStateRoundTrip(t *testing.T) {
	m, p := resourceTestModel(t)
	s := newPencodeTestSession(t, m.backend)
	m.session, p.historyKey = s, p.Path
	for _, name := range []string{"src/first.go", "second.go", "gone.go"} {
		resourceTestWrite(t, p, name, "package saved\n")
		openProjectFile(p, name)
	}
	p.ActivePath = "src/first.go"
	p.Files[p.ActivePath].editorView = pencodeEditorView{caret: 7, anchor: 2, scrollX: 12, scrollY: 420}
	p.Files["second.go"].editorID, p.Files["second.go"].editorSplit = "preview", true
	p.Files["second.go"].Preview, p.PreviewPath = true, "second.go"
	p.ExpandedDirs, p.Collapsed = map[string]bool{"src": false}, true
	p.tree.position = -1230
	p.sourceControl.Snapshot.Repository.Root = p.Path + "/nested"
	m.sidebarHidden, m.sidebarWidthStyle, m.sourceChangesWidthStyle = true, "flex-basis:300px;", "width:260px;"
	m.bottom.active, m.bottom.heightStyle = pencodePanelSourceControl, "height:320px;max-height:75%;"
	m.bottom.source, m.bottom.filter = m.outputStore().Channel("server"), "error"
	m.syncActiveProjectFields()
	// Store the user's collapsed tree after selection has already been synced.
	p.ExpandedDirs["src"], p.Collapsed = false, true
	m.saveProjectStates()
	if err := os.Remove(filepath.Join(p.Path, "gone.go")); err != nil {
		t.Fatal(err)
	}
	// Restore in another session, through the real workspace/document protocol.
	next := &workspaceModel{backend: m.backend, session: newSession(m.backend, "", s.stateDirectory), activeProjectIndex: -1}
	t.Cleanup(func() {
		for _, project := range next.projects {
			project.stopScan()
			project.stopWatch()
			for _, file := range project.Files {
				file.closeDocument()
			}
		}
	})
	if err := loadProjectAndWait(t, next, p.Path); err != nil {
		t.Fatal(err)
	}
	restored := next.activeProjectState()
	if !slices.Equal(restored.Tabs, []string{"src/first.go", "second.go"}) || restored.ActivePath != p.ActivePath || restored.restoring != nil {
		t.Fatalf("restored tabs=%v active=%q", restored.Tabs, restored.ActivePath)
	}
	if got := restored.Files[p.ActivePath]; got.editorView != p.Files[p.ActivePath].editorView || got.content() != "package saved\n" || got.Dirty {
		t.Fatalf("restored editor=%+v", got)
	}
	if restored.Files["second.go"].editorID != "preview" || !restored.Files["second.go"].editorSplit || restored.PreviewPath != "second.go" || !restored.Files["second.go"].Preview {
		t.Fatal("custom editor selection was lost")
	}
	if !restored.Collapsed || !reflect.DeepEqual(restored.ExpandedDirs, p.ExpandedDirs) || restored.tree.position != -1230 || !restored.tree.restore || next.revealProject != nil {
		t.Fatal("restoration expanded or recentered the saved tree")
	}
	if next.bottom.active != m.bottom.active || next.bottom.heightStyle != m.bottom.heightStyle || next.sidebarHidden != m.sidebarHidden || next.sidebarWidthStyle != m.sidebarWidthStyle || next.sourceChangesWidthStyle != m.sourceChangesWidthStyle || next.bottom.source.Name != "server" || next.bottom.filter != "error" {
		t.Fatal("panel layout was not restored")
	}
	if restored.sourceControl.Snapshot.Repository.Root != p.sourceControl.Snapshot.Repository.Root || restored.sourceControl.FollowPath != restored.ActivePath {
		t.Fatal("repository choice was not restored")
	}
}

func TestProjectStateSavesOnProjectAndSessionClose(t *testing.T) {
	for _, closeProject := range []bool{true, false} {
		t.Run(fmt.Sprintf("project=%v", closeProject), func(t *testing.T) {
			m, p := resourceTestModel(t)
			s := newPencodeTestSession(t, m.backend)
			m.session, p.historyKey = s, p.Path
			s.active = &Mod{model: m, session: s}
			resourceTestWrite(t, p, "main.go", "package saved\n")
			openProjectFile(p, "main.go")
			m.syncActiveProjectFields()
			if closeProject {
				m.closeProject(p)
			} else {
				s.Close()
			}
			data, err := os.ReadFile(s.projectStateFile(p.Path))
			if err != nil {
				t.Fatal(err)
			}
			var state pencodeProjectState
			if err := json.Unmarshal(data, &state); err != nil || state.ActivePath != "main.go" || len(state.Tabs) != 1 || state.Tabs[0].Path != "main.go" {
				t.Fatalf("close did not persist tabs: %+v (%v)", state, err)
			}
		})
	}
}

func TestProjectStateStorageIsolationAndCorruption(t *testing.T) {
	s := newPencodeTestSession(t, nil)
	local := s.projectStateFile("/same/path")
	remote := s.projectStateFile("ssh://user@host/same/path")
	other := s.projectStateFile("/other/path")
	if local == remote || local == other || local == newPencodeTestSession(t, nil).projectStateFile("/same/path") {
		t.Fatal("project state storage is not isolated")
	}
	if err := writeProjectState(local, &pencodeProjectState{Panel: pencodePanelOutput}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(local, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &pencodeProject{historyKey: "/same/path", Files: make(map[string]*openFile)}
	m := &workspaceModel{session: s, projects: []*pencodeProject{p}, bottom: pencodeBottomPanel{active: pencodePanelProblems}}
	m.restoreProjectState(p)
	if p.restoring != nil || p.tree.restore || m.bottom.active != pencodePanelProblems {
		t.Fatal("corrupt state changed the workspace")
	}
}

func TestProjectStatePendingRestorePreservesTabsAndNewSelection(t *testing.T) {
	m, p := resourceTestModel(t)
	m.session, p.historyKey = newPencodeTestSession(t, m.backend), p.Path
	for _, name := range []string{"first.go", "second.go"} {
		resourceTestWrite(t, p, name, "package saved\n")
	}
	state := &pencodeProjectState{Tabs: []pencodeTabState{{Path: "first.go"}, {Path: "second.go"}}, ActivePath: "first.go"}
	if err := writeProjectState(m.session.projectStateFile(p.historyKey), state); err != nil {
		t.Fatal(err)
	}
	m.restoreProjectState(p)
	// An immediate exit must retain the tabs whose document reads are pending.
	m.saveProjectStates()
	data, err := os.ReadFile(m.session.projectStateFile(p.historyKey))
	if err != nil {
		t.Fatal(err)
	}
	var saved pencodeProjectState
	if err := json.Unmarshal(data, &saved); err != nil || !reflect.DeepEqual(saved.Tabs, state.Tabs) || saved.ActivePath != state.ActivePath {
		t.Fatalf("pending restore was overwritten: %+v (%v)", saved, err)
	}
	if err := m.requestFileOpen(p, "second.go", false, false, nil); err != nil {
		t.Fatal(err)
	}
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	if p.ActivePath != "second.go" || !slices.Equal(p.Tabs, []string{"first.go", "second.go"}) {
		t.Fatalf("restore stole selection or duplicated tabs: %q %v", p.ActivePath, p.Tabs)
	}
}

func TestProjectStateTreeScrollWaitsForIndexAndLayout(t *testing.T) {
	app, session := newPencodeTestApp(t)
	m := session.active.model
	m.app.GetViewport = func() (int, int) { return 1440, 900 }
	app.World.InsertResource(scene.ViewportSize{Width: 1440, Height: 900, ScaleFactor: 1})
	for _, camera := range app.World.Query[scene.Camera]().All {
		camera.Mut().UpdateViewport(1440, 900)
	}
	p, world := m.activeProjectState(), m.fontResources
	p.stopScan()
	p.stopWatch()
	p.Entries = make([]treeEntry, 500)
	for i := range p.Entries {
		name := fmt.Sprintf("file-%04d.go", i)
		p.Entries[i] = treeEntry{Path: name, Name: name, Openable: true}
	}
	p.tree.revision++
	p.tree.position, p.tree.restore = -6000, true
	m.revealProject, m.revealPath = nil, ""
	p.scan = &pencodeProjectScan{cancel: func() {}}
	markPencodeRootDirtyByModel(world, m)
	languageInteractionStep(t, app)
	if !p.tree.restore || p.tree.position != -6000 {
		t.Fatal("scroll was restored before indexing completed")
	}
	p.stopScan()
	markPencodeRootDirtyByModel(world, m)
	for range 3 {
		languageInteractionStep(t, app)
	}
	scroll := world.Ref[scene.UIScroll](p.tree.area)
	if scroll == nil || p.tree.restore || scroll.Position.Y() != -6000 {
		t.Fatalf("saved tree position was not applied: %+v (pending=%v)", scroll, p.tree.restore)
	}
	if n := testing.AllocsPerRun(100, func() { m.syncTreeScroll(world) }); n != 0 {
		t.Fatalf("stable scroll tracking allocated %g", n)
	}
}

func TestProjectStateRestoresNativeEditorView(t *testing.T) {
	app, session := newPencodeTestApp(t)
	m := session.active.model
	root := t.TempDir()
	content := strings.Repeat(strings.Repeat("x", 256)+"\n", 150)
	if err := os.WriteFile(filepath.Join(root, "saved.go"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	view := pencodeEditorView{caret: 80, anchor: 5, scrollX: 100, scrollY: 600}
	state := &pencodeProjectState{ActivePath: "saved.go", Tabs: []pencodeTabState{{Path: "saved.go", Caret: view.caret, Anchor: view.anchor, ScrollX: view.scrollX, ScrollY: view.scrollY}}}
	if err := writeProjectState(session.projectStateFile(root), state); err != nil {
		t.Fatal(err)
	}
	if err := loadProjectAndWait(t, m, root); err != nil {
		t.Fatal(err)
	}
	markPencodeRootDirtyByModel(m.fontResources, m)
	for range 3 {
		languageInteractionStep(t, app)
	}
	field := m.fontResources.Ref[engineui.TextField](m.editor.control)
	textarea := m.fontResources.Ref[engineui.Textarea](m.editor.control)
	if field == nil || textarea == nil {
		t.Fatal("editor controls were not created")
	}
	got := pencodeEditorView{field.Caret, field.Anchor, textarea.ScrollX, textarea.ScrollY}
	if got != view {
		t.Fatalf("native editor view=%+v, want %+v", got, view)
	}
}

func TestProjectStateRepositorySurvivesDiscovery(t *testing.T) {
	r := sourceTestRepository(t)
	sourceTestWrite(t, r, "main.go", "package parent\n")
	sourceTestGit(t, r, "add", "main.go")
	sourceTestGit(t, r, "commit", "-m", "parent")
	sourceTestGit(t, r, "init", "-b", "main", "nested")
	nested := filepath.ToSlash(filepath.Join(r.Root, "nested"))
	if _, err := r.Backend.OpenWorkspace(t.Context(), nested); err != nil {
		t.Fatal(err)
	}
	sourceTestGit(t, sourceRepository{Root: nested, Backend: r.Backend}, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "child")
	session := newPencodeTestSession(t, r.Backend)
	state := &pencodeProjectState{ActivePath: "main.go", Repository: nested, Tabs: []pencodeTabState{{Path: "main.go"}}}
	if err := writeProjectState(session.projectStateFile(r.Root), state); err != nil {
		t.Fatal(err)
	}
	m := &workspaceModel{backend: r.Backend, session: session, activeProjectIndex: -1}
	if err := loadProjectAndWait(t, m, r.Root); err != nil {
		t.Fatal(err)
	}
	p := m.activeProjectState()
	t.Cleanup(func() { p.stopScan(); p.stopWatch(); p.sourceControl.stop() })
	s := m.sourceState()
	for range 2 {
		m.refreshSourceControl()
		select {
		case result := <-s.Done:
			s.Done <- result
		case <-time.After(10 * time.Second):
			t.Fatal("repository refresh timed out")
		}
		m.pollSourceResult(s, p)
		if len(s.Repositories) != 2 || s.Snapshot.Repository.Root != nested || m.followSourceRepository() {
			t.Fatalf("discovery overrode manual repository: roots=%v current=%q follow=%q", s.Repositories, s.Snapshot.Repository.Root, s.FollowPath)
		}
	}
}
