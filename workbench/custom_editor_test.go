package workbench

import (
	"context"
	"encoding/json"
	"html"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/graphics"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/projects/pencode/client"
	"github.com/diiyw/onix/projects/pencode/extensions"
	"github.com/diiyw/onix/projects/pencode/peb"
	"github.com/diiyw/onix/scene"
	engineui "github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
)

func TestPencodeCustomEditorProcess(t *testing.T) {
	if !slices.Contains(os.Args, "pencode-editor-fixture") {
		return
	}
	err := peb.ServeEditors(os.Stdin, os.Stdout, peb.Contributions{Editors: []peb.Editor{{ID: "source", Label: "Custom source", Default: true, Selectors: []peb.DocumentSelector{{Match: peb.FileSuffix, Pattern: ".go"}}}}}, func(message peb.EditorMessage) (peb.EditorReply, error) {
		if message.Event.Kind == "input" {
			return peb.EditorReply{Content: &message.Event.Value}, nil
		}
		return peb.EditorReply{View: &peb.EditorView{HTML: `<img id="pencode-extension-preview" src="resources/preview.svg" alt="Preview"><textarea id="pencode-extension-source" data-ref="source" aria-label="Custom source">` + html.EscapeString(message.Content) + `</textarea>`, CSS: `textarea { width: 100%; min-height: 300px; color: red; }`, Events: []peb.EditorBinding{{Ref: "source", Kind: "input"}}}}, nil
	})
	if err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func installPencodeEditorFixture(t *testing.T, backend *client.Client) *extensions.Bundle {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "source")
	if err := os.MkdirAll(filepath.Join(directory, "resources"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "resources", "preview.svg"), []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16"><rect width="16" height="16" fill="#f00"/></svg>`), 0644); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(directory, "fixture")
	if err := os.Link(executable, entry); err != nil {
		source, err := os.Open(executable)
		if err != nil {
			t.Fatal(err)
		}
		defer source.Close()
		target, err := os.OpenFile(entry, os.O_CREATE|os.O_WRONLY, 0755)
		if err != nil {
			t.Fatal(err)
		}
		_, err = io.Copy(target, source)
		closeErr := target.Close()
		if err != nil {
			t.Fatal(err)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	data, err := json.Marshal(extensions.Manifest{ID: "source", Entry: "fixture", Args: []string{"-test.run=^TestPencodeCustomEditorProcess$", "--", "pencode-editor-fixture"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "resources/extension.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	_, bundle, err := extensions.Install(context.Background(), backend, directory)
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}

func TestPencodeCustomEditorDOMEventsAndHostHistory(t *testing.T) {
	app, session := newPencodeTestApp(t)
	model := session.active.model
	file := model.activeFile()
	source := file.Content
	model.importEditors(installPencodeEditorFixture(t, session.backend).Editors)
	markPencodeRootDirtyByModel(model.fontResources, model)
	wait := func(ready func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := app.Step(); err != nil {
				t.Fatal(err)
			}
			if ready() {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatalf("custom editor did not settle: status=%q message=%q", model.status, model.customDocuments[file].message)
	}
	var control ecs.Entity
	wait(func() bool {
		var ok bool
		control, ok = model.fontResources.HTMLControl(model.surface, "pencode-extension-source")
		return ok
	})
	if len(model.extensionImages["peb://source/resources/preview.svg"]) == 0 {
		t.Fatal("custom editor asset was not fetched through the server")
	}
	if model.textEditorActive() || model.highlightRequest.file != nil {
		t.Fatal("custom view left the native syntax editor active")
	}
	definition := model.customDocuments[file].definition
	model.selectTheme("light-plus")
	markPencodeRootDirtyByModel(model.fontResources, model)
	for range 2 {
		if _, err := app.Step(); err != nil {
			t.Fatal(err)
		}
	}
	surface, _ := module.ContentGet[htmlui.Surface](model.fontResources, model.surface)
	if !strings.Contains(surface.CSS, definition.CSS) {
		t.Fatal("theme synchronization discarded extension styles")
	}
	textEntity, _ := module.ContentFindPart[engineui.ValuePart, scene.Text2D](model.fontResources, control)
	textValue, _ := module.ContentGet[scene.Text2D](model.fontResources, textEntity)
	if textValue.Color != graphics.ColorFromRGBA(255, 0, 0, 255) {
		t.Fatalf("extension CSS did not reach the retained input: %v", textValue.Color)
	}
	// Use the retained HTML input event stream to exercise the actual component
	// subscription, process exchange, host edit history and dirty tab behavior.
	value := source + "// edited through extension\n"
	model.fontResources.SetControlValue(control, value)
	ecs.WriteMessage(app.World, htmlui.Event{Entity: model.surface, Target: control, Kind: htmlui.EventInput, Value: value, Bubbles: true})
	wait(func() bool { return file.Content == value && !model.customDocuments[file].busy() })
	if !file.Dirty || len(file.undo) != 1 || model.customDocuments[file].definition != definition {
		t.Fatal("input lost history or replaced the retained DOM")
	}
	if current, _ := model.fontResources.HTMLControl(model.surface, "pencode-extension-source"); current != control {
		t.Fatal("input replaced the textarea")
	}
	if field, ok := module.ContentGet[engineui.TextField](model.fontResources, control); !ok || field.Value != value {
		t.Fatal("custom textarea content drifted")
	}
	model.saveActiveFile()
	if err := awaitFileIO(t, model); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(model.projectPath, file.Path))
	if err != nil || string(data) != value || file.Dirty {
		t.Fatal("extension edit did not save through the host")
	}
	if !model.undoActiveContent(model.fontResources) || file.Content != source {
		t.Fatal("extension edits did not use host undo")
	}
	wait(func() bool {
		return !model.customDocuments[file].busy() && model.customDocuments[file].content == file.Content
	})
	if !model.selectEditor(pencodeTextEditor) {
		t.Fatal("could not reopen with native editor")
	}
	markPencodeRootDirtyByModel(model.fontResources, model)
	wait(func() bool { return model.customDocuments[file] == nil })
	if !model.textEditorActive() {
		t.Fatal("native editor was not restored")
	}
}

func TestPencodeEditorAssociationsAndBinarySave(t *testing.T) {
	model := &workspaceModel{}
	model.importEditors([]extensions.Editor{
		{Editor: peb.Editor{ID: "extensions/test/suffix", Default: true, Selectors: []peb.DocumentSelector{{Match: peb.FileSuffix, Pattern: ".ts"}}}},
		{Editor: peb.Editor{ID: "extensions/test/long", Default: true, Selectors: []peb.DocumentSelector{{Match: peb.FileSuffix, Pattern: ".d.ts"}}}},
		{Editor: peb.Editor{ID: "extensions/test/exact", Default: true, Selectors: []peb.DocumentSelector{{Match: peb.FileName, Pattern: "exact.d.ts"}}}},
		{Editor: peb.Editor{ID: "extensions/test/image", Selectors: []peb.DocumentSelector{{Match: peb.FileSuffix, Pattern: ".png"}}}},
	})
	file := &openFile{Name: "exact.d.ts"}
	if got := model.fileEditor(file); got == nil || got.ID != "extensions/test/exact" {
		t.Fatal("exact basename did not win")
	}
	file = &openFile{Name: "other.d.ts"}
	if got := model.fileEditor(file); got == nil || got.ID != "extensions/test/long" {
		t.Fatal("longest suffix did not win")
	}
	if allocs := testing.AllocsPerRun(100, func() { model.fileEditor(file); model.pollCustomEditors() }); allocs != 0 {
		t.Fatalf("idle editor allocated %g times", allocs)
	}
	file.editorID, file.editorGeneration = pencodeTextEditor, 0
	if model.fileEditor(file) != nil {
		t.Fatal("explicit text editor was overridden")
	}
	if !model.canOpenFile("image.png") || model.canOpenFile("image.pdf") {
		t.Fatal("binary associations are incorrect")
	}
	directory := t.TempDir()
	binary := []byte{0, 255, 254, 42}
	if err := os.WriteFile(filepath.Join(directory, "image.png"), binary, 0644); err != nil {
		t.Fatal(err)
	}
	if content, err := readPencodeDocument(filepath.Join(directory, "image.png")); err != nil || content != "" {
		t.Fatal("binary file was read as text")
	}
	if err := savePencodeFile(nil, directory, &openFile{Path: "image.png", Name: "image.png"}); err == nil {
		t.Fatal("binary file was truncated by text save")
	}
	data, _ := os.ReadFile(filepath.Join(directory, "image.png"))
	if !slices.Equal(data, binary) {
		t.Fatal("binary file changed")
	}
	model.removeEditors("extensions/test/")
	if model.canOpenFile("image.png") {
		t.Fatal("removed editor retained its association")
	}
}

func TestPencodeCustomEditorPendingEditSurvivesTabSwitchAndClose(t *testing.T) {
	app, session := newPencodeTestApp(t)
	model := session.active.model
	file, project := model.activeFile(), model.activeProjectState()
	model.importEditors(installPencodeEditorFixture(t, session.backend).Editors)
	markPencodeRootDirtyByModel(model.fontResources, model)
	wait := func(ready func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := app.Step(); err != nil {
				t.Fatal(err)
			}
			if ready() {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatal("custom editor did not settle")
	}
	wait(func() bool {
		document := model.customDocuments[file]
		return document != nil && document.definition != nil && !document.busy()
	})
	document := model.customDocuments[file]
	value := file.Content + "// final edit\n"
	props := document
	document.definition.Behavior.HandleEvent(&htmlui.ComponentEventContext[*pencodeCustomDocument, struct{}]{Props: &props, Ref: "source", Event: htmlui.Event{Kind: htmlui.EventInput, Value: value}})
	model.closeProjectTab(project, file.Path)
	if project.Files[file.Path] != file || model.customClose.action != pencodeCloseTab {
		t.Fatal("closed a clean file before receiving its pending edit")
	}
	if err := os.WriteFile(filepath.Join(project.Path, "other.txt"), []byte("other"), 0644); err != nil {
		t.Fatal(err)
	}
	openFileAndWait(t, model, "other.txt")
	markPencodeRootDirtyByModel(model.fontResources, model)
	wait(func() bool { return model.closing.action == pencodeCloseTab })
	if file.Content != value || !file.Dirty || model.activeContent() != "other" || model.closing.targets[0].file != file {
		t.Fatal("reply changed the active file or skipped the unsaved prompt")
	}
	model.clearCloseRequest()
	// A reload/undo during an outstanding request must win over its old reply.
	document.enqueue(peb.EditorEvent{Kind: "input", Ref: "source", Value: "stale"})
	document.dispatch()
	file.Content = "newer host content"
	wait(func() bool { return !document.busy() })
	if file.Content != "newer host content" {
		t.Fatal("stale reply overwrote the host document")
	}
}

func TestPencodeCustomViewRejectsInvalidBindingsAndHostIDs(t *testing.T) {
	document := &pencodeCustomDocument{}
	for _, view := range []peb.EditorView{
		{HTML: `<button id="editor" data-ref="x">Bad</button>`},
		{HTML: `<button data-ref="x">Bad</button>`, Events: []peb.EditorBinding{{Ref: "missing", Kind: "click"}}},
		{HTML: `<button data-ref="x">Bad</button>`, Events: []peb.EditorBinding{{Ref: "x", Kind: "beforeinput"}}},
	} {
		if document.setView(&view) == nil {
			t.Fatal("accepted invalid extension view")
		}
	}
	if err := document.setView(&peb.EditorView{HTML: `<button data-ref="x">OK</button>`, CSS: `button { color: red; }`, Events: []peb.EditorBinding{{Ref: "x", Kind: "click"}}}); err != nil {
		t.Fatal(err)
	}
	if !document.definition.ScopedCSS || !strings.Contains(document.definition.CSS, "onix-scope-") {
		t.Fatal("extension CSS can escape its component")
	}
}
