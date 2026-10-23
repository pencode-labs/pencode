package workbench

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/projects/pencode/lsp"
	htmlui "github.com/diiyw/onix/ui/html"
)

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

func TestPencodePanelIdleCachesDoNotAllocate(t *testing.T) {
	model := &workspaceModel{}
	provider := &pencodeLanguageSession{key: pencodeLanguageKey{root: t.TempDir()}}
	model.setProblems(provider, lsp.FileURI(filepath.Join(provider.key.root, "main.go")), []lsp.Diagnostic{{Message: "warning", Severity: 2}})
	model.logOutput("pencode", "line")
	model.syncOutput()
	model.syncProblems()
	model.pollOutput()
	if n := testing.AllocsPerRun(100, func() { model.syncOutput(); model.syncProblems(); model.pollOutput() }); n != 0 {
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
		for _, access := range module.ContentQuery[htmlui.Component[pencodeProblemGroupProps, struct{}]](world) {
			props := access.Get().Props
			if props.Group.uri == uri {
				handleProblemGroupEvent(&module.ContentEventContext[pencodeProblemGroupProps, struct{}]{World: world, Props: &props, Ref: "toggle"})
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
