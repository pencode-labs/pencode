package workbench

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenPathsFilesAndDirectories(t *testing.T) {
	s := newPencodeTestSession(t, newPencodeBackend(t))
	m := &workspaceModel{session: s, backend: s.backend, activeProjectIndex: -1}
	t.Cleanup(func() {
		for _, p := range m.projects {
			p.stopScan()
			p.stopWatch()
		}
		m.stopFileIO()
		s.Close()
	})
	first, second, folder := t.TempDir(), t.TempDir(), t.TempDir()
	for _, name := range []string{"中文 file.go", "other.txt"} {
		if err := os.WriteFile(filepath.Join(first, name), []byte("original\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(second, "last.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	paths := []string{folder, filepath.Join(first, "中文 file.go"), filepath.Join(first, "other.txt"), filepath.Join(first, "中文 file.go"), filepath.Join(second, "last.json")}
	s.OpenPaths(paths...)
	paths[0] = "invalid" // The pending request owns its path list.
	for len(s.externalPaths) != 0 {
		if !s.pollExternalPaths(m) {
			t.Fatal("startup request did not advance")
		}
		if s.externalJob != nil && !s.externalJob.finished && s.pollExternalPaths(m) {
			t.Fatal("another request ran before the previous file finished opening")
		}
		if err := awaitFileIO(t, m); err != nil {
			t.Fatal(err)
		}
	}
	if len(m.projects) != 3 || len(m.projects[0].Tabs) != 0 || len(m.projects[1].Tabs) != 2 || len(m.projects[2].Tabs) != 1 || m.activePath != "last.json" || m.screen != pencodeWorkspace {
		t.Fatal("files were not pinned in their parent workspaces or directory opened an unwanted file")
	}
	p := m.projects[1]
	f := p.Files["中文 file.go"]
	if f == nil || f.Preview || f.content() != "original\n" || f.document == nil {
		t.Fatal("single file did not open as an editable pinned tab")
	}
	m.activateProject(p)
	m.updateActiveContent("edited\n")
	if err := saveFileAndWait(t, m, p.Path, f); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(first, f.Path))
	if err != nil || string(data) != "edited\n" {
		t.Fatalf("single-file save: %q, %v", data, err)
	}
	if allocations := testing.AllocsPerRun(100, func() { s.pollExternalPaths(m) }); allocations != 0 {
		t.Fatalf("idle path polling allocated %g times", allocations)
	}
}

func TestOpenPathsValidation(t *testing.T) {
	s, m := externalTestSession(t)
	s.OpenPaths()
	if s.externalPaths != nil {
		t.Fatal("empty startup request created work")
	}
	root := t.TempDir()
	t.Chdir(root)
	if err := os.WriteFile("-中文 file.txt", []byte("text"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.OpenPaths("-中文 file.txt")
	settleExternalPaths(t, s, m)
	if m.activeProjectState().Path != root || m.activePath != "-中文 file.txt" {
		t.Fatal("relative file path was not resolved")
	}
	s.OpenPaths("missing.txt")
	settleExternalPaths(t, s, m)
	if m.activePath != "-中文 file.txt" || m.status == "" {
		t.Fatal("invalid path replaced the active file or failed silently")
	}
}

func TestOpenPathOverridesRestoredActiveTab(t *testing.T) {
	m, p := resourceTestModel(t)
	s := newPencodeTestSession(t, p.backend)
	m.session = s
	resourceTestWrite(t, p, "restored.txt", "restored")
	resourceTestWrite(t, p, "requested.txt", "requested")
	state := pencodeProjectState{ActivePath: "restored.txt", Tabs: []pencodeTabState{{Path: "restored.txt"}}}
	if err := writeProjectState(s.projectStateFile(p.Path), &state); err != nil {
		t.Fatal(err)
	}
	m.projects, m.activeProjectIndex = nil, -1
	m.syncActiveProjectFields()
	s.OpenPaths(filepath.Join(p.Path, "requested.txt"))
	t.Cleanup(func() {
		for _, project := range m.projects {
			project.stopScan()
			project.stopWatch()
		}
	})
	settleExternalPaths(t, s, m)
	if m.activePath != "requested.txt" || len(m.tabs) != 2 || m.activeFile().Preview {
		t.Fatalf("restoring project state replaced the requested file selection: active=%q tabs=%v status=%s", m.activePath, m.tabs, m.status)
	}
}
