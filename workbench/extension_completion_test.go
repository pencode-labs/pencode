package workbench

import (
	"html"
	"os"
	"testing"

	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/scene"
	engineui "github.com/diiyw/onix/ui"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func TestExtensionCompletionRetainsFocusAndCaret(t *testing.T) {
	app, session := newPencodeTestApp(t)
	m := session.active.model
	w := m.fontResources
	m.app.GetViewport = func() (int, int) { return 1024, 768 }
	app.World.InsertResource(scene.ViewportSize{Width: 1024, Height: 768, ScaleFactor: 1})
	css, err := os.ReadFile("testdata/codex/chat.css")
	if err != nil {
		t.Fatal(err)
	}
	process := &extensions.ActionSession{}
	show := func(value, caret string, open bool) {
		t.Helper()
		attributes, menu := "", ""
		if caret != "" {
			attributes += ` data-pencode-caret="` + caret + `"`
		}
		if open {
			attributes += ` data-pencode-completion="true"`
			menu = `<section id="pencode-extension-completion-menu" class="codex-completion"><div class="codex-completion-heading">技能</div><div class="codex-completion-list"><button data-ref="candidate" class="codex-completion-item" role="option" aria-selected="true">/code-review</button></div></section>`
		}
		if err := m.applyExtensionUI("codex", process, peb.UI{ID: "composer", Kind: peb.EditorTab, View: &peb.EditorView{
			HTML: `<main class="codex-workspace" data-pencode-layout="fill"><section class="codex-chat-pane"><div class="codex-transcript" data-ref="transcript" data-pencode-scroll="end" data-pencode-scroll-key="task"></div><section class="codex-composer">` + menu + `<form class="codex-input-card" data-ref="form"><textarea data-ref="prompt" data-pencode-autosize data-pencode-submit-on-enter="form" class="codex-prompt"` + attributes + `>` + html.EscapeString(value) + `</textarea></form></section></section></main>`,
			CSS:  string(css), Events: []peb.EditorBinding{{Ref: "prompt", Kind: "input"}, {Ref: "form", Kind: "submit"}},
		}}); err != nil {
			t.Fatal(err)
		}
		markPencodeRootDirtyByModel(w, m)
		for range 3 {
			languageInteractionStep(t, app)
		}
	}
	show("/re", "", false)
	s := m.actions.ui.editor
	id := s.prefix + "control-prompt"
	before := editorContextBounds(t, m, id)
	control, ok := w.HTMLControl(m.surface, id)
	if !ok || !w.Focus(control) {
		t.Fatal("cannot focus prompt")
	}
	field := w.Mut[engineui.TextField](control)
	field.Anchor, field.Caret = 1, 3
	show("/re", "", true)
	control, _ = w.HTMLControl(m.surface, id)
	field = w.Mut[engineui.TextField](control)
	if w.FocusedControl() != control || field.Anchor != 1 || field.Caret != 3 {
		t.Fatal("opening completion lost focus or selection", field)
	}
	if after := editorContextBounds(t, m, id); after != before {
		t.Fatalf("popup moved composer: before=%+v after=%+v", before, after)
	}
	menu := editorContextBounds(t, m, "pencode-extension-completion-menu")
	if menu.Width < 300 || menu.Height < 30 || menu.Y+menu.Height > before.Y || menu.Y < 0 {
		t.Fatalf("completion is not visible above prompt: menu=%+v prompt=%+v", menu, before)
	}
	// Programmatic edits must restore rune offsets, including mid-message edits.
	show("前缀 @中文.go 后缀", "10:1", false)
	control, _ = w.HTMLControl(m.surface, id)
	field = w.Mut[engineui.TextField](control)
	if w.FocusedControl() != control || field.Anchor != 10 || field.Caret != 10 {
		t.Fatal("completion did not restore rune caret", field)
	}
	field.Anchor, field.Caret = 2, 4
	show("前缀 @中文.go 后缀", "10:1", false)
	control, _ = w.HTMLControl(m.surface, id)
	field = w.Mut[engineui.TextField](control)
	if field.Anchor != 2 || field.Caret != 4 {
		t.Fatal("unchanged caret request overwrote live selection", field)
	}
	show("前缀 @中文.go 后缀", "10:2", false)
	control, _ = w.HTMLControl(m.surface, id)
	field = w.Mut[engineui.TextField](control)
	if field.Anchor != 10 || field.Caret != 10 {
		t.Fatal("new revision did not restore same caret offset", field)
	}
	show("/re", "3:3", true)
	control, _ = w.HTMLControl(m.surface, id)
	for _, key := range []input.KeyCode{input.KeyDown, input.KeyUp, input.KeyTab, input.KeyEnter, input.KeyEscape} {
		state := input.NewState()
		state.PressKey(key)
		if !s.interceptKeys(state) || w.FocusedControl() != control {
			t.Fatalf("completion key %v escaped the prompt", key)
		}
	}
	state := input.NewState()
	state.PressKey(input.KeyLeftShift)
	state.PressKey(input.KeyEnter)
	if s.interceptKeys(state) {
		t.Fatal("completion swallowed Shift+Enter")
	}
	state = input.NewState()
	state.ApplyTextComposition(input.TextComposition{Text: "ni", Active: true})
	state.PressKey(input.KeyEnter)
	if s.interceptKeys(state) {
		t.Fatal("completion swallowed IME confirmation")
	}
	if n := testing.AllocsPerRun(100, func() { m.syncExtensionUI(w) }); n != 0 {
		t.Fatalf("idle completion allocated %g", n)
	}
}

func TestExtensionCompletionCommitsQueuedText(t *testing.T) {
	app, session := newPencodeTestApp(t)
	m := session.active.model
	w := m.fontResources
	if err := m.applyExtensionUI("test", &extensions.ActionSession{}, peb.UI{ID: "test", Kind: peb.EditorTab, View: &peb.EditorView{
		HTML: `<textarea data-ref="prompt">@ma</textarea>`, Events: []peb.EditorBinding{{Ref: "prompt", Kind: "input"}},
	}}); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		languageInteractionStep(t, app)
	}
	s := m.actions.ui.editor
	control, _ := w.HTMLControl(m.surface, s.focus[0].id)
	field := w.Mut[engineui.TextField](control)
	field.Anchor, field.Caret = 3, 3
	state := input.NewState()
	state.CommitText("in")
	state.PressKey(input.KeyEnter)
	event := s.commitInput(state, control, field, "prompt")
	if event.Value != "@main" || event.Anchor != 5 || event.Focus != 5 || field.Text().String() != "@main" {
		t.Fatalf("completion lost queued text or inserted newline: %+v", event)
	}
}
