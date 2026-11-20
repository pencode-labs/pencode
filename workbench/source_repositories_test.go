package workbench

import (
	"errors"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/onix-engine/onix/input"
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
	s.RepositoryRefs = []string{"main", "feature/one", "12345678", "topic"}
	s.Snapshot.Branch = "main"
	render := func() {
		m.renderSourceControl(&s.panelView)
		m.renderSourceToolbar(&s.toolsView)
	}
	render()
	if n := testing.AllocsPerRun(100, render); n != 0 {
		t.Fatalf("unchanged repository list allocated %g times", n)
	}
}

func TestSourceControlRepositoryHeadLabels(t *testing.T) {
	r := sourceTestRepository(t)
	if got := r.headLabel(); got != "main" {
		t.Fatalf("unborn branch label = %q", got)
	}
	sourceTestGit(t, r, "commit", "--allow-empty", "-m", "base")
	sourceTestGit(t, r, "switch", "-c", "feature/nested")
	if got := r.headLabel(); got != "feature/nested" {
		t.Fatalf("branch label = %q", got)
	}
	hash := sourceTestGit(t, r, "rev-parse", "--short=8", "HEAD")
	sourceTestGit(t, r, "switch", "--detach", "HEAD")
	if got := r.headLabel(); got != hash {
		t.Fatalf("detached label = %q, want %q", got, hash)
	}
}

func TestSourceControlRepositoryRefCache(t *testing.T) {
	m := &workspaceModel{projectPath: "/project"}
	s := m.sourceState()
	s.Repositories = []string{"/project", "/project/libs/child"}
	s.RepositoryRefs = []string{"main", "12345678"}
	s.Snapshot.Repository.Root, s.Snapshot.Branch = s.Repositories[0], "main"
	check := func(want ...string) {
		t.Helper()
		m.bottom.active = pencodePanelSourceControl
		m.renderSourceControl(&s.panelView)
		for i, root := range s.Repositories {
			found := false
			for _, group := range s.repositoryView.groups {
				if group.node.Key == sourceVNodeKey("div", root) {
					if len(group.children) == 2 && len(group.children[1].Children) == 1 {
						content := group.children[1].Children[0].Children
						found = len(content) == 2 && content[1].Text == want[i]
					}
					break
				}
			}
			if !found {
				t.Fatalf("repository %s did not render ref %q", root, want[i])
			}
		}
	}
	check("main", "12345678")
	s.RepositoryRefs = []string{"main", "feature/child"}
	check("main", "feature/child")
	s.Snapshot.Branch = "abcdef01 (detached)"
	check("abcdef01 (detached)", "feature/child")
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

func TestSourceControlRepositoryListAndAutomaticSelection(t *testing.T) {
	r := sourceTestRepository(t)
	sourceTestWrite(t, r, "main.go", "package parent\n")
	sourceTestGit(t, r, "add", "main.go")
	sourceTestGit(t, r, "commit", "-m", "parent")
	sourceTestGit(t, r, "branch", "topic")
	const remoteRef = "refs/remotes/origin/topic"
	sourceTestGit(t, r, "update-ref", remoteRef, "HEAD")
	child := sourceTestRepository(t)
	sourceTestWrite(t, child, "main.go", "package child\n")
	sourceTestGit(t, child, "add", "main.go")
	sourceTestGit(t, child, "commit", "-m", "child")
	sourceTestGit(t, r, "-c", "protocol.file.allow=always", "submodule", "add", child.Root, "libs/child")
	nestedRoot := filepath.ToSlash(filepath.Join(r.Root, "libs", "child"))
	parentRoot := filepath.ToSlash(r.Root)
	nested := sourceRepository{Root: nestedRoot, Backend: r.Backend}
	if _, err := r.Backend.GitRepositories(t.Context(), r.Root); err != nil {
		t.Fatal(err)
	}
	hash := sourceTestGit(t, nested, "rev-parse", "--short=8", "HEAD")
	sourceTestGit(t, nested, "switch", "--detach", "HEAD")
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
	if !slices.Equal(s.RepositoryRefs, []string{"main", hash}) {
		t.Fatalf("repository HEAD labels = %q", s.RepositoryRefs)
	}
	repositories := editorContextBounds(t, m, "sc-repositories")
	changes := editorContextBounds(t, m, "sc-changes")
	history := editorContextBounds(t, m, "sc-history")
	if repositories.X+repositories.Width > changes.X+1 || changes.X+changes.Width > history.X+1 {
		t.Fatalf("source columns overlap: repositories=%+v changes=%+v history=%+v", repositories, changes, history)
	}
	for _, id := range []string{"sc-repository:" + parentRoot, "sc-repository:" + nestedRoot, "sc-branches"} {
		button := editorContextBounds(t, m, id)
		if button.X < repositories.X || button.X+button.Width > repositories.X+repositories.Width || button.Y < repositories.Y || button.Y+button.Height > repositories.Y+repositories.Height {
			t.Fatalf("%s is outside the repositories column: button=%+v repositories=%+v", id, button, repositories)
		}
	}
	active := editorContextBounds(t, m, "sc-repository:"+nestedRoot)
	branch := editorContextBounds(t, m, "sc-branches")
	if branch.Y != active.Y || branch.Height != active.Height || branch.X < active.X+active.Width-1 {
		t.Fatalf("branch selector must sit to the right of the repository: repository=%+v branch=%+v", active, branch)
	}
	editorContextClick(t, app, m, state, "sc-branches")
	if !s.BranchMenuOpen || s.Snapshot.Repository.Root != nestedRoot {
		t.Fatal("clicking the active branch did not open the branch menu")
	}
	editorContextClick(t, app, m, state, "sc-branches")
	parentTrigger := sourceRepositoryBranchPrefix + parentRoot
	parent := editorContextBounds(t, m, "sc-repository:"+parentRoot)
	parentBranch := editorContextBounds(t, m, parentTrigger)
	if parent.Height < 34 || parentBranch.Y != parent.Y || parentBranch.Height != parent.Height || parentBranch.X < parent.X+parent.Width-1 {
		t.Fatalf("inactive row buttons are cramped or overlap: repository=%+v branch=%+v", parent, parentBranch)
	}
	editorContextClick(t, app, m, state, parentTrigger)
	sourceWait(t, app, m)
	if !s.BranchMenuOpen || s.Snapshot.Repository.Root != nestedRoot {
		t.Fatal("opening the parent branch picker changed the current repository")
	}
	for _, ref := range []string{"refs/heads/main", remoteRef} {
		row := editorContextBounds(t, m, "sc-branch:"+ref)
		scale := float64(m.settings.Zoom) / 100
		state.Enqueue(input.Event{Kind: input.EventMouseMove, X: float64(row.X+row.Width/2) * scale, Y: float64(row.Y+row.Height/2) * scale})
		languageInteractionStep(t, app)
		submenu, ok := m.fontResources.HTMLNode(m.surface, "sc-branch-submenu")
		if target, _ := submenu.Attribute("aria-labelledby"); !ok || target != "sc-branch:"+ref {
			t.Fatalf("hover on %s opened submenu for %q", ref, target)
		}
		checkout, _ := m.fontResources.HTMLNode(m.surface, "sc-branch-checkout")
		if _, disabled := checkout.Attribute("disabled"); disabled != (ref == "refs/heads/main") {
			t.Fatalf("incorrect checkout availability for %s", ref)
		}
	}
	editorContextClick(t, app, m, state, "sc-branch-tag")
	if s.Prompt != "tag" || s.PromptHash != remoteRef {
		t.Fatal("remote branch submenu did not target the hovered ref")
	}
	s.PromptValue = "parent-tag"
	editorContextClick(t, app, m, state, "sc-prompt-confirm")
	sourceWait(t, app, m)
	if s.Notice != "" || s.Snapshot.Repository.Root != nestedRoot || sourceTestGit(t, r, "tag", "--list", "parent-tag") != "parent-tag" || sourceTestGit(t, nested, "tag", "--list", "parent-tag") != "" {
		t.Fatal("submenu tag did not apply to the parent repository")
	}
	editorContextClick(t, app, m, state, parentTrigger)
	sourceWait(t, app, m)
	editorContextClick(t, app, m, state, "sc-branch:refs/heads/topic")
	sourceWait(t, app, m)
	if s.BranchMenuOpen || s.Snapshot.Repository.Root != nestedRoot || sourceTestGit(t, r, "branch", "--show-current") != "topic" || nested.headLabel() != hash {
		t.Fatal("checkout did not switch only the parent repository")
	}
	if !slices.Equal(s.RepositoryRefs, []string{"topic", hash}) {
		t.Fatalf("repository HEAD labels after checkout = %q", s.RepositoryRefs)
	}
	s.CommitMessage = "child draft"
	dirty := &sourceTab{Key: "changes::main.go", Path: "main.go", Kind: "changes", Result: "edited", Saved: "original"}
	s.Tabs = []*sourceTab{dirty}
	// Hold a sync open until the foreground has switched repositories.
	syncDone := make(chan sourceResult, 1)
	s.Done, s.JobKind = syncDone, sourceSync
	m.sourceChanged()
	languageInteractionStep(t, app)
	parent = editorContextBounds(t, m, "sc-repository:"+parentRoot)
	scale := float64(m.settings.Zoom) / 100
	x, y := float64(parent.X+parent.Width-3)*scale, float64(parent.Y+parent.Height/2)*scale
	state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseLeft, X: x, Y: y})
	state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseLeft, X: x, Y: y})
	languageInteractionStep(t, app)
	sourceWait(t, app, m)
	if s.Snapshot.Repository.Root != parentRoot || s.CommitMessage != "" || !s.unsavedRepositories() || s.unsaved() {
		t.Fatal("manual selection lost a nested draft or reused its commit message")
	}
	editorContextClick(t, app, m, state, "sc-repository:"+nestedRoot)
	if s.Done != syncDone || !s.busy() || s.CommitMessage != "child draft" {
		t.Fatal("switching back did not restore the child's running sync and draft")
	}
	editorContextClick(t, app, m, state, "sc-repository:"+parentRoot)
	sourceWait(t, app, m)
	s.CommitMessage = "parent draft"
	snapshot := s.RepositoryViews[nestedRoot].Snapshot
	syncDone <- sourceResult{Snapshot: &snapshot, Err: errors.New("sync interrupted")}
	if !m.pollSourceResult(s, m.activeProjectState()) || s.Snapshot.Repository.Root != parentRoot || s.CommitMessage != "parent draft" || s.modal() {
		t.Fatal("child sync completion disturbed the selected repository")
	}
	if !strings.Contains(m.status, nestedRoot) || !strings.Contains(m.status, "sync interrupted") {
		t.Fatalf("background sync error did not identify the repository: %q", m.status)
	}

	editorContextClick(t, app, m, state, sourceRepositoryBranchPrefix+nestedRoot)
	sourceWait(t, app, m)
	editorContextClick(t, app, m, state, "sc-branch:refs/heads/main")
	if s.Notice == "" || s.Snapshot.Repository.Root != parentRoot || nested.headLabel() != hash {
		t.Fatal("checkout did not protect unsaved comparisons in the target repository")
	}
	editorContextClick(t, app, m, state, "sc-notice-close")
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
	control, ok := m.fontResources.HTMLControl(m.surface, "sc-repository:"+parentRoot)
	if !ok || !m.fontResources.Focus(control) {
		t.Fatal("repository row cannot receive keyboard focus")
	}
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyEnter})
	languageInteractionStep(t, app)
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyEnter})
	sourceWait(t, app, m)
	if s.Snapshot.Repository.Root != parentRoot || s.CommitMessage != "parent draft" {
		t.Fatal("keyboard selection did not restore the parent repository")
	}
}
