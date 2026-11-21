package workbench

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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
					if len(group.children) == 5 && len(group.children[1].Children) == 1 {
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

func TestSourceControlRepositoryRowActionsRunIndependently(t *testing.T) {
	seed := sourceTestRepository(t)
	sourceTestWrite(t, seed, "main.go", "package main\n")
	sourceTestGit(t, seed, "add", "main.go")
	sourceTestGit(t, seed, "commit", "-m", "base")
	remote := filepath.Join(t.TempDir(), "remote.git")
	sourceTestGit(t, seed, "clone", "--bare", seed.Root, remote)
	project, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seed.Backend.OpenWorkspace(t.Context(), project); err != nil {
		t.Fatal(err)
	}
	slow := sourceRepository{Root: filepath.ToSlash(filepath.Join(project, "slow")), Backend: seed.Backend}
	fast := sourceRepository{Root: filepath.ToSlash(filepath.Join(project, "fast")), Backend: seed.Backend}
	for _, r := range []sourceRepository{slow, fast} {
		sourceTestGit(t, seed, "clone", remote, r.Root)
	}
	if _, err := seed.Backend.GitRepositories(t.Context(), project); err != nil {
		t.Fatal(err)
	}
	sourceTestGit(t, slow, "switch", "-c", "publish")
	sourceTestGit(t, slow, "push", "-u", "origin", "publish")
	sourceTestGit(t, slow, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "outgoing")
	for range 2 {
		sourceTestGit(t, seed, "commit", "--allow-empty", "-m", "incoming")
	}
	sourceTestGit(t, seed, "push", remote, "main")
	remoteHead := sourceTestGit(t, seed, "rev-parse", "HEAD")

	hooks := filepath.Join(slow.Root, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0755); err != nil {
		t.Fatal(err)
	}
	sourceTestGit(t, slow, "config", "core.hooksPath", hooks)
	const hook = "#!/bin/sh\ntouch .git/push-started\nwhile [ ! -f .git/push-release ]; do sleep 0.05; done\n"
	if err := os.WriteFile(filepath.Join(hooks, "pre-push"), []byte(hook), 0755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("ONIX_PENCODE_PROFILE_PROJECT", project)
	t.Setenv("ONIX_PENCODE_PROFILE_FILE", "slow/main.go")
	app, m, _, state := languageInteractionFixture(t)
	m.languages = nil
	release := filepath.Join(slow.Root, ".git", "push-release")
	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0644) })
	editorContextClick(t, app, m, state, "status-source-control")
	sourceWait(t, app, m)
	s := m.sourceState()
	if s.Snapshot.Repository.Root != slow.Root {
		t.Fatal("the project did not select the repository for the active file")
	}
	wait := func(ready func() bool) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for !ready() {
			if time.Now().After(deadline) {
				t.Fatalf("repository actions did not reach the expected state: %s", m.status)
			}
			languageInteractionStep(t, app)
			time.Sleep(time.Millisecond)
		}
	}
	attribute := func(id, key string) string {
		t.Helper()
		node, ok := m.fontResources.HTMLNode(m.surface, id)
		if !ok {
			t.Fatalf("missing repository action %s", id)
		}
		value, _ := node.Attribute(key)
		return value
	}
	push := sourceRepositoryActionPrefix + "push:" + slow.Root
	pull := sourceRepositoryActionPrefix + "pull:" + fast.Root
	refresh := sourceRepositoryActionPrefix + "refresh:" + fast.Root
	branch := editorContextBounds(t, m, sourceRepositoryBranchPrefix+fast.Root)
	if bounds := editorContextBounds(t, m, refresh); bounds.X < branch.X+branch.Width-1 || bounds.Y < branch.Y || bounds.Y+bounds.Height > branch.Y+branch.Height {
		t.Fatalf("refresh is outside the branch's action area: branch=%+v refresh=%+v", branch, bounds)
	}
	if attribute(push, "aria-label") != "Push (1)" || attribute(pull, "aria-label") != "Pull" {
		t.Fatal("the row actions did not show the initial tracking counts")
	}
	editorContextClick(t, app, m, state, push)
	wait(func() bool {
		_, err := os.Stat(filepath.Join(slow.Root, ".git", "push-started"))
		return err == nil && attribute(push, "aria-busy") == "true"
	})
	editorContextClick(t, app, m, state, refresh)
	wait(func() bool { return attribute(pull, "aria-label") == "Pull (2)" })
	s.RepositoriesWidthStyle = "width:120px;"
	m.sourceChanged()
	languageInteractionStep(t, app)
	column := editorContextBounds(t, m, "sc-repositories")
	fastPush := editorContextBounds(t, m, sourceRepositoryActionPrefix+"push:"+fast.Root)
	if fastPush.X+fastPush.Width > column.X+column.Width {
		t.Fatalf("count pushed an action outside the repository column: %+v", fastPush)
	}
	editorContextClick(t, app, m, state, pull)
	wait(func() bool { return attribute(pull, "aria-label") == "Pull" })
	if s.Snapshot.Repository.Root != slow.Root || attribute(push, "aria-busy") != "true" || sourceTestGit(t, fast, "rev-parse", "HEAD") != remoteHead {
		t.Fatal("the other repository's pull did not complete while push was running")
	}
	if err := os.WriteFile(release, nil, 0644); err != nil {
		t.Fatal(err)
	}
	wait(func() bool { return attribute(push, "aria-label") == "Push" && attribute(push, "aria-busy") == "" })
}

func TestSourceControlBranchCheckoutDuringCommitAndSync(t *testing.T) {
	seed := sourceTestRepository(t)
	sourceTestWrite(t, seed, "main.go", "package main\n")
	sourceTestGit(t, seed, "add", "main.go")
	sourceTestGit(t, seed, "commit", "-m", "base")
	remote := filepath.Join(t.TempDir(), "remote.git")
	sourceTestGit(t, seed, "clone", "--bare", seed.Root, remote)
	project, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seed.Backend.OpenWorkspace(t.Context(), project); err != nil {
		t.Fatal(err)
	}
	slow := sourceRepository{Root: filepath.ToSlash(filepath.Join(project, "slow")), Backend: seed.Backend}
	fast := sourceRepository{Root: filepath.ToSlash(filepath.Join(project, "fast")), Backend: seed.Backend}
	for _, r := range []sourceRepository{slow, fast} {
		sourceTestGit(t, seed, "clone", remote, r.Root)
	}
	if _, err := seed.Backend.GitRepositories(t.Context(), project); err != nil {
		t.Fatal(err)
	}
	sourceTestGit(t, fast, "branch", "topic")
	sourceTestGit(t, slow, "config", "user.name", "Test")
	sourceTestGit(t, slow, "config", "user.email", "test@example.invalid")
	sourceTestGit(t, slow, "config", "commit.gpgsign", "false")
	hooks := filepath.Join(slow.Root, ".git", "hooks")
	sourceTestGit(t, slow, "config", "core.hooksPath", hooks)
	for _, name := range []string{"pre-commit", "pre-push"} {
		hook := "#!/bin/sh\ntouch .git/hooks/" + name + "-started\nwhile [ ! -f .git/hooks/" + name + "-release ]; do sleep 0.05; done\n"
		if err := os.WriteFile(filepath.Join(hooks, name), []byte(hook), 0755); err != nil {
			t.Fatal(err)
		}
	}
	sourceTestWrite(t, slow, "main.go", "package main\n// outgoing\n")
	sourceTestGit(t, slow, "add", "main.go")

	t.Setenv("ONIX_PENCODE_PROFILE_PROJECT", project)
	t.Setenv("ONIX_PENCODE_PROFILE_FILE", "slow/main.go")
	app, m, _, state := languageInteractionFixture(t)
	t.Cleanup(func() {
		for _, name := range []string{"pre-commit", "pre-push"} {
			_ = os.WriteFile(filepath.Join(hooks, name+"-release"), nil, 0644)
		}
	})
	m.languages = nil
	editorContextClick(t, app, m, state, "status-source-control")
	sourceWait(t, app, m)
	s := m.sourceState()
	fastIndex := slices.Index(s.Repositories, fast.Root)
	if s.Snapshot.Repository.Root != slow.Root || fastIndex < 0 {
		t.Fatalf("unexpected repository selection %q from %v", s.Snapshot.Repository.Root, s.Repositories)
	}
	wait := func(ready func() bool) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for !ready() {
			if time.Now().After(deadline) {
				t.Fatalf("Git job did not reach the expected state. Status %q, notice %q", m.status, s.Notice)
			}
			languageInteractionStep(t, app)
			time.Sleep(time.Millisecond)
		}
	}
	for _, test := range []struct{ Job, Hook, Branch, WantMessage string }{
		{sourceRepositoryCommit, "pre-commit", "topic", ""},
		{sourceRepositorySync, "pre-push", "main", "slow draft"},
	} {
		s.CommitMessage = "slow draft"
		editorContextClick(t, app, m, state, "sc-create-commit")
		wait(func() bool {
			_, err := os.Stat(filepath.Join(hooks, test.Hook+"-started"))
			return err == nil && s.repositoryJobs[slow.Root].Name == test.Job
		})
		activeBranch, _ := m.fontResources.HTMLNode(m.surface, "sc-branches")
		trigger := sourceRepositoryBranchPrefix + fast.Root
		otherBranch, _ := m.fontResources.HTMLNode(m.surface, trigger)
		_, activeDisabled := activeBranch.Attribute("disabled")
		_, otherDisabled := otherBranch.Attribute("disabled")
		if !activeDisabled || otherDisabled {
			t.Fatalf("during %s, disabled branches were slow=%t, fast=%t", test.Job, activeDisabled, otherDisabled)
		}
		m.sourceAction("sc-diff:" + url.PathEscape("slow/main.go"))
		editorContextClick(t, app, m, state, trigger)
		wait(func() bool { return s.BranchMenuOpen && s.branchPicker != nil })
		row := editorContextBounds(t, m, "sc-branch:refs/heads/"+test.Branch)
		scale := float64(m.settings.Zoom) / 100
		state.Enqueue(input.Event{Kind: input.EventMouseMove, X: float64(row.X+row.Width/2) * scale, Y: float64(row.Y+row.Height/2) * scale})
		languageInteractionStep(t, app)
		editorContextClick(t, app, m, state, "sc-branch-checkout")
		wait(func() bool { return s.RepositoryRefs[fastIndex] == test.Branch })
		if s.Snapshot.Repository.Root != slow.Root || s.repositoryJobs[slow.Root].Name != test.Job || fast.headLabel() != test.Branch {
			t.Fatalf("checkout of %s did not finish independently during %s", test.Branch, test.Job)
		}
		editorContextClick(t, app, m, state, "sc-repository:"+fast.Root)
		sourceWait(t, app, m)
		if s.Snapshot.Repository.Root != fast.Root || s.Snapshot.Branch != test.Branch {
			t.Fatalf("could not select the other repository during %s", test.Job)
		}
		s.CommitMessage = "fast draft"
		if err := os.WriteFile(filepath.Join(hooks, test.Hook+"-release"), nil, 0644); err != nil {
			t.Fatal(err)
		}
		wait(func() bool { return s.repositoryJobs[slow.Root].Done == nil })
		if s.CommitMessage != "fast draft" || s.modal() || s.Snapshot.Repository.Root != fast.Root {
			t.Fatalf("completion of %s changed the selected repository's view", test.Job)
		}
		editorContextClick(t, app, m, state, "sc-repository:"+slow.Root)
		sourceWait(t, app, m)
		if s.CommitMessage != test.WantMessage {
			t.Fatalf("after %s the original draft was %q, want %q", test.Job, s.CommitMessage, test.WantMessage)
		}
	}
	if s.Snapshot.Sync != (sourceSyncStatus{}) {
		t.Fatalf("sync kept stale tracking counts: %+v", s.Snapshot.Sync)
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
	s.repositoryJobs = map[string]sourceRepositoryJob{nestedRoot: {Done: syncDone, Name: sourceRepositorySync}}
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
	if s.repositoryJobs[nestedRoot].Done != syncDone || !s.busy() || s.CommitMessage != "child draft" {
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
