package workbench

import (
	"strings"
	"testing"

	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	engineui "github.com/diiyw/onix/ui"
	"github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/textsearch"
)

func TestFindReplaceNativeControlsAndUndo(t *testing.T) {
	app, m, _, state := languageInteractionFixture(t)
	world := m.fontResources
	content := "中🙂 foo Foo food\nfoo"
	m.updateActiveContent(content)
	m.setEditorValueAndCaret(world, content, 6)
	world.Mut[engineui.TextField](m.editor.control).Anchor = 3
	m.runShortcut(world, pencodeShortcutFind)
	markPencodeRootDirtyByModel(world, m)
	for range 3 {
		languageInteractionStep(t, app)
	}
	if m.findQuery != "foo" || len(m.findMatches) != 4 || !pencodeControlFocused(world, m.surface, "find-query") {
		t.Fatalf("find not seeded/focused: %q %d", m.findQuery, len(m.findMatches))
	}
	dispatch := func(ref, value string) {
		t.Helper()
		handlePencodeRootEvent(&module.ContentEventContext[appProps, appState]{World: world, Component: m.root, Props: &appProps{Model: m}, Ref: ref, Event: html.Event{Value: value}})
		languageInteractionStep(t, app)
	}
	dispatch("find-word", "")
	if len(m.findMatches) != 3 {
		t.Fatal("whole word", len(m.findMatches))
	}
	if m.editor.findPaint.root == 0 || len(m.editor.findPaint.quads) == 0 {
		t.Fatal("matches were not highlighted")
	}
	if allocations := testing.AllocsPerRun(100, func() { m.editor.findPaint.sync(world, m) }); allocations != 0 {
		t.Fatal("warm find paint allocated", allocations)
	}
	dispatch("find-case", "")
	if len(m.findMatches) != 2 {
		t.Fatal("match case", len(m.findMatches))
	}
	editorContextClick(t, app, m, state, "find-toggle-replace")
	if !m.replaceOpen {
		t.Fatal("toggle did not reveal replace")
	}
	if bounds := editorContextBounds(t, m, "find-toggle-replace"); bounds.Height < 55 {
		t.Fatalf("expanded toggle does not span both rows: %+v", bounds)
	}
	dispatch("replace-query", "替换")
	dispatch("replace-one", "")
	if m.activeContent() != "中🙂 替换 Foo food\nfoo" || !m.activeFile().Dirty {
		t.Fatal(m.activeContent())
	}
	dispatch("replace-all", "")
	if m.activeContent() != "中🙂 替换 Foo food\n替换" {
		t.Fatal(m.activeContent())
	}
	m.undoActiveContent(world)
	if m.activeContent() != "中🙂 替换 Foo food\nfoo" {
		t.Fatal("replace-all was not one undo step")
	}
	m.undoActiveContent(world)
	if m.activeContent() != content {
		t.Fatal("replace-one lost undo")
	}
	dispatch("find-regex", "")
	dispatch("find-query", "[")
	if m.findError == "" || len(m.findMatches) != 0 {
		t.Fatal("invalid regex not reported")
	}
	dispatch("find-query", "f(o+)")
	dispatch("replace-query", "$1-$&")
	dispatch("replace-all", "")
	if m.activeContent() != "中🙂 oo-foo Foo food\noo-foo" {
		t.Fatal(m.activeContent())
	}
	editorContextClick(t, app, m, state, "find-toggle-replace")
	if m.replaceOpen {
		t.Fatal("toggle did not collapse replace")
	}
	if bounds := editorContextBounds(t, m, "find-toggle-replace"); bounds.Height > 35 {
		t.Fatalf("collapsed toggle is not compact: %+v", bounds)
	}
	// Enter in the document must remain a native edit while the find bar is open.
	world.Focus(m.editor.control)
	newlines := strings.Count(m.activeContent(), "\n")
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyEnter})
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyEnter})
	languageInteractionStep(t, app)
	if strings.Count(m.activeContent(), "\n") != newlines+1 {
		t.Fatal("editor lost native Enter")
	}
	m.closeFind(world)
	if !pencodeControlFocused(world, m.surface, "editor") {
		t.Fatal("Esc/close lost editor focus")
	}
}

func TestFindCacheAndLargeReplace(t *testing.T) {
	f := &openFile{Path: "file.txt", Name: "file.txt", Content: strings.Repeat("a ", pencodeFindMatchLimit+7)}
	m := &workspaceModel{files: map[string]*openFile{f.Path: f}, activePath: f.Path, findOpen: true, findQuery: "a", replaceQuery: "b", findOptions: textsearch.Options{CaseSensitive: true}}
	m.refreshFindMatches()
	if len(m.findMatches) != pencodeFindMatchLimit {
		t.Fatal(len(m.findMatches))
	}
	if allocations := testing.AllocsPerRun(100, m.refreshFindMatches); allocations != 0 {
		t.Fatal("warm find allocated", allocations)
	}
	m.replaceFindMatch(nil, true)
	if strings.Contains(f.Content, "a") || strings.Count(f.Content, "b") != pencodeFindMatchLimit+7 {
		t.Fatal("replace all only processed displayed matches")
	}
}

func TestFindSelectionRebasesThroughReplaceAndUndo(t *testing.T) {
	f := &openFile{Path: "file.txt", Name: "file.txt", Content: "foo\nfoo foo\nfoo"}
	m := &workspaceModel{files: map[string]*openFile{f.Path: f}, activePath: f.Path, findOpen: true, findQuery: "foo", replaceQuery: "long", findSelection: true, findScope: pencodeFindScope{f, 4, 11}}
	m.refreshFindMatches()
	if len(m.findMatches) != 2 {
		t.Fatal("selection scope", m.findMatches)
	}
	m.replaceFindMatch(nil, true)
	if f.Content != "foo\nlong long\nfoo" || m.findScope.start != 4 || m.findScope.end != 13 {
		t.Fatalf("scope did not expand with replacement: %q %+v", f.Content, m.findScope)
	}
	m.undoActiveContent(nil)
	if f.Content != "foo\nfoo foo\nfoo" || m.findScope.end != 11 {
		t.Fatal("undo did not restore scope", m.findScope)
	}
	m.refreshFindMatches()
	if len(m.findMatches) != 2 {
		t.Fatal("undo changed scope matches")
	}
}
