package workbench

import (
	"strings"
	"testing"

	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/module"
	engineui "github.com/onix-engine/onix/ui"
	"github.com/onix-engine/onix/ui/html"
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

func TestCloseFindPreservesEditorView(t *testing.T) {
	for _, method := range []string{"click", "escape"} {
		t.Run(method, func(t *testing.T) {
			app, m, _, state := languageInteractionFixture(t)
			world := m.fontResources
			const query = "target"
			content := query + "\n" + strings.Repeat(strings.Repeat("x", 256)+"\n", 160)
			m.updateActiveContent(content)
			m.setEditorValueAndCaret(world, content, 0)
			m.findQuery = query
			m.openFind(false)
			markPencodeRootDirtyByModel(world, m)
			for range 3 {
				languageInteractionStep(t, app)
			}
			if !pencodeControlFocused(world, m.surface, "find-query") {
				t.Fatal("find input did not gain focus")
			}
			// Scroll away from the selected match while find has focus.
			area := world.Mut[engineui.Textarea](m.editor.control)
			area.ScrollX, area.ScrollY = 100, 800
			languageInteractionStep(t, app)
			view := func() pencodeEditorView {
				field := world.Ref[engineui.TextField](m.editor.control)
				area := world.Ref[engineui.Textarea](m.editor.control)
				return pencodeEditorView{field.Caret, field.Anchor, area.ScrollX, area.ScrollY}
			}
			want := view()
			if want.caret != len(query) || want.anchor != 0 || want.scrollX <= 0 || want.scrollY <= 0 {
				t.Fatalf("fixture did not select the match and scroll away from it: %+v", want)
			}
			if method == "click" {
				editorContextClick(t, app, m, state, "find-close")
			} else {
				state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyEscape})
				state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyEscape})
				languageInteractionStep(t, app)
			}
			for range 3 {
				if m.findOpen || !pencodeControlFocused(world, m.surface, "editor") {
					t.Fatal("closing find did not return focus to the editor")
				}
				if got := view(); got != want {
					t.Fatalf("closing find changed editor view from %+v to %+v", want, got)
				}
				languageInteractionStep(t, app)
			}
		})
	}
}

func TestFindCacheAndLargeReplace(t *testing.T) {
	f := &openFile{Path: "file.txt", Name: "file.txt", contentSnapshot: snapshotText(strings.Repeat("a ", pencodeFindMatchLimit+7))}
	m := &workspaceModel{files: map[string]*openFile{f.Path: f}, activePath: f.Path, findOpen: true, findQuery: "a", replaceQuery: "b", findOptions: textsearch.Options{CaseSensitive: true}}
	m.refreshFindMatches()
	if len(m.findMatches) != pencodeFindMatchLimit {
		t.Fatal(len(m.findMatches))
	}
	if allocations := testing.AllocsPerRun(100, m.refreshFindMatches); allocations != 0 {
		t.Fatal("warm find allocated", allocations)
	}
	m.replaceFindMatch(nil, true)
	if strings.Contains(f.content(), "a") || strings.Count(f.content(), "b") != pencodeFindMatchLimit+7 {
		t.Fatal("replace all only processed displayed matches")
	}
}

func TestFindSelectionRebasesThroughReplaceAndUndo(t *testing.T) {
	f := &openFile{Path: "file.txt", Name: "file.txt", contentSnapshot: snapshotText("foo\nfoo foo\nfoo")}
	m := &workspaceModel{files: map[string]*openFile{f.Path: f}, activePath: f.Path, findOpen: true, findQuery: "foo", replaceQuery: "long", findSelection: true, findScope: pencodeFindScope{f, 4, 11}}
	m.refreshFindMatches()
	if len(m.findMatches) != 2 {
		t.Fatal("selection scope", m.findMatches)
	}
	m.replaceFindMatch(nil, true)
	if f.content() != "foo\nlong long\nfoo" || m.findScope.start != 4 || m.findScope.end != 13 {
		t.Fatalf("scope did not expand with replacement: %q %+v", f.content(), m.findScope)
	}
	m.undoActiveContent(nil)
	if f.content() != "foo\nfoo foo\nfoo" || m.findScope.end != 11 {
		t.Fatal("undo did not restore scope", m.findScope)
	}
	m.refreshFindMatches()
	if len(m.findMatches) != 2 {
		t.Fatal("undo changed scope matches")
	}
}
