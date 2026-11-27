package workbench

import (
	"runtime"
	"strings"
	"testing"

	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/scene"
	engineui "github.com/onix-engine/onix/ui"
)

func TestFindAcrossDiffPanes(t *testing.T) {
	app, m, _, state := languageInteractionFixture(t)
	w, s := m.fontResources, m.sourceState()
	p := m.activeProjectState()
	// A comparison opened from Source Control may be the only editor.
	p.ActivePath = ""
	const query = "目标"
	context := strings.Repeat("context\n", 200)
	tab := &sourceTab{Key: "changes", Kind: "changes", Path: "diff.txt", Local: "🙂" + query + "\n" + context, Result: context + query + "\n"}
	tab.Saved = tab.Result
	view, err := sourceDiffContext(t.Context(), m.backend, tab.Local, tab.Result)
	if err != nil {
		t.Fatal(err)
	}
	tab.initializeDiffView(view)
	s.Snapshot.Repository.Root = m.projectPath
	s.Tabs, s.Active, s.RestoreScroll = []*sourceTab{tab}, tab.Key, true
	m.sourceChanged()
	for range 3 {
		languageInteractionStep(t, app)
	}
	chord := func(keys ...input.KeyCode) {
		t.Helper()
		for _, key := range keys {
			state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: key})
		}
		languageInteractionStep(t, app)
		for _, key := range keys {
			state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: key})
		}
		languageInteractionStep(t, app)
	}
	primary := input.KeyLeftControl
	if runtime.GOOS == "darwin" {
		primary = input.KeyLeftSuper
	}
	w.Focus(s.diffEditors[0].control)
	field := w.Mut[engineui.TextField](s.diffEditors[0].control)
	field.Anchor, field.Caret = 1, 3
	chord(primary, input.KeyF)
	if !m.findOpen || m.findQuery != query || m.findCountLabel != "1/2" || !pencodeControlFocused(w, m.surface, "find-query") {
		t.Fatalf("diff find shortcut: open=%t query=%q count=%q", m.findOpen, m.findQuery, m.findCountLabel)
	}
	assertCurrent := func(side int, label string) {
		t.Helper()
		field := w.Ref[engineui.TextField](s.diffEditors[side].control)
		start, end := pencodeSelectionBytes(tab.sourceText(side), field.Anchor, field.Caret)
		if m.findCountLabel != label || tab.sourceText(side)[start:end] != query {
			t.Fatalf("pane %d selection %q, count %q", side, tab.sourceText(side)[start:end], m.findCountLabel)
		}
		for _, entity := range m.findDiff.paint[side].quads {
			visible, _ := w.Get[scene.Visibility](entity)
			sprite, _ := w.Get[scene.Sprite2D](entity)
			if visible.Visible && sprite.Shape.BorderWidth > 0 {
				return
			}
		}
		t.Fatalf("pane %d has no visible current-match outline", side)
	}
	assertCurrent(0, "1/2")
	chord(input.KeyEnter)
	assertCurrent(1, "2/2")
	if tab.FirstRow == 0 || tab.scrollApplied[0][1] != tab.scrollApplied[1][1] {
		t.Fatal("search did not reveal the offscreen match in the aligned viewports")
	}
	chord(input.KeyEnter)
	assertCurrent(0, "1/2")
	chord(input.KeyLeftShift, input.KeyEnter)
	assertCurrent(1, "2/2")
	if n := testing.AllocsPerRun(100, func() { m.syncDiffFind(w) }); n != 0 {
		t.Fatalf("warm diff find allocated %g times", n)
	}
	// Reopen the bar to select its query and type a term present only on the original side.
	chord(primary, input.KeyF)
	state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "🙂"})
	languageInteractionStep(t, app)
	if m.findCountLabel != "1/1" || s.languageSide != 0 {
		t.Fatalf("typing in diff find: query=%q count=%q side=%d", m.findQuery, m.findCountLabel, s.languageSide)
	}
	chord(input.KeyEscape)
	if m.findOpen || m.findDiff != nil || !pencodeControlFocused(w, m.surface, diffInputs[0]) || tab.Result != tab.Saved {
		t.Fatalf("closed: open=%t cached=%t side=%d focus=%d left=%d dirty=%t", m.findOpen, m.findDiff != nil, s.languageSide, w.FocusedControl(), s.diffEditors[0].control, tab.Result != tab.Saved)
	}
}
