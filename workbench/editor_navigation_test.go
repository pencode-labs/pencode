package workbench

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/onix-engine/onix/input"
	engineui "github.com/onix-engine/onix/ui"
	"github.com/pencode-labs/pencode/lsp"
)

func TestEditorNavigationShortcutsRestoreViews(t *testing.T) {
	app, m, _, state := languageInteractionFixture(t)
	w, p := m.fontResources, m.activeProjectState()
	first := m.activeFile()
	m.updateActiveContent(strings.Repeat(strings.Repeat("中🙂x", 90)+"\n", 150))
	m.setEditorValueAndCaret(w, first.content(), 80)
	w.Mut[engineui.TextField](m.editor.control).Anchor = 5
	languageInteractionStep(t, app)
	area := w.Mut[engineui.Textarea](m.editor.control)
	area.ScrollX, area.ScrollY = 100, 600
	languageInteractionStep(t, app)
	want := first.editorView
	other := &openFile{Path: "other.go", Name: "other.go", contentSnapshot: snapshotText("// 中🙂 target\n")}
	p.Files[other.Path] = other
	p.Tabs = append(p.Tabs, other.Path)
	location := lsp.Location{URI: lsp.FileURI(filepath.Join(p.Path, other.Path)), Range: lsp.Range{
		Start: lsp.Position{Character: 7}, End: lsp.Position{Character: 13},
	}}
	if !m.navigateLanguageLocation(w, location, nil) {
		t.Fatal("jump failed")
	}
	for range 2 {
		languageInteractionStep(t, app)
	}
	otherView := other.editorView
	if otherView.caret != 6 || otherView.anchor != 12 || len(m.navigation.entries) != 2 {
		t.Fatalf("jump lost Unicode selection or history: %+v, %+v", otherView, m.navigation)
	}
	primary := input.KeyLeftControl
	if runtime.GOOS == "darwin" {
		primary = input.KeyLeftSuper
	}
	chord := func(key input.KeyCode) {
		t.Helper()
		state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: primary})
		state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: key})
		languageInteractionStep(t, app)
		state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: key})
		state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: primary})
		languageInteractionStep(t, app)
	}
	check := func(file *openFile, want pencodeEditorView) {
		t.Helper()
		field := w.Ref[engineui.TextField](m.editor.control)
		area := w.Ref[engineui.Textarea](m.editor.control)
		got := pencodeEditorView{field.Caret, field.Anchor, area.ScrollX, area.ScrollY}
		if m.activeFile() != file || got != want || !field.Focused {
			t.Fatalf("file=%s view=%+v focused=%v, want %s %+v", m.activePath, got, field.Focused, file.Path, want)
		}
	}
	for range 2 {
		chord(input.KeyLeft)
		check(first, want)
		chord(input.KeyLeft) // The first entry consumes the chord without moving the caret.
		check(first, want)
		chord(input.KeyRight)
		check(other, otherView)
		chord(input.KeyRight)
		check(other, otherView)
	}
	chord(input.KeyLeft)
	location.URI = lsp.FileURI(filepath.Join(p.Path, first.Path))
	location.Range = lsp.Range{Start: lsp.Position{Line: 30}}
	if !m.navigateLanguageLocation(w, location, nil) {
		t.Fatal("new jump failed")
	}
	for range 2 {
		languageInteractionStep(t, app)
	}
	newView := first.editorView
	chord(input.KeyRight)
	check(first, newView)
	if len(m.navigation.entries) != 2 {
		t.Fatal("new jump retained forward history")
	}
	if allocs := testing.AllocsPerRun(100, func() { m.navigationLocation() }); allocs != 0 {
		t.Fatalf("capturing navigation allocated %g times", allocs)
	}
}

func TestEditorNavigationBindingsAndFocus(t *testing.T) {
	app, m, _, _ := languageInteractionFixture(t)
	w := m.fontResources
	for _, goos := range []string{"linux", "windows", "darwin"} {
		for _, id := range []string{"editor-go-back", "editor-go-forward"} {
			state := keymapState(pencodeBindingChord("Primary+Left"), goos)
			if id == "editor-go-forward" {
				state = keymapState(pencodeBindingChord("Primary+Right"), goos)
			}
			if !m.commandMatches(id, state, goos) || !m.editorNavigationShortcut(w, state, goos) || !m.editorKeyHandled(state) {
				t.Fatalf("%s did not consume the navigation chord on %s", id, goos)
			}
		}
	}
	m.settings.Bindings["editor-go-back"] = pencodeBindingChord("Alt+Left")
	if m.editorNavigationShortcut(w, keymapState(pencodeBindingChord("Primary+Left"), runtime.GOOS), runtime.GOOS) {
		t.Fatal("old binding remained active")
	}
	if !m.editorNavigationShortcut(w, keymapState(pencodeBindingChord("Alt+Left"), runtime.GOOS), runtime.GOOS) {
		t.Fatal("custom binding was ignored")
	}
	m.findOpen = true
	markPencodeRootDirtyByModel(w, m)
	languageInteractionStep(t, app)
	if !m.editorNavigationShortcut(w, keymapState(pencodeBindingChord("Alt+Left"), runtime.GOOS), runtime.GOOS) {
		t.Fatal("open find bar blocked editor navigation")
	}
	control, _ := w.HTMLControl(m.surface, "find-query")
	if !w.Focus(control) {
		t.Fatal("could not focus search input")
	}
	if m.editorNavigationShortcut(w, keymapState(pencodeBindingChord("Alt+Left"), runtime.GOOS), runtime.GOOS) {
		t.Fatal("navigation intercepted search input")
	}
	m.findOpen = false
	if m.editorNavigationShortcut(w, keymapState(pencodeBindingChord("Alt+Left"), runtime.GOOS), runtime.GOOS) {
		t.Fatal("navigation intercepted an unfocused editor")
	}
}

func TestEditorNavigationReferencesAcrossProjects(t *testing.T) {
	app, m, provider, _ := languageInteractionFixture(t)
	w, first := m.fontResources, m.activeProjectState()
	m.setEditorValueAndCaret(w, m.activeContent(), 8)
	w.Mut[engineui.TextField](m.editor.control).Anchor = 2
	languageInteractionStep(t, app)
	other := &pencodeProject{Path: t.TempDir(), Name: "other", Files: map[string]*openFile{
		"main.go": {Path: "main.go", Name: "main.go", contentSnapshot: snapshotText("package other\n")},
	}, Tabs: []string{"main.go"}, ActivePath: "main.go"}
	m.projects = append(m.projects, other)
	otherProvider := *provider
	otherProvider.key.root = other.Path
	m.languages.sessions[otherProvider.key] = &otherProvider
	location := &lsp.Location{URI: lsp.FileURI(filepath.Join(other.Path, "main.go")), Range: lsp.Range{
		Start: lsp.Position{Character: 8}, End: lsp.Position{Character: 13},
	}}
	panel := &m.languages.panel
	panel.method, panel.open = "textDocument/references", true
	panel.rows = []pencodeLanguageRow{{location: location}}
	m.chooseLanguageRow(w, 0)
	for range 2 {
		languageInteractionStep(t, app)
	}
	if len(m.navigation.entries) != 2 || m.navigation.entries[0].project != first || m.navigation.entries[1].project != other {
		t.Fatal("reference navigation lost its source project")
	}
	for _, step := range []int{-1, 1} {
		m.navigateEditorHistory(step)
		for range 2 {
			languageInteractionStep(t, app)
		}
		want := m.navigation.entries[m.navigation.index]
		field := w.Ref[engineui.TextField](m.editor.control)
		if m.activeProjectState() != want.project || field.Caret != want.view.caret || field.Anchor != want.view.anchor {
			t.Fatal("reference history restored the wrong project or selection")
		}
	}
}

func TestEditorNavigationReopensClosedFilesAndRejectsCancelledOpens(t *testing.T) {
	m, p, gate, unblock := delayedFileModel(t)
	resourceTestWrite(t, p, "first.go", "package first\n")
	resourceTestWrite(t, p, "second.go", "package second\n")
	openProjectFileAndWait(t, m, p, "first.go")
	m.activeFile().editorView = pencodeEditorView{caret: 8, anchor: 2}
	n := &fileNavigation{line: 1}
	if err := m.requestFileOpen(p, "second.go", false, true, n); err != nil {
		t.Fatal(err)
	}
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	m.closeProjectTab(p, "first.go")
	gate.pause.Store(true)
	m.navigateEditorHistory(-1)
	awaitFileGate(t, gate)
	if m.navigation.index != 1 {
		t.Fatal("pending navigation changed history")
	}
	if err := m.requestFileOpen(p, "second.go", false, false, nil); err != nil {
		t.Fatal(err)
	}
	unblock()
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	if m.navigation.index != 1 || m.activePath != "second.go" {
		t.Fatal("cancelled navigation stole selection or changed history")
	}
	m.navigateEditorHistory(-1)
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	if m.navigation.index != 0 || m.activePath != "first.go" || m.activeFile().editorView != (pencodeEditorView{caret: 8, anchor: 2}) {
		t.Fatal("reopened file lost its view or history position")
	}
}

func TestEditorNavigationHistoryIsBoundedAndReusesStorage(t *testing.T) {
	var h pencodeNavigationHistory
	location := func(caret int) pencodeNavigationLocation {
		return pencodeNavigationLocation{path: "main.go", view: pencodeEditorView{caret: caret, anchor: caret}}
	}
	for i := range pencodeNavigationLimit * 2 {
		h.record(location(i), location(i+1))
	}
	if len(h.entries) != pencodeNavigationLimit || h.entries[0].view.caret != pencodeNavigationLimit+1 {
		t.Fatalf("history did not evict the oldest entries: %+v", h)
	}
	if allocs := testing.AllocsPerRun(100, func() {
		h.record(location(h.entries[h.index].view.caret), location(h.entries[h.index].view.caret+1))
	}); allocs != 0 {
		t.Fatalf("warm history recording allocated %g times", allocs)
	}
}
