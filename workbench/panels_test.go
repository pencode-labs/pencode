package workbench

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
	engineui "github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/lsp"
)

func problemControl(t *testing.T, model *workspaceModel, label string) ecs.Entity {
	t.Helper()
	position, _, _ := strings.Cut(label, " ")
	for _, access := range module.ContentQuery2[scene.Text2D, htmlui.ControlText](model.fontResources) {
		if strings.TrimSpace(access.A.Ref().Value) == position {
			return access.B.Ref().Control
		}
	}
	t.Fatalf("missing problem control %q", label)
	return 0
}

func TestPencodePanelClosePreservesEditorSelection(t *testing.T) {
	for _, panel := range []pencodePanel{pencodePanelProblems, pencodePanelOutput} {
		for _, selection := range []struct{ caret, anchor int }{{3, 3}, {3, 1}, {1, 3}} {
			for _, split := range []bool{false, true} {
				app, model, _, state := languageInteractionFixture(t)
				world := model.fontResources
				value := "甲乙🙂尾\nsecond line\nlast line\n" + strings.Repeat(strings.Repeat("x", 256)+"\n", 150)
				model.updateActiveContent(value)
				model.setEditorValueAndCaret(world, value, selection.caret)
				module.ContentMut[engineui.TextField](world, model.editor.control).Anchor = selection.anchor
				model.bottom.active = panel
				markPencodeRootDirtyByModel(world, model)
				languageInteractionStep(t, app)
				textarea := module.ContentMut[engineui.Textarea](world, model.editor.control)
				textarea.ScrollX, textarea.ScrollY = 100, 600
				languageInteractionStep(t, app)
				wantView := model.activeFile().editorView

				bounds := editorContextBounds(t, model, "panel-close")
				x, y := float64(bounds.X+bounds.Width/2), float64(bounds.Y+bounds.Height/2)
				state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseLeft, X: x, Y: y})
				if split {
					languageInteractionStep(t, app)
				}
				state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseLeft, X: x, Y: y})
				languageInteractionStep(t, app)
				languageInteractionStep(t, app)
				field := module.ContentRef[engineui.TextField](world, model.editor.control)
				if model.bottom.active != pencodePanelClosed || !field.Focused || field.Caret != selection.caret || field.Anchor != selection.anchor {
					t.Fatalf("closing panel %d (split=%v) changed selection %+v: caret=%d anchor=%d focused=%v active=%d", panel, split, selection, field.Caret, field.Anchor, field.Focused, model.bottom.active)
				}
				if model.activeContent() != value {
					t.Fatal("closing panel changed document content")
				}
				if got := model.activeFile().editorView; got != wantView {
					t.Fatalf("closing panel changed editor view: got %+v, want %+v", got, wantView)
				}
			}
		}
	}
}

func TestPencodeProblemNavigationDoesNotChangeDocument(t *testing.T) {
	app, model, provider, state := languageInteractionFixture(t)
	world := model.fontResources
	file := model.activeFile()
	doc := model.languages.documents[file]
	items := []lsp.Diagnostic{
		{Message: "first problem", Range: lsp.Range{Start: lsp.Position{Line: 1, Character: 3}}},
		{Message: "second problem", Range: lsp.Range{Start: lsp.Position{Line: 2, Character: 5}}},
	}
	doc.diagnostics[provider] = items
	model.setProblems(provider, doc.uri, items)
	editorContextClick(t, app, model, state, "status-problems")
	languageInteractionStep(t, app)
	for len(provider.jobs) > 0 {
		job := <-provider.jobs
		if job.update != nil {
			job.update.take()
		}
	}
	version, revision := doc.version, model.bottom.problemRevision
	content := file.Content
	for i := range 4 {
		problem := model.bottom.problems[i%len(items)]
		// A reconciliation would replace the untouched sibling's props. Mark them
		// so this catches list rendering even when the final DOM looks identical.
		var sibling ecs.Entity
		for entity, access := range module.ContentQuery[htmlui.Component[pencodeProblemProps, struct{}]](world) {
			if access.Get().Props.Problem.label != problem.label {
				sibling = entity
				access.Ref().Props.Problem.path = "unchanged sibling"
				break
			}
		}
		control := problemControl(t, model, problem.label)
		transform := module.ContentRef[scene.UIGlobalTransform](world, control)
		size := module.ContentRef[scene.Size2D](world, control)
		scale := float64(model.settings.Zoom) / 100
		x, y := float64(transform.Matrix[12]+size.Width/2)*scale, float64(transform.Matrix[13]+size.Height/2)*scale
		state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseLeft, X: x, Y: y})
		state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseLeft, X: x, Y: y})
		for range 3 {
			languageInteractionStep(t, app)
		}
		if sibling == 0 || module.ContentRef[htmlui.Component[pencodeProblemProps, struct{}]](world, sibling).Props.Problem.path != "unchanged sibling" {
			t.Fatal("problem click reconciled the unchanged problem list")
		}
		field, _ := module.ContentGet[engineui.TextField](world, model.editor.control)
		offset, err := lsp.OffsetAt(content, problem.location.Range.Start, lsp.UTF16)
		if err != nil || !field.Focused || field.Caret != offset || field.Anchor != offset {
			t.Fatalf("problem did not focus its location: caret=%d anchor=%d focused=%v want=%d err=%v", field.Caret, field.Anchor, field.Focused, offset, err)
		}
		if file.Content != content || doc.version != version || model.bottom.problemRevision != revision || len(doc.diagnostics[provider]) != len(items) {
			t.Fatalf("navigation changed diagnostics: version=%d want=%d revision=%d want=%d", doc.version, version, model.bottom.problemRevision, revision)
		}
		if len(provider.jobs) != 0 {
			t.Fatalf("navigation queued %d language server jobs", len(provider.jobs))
		}
	}
	model.updateActiveContent(content + "// edited\n")
	model.syncLanguageDocuments()
	if doc.version != version+1 || len(provider.jobs) != 1 || len(doc.diagnostics[provider]) != 0 {
		t.Fatal("editing did not invalidate diagnostics and synchronize the document")
	}
}

func TestPencodeProblemNavigationOnlyMovesCaret(t *testing.T) {
	_, model, _, _ := languageInteractionFixture(t)
	world := model.fontResources
	control := module.ContentMut[htmlui.Control](world, model.editor.control)
	control.DirtyValue = false
	model.languages.panel.navigate = model.activeFile()
	model.languages.panel.offset = 3
	model.focusLanguageControls(world)
	if control.DirtyValue {
		t.Fatal("navigation rewrote unchanged editor content")
	}
	field := module.ContentRef[engineui.TextField](world, model.editor.control)
	if field.Caret != 3 || field.Anchor != 3 || model.languages.panel.navigate != nil {
		t.Fatal("navigation did not move the caret")
	}
}

func TestPencodeProblemNavigationAcrossFilesKeepsRows(t *testing.T) {
	app, model, provider, state := languageInteractionFixture(t)
	world, project := model.fontResources, model.activeProjectState()
	main := model.activeFile()
	resourceTestWrite(t, project, "other.go", "package demo\n// first line\n// second line\n")
	model.setProblems(provider, model.languages.documents[main].uri, []lsp.Diagnostic{{Message: "main problem", Range: lsp.Range{Start: lsp.Position{Line: 1, Character: 3}}}})
	otherURI := lsp.FileURI(filepath.Join(project.Path, "other.go"))
	model.setProblems(provider, otherURI, []lsp.Diagnostic{{Message: "other problem", Range: lsp.Range{Start: lsp.Position{Line: 2, Character: 5}}}})
	model.syncProblems()
	for _, group := range model.bottom.problemGroups {
		group.expanded = true
	}
	editorContextClick(t, app, model, state, "status-problems")
	languageInteractionStep(t, app)
	for len(provider.jobs) > 0 {
		job := <-provider.jobs
		if job.update != nil {
			job.update.take()
		}
	}
	revision := model.bottom.problemRevision
	opened := false
	for i := range 4 {
		problem := model.bottom.problems[i%2]
		var sibling ecs.Entity
		for entity, access := range module.ContentQuery[htmlui.Component[pencodeProblemProps, struct{}]](world) {
			if access.Get().Props.Problem.label != problem.label {
				sibling = entity
				access.Ref().Props.Problem.path = "unchanged sibling"
			}
		}
		control := problemControl(t, model, problem.label)
		transform := module.ContentRef[scene.UIGlobalTransform](world, control)
		size := module.ContentRef[scene.Size2D](world, control)
		scale := float64(model.settings.Zoom) / 100
		x, y := float64(transform.Matrix[12]+size.Width/2)*scale, float64(transform.Matrix[13]+size.Height/2)*scale
		state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseLeft, X: x, Y: y})
		state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseLeft, X: x, Y: y})
		languageInteractionStep(t, app)
		if err := awaitFileIO(t, model); err != nil {
			t.Fatal(err)
		}
		for range 3 {
			languageInteractionStep(t, app)
		}
		if model.activeFile().Path != problem.path || model.bottom.problemRevision != revision {
			t.Fatal("cross-file navigation changed the problem list or missed its target")
		}
		if sibling == 0 || module.ContentRef[htmlui.Component[pencodeProblemProps, struct{}]](world, sibling).Props.Problem.path != "unchanged sibling" {
			t.Fatal("cross-file navigation reconciled unchanged problem rows")
		}
		newDocument := !opened && problem.location.URI == otherURI
		if newDocument {
			if len(provider.jobs) != 1 {
				t.Fatalf("first open queued %d language jobs, want one document sync", len(provider.jobs))
			}
			job := <-provider.jobs
			if job.update == nil || job.update.take().uri != otherURI {
				t.Fatal("first open did not synchronize the new document")
			}
			opened = true
		} else if len(provider.jobs) != 0 {
			t.Fatal("navigation to an open file sent language server requests")
		}
	}
}

func TestPencodeProblemNavigationPreservesScroll(t *testing.T) {
	app, model, provider, state := languageInteractionFixture(t)
	world := model.fontResources
	filename := filepath.Join(model.projectPath, "other.go")
	if err := os.WriteFile(filename, []byte("package demo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	uri := lsp.FileURI(filename)
	items := make([]lsp.Diagnostic, 50)
	for i := range items {
		items[i] = lsp.Diagnostic{Message: strings.Repeat("problem ", i+1)}
	}
	model.setProblems(provider, uri, items)
	editorContextClick(t, app, model, state, "status-problems")
	node, _ := world.HTMLNode(model.surface, "problems-pane")
	area, ok := node.ScrollContainer()
	if !ok {
		t.Fatal("missing problem scroll container")
	}
	scroll := module.ContentMut[scene.UIScroll](world, area)
	scroll.Position[1] = -300
	groupEntity := func() uint64 {
		for entity, access := range module.ContentQuery[htmlui.Component[pencodeProblemGroupProps, pencodeProblemGroupState]](world) {
			if access.Get().Props.Group.uri == uri {
				return uint64(entity)
			}
		}
		return 0
	}
	originalGroup := groupEntity()
	var props pencodeProblemProps
	for _, access := range module.ContentQuery[htmlui.Component[pencodeProblemProps, struct{}]](world) {
		props = access.Get().Props
		break
	}
	handleProblemEvent(&module.ContentEventContext[pencodeProblemProps, struct{}]{World: world, Props: &props, Ref: "problem"})
	if err := awaitFileIO(t, model); err != nil {
		t.Fatal(err)
	}
	if model.activePath != "other.go" {
		t.Fatal("diagnostic did not open the target file")
	}
	for range 3 {
		languageInteractionStep(t, app)
		current, exists := module.ContentGet[scene.UIScroll](world, area)
		if !exists || current.Position.Y() != -300 {
			t.Fatalf("navigation reset problem scroll: %+v", current)
		}
	}
	revision := model.bottom.problemRevision
	model.setProblems(provider, uri, append([]lsp.Diagnostic(nil), items...))
	markPencodeRootDirtyByModel(world, model)
	languageInteractionStep(t, app)
	if model.bottom.problemRevision != revision {
		t.Fatal("unchanged diagnostics rebuilt the problem list")
	}
	if current, exists := module.ContentGet[scene.UIScroll](world, area); !exists || current.Position.Y() != -300 {
		t.Fatalf("diagnostic republish reset problem scroll: %+v", current)
	}
	model.setProblems(provider, lsp.FileURI(filepath.Join(model.projectPath, "a.go")), []lsp.Diagnostic{{Message: "new file"}})
	markPencodeRootDirtyByModel(world, model)
	languageInteractionStep(t, app)
	if groupEntity() != originalGroup {
		t.Fatal("new file diagnostics remounted an existing file group")
	}
	if current, exists := module.ContentGet[scene.UIScroll](world, area); !exists || current.Position.Y() != -300 {
		t.Fatalf("new file diagnostics reset problem scroll: %+v", current)
	}
}

func TestPencodeBottomPanelsDiagnosticsAndOutput(t *testing.T) {
	// Retained HTML and native control events only; no window or screenshot.
	app, model, provider, state := languageInteractionFixture(t)
	world := model.fontResources
	file := model.activeFile()
	doc := model.languages.documents[file]
	publish := func(uri lsp.URI, version *int, items []lsp.Diagnostic) {
		t.Helper()
		raw, err := json.Marshal(lsp.PublishDiagnosticsParams{URI: uri, Version: version, Diagnostics: items})
		if err != nil {
			t.Fatal(err)
		}
		model.languages.events <- pencodeLanguageEvent{session: provider, method: "textDocument/publishDiagnostics", params: raw}
		languageInteractionStep(t, app)
	}
	problem := lsp.Diagnostic{Message: "undefined symbol", Severity: 1, Range: lsp.Range{Start: lsp.Position{Line: 1, Character: 2}}}
	publish(doc.uri, &doc.version, []lsp.Diagnostic{problem})
	other := lsp.FileURI(filepath.Join(model.projectPath, "unopened.go"))
	publish(other, nil, []lsp.Diagnostic{{Message: "workspace warning", Severity: 2}})
	if model.bottom.count != "2" {
		t.Fatalf("workspace count=%s", model.bottom.count)
	}
	if _, ok := world.HTMLNode(model.surface, "lsp-problems"); ok {
		t.Fatal("obsolete diagnostic toolbar remains")
	}
	before := editorContextBounds(t, model, "editor-body")
	editorContextClick(t, app, model, state, "status-problems")
	if model.bottom.active != pencodePanelProblems {
		t.Fatal("Problems did not open")
	}
	panel := editorContextBounds(t, model, "bottom-panel")
	editor := editorContextBounds(t, model, "editor-body")
	status := editorContextBounds(t, model, "statusbar")
	if panel.Height < 40 || editor.Height >= before.Height || panel.Y < editor.Y+editor.Height-1 || panel.Y+panel.Height > status.Y+1 {
		t.Fatalf("panel not docked above status bar: editor=%+v panel=%+v status=%+v", editor, panel, status)
	}
	rows := 0
	for _, access := range module.ContentQuery[htmlui.Component[pencodeProblemProps, struct{}]](world) {
		props := access.Get().Props
		if strings.Contains(props.Problem.label, "undefined symbol") {
			rows++
			handleProblemEvent(&module.ContentEventContext[pencodeProblemProps, struct{}]{World: world, Props: &props, Ref: "problem"})
		}
	}
	if rows != 1 {
		t.Fatalf("diagnostic rows=%d", rows)
	}
	wantOffset, err := lsp.OffsetAt(file.Content, problem.Range.Start, lsp.UTF16)
	if err != nil || model.languages.panel.navigate != file || model.languages.panel.offset != wantOffset {
		t.Fatal("diagnostic click did not navigate to its source position")
	}
	// Diagnostic navigation restores editor focus on the next layout pass.
	languageInteractionStep(t, app)
	editorContextClick(t, app, model, state, "status-output")
	if model.bottom.active != pencodePanelOutput {
		t.Fatal("status button did not activate Output")
	}
	editorContextClick(t, app, model, state, "status-problems")
	if model.bottom.active != pencodePanelProblems {
		t.Fatal("status button did not activate Problems")
	}
	stale := doc.version - 1
	publish(doc.uri, &stale, nil)
	if model.bottom.count != "2" {
		t.Fatal("stale diagnostic cleared current results")
	}
	publish(other, nil, nil)
	if model.bottom.count != "1" {
		t.Fatal("empty diagnostics did not update count")
	}
	model.logOutput("Extension: sample", "[Info] plugin ready")
	model.logOutput("pencode", "host output")
	editorContextClick(t, app, model, state, "status-output")
	if model.bottom.active != pencodePanelOutput {
		t.Fatal("Output did not open")
	}
	entity, ok := world.HTMLControl(model.surface, "output-source")
	control, exists := module.ContentGet[htmlui.Control](world, entity)
	if !ok || !exists || len(control.Options) < 2 {
		t.Fatalf("missing output sources: %+v", control)
	}
	event := func(ref, value string) {
		t.Helper()
		if !model.handleBottomPanelEvent(world, ref, value) {
			t.Fatalf("unhandled %s", ref)
		}
		markPencodeRootDirtyByModel(world, model)
		languageInteractionStep(t, app)
	}
	event("output-source", "Extension: sample")
	readOutput := func() htmlui.Control {
		t.Helper()
		entity, ok := world.HTMLControl(model.surface, "output-content")
		control, exists := module.ContentGet[htmlui.Control](world, entity)
		if !ok || !exists || !control.ReadOnly {
			t.Fatal("missing read-only output control")
		}
		return control
	}
	if got := readOutput().Value; got != "[Info] plugin ready\n" {
		t.Fatalf("wrong source output %q", got)
	}
	model.logOutput("Extension: sample", "[Error] failed")
	languageInteractionStep(t, app)
	event("output-filter", "Error")
	if got := readOutput().Value; got != "[Error] failed\n" {
		t.Fatalf("filter output %q", got)
	}
	event("output-filter", "")
	event("output-clear", "")
	if got := readOutput().Value; got != "" {
		t.Fatalf("clear output %q", got)
	}
	event("output-source", "pencode")
	if got := readOutput().Value; !strings.Contains(got, "host output") {
		t.Fatalf("clearing source lost host output %q", got)
	}
	provider.server.Label = "Fixture LSP"
	raw := json.RawMessage(`{"type":2,"message":"language warning"}`)
	model.languages.events <- pencodeLanguageEvent{session: provider, method: "window/logMessage", params: raw}
	languageInteractionStep(t, app)
	event("output-source", "Language Server: Fixture LSP")
	if got := readOutput().Value; got != "[Warning] language warning\n" {
		t.Fatalf("missing classified LSP output %q", got)
	}
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyEscape})
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyEscape})
	languageInteractionStep(t, app)
	if model.bottom.active != pencodePanelClosed {
		t.Fatal("Escape did not close panel")
	}
	model.removeLanguageServers("test/")
	model.syncProblems()
	if model.bottom.count != "0" {
		t.Fatal("unloaded server left stale diagnostics")
	}
}

func TestPencodeProblemMetadataDoesNotRefreshRows(t *testing.T) {
	app, model, provider, state := languageInteractionFixture(t)
	doc := model.languages.documents[model.activeFile()]
	publish := func(item lsp.Diagnostic) {
		t.Helper()
		raw, err := json.Marshal(lsp.PublishDiagnosticsParams{URI: doc.uri, Version: &doc.version, Diagnostics: []lsp.Diagnostic{item}})
		if err != nil {
			t.Fatal(err)
		}
		model.languages.events <- pencodeLanguageEvent{session: provider, method: "textDocument/publishDiagnostics", params: raw}
		languageInteractionStep(t, app)
	}
	item := lsp.Diagnostic{Message: "unnecessary type arguments", Severity: 3, Source: "infertypeargs", Data: json.RawMessage(`{"version":1}`)}
	publish(item)
	editorContextClick(t, app, model, state, "status-problems")
	group := model.bottom.problemGroups[0]
	revision := model.bottom.problemRevision
	item.Data = json.RawMessage(`{"version":2}`)
	item.Code = json.RawMessage(`"updated-code"`)
	item.Tags = []int{1}
	item.RelatedInformation = json.RawMessage(`[]`)
	publish(item)
	if model.bottom.problemRevision != revision || model.bottom.problemGroups[0] != group || !group.expanded {
		t.Fatal("diagnostic metadata change rebuilt visible problems")
	}
	if got := doc.diagnostics[provider][0]; string(got.Data) != string(item.Data) || string(model.bottom.diagnostics[pencodeDiagnosticKey{provider, doc.uri}][0].Data) != string(item.Data) {
		t.Fatal("diagnostic metadata was not updated")
	}
	for _, change := range []func(*lsp.Diagnostic){
		func(d *lsp.Diagnostic) { d.Message = "updated message" },
		func(d *lsp.Diagnostic) { d.Severity = 1 },
		func(d *lsp.Diagnostic) { d.Source = "updated source" },
		func(d *lsp.Diagnostic) { d.Range.Start.Line++ },
	} {
		change(&item)
		publish(item)
		revision++
		if model.bottom.problemRevision != revision {
			t.Fatal("visible diagnostic change did not refresh problems")
		}
	}
}

func TestPencodeProblemRepublishDoesNotRefreshWorkbench(t *testing.T) {
	_, model, provider, _ := languageInteractionFixture(t)
	doc := model.languages.documents[model.activeFile()]
	item := lsp.Diagnostic{Message: "problem", Severity: 2}
	publish := func(version int, items []lsp.Diagnostic) bool {
		t.Helper()
		raw, err := json.Marshal(lsp.PublishDiagnosticsParams{URI: doc.uri, Version: &version, Diagnostics: items})
		if err != nil {
			t.Fatal(err)
		}
		model.languages.events <- pencodeLanguageEvent{session: provider, method: "textDocument/publishDiagnostics", params: raw}
		return model.pollLanguageServers(model.fontResources)
	}
	if !publish(doc.version, []lsp.Diagnostic{item}) {
		t.Fatal("new diagnostics did not request a refresh")
	}
	model.syncProblems()
	for range 3 {
		item.Data = json.RawMessage(`{"updated":true}`)
		if publish(doc.version, []lsp.Diagnostic{item}) {
			t.Fatal("identical diagnostics requested a workbench refresh")
		}
	}
	if publish(doc.version-1, nil) {
		t.Fatal("stale diagnostics requested a workbench refresh")
	}
	if !publish(doc.version, nil) {
		t.Fatal("clearing diagnostics did not request a refresh")
	}
}

func TestPencodePanelIdleCachesDoNotAllocate(t *testing.T) {
	model := &workspaceModel{}
	provider := &pencodeLanguageSession{key: pencodeLanguageKey{root: t.TempDir()}}
	uri := lsp.FileURI(filepath.Join(provider.key.root, "main.go"))
	items := []lsp.Diagnostic{{Message: "warning", Severity: 2}}
	model.setProblems(provider, uri, items)
	model.logOutput("pencode", "line")
	model.syncOutput()
	model.syncProblems()
	model.pollOutput()
	if n := testing.AllocsPerRun(100, func() {
		model.setProblems(provider, uri, items)
		model.syncOutput()
		model.syncProblems()
		model.pollOutput()
	}); n != 0 {
		t.Fatalf("idle panels allocated %g", n)
	}
}

func TestPencodeProblemsGroupByFile(t *testing.T) {
	app, model, provider, state := languageInteractionFixture(t)
	world := model.fontResources
	first := lsp.FileURI(filepath.Join(model.projectPath, "a.go"))
	second := lsp.FileURI(filepath.Join(model.projectPath, "b.go"))
	otherProvider := &pencodeLanguageSession{key: provider.key}
	model.setProblems(provider, second, []lsp.Diagnostic{{Message: "second file"}})
	markPencodeRootDirtyByModel(world, model)
	languageInteractionStep(t, app)
	model.setProblems(provider, first, []lsp.Diagnostic{
		{Message: "later line", Range: lsp.Range{Start: lsp.Position{Line: 5}}},
		{Message: "first line"},
	})
	model.setProblems(otherProvider, first, []lsp.Diagnostic{{Message: "other server", Range: lsp.Range{Start: lsp.Position{Line: 2}}}})
	editorContextClick(t, app, model, state, "status-problems")
	groups := model.bottom.problemGroups
	if model.bottom.count != "4" || len(groups) != 2 || groups[0].uri != first || groups[0].count != "3" || groups[1].uri != second || groups[1].count != "1" {
		t.Fatalf("incorrect file groups: count=%s groups=%+v", model.bottom.count, groups)
	}
	if !groups[0].expanded || groups[1].expanded {
		t.Fatal("only the first file should initially expand")
	}
	assertRows := func(want ...string) {
		t.Helper()
		rows := 0
		for _, access := range module.ContentQuery[htmlui.Component[pencodeProblemProps, struct{}]](world) {
			label := access.Get().Props.Problem.label
			matched := false
			for _, message := range want {
				matched = matched || strings.Contains(label, message)
			}
			if !matched {
				t.Fatalf("unexpected visible problem %q", label)
			}
			rows++
		}
		if rows != len(want) {
			t.Fatalf("visible problems=%d, want %d", rows, len(want))
		}
	}
	toggle := func(uri lsp.URI) {
		t.Helper()
		found := false
		for _, access := range module.ContentQuery[htmlui.Component[pencodeProblemGroupProps, pencodeProblemGroupState]](world) {
			props := access.Get().Props
			if props.Group.uri == uri {
				handleProblemGroupEvent(&module.ContentEventContext[pencodeProblemGroupProps, pencodeProblemGroupState]{World: world, Props: &props, Ref: "toggle"})
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing file group %s", uri)
		}
		languageInteractionStep(t, app)
	}
	assertRows("first line", "other server", "later line")
	if got := model.bottom.problems[1].label; !strings.Contains(got, "other server") {
		t.Fatalf("problems are not ordered by source position: %q", got)
	}
	toggle(first)
	assertRows()
	toggle(second)
	assertRows("second file")
	model.setProblems(provider, second, []lsp.Diagnostic{{Message: "updated second file"}})
	markPencodeRootDirtyByModel(world, model)
	languageInteractionStep(t, app)
	assertRows("updated second file")
	if groups[0].expanded || !groups[1].expanded {
		t.Fatal("diagnostic refresh reset expansion state")
	}
	editorContextClick(t, app, model, state, "status-output")
	editorContextClick(t, app, model, state, "status-problems")
	assertRows("updated second file")
	model.setProblems(provider, first, nil)
	model.setProblems(otherProvider, first, nil)
	markPencodeRootDirtyByModel(world, model)
	languageInteractionStep(t, app)
	if len(model.bottom.problemGroups) != 1 || len(model.bottom.problemGroupByURI) != 1 || model.bottom.count != "1" {
		t.Fatal("resolved file retained a stale group or count")
	}
	assertRows("updated second file")
	model.setProblems(provider, second, nil)
	markPencodeRootDirtyByModel(world, model)
	languageInteractionStep(t, app)
	if len(model.bottom.problemGroups) != 0 || len(model.bottom.problemGroupByURI) != 0 || model.bottom.count != "0" {
		t.Fatal("clearing diagnostics retained groups")
	}
	assertRows()
}
