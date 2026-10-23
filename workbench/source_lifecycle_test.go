package workbench

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/diiyw/onix"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
	engineui "github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
)

func TestSourceControlCloseChecksOnlyUnsavedEditsAndWrites(t *testing.T) {
	for _, name := range []string{"clean", "refresh", "query", "write", "completed-write", "sync", "completed-sync", "clean-diff", "dirty-diff", "clean-merge", "dirty-merge"} {
		t.Run(name, func(t *testing.T) {
			project := &pencodeProject{Path: "/project"}
			m := &workspaceModel{projects: []*pencodeProject{project}, activeProjectIndex: 0, projectPath: project.Path}
			s := &project.sourceControl
			wantBlocked := false
			switch name {
			case "refresh", "query", "write", "completed-write", "sync", "completed-sync":
				s.Done = make(chan sourceResult, 1)
				s.JobKind = sourceQuery
				if name == "refresh" {
					s.JobKind = sourceRefresh
				}
				if name == "write" || name == "completed-write" {
					s.JobKind = sourceWrite
					wantBlocked = name == "write"
				}
				if name == "sync" || name == "completed-sync" {
					s.JobKind = sourceSync
					wantBlocked = name == "sync"
				}
				if strings.HasPrefix(name, "completed-") {
					s.Done <- sourceResult{}
				}
			case "clean-diff", "dirty-diff", "clean-merge", "dirty-merge":
				kind := "changes"
				if strings.HasSuffix(name, "merge") {
					kind = "merge"
				}
				tab := &sourceTab{Kind: kind, Saved: "saved", Result: "saved"}
				if strings.HasPrefix(name, "dirty") {
					tab.Result = "edited"
					wantBlocked = true
				}
				s.Tabs = []*sourceTab{tab}
			}
			if got := m.sourceCloseBlocked(project); got != wantBlocked {
				t.Fatalf("close blocked=%v, want %v", got, wantBlocked)
			}
			if (s.Notice != "") != wantBlocked {
				t.Fatalf("unexpected close notice: %q", s.Notice)
			}
		})
	}
}

func TestSourceControlRefreshDoesNotSwallowActionsOrLateResults(t *testing.T) {
	m := &workspaceModel{}
	s := m.sourceState()
	t.Cleanup(s.stop)
	s.Context, s.Cancel = context.WithCancel(context.Background())
	refreshContext, cancel := context.WithCancel(s.Context)
	s.RefreshCancel = cancel
	s.JobKind, s.Done = sourceRefresh, make(chan sourceResult, 1)
	stale := s.Done
	m.sourceAction("sc-branches")
	if !s.BranchMenuOpen || s.Done != nil || refreshContext.Err() == nil || s.Context.Err() != nil {
		t.Fatal("background refresh swallowed a branch click or cancelled the project")
	}
	stale <- sourceResult{Err: context.Canceled}
	if m.pollSourceResult(s, nil) || s.Notice != "" {
		t.Fatal("cancelled background result was published")
	}
	m.sourceJob(sourceQuery, func() sourceResult { return sourceResult{Message: "fresh result"} })
	deadline := time.Now().Add(time.Second)
	for s.Done != nil && time.Now().Before(deadline) {
		m.pollSourceResult(s, nil)
		time.Sleep(time.Millisecond)
	}
	if s.Done != nil || m.status != "fresh result" || s.Notice != "" {
		t.Fatal("foreground query did not complete after cancelling a refresh")
	}
}

func TestSourceControlRefreshKeepsRepositoryContextAlive(t *testing.T) {
	m := &workspaceModel{}
	s := m.sourceState()
	t.Cleanup(s.stop)
	s.Context, s.Cancel = context.WithCancel(context.Background())
	ctx, cancel := context.WithCancel(s.Context)
	s.RefreshCancel = cancel
	s.Done = make(chan sourceResult, 1)
	s.Done <- sourceResult{Snapshot: &sourceSnapshot{Repository: sourceRepository{Root: "/repository", Context: ctx}}}
	if !m.pollSourceResult(s, nil) || ctx.Err() == nil || s.Snapshot.Repository.Context.Err() != nil {
		t.Fatal("refresh published a cancelled repository context")
	}
}

func TestSourceControlBackgroundStatusOutsideRepository(t *testing.T) {
	m := &workspaceModel{projectPath: t.TempDir(), backend: newPencodeBackend(t)}
	if _, err := m.backend.OpenWorkspace(t.Context(), m.projectPath); err != nil {
		t.Fatal(err)
	}
	s := m.sourceState()
	t.Cleanup(s.stop)
	if !m.updateSourceControl(nil, nil) || s.Done == nil {
		t.Fatal("closed panel did not start a background status refresh")
	}
	wait := func() {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for s.Done != nil && time.Now().Before(deadline) {
			m.pollSourceResult(s, nil)
			time.Sleep(time.Millisecond)
		}
		if s.Done != nil {
			t.Fatal("status refresh did not finish")
		}
	}
	wait()
	if s.Notice != "" || s.Snapshot.Repository.Root != "" {
		t.Fatal("background status refresh opened a Git error outside a repository")
	}
	m.sourceAction("sc-toggle")
	wait()
	if s.Notice == "" {
		t.Fatal("opening Source Control did not report the repository error")
	}
}

func TestSourceControlRefreshLeavesDiffEditorWritable(t *testing.T) {
	r := sourceTestRepository(t)
	name := "resources/main.go"
	sourceTestWrite(t, r, name, "package demo\n")
	t.Setenv("ONIX_PENCODE_PROFILE_PROJECT", r.Root)
	t.Setenv("ONIX_PENCODE_PROFILE_FILE", name)
	app, m, _, _ := languageInteractionFixture(t)
	m.languages = nil
	s := m.sourceState()
	tab := &sourceTab{Key: "changes::" + name, Path: name, Kind: "changes", Result: "package demo\n", Saved: "package demo\n"}
	s.Snapshot.Repository = r
	s.Tabs, s.Active = []*sourceTab{tab}, tab.Key
	s.NextRefresh = time.Now().Add(time.Hour)
	s.Done, s.JobKind = make(chan sourceResult, 1), sourceRefresh
	m.sourceChanged()
	languageInteractionStep(t, app)
	m.bindSourceDiffControls(m.fontResources)
	control, ok := m.fontResources.HTMLControl(m.surface, "sc-code-right-input")
	binding, bound := module.ContentGet[engineui.TextField](m.fontResources, control)
	if !ok || !bound || binding.Value != tab.Result {
		t.Fatal("background refresh hid the diff input")
	}
	if binding, ok := module.ContentGet[htmlui.Control](m.fontResources, control); !ok || binding.ReadOnly {
		t.Fatal("background refresh made the diff input read only")
	}
}

func TestSourceControlPublishesBackgroundResultWithoutInput(t *testing.T) {
	r := sourceTestRepository(t)
	sourceTestWrite(t, r, "resources/main.go", "package demo\n")
	t.Setenv("ONIX_PENCODE_PROFILE_PROJECT", r.Root)
	t.Setenv("ONIX_PENCODE_PROFILE_FILE", "resources/main.go")
	app, m, _, _ := languageInteractionFixture(t)
	m.languages = nil
	m.sourceAction("sc-toggle")
	sourceWait(t, app, m)
	app.SetRenderMode(onix.RenderOnDemand)
	app.SetIdleEnabled(true)
	s := m.sourceState()
	next := s.Snapshot
	next.Branch = "fresh"
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	t.Cleanup(unblock)
	woken := make(chan struct{}, 1)
	wakeAt := m.app.WakeAt
	m.app.WakeAt = func(deadline time.Time) {
		wakeAt(deadline)
		if !deadline.After(time.Now()) {
			select {
			case woken <- struct{}{}:
			default:
			}
		}
	}
	m.sourceJob(sourceQuery, func() sourceResult {
		<-release
		return sourceResult{Snapshot: &next}
	})
	for range 3 {
		languageInteractionStep(t, app)
	}
	unblock()
	select {
	case <-woken:
	case <-time.After(time.Second):
		t.Fatal("Git completion did not wake the idle app")
	}
	for range 3 {
		languageInteractionStep(t, app)
	}
	for _, access := range module.ContentQuery[scene.Text2D](m.fontResources) {
		if strings.Contains(access.Get().Value, next.Branch) {
			return
		}
	}
	t.Fatalf("Git completion did not publish the new branch label without user input: branch=%q pending=%v revision=%d tools=%q", s.Snapshot.Branch, s.Done != nil, s.RenderRevision, s.toolsKey.Branch)
}
