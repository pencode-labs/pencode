package workbench

import (
	"net/url"
	"path/filepath"
	"slices"
	"testing"

	"github.com/diiyw/onix/input"
)

func TestSourceControlRepositoryPathOwnership(t *testing.T) {
	roots := []string{"/project", "/project/libs/one", "/project/libs/one/nested", "/project/libs/two"}
	for name, want := range map[string]string{
		"/project/main.go": "/project", "/project/libs/one/main.go": "/project/libs/one",
		"/project/libs/one/nested/main.go": "/project/libs/one/nested", "/project/libs/one-more/main.go": "/project",
		"/outside/main.go": "", "/project/libs/two/main.go": "/project/libs/two",
	} {
		if got := sourceRepositoryForPath(roots, name); got != want {
			t.Fatalf("%s belongs to %s, want %s", name, got, want)
		}
	}
	if got := sourceRepositoryForPath([]string{"C:/Project", "C:/Project/Sub"}, "c:/project/sub/main.go"); got != "C:/Project/Sub" {
		t.Fatal("Windows path matching", got)
	}
	if got := sourceRepositoryForPath([]string{"/"}, "/main.go"); got != "/" {
		t.Fatal("filesystem root matching", got)
	}
	if n := testing.AllocsPerRun(100, func() { sourceRepositoryForPath(roots, "/project/libs/one/nested/main.go") }); n != 0 {
		t.Fatalf("path matching allocated %g times", n)
	}
	m := &workspaceModel{projectPath: "/project"}
	m.bottom.active = pencodePanelSourceControl
	s := m.sourceState()
	s.Repositories, s.Snapshot.Repository.Root = roots, roots[0]
	render := func() {
		m.renderSourceToolbar(&s.toolsView)
		s.overlayView.reset()
		m.renderSourceRepositories(&s.overlayView)
	}
	render()
	if n := testing.AllocsPerRun(100, render); n != 0 {
		t.Fatalf("unchanged repository menus allocated %g times", n)
	}
}

func TestSourceControlRepositoryFailureRetainsDrafts(t *testing.T) {
	m := &workspaceModel{}
	s := m.sourceState()
	s.Snapshot.Repository.Root = "/project"
	dirty := &sourceTab{Key: "changes::main.go", Result: "edited", Saved: "original"}
	s.Tabs, s.CommitMessage = []*sourceTab{dirty}, "draft"
	s.Done = make(chan sourceResult, 1)
	s.Done <- sourceResult{RepositoryError: "repository unavailable"}
	m.pollSourceResult(s, nil)
	if !s.unsavedRepositories() || s.Snapshot.Repository.Root != "" || s.RepositoryError == "" {
		t.Fatal("repository failure lost edits or retained stale state")
	}
	s.selectRepository(sourceRepository{Root: "/project"})
	if !slices.Contains(s.Tabs, dirty) || s.CommitMessage != "draft" {
		t.Fatal("recovering the repository lost its drafts")
	}
	s.RepositoriesLoaded = true
	s.stop()
	if s.RepositoriesLoaded || !s.unsaved() {
		t.Fatal("reconnection must rediscover roots and retain edits")
	}
}

func TestSourceControlRepositoryMenuAndAutomaticSelection(t *testing.T) {
	r := sourceTestRepository(t)
	sourceTestWrite(t, r, "main.go", "package parent\n")
	sourceTestGit(t, r, "add", "main.go")
	sourceTestGit(t, r, "commit", "-m", "parent")
	child := sourceTestRepository(t)
	sourceTestWrite(t, child, "main.go", "package child\n")
	sourceTestGit(t, child, "add", "main.go")
	sourceTestGit(t, child, "commit", "-m", "child")
	sourceTestGit(t, r, "-c", "protocol.file.allow=always", "submodule", "add", child.Root, "libs/child")
	nestedRoot := filepath.ToSlash(filepath.Join(r.Root, "libs", "child"))
	parentRoot := filepath.ToSlash(r.Root)
	t.Setenv("ONIX_PENCODE_PROFILE_PROJECT", r.Root)
	t.Setenv("ONIX_PENCODE_PROFILE_FILE", "libs/child/main.go")
	app, m, _, state := languageInteractionFixture(t)
	m.languages = nil
	editorContextClick(t, app, m, state, "status-source-control")
	sourceWait(t, app, m)
	s := m.sourceState()
	if len(s.Repositories) != 2 || s.Snapshot.Repository.Root != nestedRoot || s.Snapshot.Commits[0].Subject != "child" {
		t.Fatalf("submodule selection: roots=%q, snapshot=%+v", s.Repositories, s.Snapshot)
	}
	// Check menu positioning, native click bindings, and keyboard selection.
	editorContextClick(t, app, m, state, "sc-repositories")
	menu := editorContextBounds(t, m, "sc-repository-menu")
	trigger := editorContextBounds(t, m, "sc-repositories")
	if menu.Width < 200 || menu.Y+menu.Height > trigger.Y+1 {
		t.Fatalf("repository menu bounds=%+v trigger=%+v", menu, trigger)
	}
	s.CommitMessage = "child draft"
	dirty := &sourceTab{Key: "changes::main.go", Path: "main.go", Kind: "changes", Result: "edited", Saved: "original"}
	s.Tabs = []*sourceTab{dirty}
	editorContextClick(t, app, m, state, "sc-repository:"+parentRoot)
	sourceWait(t, app, m)
	if s.Snapshot.Repository.Root != parentRoot || s.CommitMessage != "" || !s.unsavedRepositories() || s.unsaved() {
		t.Fatal("manual selection lost a nested draft or reused its commit message")
	}
	if m.followSourceRepository() {
		t.Fatal("unchanged active file overrode a manual repository selection")
	}
	view := s.RepositoryViews[nestedRoot]
	view.Active = dirty.Key
	s.RepositoryViews[nestedRoot] = view
	s.CommitMessage = "parent draft"
	// Stage through the selected root; the same relative file exists in both.
	sourceTestWrite(t, r, "main.go", "package changed\n")
	m.refreshSourceControl()
	sourceWait(t, app, m)
	m.sourceAction("sc-stage:" + url.PathEscape("main.go"))
	sourceWait(t, app, m)
	if len(s.Snapshot.Groups[1]) == 0 {
		t.Fatal("staging did not operate on the selected repository")
	}
	project := m.activeProjectState()
	// A file change resumes automatic selection, including when the panel is closed.
	m.bottom.active = pencodePanelClosed
	project.ActivePath = "main.go"
	m.syncActiveProjectFields()
	m.followSourceRepository()
	project.ActivePath = "libs/child/main.go"
	m.syncActiveProjectFields()
	if !m.followSourceRepository() {
		t.Fatal("active submodule file did not select its repository")
	}
	sourceWait(t, app, m)
	if s.Snapshot.Repository.Root != nestedRoot || s.CommitMessage != "child draft" || !slices.Contains(s.Tabs, dirty) {
		t.Fatal("automatic selection failed to restore the submodule's drafts")
	}
	if s.visible() {
		t.Fatal("automatic repository selection replaced the active file with an older comparison")
	}
	file := project.Files["libs/child/main.go"]
	file.Dirty = true
	if !m.sourceHasDrafts() {
		t.Fatal("nested repository failed to detect its editor's unsaved changes")
	}
	file.Dirty = false
	if allocations := testing.AllocsPerRun(100, func() { m.followSourceRepository() }); allocations != 0 {
		t.Fatalf("unchanged active file allocated %g times", allocations)
	}
	m.bottom.active = pencodePanelSourceControl
	m.sourceChanged()
	languageInteractionStep(t, app)
	editorContextClick(t, app, m, state, "sc-repositories")
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyHome})
	languageInteractionStep(t, app)
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyHome})
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyEnter})
	languageInteractionStep(t, app)
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyEnter})
	sourceWait(t, app, m)
	if s.RepositoryMenuOpen || s.Snapshot.Repository.Root != parentRoot || s.CommitMessage != "parent draft" {
		t.Fatal("keyboard selection did not restore the parent repository")
	}
}
