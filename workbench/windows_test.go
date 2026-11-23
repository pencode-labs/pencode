package workbench

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/onix-engine/onix/module"
	"github.com/pencode-labs/pencode/client"
	"github.com/pencode-labs/pencode/connection"
	"github.com/pencode-labs/pencode/remote"
)

func configureTestWindow(t *testing.T, s *Session, directory, id string, restore bool) []string {
	t.Helper()
	s.stateDirectory = directory
	ids, err := s.ConfigureWindows(id, restore)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.window.store.Release)
	return ids
}

func flushTestWindow(s *Session, m *workspaceModel) {
	s.window.due = time.Now().Add(-time.Second)
	s.checkpointWindow(m)
}

func restoreTestWindow(t *testing.T, s *Session, m *workspaceModel) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for s.window.restore != nil {
		if time.Now().After(deadline) {
			t.Fatal("window restoration did not finish")
		}
		s.pollWindowRestore(m)
		if err := awaitFileIO(t, m); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWindowRecoveryIncludesProjectsLooseFilesTabsAndLayout(t *testing.T) {
	s, m := externalTestSession(t)
	directory := t.TempDir()
	configureTestWindow(t, s, directory, "", false)
	root, otherRoot, loose := t.TempDir(), t.TempDir(), t.TempDir()
	for _, name := range []string{"first.txt", "second.txt", "deleted.txt"} {
		resourceTestWrite(t, &pencodeProject{Path: root}, name, name)
	}
	resourceTestWrite(t, &pencodeProject{Path: loose}, "loose.txt", "loose")
	s.OpenPaths(root, filepath.Join(root, "first.txt"), filepath.Join(root, "second.txt"), filepath.Join(root, "deleted.txt"), otherRoot, filepath.Join(loose, "loose.txt"))
	settleExternalPaths(t, s, m)
	m.selectProjectTab(m.projects[0], "first.txt")
	m.activeFile().editorView = pencodeEditorView{caret: 4, anchor: 1, scrollY: 80}
	m.toggleSidebar()
	m.sidebarWidthStyle = "flex-basis:300px;"
	m.bottom.heightStyle = "height:250px;max-height:75%;"
	m.viewportWidth, m.viewportHeight = 1200, 800
	flushTestWindow(s, m)
	if !s.window.due.IsZero() {
		t.Fatal("checkpoint failed")
	}
	// Neither allocation nor disk writes should occur on unchanged frames.
	if allocations := testing.AllocsPerRun(100, func() { s.checkpointWindow(m) }); allocations != 0 {
		t.Fatalf("unchanged checkpoint allocated %g times", allocations)
	}
	s.window.store.Release() // Simulate process loss without the close lifecycle.
	if err := os.Remove(filepath.Join(root, "deleted.txt")); err != nil {
		t.Fatal(err)
	}
	// Another window's project history must not replace this window's tabs.
	other := pencodeProjectState{ActivePath: "second.txt", Tabs: []pencodeTabState{{Path: "second.txt"}}}
	if err := writeProjectState(s.projectStateFile(root), &other); err != nil {
		t.Fatal(err)
	}
	next, model := externalTestSession(t)
	configureTestWindow(t, next, directory, "", true)
	if width, height := next.RestoredWindowSize(); width != 1200 || height != 800 {
		t.Fatalf("window size=%d,%d", width, height)
	}
	restoreTestWindow(t, next, model)
	if len(model.projects) != 3 || model.activeProjectIndex != 0 || model.projects[0].Loose || model.projects[1].Loose || !model.projects[2].Loose {
		t.Fatal("project order, loose file workspace, or selected project changed")
	}
	p := model.projects[0]
	if !slices.Equal(p.Tabs, []string{"first.txt", "second.txt"}) || p.ActivePath != "first.txt" || p.Files["first.txt"].editorView != m.projects[0].Files["first.txt"].editorView {
		t.Fatalf("lost tabs/selection: tabs=%v active=%s", p.Tabs, p.ActivePath)
	}
	if next.recentProjects.count != 2 || next.recentProjects.entries[0].Path != otherRoot || next.recentProjects.entries[1].Path != root {
		t.Fatal("loose file parent was remembered as a project")
	}
	if !model.sidebarHidden() || model.projects[1].sidebarCollapsed || model.sidebarWidthStyle != m.sidebarWidthStyle || model.bottom.heightStyle != m.bottom.heightStyle {
		t.Fatal("window layout was not restored")
	}
	if model.projects[2].watch != nil || model.projects[2].scan != nil {
		t.Fatal("restored loose workspace started a project scan")
	}
}

func TestWindowCloseCancellationAndDiscard(t *testing.T) {
	s, m := externalTestSession(t)
	directory := t.TempDir()
	configureTestWindow(t, s, directory, "", false)
	root := t.TempDir()
	resourceTestWrite(t, &pencodeProject{Path: root}, "file.txt", "disk")
	s.OpenPaths(root, filepath.Join(root, "file.txt"))
	settleExternalPaths(t, s, m)
	flushTestWindow(s, m)
	m.updateActiveContent("unsaved")
	if m.requestWindowClose() || s.window.closed {
		t.Fatal("dirty window was closed without approval")
	}
	m.clearCloseRequest()
	files, err := filepath.Glob(filepath.Join(directory, "windows", "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("cancelled close removed recovery state: %v, %v", files, err)
	}
	if m.requestWindowClose() {
		t.Fatal("dirty window did not ask again after cancellation")
	}
	m.resolveClose(false)
	if !s.window.closed {
		t.Fatal("confirmed close retained its recovery record")
	}
	flushTestWindow(s, m)
	files, _ = filepath.Glob(filepath.Join(directory, "windows", "*.json"))
	if len(files) != 0 {
		t.Fatal("closed window's checkpoint recreated the record")
	}
	s.window.store.Release()
	next, _ := externalTestSession(t)
	if ids := configureTestWindow(t, next, directory, "", true); next.window.restore != nil || len(ids) != 0 {
		t.Fatal("explicitly closed window was recovered")
	}
}

func TestWindowsKeepIndependentTabsForTheSameProject(t *testing.T) {
	directory, root := t.TempDir(), t.TempDir()
	for _, name := range []string{"one.txt", "two.txt"} {
		resourceTestWrite(t, &pencodeProject{Path: root}, name, name)
	}
	for _, name := range []string{"one.txt", "two.txt"} {
		s, m := externalTestSession(t)
		configureTestWindow(t, s, directory, "", false)
		s.OpenPaths(root, filepath.Join(root, name))
		settleExternalPaths(t, s, m)
		flushTestWindow(s, m)
		s.window.store.Release()
	}
	s, m := externalTestSession(t)
	ids := configureTestWindow(t, s, directory, "", true)
	if len(ids) != 1 {
		t.Fatalf("additional windows=%v", ids)
	}
	restoreTestWindow(t, s, m)
	first := m.activePath
	next, model := externalTestSession(t)
	configureTestWindow(t, next, directory, ids[0], false)
	restoreTestWindow(t, next, model)
	if len(m.projects[0].Tabs) != 1 || len(model.projects[0].Tabs) != 1 || model.activePath == first {
		t.Fatal("windows shared project tabs")
	}
}

func TestWindowCheckpointSchedulesIdleWakeAndPreservesUpdates(t *testing.T) {
	s, m := externalTestSession(t)
	configureTestWindow(t, s, t.TempDir(), "", false)
	var wake time.Time
	m.app = &module.Host{WakeAt: func(deadline time.Time) { wake = deadline }}
	s.checkpointWindow(m)
	if wake.IsZero() || wake != s.window.due {
		t.Fatal("checkpoint did not schedule an idle wake")
	}
	flushTestWindow(s, m)
	s.updates = &pencodeUpdates{requested: true}
	if !m.requestWindowClose() || s.window.closed || !s.updates.approved {
		t.Fatal("update removed the window or failed to approve restart")
	}
}

func TestRemoteWindowRecoveryWaitsAfterConnectionFailure(t *testing.T) {
	s, _ := externalTestSession(t)
	directory := t.TempDir()
	configureTestWindow(t, s, directory, "", false)
	state := pencodeWindowState{Profile: connection.Profile{SSH: remote.Target{Host: "offline"}}, Projects: []pencodeWindowProject{{Path: "/remote/project"}}}
	if err := s.window.store.Save(&state); err != nil {
		t.Fatal(err)
	}
	s.window.store.Release()
	next, model := externalTestSession(t)
	configureTestWindow(t, next, directory, "", true)
	attempts := 0
	next.ConfigureConnection(func(context.Context, connection.Profile, func(string)) (*client.Client, error) {
		attempts++
		return nil, errors.New("offline")
	}, connection.Profile{})
	if !next.pollWindowRestore(model) {
		t.Fatal("remote restore did not start connection")
	}
	<-next.connection.done
	model.pollConnection()
	for range 10 {
		next.pollWindowRestore(model)
		next.checkpointWindow(model)
	}
	if attempts != 1 || next.window.restore == nil {
		t.Fatal("failed remote restore retried or discarded its snapshot")
	}
}

func TestRemoteWindowRecoveryRestoresConnectionAndTabs(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	s, _ := externalTestSession(t)
	directory, root := t.TempDir(), t.TempDir()
	resourceTestWrite(t, &pencodeProject{Path: root}, "remote.txt", "remote content")
	configureTestWindow(t, s, directory, "", false)
	profile := connection.Profile{SSH: remote.Target{Host: "test-host"}}
	state := pencodeWindowState{Profile: profile, Projects: []pencodeWindowProject{{Path: root, State: pencodeProjectState{ActivePath: "remote.txt", Tabs: []pencodeTabState{{Path: "remote.txt", Caret: 3}}}}}}
	if err := s.window.store.Save(&state); err != nil {
		t.Fatal(err)
	}
	s.window.store.Release()
	next, model := externalTestSession(t)
	configureTestWindow(t, next, directory, "", true)
	backend := newPencodeBackend(t)
	next.ConfigureConnection(func(_ context.Context, got connection.Profile, _ func(string)) (*client.Client, error) {
		if got != profile {
			return nil, errors.New("wrong remote profile")
		}
		return backend, nil
	}, connection.Profile{})
	t.Cleanup(next.Close)
	deadline := time.Now().Add(5 * time.Second)
	for next.window.restore != nil {
		model.pollConnection()
		next.pollWindowRestore(model)
		if err := awaitFileIO(t, model); err != nil {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("remote restore stalled: %s", next.connection.message)
		}
		time.Sleep(time.Millisecond)
	}
	if model.backend != backend || model.activeFile() == nil || model.activeFile().content() != "remote content" || model.activeFile().editorView.caret != 3 || !next.connection.workspaceProfile.SameHost(profile) || next.connection.workspaceProfile.Directory != root {
		t.Fatal("remote connection or editor was not restored")
	}
	flushTestWindow(next, model)
	if allocations := testing.AllocsPerRun(100, func() { next.checkpointWindow(model) }); allocations != 0 {
		t.Fatalf("remote idle checkpoint allocated %g times", allocations)
	}
}

func TestWindowRequestsUseApplicationOpener(t *testing.T) {
	s, m := externalTestSession(t)
	var requests [][]string
	s.SetWindowOpener(func(paths []string, _ connection.Profile) error {
		requests = append(requests, slices.Clone(paths))
		return nil
	})
	if err := s.OpenWindow(nil); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 1 || len(requests[0]) != 0 {
		t.Fatal("new window did not use the owning application")
	}
	// The whole batch goes to a local window.
	profile := connection.Profile{SSH: remote.Target{Host: "remote-host"}}
	s.connection = &pencodeConnection{workspaceProfile: profile}
	p := &pencodeProject{Path: "/remote/project", Files: make(map[string]*openFile)}
	m.projects, m.activeProjectIndex = []*pencodeProject{p}, 0
	s.OpenPaths("/local/first.txt", "/local/second.txt")
	if !s.pollExternalPaths(m) || len(requests) != 2 || !slices.Equal(requests[1], []string{"/local/first.txt", "/local/second.txt"}) {
		t.Fatal("local drop did not forward both paths to a local window")
	}
	if s.connection.workspaceProfile != profile || len(s.externalPaths) != 0 {
		t.Fatal("opening another window modified the current connection")
	}
}

func TestDuplicateProjectSelectsExistingTabOnItsHost(t *testing.T) {
	owner, project := resourceTestModel(t)
	s := newPencodeTestSession(t, project.backend)
	owner.session, s.active = s, &Mod{model: owner}
	remoteHost := connection.Profile{SSH: remote.Target{Host: "one", Port: 2222}}
	s.ConfigureConnection(nil, connection.Profile{})
	s.connection.workspaceProfile = remoteHost
	t.Cleanup(s.Close)
	resourceTestWrite(t, project, "dirty.go", "original")
	openProjectFile(project, "dirty.go")
	owner.syncActiveProjectFields()
	owner.updateActiveContent("unsaved")
	file, doc := owner.activeFile(), owner.activeFile().document
	other := &pencodeProject{Path: "/other"}
	owner.projects = append(owner.projects, other)
	owner.activateProject(other)
	source, m := externalTestSession(t)
	source.SetProjectActivator(func(_ *Session, profile connection.Profile, path string) bool {
		return s.ActivateProject(profile, path)
	})
	entry := pencodeRecentProject{Path: project.Path, Profile: remoteHost}
	entry.Profile.Name = "renamed"
	m.openRecentProject(entry)
	if owner.activeProjectState() != project || owner.activeFile() != file || file.document != doc || file.content() != "unsaved" || !file.Dirty || m.fileIO != nil || owner.fileIO != nil {
		t.Fatal("duplicate open did not select the original unsaved tab without I/O")
	}
	if allocations := testing.AllocsPerRun(100, func() { s.ActivateProject(remoteHost, project.Path) }); allocations != 0 {
		t.Fatalf("activating current project allocated %g times", allocations)
	}
	for _, profile := range []connection.Profile{{}, {SSH: remote.Target{Host: "two", Port: 2222}}, {SSH: remote.Target{Host: "one", Port: 22}}} {
		if s.ActivateProject(profile, project.Path) {
			t.Fatal("matched an identical path on another host", profile)
		}
	}
}

func TestRemoteWindowRoutesAnotherHostAfterClosingLastTab(t *testing.T) {
	s, m := externalTestSession(t)
	first := connection.Profile{SSH: remote.Target{Host: "first"}}
	second := connection.Profile{SSH: remote.Target{Host: "second"}}
	s.ConfigureConnection(nil, connection.Profile{})
	s.connection.workspaceProfile = first
	backend := newPencodeBackend(t)
	s.connection.links[second.Endpoint()] = &pencodeRemoteLink{backend: backend, profile: second}
	t.Cleanup(s.Close)
	var opened connection.Profile
	s.SetWindowOpener(func(paths []string, profile connection.Profile) error {
		if len(paths) != 0 {
			t.Fatal("remote project received as a local path")
		}
		opened = profile
		return nil
	})
	second.Directory = t.TempDir()
	m.openRemoteProject(second)
	waitProjectPicker(t, m)
	m.projectPickerEvent("connection-project-open", "")
	if !opened.SameHost(second) || opened.Directory != filepath.ToSlash(second.Directory) || s.windowProfile() != first || m.backend == backend {
		t.Fatal("empty remote window was reassigned to the other host")
	}
}

func TestRemoteWindowQueuesProjectsDuringLaunch(t *testing.T) {
	s, m := externalTestSession(t)
	profile := connection.Profile{SSH: remote.Target{Host: "dev"}, Directory: t.TempDir()}
	backend := newPencodeBackend(t)
	s.ConfigureConnection(func(context.Context, connection.Profile, func(string)) (*client.Client, error) { return backend, nil }, profile)
	t.Cleanup(s.Close)
	next := profile
	next.Directory = t.TempDir()
	if !s.ActivateProject(profile, profile.Directory) || !s.QueueRemoteProject(next) || !s.ActivateProject(next, next.Directory) || s.pollRemoteProjects(m) {
		t.Fatal("remote window lost ownership of its pending projects")
	}
	m.pollConnection()
	waitConnection(t, m)
	waitProjectPicker(t, m)
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	if !s.pollRemoteProjects(m) {
		t.Fatal("second project was left pending")
	}
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	if len(m.projects) != 2 || m.activeProjectState().Path != next.Directory || m.backend != backend {
		t.Fatal("queued project did not open on its host")
	}
}
