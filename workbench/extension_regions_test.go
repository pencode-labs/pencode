package workbench

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/diiyw/onix/scene"

	engineui "github.com/diiyw/onix/ui"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func TestCodexRegionsRenderContent(t *testing.T) {
	app, session := newPencodeTestApp(t)
	m := session.active.model
	css, err := os.ReadFile("testdata/codex/chat.css")
	if err != nil {
		t.Fatal(err)
	}
	process := &extensions.ActionSession{}
	for round := range 8 {
		progress := fmt.Sprintf("Progress%d", round)
		reply := string(rune('A' + round))
		history := fmt.Sprintf("History%d", round)
		if err := m.applyExtensionUI("codex", process, peb.UI{ID: "composer", Kind: peb.EditorTab, View: &peb.EditorView{
			HTML: `<main class="codex-workspace" data-pencode-layout="fill"><section class="codex-chat-pane"><div class="codex-transcript" data-ref="transcript" data-pencode-scroll="end"><article class="codex-chat"><section class="codex-message codex-user" data-ref="message/user" data-pencode-region><div class="codex-text">U</div></section><div class="codex-turn-progress" data-ref="progress/turn" data-pencode-region>` + progress + `</div><section class="codex-message" data-ref="message/agent" data-pencode-region><p>` + reply + `</p></section></article></div><section class="codex-composer"><textarea data-ref="prompt" class="codex-prompt">` + strings.Repeat("draft", round+1) + `</textarea></section></section><aside class="codex-sidebar"><section class="codex-history"><div class="codex-history-header" data-ref="history-header" data-pencode-region><h3>ProjectHistory</h3><button data-ref="new" type="button">NewChat</button></div><div class="codex-history-list" data-ref="history-list" data-pencode-region data-pencode-scroll="preserve"><button data-ref="saved" class="codex-history-row">` + history + `</button></div></section></aside></main>`,
			CSS:  string(css), Events: []peb.EditorBinding{{Ref: "prompt", Kind: "input"}, {Ref: "new", Kind: "click"}, {Ref: "saved", Kind: "click"}},
		}}); err != nil {
			t.Fatal(err)
		}
		markPencodeRootDirtyByModel(m.fontResources, m)
		for range 3 {
			languageInteractionStep(t, app)
		}
		for _, want := range []string{"U", progress, reply, "ProjectHistory", "NewChat", history} {
			found := false
			for _, text := range app.World.Query[scene.Text2D]().All {
				found = found || text.Ref().Text().String() == want
			}
			if !found {
				t.Errorf("round %d: region text %q is missing from the rendered scene", round, want)
			}
		}
	}
}

func TestExtensionHistoryRegionRetainsChatAndInput(t *testing.T) {
	app, session := newPencodeTestApp(t)
	m := session.active.model
	w := m.fontResources
	process := &extensions.ActionSession{}
	show := func(history string) {
		t.Helper()
		if err := m.applyExtensionUI("codex", process, peb.UI{ID: "composer", Kind: peb.EditorTab, View: &peb.EditorView{
			HTML:   `<main data-pencode-layout="fill"><div data-ref="transcript" data-pencode-scroll="end" data-pencode-scroll-key="task"><p>Existing conversation</p></div><textarea data-ref="prompt">draft</textarea><div data-ref="history-list" data-pencode-region data-pencode-scroll="preserve">` + history + `</div></main>`,
			CSS:    `.history-row { color: red; }`,
			Events: []peb.EditorBinding{{Ref: "prompt", Kind: "input"}, {Ref: "saved", Kind: "click"}},
		}}); err != nil {
			t.Fatal(err)
		}
		markPencodeRootDirtyByModel(w, m)
		for range 3 {
			languageInteractionStep(t, app)
		}
	}
	show(`<button data-ref="saved" class="history-row">Saved task</button>`)
	s := m.actions.ui.editor
	definition := s.definition
	transcript, ok := w.HTMLNode(m.surface, s.prefix+"control-transcript")
	if !ok {
		t.Fatal("missing transcript")
	}
	promptID := s.prefix + "control-prompt"
	control, ok := w.HTMLControl(m.surface, promptID)
	if !ok || !w.Focus(control) {
		t.Fatal("cannot focus prompt")
	}
	w.SetControlValue(control, "draft still being typed")
	field := w.Mut[engineui.TextField](control)
	field.Anchor, field.Caret = 3, 8
	show(`<button data-ref="saved" class="history-row">Updated task</button><p>More history</p>`)
	if current, ok := w.HTMLControl(m.surface, promptID); !ok || current != control || w.FocusedControl() != control {
		t.Fatal("history refresh replaced or unfocused the prompt")
	}
	field = w.Mut[engineui.TextField](control)
	if field.Text().String() != "draft still being typed" || field.Anchor != 3 || field.Caret != 8 {
		t.Fatalf("history refresh changed the draft or selection: %+v", field)
	}
	if current, ok := w.HTMLNode(m.surface, s.prefix+"control-transcript"); !ok || current != transcript || s.definition != definition {
		t.Fatal("history refresh replaced the chat")
	}
	if _, ok := w.HTMLControl(m.surface, s.prefix+"control-saved"); !ok {
		t.Fatal("updated history control is missing")
	}
	region := s.regions[0].definition
	show(`<button data-ref="saved" class="history-row">Updated task</button><p>More history</p>`)
	if s.regions[0].definition != region {
		t.Fatal("unchanged history was recompiled")
	}
	if allocs := testing.AllocsPerRun(100, func() { m.syncExtensionUI(w) }); allocs != 0 {
		t.Fatalf("unchanged regions allocated %g", allocs)
	}
}

func TestSplitExtensionRegions(t *testing.T) {
	for _, source := range []string{
		`<main><div data-ref="first" data-pencode-region><div>nested</div><textarea>&lt;/div&gt;</textarea></div><aside data-ref="second" data-pencode-region>SECOND CONTENT</aside></main>`,
		`<div data-ref="empty" data-pencode-region></div>`,
	} {
		template, regions, err := splitExtensionRegions(source)
		if err != nil || len(regions) == 0 {
			t.Fatalf("split failed: %v", err)
		}
		for _, region := range regions {
			if !strings.Contains(template, `data-ref="`+region.ref+`"`) || region.html != "" && strings.Contains(template, region.html) {
				t.Fatalf("region was not extracted: %s %+v", template, region)
			}
		}
	}
	for _, source := range []string{`<div data-pencode-region></div>`, `<div data-ref="broken" data-pencode-region>`} {
		if _, _, err := splitExtensionRegions(source); err == nil {
			t.Fatalf("accepted invalid region: %s", source)
		}
	}
}
