package workbench

import (
	"os"
	"testing"

	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/scene"
	engineui "github.com/onix-engine/onix/ui"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func TestCodexMessageHoverCopyAndInlineEditorFocus(t *testing.T) {
	app, m, _, state := languageInteractionFixture(t)
	w := m.fontResources
	css, err := os.ReadFile("../extensions/codex/resources/chat.css")
	if err != nil {
		t.Fatal(err)
	}
	process := &extensions.ActionSession{}
	show := func(edit bool, history string) {
		t.Helper()
		message := `<div class="codex-user-bubble"><p>任务内容</p></div><div class="codex-message-actions"><span class="codex-message-time">10:06</span><button type="button" class="codex-icon-button" data-ref="copy" data-pencode-copy="任务内容">C</button><button type="button" class="codex-icon-button codex-message-edit" data-ref="edit">E</button></div>`
		class := "codex-message codex-user"
		bindings := []peb.EditorBinding{{Ref: "prompt", Kind: "input"}, {Ref: "edit", Kind: "click"}}
		if edit {
			class += " codex-user-editing"
			message = `<form class="codex-user-bubble" data-ref="message-edit-form" data-pencode-focus="message-edit-text"><textarea class="codex-message-editor" data-ref="message-edit-text" data-pencode-autosize data-pencode-submit-on-enter="message-edit-form" data-pencode-caret="4:1">任务内容</textarea><div class="codex-message-edit-footer"><button type="button" data-ref="cancel">取消</button><button type="submit">发送</button></div></form>`
			bindings = []peb.EditorBinding{{Ref: "prompt", Kind: "input"}, {Ref: "message-edit-text", Kind: "input"}, {Ref: "message-edit-form", Kind: "submit"}, {Ref: "cancel", Kind: "click"}}
		}
		if err := m.applyExtensionUI("codex", process, peb.UI{ID: "messages", Kind: peb.EditorTab, View: &peb.EditorView{
			HTML: `<main class="codex-workspace" data-pencode-layout="fill"><section class="codex-chat-pane"><div class="codex-transcript" data-ref="transcript" data-pencode-scroll="end"><article class="codex-chat"><section id="pencode-extension-message" class="` + class + `" data-ref="message" data-pencode-region>` + message + `</section></article></div><section class="codex-composer"><textarea class="codex-prompt" data-ref="prompt">keep draft</textarea></section></section><aside class="codex-sidebar"><div data-ref="history" data-pencode-region>` + history + `</div></aside></main>`,
			CSS:  string(css), Events: bindings,
		}}); err != nil {
			t.Fatal(err)
		}
		markPencodeRootDirtyByModel(w, m)
		for range 3 {
			languageInteractionStep(t, app)
		}
	}
	show(false, "history")
	s := m.actions.ui.editor
	var copied string
	state.SetClipboard(func(value string) bool { copied = value; return true }, nil)
	bubble := editorContextBounds(t, m, "pencode-extension-message")
	state.Enqueue(input.Event{Kind: input.EventMouseMove, X: float64(bubble.X + bubble.Width/2), Y: float64(bubble.Y + bubble.Height/2)})
	for range 2 {
		languageInteractionStep(t, app)
	}
	for _, ref := range []string{"copy", "edit", "copy"} {
		id := s.prefix + "control-" + ref
		button := editorContextBounds(t, m, id)
		// Move through the space below the bubble before approaching the button.
		for _, x := range []float32{bubble.X + bubble.Width/2, button.X + button.Width/2} {
			scale := float64(m.settings.Zoom) / 100
			state.Enqueue(input.Event{Kind: input.EventMouseMove, X: float64(x) * scale, Y: float64(button.Y+button.Height/2) * scale})
			for range 3 {
				languageInteractionStep(t, app)
			}
			control, ok := w.HTMLControl(m.surface, id)
			target := w.Ref[scene.InputTarget2D](control)
			if !ok || target == nil || target.PassThrough {
				t.Fatalf("message action %s disappeared while approaching it: message=%+v button=%+v x=%v", ref, bubble, button, x)
			}
		}
	}
	editorContextClick(t, app, m, state, s.prefix+"control-copy")
	if copied != "任务内容" {
		t.Fatal("hover toolbar copy was not clickable", copied)
	}
	show(true, "history")
	s = m.actions.ui.editor
	id := s.prefix + "control-message-edit-text"
	control, ok := w.HTMLControl(m.surface, id)
	field := w.Mut[engineui.TextField](control)
	if !ok || field == nil || w.FocusedControl() != control || field.Caret != 4 {
		t.Fatal("opening inline editor did not focus the message text", field)
	}
	before := editorContextBounds(t, m, id)
	state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "\n新增一行\n再一行"})
	languageInteractionStep(t, app)
	if after := editorContextBounds(t, m, id); after.Height <= before.Height {
		t.Fatal("inline editor did not grow with its content", before, after)
	}
	value := field.Value
	show(true, "changed history")
	control, _ = w.HTMLControl(m.surface, id)
	field = w.Mut[engineui.TextField](control)
	if w.FocusedControl() != control || field.Value != value {
		t.Fatal("history update replaced the inline draft or focus", field)
	}
	enter := input.NewState()
	enter.PressKey(input.KeyEnter)
	if !s.interceptKeys(enter) {
		t.Fatal("inline editor did not route Enter to its own form")
	}
}
