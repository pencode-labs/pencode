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
	"github.com/diiyw/onix/scene"
	engineui "github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/client"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func TestPencodeCustomEditorProcess(t *testing.T) {
	if !slices.Contains(os.Args, "pencode-editor-fixture") {
		return
	}
	err := peb.ServeEditors(os.Stdin, os.Stdout, peb.Contributions{Actions: []peb.Action{{ID: "source", Text: "Custom source", Location: peb.OpenWith, Default: true, Selectors: []peb.DocumentSelector{{Match: peb.FileSuffix, Pattern: ".go"}}}}}, func(message peb.EditorMessage) (peb.EditorReply, error) {
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
	source := file.content()
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
	surface, _ := model.fontResources.Get[htmlui.Surface](model.surface)
	if !strings.Contains(surface.CSS, definition.CSS) {
		t.Fatal("theme synchronization discarded extension styles")
	}
	textEntity, _ := model.fontResources.FindPart[engineui.ValuePart, scene.Text2D](control)
	textValue, _ := model.fontResources.Get[scene.Text2D](textEntity)
	if textValue.Color != graphics.ColorFromRGBA(255, 0, 0, 255) {
		t.Fatalf("extension CSS did not reach the retained input: %v", textValue.Color)
	}
	// Use the retained HTML input event stream to exercise the actual component
	// subscription, process exchange, host edit history and dirty tab behavior.
	value := source + "// edited through extension\n"
	model.fontResources.SetControlValue(control, value)
	app.World.WriteMessage(htmlui.Event{Entity: model.surface, Target: control, Kind: htmlui.EventInput, Value: value, Bubbles: true})
	wait(func() bool { return file.content() == value && !model.customDocuments[file].busy() })
	if !file.Dirty || len(file.undo) != 1 || model.customDocuments[file].definition != definition {
		t.Fatal("input lost history or replaced the retained DOM")
	}
	if current, _ := model.fontResources.HTMLControl(model.surface, "pencode-extension-source"); current != control {
		t.Fatal("input replaced the textarea")
	}
	if field, ok := model.fontResources.Get[engineui.TextField](control); !ok || field.Text().String() != value {
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
	if !model.undoActiveContent(model.fontResources) || file.content() != source {
		t.Fatal("extension edits did not use host undo")
	}
	wait(func() bool {
		return !model.customDocuments[file].busy() && model.customDocuments[file].content == file.content()
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
		{Action: peb.Action{ID: "extensions/test/suffix", Text: "Suffix", Location: peb.OpenWith, Default: true, Selectors: []peb.DocumentSelector{{Match: peb.FileSuffix, Pattern: ".ts"}}}},
		{Action: peb.Action{ID: "extensions/test/long", Text: "Long", Location: peb.OpenWith, Default: true, Selectors: []peb.DocumentSelector{{Match: peb.FileSuffix, Pattern: ".d.ts"}}}},
		{Action: peb.Action{ID: "extensions/test/exact", Text: "Exact", Location: peb.OpenWith, Default: true, Selectors: []peb.DocumentSelector{{Match: peb.FileName, Pattern: "exact.d.ts"}}}},
		{Action: peb.Action{ID: "extensions/test/image", Text: "Image", Location: peb.OpenWith, Selectors: []peb.DocumentSelector{{Match: peb.FileSuffix, Pattern: ".png"}}}},
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
	value := file.content() + "// final edit\n"
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
	if file.content() != value || !file.Dirty || model.activeContent() != "other" || model.closing.targets[0].file != file {
		t.Fatal("reply changed the active file or skipped the unsaved prompt")
	}
	model.clearCloseRequest()
	// A reload/undo during an outstanding request must win over its old reply.
	document.enqueue(peb.EditorEvent{Kind: "input", Ref: "source", Value: "stale"})
	document.dispatch()
	file.contentSnapshot = snapshotText("newer host content")
	wait(func() bool { return !document.busy() })
	if file.content() != "newer host content" {
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

func TestPencodeCustomViewLinksOpenWorkspaceDocuments(t *testing.T) {
	model, project := resourceTestModel(t)
	resourceTestWrite(t, project, "docs/README.md", "# Readme")
	resourceTestWrite(t, project, "guide with spaces.md", "# Guide")
	openProjectFile(project, "docs/README.md")
	model.syncActiveProjectFields()
	document := &pencodeCustomDocument{model: model, project: project, file: model.activeFile(), process: &extensions.EditorSession{}}
	view := &peb.EditorView{HTML: `<a data-ref="guide" href="../guide%20with%20spaces.md">Guide</a><a data-ref="site" href="https://example.com">Site</a><a hidden data-ref="hidden" href="hidden.md">Hidden</a><a data-ref="owned" href="owned.md">Owned</a>`, Events: []peb.EditorBinding{{Ref: "owned", Kind: "click"}}}
	if err := document.setView(view); err != nil {
		t.Fatal(err)
	}
	if len(document.links) != 2 || document.links["site"] != "https://example.com" {
		t.Fatal(document.links)
	}
	document.definition.Behavior.HandleEvent(&htmlui.ComponentEventContext[*pencodeCustomDocument, struct{}]{Props: &document, Ref: "guide", Event: htmlui.Event{Kind: htmlui.EventClick}})
	if err := awaitFileIO(t, model); err != nil {
		t.Fatal(err)
	}
	if file := model.activeFile(); file == nil || file.Path != "guide with spaces.md" {
		t.Fatal("link did not open its document", file)
	}
	if document.count != 0 {
		t.Fatal("host link was forwarded to the extension")
	}
	for _, href := range []string{"../../outside.md", "javascript:alert(1)", "file:///tmp/private.md", "#section"} {
		document.openLink(href)
		if model.activeFile().Path != "guide with spaces.md" {
			t.Fatalf("invalid link changed the active document: %q", href)
		}
	}
}

func TestPencodeOpenWithActionsReplaceAndSplit(t *testing.T) {
	// Headless retained HTML controls; no editor window or screenshots.
	app, model, _, state := languageInteractionFixture(t)
	file := model.activeFile()
	bundle := installPencodeEditorFixture(t, model.backend)
	bundle.Editors[0].Default = false
	model.importEditors(bundle.Editors)
	step := func() {
		t.Helper()
		markPencodeRootDirtyByModel(model.fontResources, model)
		languageInteractionStep(t, app)
		languageInteractionStep(t, app)
	}
	wait := func(ready func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			step()
			if ready() {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatal("open-with view did not settle")
	}
	step()
	if !model.textEditorActive() {
		t.Fatal("optional view replaced source before a click")
	}
	id := bundle.Editors[0].ID
	editorContextClick(t, app, model, state, "action-"+id)
	wait(func() bool { d := model.customDocuments[file]; return d != nil && d.definition != nil && !d.busy() })
	if model.textEditorActive() || file.editorSplit {
		t.Fatal("open-with action did not replace source")
	}
	body := editorContextBounds(t, model, "editor-body")
	full := editorContextBounds(t, model, "custom-editor")
	if full.Width < body.Width-2 || full.X < body.X || full.X > body.X+1 {
		t.Fatalf("single view did not fill the body: %+v %+v", full, body)
	}
	editorContextClick(t, app, model, state, "editor-split")
	step()
	source := editorContextBounds(t, model, "editor")
	preview := editorContextBounds(t, model, "custom-editor")
	if !model.textEditorActive() || !file.editorSplit || source.X+source.Width > preview.X || preview.X < body.X+body.Width/2-1 || preview.Width > body.Width/2+1 {
		t.Fatalf("incorrect split: source=%+v preview=%+v body=%+v", source, preview, body)
	}
	// Switching an action in split mode replaces only the extension pane.
	alternate := bundle.Editors[0]
	alternate.ID, alternate.Text = "extensions/source/alternate", "Alternate view"
	model.importEditors([]extensions.Editor{alternate})
	step()
	editorContextClick(t, app, model, state, "action-"+alternate.ID)
	wait(func() bool {
		d := model.customDocuments[file]
		return d != nil && d.provider.ID == alternate.ID && d.definition != nil && !d.busy()
	})
	if !file.editorSplit || !model.textEditorActive() {
		t.Fatal("switching extension views collapsed the split")
	}
	document := model.customDocuments[file]
	updated := alternate.Action
	updated.Tooltip = "Updated preview"
	if !model.updateEditorAction(updated) {
		t.Fatal("live open-with action was not updated")
	}
	step()
	if model.customDocuments[file] != document {
		t.Fatal("appearance update replaced a document session")
	}
	// Native edits still reach the extension snapshot while both panes are visible.
	content := file.content() + "// split edit\n"
	model.updateActiveContent(content)
	wait(func() bool { d := model.customDocuments[file]; return !d.busy() && d.content == content })
	if !file.Dirty {
		t.Fatal("split edit did not use host history")
	}
	editorContextClick(t, app, model, state, "editor-split")
	step()
	if model.textEditorActive() || file.editorSplit {
		t.Fatal("collapse did not retain the selected extension")
	}
	editorContextClick(t, app, model, state, "editor-text")
	wait(func() bool { return model.customDocuments[file] == nil })
	if !model.textEditorActive() || file.editorSplit || file.content() != content {
		t.Fatal("returning to source lost content or layout")
	}
	// The layout button can also open a preview directly from source.
	editorContextClick(t, app, model, state, "editor-split")
	wait(func() bool { d := model.customDocuments[file]; return d != nil && d.definition != nil && !d.busy() })
	if !model.textEditorActive() || !file.editorSplit {
		t.Fatal("split from source did not select the preview")
	}
	if allocs := testing.AllocsPerRun(100, func() { model.syncActions(); model.fileEditor(file); model.textEditorActive() }); allocs != 0 {
		t.Fatalf("idle open-with state allocated %g", allocs)
	}
	model.removeEditors("extensions/source/")
	step()
	if !model.textEditorActive() {
		t.Fatal("removing the provider did not restore source")
	}
}
