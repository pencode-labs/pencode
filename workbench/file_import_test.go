package workbench

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/onix-engine/onix/input"
)

func TestExternalFilePasteBatchOverwriteAndCancel(t *testing.T) {
	model, project := resourceTestModel(t)
	source := t.TempDir()
	var files []string
	for _, name := range []string{"first.txt", "中文.txt", "last.txt"} {
		filename := filepath.Join(source, name)
		if err := os.WriteFile(filename, []byte("new "+name), 0644); err != nil {
			t.Fatal(err)
		}
		files = append(files, filename)
	}
	resourceTestWrite(t, project, "中文.txt", "keep")
	resourceTestWrite(t, project, "last.txt", "keep last")
	if err := model.submitResourceIO(&fileIOJob{kind: fileImport, project: project, imports: files}); err != nil {
		t.Fatal(err)
	}
	if err := awaitFileIO(t, model); err == nil {
		t.Fatal("missing collision")
	}
	if c := &model.fileContext; c.dialog != "overwrite" || c.project != project || len(c.transfer.imports) != 2 || c.transfer.imports[0] != files[1] {
		t.Fatalf("incorrect pending import: %+v", c)
	}
	if got, _ := os.ReadFile(filepath.Join(project.Path, "first.txt")); string(got) != "new first.txt" {
		t.Fatal("files before the collision were not copied")
	}
	if got, _ := os.ReadFile(filepath.Join(project.Path, "中文.txt")); string(got) != "keep" {
		t.Fatal("overwrote before confirmation")
	}
	if err := fileDialogAndWait(t, model); err == nil {
		t.Fatal("second collision did not pause the batch")
	}
	if c := &model.fileContext; c.dialog != "overwrite" || len(c.transfer.imports) != 1 || c.transfer.imports[0] != files[2] {
		t.Fatalf("batch did not advance to the next collision: %+v", c)
	}
	if got, _ := os.ReadFile(filepath.Join(project.Path, "中文.txt")); string(got) != "new 中文.txt" {
		t.Fatal("confirmed overwrite was not applied")
	}
	model.handleFileContextEvent(nil, "file-dialog-cancel", "")
	if model.fileContext.dialog != "" || len(model.fileIO.pending) != 0 {
		t.Fatal("cancel retained the import")
	}
	if got, _ := os.ReadFile(filepath.Join(project.Path, "last.txt")); string(got) != "keep last" {
		t.Fatal("cancel changed the conflicting file")
	}
	for _, file := range files {
		if _, err := os.Stat(file); err != nil {
			t.Fatal("paste removed a source file", err)
		}
	}
}

func TestExternalFilePasteReloadsCleanTabsAndPreservesDrafts(t *testing.T) {
	for _, dirty := range []bool{false, true} {
		t.Run(map[bool]string{false: "clean", true: "draft"}[dirty], func(t *testing.T) {
			model, project := resourceTestModel(t)
			resourceTestWrite(t, project, "note.txt", "saved")
			openProjectFile(project, "note.txt")
			model.syncActiveProjectFields()
			if dirty {
				model.updateActiveContent("draft")
			}
			source := filepath.Join(t.TempDir(), "note.txt")
			if err := os.WriteFile(source, []byte("external"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := model.submitResourceIO(&fileIOJob{kind: fileImport, project: project, imports: []string{source}}); err != nil {
				t.Fatal(err)
			}
			if err := awaitFileIO(t, model); err == nil || model.fileContext.dialog != "overwrite" {
				t.Fatal("missing overwrite confirmation", err)
			}
			if err := fileDialogAndWait(t, model); err != nil {
				t.Fatal(err)
			}
			want := "external"
			if dirty {
				want = "draft"
			}
			if model.activeContent() != want || model.activeFile().Dirty != dirty {
				t.Fatalf("buffer after overwrite: %q dirty=%v", model.activeContent(), model.activeFile().Dirty)
			}
		})
	}
}

func TestExternalFileOverwriteDialogUsesExistingControls(t *testing.T) {
	app, model, _, state := languageInteractionFixture(t)
	project := model.activeProjectState()
	source := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(source, []byte("package pasted\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := model.submitResourceIO(&fileIOJob{kind: fileImport, project: project, imports: []string{source}}); err != nil {
		t.Fatal(err)
	}
	if err := awaitFileIO(t, model); err == nil {
		t.Fatal("missing conflict")
	}
	markPencodeRootDirtyByModel(model.fontResources, model)
	languageInteractionStep(t, app)
	languageInteractionStep(t, app)
	node, ok := model.fontResources.HTMLNode(model.surface, "file-dialog-name")
	_, hidden := node.Attribute("hidden")
	if !ok || !hidden {
		t.Fatal("overwrite dialog exposed the rename input")
	}
	editorContextClick(t, app, model, state, "file-dialog-submit")
	if err := awaitFileIO(t, model); err != nil {
		t.Fatal(err)
	}
	if model.fileContext.dialog != "" || model.activeContent() != "package pasted\n" {
		t.Fatal("overwrite button did not resume paste")
	}
	idle := input.NewState()
	if n := testing.AllocsPerRun(100, func() { model.fileTreePasteShortcut(model.fontResources, idle, "windows") }); n != 0 {
		t.Fatalf("idle file paste allocated %g", n)
	}
}

func TestFileTreePasteShortcutTargetsFocusedDirectory(t *testing.T) {
	app, model, _, _ := languageInteractionFixture(t)
	project, world := model.activeProjectState(), model.fontResources
	resourceTestWrite(t, project, "target/keep.txt", "keep")
	model.startProjectScan(project, false)
	waitPencodeProjectScan(t, project, func() { languageInteractionStep(t, app) })
	languageInteractionStep(t, app)
	model.fileClipboard = pencodeFileClipboard{project: project, path: "main.go"}
	control, ok := world.HTMLControl(model.surface, model.projectTreeRowID(project, "target"))
	if !ok || !world.Focus(control) {
		t.Fatal("could not focus directory row")
	}
	state := input.NewState()
	state.PressKey(input.KeyLeftControl)
	state.PressKey(input.KeyV)
	before := model.activeContent()
	if !model.handleShortcutKeys(world, state, "windows") || model.fileContext.open {
		t.Fatal("paste required an open context menu")
	}
	if err := awaitFileIO(t, model); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(project.Path, "target/main.go")); err != nil || string(got) != before || model.activeContent() != before {
		t.Fatalf("paste did not use the focused directory: %q %v", got, err)
	}
}
