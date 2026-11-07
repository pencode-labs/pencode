package workbench

import (
	"os"
	"strings"
	"testing"

	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/module"
	"github.com/onix-engine/onix/scene"
	"github.com/onix-engine/onix/ui/html"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func TestRenderedFindHighlightsNavigationAndRefresh(t *testing.T) {
	app, m, _, keys := languageInteractionFixture(t)
	w := m.fontResources
	file := m.activeFile()
	source := file.content()
	provider := &extensions.Editor{Action: peb.Action{ID: "extensions/test/preview", Text: "Preview", Selectors: []peb.DocumentSelector{{Match: peb.FileSuffix, Pattern: ".go"}}}}
	m.customEditors = append(m.customEditors, provider)
	m.editorGeneration++
	file.editorID, file.editorGeneration = provider.ID, 0
	doc := &pencodeCustomDocument{model: m, file: file, project: m.activeProjectState(), provider: provider}
	m.customDocuments = map[*openFile]*pencodeCustomDocument{file: doc}
	previewCSS, err := os.ReadFile("../extensions/markdown-preview/resources/editor.css")
	if err != nil {
		t.Fatal(err)
	}
	show := func(body string) {
		t.Helper()
		if err := doc.setView(&peb.EditorView{
			HTML: `<article class="markdown-preview" data-ref="preview" data-pencode-searchable>` + body + `</article>`,
			CSS:  string(previewCSS) + `article { padding: 24px; line-height: 24px; } p { margin: 24px 0; } pre { white-space: pre; overflow: auto; } pre code { white-space: inherit; }`,
		}); err != nil {
			t.Fatal(err)
		}
		markPencodeRootDirtyByModel(w, m)
		for range 3 {
			languageInteractionStep(t, app)
		}
	}
	show(`<h1>中文🙂 Match</h1><p><strong>Match</strong> <em>phrase</em> &amp; <code>code</code></p><a href="hidden-address.md">Match</a><p hidden>hidden-only</p>` + strings.Repeat(`<p>filler</p>`, 50) + `<pre><code>Match at the end</code></pre>`)
	m.findQuery = "Match"
	m.runShortcut(w, pencodeShortcutFind)
	markPencodeRootDirtyByModel(w, m)
	for range 3 {
		languageInteractionStep(t, app)
	}
	if !m.findOpen || len(m.findMatches) != 4 || m.findCountLabel != "1/4" || !pencodeControlFocused(w, m.surface, "find-query") {
		t.Fatalf("preview find did not open: open=%v matches=%v count=%s", m.findOpen, m.findMatches, m.findCountLabel)
	}
	f := doc.find
	visible := func() int {
		count := 0
		for _, entity := range f.text.quads {
			if visibility, _ := w.Get[scene.Visibility](entity); visibility.Visible {
				sprite, _ := w.Get[scene.Sprite2D](entity)
				if sprite.Shape.BorderWidth > 0 {
					if sprite.Shape.Border != m.workbenchTheme.findColors[2] {
						t.Fatal("current match border did not follow the theme")
					}
					continue
				}
				count++
				if sprite.Shape.Fill != m.workbenchTheme.findColors[0] && sprite.Shape.Fill != m.workbenchTheme.findColors[1] {
					t.Fatal("find highlight did not follow the theme")
				}
			}
		}
		return count
	}
	if visible() < 3 {
		t.Fatalf("visible matches were not highlighted: spans=%+v quads=%d", f.text.spans, visible())
	}
	if allocations := testing.AllocsPerRun(100, func() { m.refreshFindMatches(); m.syncRenderedFind(w) }); allocations != 0 {
		t.Fatalf("warm rendered find allocated %g", allocations)
	}
	if allocations := testing.AllocsPerRun(100, func() { f.dirty = true; m.syncRenderedFind(w) }); allocations != 0 {
		t.Fatalf("rebinding rendered text allocated %g", allocations)
	}
	dispatch := func(ref, value string) {
		t.Helper()
		m.handleFindEvent(&module.ContentEventContext[appProps, appState]{World: w, Ref: ref, Event: html.Event{Value: value}})
		markPencodeRootDirtyByModel(w, m)
		for range 3 {
			languageInteractionStep(t, app)
		}
	}
	dispatch("find-query", "Match phrase & code")
	if len(m.findMatches) != 1 || visible() < 4 {
		t.Fatalf("cross-format match lost: matches=%v visible=%d", m.findMatches, visible())
	}
	for _, query := range []string{"hidden-address", "hidden-only", "<strong>"} {
		dispatch("find-query", query)
		if len(m.findMatches) != 0 || visible() != 0 {
			t.Fatalf("matched markup or hidden content: %q", query)
		}
	}
	dispatch("find-query", "match")
	dispatch("find-case", "")
	if len(m.findMatches) != 0 {
		t.Fatal("case-sensitive preview find matched the wrong case")
	}
	dispatch("find-case", "")
	dispatch("find-word", "")
	dispatch("find-query", "Mat")
	if len(m.findMatches) != 0 {
		t.Fatal("whole-word preview find matched part of a word")
	}
	dispatch("find-word", "")
	dispatch("find-query", "Match")
	keys.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyF3})
	keys.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyF3})
	languageInteractionStep(t, app)
	if m.findIndex != 1 {
		t.Fatal("F3 did not advance the preview match", m.findIndex)
	}
	dispatch("find-previous", "")
	dispatch("find-previous", "")
	if m.findIndex != 3 || w.Ref[scene.UIScroll](f.text.area).Position[1] >= 0 || visible() == 0 {
		t.Fatalf("previous did not wrap and reveal the last match: index=%d scroll=%+v visible=%d spans=%+v", m.findIndex, w.Ref[scene.UIScroll](f.text.area), visible(), f.text.spans[max(0, len(f.text.spans)-10):])
	}
	dispatch("find-next", "")
	if m.findIndex != 0 || w.Ref[scene.UIScroll](f.text.area).Position[1] != 0 {
		t.Fatal("next did not wrap and reveal the first match")
	}
	dispatch("find-regex", "")
	dispatch("find-query", "[")
	if m.findError == "" || len(m.findMatches) != 0 {
		t.Fatal("invalid preview regex was not reported")
	}
	dispatch("find-query", "M.tch")
	if len(m.findMatches) != 4 {
		t.Fatal("preview regex did not match")
	}
	dispatch("find-toggle-replace", "")
	m.replaceQuery = "changed"
	m.replaceFindMatch(w, true)
	if m.replaceOpen || file.content() != source {
		t.Fatal("preview search modified the source")
	}
	show(`<p>Match updated</p>`)
	if len(m.findMatches) != 1 || m.findCountLabel != "1/1" || visible() != 1 {
		t.Fatalf("document update left stale matches: %v %s visible=%d", m.findMatches, m.findCountLabel, visible())
	}
	dispatch("find-close", "")
	if visible() != 0 {
		t.Fatal("closing preview find left highlights")
	}
	// A split keeps the native editor's offsets separate from preview offsets.
	file.editorSplit = true
	show(`<p>Match</p><pre><code>code example</code></pre>`)
	w.Focus(m.editor.control)
	m.findQuery = "Match"
	m.openFind(false)
	if m.renderedFind() != nil || len(m.findMatches) != 0 {
		t.Fatal("source find used rendered offsets in split mode")
	}
	markPencodeRootDirtyByModel(w, m)
	languageInteractionStep(t, app)
	m.openFind(false)
	if m.renderedFind() != nil {
		t.Fatal("reopening the source find bar switched to the preview")
	}
	m.closeFind(w)
	f.focus(w)
	m.openFind(false)
	if m.renderedFind() != f || len(m.findMatches) != 1 {
		t.Fatal("preview focus did not choose rendered find in split mode")
	}
	dispatch("find-query", "^Match")
	if len(m.findMatches) != 1 {
		t.Fatal("preview regex did not follow split layout")
	}
	dispatch("find-query", "^")
	if len(m.findMatches) == 0 || visible() == 0 {
		t.Fatal("zero-width preview matches have no visible marker")
	}
}
