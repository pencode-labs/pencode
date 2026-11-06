package workbench

import (
	"math"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/scene"
	engineui "github.com/diiyw/onix/ui"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func TestCodexQuestionCardLayoutAndInputRetention(t *testing.T) {
	for _, width := range []int{1280, 640} {
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			app, m, _, state := languageInteractionFixture(t)
			w := m.fontResources
			m.app.GetViewport = func() (int, int) { return width, 768 }
			app.World.InsertResource(scene.ViewportSize{Width: float32(width), Height: 768, ScaleFactor: 1})
			for _, camera := range app.World.Query[scene.Camera]().All {
				camera.Mut().UpdateViewport(width, 768)
			}
			css, err := os.ReadFile("../extensions/codex/resources/chat.css")
			if err != nil {
				t.Fatal(err)
			}
			process := &extensions.ActionSession{}
			show := func(long bool, value string) {
				t.Helper()
				options := `<button type="button" class="codex-question-option" data-ref="option" aria-pressed="false"><span class="codex-question-number">1</span><span class="codex-question-option-text"><span>开发新功能</span><span class="codex-question-description">实现需求</span></span></button><button type="button" class="codex-question-option"><span class="codex-question-number">2</span><span class="codex-question-option-text"><span>排查并修复问题</span></span></button><button type="button" class="codex-question-option"><span class="codex-question-number">3</span><span class="codex-question-option-text"><span>审查代码</span></span></button>`
				if long {
					options += strings.Repeat(`<button type="button" class="codex-question-option"><span class="codex-question-number">4</span><span class="codex-question-option-text"><span>更多选项</span><span class="codex-question-description">较长的选项说明需要保持可读</span></span></button>`, 12)
				}
				if err := m.applyExtensionUI("codex", process, peb.UI{ID: "composer", Kind: peb.EditorTab, View: &peb.EditorView{
					HTML: `<main class="codex-workspace" data-pencode-layout="fill"><section class="codex-chat-pane"><div class="codex-transcript"><article class="codex-chat"><p>聊天消息</p></article></div><section class="codex-composer"><form id="pencode-extension-question-card" class="codex-question-card" data-ref="question-form" aria-label="问题"><header class="codex-question-header"><span class="codex-question-symbol">?</span><span>问题</span><button type="button" data-ref="close" class="codex-icon-button codex-question-close">×</button></header><div id="pencode-extension-question-body" class="codex-question-body"><h3>你现在最希望我帮你完成哪项工作？</h3><div class="codex-question-options">` + options + `</div></div><footer class="codex-question-footer"><label class="codex-question-custom"><img alt=""><input data-ref="answer" aria-label="自定义回答" placeholder="或自行撰写回复" value="` + value + `"></label><div class="codex-question-actions"><button type="button" data-ref="skip" class="codex-question-skip">跳过</button><button id="pencode-extension-question-send" type="button" data-ref="next" class="codex-question-send" disabled>发送</button></div></footer></form><form id="pencode-extension-question-composer" data-ref="form" data-pencode-focus="prompt" class="codex-input-card"><textarea data-ref="prompt" class="codex-prompt">保留草稿</textarea><div class="codex-input-footer"><button type="button">发送</button></div></form></section></section></main>`,
					CSS:  string(css), Events: []peb.EditorBinding{{Ref: "answer", Kind: "input"}, {Ref: "option", Kind: "click"}, {Ref: "close", Kind: "click"}, {Ref: "skip", Kind: "click"}, {Ref: "next", Kind: "click"}, {Ref: "question-form", Kind: "submit"}, {Ref: "prompt", Kind: "input"}},
				}}); err != nil {
					t.Fatal(err)
				}
				markPencodeRootDirtyByModel(w, m)
				for range 3 {
					languageInteractionStep(t, app)
				}
			}
			show(false, "")
			s := m.actions.ui.editor
			card := editorContextBounds(t, m, "pencode-extension-question-card")
			composer := editorContextBounds(t, m, "pencode-extension-question-composer")
			answerID := s.prefix + "control-answer"
			answer := editorContextBounds(t, m, answerID)
			skip := editorContextBounds(t, m, s.prefix+"control-skip")
			next := editorContextBounds(t, m, "pencode-extension-question-send")
			if card.Y < 0 || card.Y+card.Height > composer.Y || math.Abs(float64(card.X-composer.X)) > 1 || math.Abs(float64(card.Width-composer.Width)) > 1 {
				t.Fatalf("question card must fit above the aligned composer: card=%+v composer=%+v", card, composer)
			}
			if answer.Width < 40 || answer.Height < 20 || answer.X+answer.Width > skip.X || skip.X+skip.Width > next.X || next.X+next.Width > card.X+card.Width || answer.Y+answer.Height > card.Y+card.Height {
				t.Fatalf("question footer overlaps or clips: card=%+v answer=%+v skip=%+v send=%+v", card, answer, skip, next)
			}
			editorContextClick(t, app, m, state, answerID)
			state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "自定义回答"})
			languageInteractionStep(t, app)
			control, _ := w.HTMLControl(m.surface, answerID)
			field := w.Mut[engineui.TextField](control)
			if field == nil || field.Text().String() != "自定义回答" {
				t.Fatal("question input did not accept typing", field)
			}
			show(true, "自定义回答")
			control, _ = w.HTMLControl(m.surface, answerID)
			field = w.Mut[engineui.TextField](control)
			if field == nil || field.Text().String() != "自定义回答" || w.FocusedControl() != control {
				t.Fatal("question update lost the answer or keyboard focus", field)
			}
			body := editorContextBounds(t, m, "pencode-extension-question-body")
			card = editorContextBounds(t, m, "pencode-extension-question-card")
			composer = editorContextBounds(t, m, "pencode-extension-question-composer")
			if body.Height > .36*768+1 || card.Y < 0 || card.Y+card.Height > composer.Y {
				t.Fatalf("long options must scroll above the composer: body=%+v card=%+v composer=%+v", body, card, composer)
			}
			prompt, _ := w.HTMLControl(m.surface, s.prefix+"control-prompt")
			if w.Mut[engineui.TextField](prompt).Text().String() != "保留草稿" {
				t.Fatal("question update lost the chat draft")
			}
		})
	}
}
