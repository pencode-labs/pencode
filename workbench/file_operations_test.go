package workbench

import (
	"os"
	"path/filepath"
	"testing"
)

func resourceTestModel(t *testing.T) (*workspaceModel, *pencodeProject) {
	t.Helper()
	project := &pencodeProject{Path: t.TempDir(), Name: "test", Files: make(map[string]*openFile), ExpandedDirs: make(map[string]bool)}
	connectPencodeProject(t, project)
	t.Cleanup(project.stopScan)
	model := &workspaceModel{backend: project.backend, projects: []*pencodeProject{project}, activeProjectIndex: 0}
	t.Cleanup(model.stopFileIO)
	model.syncActiveProjectFields()
	return model, project
}

func resourceTestWrite(t *testing.T, project *pencodeProject, relative, content string) {
	t.Helper()
	filename := filepath.Join(project.Path, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestProjectResourcesRenameDirectoryPreservesBuffersAndHistory(t *testing.T) {
	model, project := resourceTestModel(t)
	resourceTestWrite(t, project, "src/nested/main.go", "package saved\n")
	resourceTestWrite(t, project, "other.go", "package other\n")
	openProjectFile(project, "other.go")
	openProjectFile(project, "src/nested/main.go")
	model.syncActiveProjectFields()
	model.updateActiveContent("package unsaved\n")
	before := model.activeFile()
	project.ExpandedDirs["src"], project.ExpandedDirs["src/nested"] = true, true
	model.fileContext = pencodeFileContext{project: project, path: "src", directory: true, dialog: "rename", name: "lib"}
	if err := fileDialogAndWait(t, model); err != nil {
		t.Fatal(err)
	}
	file := project.Files["lib/nested/main.go"]
	if file == nil || file == before || !file.Dirty || file.content() != before.content() || len(file.undo) != len(before.undo) || project.ActivePath != file.Path || project.Files["src/nested/main.go"] != nil {
		t.Fatal("directory rename lost open document state")
	}
	if !project.ExpandedDirs["lib/nested"] || len(project.Tabs) != 2 || project.Tabs[1] != "lib/nested/main.go" {
		t.Fatal("directory rename did not rebase tabs and expansion state")
	}
	if !model.undoActiveContent(nil) || model.activeContent() != "package saved\n" {
		t.Fatal("renamed file lost undo history")
	}
	resourceTestWrite(t, project, "existing.txt", "keep")
	model.fileContext = pencodeFileContext{project: project, path: "lib/nested/main.go", dialog: "rename", name: "existing.txt"}
	resourceTestWrite(t, project, "lib/nested/existing.txt", "keep")
	if err := fileDialogAndWait(t, model); err == nil || project.Files["lib/nested/main.go"] != file {
		t.Fatal("conflicting rename overwrote a resource or changed its buffer")
	}
	model.fileContext = pencodeFileContext{project: project, path: "lib", directory: true, dialog: "delete"}
	if err := fileDialogAndWait(t, model); err != nil {
		t.Fatal(err)
	}
	if len(project.Tabs) != 1 || project.ActivePath != "other.go" || project.Files["lib/nested/main.go"] != nil {
		t.Fatal("deleting a directory left stale tabs")
	}
}

func TestProjectResourcesCopyMoveAndCrossProjectBuffers(t *testing.T) {
	model, project := resourceTestModel(t)
	resourceTestWrite(t, project, "src/nested/a.txt", "disk text")
	resourceTestWrite(t, project, "target/keep.txt", "keep")
	openProjectFile(project, "src/nested/a.txt")
	model.syncActiveProjectFields()
	model.updateActiveContent("unsaved text")
	model.fileClipboard = pencodeFileClipboard{project: project, path: "src"}
	if err := pasteResourceAndWait(t, model, project, "src/nested"); err == nil {
		t.Fatal("copied a directory into its own descendant")
	}
	if err := pasteResourceAndWait(t, model, project, "."); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(filepath.Join(project.Path, "src 副本 1/nested/a.txt")); err != nil || string(content) != "disk text" {
		t.Fatalf("directory copy: %q %v", content, err)
	}
	model.fileClipboard = pencodeFileClipboard{project: project, path: "src/nested/a.txt"}
	if err := pasteResourceAndWait(t, model, project, "src/nested"); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(filepath.Join(project.Path, "src/nested/a 副本 1.txt")); err != nil || string(content) != "disk text" {
		t.Fatalf("file copy: %q %v", content, err)
	}
	model.fileClipboard = pencodeFileClipboard{project: project, path: "src", cut: true}
	if err := pasteResourceAndWait(t, model, project, "target"); err != nil {
		t.Fatal(err)
	}
	if project.ActivePath != "target/src/nested/a.txt" || model.activeContent() != "unsaved text" || !model.activeFile().Dirty || model.fileClipboard.project != nil {
		t.Fatal("move lost the active buffer or retained cut state")
	}
	other := &pencodeProject{Path: t.TempDir(), Files: make(map[string]*openFile)}
	attachPencodeProject(t, other, project.backend)
	t.Cleanup(other.stopScan)
	model.projects = append(model.projects, other)
	model.fileClipboard = pencodeFileClipboard{project: project, path: "target/src/nested/a.txt", cut: true}
	if err := pasteResourceAndWait(t, model, other, ""); err != nil {
		t.Fatal(err)
	}
	file := other.Files["a.txt"]
	if file == nil || !file.Dirty || file.content() != "unsaved text" || len(file.undo) == 0 || len(project.Tabs) != 0 || other.ActivePath != "a.txt" {
		t.Fatal("cross-project move lost the unsaved document")
	}
	if content, err := os.ReadFile(filepath.Join(other.Path, "a.txt")); err != nil || string(content) != "disk text" {
		t.Fatalf("cross-project move changed saved bytes: %q %v", content, err)
	}
}

func TestProjectResourcesCreationValidationAndRootProtection(t *testing.T) {
	model, project := resourceTestModel(t)
	for _, name := range []string{"", ".", "..", "../escape", "nested/name", "bad\\name"} {
		model.fileContext = pencodeFileContext{project: project, directory: true, dialog: "new-file", name: name}
		if err := fileDialogAndWait(t, model); err == nil {
			t.Fatalf("accepted invalid name %q", name)
		}
	}
	for _, action := range []string{"rename", "delete"} {
		model.fileContext = pencodeFileContext{project: project, directory: true, dialog: action, name: "renamed"}
		if err := fileDialogAndWait(t, model); err == nil {
			t.Fatalf("%s allowed on the project root", action)
		}
	}
	model.fileContext = pencodeFileContext{project: project, directory: true, dialog: "new-folder", name: "资料"}
	if err := fileDialogAndWait(t, model); err != nil {
		t.Fatal(err)
	}
	model.fileContext = pencodeFileContext{project: project, path: "资料", directory: true, dialog: "new-file", name: "笔记.txt"}
	if err := fileDialogAndWait(t, model); err != nil {
		t.Fatal(err)
	}
	if project.ActivePath != "资料/笔记.txt" || !project.ExpandedDirs["资料"] {
		t.Fatal("new nested file was not opened and revealed")
	}
	if err := fileDialogAndWait(t, model); err == nil {
		t.Fatal("creating an existing file was accepted")
	}
}
