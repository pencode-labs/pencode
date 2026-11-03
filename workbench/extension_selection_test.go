package workbench

import (
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/scene"
	engineui "github.com/diiyw/onix/ui"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func TestExtensionTranscriptSelection(t *testing.T) {
	app, session := newPencodeTestApp(t)
	m := session.active.model
	w := m.fontResources
	m.app.GetViewport = func() (int, int) { return 1440, 900 }
	app.World.InsertResource(scene.ViewportSize{Width: 1440, Height: 900, ScaleFactor: 1})
	for _, camera := range app.World.Query[scene.Camera]().All {
		camera.Mut().UpdateViewport(1440, 900)
	}
	state, _ := app.World.GetResource[*input.State]()
	css, err := os.ReadFile("testdata/codex/chat.css")
	if err != nil {
		t.Fatal(err)
	}
	show := func(tail string) {
		t.Helper()
		if err := m.applyExtensionUI("codex", &extensions.ActionSession{}, peb.UI{ID: "composer", Kind: peb.EditorTab, View: &peb.EditorView{
			HTML: `<main class="codex-workspace" data-pencode-layout="fill"><section class="codex-chat-pane"><div class="codex-transcript" data-ref="transcript" data-pencode-selectable><article class="codex-chat"><section data-ref="message" data-pencode-region><p>你好🙂 世界</p><p>正文 <strong>加粗</strong> <button type="button" class="codex-link" data-ref="link" role="link">main.go</button></p><pre><code>echo hello
输出 中文</code></pre><p>` + tail + `</p></section></article></div><section class="codex-composer"><textarea data-ref="prompt" class="codex-prompt">keep draft</textarea></section></section></main>`,
			CSS: string(css), Events: []peb.EditorBinding{{Ref: "link", Kind: "click"}, {Ref: "prompt", Kind: "input"}},
		}}); err != nil {
			t.Fatal(err)
		}
		markPencodeRootDirtyByModel(w, m)
		for range 3 {
			languageInteractionStep(t, app)
		}
	}
	show("tail-end")
	selection := &m.actions.ui.selection
	var copied string
	state.SetClipboard(func(value string) bool { copied = value; return true }, nil)
	point := func(label string, caret int) (float64, float64) {
		t.Helper()
		for _, values := range w.Query2[scene.Text2D, scene.UIGlobalTransform]() {
			text := values.A.Ref()
			if text.Value != label {
				continue
			}
			x, y, _, height, ok := scene.TextCaretRect2D(text, text.Text(), caret)
			if !ok {
				t.Fatal("missing text geometry")
			}
			if caret > 0 {
				x -= .2
			}
			matrix := values.B.Ref().Matrix
			return float64(matrix[12] + (x+.1)*matrix[0]), float64(matrix[13] + (y+height*.5)*matrix[5])
		}
		t.Fatalf("missing text %q", label)
		return 0, 0
	}
	drag := func(from string, start int, to string, end int) {
		t.Helper()
		selection.lastClick = selection.lastClick.Add(-1e9)
		x, y := point(from, start)
		state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseLeft, X: x, Y: y})
		languageInteractionStep(t, app)
		x, y = point(to, end)
		state.Enqueue(input.Event{Kind: input.EventMouseMove, X: x, Y: y})
		languageInteractionStep(t, app)
		state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseLeft, X: x, Y: y})
		languageInteractionStep(t, app)
	}
	shortcut := func(key input.KeyCode) {
		t.Helper()
		primary := input.KeyLeftControl
		if runtime.GOOS == "darwin" {
			primary = input.KeyLeftSuper
		}
		state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: primary})
		state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: key})
		languageInteractionStep(t, app)
		state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: key})
		state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: primary})
		languageInteractionStep(t, app)
	}
	drag("好", 0, "🙂", 1)
	shortcut(input.KeyC)
	if copied != "好🙂" {
		t.Fatalf("Unicode selection copied %q", copied)
	}
	if len(selection.quads) == 0 {
		t.Fatal("selection has no highlight")
	}
	m.extensionFeedback = ""
	drag("main.go", 2, "main.go", 5)
	if m.extensionFeedback != "" {
		t.Fatal("dragging link text activated the link")
	}
	selection.lastClick = selection.lastClick.Add(-1e9)
	editorContextClick(t, app, m, state, m.actions.ui.editor.prefix+"control-link")
	for range 3 {
		languageInteractionStep(t, app)
	}
	if m.extensionFeedback == "" {
		t.Fatal("ordinary link clicks were suppressed")
	}

	for _, zoom := range []int{70, 150, 100} {
		m.setZoom(w, zoom)
		languageInteractionStep(t, app)
		drag("tail-end", 8, "你", 0)
		shortcut(input.KeyC)
		if !strings.HasPrefix(copied, "你好🙂 世界\n正文 ") || !strings.Contains(copied, "加粗") || !strings.Contains(copied, "main.go") || !strings.HasSuffix(copied, "tail-end") || strings.Contains(copied, "keep draft") {
			t.Fatalf("zoom %d: copied %q", zoom, copied)
		}
	}
	drag("你", 0, "🙂", 1)
	x, y := point("好", 0)
	state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseRight, X: x, Y: y})
	languageInteractionStep(t, app)
	if !m.inputContext.open || m.inputContext.selection != selection {
		t.Fatal("missing selection context menu")
	}
	m.runInputContextEdit(app.World, "input-context-copy")
	if copied != "你好🙂" {
		t.Fatalf("context copy: %q", copied)
	}
	languageInteractionStep(t, app)
	shortcut(input.KeyA)
	shortcut(input.KeyC)
	if !strings.Contains(copied, "输出 中文") || !strings.Contains(copied, "tail-end") {
		t.Fatalf("select all: %q", copied)
	}
	if n := testing.AllocsPerRun(100, func() { selection.sync(w, m) }); n != 0 {
		t.Fatalf("warm selection highlight allocated %g", n)
	}
	prompt, _ := w.HTMLControl(m.surface, m.actions.ui.editor.prefix+"control-prompt")
	w.Focus(prompt)
	field := w.Mut[engineui.TextField](prompt)
	field.Anchor, field.Caret = 0, 4
	shortcut(input.KeyC)
	if copied != "keep" {
		t.Fatalf("selection stole composer copy: %q", copied)
	}
	show("更新之后")
	if selection.valid(w) {
		t.Fatal("stale selection survived changed text")
	}
	longText := strings.Repeat("wrapped words ", 300) + "end-of-transcript"
	show(longText)
	node, _ := w.HTMLNode(m.surface, m.actions.ui.editor.prefix+"control-transcript")
	area, _ := node.ScrollContainer()
	before := w.Ref[scene.UIScroll](area).Position
	drag("输出", 0, "你", 0)
	if after := w.Ref[scene.UIScroll](area).Position; after != before {
		t.Fatalf("selecting transcript text scrolled the list: %v -> %v", before, after)
	}
	drag("你", 0, "🙂", 1)
	shortcut(input.KeyA)
	shortcut(input.KeyC)
	if !strings.HasSuffix(copied, longText) {
		t.Fatalf("copy lost offscreen text or inserted soft line breaks: bytes=%d content=%d spans=%d last=%q", len(copied), len(selection.content), len(selection.spans), copied[max(0, len(copied)-80):])
	}
	if n := testing.AllocsPerRun(100, func() { _ = selection.text() }); n != 0 {
		t.Fatalf("copying a selected range allocated %g", n)
	}
}
