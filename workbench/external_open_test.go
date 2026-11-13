package workbench

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/onix-engine/onix/input"
	engineui "github.com/onix-engine/onix/ui"
	"github.com/pencode-labs/pencode/connection"
)

func settleExternalPaths(t *testing.T, s *Session, m *workspaceModel) {
	t.Helper()
	for len(s.externalPaths) != 0 || s.externalJob != nil {
		s.pollExternalPaths(m)
		_ = awaitFileIO(t, m)
		if s.externalJob != nil && s.externalJob.finished {
			s.externalJob = nil
		}
	}
}

func externalTestSession(t *testing.T) (*Session, *workspaceModel) {
	t.Helper()
	backend := newPencodeBackend(t)
	s := newSession(backend, "", "")
	m := &workspaceModel{backend: backend, session: s, activeProjectIndex: -1}
	t.Cleanup(func() {
		m.stopFileIO()
		for _, p := range m.projects {
			p.stopScan()
			p.stopWatch()
			for _, f := range p.Files {
				f.closeDocument()
			}
		}
	})
	return s, m
}

func TestExternalPathsMultipleFilesAndDirectories(t *testing.T) {
	s, m := externalTestSession(t)
	root := t.TempDir()
	for _, name := range []string{"中文 空格.go", "--second.md", "nested/third.txt"} {
		resourceTestWrite(t, &pencodeProject{Path: root}, name, name)
	}
	// Queue before a foreground view exists, as happens during a Finder launch.
	s.OpenPaths(root, filepath.Join(root, "中文 空格.go"), filepath.Join(root, "--second.md"), filepath.Join(root, "nested", "third.txt"))
	settleExternalPaths(t, s, m)
	if len(m.projects) != 1 || m.screen != pencodeWorkspace {
		t.Fatalf("expected one workspace: projects=%d screen=%d", len(m.projects), m.screen)
	}
	p := m.activeProjectState()
	if !slices.Equal(p.Tabs, []string{"中文 空格.go", "--second.md", "nested/third.txt"}) || p.ActivePath != "nested/third.txt" {
		t.Fatalf("dropped files were cancelled, reordered or opened outside the existing root: %v, %s", p.Tabs, p.ActivePath)
	}
	for name, f := range p.Files {
		if f.Preview || f.content() != name {
			t.Fatalf("file %s is not a pinned, loaded tab: %+v", name, f)
		}
	}
}

func TestExternalFilesDoNotRememberParentProjects(t *testing.T) {
	s, m := externalTestSession(t)
	s.recentProjects = loadPencodeRecentProjects(t.TempDir())
	root, other := t.TempDir(), t.TempDir()
	resourceTestWrite(t, &pencodeProject{Path: root}, "file.txt", "text")
	resourceTestWrite(t, &pencodeProject{Path: other}, "other.txt", "other")
	s.OpenPaths(filepath.Join(root, "file.txt"), filepath.Join(other, "other.txt"))
	settleExternalPaths(t, s, m)
	if s.recentProjects.count != 0 {
		t.Fatal("opening files remembered their parent directories as projects")
	}
	m.discardProject(m.activeProjectState())
	if h := loadPencodeRecentProjects(filepath.Dir(s.recentProjects.file)); h.count != 0 {
		t.Fatal("a closed file's parent directory reappeared after reloading history")
	}
	// Explicitly opening the same directory must still record the project.
	s.OpenPaths(root)
	settleExternalPaths(t, s, m)
	if err := loadProjectAndWait(t, m, other); err != nil {
		t.Fatal(err)
	}
	s.OpenPaths(filepath.Join(root, "file.txt"))
	settleExternalPaths(t, s, m)
	for len(m.projects) != 0 {
		m.discardProject(m.projects[len(m.projects)-1])
	}
	h := loadPencodeRecentProjects(filepath.Dir(s.recentProjects.file))
	if h.count != 2 || h.entries[0].Path != other || h.entries[1].Path != root {
		t.Fatal("file opens changed persisted project history or explicit folder opens were lost")
	}
}

func TestExternalPathsReuseDirtyPreviewAndContinueAfterFailure(t *testing.T) {
	s, m := externalTestSession(t)
	root := t.TempDir()
	resourceTestWrite(t, &pencodeProject{Path: root}, "first.txt", "disk")
	resourceTestWrite(t, &pencodeProject{Path: root}, "second.txt", "second")
	s.OpenPaths(filepath.Join(root, "first.txt"))
	settleExternalPaths(t, s, m)
	p, f := m.activeProjectState(), m.activeFile()
	f.Preview, p.PreviewPath = true, f.Path
	m.updateActiveContent("unsaved")
	if err := os.Remove(filepath.Join(root, "first.txt")); err != nil {
		t.Fatal(err)
	}
	s.OpenPaths(filepath.Join(root, "missing.txt"), filepath.Join(root, "second.txt"), filepath.Join(root, "first.txt"))
	settleExternalPaths(t, s, m)
	if len(m.projects) != 1 || len(p.Tabs) != 2 || m.activeFile() != f || !f.Dirty || f.content() != "unsaved" || f.Preview || p.PreviewPath != "" {
		t.Fatal("reopening an external file replaced or failed to select its unsaved tab")
	}
}

func TestExternalPathsDifferentDirectoriesAndSymlink(t *testing.T) {
	s, m := externalTestSession(t)
	a, b := t.TempDir(), t.TempDir()
	resourceTestWrite(t, &pencodeProject{Path: a}, "a.txt", "a")
	resourceTestWrite(t, &pencodeProject{Path: b}, "b.txt", "b")
	s.OpenPaths(filepath.Join(a, "a.txt"), filepath.Join(b, "b.txt"))
	settleExternalPaths(t, s, m)
	if len(m.projects) != 2 || m.activeFile().content() != "b" {
		t.Fatal("files from different directories did not get separate workspaces")
	}
	link := filepath.Join(t.TempDir(), "alias.txt")
	if err := os.Symlink(filepath.Join(a, "a.txt"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	s.OpenPaths(link)
	settleExternalPaths(t, s, m)
	if len(m.projects) != 2 || m.activeFile().content() != "a" || len(m.activeProjectState().Tabs) != 1 {
		t.Fatal("symlink did not resolve to the existing document")
	}
}

func TestExternalPathsIdleDoesNotAllocate(t *testing.T) {
	s := &Session{}
	m := &workspaceModel{}
	if got := testing.AllocsPerRun(100, func() { s.pollExternalPaths(m) }); got != 0 {
		t.Fatalf("idle external file handling allocated %g objects", got)
	}
}

func TestExternalPathsNativeDropUpdatesEditor(t *testing.T) {
	app, s := newPencodeTestApp(t)
	app.AddInputHandler(input.PhaseGlobal, s.HandleFileDrop)
	m := s.active.model
	p := m.activeProjectState()
	resourceTestWrite(t, p, "dropped.txt", "dropped content")
	state, _ := app.World.GetResource[*input.State]()
	state.Enqueue(input.Event{Kind: input.EventFilesDropped, File: filepath.Join(p.Path, "dropped.txt")})
	deadline := time.Now().Add(5 * time.Second)
	for m.activePath != "dropped.txt" {
		languageInteractionStep(t, app)
		if time.Now().After(deadline) {
			t.Fatal("native drop did not open the file")
		}
		time.Sleep(time.Millisecond)
	}
	languageInteractionStep(t, app)
	f := m.activeFile()
	m.updateActiveContent("unsaved dropped content")
	// Reopen the original tab, then drop the cached unsaved file again. Both
	// selection and native textarea value must change in the same frame.
	m.selectProjectTab(p, p.Tabs[0])
	state.Enqueue(input.Event{Kind: input.EventFilesDropped, File: filepath.Join(p.Path, "dropped.txt")})
	languageInteractionStep(t, app)
	field := m.fontResources.Ref[engineui.TextField](m.editor.control)
	if m.activeFile() != f || !f.Dirty || field == nil || field.Text().String() != "unsaved dropped content" {
		t.Fatalf("cached drop did not update the native editor: file=%p want=%p field=%+v", m.activeFile(), f, field)
	}
}

func TestExternalPathsLauncherDropEntersWorkspace(t *testing.T) {
	app, s := newPencodeTestLauncher(t)
	app.AddInputHandler(input.PhaseGlobal, s.HandleFileDrop)
	root := t.TempDir()
	for _, name := range []string{"first.txt", "second.txt"} {
		resourceTestWrite(t, &pencodeProject{Path: root}, name, name)
	}
	state, _ := app.World.GetResource[*input.State]()
	state.Enqueue(input.Event{Kind: input.EventFilesDropped, File: filepath.Join(root, "first.txt")})
	state.Enqueue(input.Event{Kind: input.EventFilesDropped, File: filepath.Join(root, "second.txt")})
	deadline := time.Now().Add(5 * time.Second)
	for {
		languageInteractionStep(t, app)
		m := s.active.model
		if s.active.screen == pencodeWorkspace && m.activePath == "second.txt" {
			if !slices.Equal(m.activeProjectState().Tabs, []string{"first.txt", "second.txt"}) {
				t.Fatal("scene switch lost a dropped file")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("launcher drop did not enter the workspace")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestExternalPathsRemoteConnectionNeverSubmitsLocalIO(t *testing.T) {
	s, m := externalTestSession(t)
	s.ConfigureConnection(nil, connection.Profile{})
	s.connection.workspaceProfile = connection.Profile{Directory: "/remote"}
	s.connection.workspaceProfile.SSH.Host = "remote"
	// A NUL cannot be passed to a process, so this checks failed local-window
	// dispatch without launching a second GUI or replacing the remote backend.
	s.OpenPaths("invalid\x00path")
	if !s.pollExternalPaths(m) || m.fileIO != nil || len(s.externalPaths) != 0 || m.status == "" || s.connection.workspaceProfile.SSH.Host != "remote" {
		t.Fatal("a local drop used or replaced the remote connection")
	}
}
