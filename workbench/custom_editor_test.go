package workbench

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onix-engine/onix/ecs"
	"github.com/onix-engine/onix/graphics"
	"github.com/onix-engine/onix/scene"
	uinode "github.com/onix-engine/onix/scene/node"
	engineui "github.com/onix-engine/onix/ui"
	htmlui "github.com/onix-engine/onix/ui/html"
	"github.com/pencode-labs/pencode/client"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func TestPencodeCustomEditorProcess(t *testing.T) {
	if !slices.Contains(os.Args, "pencode-editor-fixture") {
		return
	}
	err := peb.ServeEditors(os.Stdin, os.Stdout, peb.Contributions{Actions: []peb.Action{{ID: "source", Text: "Custom source", Location: peb.OpenWith, Default: true, Selectors: []peb.DocumentSelector{{Match: peb.FileSuffix, Pattern: ".go"}}}}}, func(message peb.EditorMessage) (peb.EditorReply, error) {
		if markup, ok := strings.CutPrefix(message.Content, "// editor fixture HTML\n"); ok {
			return peb.EditorReply{View: &peb.EditorView{HTML: markup}}, nil
		}
		if message.Binary {
			data, err := os.ReadFile(message.Path)
			if err != nil {
				return peb.EditorReply{}, err
			}
			return peb.EditorReply{View: &peb.EditorView{HTML: "<p>" + html.EscapeString(message.Event.Kind+" "+string(data)) + "</p>"}}, nil
		}
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

func TestBinaryEditorRefreshesOnExternalReplacement(t *testing.T) {
	m, p := resourceTestModel(t)
	editors := installPencodeEditorFixture(t, p.backend).Editors
	editors[0].Selectors = []peb.DocumentSelector{{Match: peb.FileSuffix, Pattern: ".xlsx"}}
	m.importEditors(editors)
	m.startProjectWatch(p)
	t.Cleanup(p.stopWatch)
	waitPencodeScanSignal(t, p.watch.ready)
	resourceTestWrite(t, p, "系统信息.xlsx", "first")
	openProjectFileAndWait(t, m, p, "系统信息.xlsx")
	doc := m.customDocument()
	t.Cleanup(m.closeCustomEditors)
	wait := func(html string) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
			m.pollProjectWatches()
			m.pollFileIO()
			m.pollCustomEditors()
			if doc.view != nil && doc.view.HTML == html {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatalf("expected %s, view=%+v error=%s", html, doc.view, doc.message)
	}
	wait("<p>open first</p>")
	resourceTestWrite(t, p, "temporary", "second")
	if err := os.Rename(filepath.Join(p.Path, "temporary"), filepath.Join(p.Path, "系统信息.xlsx")); err != nil {
		t.Fatal(err)
	}
	wait("<p>documentchange second</p>")
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
	if len(model.extensionImages["peb://source/resources/preview.svg"].data) == 0 {
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
	if len(model.extensionImages) != 0 {
		t.Fatal("returning to source retained preview assets")
	}
	if !model.textEditorActive() {
		t.Fatal("native editor was not restored")
	}
}

func TestPencodeEditorAssociationsAndBinarySave(t *testing.T) {
	model := &workspaceModel{}
	model.importEditors([]extensions.Editor{
		{Action: peb.Action{ID: "extensions/test/suffix", Text: "Suffix", Location: peb.OpenWith, Default: true, Selectors: []peb.DocumentSelector{{Match: peb.FileSuffix, Pattern: ".ts"}}}},
		{Action: peb.Action{ID: "extensions/test/long", Text: "Long", Location: peb.OpenWith, Default: true, Selectors: []peb.DocumentSelector{{Match: peb.FileSuffix, Pattern: ".d.ts"}}}},
		{Action: peb.Action{ID: "extensions/test/alternate", Text: "Alternate", Location: peb.OpenWith, Selectors: []peb.DocumentSelector{{Match: peb.FileSuffix, Pattern: ".d.ts"}}}},
		{Action: peb.Action{ID: "extensions/test/exact", Text: "Exact", Location: peb.OpenWith, Selectors: []peb.DocumentSelector{{Match: peb.FileName, Pattern: "exact.d.ts"}}}},
		{Action: peb.Action{ID: "extensions/test/image", Text: "Image", Location: peb.OpenWith, Selectors: []peb.DocumentSelector{{Match: peb.FileSuffix, Pattern: ".png"}}}},
	})
	file := &openFile{Name: "exact.d.ts"}
	if got := model.fileEditor(file); got == nil || got.ID != "extensions/test/exact" {
		t.Fatal("exact basename did not win")
	}
	file = &openFile{Name: "other.d.ts"}
	if got := model.fileEditor(file); got == nil || got.ID != "extensions/test/long" {
		t.Fatal("expected the default tool matching the longest suffix")
	}
	if allocs := testing.AllocsPerRun(100, func() { model.fileEditor(file); model.pollCustomEditors() }); allocs != 0 {
		t.Fatalf("idle editor allocated %g times", allocs)
	}
	file.editorID, file.editorGeneration = "extensions/test/alternate", 0
	if got := model.fileEditor(file); got == nil || got.ID != file.editorID {
		t.Fatal("explicit tool choice was overridden")
	}
	model.removeEditors(file.editorID)
	if got := model.fileEditor(file); got == nil || got.ID != "extensions/test/long" {
		t.Fatal("removing the selected tool did not activate the next matching tool")
	}
	file.editorID, file.editorGeneration = pencodeTextEditor, 0
	if model.fileEditor(file) != nil {
		t.Fatal("explicit text editor was overridden")
	}
	if !model.canOpenFile("image.png") || !model.canOpenFile("image.pdf") {
		t.Fatal("unsupported files must open a confirmation tab")
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
	if model.fileEditor(&openFile{Name: "image.png"}) != &pencodeImageEditor {
		t.Fatal("removed editor did not fall back to the built-in image preview")
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

func TestCustomEditorRefreshRetainsFocusedInput(t *testing.T) {
	app, session := newPencodeTestApp(t)
	m := session.active.model
	w, file := m.fontResources, m.activeFile()
	provider := &extensions.Editor{Action: peb.Action{ID: "extensions/test/form", Selectors: []peb.DocumentSelector{{Match: peb.FileSuffix, Pattern: ".go"}}}}
	m.customEditors = append(m.customEditors, provider)
	m.editorGeneration++
	file.editorID, file.editorGeneration = provider.ID, 0
	doc := &pencodeCustomDocument{model: m, file: file, provider: provider, project: m.activeProjectState()}
	m.customDocuments = map[*openFile]*pencodeCustomDocument{file: doc}
	show := func(fields string) {
		t.Helper()
		if err := doc.setView(&peb.EditorView{HTML: fields + `<input data-ref="name" value="abc">`}); err != nil {
			t.Fatal(err)
		}
		markPencodeRootDirtyByModel(w, m)
		for range 2 {
			languageInteractionStep(t, app)
		}
	}
	show("")
	id := fmt.Sprintf("pencode-extension-editor/%p/control-name", doc)
	control, ok := w.HTMLControl(m.surface, id)
	if !ok || !w.Focus(control) {
		t.Fatal("cannot focus the custom editor input")
	}
	w.SetControlValue(control, "草稿abc")
	field := w.Mut[engineui.TextField](control)
	field.Anchor, field.Caret = 4, 1
	show(`<input data-ref="filter">`)
	control, ok = w.HTMLControl(m.surface, id)
	field = w.Ref[engineui.TextField](control)
	if !ok || field == nil || w.FocusedControl() != control || field.Value != "草稿abc" || field.Anchor != 4 || field.Caret != 1 {
		t.Fatalf("inserting a sibling input interrupted editing: %+v", field)
	}
}

func TestPencodeCustomViewSparseTextIDs(t *testing.T) {
	document := &pencodeCustomDocument{}
	view := &peb.EditorView{HTML: `<article data-ref="sheet" data-pencode-searchable data-pencode-selectable><div style="overflow:auto"><table><tr><td></td><td id="pencode-extension-kept"></td><td data-ref="dynamic"></td><td>target</td></tr></table></div><button data-ref="action" title="Action"></button></article>`}
	if err := document.setView(view); err != nil {
		t.Fatal(err)
	}
	root := document.find.tree
	ancestor := root.Children[0]
	cells := ancestor.Children[0].Children[0].Children[0].Children
	// HTML parsing inserts tbody, so the source and component must agree on
	// the repaired structure without rewriting markup through a serializer.
	if len(cells) != 4 {
		t.Fatalf("unexpected table structure: %s", root)
	}
	if id, _ := cells[0].Attr("id"); id != "" {
		t.Fatal("empty padding cell acquired a lookup ID")
	}
	if id, _ := cells[1].Attr("id"); id != "pencode-extension-kept" {
		t.Fatal("authored empty-cell ID was discarded")
	}
	for _, node := range []*uinode.Node{root, ancestor, cells[2], cells[3], root.Children[1]} {
		if id, _ := node.Attr("id"); id == "" {
			t.Fatalf("text, scroll ancestor, reference or control lost its ID: %s", node)
		}
	}
	if tabindex, _ := root.Attr("tabindex"); tabindex != "-1" {
		t.Fatal("text root is no longer focusable")
	}
	if document.selection.tree.Children[0] != root || !strings.Contains(document.find.content.String(), "\t\t\ttarget") {
		t.Fatal("search and selection lost the shared structure or empty-cell separators")
	}
	previous := document.definition
	prefix := fmt.Sprintf("pencode-extension-editor/%p/", document)
	for _, source := range []string{
		`<article data-ref="sheet" data-pencode-searchable><p id="editor">bad</p></article>`,
		`<article data-ref="sheet" data-pencode-searchable><p id="` + prefix + `0">collision</p></article>`,
		`<p id="pencode-extension-same"></p><p id="pencode-extension-same"></p>`,
	} {
		if document.setView(&peb.EditorView{HTML: source}) == nil || document.definition != previous {
			t.Fatal("parsed template bypassed ID validation or replaced the current view on error")
		}
	}
}

func TestPencodeCustomViewReusesUnchangedMarkup(t *testing.T) {
	document := &pencodeCustomDocument{}
	view := peb.EditorView{HTML: `<button data-ref="x">OK</button>`, CSS: `button { color: red; }`, Events: []peb.EditorBinding{{Ref: "x", Kind: "click"}}}
	if err := document.setView(&view); err != nil {
		t.Fatal(err)
	}
	definition := document.definition
	copy := view
	if n := testing.AllocsPerRun(100, func() {
		if err := document.setView(&copy); err != nil {
			t.Fatal(err)
		}
	}); n != 0 || document.definition != definition {
		t.Fatalf("unchanged preview rebuilt its definition: %g allocations", n)
	}
	invalid := copy
	invalid.Events = []peb.EditorBinding{{Ref: "missing", Kind: "click"}}
	if document.setView(&invalid) == nil || document.definition != definition {
		t.Fatal("reuse bypassed binding validation or discarded the current view")
	}
}

func TestPencodeCustomViewLinksOpenWorkspaceDocuments(t *testing.T) {
	model, project := resourceTestModel(t)
	resourceTestWrite(t, project, "docs/README.md", "# Readme")
	resourceTestWrite(t, project, "guide with spaces.md", "# Guide")
	openProjectFile(project, "docs/README.md")
	model.syncActiveProjectFields()
	project.tree.selection.set("docs/README.md")
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
	if s := &project.tree.selection; s.anchor != model.activePath || s.paths != nil {
		t.Fatalf("link to %q left the tree selection at %+v", model.activePath, s)
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
	bundle := installPencodeEditorFixture(t, model.backend)
	bundle.Editors[0].Default = false
	model.importEditors(bundle.Editors)
	project := model.activeProjectState()
	resourceTestWrite(t, project, "example.go", "package example\n")
	openProjectFileAndWait(t, model, project, "example.go")
	file := model.activeFile()
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
	id := bundle.Editors[0].ID
	wait(func() bool { d := model.customDocuments[file]; return d != nil && d.definition != nil && !d.busy() })
	if model.customDocuments[file].provider.ID != id || model.textEditorActive() || file.editorSplit {
		t.Fatal("opening the file did not activate its registered tool")
	}
	editorContextClick(t, app, model, state, "editor-text")
	wait(func() bool { return model.textEditorActive() && model.customDocuments[file] == nil })
	editorContextClick(t, app, model, state, "action-"+id)
	wait(func() bool { d := model.customDocuments[file]; return d != nil && d.definition != nil && !d.busy() })
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

func TestCustomEditorLoadsImagesAsTheyApproachViewport(t *testing.T) {
	var first, last atomic.Int32
	waitImage := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/top.svg":
			first.Add(1)
			select {
			case <-waitImage:
			case <-r.Context().Done():
				return
			}
		case "/bottom.svg":
			last.Add(1)
		default:
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `<svg width="64" height="48"><rect width="64" height="48" fill="red"/></svg>`)
	}))
	defer server.Close()
	app, session := newPencodeTestApp(t)
	model := session.active.model
	file := model.activeFile()
	top := server.URL + "/top.svg"
	bottom := server.URL + "/bottom.svg"
	var body strings.Builder
	fmt.Fprintf(&body, `// editor fixture HTML
<p>Body</p>
<table><tr><td><img id="pencode-extension-top" width="32" loading="lazy" src=%q></td></tr></table>
<p><img id="pencode-extension-height" height="24" loading="lazy" src=%q><img id="pencode-extension-auto" loading="lazy" src=%q></p>`, top, top, top)
	for i := range 70 {
		fmt.Fprintf(&body, `<p style="height:200px"><img loading="lazy" src="%s/%d.svg"></p>`, server.URL, i)
	}
	fmt.Fprintf(&body, `<p><img loading="lazy" src=%q></p>`, bottom)
	model.applyFileContent(file, model.activeProjectState(), body.String(), true)
	model.importEditors(installPencodeEditorFixture(t, session.backend).Editors)
	markPencodeRootDirtyByModel(model.fontResources, model)
	wait := func(ready func() bool) {
		t.Helper()
		for until := time.Now().Add(5 * time.Second); time.Now().Before(until); {
			if _, err := app.Step(); err != nil {
				t.Fatal(err)
			}
			if ready() {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatalf("preview did not settle: status=%q, first=%d, last=%d", model.status, first.Load(), last.Load())
	}
	wait(func() bool { return first.Load() != 0 })
	doc := model.customDocuments[file]
	if doc.view == nil || doc.message != "" || len(doc.lazy.bySource) != 72 || last.Load() != 0 {
		t.Fatalf("opening preview fetched a distant image or hid the body: %q last=%d", doc.message, last.Load())
	}
	close(waitImage)
	wait(func() bool {
		node, _ := model.fontResources.HTMLNode(model.surface, "pencode-extension-top")
		width, _ := node.Attribute("width")
		height, _ := node.Attribute("height")
		src, _ := node.Attribute("src")
		return width == "32" && height == "24" && src == doc.lazy.bySource[top].uri && src != ""
	})
	for _, id := range []string{"pencode-extension-height", "pencode-extension-auto"} {
		node, _ := model.fontResources.HTMLNode(model.surface, id)
		width, _ := node.Attribute("width")
		height, _ := node.Attribute("height")
		wantWidth, wantHeight := "32", "24"
		if id == "pencode-extension-auto" {
			wantWidth, wantHeight = "64", "48"
		}
		if width != wantWidth || height != wantHeight {
			t.Errorf("%s dimensions = %s × %s, want %s × %s", id, width, height, wantWidth, wantHeight)
		}
	}
	if first.Load() != 1 || last.Load() != 0 {
		t.Fatal("image request was repeated or premature")
	}
	viewport, _ := model.fontResources.HTMLNode(model.surface, "custom-editor")
	area, _ := viewport.ScrollContainer()
	scroll := model.fontResources.Mut[scene.UIScroll](area)
	scroll.Position[1] = scroll.ViewportSize[1] - scroll.ContentSize[1]
	wait(func() bool { return doc.lazy.bySource[bottom].uri != "" })
	if last.Load() != 1 {
		t.Fatal("scrolled image was not fetched exactly once")
	}
	model.selectEditor(pencodeTextEditor)
	markPencodeRootDirtyByModel(model.fontResources, model)
	wait(func() bool { return model.customDocuments[file] == nil })
	if len(model.extensionImages) != 0 {
		t.Fatal("closing the preview retained image bytes")
	}
}
